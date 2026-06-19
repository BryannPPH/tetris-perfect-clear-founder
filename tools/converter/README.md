# DPC Opening Book Converter Full

Converter ini mengubah `DPC_All_Search_Database_v1.0.0.xlsx` menjadi `opening_book.json` untuk advisor Go versi full-book.

Perbedaan dari converter lama:

- Memakai decoder `tetris-fumen`, bukan decoder D115 manual.
- `D115@` otomatis dibaca sebagai `v115@` agar bisa didecode oleh library fumen.
- Menyimpan `placementSteps` sebagai array, sehingga setup dengan piece duplikat seperti `O,O`, `S,S`, `Z,Z`, dan lain-lain tidak dibuang.
- Bisa mengonversi semua 4081 row valid dari sheet `O/S/Z/I/T/J/L`.
- Membuat `byOrder` broad index dari kolom `Setup Condition`.

## Cara Pakai

```bash
npm install
node convert_dpc_excel_to_opening_book_full.js ../DPC_All_Search_Database_v1.0.0.xlsx ./opening_book.json
```

Atau langsung copy hasilnya ke folder advisor:

```bash
node convert_dpc_excel_to_opening_book_full.js \
  ../DPC_All_Search_Database_v1.0.0.xlsx \
  ./opening_book.json \
  --copy-to=../tetrio_go_srs_allspin_advisor_full_book/opening_book.json
```

## Output

Output utama adalah:

- `opening_book.json`
- `conversion_report_full.md`

## Catatan

File `opening_book.json` full cukup besar karena menyimpan semua entry, semua setup rows, placement steps, dan by-order index. Kalau ingin file lebih kecil, gunakan opsi:

```bash
node convert_dpc_excel_to_opening_book_full.js input.xlsx output.json --no-by-order
```

Tetapi jika `--no-by-order` dipakai, advisor tidak bisa memilih opening secara otomatis berdasarkan urutan 7-bag dan lebih cocok untuk mode manual/debug.
