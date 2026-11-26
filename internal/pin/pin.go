// Package pin is the libh9 release go-h9 is pinned to, and its
// checksums, for cmd/h9lib and the library search.
package pin

import (
	_ "embed"
	"strings"
)

// Version is the hyper9 release tag whose libh9 archives cmd/h9lib
// fetches, or "" before one is pinned. scripts/pin.sh sets it, with
// SHA256SUMS.
const Version = ""

//go:embed SHA256SUMS
var sums string

// Sum is the SHA-256 of a release asset, in hex, or "" if it isn't
// pinned.
func Sum(asset string) string {
	for _, l := range strings.Split(sums, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			return f[0]
		}
	}
	return ""
}
