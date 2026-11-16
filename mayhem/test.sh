#!/usr/bin/env bash
#
# mayhem/test.sh — RUN hugo's own known-answer tests (pre-compiled by mayhem/build.sh) and
# emit a CTRF summary. Two legs:
#   1. mayhem/build/kat — a dynamically linked probe asserting exact rendered/parsed values
#      (markdown -> HTML, front matter, TOML/JSON decoding, ...). Each expected "KAT <n> ok" line
#      is one test; a probe that prints nothing (a neutered or no-op binary) fails them all.
#   2. `go test` over the packages in mayhem/test-packages.sh (parsers, goldmark rendering,
#      transform functions, config), whose qt assertions compare output values.
# exit 0 iff nothing failed.
set -uo pipefail
[ -n "${SOURCE_DATE_EPOCH:-}" ] || unset SOURCE_DATE_EPOCH
: "${SRC:=/mayhem}"
: "${MAYHEM_JOBS:=$(nproc)}"
cd "$SRC"

export GOFLAGS="${GOFLAGS:--mod=mod} -buildvcs=false"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"
export GOPATH="${GOPATH:-/opt/toolchains/go-path}"
export GOCACHE="${GOCACHE:-/opt/toolchains/go-path/build-cache}"
export GOMODCACHE="${GOMODCACHE:-/opt/toolchains/go-path/pkg/mod}"
export PATH="/opt/toolchains/go/bin:/opt/toolchains/go-path/bin:$PATH"
export GOPROXY="${GOPROXY:-file://$(go env GOMODCACHE)/cache/download,https://proxy.golang.org,direct}"
export CGO_ENABLED=1

# emit_ctrf <tool> <passed> <failed> [skipped] [pending] [other]
# Writes a CTRF report (file + stdout `CTRF {...}` marker) and returns non-zero iff failed>0.
emit_ctrf() {
  local tool="$1" passed="$2" failed="$3" skipped="${4:-0}" pending="${5:-0}" other="${6:-0}"
  local tests=$(( passed + failed + skipped + pending + other ))
  cat > "${CTRF_REPORT:-$SRC/ctrf-report.json}" <<JSON
{
  "results": {
    "tool": { "name": "$tool" },
    "summary": {
      "tests": $tests,
      "passed": $passed,
      "failed": $failed,
      "pending": $pending,
      "skipped": $skipped,
      "other": $other
    }
  }
}
JSON
  printf 'CTRF {"results":{"tool":{"name":"%s"},"summary":{"tests":%d,"passed":%d,"failed":%d,"pending":%d,"skipped":%d,"other":%d}}}\n' \
    "$tool" "$tests" "$passed" "$failed" "$pending" "$skipped" "$other"
  [ "$failed" -eq 0 ]
}

KAT=mayhem/build/kat
[ -x "$KAT" ] || { echo "test.sh: $KAT missing — mayhem/build.sh must build it" >&2; emit_ctrf "hugo-kat+go-test" 0 1 0; exit 1; }
KAT_EXPECTED=21

OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

"$KAT" > "$OUT/kat.log" 2>&1 || true
cat "$OUT/kat.log"
kat_pass=$(grep -c '^KAT .* ok$' "$OUT/kat.log" || true)
kat_fail=$(( KAT_EXPECTED - kat_pass ))
[ "$kat_fail" -ge 0 ] || kat_fail=0

. mayhem/test-packages.sh
# shellcheck disable=SC2086
go test -count=1 -json $TEST_PACKAGES > "$OUT/go.json" 2> "$OUT/go.err"; go_rc=$?
tail -n 5 "$OUT/go.err" >&2
go_pass=$(grep -cE '"Action":"pass".*"Test":' "$OUT/go.json" || true)
go_fail=$(grep -cE '"Action":"fail".*"Test":' "$OUT/go.json" || true)
go_skip=$(grep -cE '"Action":"skip".*"Test":' "$OUT/go.json" || true)
# A package that fails to build/run reports a package-level fail with no test events.
pkg_fail=$(grep -cE '"Action":"fail"' "$OUT/go.json" || true)
[ "$go_fail" -gt 0 ] || go_fail=$pkg_fail
# go test failed without reporting a failing test or package (e.g. it could not start).
[ "$go_rc" -eq 0 ] || [ "$go_fail" -gt 0 ] || go_fail=1

emit_ctrf "hugo-kat+go-test" $(( kat_pass + go_pass )) $(( kat_fail + go_fail )) "$go_skip"
