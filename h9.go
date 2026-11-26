// Package h9 runs 9front and Linux programs in h9 machines, from Go.
//
// It wraps libh9, h9's C ABI, which it loads at run time with purego, so
// it builds without cgo and cross-compiles like any Go package. The
// library itself comes from a hyper9 release (see cmd/h9lib) or a
// hyper9 checkout (`zig build libh9`); see [LibraryPath] for where it's
// looked for.
//
// A machine is configured with the h9 command's own flags, as h9's
// manual describes them:
//
//	m, err := h9.NewMachine("--dir", "/path/to/9front/root", "--net", "disable")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer m.Close()
//	out, err := m.Command("rc", "-c", "echo hello from $sysname").Output(ctx)
//
// One machine runs one program at a time, each run with a fresh
// namespace and process tree. Make several machines to run programs at
// once.
package h9

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
)

// noFD is h9.h's H9_NOFD.
const noFD = ^uintptr(0)

// A Machine is one h9 machine: a VM, a namespace, a console, and a
// process tree, which runs programs one at a time.
type Machine struct {
	mu     sync.Mutex // held by a run, and by Close
	ptr    uintptr
	engine string
	arch   string
}

// NewMachine makes a machine from h9 command flags, each a separate
// argument: "--dir", "/root", not "--dir /root". Program-running flags
// (--rc, --print, --check) are the command's, and --gfx, --serve,
// --trace, and --cred aren't in libh9 yet. Unlike the h9 command, the
// library reads no environment: with no mount on /, the root is h9's
// built-in one, not $H9ROOT.
func NewMachine(flags ...string) (*Machine, error) {
	if err := ensure(); err != nil {
		return nil, err
	}
	cflags, keep := cstrings(flags)
	errbuf := make([]byte, 512)
	p := lib.machineNew(cflags, uintptr(len(flags)), &errbuf[0], uintptr(len(errbuf)))
	runtime.KeepAlive(keep)
	if p == 0 {
		return nil, fmt.Errorf("h9: %s", cstring(errbuf))
	}
	return &Machine{ptr: p, engine: lib.machineEngine(p), arch: lib.machineArch(p)}, nil
}

// Engine is what the machine runs on: "hvf", "kvm", "whp", "hcpu" (h9's
// own emulator), or "sud".
func (m *Machine) Engine() string { return m.engine }

// Arch is the architecture of the programs it runs: "arm64", "amd64", or
// "386".
func (m *Machine) Arch() string { return m.arch }

// Close stops a run in progress and frees the machine.
func (m *Machine) Close() error {
	m.Signal(Stop)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ptr != 0 {
		lib.machineFree(m.ptr)
		m.ptr = 0
	}
	return nil
}

// A Signal is a note for a running machine.
type Signal int32

const (
	// Interrupt posts `interrupt` to the foreground processes, as Ctrl-C.
	Interrupt Signal = 0
	// Hangup posts `hangup` to every process, and tears the machine
	// down two seconds later.
	Hangup Signal = 1
	// Stop tears the machine down now.
	Stop Signal = 2
)

// Signal delivers s to the program running in m, from any goroutine. It
// does nothing when nothing runs.
func (m *Machine) Signal(s Signal) {
	if p := m.ptr; p != 0 {
		lib.machineSignal(p, int32(s))
	}
}

// Cmd is a program to run in a machine, as [os/exec.Cmd] is for the host.
type Cmd struct {
	// Args is the program's arguments, with Args[0] the name it sees for
	// itself. Without Program, Args[0] is looked up in the machine's
	// namespace as the h9 command looks it up.
	Args []string

	// Program, if set, is the program to run: the bytes of a 9front
	// a.out or a Linux ELF for the machine's architecture.
	Program []byte

	// Stdin is the console's input; nil reads end of file. Stdout and
	// Stderr get what's written to file descriptors 1 and 2; nil drops it.
	// An *os.File is used directly; anything else is copied through a pipe,
	// and Run returns once the copies to Stdout and Stderr are done.
	Stdin          io.Reader
	Stdout, Stderr io.Writer

	m *Machine
}

// Command returns a Cmd that runs args in m.
func (m *Machine) Command(args ...string) *Cmd {
	return &Cmd{Args: args, m: m}
}

// ExitError is a program that ended without success.
type ExitError struct {
	// Status is the exits string, or with Killed, the note that killed
	// the program, such as "sys: trap: fault read addr=0x0" or
	// "h9: timeout".
	Status string
	Killed bool
}

func (e *ExitError) Error() string {
	if e.Killed {
		return "h9: killed: " + e.Status
	}
	return "h9: exited: " + e.Status
}

