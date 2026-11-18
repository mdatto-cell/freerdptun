#!/usr/bin/env bash
# Собирает нативные библиотеки для iPhone (arm64) в ios/Libs:
#   OpenSSL (static) -> FreeRDP 3.28.0 (static) -> bridge.c (-DFRT_LIBRARY) -> Go c-archive.
# Запускать на macOS с Xcode, Go 1.23+, cmake, ninja (brew install cmake ninja go perl).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
IOS="$ROOT/ios"
OUT="$IOS/Libs"
WORK="${WORK:-$ROOT/build/ios}"
MIN_IOS="${MIN_IOS:-16.0}"
JOBS="${JOBS:-$(sysctl -n hw.ncpu)}"
OPENSSL_VERSION="${OPENSSL_VERSION:-3.5.9}"
OPENSSL_SHA256="603f5602e2eef00d77fbd429d34dcd5822bb301757a1bc9cdb24c670f1eb859a"
FREERDP_SOURCE="${FREERDP_SOURCE:-$ROOT/third_party/FreeRDP}"
SDK="$(xcrun --sdk iphoneos --show-sdk-path)"
CLANG="$(xcrun --sdk iphoneos --find clang)"
mkdir -p "$WORK" "$OUT/lib" "$OUT/include"

# ---------- 1. OpenSSL ----------
OSSL="$WORK/openssl-ios"
if [[ ! -f "$OSSL/lib/libcrypto.a" ]]; then
  cd "$WORK"
  if [[ ! -f openssl.tar.gz ]]; then
    curl -fL "https://github.com/openssl/openssl/releases/download/openssl-$OPENSSL_VERSION/openssl-$OPENSSL_VERSION.tar.gz" -o openssl.tar.gz
  fi
  echo "$OPENSSL_SHA256  openssl.tar.gz" | shasum -a 256 -c -
  rm -rf "openssl-$OPENSSL_VERSION"; tar xzf openssl.tar.gz
  cd "openssl-$OPENSSL_VERSION"
  ./Configure ios64-xcrun no-shared no-tests no-apps no-module no-dso no-engine no-async \
    "-mios-version-min=$MIN_IOS" --prefix="$OSSL" --openssldir="$OSSL/ssl"
  make -j"$JOBS" build_libs
  make install_dev
fi

# ---------- 2. FreeRDP (static, client only) ----------
source "$ROOT/scripts/freerdp-options.sh"
FR="$WORK/freerdp"
PREFIX="$WORK/freerdp-prefix"
cmake -S "$FREERDP_SOURCE" -B "$FR" -G Ninja \
  -DCMAKE_TOOLCHAIN_FILE="$FREERDP_SOURCE/cmake/ios.toolchain.cmake" \
  -DPLATFORM=OS64 -DDEPLOYMENT_TARGET="$MIN_IOS" -DENABLE_BITCODE=OFF \
  "${opts[@]}" \
  -DBUILD_SHARED_LIBS=OFF -DWITH_SERVER=OFF -DWITH_SERVER_CHANNELS=OFF \
  -DWITH_CLIENT=OFF -DWITH_CLIENT_COMMON=ON -DWITH_CLIENT_CHANNELS=ON \
  -DWITH_OPENSSL=ON -DOPENSSL_ROOT_DIR="$OSSL" -DOPENSSL_USE_STATIC_LIBS=ON \
  -DCMAKE_PREFIX_PATH="$OSSL" -DCMAKE_INSTALL_PREFIX="$PREFIX"
cmake --build "$FR" -j "$JOBS"
cmake --install "$FR"

# ---------- 3. native bridge as a library ----------
BR="$WORK/bridge"; mkdir -p "$BR"
FRINC=()
for d in "$PREFIX"/include/freerdp3 "$PREFIX"/include/winpr3 "$OSSL/include"; do FRINC+=("-I$d"); done
"$CLANG" -arch arm64 -isysroot "$SDK" -miphoneos-version-min="$MIN_IOS" -O2 -Wall \
  -DFRT_LIBRARY -I"$ROOT/native" "${FRINC[@]}" -c "$ROOT/native/bridge.c" -o "$BR/bridge.o"
rm -f "$OUT/lib/libfrtbridge.a"; ar rcs "$OUT/lib/libfrtbridge.a" "$BR/bridge.o"

# ---------- 4. Go core ----------
cd "$ROOT"
CGO_ENABLED=1 GOOS=ios GOARCH=arm64 CC="$CLANG" \
CGO_CFLAGS="-isysroot $SDK -arch arm64 -miphoneos-version-min=$MIN_IOS" \
CGO_LDFLAGS="-isysroot $SDK -arch arm64 -miphoneos-version-min=$MIN_IOS" \
  go build -trimpath -ldflags="-s -w" -buildmode=c-archive -o "$OUT/lib/libfrtcore.a" ./ios/core

# ---------- 5. collect libraries ----------
# FreeRDP/WinPR/channel static archives (names are unique across the build tree).
find "$FR" -name '*.a' -exec cp -f {} "$OUT/lib/" \;
cp -f "$OSSL/lib/libssl.a" "$OSSL/lib/libcrypto.a" "$OUT/lib/"
libs=""
for f in "$OUT"/lib/lib*.a; do
  n="$(basename "$f" .a)"; libs="$libs -l${n#lib}"
done
cat > "$OUT/frt.xcconfig" <<XC
// Сгенерировано scripts/build-ios-libs.sh
FRT_LIBS =$libs
XC
echo "OK: $OUT"
ls -la "$OUT/lib"
