#!/usr/bin/env sh
set -eu

VERSION="${1:-1.0.0}"
COMMIT="${2:-unknown}"
BUILD_DATE="${3:-unknown}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
DIST="$ROOT/dist"

mkdir -p "$DIST"

build_one() {
  os="$1"
  arch="$2"
  out="$DIST/pachat_${VERSION}_${os}_${arch}"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w -X agent/internal/version.Version=${VERSION} -X agent/internal/version.Commit=${COMMIT} -X agent/internal/version.BuildDate=${BUILD_DATE}" \
    -o "$out/pachat" \
    ./cmd/pachat
  (cd "$out" && tar -czf "../pachat_${VERSION}_${os}_${arch}.tar.gz" pachat)
}

cd "$ROOT"
build_one darwin arm64
build_one linux amd64
build_one linux arm64
