#!/bin/bash
set -e
cd "$(dirname "$0")"
echo "[*] Building rpz-master (Go)..."
go build -ldflags "-s -w" -o rpz-master .
echo "[✓] Built: $(pwd)/rpz-master"