// Run runs the command and waits for its first process to exit. It
// returns nil for an empty exits string, which is success in 9front, an
// *ExitError for any other end, and another error if the program
// couldn't run. When ctx is done before the program is, the machine is
// stopped and Run returns ctx.Err().
func (c *Cmd) Run(ctx context.Context) error {
	if len(c.Args) == 0 {
		return errors.New("h9: Cmd has no Args")
	}
	m := c.m
	if m == nil {
		return errors.New("h9: Cmd wasn't made by Machine.Command")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ptr == 0 {
		return errors.New("h9: the machine is closed")
	}

	var st stdio
	defer st.close()
	in, err := st.reader(c.Stdin)
	if err != nil {
		return err
	}
	// The same Writer for both gets one pipe, as with os/exec, so its
	// writes stay in order and never overlap.
	out, err := st.writer(c.Stdout)
	if err != nil {
		return err
	}
	errfd, err := st.writer(c.Stderr)
	if err != nil {
		return err
	}

	argv, keep := cstrings(c.Args)
	var aout *byte
	if len(c.Program) > 0 {
		aout = &c.Program[0]
	}
	status := make([]byte, 512)

	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			lib.machineSignal(m.ptr, int32(Stop))
		case <-done:
		}
	}()
	rc := lib.machineRun(m.ptr, argv, uintptr(len(c.Args)), aout, uintptr(len(c.Program)), in, out, errfd, &status[0], uintptr(len(status)))
	close(done)
	<-stopped
	runtime.KeepAlive(keep)
	runtime.KeepAlive(c.Program)
	st.wait()

	msg := cstring(status)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case rc == 0 && msg == "":
		return nil
	case rc == 0:
		return &ExitError{Status: msg}
	case rc == 1:
		return &ExitError{Status: msg, Killed: true}
	default:
		return fmt.Errorf("h9: %s: %s", c.Args[0], msg)
	}
}

// Output runs the command and returns what it wrote to Stdout.
func (c *Cmd) Output(ctx context.Context) ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("h9: Stdout already set")
	}
	var b bytes.Buffer
	c.Stdout = &b
	err := c.Run(ctx)
	return b.Bytes(), err
}

// CombinedOutput runs the command and returns what it wrote to Stdout and
// Stderr together.
func (c *Cmd) CombinedOutput(ctx context.Context) ([]byte, error) {
	if c.Stdout != nil || c.Stderr != nil {
		return nil, errors.New("h9: Stdout or Stderr already set")
	}
	var b bytes.Buffer
	c.Stdout = &b
	c.Stderr = &b
	err := c.Run(ctx)
	return b.Bytes(), err
}

// stdio is a run's console: the host descriptors it passes libh9, and
// the pipes and copies behind them.
type stdio struct {
	closeAfter []*os.File // closed once the run returns
	closeEnd   []*os.File // closed once the copies are done
	copies     sync.WaitGroup
	shared     map[io.Writer]uintptr
}

func (s *stdio) reader(r io.Reader) (uintptr, error) {
	if r == nil {
		return noFD, nil
	}
	if f, ok := r.(*os.File); ok {
		return f.Fd(), nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	s.closeAfter = append(s.closeAfter, pr)
	// Not waited for: r may block, as with os/exec. Once the run is
	// over, its next write to the pipe fails and it ends.
	go func() {
		io.Copy(pw, r)
		pw.Close()
	}()
	return pr.Fd(), nil
}

func (s *stdio) writer(w io.Writer) (uintptr, error) {
	if w == nil {
		return noFD, nil
	}
	if f, ok := w.(*os.File); ok {
		return f.Fd(), nil
	}
	if fd, ok := s.shared[w]; ok {
		return fd, nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	s.closeAfter = append(s.closeAfter, pw)
	s.closeEnd = append(s.closeEnd, pr)
	s.copies.Add(1)
	go func() {
		defer s.copies.Done()
		io.Copy(w, pr)
	}()
	if s.shared == nil {
		s.shared = map[io.Writer]uintptr{}
	}
	s.shared[w] = pw.Fd()
	return pw.Fd(), nil
}

// wait closes the run's ends of the pipes and waits for the copies.
func (s *stdio) wait() {
	for _, f := range s.closeAfter {
		f.Close()
	}
	s.closeAfter = nil
	s.copies.Wait()
}

func (s *stdio) close() {
	s.wait()
	for _, f := range s.closeEnd {
		f.Close()
	}
	s.closeEnd = nil
}

// cargv is a C argv and the strings it points to, which must stay alive
// for the call that takes it.
type cargv struct {
	ptrs []*byte
	strs [][]byte
}

// cstrings makes a C argv from args.
func cstrings(args []string) (**byte, *cargv) {
	if len(args) == 0 {
		return nil, nil
	}
	c := &cargv{ptrs: make([]*byte, len(args)), strs: make([][]byte, len(args))}
	for i, a := range args {
		c.strs[i] = append([]byte(a), 0)
		c.ptrs[i] = &c.strs[i][0]
	}
	return &c.ptrs[0], c
}

func cstring(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
