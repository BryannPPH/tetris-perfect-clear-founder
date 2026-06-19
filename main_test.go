package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func advanceVisible(current byte, next []byte, hold byte, source string, stream []byte, used int) (byte, []byte, byte, int, bool) {
	if len(next) != 5 {
		return 0, nil, 0, used, false
	}
	consume := 0
	nextHold := hold
	switch source {
	case "ACTIVE":
		consume = 1
	case "HOLD_SWAP":
		if hold == 0 {
			return 0, nil, 0, used, false
		}
		consume = 1
		nextHold = current
	case "HOLD_EMPTY":
		if hold != 0 {
			return 0, nil, 0, used, false
		}
		consume = 2
		nextHold = current
	default:
		return 0, nil, 0, used, false
	}
	used += consume
	if used+6 > len(stream) {
		return 0, nil, 0, used, false
	}
	return stream[used], append([]byte{}, stream[used+1:used+6]...), nextHold, used, true
}

func TestSearchBestPrefersExactPerfectClear(t *testing.T) {
	rows, err := boardFromStrings([]string{
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"########..",
		"########..",
	})
	if err != nil {
		t.Fatalf("board parse failed: %v", err)
	}

	resp := SearchBest(rows, 'O', []byte{'I', 'J', 'L', 'S', 'T'}, 0, 10, 800, true)
	if !resp.Ok {
		t.Fatalf("expected ok response, got error: %s", resp.Error)
	}
	if resp.Move.Piece != "O" {
		t.Fatalf("expected O move, got %s", resp.Move.Piece)
	}
	if !resp.Move.PerfectClear {
		t.Fatalf("expected exact perfect clear move")
	}
}

func TestStateKeyIncludesResidue(t *testing.T) {
	s1 := State{Queue: []byte{'T', 'I'}, Residue: []byte{'J', 'L'}, Hold: 'O'}
	s2 := State{Queue: []byte{'T', 'I'}, Residue: []byte{'S', 'Z'}, Hold: 'O'}

	if stateKey(s1) == stateKey(s2) {
		t.Fatalf("stateKey should distinguish residue")
	}
}

func TestStateKeyIncludesCanHold(t *testing.T) {
	s1 := State{Queue: []byte{'T', 'I'}, Residue: []byte{'J', 'L'}, Hold: 'O', CanHold: true}
	s2 := State{Queue: []byte{'T', 'I'}, Residue: []byte{'J', 'L'}, Hold: 'O', CanHold: false}

	if stateKey(s1) == stateKey(s2) {
		t.Fatalf("stateKey should distinguish hold availability")
	}
}

func TestBookStateKeyIncludesCanHold(t *testing.T) {
	s1 := BookState{Queue: []byte{'T', 'I'}, Hold: 'O', CanHold: true}
	s2 := BookState{Queue: []byte{'T', 'I'}, Hold: 'O', CanHold: false}

	if bookStateKey(s1) == bookStateKey(s2) {
		t.Fatalf("bookStateKey should distinguish hold availability")
	}
}

func TestApplyCandidateHoldTransitions(t *testing.T) {
	rows := [BoardH]uint16{}
	activeState := State{Rows: rows, Queue: []byte{'T', 'I', 'L'}, Hold: 'O', CanHold: true}
	activeCand := Candidate{Move: Move{Source: "ACTIVE", Piece: "T"}, Rows: rows, Score: 0, Combo: -1, B2B: false}
	activeNext := applyCandidate(activeState, activeCand)
	if !activeNext.CanHold {
		t.Fatalf("expected hold to be available after active placement")
	}

	emptyState := State{Rows: rows, Queue: []byte{'T', 'I', 'L'}, Hold: 0, CanHold: true}
	emptyCand := Candidate{Move: Move{Source: "HOLD_EMPTY", Piece: "I"}, Rows: rows, Score: 0, Combo: -1, B2B: false}
	emptyNext := applyCandidate(emptyState, emptyCand)
	if emptyNext.CanHold {
		t.Fatalf("expected hold to be locked after hold-empty use")
	}
	if emptyNext.Hold != 'T' {
		t.Fatalf("expected hold-empty to store current piece, got %q", emptyNext.Hold)
	}

	swapState := State{Rows: rows, Queue: []byte{'T', 'I', 'L'}, Hold: 'O', CanHold: true}
	swapCand := Candidate{Move: Move{Source: "HOLD_SWAP", Piece: "O"}, Rows: rows, Score: 0, Combo: -1, B2B: false}
	swapNext := applyCandidate(swapState, swapCand)
	if swapNext.CanHold {
		t.Fatalf("expected hold to be locked after hold-swap use")
	}
	if swapNext.Hold != 'T' {
		t.Fatalf("expected hold-swap to move current piece into hold, got %q", swapNext.Hold)
	}
}

