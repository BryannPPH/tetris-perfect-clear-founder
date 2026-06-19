# TETR.IO Go SRS All-Spin + DPC Advisor

Ini adalah paket final source program untuk offline TETR.IO placement advisor berbasis Go.

## Isi utama

- `main.go` — server lokal, UI web, search engine, SRS/all-spin movement, scoring approximation, DP/beam search.
- `opening_book.go` — loader dan matcher untuk DPC opening book.
- `opening_book.json` — full DPC opening book hasil convert dari database Excel, berisi 4081 entry valid.
- `go.mod` — konfigurasi module Go.
- `run_mac.command` — shortcut menjalankan program di macOS.
- `run_windows.bat` — shortcut menjalankan program di Windows.
- `build_mac.command` — optional build binary lokal.
- `tools/converter/` — converter Excel DPC ke `opening_book.json`.
- `data/DPC_All_Search_Database_v1.0.0.xlsx` — file database sumber untuk regenerate opening book.
- `reports/opening_book_full_conversion_report.md` — ringkasan hasil convert.

## Cara menjalankan di macOS

Pastikan Go sudah terinstall, lalu jalankan:

```bash
./run_mac.command
```

Atau manual:

```bash
go run .
```

Lalu buka browser:

```text
http://127.0.0.1:8787
```

## Cara menjalankan di Windows

Klik dua kali:

```text
run_windows.bat
```

Atau buka terminal di folder ini:

```bash
go run .
```

## Build binary lokal

Karena `opening_book.json` besar dan di-embed ke binary, binary hasil build akan besar juga. Untuk build sendiri:

```bash
go build -o tetrio-dpc-advisor .
```

Di macOS bisa pakai:

```bash
./build_mac.command
```

## Fitur utama

- Board 20x10 clickable.
- Current piece, hold, dan next 5 piece input.
- SRS-style wall kick reachability.
- Movement simulation: left, right, down, CW, CCW.
- T-Spin dan Mini T-Spin detection.
- Conservative all-spin detection untuk S/Z/J/L/I spin berbasis rotated + immobile placement.
- PC/DPC mode.
- Seven-bag residue inference.
- Beam-pruned finite-horizon dynamic programming search.
- Full DPC opening book dari 4081 entry database.

## Regenerate opening_book.json dari Excel

Masuk ke folder converter:

```bash
cd tools/converter
npm install
node convert_dpc_excel_to_opening_book_full.js ../../data/DPC_All_Search_Database_v1.0.0.xlsx ../../opening_book.json
```

Setelah mengganti `opening_book.json`, jika memakai binary hasil build, rebuild binary karena file ini di-embed oleh Go:

```bash
go build -o tetrio-dpc-advisor .
```

## Catatan penting

Program ini adalah offline advisor untuk analisis dan latihan, bukan live gameplay bot. SRS, spin detection, dan scoring masih approximation, bukan byte-perfect clone engine TETR.IO asli. Namun versi ini sudah jauh lebih lengkap dibanding hard-drop-only search karena sudah memasukkan SRS reachability, all-spin classification approximation, seven-bag residue, dan DPC opening book.
