#!/usr/bin/env bash
# Собирает нативные библиотеки для Android arm64-v8a в android/app/src/main/jniLibs:
#   OpenSSL (static) -> FreeRDP 3.28.0 (static) -> bridge.c (-DFRT_LIBRARY) + JNI -> libfrtjni.so
#   Go-ядро (mobile/core)                                                   -> libfrtcore.so
# Нужны: Android NDK (r27+), Go 1.23+, cmake, ninja, perl, curl. Linux или macOS.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/android/app/src/main/jniLibs/arm64-v8a"
WORK="${WORK:-$ROOT/build/android}"
API="${API:-26}"
JOBS="${JOBS:-$( (nproc || sysctl -n hw.ncpu) 2>/dev/null || echo 4)}"
OPENSSL_VERSION="${OPENSSL_VERSION:-3.5.9}"
OPENSSL_SHA256="603f5602e2eef00d77fbd429d34dcd5822bb301757a1bc9cdb24c670f1eb859a"
FREERDP_SOURCE="${FREERDP_SOURCE:-$ROOT/third_party/FreeRDP}"

if [[ -z "${ANDROID_NDK_HOME:-}" ]]; then
  SDK="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Android/Sdk}}"
  [[ -d "$SDK" ]] || SDK="$HOME/Library/Android/sdk"
  ANDROID_NDK_HOME="$(ls -d "$SDK"/ndk/* 2>/dev/null | sort -V | tail -1)"
fi
[[ -d "${ANDROID_NDK_HOME:-}" ]] || { echo "Задайте ANDROID_NDK_HOME (SDK Manager -> NDK)"; exit 1; }
case "$(uname -s)" in Darwin) HOST=darwin-x86_64;; *) HOST=linux-x86_64;; esac
TOOLS="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$HOST"
export ANDROID_NDK_ROOT="$ANDROID_NDK_HOME" PATH="$TOOLS/bin:$PATH"
CC="$TOOLS/bin/aarch64-linux-android${API}-clang"
PAGE="-Wl,-z,max-page-size=16384"   # Android 15+: страницы 16 КБ
mkdir -p "$WORK" "$OUT"

# ---------- 1. OpenSSL ----------
OSSL="$WORK/openssl"
if [[ ! -f "$OSSL/lib/libcrypto.a" ]]; then
  cd "$WORK"
  [[ -f openssl.tar.gz ]] || curl -fL "https://github.com/openssl/openssl/releases/download/openssl-$OPENSSL_VERSION/openssl-$OPENSSL_VERSION.tar.gz" -o openssl.tar.gz
  echo "$OPENSSL_SHA256  openssl.tar.gz" | (sha256sum -c - 2>/dev/null || shasum -a 256 -c -)
  rm -rf "openssl-$OPENSSL_VERSION"; tar xzf openssl.tar.gz; cd "openssl-$OPENSSL_VERSION"
  ./Configure android-arm64 -D__ANDROID_API__="$API" no-shared no-tests no-apps no-module no-engine \
    --prefix="$OSSL" --openssldir="$OSSL/ssl"
  make -j"$JOBS" build_libs
  make install_dev
  [[ -f "$OSSL/lib/libcrypto.a" ]] || cp "$OSSL"/lib64/lib*.a "$OSSL/lib/"
fi

# ---------- 2. FreeRDP (static, client only) ----------
source "$ROOT/scripts/freerdp-options.sh"
FR="$WORK/freerdp"; PREFIX="$WORK/freerdp-prefix"
cmake -S "$FREERDP_SOURCE" -B "$FR" -G Ninja \
  -DCMAKE_TOOLCHAIN_FILE="$ANDROID_NDK_HOME/build/cmake/android.toolchain.cmake" \
  -DANDROID_ABI=arm64-v8a -DANDROID_PLATFORM="android-$API" \
  "${opts[@]}" \
  -DBUILD_SHARED_LIBS=OFF -DWITH_SERVER=OFF -DWITH_SERVER_CHANNELS=OFF \
  -DWITH_CLIENT=OFF -DWITH_CLIENT_COMMON=ON -DWITH_CLIENT_CHANNELS=ON \
  -DWITH_OPENSSL=ON -DOPENSSL_ROOT_DIR="$OSSL" -DOPENSSL_USE_STATIC_LIBS=ON \
  -DCMAKE_PREFIX_PATH="$OSSL" -DCMAKE_POSITION_INDEPENDENT_CODE=ON \
  -DCMAKE_SHARED_LINKER_FLAGS="$PAGE" -DCMAKE_INSTALL_PREFIX="$PREFIX"
cmake --build "$FR" -j "$JOBS"
cmake --install "$FR"

# ---------- 3. bridge + JNI -> libfrtjni.so ----------
INC=(-I"$ROOT/native" -I"$PREFIX/include/freerdp3" -I"$PREFIX/include/winpr3" -I"$OSSL/include")
mkdir -p "$WORK/obj"
"$CC" -O2 -fPIC -DFRT_LIBRARY "${INC[@]}" -c "$ROOT/native/bridge.c" -o "$WORK/obj/bridge.o"
"$CC" -O2 -fPIC "${INC[@]}" -c "$ROOT/android/jni/frt_jni.c" -o "$WORK/obj/frt_jni.o"

# ---------- 4. Go core -> libfrtcore.so ----------
cd "$ROOT"
CGO_ENABLED=1 GOOS=android GOARCH=arm64 CC="$CC" \
  go build -trimpath -buildmode=c-shared \
  -ldflags="-s -w -extldflags '-Wl,-soname,libfrtcore.so $PAGE'" \
  -o "$OUT/libfrtcore.so" ./mobile/core
rm -f "$OUT/libfrtcore.h"

# ---------- 5. link JNI library ----------
LIBS=("$PREFIX"/lib/libfreerdp-client3.a "$PREFIX"/lib/libfreerdp3.a "$PREFIX"/lib/libwinpr3.a
      "$OSSL"/lib/libssl.a "$OSSL"/lib/libcrypto.a)
"$CC" -shared -o "$OUT/libfrtjni.so" -Wl,-soname,libfrtjni.so "$PAGE" -Wl,--no-undefined \
  "$WORK/obj/frt_jni.o" "$WORK/obj/bridge.o" -Wl,--start-group "${LIBS[@]}" -Wl,--end-group \
  -L"$OUT" -lfrtcore -llog -lz -lm -ldl
"$TOOLS/bin/llvm-strip" --strip-unneeded "$OUT/libfrtjni.so" || true
echo "OK: $OUT"; ls -la "$OUT"
