#!/usr/bin/env node

const fs = require('fs');
const path = require('path');

function loadModule(name) {
  const local = process.env.OPENING_BOOK_NODE_MODULES;
  if (local) {
    return require(path.join(local, name));
  }
  try {
    return require(name);
  } catch (_) {
    const fallback = '/tmp/tetris-fumen-tools/node_modules';
    return require(path.join(fallback, name));
  }
}

const xlsx = loadModule('xlsx');
const { decoder } = loadModule('tetris-fumen');

const root = path.resolve(__dirname, '..');
const workbookPath = process.argv[2] || '/Users/bryanpratama/Downloads/DPC_All_Search_Database_v1.0.0.xlsx';
const inputJSON = process.argv[3] || path.join(root, 'opening_book.json');
const outputJSON = process.argv[4] || inputJSON;
const sheets = ['O', 'S', 'Z', 'I', 'T', 'J', 'L'];

function normalizeFumen(value) {
  if (!value) return null;
  const raw = String(value).trim();
  const match = raw.match(/([A-Za-z0-9]?115@[A-Za-z0-9+/?-]+)/);
  if (!match) return null;
  let code = match[1].replace(/\?/g, '');
  if (code[0] !== 'v') code = 'v' + code.slice(1);
  return code;
}

function parsePercent(text) {
  const match = String(text || '').match(/([0-9]+(?:\.[0-9]+)?)\s*%/);
  if (!match) return 0;
  return Number(match[1]);
}

function parseOrder(text) {
  const match = String(text || '').match(/([IJLOSTZ]{7})/);
  return match ? match[1] : '';
}

function pageToRows(page) {
  let top = -1;
  for (let y = 0; y < 23; y++) {
    for (let x = 0; x < 10; x++) {
      if (page.field.at(x, y) !== '_') {
        if (y > top) top = y;
      }
    }
  }
  if (top < 0) return [];
  const rows = [];
  for (let y = 0; y <= top; y++) {
    let row = '';
    for (let x = 0; x < 10; x++) {
      row += page.field.at(x, y);
    }
    rows.push(row);
  }
  return rows;
}

function decodePages(value) {
  const code = normalizeFumen(value);
  if (!code) return [];
  try {
    return decoder.decode(code);
  } catch (_) {
    return [];
  }
}

function enrichEntry(entry, row) {
  const afterPages = decodePages(row[29]);
  if (afterPages.length >= 1) {
    entry.triggerRows = pageToRows(afterPages[0]);
  }
  if (afterPages.length >= 2) {
    entry.afterRows = pageToRows(afterPages[1]);
  }

  const merged = new Map();
  for (const idx of [16, 24]) {
    const pages = decodePages(row[idx]);
    for (const page of pages) {
      const rows = pageToRows(page);
      const order = parseOrder(page.comment || '');
      if (!order || rows.length === 0) continue;
      const rate = parsePercent(page.comment || '');
      const key = `${order}|${rows.join('/')}`;
      const prev = merged.get(key);
      if (!prev || rate > prev.rate) {
        merged.set(key, { order, rate, rows });
      }
    }
  }

  entry.pcSolutions = Array.from(merged.values()).sort((a, b) => {
    if (b.rate !== a.rate) return b.rate - a.rate;
    return a.order.localeCompare(b.order);
  });
}

function main() {
  const book = JSON.parse(fs.readFileSync(inputJSON, 'utf8'));
  const byKey = new Map();
  for (const entry of book.entries) {
    const key = `${entry.sheet}\t${entry.no}\t${String(entry.condition || '').trim()}`;
    byKey.set(key, entry);
    delete entry.triggerRows;
    delete entry.afterRows;
    delete entry.pcSolutions;
  }

  const wb = xlsx.readFile(workbookPath);
  for (const sheet of sheets) {
    const ws = wb.Sheets[sheet];
    if (!ws) continue;
    const rows = xlsx.utils.sheet_to_json(ws, { header: 1, raw: false });
    for (let i = 1; i < rows.length; i++) {
      const row = rows[i];
      if (!row || !row[15]) continue;
      const no = Number(row[0]);
      if (!Number.isFinite(no)) continue;
      const condition = String(row[8] || '').trim();
      const key = `${sheet}\t${no}\t${condition}`;
      const entry = byKey.get(key);
      if (!entry) continue;
      enrichEntry(entry, row);
    }
  }

  fs.writeFileSync(outputJSON, JSON.stringify(book));
}

main();