func TestApplyBookMoveHoldTransitions(t *testing.T) {
	rows := [BoardH]uint16{}
	base := BookState{Rows: rows, Queue: []byte{'T', 'I', 'L'}, Hold: 'O', CanHold: true}

	active := applyBookMove(base, Move{Source: "ACTIVE", Piece: "T"})
	if !active.CanHold {
		t.Fatalf("expected hold to stay available after active placement")
	}
	if active.Hold != 'O' {
		t.Fatalf("expected active placement to preserve hold piece, got %q", active.Hold)
	}

	empty := applyBookMove(BookState{Rows: rows, Queue: []byte{'T', 'I', 'L'}, Hold: 0, CanHold: true}, Move{Source: "HOLD_EMPTY", Piece: "I"})
	if empty.CanHold {
		t.Fatalf("expected hold to be locked after hold-empty use")
	}
	if empty.Hold != 'T' {
		t.Fatalf("expected hold-empty to store current piece, got %q", empty.Hold)
	}

	swap := applyBookMove(base, Move{Source: "HOLD_SWAP", Piece: "O"})
	if swap.CanHold {
		t.Fatalf("expected hold to be locked after hold-swap use")
	}
	if swap.Hold != 'T' {
		t.Fatalf("expected hold-swap to move current piece into hold, got %q", swap.Hold)
	}
}

func TestIndexHTMLUsesCorrectJAndLColors(t *testing.T) {
	if !strings.Contains(indexHTML, "--piece-j:#3ba7ff;") {
		t.Fatalf("expected J piece color to be blue")
	}
	if !strings.Contains(indexHTML, "--piece-l:#ff9f3f;") {
		t.Fatalf("expected L piece color to be orange")
	}
}

func BenchmarkSearchBestOpeningPC(b *testing.B) {
	rows, err := boardFromStrings([]string{
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
		"..........",
	})
	if err != nil {
		b.Fatalf("board parse failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp := SearchBest(rows, 'T', []byte{'S', 'Z', 'I', 'L', 'O'}, 0, 9, 160, true)
		if !resp.Ok {
			b.Fatalf("unexpected error: %s", resp.Error)
		}
	}
}

func BenchmarkSearchBestWithBookHit(b *testing.B) {
	var rows [BoardH]uint16
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp := SearchBestWithBook(rows, 'Z', []byte{'T', 'O', 'J', 'S', 'I'}, 0, 9, 160, true, 0, "", "")
		if !resp.Ok {
			b.Fatalf("unexpected error: %s", resp.Error)
		}
		if resp.ExploredStates != 0 {
			b.Fatalf("expected opening-book response, explored=%d", resp.ExploredStates)
		}
	}
}

func TestOpeningBookHasPlayableOrder(t *testing.T) {
	var rows [BoardH]uint16
	for order, ids := range openingBook.ByOrder {
		if len(ids) == 0 {
			continue
		}
		current := order[0]
		next := []byte(order[1:6])
		for _, id := range ids {
			entry := openingBook.Entries[id]
			if entry == nil {
				continue
			}
			resp, ok := tryOpeningBook(rows, current, next, 0, entry.ID, order, "")
			if ok {
				t.Logf("opening order %s uses entry %d", order, id)
				if !resp.Ok {
					t.Fatalf("expected ok response for order %s", order)
				}
				if resp.ExploredStates != 0 {
					t.Fatalf("expected opening-book response, explored=%d", resp.ExploredStates)
				}
				if resp.OpeningOrder != order {
					t.Fatalf("expected opening order %s, got %q", order, resp.OpeningOrder)
				}
				return
			}
		}
	}
	t.Fatalf("expected at least one playable opening-book order")
}

