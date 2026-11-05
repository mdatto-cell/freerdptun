#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FREERDP_SOURCE="${FREERDP_SOURCE:-$ROOT/third_party/FreeRDP}"
BUILD="${BUILD:-$ROOT/build/linux}"
PREFIX="$BUILD/prefix"
OUT="${OUT:-$ROOT/dist/linux-amd64}"
source "$ROOT/scripts/freerdp-options.sh"
cmake -S "$FREERDP_SOURCE" -B "$BUILD/freerdp" -G Ninja \
  "${opts[@]}" "-DCMAKE_INSTALL_PREFIX=$PREFIX"
cmake --build "$BUILD/freerdp" -j "${JOBS:-4}"
cmake --install "$BUILD/freerdp"
cmake -S "$ROOT/native" -B "$BUILD/bridge" -G Ninja \
  -DCMAKE_BUILD_TYPE=Release "-DCMAKE_PREFIX_PATH=$PREFIX" \
  '-DCMAKE_INSTALL_RPATH=$ORIGIN/lib' -DCMAKE_BUILD_WITH_INSTALL_RPATH=ON
cmake --build "$BUILD/bridge" -j "${JOBS:-4}"
mkdir -p "$OUT/lib"
cp "$BUILD/bridge/rdpt-bridge" "$OUT/"
find "$PREFIX/lib" "$PREFIX/lib64" -maxdepth 1 -name '*.so*' -exec cp -a {} "$OUT/lib/" \; 2>/dev/null || true
if command -v go >/dev/null; then
  (cd "$ROOT"; CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$OUT/freerdptun" ./cmd/freerdptun)
else
  echo "Go not found: native bridge built; build Go executable separately." >&2
fi
echo "Linux output: $OUT"
