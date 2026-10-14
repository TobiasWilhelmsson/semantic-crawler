#!/bin/sh
set -eu
command -v gofmt >/dev/null 2>&1 || { printf '%s\n' 'gofmt is required.' >&2; exit 1; }
root=$(pwd -P)
git_root=$(git rev-parse --show-toplevel 2>/dev/null || :)
if [ "$git_root" = "$root" ]; then
  unformatted=$(git ls-files -z -- '*.go' | xargs -0 gofmt -l)
else
  # Source archives have no Git metadata; only inspect the bundled Go packages.
  unformatted=$(find cmd internal scripts -type f -name '*.go' -exec gofmt -l {} +)
fi
if [ -n "$unformatted" ]; then
  printf '%s\n' 'Unformatted Go files (run gofmt):' "$unformatted" >&2
  exit 1
fi
