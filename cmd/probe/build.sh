#!/bin/bash
# Cross-compile qingzhou-probe for Linux amd64 and arm64.
# Run from the project root: bash cmd/probe/build.sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DIST_DIR="$SCRIPT_DIR/dist"
mkdir -p "$DIST_DIR"

echo "Building probe agent..."
VERSION="${VERSION:-dev}"
LDFLAGS="-s -w -X qingzhou/internal/version.Version=${VERSION}"
GOOS=linux GOARCH=amd64 go build -ldflags="$LDFLAGS" -o "$DIST_DIR/probe-linux-amd64" "$SCRIPT_DIR/"
GOOS=linux GOARCH=arm64 go build -ldflags="$LDFLAGS" -o "$DIST_DIR/probe-linux-arm64" "$SCRIPT_DIR/"

ls -lh "$DIST_DIR"/probe-*
echo "Done."
