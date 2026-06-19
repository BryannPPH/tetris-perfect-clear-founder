package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"runtime"
	"sort"
	"strings"
	"sync"
)

//go:embed opening_book.json
var openingBookJSON []byte

type RawOpeningBook struct {
	Entries []RawOpeningEntry `json:"entries"`
	ByOrder map[string][]int  `json:"byOrder"`
}

type RawPCSolution struct {
	Order string   `json:"order"`
	Rate  float64  `json:"rate"`
	Rows  []string `json:"rows"`
}

type RawOpeningEntry struct {
	ID             int             `json:"id"`
	Sheet          string          `json:"sheet"`
	No             int             `json:"no"`
	Condition      string          `json:"condition"`
	PCRate         float64         `json:"pcRate"`
	SetupRate      float64         `json:"setupRate"`
	DTETPCRate     float64         `json:"dtetpcRate"`
	DDPCRate       float64         `json:"ddpcRate"`
	DSPCRate       float64         `json:"dspcRate"`
	Score          float64         `json:"score"`
	SetupRows      []string        `json:"setupRows"`
	Placements     map[string]Move `json:"placements,omitempty"`
	PlacementSteps []Move          `json:"placementSteps,omitempty"`
	TriggerRows    []string        `json:"triggerRows,omitempty"`
	AfterRows      []string        `json:"afterRows,omitempty"`
	PCSolutions    []RawPCSolution `json:"pcSolutions,omitempty"`
}

type OpeningBook struct {
	Entries map[int]*OpeningEntry
	ByOrder map[string][]int
}

type OpeningEntry struct {
	ID           int
	FirstPiece   byte
	No           int
	Condition    string
	PCRate       float64
	SetupRate    float64
	DTETPCRate   float64
	DDPCRate     float64
	DSPCRate     float64
	Score        float64
	TargetRows   [BoardH]uint16
	Variants     []OpeningVariant
	HasAfterRows bool
	AfterRows    [BoardH]uint16
	Trigger      *TriggerTemplate
	PCSolutions  map[string][]PCSolution
}

type OpeningVariant struct {
	Placements map[byte]Move
	Steps      []Move
}

type TriggerTemplate struct {
	Piece byte
	Move  Move
}

type PCSolution struct {
	Order    string
	Rate     float64
	BaseRows [BoardH]uint16
}

type BookState struct {
	Rows    [BoardH]uint16
	Queue   []byte
	Hold    byte
	CanHold bool
	Path    []Move
}

type bookProposal struct {
	Response AdvisorResponse
	Phase    int
	Rank     float64
}

var openingBook OpeningBook
var allSevenBagOrders []string

func init() {
	if err := loadOpeningBook(); err != nil {
		log.Printf("opening book disabled: %v", err)
	}
}

func loadOpeningBook() error {
	var raw RawOpeningBook
	if err := json.Unmarshal(openingBookJSON, &raw); err != nil {
		return err
	}

	book := OpeningBook{
		Entries: map[int]*OpeningEntry{},
		ByOrder: map[string][]int{},
	}

	for _, re := range raw.Entries {
		entry, err := buildOpeningEntry(re)
		if err != nil {
			continue
		}
		book.Entries[entry.ID] = entry
	}

	if len(raw.ByOrder) > 0 {
		book.ByOrder = filterRawByOrder(raw.ByOrder, book.Entries)
	} else {
		book.ByOrder = rebuildOpeningOrderIndex(book.Entries)
	}

	for order, ids := range book.ByOrder {
		sort.SliceStable(ids, func(i, j int) bool {
			a := book.Entries[ids[i]]
			b := book.Entries[ids[j]]
			if a == nil || b == nil {
				return ids[i] < ids[j]
			}
			if a.Score != b.Score {
				return a.Score > b.Score
			}
			if a.PCRate != b.PCRate {
				return a.PCRate > b.PCRate
			}
			if a.SetupRate != b.SetupRate {
				return a.SetupRate > b.SetupRate
			}
			return a.ID < b.ID
		})
		book.ByOrder[order] = ids
	}

	if len(book.Entries) == 0 {
		return fmt.Errorf("no valid opening entries")
	}
	openingBook = book
	return nil
}

func filterRawByOrder(raw map[string][]int, entries map[int]*OpeningEntry) map[string][]int {
	out := map[string][]int{}
	for order, ids := range raw {
		if len(order) != 7 {
			continue
		}
		for _, id := range ids {
			if entries[id] != nil {
				out[order] = append(out[order], id)
			}
		}
	}
	return out
}

