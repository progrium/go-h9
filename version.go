package h9

import "github.com/progrium/go-libh9/internal/pin"

// LibVersion is the hyper9 release whose libh9 this package is pinned to,
// which the fetch command fetches, or "" before one is pinned.
const LibVersion = pin.Version
