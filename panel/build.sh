#!/bin/bash
# Build dnsdist-panel binary
set -e
cd "$(dirname "$0")"
echo "[*] Building dnsdist-panel..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o ../tools/dnsdist-panel .
echo "[✓] panel binary: tools/dnsdist-panel ($(du -sh ../tools/dnsdist-panel | cut -f1))"
