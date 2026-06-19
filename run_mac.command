#!/bin/bash
cd "$(dirname "$0")"
if command -v go >/dev/null 2>&1; then
  go run .
else
  ARCH=$(uname -m)
  if [ "$ARCH" = "arm64" ]; then
    ./bin/tetrio-go-smart-advisor-darwin-arm64
  else
    ./bin/tetrio-go-smart-advisor-darwin-amd64
  fi
fi
