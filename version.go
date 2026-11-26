package h9

import "github.com/progrium/go-h9/internal/pin"

// LibVersion is the hyper9 release whose libh9 this package is pinned to,
// which cmd/h9lib fetches, or "" before one is pinned.
const LibVersion = pin.Version
