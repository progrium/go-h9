// Command fetch fetches libh9 from the hyper9 release go-libh9 is pinned
// to, checks it against the pinned checksum, and puts it where the h9
// package looks for it:
//
//	go run github.com/progrium/go-libh9/fetch
//
// With -o DIR it writes the library to DIR instead, such as next to a
// program you ship. -os and -arch fetch another platform's.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/progrium/go-libh9/internal/pin"
)

// targets maps GOOS/GOARCH to the Zig target hyper9's release names
// libh9's archives by.
var targets = map[string]string{
	"linux/amd64":   "x86_64-linux-gnu",
	"linux/arm64":   "aarch64-linux-gnu",
	"darwin/arm64":  "aarch64-macos",
	"windows/amd64": "x86_64-windows-gnu",
}

func main() {
	goos := flag.String("os", runtime.GOOS, "the platform's GOOS")
	goarch := flag.String("arch", runtime.GOARCH, "the platform's GOARCH")
	out := flag.String("o", "", "directory to write the library to (default: the h9 package's cache directory)")
	flag.Parse()
	if err := run(*goos, *goarch, *out); err != nil {
		fmt.Fprintln(os.Stderr, "fetch:", err)
		os.Exit(1)
	}
}

func run(goos, goarch, out string) error {
	if pin.Version == "" {
		return fmt.Errorf("go-libh9 isn't pinned to a hyper9 release with libh9 yet; build it from a hyper9 checkout with `zig build libh9 -Doptimize=ReleaseSafe` and set H9_LIBRARY to zig-out/libh9/%s", libFile(goos))
	}
	target, ok := targets[goos+"/"+goarch]
	if !ok {
		return fmt.Errorf("no libh9 for %s/%s", goos, goarch)
	}
	if out == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		out = filepath.Join(dir, "go-libh9", pin.Version, goos+"-"+goarch)
	}

	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	name := "libh9-" + pin.Version + "-" + target
	asset := name + ext
	want := pin.Sum(asset)
	if want == "" {
		return fmt.Errorf("no checksum pinned for %s", asset)
	}
	url := "https://github.com/progrium/hyper9/releases/download/" + pin.Version + "/" + asset
	fmt.Fprintln(os.Stderr, "fetching", url)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("%s: checksum %s, want %s", asset, got, want)
	}

	lib, err := extract(data, ext, path.Join(name, libFile(goos)))
	if err != nil {
		return fmt.Errorf("%s: %w", asset, err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(out, libFile(goos))
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, lib, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	fmt.Println(dst)
	return nil
}

func libFile(goos string) string {
	switch goos {
	case "windows":
		return "h9.dll"
	case "darwin":
		return "libh9.dylib"
	default:
		return "libh9.so"
	}
}

// extract returns the file at member in a .tar.gz or .zip archive.
func extract(data []byte, ext, member string) ([]byte, error) {
	if ext == ".zip" {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == member {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("no %s in it", member)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no %s in it", member)
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimPrefix(h.Name, "./") == member {
			return io.ReadAll(tr)
		}
	}
}
