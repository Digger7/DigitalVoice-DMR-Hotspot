#!/usr/bin/env bash
# Rebuild internal/dmr/libopendmr.dylib from the patched OpenDMR v1.0.0 source.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SRC=${OPENDMR_SRC:-"$ROOT/.cache/OpenDMR"}
PATCH="$ROOT/tools/opendmr/opendmr-fixes.patch"
REPO=${OPENDMR_REPO:-https://github.com/MW0MWZ/OpenDMR.git}
REV=${OPENDMR_REV:-d28164b39ba4d91ad5948ff22707937f8944f70f}

if [ ! -d "$SRC/.git" ]; then
	git clone "$REPO" "$SRC"
fi

git -C "$SRC" fetch --depth 1 origin "$REV"
git -C "$SRC" checkout --force "$REV"
git -C "$SRC" clean -fdx
git -C "$SRC" apply --check "$PATCH"
git -C "$SRC" apply "$PATCH"

make -C "$SRC" clean
make -C "$SRC" libopendmr.dylib

if [ "$(uname -s)" = "Darwin" ]; then
	codesign --force --sign - "$SRC/libopendmr.dylib"
fi

cp "$SRC/libopendmr.dylib" "$ROOT/internal/dmr/libopendmr.dylib"

if [ "$(uname -s)" = "Darwin" ]; then
	codesign --force --sign - "$ROOT/internal/dmr/libopendmr.dylib"
fi

echo "built $ROOT/internal/dmr/libopendmr.dylib"
