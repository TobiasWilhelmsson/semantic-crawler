#!/bin/sh
set -eu

version=${1:-}
revision=${2:-}
if [ -z "$version" ]; then
  if [ -f VERSION ]; then version=$(cat VERSION); else version=dev; fi
fi
if [ -z "$revision" ] && [ -f REVISION ]; then revision=$(cat REVISION); fi

# Linker arguments and filenames must contain data, never shell/Make syntax.
if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^(dev|[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?)$' ||
   [ "$(printf '%s' "$version" | wc -l | tr -d ' ')" != 0 ]; then
  printf '%s\n' 'Invalid version: use dev or a semantic version such as 0.1.0.' >&2
  exit 1
fi
if [ -n "$revision" ]; then
  if ! printf '%s\n' "$revision" | LC_ALL=C grep -Eq '^(unknown|[0-9a-f]{40}|[0-9a-f]{64})$' ||
     [ "$(printf '%s' "$revision" | wc -l | tr -d ' ')" != 0 ]; then
    printf '%s\n' 'Invalid revision: use a full lowercase Git revision or unknown.' >&2
    exit 1
  fi
fi
printf '%s' "-X main.buildVersion=$version"
if [ -n "$revision" ]; then printf ' -X main.buildRevision=%s' "$revision"; fi
printf '\n'