func TestOpeningBookHasExactPCContinuation(t *testing.T) {
	for _, entry := range openingBook.Entries {
		if entry == nil || !entry.HasAfterRows {
			continue
		}
		for order := range entry.PCSolutions {
			if len(order) != 7 {
				continue
			}
			resp, ok := pcBookResponse(entry.AfterRows, order[0], []byte(order[1:6]), 0, entry, "TESTING", order)
			if !ok {
				continue
			}
			if !resp.Response.Ok {
				t.Fatalf("expected ok PC response for entry %d order %s", entry.ID, order)
			}
			if resp.Response.ExploredStates <= 0 {
				t.Fatalf("expected exact-PC search work for entry %d order %s", entry.ID, order)
			}
			wantNext := removePieceFromOrder(order, resp.Response.Move.Piece[0])
			if resp.Response.FinishOrder != wantNext {
				t.Fatalf("unexpected finish order %q for %s, want %q", resp.Response.FinishOrder, order, wantNext)
			}
			return
		}
	}
	t.Fatalf("expected at least one exact PC continuation from workbook data")
}

func TestBuildPCSolutionSample(t *testing.T) {
	var raw RawOpeningBook
	if err := json.Unmarshal(openingBookJSON, &raw); err != nil {
		t.Fatalf("unmarshal raw book: %v", err)
	}
	for _, entry := range raw.Entries {
		if len(entry.PCSolutions) == 0 {
			continue
		}
		sol, err := buildPCSolution(entry.PCSolutions[0])
		if err != nil {
			t.Fatalf("buildPCSolution failed for entry %d %s-%d order %s: %v rows=%v", entry.ID, entry.Sheet, entry.No, entry.PCSolutions[0].Order, err, entry.PCSolutions[0].Rows)
		}
		if countCells(sol.BaseRows) == 0 {
			t.Fatalf("expected base rows from X cells")
		}
		return
	}
	t.Fatalf("no raw PC solution found")
}

func TestBuildOpeningEntrySample(t *testing.T) {
	var raw RawOpeningBook
	if err := json.Unmarshal(openingBookJSON, &raw); err != nil {
		t.Fatalf("unmarshal raw book: %v", err)
	}
	for _, entry := range raw.Entries {
		if len(entry.PCSolutions) == 0 {
			continue
		}
		built, err := buildOpeningEntry(entry)
		if err != nil {
			continue
		}
		t.Logf("sample buildable entry=%d sheet=%c no=%d rawpc=%d", built.ID, built.FirstPiece, built.No, len(entry.PCSolutions))
		if len(built.PCSolutions) == 0 {
			t.Fatalf("expected PC solutions on built entry")
		}
		return
	}
	t.Fatalf("no buildable enriched raw entry found")
}

