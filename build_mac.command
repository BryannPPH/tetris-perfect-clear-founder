#!/bin/bash
cd "$(dirname "$0")"
echo "Building local binary with embedded full opening_book.json..."
go build -o tetrio-dpc-advisor .
echo "Done. Run ./tetrio-dpc-advisor"
