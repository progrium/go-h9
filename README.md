# go-h9

Go bindings for [h9](https://github.com/progrium/hyper9): run 9front and
Linux programs in isolated h9 machines from a Go program.

```go
import "github.com/progrium/go-h9"

m, err := h9.NewMachine("--dir", "/path/to/9front/root", "--net", "disable", "--timeout", "30s")
if err != nil {
	log.Fatal(err)
}
defer m.Close()

out, err := m.Command("rc", "-c", "echo hello from $sysname").Output(ctx)
// out is "hello from h9\n"
```

The package wraps libh9, h9's C ABI ([h9.h][h9h]), and loads it at run
time with [purego](https://github.com/ebitengine/purego), so it needs no
cgo: `CGO_ENABLED=0` builds work, and cross-compiling works as it does
for any Go package.

## The API

- `h9.NewMachine(flags...)` makes a machine from the `h9` command's own
  flags, as h9's manual describes them, each a separate argument:
  `"--dir", "/root"`, `"--env", "NAME=value"`, `"--memory", "512M"`,
  `"--engine", "hcpu"`, and so on. The library reads no environment, so
  with no mount on `/` the root is h9's built-in one, never `$H9ROOT`.
- `m.Command(args...)` returns a `*h9.Cmd`, which works like
  `os/exec.Cmd`: set `Stdin`, `Stdout`, and `Stderr`, or `Program` to the
  bytes of an a.out or ELF from the host, then call `Run(ctx)`,
  `Output(ctx)`, or `CombinedOutput(ctx)`. A program that exits with a
  non-empty exits string, or is killed, returns an `*h9.ExitError`.
  Cancelling `ctx` stops the machine.
- `m.Signal(h9.Interrupt)`, `h9.Hangup`, or `h9.Stop` delivers a note to
  the running program from any goroutine.
- `m.Engine()` and `m.Arch()` say what the machine runs on, and
  `m.Close()` frees it.

One machine runs one program at a time, each run with a fresh namespace
and process tree. Make more machines to run programs at once.

## Getting libh9

libh9 is a shared library built by h9's Zig build, which `go get` can't
run, so it comes separately. The package looks for it, in order:

1. at the path given to `h9.Load`, if called first;
2. at `$H9_LIBRARY`;
3. next to the program's executable (`libh9.so`, `libh9.dylib`, or
   `h9.dll`);
4. in the user's cache directory, where `cmd/h9lib` puts it.

`cmd/h9lib` downloads the libh9 archive for this platform from the hyper9
release this module is pinned to, and checks it against the checksum
committed here in `internal/pin`:

```sh
go run github.com/progrium/go-h9/cmd/h9lib            # into the cache
go run github.com/progrium/go-h9/cmd/h9lib -o ./dist   # next to a program you ship
```

Releases have libh9 for Linux on amd64 and arm64 (against glibc), macOS
on arm64, and Windows on amd64. Until go-h9 is pinned to one, or to use
an unreleased h9, build it from a hyper9 checkout:

```sh
zig build libh9 -Doptimize=ReleaseSafe
export H9_LIBRARY=$PWD/zig-out/libh9/libh9.so
```

`scripts/pin.sh TAG` pins the module to a hyper9 release.

## Platform notes

- **macOS.** HVF needs the program to be signed with the hypervisor
  entitlement (hyper9's `h9.entitlements`), as the `h9` command is.
  Without it, machines run on h9's emulator, hcpu. Hypervisor.framework
  allows one VM per process, so run other machines with
  `--engine hcpu`.
- **Linux.** KVM needs access to `/dev/kvm`; without it, machines run on
  hcpu. h9 uses `SIGRTMIN+1` to kick KVM vCPUs, so leave that signal to
  it.
- **Windows.** WHP needs the Windows Hypervisor Platform feature;
  without it, machines run on hcpu.

## Tests

```sh
H9_LIBRARY=/path/to/libh9.so go test ./...
```

The tests skip when no libh9 is found, unless `H9_TEST_REQUIRE` is set.
With `H9_TEST_ROOT` set to a 9front root tree, they also run `rc` and
`cat` from it. CI builds libh9 from hyper9 on Linux (amd64 and arm64),
macOS, and Windows, and runs the tests on each.

[h9h]: https://github.com/progrium/hyper9/blob/main/src/capi/h9.h
