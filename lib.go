package h9

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

// abiVersion is the H9_ABI_VERSION this package was written against.
const abiVersion = 1

// libh9's functions, as h9.h declares them.
var lib struct {
	abiVersion    func() int32
	version       func() string
	machineNew    func(flags **byte, n uintptr, errbuf *byte, errcap uintptr) uintptr
	machineFree   func(m uintptr)
	machineEngine func(m uintptr) string
	machineArch   func(m uintptr) string
	machineRun    func(m uintptr, argv **byte, argc uintptr, aout *byte, aoutlen uintptr, in, out, err uintptr, status *byte, statuscap uintptr) int32
	machineSignal func(m uintptr, sig int32)
}

var (
	loadOnce sync.Once
	loadErr  error
	loadPath string
)

// Load loads libh9 from path. Call it before anything else in the
// package to choose the library; otherwise the first call that needs it
// looks for one (see [LibraryPath]). It fails once a library is loaded.
func Load(path string) error {
	loaded := false
	loadOnce.Do(func() {
		loaded = true
		loadErr = load(path)
	})
	if !loaded {
		if loadErr != nil {
			return loadErr
		}
		return fmt.Errorf("h9: libh9 is already loaded from %s", loadPath)
	}
	return loadErr
}

func ensure() error {
	loadOnce.Do(func() {
		path, err := LibraryPath()
		if err != nil {
			loadErr = err
			return
		}
		loadErr = load(path)
	})
	return loadErr
}

// LibraryFile is libh9's file name on this platform.
func LibraryFile() string {
	switch runtime.GOOS {
	case "windows":
		return "h9.dll"
	case "darwin":
		return "libh9.dylib"
	default:
		return "libh9.so"
	}
}

// LibraryPath is where the package loads libh9 from when [Load] isn't
// called: the first of $H9_LIBRARY, [LibraryFile] next to the
// executable, and [LibraryFile] in the cache directory cmd/h9lib fetches
// it into, that exists.
func LibraryPath() (string, error) {
	if p := os.Getenv("H9_LIBRARY"); p != "" {
		return p, nil
	}
	var tried []string
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), LibraryFile())
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		tried = append(tried, p)
	}
	if dir, err := CacheDir(); err == nil {
		p := filepath.Join(dir, LibraryFile())
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		tried = append(tried, p)
	}
	return "", fmt.Errorf("h9: libh9 not found (tried %q); fetch it with `go run github.com/progrium/go-h9/cmd/h9lib`, or set H9_LIBRARY", tried)
}

// CacheDir is where cmd/h9lib puts the libh9 this package is pinned to
// ([LibVersion]) for this platform.
func CacheDir() (string, error) {
	if LibVersion == "" {
		return "", errors.New("h9: no libh9 release is pinned")
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "go-h9", LibVersion, runtime.GOOS+"-"+runtime.GOARCH), nil
}

func load(path string) error {
	h, err := dlopen(path)
	if err != nil {
		return fmt.Errorf("h9: loading libh9: %w", err)
	}
	purego.RegisterLibFunc(&lib.abiVersion, h, "h9_abi_version")
	if v := lib.abiVersion(); v != abiVersion {
		return fmt.Errorf("h9: %s has ABI version %d; this package needs %d", path, v, abiVersion)
	}
	purego.RegisterLibFunc(&lib.version, h, "h9_version")
	purego.RegisterLibFunc(&lib.machineNew, h, "h9_machine_new")
	purego.RegisterLibFunc(&lib.machineFree, h, "h9_machine_free")
	purego.RegisterLibFunc(&lib.machineEngine, h, "h9_machine_engine")
	purego.RegisterLibFunc(&lib.machineArch, h, "h9_machine_arch")
	purego.RegisterLibFunc(&lib.machineRun, h, "h9_machine_run")
	purego.RegisterLibFunc(&lib.machineSignal, h, "h9_machine_signal")
	loadPath = path
	return nil
}

// Version is the loaded libh9's h9 version, as `h9 --version` prints it.
func Version() (string, error) {
	if err := ensure(); err != nil {
		return "", err
	}
	return lib.version(), nil
}