func TestOpeningBookFullFlowSample(t *testing.T) {
	for _, entry := range openingBook.Entries {
		if entry == nil || entry.Trigger == nil || len(entry.PCSolutions) == 0 {
			continue
		}
		openingOrder := ""
		for order, ids := range openingBook.ByOrder {
			for _, id := range ids {
				if id == entry.ID {
					openingOrder = order
					break
				}
			}
			if openingOrder != "" {
				break
			}
		}
		if openingOrder == "" {
			continue
		}
		for finishOrder := range entry.PCSolutions {
			stream := append([]byte(openingOrder), entry.Trigger.Piece)
			stream = append(stream, []byte(finishOrder)...)
			stream = append(stream, AllPieces...)
			current := stream[0]
			next := append([]byte{}, stream[1:6]...)
			hold := byte(0)
			used := 0
			var rows [BoardH]uint16
			openingID, stateOpeningOrder, stateFinishOrder := 0, "", ""
			for step := 0; step < 20; step++ {
				resp := SearchBestWithBook(rows, current, next, hold, 9, 220, true, openingID, stateOpeningOrder, stateFinishOrder)
				if !resp.Ok {
					goto nextCase
				}
				if resp.Move.Piece == "" {
					goto nextCase
				}
				openingID = resp.OpeningID
				stateOpeningOrder = resp.OpeningOrder
				stateFinishOrder = resp.FinishOrder
				boardAfter, err := boardFromStrings(resp.BoardAfter)
				if err != nil {
					t.Fatalf("bad boardAfter: %v", err)
				}
				rows = boardAfter
				if isEmpty(rows) {
					t.Logf("sample success entry=%d opening=%s finish=%s steps=%d", entry.ID, openingOrder, finishOrder, step+1)
					return
				}
				var ok bool
				current, next, hold, used, ok = advanceVisible(current, next, hold, resp.Move.Source, stream, used)
				if !ok {
					goto nextCase
				}
			}
		nextCase:
		}
	}
	t.Fatalf("no full-flow sample reached perfect clear")
}

func TestOpeningBookRejectsFloatingTemplateMove(t *testing.T) {
	st := BookState{}
	variant := OpeningVariant{
		Placements: map[byte]Move{
			'O': {
				Piece:    "O",
				Rotation: 0,
				X:        0,
				Y:        10,
				Cells: []Point{
					{X: 0, Y: 10}, {X: 1, Y: 10},
					{X: 0, Y: 11}, {X: 1, Y: 11},
				},
			},
		},
	}
	if _, ok := openingCandidateForPiece(st, 'O', "ACTIVE", variant); ok {
		t.Fatalf("expected floating opening-book template to be rejected")
	}
}

func TestTriggerMoveRejectsFloatingTemplateMove(t *testing.T) {
	template := Move{
		Piece:    "O",
		Rotation: 0,
		X:        0,
		Y:        10,
	}
	if _, ok := triggerMoveFromTemplate([BoardH]uint16{}, template, "ACTIVE", [BoardH]uint16{}); ok {
		t.Fatalf("expected floating trigger template to be rejected")
	}
}

func TestOpeningBookStats(t *testing.T) {
	reachable := map[int]bool{}
	for _, ids := range openingBook.ByOrder {
		for _, id := range ids {
			reachable[id] = true
		}
	}
	total := len(openingBook.Entries)
	withTrigger := 0
	withPC := 0
	reachableCount := 0
	reachableWithTrigger := 0
	reachableWithPC := 0
	for id, entry := range openingBook.Entries {
		if entry.Trigger != nil {
			withTrigger++
		}
		if len(entry.PCSolutions) > 0 {
			withPC++
		}
		if reachable[id] {
			reachableCount++
			if entry.Trigger != nil {
				reachableWithTrigger++
			}
			if len(entry.PCSolutions) > 0 {
				reachableWithPC++
			}
		}
	}
	t.Logf("entries total=%d reachable=%d withTrigger=%d withPC=%d reachableWithTrigger=%d reachableWithPC=%d", total, reachableCount, withTrigger, withPC, reachableWithTrigger, reachableWithPC)
}

func TestRawOpeningBuildErrors(t *testing.T) {
	var raw RawOpeningBook
	if err := json.Unmarshal(openingBookJSON, &raw); err != nil {
		t.Fatalf("unmarshal raw book: %v", err)
	}
	errs := map[string]int{}
	okCount := 0
	for _, entry := range raw.Entries {
		if _, err := buildOpeningEntry(entry); err != nil {
			errs[err.Error()]++
			continue
		}
		okCount++
	}
	t.Logf("buildable=%d failed=%d uniqueErrs=%d", okCount, len(raw.Entries)-okCount, len(errs))
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range errs {
		list = append(list, kv{k, v})
	}
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].v > list[i].v {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	for i := 0; i < len(list) && i < 12; i++ {
		t.Logf("%d x %s", list[i].v, list[i].k)
	}
}
