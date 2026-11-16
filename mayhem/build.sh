#!/usr/bin/env bash
#
# mayhem/build.sh — build hugo's libFuzzer targets with Go's native libFuzzer
# instrumentation (-gcflags=all=-d=libfuzzer, buildmode=c-archive; each target is a cgo
# main package exporting LLVMFuzzerTestOneInput) linked with clang -fsanitize=fuzzer,address,
# plus the known-answer probe and the pre-compiled test packages mayhem/test.sh runs.
#
# Runs in the commit image as `mayhem` in /mayhem. Idempotent and air-gapped on re-run: the
# module cache is the file proxy at $GOMODCACHE/cache/download (network only fills misses on
# the first online build).
set -euo pipefail

[ -n "${SOURCE_DATE_EPOCH:-}" ] || unset SOURCE_DATE_EPOCH

: "${SRC:=/mayhem}"
: "${CC:=clang}" ; : "${CXX:=clang++}" ; : "${LIB_FUZZING_ENGINE:=-fsanitize=fuzzer}"
# Go links are ASan-only (OSS-Fuzz Go path); an explicit empty SANITIZER_FLAGS disables it.
: "${SANITIZER_FLAGS=-fsanitize=address}"
: "${MAYHEM_JOBS:=$(nproc)}"
export CC CXX LIB_FUZZING_ENGINE SANITIZER_FLAGS MAYHEM_JOBS

# Go's gc compiler emits DWARF 4/5; the C shim compiled with -gdwarf-3 is linked FIRST so the
# first .debug_info CU is DWARF 3 (SPEC §6.2 item 10).
: "${GO_DEBUG_FLAGS:=-g -gdwarf-3}"
export CGO_CFLAGS="${CGO_CFLAGS:+$CGO_CFLAGS }$GO_DEBUG_FLAGS"
export CGO_CXXFLAGS="${CGO_CXXFLAGS:+$CGO_CXXFLAGS }$GO_DEBUG_FLAGS"
export CGO_ENABLED=1

export GOFLAGS="${GOFLAGS:--mod=mod}"
export GOFLAGS="$GOFLAGS -buildvcs=false"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
export GOPATH="${GOPATH:-/opt/toolchains/go-path}"
export GOCACHE="${GOCACHE:-/opt/toolchains/go-path/build-cache}"
export GOMODCACHE="${GOMODCACHE:-/opt/toolchains/go-path/pkg/mod}"
export PATH="/opt/toolchains/go/bin:/opt/toolchains/go-path/bin:$PATH"
export GOPROXY="${GOPROXY:-file://$(go env GOMODCACHE)/cache/download,https://proxy.golang.org,direct}"

cd "$SRC"
go version

BUILD="$SRC/mayhem/build"
mkdir -p "$BUILD"

# Init-ordering shim (LLVMFuzzerInitialize), linked FIRST into every target.
SHIM_O="$BUILD/go_runtime_ready.o"
$CC $GO_DEBUG_FLAGS -c "$SRC/mayhem/go_runtime_ready.c" -o "$SHIM_O"

# LeakSanitizer off at build time (fleet policy); ASan stays on.
LSAN_O="$BUILD/lsan_off.o"
$CXX $SANITIZER_FLAGS $GO_DEBUG_FLAGS -c "$SRC/mayhem/lsan_off.cc" -o "$LSAN_O"

TARGETS="fuzzmarkdownify fuzzpageparse fuzzdecode fuzzconfig fuzzimagemeta fuzztransform fuzzpath fuzzminify fuzzstrings fuzzorg fuzzimagedecode fuzzfrontmatter"
for t in $TARGETS; do
  echo "=== building $t ==="
  go build -tags libfuzzer -buildmode=c-archive "-gcflags=all=-d=libfuzzer" \
    -o "$BUILD/$t.a" "./mayhem/$t"
  $CXX $SANITIZER_FLAGS $LIB_FUZZING_ENGINE $GO_DEBUG_FLAGS "$SHIM_O" "$LSAN_O" "$BUILD/$t.a" \
    -o "$SRC/$t" -lpthread
  # No `grep -q`: under pipefail nm would die of SIGPIPE.
  nm "$SRC/$t" | grep ' T LLVMFuzzerInitialize$' >/dev/null \
    || { echo "build.sh: LLVMFuzzerInitialize init-ordering shim missing from $SRC/$t" >&2; exit 1; }
  echo "built $SRC/$t"
done

# Known-answer probe for mayhem/test.sh. It must be DYNAMICALLY linked (cgo): the sabotage
# check LD_PRELOADs an exit(0) constructor, which only takes effect in dynamic executables.
echo "=== building kat ==="
go build -o "$BUILD/kat" ./mayhem/kat
interp="$(readelf -l "$BUILD/kat" | grep 'Requesting program interpreter' || true)"
[ -n "$interp" ] || { echo "build.sh: $BUILD/kat is statically linked" >&2; exit 1; }

# Pre-compile the test packages mayhem/test.sh runs (normal flags, nothing executed).
. "$SRC/mayhem/test-packages.sh"
echo "=== pre-compiling tests ($TEST_PACKAGES) ==="
go test -count=1 -run '^$' $TEST_PACKAGES

echo "build.sh complete"
