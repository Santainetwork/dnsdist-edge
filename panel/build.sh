#!/bin/bash
# Build dnsdist-panel binary
set -e
cd "$(dirname "$0")"
echo "[*] Building dnsdist-panel..."
GOOS=linux GOARCH=amd64 go build -ldflags='-s -w' -o ../tools/dnsdist-panel .
echo "[✓] panel binary: tools/dnsdist-panel ($(du -sh ../tools/dnsdist-panel | cut -f1))"
