#!/usr/bin/env bash
# Pins go-h9 to a hyper9 release's libh9: sets internal/pin.Version and
# copies the release's libh9 checksums into internal/pin/SHA256SUMS.
#
#   scripts/pin.sh TAG
set -euo pipefail
tag=${1:?usage: scripts/pin.sh TAG}
cd "$(dirname "$0")/.."
sums=$(curl -fsSL "https://github.com/progrium/hyper9/releases/download/$tag/SHA256SUMS")
libs=$(printf '%s\n' "$sums" | grep " \*\?libh9-$tag-" || true)
[ -n "$libs" ] || { echo "pin: release $tag has no libh9 archives" >&2; exit 1; }
printf '%s\n' "$libs" >internal/pin/SHA256SUMS
sed -i.bak "s/^const Version = .*/const Version = \"$tag\"/" internal/pin/pin.go && rm internal/pin/pin.go.bak
echo "pinned to $tag:"
cat internal/pin/SHA256SUMS
