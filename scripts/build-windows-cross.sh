#!/usr/bin/env bash
# Run on Linux with MinGW-w64 and a separately cross-built OpenSSL 3.5.x.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
: "${OPENSSL_WIN:?Set OPENSSL_WIN to the Windows OpenSSL prefix}"
FREERDP_SOURCE="${FREERDP_SOURCE:-$ROOT/third_party/FreeRDP}"
BUILD="${BUILD:-$ROOT/build/windows}"
PREFIX="$BUILD/prefix"
OUT="${OUT:-$ROOT/dist/windows-amd64}"
source "$ROOT/scripts/freerdp-options.sh"
ssl_lib="$OPENSSL_WIN/lib64"
[[ -f "$ssl_lib/libssl.a" ]] || ssl_lib="$OPENSSL_WIN/lib"
common=(
 "-DCMAKE_TOOLCHAIN_FILE=$ROOT/scripts/mingw-toolchain.cmake"
 "-DOPENSSL_INCLUDE_DIR=$OPENSSL_WIN/include"
 "-DOPENSSL_ROOT_DIR=$OPENSSL_WIN"
 "-DLIB_EAY=$ssl_lib/libcrypto.a"
 "-DSSL_EAY=$ssl_lib/libssl.a"
 -DCMAKE_C_FLAGS=-D__STDC_NO_THREADS__
 "-DOPENSSL_SSL_LIBRARY=$ssl_lib/libssl.a"
 "-DOPENSSL_CRYPTO_LIBRARY=$ssl_lib/libcrypto.a"
 -DOPENSSL_USE_STATIC_LIBS=TRUE
)
cmake -S "$FREERDP_SOURCE" -B "$BUILD/freerdp" -G Ninja \
 "${opts[@]}" "${common[@]}" "-DCMAKE_INSTALL_PREFIX=$PREFIX"
cmake --build "$BUILD/freerdp" -j "${JOBS:-4}"
cmake --install "$BUILD/freerdp"
cmake -S "$ROOT/native" -B "$BUILD/bridge" -G Ninja \
 "${common[@]}" -DCMAKE_BUILD_TYPE=Release "-DCMAKE_PREFIX_PATH=$PREFIX"
cmake --build "$BUILD/bridge" -j "${JOBS:-4}"
mkdir -p "$OUT"
cp "$BUILD/bridge/rdpt-bridge.exe" "$OUT/"
find "$PREFIX" -name '*.dll' ! -name '*freerdp-server*' -exec cp {} "$OUT/" \;
rm -f "$OUT/libfreerdp-server3.dll" "$OUT/libstdc++-6.dll" "$OUT/libwinpthread-1.dll" "$OUT/libgcc_s_seh-1.dll"
# Copy compiler runtime DLLs only if they exist. Dependency closure checked by release audit.
for dll in libgcc_s_seh-1.dll libwinpthread-1.dll libstdc++-6.dll; do
 imports="$(x86_64-w64-mingw32-objdump -p "$OUT"/*.dll "$OUT"/rdpt-bridge.exe)"
 if [[ "${imports,,}" == *"dll name: ${dll,,}"* ]]; then
   p="$(x86_64-w64-mingw32-gcc -print-file-name="$dll")"
   [[ -f "$p" ]] && cp "$p" "$OUT/" || true
 fi
done
(cd "$ROOT"; CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$OUT/freerdptun.exe" ./cmd/freerdptun)
echo "Windows output: $OUT"
