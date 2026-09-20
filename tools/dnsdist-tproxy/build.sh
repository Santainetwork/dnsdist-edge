#!/bin/bash
set -e
cd "$(dirname "$0")"
GOTOOLCHAIN=local go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=local go build -trimpath -ldflags='-s -w' -o ../dnsdist-tproxy-bin .
echo "dnsdist-tproxy built: $(pwd)/../dnsdist-tproxy-bin"