func rebuildOpeningOrderIndex(entries map[int]*OpeningEntry) map[string][]int {
	if len(allSevenBagOrders) == 0 {
		allSevenBagOrders = enumerateSevenBagOrders()
	}
	out := map[string][]int{}
	type task struct {
		id    int
		entry *OpeningEntry
	}
	var tasks []task
	for id, entry := range entries {
		if entry == nil || len(entry.Variants) == 0 {
			continue
		}
		if len(entry.Variants[0].Placements) != 7 {
			continue
		}
		tasks = append(tasks, task{id: id, entry: entry})
	}
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > len(tasks) {
		workers = len(tasks)
	}
	if workers < 1 {
		return out
	}

	taskCh := make(chan task)
	type result struct {
		order string
		id    int
	}
	resultCh := make(chan result, 1024)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var empty [BoardH]uint16
			for t := range taskCh {
				for _, order := range allSevenBagOrders {
					state := BookState{
						Rows:    empty,
						Queue:   []byte(setupOrderForEntry(order, t.entry)),
						Hold:    0,
						CanHold: true,
					}
					if openingEntryPlayable(state, t.entry) {
						resultCh <- result{order: order, id: t.id}
					}
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()
	go func() {
		for _, t := range tasks {
			taskCh <- t
		}
		close(taskCh)
	}()

	for r := range resultCh {
		out[r.order] = append(out[r.order], r.id)
	}
	return out
}

func openingEntryPlayable(st BookState, entry *OpeningEntry) bool {
	for _, variant := range entry.Variants {
		if _, ok := searchOpeningPlan(st, entry.TargetRows, variant, map[string]bool{}); ok {
			return true
		}
	}
	return false
}

func enumerateSevenBagOrders() []string {
	pieces := append([]byte{}, AllPieces...)
	var out []string
	var rec func(int)
	rec = func(i int) {
		if i == len(pieces) {
			out = append(out, string(append([]byte{}, pieces...)))
			return
		}
		for j := i; j < len(pieces); j++ {
			pieces[i], pieces[j] = pieces[j], pieces[i]
			rec(i + 1)
			pieces[i], pieces[j] = pieces[j], pieces[i]
		}
	}
	rec(0)
	return out
}

func buildOpeningEntry(raw RawOpeningEntry) (*OpeningEntry, error) {
	if len(raw.Sheet) != 1 {
		return nil, fmt.Errorf("bad sheet %q", raw.Sheet)
	}

	targetRows, pieceCells, _, err := openingTemplateFromRows(raw.SetupRows)
	if err != nil {
		return nil, err
	}
	var variants []OpeningVariant
	if len(raw.PlacementSteps) > 0 || len(raw.Placements) > 0 {
		steps := normalizeRawPlacementSteps(raw.PlacementSteps, raw.Placements)
		if len(steps) == 0 {
			return nil, fmt.Errorf("no placement steps")
		}
		variants = []OpeningVariant{{Placements: mapFromSteps(steps), Steps: steps}}
	} else {
		variants, err = openingVariantsFromCells(raw.Sheet[0], pieceCells)
		if err != nil {
			return nil, err
		}
	}

	pcSolutions := map[string][]PCSolution{}
	var inferredAfter [BoardH]uint16
	hasAfter := false
	for _, rawSol := range raw.PCSolutions {
		sol, err := buildPCSolution(rawSol)
		if err != nil {
			continue
		}
		pcSolutions[sol.Order] = append(pcSolutions[sol.Order], sol)
		if !hasAfter {
			inferredAfter = sol.BaseRows
			hasAfter = true
		}
	}

	for order, sols := range pcSolutions {
		sort.SliceStable(sols, func(i, j int) bool {
			return sols[i].Rate > sols[j].Rate
		})
		pcSolutions[order] = sols
	}

	var afterRows [BoardH]uint16
	hasExplicitAfter := false
	if len(raw.AfterRows) > 0 {
		afterRows, _, _, err = openingTemplateFromRows(raw.AfterRows)
		if err == nil {
			hasExplicitAfter = true
		}
	}
	if !hasExplicitAfter && hasAfter {
		afterRows = inferredAfter
		hasExplicitAfter = true
	}

	var trigger *TriggerTemplate
	if len(raw.TriggerRows) > 0 && hasExplicitAfter {
		trigger, _ = buildTriggerTemplate(targetRows, afterRows, raw.TriggerRows)
	}

	return &OpeningEntry{
		ID:           raw.ID,
		FirstPiece:   raw.Sheet[0],
		No:           raw.No,
		Condition:    raw.Condition,
		PCRate:       raw.PCRate,
		SetupRate:    raw.SetupRate,
		DTETPCRate:   raw.DTETPCRate,
		DDPCRate:     raw.DDPCRate,
		DSPCRate:     raw.DSPCRate,
		Score:        raw.Score,
		TargetRows:   targetRows,
		Variants:     variants,
		HasAfterRows: hasExplicitAfter,
		AfterRows:    afterRows,
		Trigger:      trigger,
		PCSolutions:  pcSolutions,
	}, nil
}

func buildPCSolution(raw RawPCSolution) (PCSolution, error) {
	_, _, xCells, err := openingTemplateFromRows(raw.Rows)
	if err != nil {
		return PCSolution{}, err
	}
	return PCSolution{
		Order:    raw.Order,
		Rate:     raw.Rate,
		BaseRows: rowsFromPoints(xCells),
	}, nil
}

func buildTriggerTemplate(setupRows, afterRows [BoardH]uint16, triggerRows []string) (*TriggerTemplate, error) {
	triggerOcc, pieceCells, _, err := openingTemplateFromRows(triggerRows)
	if err != nil {
		return nil, err
	}

	var added []Point
	pointPiece := map[Point]byte{}
	for piece, cells := range pieceCells {
		for _, c := range cells {
			pointPiece[c] = piece
		}
	}

	for y := 0; y < BoardH; y++ {
		addedBits := triggerOcc[y] &^ setupRows[y]
		for x := 0; x < BoardW; x++ {
			if addedBits&(1<<x) == 0 {
				continue
			}
			added = append(added, Point{X: x, Y: y})
		}
	}
	if len(added) != 4 {
		return nil, fmt.Errorf("trigger move expected 4 added cells, got %d", len(added))
	}

	piece := byte(0)
	for _, c := range added {
		cp := pointPiece[c]
		if cp == 0 {
			return nil, fmt.Errorf("trigger move missing colored cell at %+v", c)
		}
		if piece == 0 {
			piece = cp
		} else if piece != cp {
			return nil, fmt.Errorf("trigger move spans multiple pieces")
		}
	}
	mv, err := inferPlacementFromCells(piece, added)
	if err != nil {
		return nil, err
	}
	rot := Pieces[piece][mv.Rotation]
	got, _, _, _ := placeAndClear(setupRows, rot, mv.X, mv.Y)
	if !rowsEqual(got, afterRows) {
		return nil, fmt.Errorf("trigger move does not land on after rows")
	}
	return &TriggerTemplate{Piece: piece, Move: mv}, nil
}

func openingTemplateFromRows(rows []string) ([BoardH]uint16, map[byte][]Point, []Point, error) {
	var out [BoardH]uint16
	cells := map[byte][]Point{}
	var xCells []Point
	for yb, row := range rows {
		if yb >= BoardH {
			return out, nil, nil, fmt.Errorf("opening rows too tall")
		}
		if len(row) != BoardW {
			return out, nil, nil, fmt.Errorf("opening row width mismatch")
		}
		boardY := BoardH - 1 - yb
		for x := 0; x < BoardW; x++ {
			ch := row[x]
			if ch == '_' {
				continue
			}
			out[boardY] |= 1 << x
			switch {
			case ch == 'X':
				xCells = append(xCells, Point{X: x, Y: boardY})
			case strings.IndexByte("IJLOSTZ", ch) >= 0:
				cells[ch] = append(cells[ch], Point{X: x, Y: boardY})
			default:
				return out, nil, nil, fmt.Errorf("unexpected cell %q", ch)
			}
		}
	}
	return out, cells, xCells, nil
}

func rowsFromPoints(points []Point) [BoardH]uint16 {
	var out [BoardH]uint16
	for _, p := range points {
		if p.X < 0 || p.X >= BoardW || p.Y < 0 || p.Y >= BoardH {
			continue
		}
		out[p.Y] |= 1 << p.X
	}
	return out
}

func openingVariantsFromCells(sheet byte, pieceCells map[byte][]Point) ([]OpeningVariant, error) {
	base := map[byte]Move{}
	for _, p := range AllPieces {
		if p == sheet || (sheet != 'T' && p == 'T') {
			continue
		}
		mv, err := inferPlacementFromCells(p, pieceCells[p])
		if err != nil {
			return nil, err
		}
		base[p] = mv
	}

	if sheet == 'T' {
		mv, err := inferPlacementFromCells('T', pieceCells['T'])
		if err != nil {
			return nil, err
		}
		base['T'] = mv
		return []OpeningVariant{{Placements: copyPlacementMap(base), Steps: stepsFromMap(base)}}, nil
	}

	dup := pieceCells[sheet]
	if len(dup) != 8 {
		return nil, fmt.Errorf("sheet %c expected 8 dup cells, got %d", sheet, len(dup))
	}
	sheetMoves := placementCandidatesFromSuperset(sheet, dup)
	if len(sheetMoves) == 0 {
		return nil, fmt.Errorf("no starter candidates for sheet %c", sheet)
	}
	var variants []OpeningVariant
	for _, sm := range sheetMoves {
		m := copyPlacementMap(base)
		m[sheet] = sm
		variants = append(variants, OpeningVariant{Placements: m, Steps: stepsFromMap(m)})
	}
	if len(variants) == 0 {
		return nil, fmt.Errorf("no variants for sheet %c", sheet)
	}
	return variants, nil
}

func copyPlacementMap(in map[byte]Move) map[byte]Move {
	out := make(map[byte]Move, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func inferPlacementFromCells(piece byte, cells []Point) (Move, error) {
	if len(cells) != 4 {
		return Move{}, fmt.Errorf("piece %c has %d cells", piece, len(cells))
	}
	set := map[Point]bool{}
	minX, minY := BoardW, BoardH
	for _, c := range cells {
		set[c] = true
		if c.X < minX {
			minX = c.X
		}
		if c.Y < minY {
			minY = c.Y
		}
	}
	for ri, rot := range Pieces[piece] {
		match := true
		var translated []Point
		for _, p := range rot {
			q := Point{X: minX + p.X, Y: minY + p.Y}
			if !set[q] {
				match = false
				break
			}
			translated = append(translated, q)
		}
		if match {
			return Move{Piece: string(piece), Rotation: ri, X: minX, Y: minY, Cells: translated}, nil
		}
	}
	return Move{}, fmt.Errorf("could not infer placement for %c", piece)
}

func placementCandidatesFromSuperset(piece byte, sup []Point) []Move {
	set := map[Point]bool{}
	for _, c := range sup {
		set[c] = true
	}
	seen := map[string]bool{}
	var out []Move
	for ri, rot := range Pieces[piece] {
		for _, anchor := range sup {
			for _, rp := range rot {
				x := anchor.X - rp.X
				y := anchor.Y - rp.Y
				var cells []Point
				ok := true
				for _, p := range rot {
					q := Point{X: x + p.X, Y: y + p.Y}
					if !set[q] {
						ok = false
						break
					}
					cells = append(cells, q)
				}
				if !ok {
					continue
				}
				key := fmt.Sprintf("%c:%d:%d:%d", piece, ri, x, y)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, Move{Piece: string(piece), Rotation: ri, X: x, Y: y, Cells: cells})
			}
		}
	}
	return out
}

func movesDisjoint(a, b Move) bool {
	set := map[Point]bool{}
	for _, c := range a.Cells {
		set[c] = true
	}
	for _, c := range b.Cells {
		if set[c] {
			return false
		}
	}
	return true
}

func movesCoverCells(a, b Move, sup []Point) bool {
	if len(a.Cells)+len(b.Cells) != len(sup) {
		return false
	}
	set := map[Point]bool{}
	for _, c := range a.Cells {
		set[c] = true
	}
	for _, c := range b.Cells {
		set[c] = true
	}
	if len(set) != len(sup) {
		return false
	}
	for _, c := range sup {
		if !set[c] {
			return false
		}
	}
	return true
}

func SearchBestWithBook(rows [BoardH]uint16, current byte, next []byte, hold byte, depth, beam int, pcMode bool, openingID int, openingOrder, finishOrder string) AdvisorResponse {
	if pcMode {
		if resp, ok := tryOpeningBook(rows, current, next, hold, openingID, openingOrder, finishOrder); ok {
			return resp
		}
	}
	return SearchBest(rows, current, next, hold, depth, beam, pcMode)
}

func tryOpeningBook(rows [BoardH]uint16, current byte, next []byte, hold byte, openingID int, openingOrder, finishOrder string) (AdvisorResponse, bool) {
	if len(openingBook.Entries) == 0 {
		return AdvisorResponse{}, false
	}

	candidates := gatherOpeningCandidates(rows, current, next, hold, openingID, openingOrder)
	if len(candidates) == 0 {
		return AdvisorResponse{}, false
	}

	var best *bookProposal
	for _, cand := range candidates {
		prop, ok := bookProposalForEntry(rows, current, next, hold, cand.entry, cand.order, finishOrder)
		if !ok {
			continue
		}
		if best == nil || prop.Rank > best.Rank {
			cp := prop
			best = &cp
		}
	}
	if best == nil {
		return AdvisorResponse{}, false
	}
	return best.Response, true
}

type openingCandidate struct {
	entry *OpeningEntry
	order string
}

func gatherOpeningCandidates(rows [BoardH]uint16, current byte, next []byte, hold byte, openingID int, openingOrder string) []openingCandidate {
	var out []openingCandidate
	seen := map[int]bool{}
	add := func(id int, order string) {
		if seen[id] {
			return
		}
		entry := openingBook.Entries[id]
		if entry == nil {
			return
		}
		seen[id] = true
		out = append(out, openingCandidate{entry: entry, order: order})
	}

	if openingID > 0 && len(openingOrder) == 7 {
		add(openingID, openingOrder)
	}
	if len(openingOrder) == 7 {
		for _, id := range openingBook.ByOrder[openingOrder] {
			add(id, openingOrder)
		}
	}
	if len(out) > 0 {
		return out
	}

	if !isEmpty(rows) || hold != 0 {
		return nil
	}
	order, ok := inferOpeningOrder(current, next)
	if !ok {
		return nil
	}
	for _, id := range openingBook.ByOrder[order] {
		add(id, order)
	}
	return out
}

func inferOpeningOrder(current byte, next []byte) (string, bool) {
	visible := append([]byte{current}, next...)
	if len(visible) != 6 || len(uniqueBytes(visible)) != 6 {
		return "", false
	}
	residue, _ := inferResidueFromVisible(visible)
	if len(residue) != 1 {
		return "", false
	}
	return string(append(visible, residue[0])), true
}

func bookProposalForEntry(rows [BoardH]uint16, current byte, next []byte, hold byte, entry *OpeningEntry, openingOrder, finishOrder string) (bookProposal, bool) {
	if finishOrder != "" {
		if prop, ok := pcBookResponse(rows, current, next, hold, entry, openingOrder, finishOrder); ok {
			return prop, true
		}
	}
	if entry.HasAfterRows {
		if order, ok := inferOpeningOrder(current, next); ok {
			if prop, ok := pcBookResponse(rows, current, next, hold, entry, openingOrder, order); ok {
				return prop, true
			}
		}
	}
	if entry.Trigger != nil && rowsEqual(rows, entry.TargetRows) {
		if prop, ok := triggerBookResponse(rows, current, next, hold, entry, openingOrder, finishOrder); ok {
			return prop, true
		}
	}
	if prop, ok := setupBookResponse(rows, current, next, hold, entry, openingOrder); ok {
		return prop, true
	}
	return bookProposal{}, false
}

func setupBookResponse(rows [BoardH]uint16, current byte, next []byte, hold byte, entry *OpeningEntry, order string) (bookProposal, bool) {
	for _, variant := range entry.Variants {
		placed, complete, ok := openingSubsetStatus(rows, entry.TargetRows, variant)
		if !ok || complete {
			continue
		}
		setupOrder := setupOrderForEntry(order, entry)
		queue := buildExactQueue(setupOrder, current, next, hold, placed)
		state := BookState{Rows: rows, Queue: queue, Hold: hold, CanHold: true}
		plan, ok := searchOpeningPlan(state, entry.TargetRows, variant, map[string]bool{})
		if !ok || len(plan) == 0 {
			continue
		}
		after := applyBookMove(state, plan[0])
		resp := AdvisorResponse{
			Ok:             true,
			Move:           plan[0],
			BoardAfter:     rowsToStrings(after.Rows),
			BoardPreview:   overlayStrings(rows, plan[0].Cells),
			Plan:           plan,
			BagInfo:        fmt.Sprintf("Opening book entry %c-%d on first-bag order %s; setup queue model %s.", entry.FirstPiece, entry.No, order, setupOrder),
			Reason:         fmt.Sprintf("Workbook setup phase. Entry #%d matches the current partial stack. PC %.2f%%, setup %.2f%%.", entry.No, entry.PCRate, entry.SetupRate),
			ExploredStates: 0,
			OpeningID:      entry.ID,
			OpeningOrder:   order,
		}
		return bookProposal{Response: resp, Phase: 1, Rank: 1e9 + entry.Score}, true
	}
	return bookProposal{}, false
}

func triggerBookResponse(rows [BoardH]uint16, current byte, next []byte, hold byte, entry *OpeningEntry, openingOrder, finishOrder string) (bookProposal, bool) {
	state := BookState{
		Rows:    rows,
		Queue:   append([]byte{current}, next...),
		Hold:    hold,
		CanHold: true,
	}
	mv, ok := triggerCandidate(state, entry.Trigger, entry.AfterRows)
	if !ok {
		return bookProposal{}, false
	}
	after := applyBookMove(state, mv)
	resp := AdvisorResponse{
		Ok:             true,
		Move:           mv,
		BoardAfter:     rowsToStrings(after.Rows),
		BoardPreview:   overlayStrings(rows, mv.Cells),
		Plan:           []Move{mv},
		BagInfo:        fmt.Sprintf("Opening book trigger for entry %c-%d.", entry.FirstPiece, entry.No),
		Reason:         fmt.Sprintf("Workbook trigger phase. The setup is complete; this exact %c placement converts it into the recorded DPC/PC continuation board.", entry.Trigger.Piece),
		ExploredStates: 0,
		OpeningID:      entry.ID,
		OpeningOrder:   openingOrder,
		FinishOrder:    finishOrder,
	}
	return bookProposal{Response: resp, Phase: 2, Rank: 2e9 + entry.Score}, true
}

func pcBookResponse(rows [BoardH]uint16, current byte, next []byte, hold byte, entry *OpeningEntry, openingOrder, finishOrder string) (bookProposal, bool) {
	if !entry.HasAfterRows || finishOrder == "" {
		return bookProposal{}, false
	}
	fullOrder := finishOrder
	rate := 100.0
	if len(finishOrder) == 7 {
		solutions := entry.PCSolutions[finishOrder]
		if len(solutions) == 0 {
			return bookProposal{}, false
		}
		if !rowsEqual(rows, entry.AfterRows) {
			return bookProposal{}, false
		}
		rate = solutions[0].Rate
	} else if !looksLikeRemainingOrder(finishOrder) {
		return bookProposal{}, false
	}

	queue := buildExactQueue(finishOrder, current, next, hold, map[byte]int{})
	if len(queue) == 0 {
		return bookProposal{}, false
	}

	init := State{
		Rows:    rows,
		Queue:   queue,
		Residue: []byte{},
		Hold:    hold,
		CanHold: true,
		Score:   0,
		Combo:   -1,
		B2B:     false,
	}
	best, ok, explored := perfectClearSearchWithLimit(init, len(finishOrder), 720, 640)
	if !ok || best.Path == nil {
		return bookProposal{}, false
	}
	plan := pathToMoves(best.Path, best.PathLen)
	first := plan[0]
	after := replayFirst(rows, init, first, true)
	nextFinishOrder := removePieceFromOrder(fullOrder, first.Piece[0])
	resp := AdvisorResponse{
		Ok:             true,
		Move:           first,
		BoardAfter:     rowsToStrings(after.Rows),
		BoardPreview:   overlayStrings(rows, first.Cells),
		Plan:           plan,
		BagInfo:        fmt.Sprintf("Workbook PC phase for entry %c-%d on exact order %s.", entry.FirstPiece, entry.No, fullOrder),
		Reason:         fmt.Sprintf("Workbook exact PC continuation. The residue matches the recorded DPC board, and order %s has a stored continuation (%.1f%%).", fullOrder, rate),
		ExploredStates: explored,
		OpeningID:      entry.ID,
		OpeningOrder:   openingOrder,
		FinishOrder:    nextFinishOrder,
	}
	return bookProposal{Response: resp, Phase: 3, Rank: 3e9 + rate*1e6 + entry.Score}, true
}

func normalizeRawPlacementSteps(steps []Move, placements map[string]Move) []Move {
	var out []Move
	if len(steps) > 0 {
		for _, mv := range steps {
			if len(mv.Piece) == 0 {
				continue
			}
			out = append(out, mv)
		}
		return out
	}
	for _, p := range AllPieces {
		if mv, ok := placements[string(p)]; ok {
			if mv.Piece == "" {
				mv.Piece = string(p)
			}
			out = append(out, mv)
		}
	}
	return out
}

func mapFromSteps(steps []Move) map[byte]Move {
	m := map[byte]Move{}
	for _, mv := range steps {
		if len(mv.Piece) == 0 {
			continue
		}
		if _, exists := m[mv.Piece[0]]; !exists {
			m[mv.Piece[0]] = mv
		}
	}
	return m
}

func stepsFromMap(m map[byte]Move) []Move {
	var out []Move
	for _, p := range AllPieces {
		if mv, ok := m[p]; ok {
			if mv.Piece == "" {
				mv.Piece = string(p)
			}
			out = append(out, mv)
		}
	}
	return out
}

func variantSteps(variant OpeningVariant) []Move {
	if len(variant.Steps) > 0 {
		return variant.Steps
	}
	return stepsFromMap(variant.Placements)
}

func openingSubsetStatus(rows, target [BoardH]uint16, variant OpeningVariant) (map[byte]int, bool, bool) {
	if !rowsSubset(rows, target) {
		return nil, false, false
	}
	placed := map[byte]int{}
	placedSteps := 0
	for _, mv := range variantSteps(variant) {
		n := 0
		for _, c := range mv.Cells {
			if rows[c.Y]&(1<<c.X) != 0 {
				n++
			}
		}
		if n != 0 && n != 4 {
			return nil, false, false
		}
		if n == 4 {
			placed[mv.Piece[0]]++
			placedSteps++
		}
	}
	if countCells(rows) != placedSteps*4 {
		return nil, false, false
	}
	return placed, placedSteps == len(variantSteps(variant)), true
}

func rowsSubset(rows, target [BoardH]uint16) bool {
	for y := 0; y < BoardH; y++ {
		if rows[y]&^target[y] != 0 {
			return false
		}
	}
	return true
}

func rowsEqual(a, b [BoardH]uint16) bool {
	for y := 0; y < BoardH; y++ {
		if a[y] != b[y] {
			return false
		}
	}
	return true
}

func buildExactQueue(order string, current byte, next []byte, hold byte, placed map[byte]int) []byte {
	remaining := map[byte]int{}
	for i := 0; i < len(order); i++ {
		p := order[i]
		remaining[p]++
	}
	for p, n := range placed {
		remaining[p] -= n
		if remaining[p] < 0 {
			remaining[p] = 0
		}
	}

	var queue []byte
	present := map[byte]int{}
	visible := append([]byte{current}, next...)
	for _, p := range visible {
		if remaining[p] <= 0 || present[p] >= remaining[p] {
			continue
		}
		queue = append(queue, p)
		present[p]++
	}
	if hold != 0 && remaining[hold] > 0 && present[hold] < remaining[hold] {
		present[hold]++
	}
	for i := 0; i < len(order); i++ {
		p := order[i]
		for remaining[p] > 0 && present[p] < remaining[p] {
			queue = append(queue, p)
			present[p]++
		}
	}
	return queue
}

func setupOrderForEntry(order string, entry *OpeningEntry) string {
	if entry == nil || len(order) == 0 {
		return order
	}
	required := map[byte]int{}
	if len(entry.Variants) > 0 {
		for _, mv := range variantSteps(entry.Variants[0]) {
			if len(mv.Piece) > 0 {
				required[mv.Piece[0]]++
			}
		}
	}
	counts := map[byte]int{}
	for i := 0; i < len(order); i++ {
		counts[order[i]]++
	}
	out := order
	for _, p := range AllPieces {
		for counts[p] < required[p] {
			out += string(p)
			counts[p]++
		}
	}
	return out
}

func looksLikeRemainingOrder(order string) bool {
	if len(order) == 0 || len(order) > 7 {
		return false
	}
	seen := map[byte]bool{}
	for i := 0; i < len(order); i++ {
		p := order[i]
		if !validPiece(p) || seen[p] {
			return false
		}
		seen[p] = true
	}
	return true
}

func removePieceFromOrder(order string, piece byte) string {
	if piece == 0 {
		return order
	}
	out := make([]byte, 0, len(order))
	removed := false
	for i := 0; i < len(order); i++ {
		if order[i] == piece && !removed {
			removed = true
			continue
		}
		out = append(out, order[i])
	}
	return string(out)
}

func searchOpeningPlan(st BookState, target [BoardH]uint16, variant OpeningVariant, seen map[string]bool) ([]Move, bool) {
	if rowsEqual(st.Rows, target) {
		return []Move{}, true
	}
	key := bookStateKey(st)
	if seen[key] {
		return nil, false
	}
	seen[key] = true
	defer delete(seen, key)

	for _, cand := range openingCandidates(st, variant) {
		ns := applyBookMove(st, cand)
		if !rowsSubset(ns.Rows, target) {
			continue
		}
		path, ok := searchOpeningPlan(ns, target, variant, seen)
		if ok {
			return append([]Move{cand}, path...), true
		}
	}
	return nil, false
}

func openingCandidates(st BookState, variant OpeningVariant) []Move {
	var out []Move
	if len(st.Queue) > 0 {
		if mv, ok := openingCandidateForPiece(st, st.Queue[0], "ACTIVE", variant); ok {
			out = append(out, mv)
		}
	}
	if st.CanHold {
		if st.Hold == 0 {
			if len(st.Queue) >= 2 {
				if mv, ok := openingCandidateForPiece(st, st.Queue[1], "HOLD_EMPTY", variant); ok {
					out = append(out, mv)
				}
			}
		} else {
			if mv, ok := openingCandidateForPiece(st, st.Hold, "HOLD_SWAP", variant); ok {
				out = append(out, mv)
			}
		}
	}
	return out
}

func openingCandidateForPiece(st BookState, piece byte, source string, variant OpeningVariant) (Move, bool) {
	for _, template := range variantSteps(variant) {
		if len(template.Piece) == 0 || template.Piece[0] != piece {
			continue
		}
		blocked := false
		for _, c := range template.Cells {
			if st.Rows[c.Y]&(1<<c.X) != 0 {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		rot := Pieces[piece][template.Rotation]
		if !matchesHardDrop(st.Rows, rot, template.X, template.Y) {
			continue
		}
		_, lines, pc, cells := placeAndClear(st.Rows, rot, template.X, template.Y)
		if lines != 0 || pc {
			continue
		}
		mv := template
		mv.Source = source
		mv.Piece = string(piece)
		mv.Lines = 0
		mv.PerfectClear = false
		mv.Immediate = 0
		mv.Eval = 0
		mv.Cells = cells
		return mv, true
	}
	return Move{}, false
}

func triggerCandidate(st BookState, trigger *TriggerTemplate, afterRows [BoardH]uint16) (Move, bool) {
	if len(st.Queue) > 0 && st.Queue[0] == trigger.Piece {
		if mv, ok := triggerMoveFromTemplate(st.Rows, trigger.Move, "ACTIVE", afterRows); ok {
			return mv, true
		}
	}
	if !st.CanHold {
		return Move{}, false
	}
	if st.Hold == 0 {
		if len(st.Queue) >= 2 && st.Queue[1] == trigger.Piece {
			if mv, ok := triggerMoveFromTemplate(st.Rows, trigger.Move, "HOLD_EMPTY", afterRows); ok {
				return mv, true
			}
		}
		return Move{}, false
	}
	if st.Hold == trigger.Piece {
		return triggerMoveFromTemplate(st.Rows, trigger.Move, "HOLD_SWAP", afterRows)
	}
	return Move{}, false
}

func triggerMoveFromTemplate(rows [BoardH]uint16, template Move, source string, afterRows [BoardH]uint16) (Move, bool) {
	rot := Pieces[template.Piece[0]][template.Rotation]
	if !matchesHardDrop(rows, rot, template.X, template.Y) {
		return Move{}, false
	}
	nr, lines, pc, cells := placeAndClear(rows, rot, template.X, template.Y)
	if pc || lines == 0 || !rowsEqual(nr, afterRows) {
		return Move{}, false
	}
	mv := template
	mv.Source = source
	mv.Lines = lines
	mv.PerfectClear = false
	mv.Immediate = 0
	mv.Eval = 0
	mv.Cells = cells
	return mv, true
}

func applyBookMove(st BookState, mv Move) BookState {
	ns := st
	rot := Pieces[mv.Piece[0]][mv.Rotation]
	nr, _, _, _ := placeAndClear(st.Rows, rot, mv.X, mv.Y)
	ns.Rows = nr
	switch mv.Source {
	case "ACTIVE":
		ns.CanHold = true
		if len(ns.Queue) > 0 {
			ns.Queue = append([]byte{}, ns.Queue[1:]...)
		}
	case "HOLD_EMPTY":
		ns.CanHold = false
		oldActive := byte(0)
		if len(ns.Queue) > 0 {
			oldActive = ns.Queue[0]
		}
		ns.Hold = oldActive
		if len(ns.Queue) > 1 {
			ns.Queue = append([]byte{}, ns.Queue[2:]...)
		} else {
			ns.Queue = []byte{}
		}
	case "HOLD_SWAP":
		ns.CanHold = false
		oldActive := byte(0)
		if len(ns.Queue) > 0 {
			oldActive = ns.Queue[0]
		}
		ns.Hold = oldActive
		if len(ns.Queue) > 0 {
			ns.Queue = append([]byte{}, ns.Queue[1:]...)
		}
	}
	ns.Path = append(append([]Move{}, st.Path...), mv)
	return ns
}

func matchesHardDrop(rows [BoardH]uint16, rot Rotation, x, wantY int) bool {
	gotY, ok := hardDropY(rows, rot, x)
	return ok && gotY == wantY
}

func bookStateKey(st BookState) string {
	var b strings.Builder
	for _, r := range st.Rows {
		fmt.Fprintf(&b, "%x,", r)
	}
	b.WriteByte('|')
	b.Write(st.Queue)
	b.WriteByte('|')
	b.WriteByte(st.Hold)
	b.WriteByte('|')
	if st.CanHold {
		b.WriteByte('1')
	} else {
		b.WriteByte('0')
	}
	return b.String()
}
