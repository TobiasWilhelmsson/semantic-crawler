#!/bin/sh
set -eu
umask 077

# Command-line settings are also carried by Make's override channels. Keep the
# selected Go executable, but prevent outer source/report settings being restored.
unset MAKEFLAGS MFLAGS MAKEOVERRIDES

# CI runs these checks in its platform matrix and invokes the offline stage once.
case ${1:-} in
  '') make fmt-check mod-check test race vet lint build ;;
  --skip-code-checks) [ "$#" = 1 ] || exit 2 ;;
  *) printf '%s\n' 'Usage: sh scripts/release-check.sh [--skip-code-checks]' >&2; exit 2 ;;
esac

go_tool=${GO:-go}
# Do not inherit the operator's source selection or provider credentials.
for name in $(env | sed -n 's/^\(SEMANTIC_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$name"; done
unset SOURCE MODEL_MODE DB REPORT_ROOT BASELINE MAX_PAGES VERSION REVISION
root=$(pwd -P)
work=$(mktemp -d "${TMPDIR:-/tmp}/semantic-release-check.XXXXXX")
complete=0
cleanup() {
  if [ "$complete" = 1 ]; then rm -rf "$work"
  else printf 'Release check failed; logs and isolated data retained at %s\n' "$work" >&2; fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

version="0.0.0-check.$(date +%s).$$"
archive="dist/semantic-crawler-$version-source.tar.gz"
sh scripts/package-source.sh "$version" > "$work/package.log" 2>&1 || { cat "$work/package.log" >&2; exit 1; }
tar -xf "$archive" -C "$work"
source_dir="$work/semantic-crawler-$version"

# Each report root is new. These are deterministic pipeline checks, not LLM runs.
if ! (cd "$source_dir" && make setup build && make demo SOURCE=generic-health-sample DB="$work/demo.db" && make eval benchmark proof reviewer-learning SOURCE=generic-health-sample REPORT_ROOT="$work/reports") > "$work/offline.log" 2>&1; then
  tail -50 "$work/offline.log" >&2
  exit 1
fi
if ! (cd "$source_dir" && RELEASE_CHECK_WORK="$work" "$go_tool" run ./scripts/release-smoke.go) > "$work/smoke.log" 2>&1; then
  tail -50 "$work/smoke.log" >&2
  exit 1
fi
cat "$work/smoke.log"
printf 'Source archive and fresh offline workflows verified (%s).\n' "$version"
rm -f "$root/$archive" "$root/$archive.sha256"
complete=1
