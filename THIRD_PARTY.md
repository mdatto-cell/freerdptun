# Third-party components

FreeRDPTun's own code is distributed under Apache-2.0. The following upstream
components retain their own copyrights and licenses; upstream sources and
unmodified license texts are included or identified below.

## Go standard library

The Go executables include compiled Go standard-library code. The installed
toolchain's unmodified copyright and license notice, including the Go license,
is provided in `licenses/Go-COPYRIGHT.txt`.

## FreeRDP and WinPR

- **Version:** 3.28.0.
- **Pinned commit:** `5370fb26fbf034ecd11d3026b6ad639b5fff493f`.
- **License:** Apache-2.0, with upstream component notices preserved.
- **Source:** `third_party/FreeRDP/`, without `.git`; no upstream source patches.
- **Upstream:** https://github.com/FreeRDP/FreeRDP/releases/tag/3.28.0
- **Notices:** original `LICENSE`, `NOTICE` and component licenses are preserved
  in the vendored source tree and copied into `licenses/` where applicable.

## OpenSSL

- **Linux:** Ubuntu 22.04 system `libssl3` / `libcrypto3`, not bundled.
- **Windows:** OpenSSL 3.5.9, statically linked into native dependencies.
- **License:** Apache-2.0; unmodified text in `licenses/OpenSSL-LICENSE.txt`.
- **Source:** https://github.com/openssl/openssl/releases/download/openssl-3.5.9/openssl-3.5.9.tar.gz
- **Source archive SHA-256:** `603f5602e2eef00d77fbd429d34dcd5822bb301757a1bc9cdb24c670f1eb859a`.
- **Build:** MinGW-w64, `no-shared no-tests no-module`; see `docs/BUILD_RU.md`.

## Wintun

- **Version:** 0.14.1, original signed amd64 `wintun.dll`.
- **Distribution:** unmodified DLL as a non-standalone component of FreeRDPTun.
- **License:** upstream prebuilt-binary terms, copied unmodified to
  `licenses/Wintun-LICENSE.txt`; read these before redistributing the DLL.
- **Official site:** https://www.wintun.net/
- **Archive:** https://www.wintun.net/builds/wintun-0.14.1.zip
- **Archive SHA-256:** `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`.

## Compiler runtime

The Windows build may include `libgcc_s_seh-1.dll`. GCC Runtime Library
Exception and the corresponding GNU GPL texts are preserved in `licenses/`;
this does not relicense FreeRDPTun's own code.

## Operating-system libraries

Linux system OpenSSL, zlib, zstd, glibc and Windows system libraries are not
redistributed by this archive. Windows dependency imports are checked separately
in the release verification described in `docs/TEST_REPORT_RU.md`.
