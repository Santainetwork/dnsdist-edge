#!/bin/bash
# Build trust-builder dari source (Go 1.22+)
set -e
cd "$(dirname "$0")"
go build -o trust-builder main.go
echo "[✓] Built: $(pwd)/trust-builder"
