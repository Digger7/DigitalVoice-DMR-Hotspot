#!/usr/bin/env bash
# Rebuild the Go binaries into bin/ (dmr-gui, dmr-hotspot, dmr-diag, dmr-selftest).
# Usage:
#   tools/rebuild.sh                 # all binaries, local platform
#   tools/rebuild.sh dmr-hotspot     # only the listed binaries
#   CGO_ENABLED=0 tools/rebuild.sh   # without the cgo vocoder (Linux/Windows)
# This does not rebuild the OpenDMR vocoder library; use tools/build-opendmr.sh for that.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

export CGO_ENABLED=${CGO_ENABLED:-1}

targets=("$@")
if [ ${#targets[@]} -eq 0 ]; then
	targets=(dmr-gui dmr-hotspot dmr-diag dmr-selftest)
fi

mkdir -p bin
for t in "${targets[@]}"; do
	if [ ! -d "cmd/$t" ]; then
		echo "unknown target '$t' (no cmd/$t)" >&2
		exit 1
	fi
	echo "building bin/$t"
	go build -o "bin/$t" "./cmd/$t"
done

echo "done: $ROOT/bin"
