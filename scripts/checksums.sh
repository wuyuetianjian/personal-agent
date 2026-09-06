#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
DIST="$ROOT/dist"

if [ ! -d "$DIST" ]; then
  echo "dist directory is missing; run make release-build first" >&2
  exit 1
fi

cd "$DIST"
rm -f SHA256SUMS
for file in *.tar.gz; do
  [ -e "$file" ] || continue
  shasum -a 256 "$file" >> SHA256SUMS
done

test -s SHA256SUMS
