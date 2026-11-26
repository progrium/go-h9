package h9_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progrium/go-libh9"
)

// The tests need libh9: $H9_LIBRARY, or one the fetch command fetched. With
// $H9_TEST_ROOT, a 9front root tree, they also run rc in it.

func TestMain(m *testing.M) {
	if _, err := h9.Version(); err != nil {
		fmt.Fprintln(os.Stderr, "skipping go-libh9's tests:", err)
		if os.Getenv("H9_TEST_REQUIRE") != "" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// hello is tests/fixtures/hello.zig from hyper9: it writes "hello\n" and
// exits(nil).
func hello(t testing.TB, arch string) []byte {
	b, err := os.ReadFile(filepath.Join("testdata", "hello-"+arch))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hostArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}

func newMachine(t testing.TB, flags ...string) *h9.Machine {
	t.Helper()
	m, err := h9.NewMachine(flags...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func TestVersion(t *testing.T) {
	v, err := h9.Version()
	if err != nil || v == "" {
		t.Fatalf("Version() = %q, %v", v, err)
	}
	t.Logf("libh9 %s", v)
}

func TestHello(t *testing.T) {
	m := newMachine(t)
	t.Logf("engine %s, arch %s", m.Engine(), m.Arch())
	if m.Arch() != hostArch() {
		t.Errorf("Arch() = %q, want %q", m.Arch(), hostArch())
	}
	cmd := m.Command("hello")
	cmd.Program = hello(t, m.Arch())
	out, err := cmd.Output(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello\n" {
		t.Errorf("output %q, want %q", out, "hello\n")
	}

	// A machine runs any number of programs, one after another.
	for i := 0; i < 3; i++ {
		cmd := m.Command("hello")
		cmd.Program = hello(t, m.Arch())
		out, err := cmd.CombinedOutput(context.Background())
		if err != nil || string(out) != "hello\n" {
			t.Fatalf("run %d: %q, %v", i, out, err)
		}
	}
}

func TestEmulator386(t *testing.T) {
	m := newMachine(t, "--engine", "hcpu", "--arch", "386")
	if m.Engine() != "hcpu" || m.Arch() != "386" {
		t.Fatalf("engine %s, arch %s", m.Engine(), m.Arch())
	}
	cmd := m.Command("hello")
	cmd.Program = hello(t, "386")
	out, err := cmd.Output(context.Background())
	if err != nil || string(out) != "hello\n" {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestStdoutFile(t *testing.T) {
	m := newMachine(t)
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cmd := m.Command("hello")
	cmd.Program = hello(t, m.Arch())
	cmd.Stdout = f
	if err := cmd.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f.Name())
	if string(b) != "hello\n" {
		t.Errorf("file has %q", b)
	}
}

func TestWrongArch(t *testing.T) {
	m := newMachine(t)
	other := "arm64"
	if m.Arch() == "arm64" {
		other = "amd64"
	}
	cmd := m.Command("hello")
	cmd.Program = hello(t, other)
	err := cmd.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--arch") {
		t.Fatalf("err = %v, want one naming --arch", err)
	}
}

func TestNotFound(t *testing.T) {
	m := newMachine(t)
	err := m.Command("nosuchprogram").Run(context.Background())
	var ee *h9.ExitError
	if err == nil || errors.As(err, &ee) {
		t.Fatalf("err = %v, want a failure to run", err)
	}
}

func TestBadFlags(t *testing.T) {
	for _, flags := range [][]string{
		{"--nosuchflag"},
		{"--net", "sideways"},
		{"--dir", "/no/such/dir"},
		{"stray"},
		{"--rc", "echo"},
		{"--gfx"},
	} {
		if m, err := h9.NewMachine(flags...); err == nil {
			m.Close()
			t.Errorf("NewMachine(%q) succeeded", flags)
		} else {
			t.Logf("NewMachine(%q): %v", flags, err)
		}
	}
}

func TestClosed(t *testing.T) {
	m, err := h9.NewMachine()
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	m.Close()
	cmd := m.Command("hello")
	cmd.Program = hello(t, m.Arch())
	if err := cmd.Run(context.Background()); err == nil {
		t.Fatal("ran on a closed machine")
	}
}

// Machines run at once on their own goroutines, while the Go runtime's
// GC and preemption signals keep going.
func TestConcurrent(t *testing.T) {
	stop := make(chan struct{})
	var churn sync.WaitGroup
	churn.Add(1)
	go func() {
		defer churn.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = make([]byte, 1<<16)
				runtime.Gosched()
			}
		}
	}()
	defer func() {
		close(stop)
		churn.Wait()
	}()

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The emulator, since some hypervisors allow one VM per process.
			m, err := h9.NewMachine("--engine", "hcpu")
			if err != nil {
				errs <- err
				return
			}
			defer m.Close()
			for j := 0; j < 3; j++ {
				var out bytes.Buffer
				cmd := m.Command("hello")
				cmd.Program = hello(t, m.Arch())
				cmd.Stdout = &out
				if err := cmd.Run(context.Background()); err != nil || out.String() != "hello\n" {
					errs <- fmt.Errorf("machine %d run %d: %q, %v", i, j, out.String(), err)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func rootMachine(t *testing.T, flags ...string) *h9.Machine {
	root := os.Getenv("H9_TEST_ROOT")
	if root == "" {
		t.Skip("no $H9_TEST_ROOT")
	}
	return newMachine(t, append([]string{"--dir", root}, flags...)...)
}

func TestRc(t *testing.T) {
	m := rootMachine(t, "--env", "greeting=hi")
	out, err := m.Command("rc", "-c", "echo $greeting from $sysname").Output(context.Background())
	if err != nil || string(out) != "hi from h9\n" {
		t.Fatalf("%q, %v", out, err)
	}

	err = m.Command("rc", "-c", "exit oops").Run(context.Background())
	var ee *h9.ExitError
	if !errors.As(err, &ee) || ee.Killed || !strings.Contains(ee.Status, "oops") {
		t.Fatalf("err = %#v, want an exit with oops", err)
	}
}

func TestStdin(t *testing.T) {
	m := rootMachine(t)
	cmd := m.Command("cat")
	cmd.Stdin = strings.NewReader("one\ntwo\n")
	out, err := cmd.Output(context.Background())
	if err != nil || string(out) != "one\ntwo\n" {
		t.Fatalf("%q, %v", out, err)
	}
}

func TestContext(t *testing.T) {
	m := rootMachine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	// cat waits on a stdin that never ends, so only the context ends it.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer r.Close()
	cmd := m.Command("cat")
	cmd.Stdin = r
	err = cmd.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v to stop", d)
	}
	// The machine runs again afterward.
	out, err := m.Command("rc", "-c", "echo again").Output(context.Background())
	if err != nil || string(out) != "again\n" {
		t.Fatalf("%q, %v", out, err)
	}
}
