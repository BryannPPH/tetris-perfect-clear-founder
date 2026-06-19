# TETR.IO Go Smart Advisor

Offline GUI/practice tool for TETR.IO Blitz placement planning.

This version is written in **Go** and uses a local web GUI. It does not need heavy GUI libraries such as Tkinter/Fyne. The Go program runs a local server at `http://127.0.0.1:8787`, then opens the interface in your browser.

## Features

- Clickable 20x10 board.
- Input current piece and exactly 5 next pieces.
- Optional hold piece.
- Uses current + 5 next as the 6 visible pieces.
- After the visible pieces are consumed, it branches using an inferred **7-bag randomizer residue**.
- Beam-search / dynamic-programming-style lookahead.
- PC/DPC-focused heuristic mode for perfect clear planning.
- Scores Single, Double, Triple, Quad/Tetris, combo, B2B, and Perfect Clear approximately.
- Shows the recommended first placement and a planned line of future placements.

## Run on Mac

If you have Go installed:

```bash
go run .
```

Or double-click/run:

```bash
./run_mac.command
```

If macOS blocks it, run:

```bash
chmod +x run_mac.command
./run_mac.command
```

## Run with included binary

For Intel Mac:

```bash
./bin/tetrio-go-smart-advisor-darwin-amd64
```

For Apple Silicon Mac:

```bash
./bin/tetrio-go-smart-advisor-darwin-arm64
```

Then open:

```text
http://127.0.0.1:8787
```

## How to use

1. Click board cells to match your current TETR.IO board.
2. Input the current piece, for example `T`.
3. Input the 5 next pieces, for example `SZILO`.
4. Optionally input hold piece.
5. Set depth and beam width.
6. Click **Recommend Placement**.
7. Green cells show where to place the piece.
8. Click **Apply Recommendation** if you want to update the simulated board.

## Important notes

This is an offline practice/analysis tool, not a live gameplay bot. It does not read your screen, press keys, or submit scores.

The rotation model is simplified and does not fully implement TETR.IO/SRS wall-kick pathfinding. The strategy search is strong enough for a makalah prototype and manual practice, but it is not guaranteed to be globally optimal.

## Algorithm summary

The advisor evaluates candidate placements using:

- immediate score,
- board holes,
- bumpiness,
- maximum height,
- Tetris well quality,
- perfect clear potential,
- B2B readiness,
- visible 6-piece queue,
- inferred seven-bag residue after the preview.

The search keeps only the best states at every depth using beam pruning, making it much faster than exhaustive search.
