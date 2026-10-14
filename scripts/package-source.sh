#!/bin/sh
set -eu
umask 077

# Keep host filesystem attributes and macOS AppleDouble files out of releases.
COPYFILE_DISABLE=1
export COPYFILE_DISABLE

fail() { printf '%s\n' "$*" >&2; exit 1; }
for tool in git tar gzip mktemp; do command -v "$tool" >/dev/null 2>&1 || fail "$tool is required."; done
if command -v sha256sum >/dev/null 2>&1; then checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then checksum_tool='shasum -a 256'
else fail 'SHA-256 tooling is required: sha256sum or shasum.'; fi

version=${1:-}
[ -n "$version" ] && [ "$version" != dev ] || fail 'Set VERSION to a release version, for example make dist VERSION=0.1.0.'
sh scripts/build-flags.sh "$version" '' >/dev/null
[ "$(git rev-parse --show-toplevel)" = "$(pwd -P)" ] || fail 'Run from the repository root.'
revision=$(git rev-parse --verify HEAD)
git diff --quiet HEAD -- || fail 'Tracked tree is dirty. Commit the reviewed changes before packaging.'

name="semantic-crawler-$version-source.tar.gz"
directory="semantic-crawler-$version"
[ ! -L dist ] || fail 'dist must not be a symlink.'
mkdir -p dist
[ ! -e "dist/$name" ] && [ ! -L "dist/$name" ] || fail "Destination already exists: dist/$name"
[ ! -e "dist/$name.sha256" ] && [ ! -L "dist/$name.sha256" ] || fail "Destination already exists: dist/$name.sha256"

staging=$(mktemp -d "${TMPDIR:-/tmp}/semantic-source.XXXXXX")
archive_tmp=
checksum_tmp=
owns_archive=0
owns_checksum=0
complete=0
cleanup() {
  rm -rf "$staging"
  [ -z "$archive_tmp" ] || rm -f "$archive_tmp"
  [ -z "$checksum_tmp" ] || rm -f "$checksum_tmp"
  if [ "$complete" = 0 ] && [ "$owns_archive" = 1 ]; then rm -f "dist/$name"; fi
  if [ "$complete" = 0 ] && [ "$owns_checksum" = 1 ]; then rm -f "dist/$name.sha256"; fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

git archive --format=tar --prefix="$directory/" HEAD > "$staging/source.tar"
tar -xf "$staging/source.tar" -C "$staging"
printf '%s\n' "$version" > "$staging/$directory/VERSION"
printf '%s\n' "$revision" > "$staging/$directory/REVISION"
chmod 644 "$staging/$directory/VERSION" "$staging/$directory/REVISION"
archive_tmp=$(mktemp "dist/.source.XXXXXX")
tar --no-xattrs -cf "$staging/release.tar" -C "$staging" "$directory"
gzip -n -c "$staging/release.tar" > "$archive_tmp"
checksum_tmp=$(mktemp "dist/.checksum.XXXXXX")
checksum_output=$($checksum_tool "$archive_tmp") || fail 'SHA-256 computation failed.'
hash=$(printf '%s\n' "$checksum_output" | awk '{print $1}')
printf '%s\n' "$hash" | LC_ALL=C grep -Eq '^[0-9a-f]{64}$' || fail 'Invalid SHA-256 output.'
printf '%s  %s\n' "$hash" "$name" > "$checksum_tmp"

# Hard-link publication in one filesystem refuses an existing destination,
# including one created after the initial check. Never overwrite release files.
chmod 644 "$archive_tmp" "$checksum_tmp"
ln "$archive_tmp" "dist/$name"
owns_archive=1
ln "$checksum_tmp" "dist/$name.sha256"
owns_checksum=1
complete=1
printf '%s\n' "dist/$name" "dist/$name.sha256"
