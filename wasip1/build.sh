#!/bin/sh
# Build gh for WebAssembly (WASI preview1).
#
#   ./wasip1/build.sh [output]      # default: gh.wasm
#
# Needs Go (the version go.mod asks for) and network access the first time, to
# download the modules. Nothing else: no wasi-sdk, no emscripten - the Go
# toolchain targets wasip1 on its own.
#
# The dependencies are vendored and patched here rather than forked, because
# what they are missing is small (a build tag, a stub for a syscall wasip1 does
# not have) and a patch that stops applying is how we learn an upgrade needs
# looking at. See wasip1/patches and wasip1/README.md.
set -eu

out=${1:-gh.wasm}
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

echo "==> go mod vendor"
go mod vendor

echo "==> applying wasip1 patches"
for patch in wasip1/patches/*.patch; do
	echo "    $(basename "$patch")"
	git apply -p1 "$patch"
done

echo "==> building $out (GOOS=wasip1 GOARCH=wasm)"
# -trimpath: the same source produces the same module, whoever built it.
# -s -w: drop the symbol table and DWARF. The module is downloaded by a browser,
# and a stack trace from a stripped Go binary still names the functions.
GOOS=wasip1 GOARCH=wasm go build \
	-mod=vendor \
	-trimpath \
	-ldflags="-s -w" \
	-o "$out" \
	./cmd/gh

ls -l "$out"
