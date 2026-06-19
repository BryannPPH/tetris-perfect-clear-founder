package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	BoardW = 10
	BoardH = 20
)

var AllPieces = []byte{'I', 'J', 'L', 'O', 'S', 'T', 'Z'}

type Point struct{ X, Y int }

type Rotation []Point

type Move struct {
	Source       string  `json:"source"`
	Piece        string  `json:"piece"`
	Rotation     int     `json:"rotation"`
	X            int     `json:"x"`
	Y            int     `json:"y"`
	Lines        int     `json:"lines"`
	PerfectClear bool    `json:"perfectClear"`
	Immediate    int     `json:"immediate"`
	Eval         float64 `json:"eval"`
	Cells        []Point `json:"cells"`
}

type Candidate struct {
	Move  Move
	Rows  [BoardH]uint16
	Score int
	Combo int
	B2B   bool
}

type PathNode struct {
	Move Move
	Prev *PathNode
}

type State struct {
	Rows    [BoardH]uint16
	Queue   []byte
	Residue []byte
	Hold    byte
	CanHold bool
	Score   int
	Combo   int
	B2B     bool
	Path    *PathNode
	PathLen int
}

type SearchKey struct {
	Rows       [BoardH]uint16
	Queue      [8]byte
	QueueLen   uint8
	Residue    [8]byte
	ResidueLen uint8
	Hold       byte
	CanHold    bool
	Combo      int
	Flags      uint8
}

type AdvisorRequest struct {
	Board        []string `json:"board"`
	Current      string   `json:"current"`
	Next         string   `json:"next"`
	Hold         string   `json:"hold"`
	Depth        int      `json:"depth"`
	Beam         int      `json:"beam"`
	PCMode       bool     `json:"pcMode"`
	OpeningID    int      `json:"openingId"`
	OpeningOrder string   `json:"openingOrder"`
	FinishOrder  string   `json:"finishOrder"`
}

type AdvisorResponse struct {
	Ok             bool     `json:"ok"`
	Error          string   `json:"error,omitempty"`
	Move           Move     `json:"move"`
	BoardAfter     []string `json:"boardAfter"`
	BoardPreview   []string `json:"boardPreview"`
	Plan           []Move   `json:"plan"`
	BagInfo        string   `json:"bagInfo"`
	Reason         string   `json:"reason"`
	ExploredStates int      `json:"exploredStates"`
	OpeningID      int      `json:"openingId,omitempty"`
	OpeningOrder   string   `json:"openingOrder,omitempty"`
	FinishOrder    string   `json:"finishOrder,omitempty"`
}

// Coordinates use row-major board coordinates: x = 0..9, y = 0..19, y=0 is top.
// Piece local coordinates are anchored near the top-left of a bounding box.
var Pieces = map[byte][]Rotation{
	'I': uniqueRotations([]Point{{0, 0}, {1, 0}, {2, 0}, {3, 0}}),
	'O': uniqueRotations([]Point{{0, 0}, {1, 0}, {0, 1}, {1, 1}}),
	'T': uniqueRotations([]Point{{0, 0}, {1, 0}, {2, 0}, {1, 1}}),
	'J': uniqueRotations([]Point{{0, 0}, {0, 1}, {1, 1}, {2, 1}}),
	'L': uniqueRotations([]Point{{2, 0}, {0, 1}, {1, 1}, {2, 1}}),
	'S': uniqueRotations([]Point{{1, 0}, {2, 0}, {0, 1}, {1, 1}}),
	'Z': uniqueRotations([]Point{{0, 0}, {1, 0}, {1, 1}, {2, 1}}),
}

func rotateCW(cells []Point) []Point {
	out := make([]Point, len(cells))
	for i, p := range cells {
		out[i] = Point{X: p.Y, Y: -p.X}
	}
	return normalize(out)
}

func normalize(cells []Point) []Point {
	minX, minY := 999, 999
	for _, p := range cells {
		if p.X < minX {
			minX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
	}
	out := make([]Point, len(cells))
	for i, p := range cells {
		out[i] = Point{p.X - minX, p.Y - minY}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Y == out[j].Y {
			return out[i].X < out[j].X
		}
		return out[i].Y < out[j].Y
	})
	return out
}

func rotKey(cells []Point) string {
	var b strings.Builder
	for _, p := range normalize(cells) {
		fmt.Fprintf(&b, "%d,%d;", p.X, p.Y)
	}
	return b.String()
}

func uniqueRotations(base []Point) []Rotation {
	current := normalize(base)
	seen := map[string]bool{}
	out := []Rotation{}
	for i := 0; i < 4; i++ {
		current = normalize(current)
		k := rotKey(current)
		if !seen[k] {
			seen[k] = true
			r := make([]Point, len(current))
			copy(r, current)
			out = append(out, Rotation(r))
		}
		current = rotateCW(current)
	}
	return out
}

func validPiece(p byte) bool {
	_, ok := Pieces[p]
	return ok
}

func cleanPieceString(s string) []byte {
	s = strings.ToUpper(strings.TrimSpace(s))
	out := []byte{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if validPiece(c) {
			out = append(out, c)
		}
	}
	return out
}

func boardFromStrings(rows []string) ([BoardH]uint16, error) {
	var out [BoardH]uint16
	if len(rows) != BoardH {
		return out, fmt.Errorf("board must have %d rows", BoardH)
	}
	for y, row := range rows {
		if len(row) != BoardW {
			return out, fmt.Errorf("row %d must have width %d", y, BoardW)
		}
		var bits uint16
		for x := 0; x < BoardW; x++ {
			if row[x] == '#' || row[x] == 'X' || row[x] == '1' {
				bits |= 1 << x
			}
		}
		out[y] = bits
	}
	return out, nil
}

func rowsToStrings(rows [BoardH]uint16) []string {
	out := make([]string, BoardH)
	for y := 0; y < BoardH; y++ {
		bs := make([]byte, BoardW)
		for x := 0; x < BoardW; x++ {
			if rows[y]&(1<<x) != 0 {
				bs[x] = '#'
			} else {
				bs[x] = '.'
			}
		}
		out[y] = string(bs)
	}
	return out
}

func overlayStrings(rows [BoardH]uint16, cells []Point) []string {
	grid := rowsToStrings(rows)
	bsRows := make([][]byte, BoardH)
	for y := 0; y < BoardH; y++ {
		bsRows[y] = []byte(grid[y])
	}
	for _, c := range cells {
		if c.X >= 0 && c.X < BoardW && c.Y >= 0 && c.Y < BoardH {
			if bsRows[c.Y][c.X] == '#' {
				bsRows[c.Y][c.X] = '@'
			} else {
				bsRows[c.Y][c.X] = '*'
			}
		}
	}
	out := make([]string, BoardH)
	for y := 0; y < BoardH; y++ {
		out[y] = string(bsRows[y])
	}
	return out
}

func collides(rows [BoardH]uint16, rot Rotation, x, y int) bool {
	for _, p := range rot {
		bx, by := x+p.X, y+p.Y
		if bx < 0 || bx >= BoardW || by >= BoardH {
			return true
		}
		if by >= 0 && rows[by]&(1<<bx) != 0 {
			return true
		}
	}
	return false
}

func hardDropY(rows [BoardH]uint16, rot Rotation, x int) (int, bool) {
	y := -4
	for !collides(rows, rot, x, y+1) {
		y++
	}
	if collides(rows, rot, x, y) {
		return y, false
	}
	return y, true
}

func placeAndClear(rows [BoardH]uint16, rot Rotation, x, y int) ([BoardH]uint16, int, bool, []Point) {
	nr := rows
	cells := []Point{}
	for _, p := range rot {
		bx, by := x+p.X, y+p.Y
		if by >= 0 && by < BoardH && bx >= 0 && bx < BoardW {
			nr[by] |= 1 << bx
			cells = append(cells, Point{bx, by})
		}
	}
	full := uint16((1 << BoardW) - 1)
	kept := make([]uint16, 0, BoardH)
	cleared := 0
	for yy := 0; yy < BoardH; yy++ {
		if nr[yy] == full {
			cleared++
		} else {
			kept = append(kept, nr[yy])
		}
	}
	var out [BoardH]uint16
	emptyRows := BoardH - len(kept)
	for i := 0; i < emptyRows; i++ {
		out[i] = 0
	}
	for i, v := range kept {
		out[emptyRows+i] = v
	}
	return out, cleared, isEmpty(out), cells
}

func isEmpty(rows [BoardH]uint16) bool {
	for _, r := range rows {
		if r != 0 {
			return false
		}
	}
	return true
}

func countCells(rows [BoardH]uint16) int {
	n := 0
	for _, r := range rows {
		n += bitsCount16(r)
	}
	return n
}

func bitsCount16(v uint16) int {
	c := 0
	for v != 0 {
		v &= v - 1
		c++
	}
	return c
}

func scoreEvent(lines int, pc bool, combo int, b2b bool) (score int, newCombo int, newB2B bool) {
	base := 0
	switch lines {
	case 1:
		base = 100
	case 2:
		base = 300
	case 3:
		base = 500
	case 4:
		base = 800
	}
	difficult := lines == 4
	if difficult && b2b {
		base = int(math.Round(float64(base) * 1.5))
	}
	if lines > 0 {
		newCombo = combo + 1
	} else {
		newCombo = -1
	}
	comboBonus := 0
	if lines > 0 && newCombo > 0 {
		comboBonus = newCombo * 50
	}
	pcBonus := 0
	if pc && lines > 0 {
		pcBonus = 3500
	}
	newB2B = b2b
	if lines > 0 {
		if difficult {
			newB2B = true
		} else {
			newB2B = false
		}
	}
	return base + comboBonus + pcBonus, newCombo, newB2B
}

func columnHeights(rows [BoardH]uint16) [BoardW]int {
	var h [BoardW]int
	for x := 0; x < BoardW; x++ {
		h[x] = 0
		for y := 0; y < BoardH; y++ {
			if rows[y]&(1<<x) != 0 {
				h[x] = BoardH - y
				break
			}
		}
	}
	return h
}

func boardFeatures(rows [BoardH]uint16) (holes, bumpiness, maxH, well, pcPot, b2bReady int) {
	h := columnHeights(rows)
	for x := 0; x < BoardW; x++ {
		if h[x] > maxH {
			maxH = h[x]
		}
		seen := false
		for y := 0; y < BoardH; y++ {
			filled := rows[y]&(1<<x) != 0
			if filled {
				seen = true
			} else if seen {
				holes++
			}
		}
	}
	for x := 0; x < BoardW-1; x++ {
		bumpiness += abs(h[x] - h[x+1])
	}

	// Reward a clean right or left Tetris well.
	well = 0
	rightWellClean := true
	leftWellClean := true
	for y := BoardH - 4; y < BoardH; y++ {
		if rows[y]&(1<<9) != 0 {
			rightWellClean = false
		}
		if rows[y]&(1<<0) != 0 {
			leftWellClean = false
		}
	}
	if rightWellClean {
		well += minInt(8, minColumnNeighbor(h, 8, h[9]))
	}
	if leftWellClean {
		well += minInt(8, minColumnNeighbor(h, 1, h[0]))
	}

	cells := countCells(rows)
	if holes == 0 && maxH <= 4 {
		pcPot += 80
	}
	if holes == 0 && maxH <= 8 {
		pcPot += 25
	}
	if cells%4 == 0 {
		pcPot += 10
	}
	if isEmpty(rows) {
		pcPot += 500
	}

	if well >= 4 && holes <= 2 {
		b2bReady = 25 + 3*well
	}
	return
}

func minColumnNeighbor(h [BoardW]int, neighbor int, wellH int) int {
	v := h[neighbor] - wellH
	if v < 0 {
		return 0
	}
	return v
}

func evaluate(rows [BoardH]uint16, score int, combo int, b2b bool, queue []byte, residue []byte, pcMode bool) float64 {
	holes, bump, maxH, well, pcPot, b2bReady := boardFeatures(rows)
	val := float64(score)
	val -= 165.0 * float64(holes)
	val -= 18.0 * float64(bump)
	val -= 22.0 * float64(maxH)
	val += 38.0 * float64(well)
	val += 1.0 * float64(b2bReady)
	if b2b {
		val += 180
	}
	if combo > 0 {
		val += float64(combo * 25)
	}
	if pcMode {
		val += 6.5 * float64(pcPot)
		if contains(queue, 'T') {
			val += 40
		}
		if contains(queue, 'I') {
			val += 35
		}
		if contains(residue, 'T') {
			val += 15
		}
		if contains(residue, 'I') {
			val += 15
		}
		// DPC-ish: low, clean 8-height stack with T availability is rewarded.
		if holes == 0 && maxH <= 8 && (contains(queue, 'T') || contains(residue, 'T')) {
			val += 120
		}
	}
	return val
}

func countNearFullRows(rows [BoardH]uint16) int {
	score := 0
	seenStack := false
	emptyInsideStack := false
	for y := 0; y < BoardH; y++ {
		filled := bitsCount16(rows[y])
		if filled > 0 {
			seenStack = true
		}
		if !seenStack {
			continue
		}
		if filled == 0 {
			emptyInsideStack = true
			continue
		}
		switch {
		case filled == 9:
			score += 30
		case filled == 8:
			score += 22
		case filled == 7:
			score += 15
		case filled == 6:
			score += 9
		case filled <= 2:
			score -= 10
		}
		if emptyInsideStack {
			score -= 18
		}
	}
	return score
}

func heuristicStateScore(s State, pcMode bool) float64 {
	return evaluate(s.Rows, s.Score, s.Combo, s.B2B, s.Queue, s.Residue, pcMode)
}

func perfectClearSearchScore(s State) float64 {
	holes, bump, maxH, _, pcPot, _ := boardFeatures(s.Rows)
	cells := countCells(s.Rows)
	nearFull := countNearFullRows(s.Rows)
	val := float64(s.Score)
	val -= 72.0 * float64(cells)
	val -= 320.0 * float64(holes)
	val -= 18.0 * float64(bump)
	val -= 26.0 * float64(maxH)
	val += 14.0 * float64(pcPot)
	val += float64(nearFull)
	if holes == 0 {
		val += 120
	}
	if maxH <= 4 {
		val += 80
	} else if maxH <= 8 {
		val += 30
	}
	if cells%4 == 0 {
		val += 20
	}
	if contains(s.Queue, 'T') {
		val += 18
	}
	if contains(s.Queue, 'I') {
		val += 12
	}
	if s.Hold != 0 {
		val += 10
	}
	if isEmpty(s.Rows) {
		val += 1000000
	}
	return val
}

func terminalPerfectClearScore(s State) float64 {
	val := float64(s.Score) + 1000000
	val -= 25.0 * float64(s.PathLen)
	if s.B2B {
		val += 30
	}
	return val
}

func pruneStates(candidates []State, limit int, scorer func(State) float64) []State {
	sort.Slice(candidates, func(i, j int) bool {
		return scorer(candidates[i]) > scorer(candidates[j])
	})
	pruned := make([]State, 0, minInt(limit, len(candidates)))
	seen := map[SearchKey]bool{}
	for _, s := range candidates {
		k := stateKey(s)
		if seen[k] {
			continue
		}
		seen[k] = true
		pruned = append(pruned, s)
		if len(pruned) >= limit {
			break
		}
	}
	return pruned
}

func shouldAttemptPerfectClear(rows [BoardH]uint16, pcMode bool) bool {
	if !pcMode {
		return false
	}
	holes, _, maxH, _, _, _ := boardFeatures(rows)
	cells := countCells(rows)
	return holes == 0 && maxH <= 6 && cells <= 24
}

func perfectClearSearch(init State, depth, beam int) (State, bool, int) {
	return perfectClearSearchWithLimit(init, depth, beam, 180)
}

func perfectClearSearchWithLimit(init State, depth, beam int, beamCap int) (State, bool, int) {
	frontier := []State{init}
	explored := 0
	exactBeam := beam / 2
	if exactBeam < 60 {
		exactBeam = 60
	}
	if beamCap > 0 && exactBeam > beamCap {
		exactBeam = beamCap
	}
	seen := map[SearchKey]float64{
		stateKey(init): perfectClearSearchScore(init),
	}

	for d := 0; d < depth; d++ {
		candidates := make([]State, 0, len(frontier)*20)
		exacts := []State{}
		for _, st := range frontier {
			cands := generatePlacementCandidatesKnown(st, true)
			explored += len(cands)
			for _, c := range cands {
				ns := applyCandidate(st, c)
				score := perfectClearSearchScore(ns)
				key := stateKey(ns)
				if prev, ok := seen[key]; ok && prev >= score {
					continue
				}
				seen[key] = score
				if ns.Path != nil {
					ns.Path.Move.Eval = score
				}
				if isEmpty(ns.Rows) && ns.Path != nil {
					exacts = append(exacts, ns)
				}
				candidates = append(candidates, ns)
			}
		}
		if len(exacts) > 0 {
			sort.Slice(exacts, func(i, j int) bool {
				return terminalPerfectClearScore(exacts[i]) > terminalPerfectClearScore(exacts[j])
			})
			return exacts[0], true, explored
		}
		if len(candidates) == 0 {
			break
		}
		frontier = pruneStates(candidates, exactBeam, perfectClearSearchScore)
	}
	return State{}, false, explored
}

func generatePlacementCandidates(st State, pcMode bool) []Candidate {
	return generatePlacementCandidatesWithMode(st, pcMode, true)
}

func generatePlacementCandidatesKnown(st State, pcMode bool) []Candidate {
	return generatePlacementCandidatesWithMode(st, pcMode, false)
}

func generatePlacementCandidatesWithMode(st State, pcMode bool, allowUnknownBranch bool) []Candidate {
	var variants []State
	if allowUnknownBranch {
		// Ensure enough queue for hold-empty case.
		variants = ensureQueueLen(st, 2)
	} else {
		variants = []State{st}
	}
	candidates := []Candidate{}
	for _, s := range variants {
		if len(s.Queue) == 0 {
			continue
		}
		active := s.Queue[0]
		// Normal active placement.
		candidates = append(candidates, candidatesForPiece(s, active, "ACTIVE", 0, pcMode)...)

		// Hold placement.
		if s.CanHold {
			if s.Hold == 0 {
				if len(s.Queue) >= 2 {
					holdPiece := s.Queue[1]
					candidates = append(candidates, candidatesForPiece(s, holdPiece, "HOLD_EMPTY", 0, pcMode)...)
				}
			} else {
				candidates = append(candidates, candidatesForPiece(s, s.Hold, "HOLD_SWAP", 0, pcMode)...)
			}
		}
	}
	return candidates
}

func candidatesForPiece(s State, piece byte, source string, level int, pcMode bool) []Candidate {
	rots := Pieces[piece]
	out := []Candidate{}
	for ri, rot := range rots {
		maxX := maxRotX(rot)
		for x := -2; x <= BoardW-maxX+1; x++ {
			y, ok := hardDropY(s.Rows, rot, x)
			if !ok {
				continue
			}
			nr, lines, pc, cells := placeAndClear(s.Rows, rot, x, y)
			if topOut(nr) {
				continue
			}
			immediate, nc, nb := scoreEvent(lines, pc, s.Combo, s.B2B)
			mv := Move{Source: source, Piece: string(piece), Rotation: ri, X: x, Y: y, Lines: lines, PerfectClear: pc, Immediate: immediate, Cells: cells}
			out = append(out, Candidate{Move: mv, Rows: nr, Score: s.Score + immediate, Combo: nc, B2B: nb})
		}
	}
	return out
}

func applyCandidate(s State, c Candidate) State {
	ns := s
	ns.Rows = c.Rows
	ns.Score = c.Score
	ns.Combo = c.Combo
	ns.B2B = c.B2B
	switch c.Move.Source {
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
	mv := c.Move
	ns.Path = &PathNode{Move: mv, Prev: s.Path}
	ns.PathLen = s.PathLen + 1
	return ns
}

func ensureQueueLen(st State, n int) []State {
	states := []State{st}
	for {
		allOK := true
		nextStates := []State{}
		for _, s := range states {
			if len(s.Queue) >= n {
				nextStates = append(nextStates, s)
				continue
			}
			allOK = false
			residue := append([]byte{}, s.Residue...)
			if len(residue) == 0 {
				residue = append([]byte{}, AllPieces...)
			}
			for _, p := range residue {
				ns := s
				ns.Queue = append(append([]byte{}, s.Queue...), p)
				ns.Residue = removePiece(residue, p)
				nextStates = append(nextStates, ns)
			}
		}
		states = nextStates
		if allOK {
			return states
		}
		if len(states) > 2000 {
			return states[:2000]
		}
	}
}

func topOut(rows [BoardH]uint16) bool {
	// Conservative top-out: any block in top two hidden-ish rows is risky.
	return rows[0] != 0
}

func maxRotX(rot Rotation) int {
	m := 0
	for _, p := range rot {
		if p.X > m {
			m = p.X
		}
	}
	return m
}

func inferResidueFromVisible(visible []byte) ([]byte, string) {
	seen := map[byte]bool{}
	for _, p := range visible {
		if seen[p] {
			// Greedy boundary: duplicate implies a new bag began before this piece.
			seen = map[byte]bool{}
		}
		seen[p] = true
	}
	residue := []byte{}
	for _, p := range AllPieces {
		if !seen[p] {
			residue = append(residue, p)
		}
	}
	info := fmt.Sprintf("Visible queue consumed as seven-bag sequence. Seen in current inferred bag: %s. Possible hidden next pieces after preview: %s.", keysString(seen), string(residue))
	if len(uniqueBytes(visible)) == len(visible) && len(visible) == 6 {
		info = fmt.Sprintf("The 6 visible pieces are all unique, so the standard 7-bag inference says the next hidden piece is likely the missing piece: %s. After that, a fresh bag begins.", string(residue))
	}
	return residue, info
}

func SearchBest(rows [BoardH]uint16, current byte, next []byte, hold byte, depth, beam int, pcMode bool) AdvisorResponse {
	if depth < 1 {
		if pcMode {
			depth = 9
		} else {
			depth = 8
		}
	}
	if depth > 14 {
		depth = 14
	}
	if beam < 10 {
		if pcMode {
			beam = 160
		} else {
			beam = 80
		}
	}
	if beam > 1200 {
		beam = 1200
	}
	visible := append([]byte{current}, next...)
	residue, bagInfo := inferResidueFromVisible(visible)
	initQueue := append([]byte{}, visible...)
	init := State{Rows: rows, Queue: initQueue, Residue: residue, Hold: hold, CanHold: true, Score: 0, Combo: -1, B2B: false}

	explored := 0
	if shouldAttemptPerfectClear(rows, pcMode) {
		exactInit := init
		if len(exactInit.Residue) == 1 {
			exactInit.Queue = append(exactInit.Queue, exactInit.Residue[0])
			exactInit.Residue = []byte{}
		}
		pcDepth := len(exactInit.Queue)
		if pcDepth > depth {
			pcDepth = depth
		}
		if pcDepth > 6 {
			pcDepth = 6
		}
		pcBest, _, pcExplored := perfectClearSearch(exactInit, pcDepth, beam)
		explored += pcExplored
		if pcBest.Path != nil {
			plan := pathToMoves(pcBest.Path, pcBest.PathLen)
			first := plan[0]
			afterFirst := replayFirst(rows, init, first, pcMode)
			reason := buildReason(pcBest, pcMode, true)
			return AdvisorResponse{Ok: true, Move: first, BoardAfter: rowsToStrings(afterFirst.Rows), BoardPreview: overlayStrings(rows, first.Cells), Plan: plan, BagInfo: bagInfo, Reason: reason, ExploredStates: explored}
		}
	}

	frontier := []State{init}
	seen := map[SearchKey]float64{
		stateKey(init): heuristicStateScore(init, pcMode),
	}

	var best State
	bestVal := math.Inf(-1)

	for d := 0; d < depth; d++ {
		candidates := []State{}
		for _, st := range frontier {
			cands := generatePlacementCandidates(st, pcMode)
			explored += len(cands)
			for _, c := range cands {
				ns := applyCandidate(st, c)
				score := heuristicStateScore(ns, pcMode)
				key := stateKey(ns)
				if prev, ok := seen[key]; ok && prev >= score {
					continue
				}
				seen[key] = score
				if ns.Path != nil {
					ns.Path.Move.Eval = score
				}
				candidates = append(candidates, ns)
			}
		}
		if len(candidates) == 0 {
			break
		}
		frontier = pruneStates(candidates, beam, func(s State) float64 {
			return heuristicStateScore(s, pcMode)
		})
		for _, s := range frontier {
			v := heuristicStateScore(s, pcMode)
			if v > bestVal && s.Path != nil {
				bestVal = v
				best = s
			}
		}
	}

	if best.Path == nil {
		return AdvisorResponse{Ok: false, Error: "No legal placement found. Check the board/current piece input."}
	}
	plan := pathToMoves(best.Path, best.PathLen)
	first := plan[0]
	afterFirst := replayFirst(rows, init, first, pcMode)
	reason := buildReason(best, pcMode, false)
	return AdvisorResponse{Ok: true, Move: first, BoardAfter: rowsToStrings(afterFirst.Rows), BoardPreview: overlayStrings(rows, first.Cells), Plan: plan, BagInfo: bagInfo, Reason: reason, ExploredStates: explored}
}

func replayFirst(rows [BoardH]uint16, init State, mv Move, pcMode bool) State {
	st := init
	var piece byte = mv.Piece[0]
	rot := Pieces[piece][mv.Rotation]
	nr, lines, pc, _ := placeAndClear(rows, rot, mv.X, mv.Y)
	imm, nc, nb := scoreEvent(lines, pc, st.Combo, st.B2B)
	cand := Candidate{Move: mv, Rows: nr, Score: imm, Combo: nc, B2B: nb}
	return applyCandidate(st, cand)
}

func stateKey(s State) SearchKey {
	k := SearchKey{
		Rows:  s.Rows,
		Hold:  s.Hold,
		CanHold: s.CanHold,
		Combo: s.Combo,
	}
	if s.B2B {
		k.Flags |= 1
	}
	k.QueueLen = uint8(minInt(len(s.Queue), len(k.Queue)))
	for i := 0; i < int(k.QueueLen); i++ {
		k.Queue[i] = s.Queue[i]
	}
	k.ResidueLen = uint8(minInt(len(s.Residue), len(k.Residue)))
	for i := 0; i < int(k.ResidueLen); i++ {
		k.Residue[i] = s.Residue[i]
	}
	return k
}

func buildReason(s State, pcMode bool, exactPC bool) string {
	holes, bump, maxH, well, pcPot, b2bReady := boardFeatures(s.Rows)
	mode := "balanced"
	if pcMode {
		mode = "PC/DPC-focused"
	}
	if exactPC {
		return fmt.Sprintf("Mode %s. Exact perfect clear line found in %d moves. Planned score=%d, holes=%d, bumpiness=%d, max height=%d, well=%d, pcPotential=%d, b2bReady=%d. This line is preferred because it reaches an actual empty board, not just a good heuristic shape.", mode, s.PathLen, s.Score, holes, bump, maxH, well, pcPot, b2bReady)
	}
	return fmt.Sprintf("Mode %s. Planned score=%d, holes=%d, bumpiness=%d, max height=%d, well=%d, pcPotential=%d, b2bReady=%d. The first move is chosen because it leads to the best beam-search value after the visible queue plus seven-bag branching.", mode, s.Score, holes, bump, maxH, well, pcPot, b2bReady)
}

func pathToMoves(node *PathNode, n int) []Move {
	if node == nil || n == 0 {
		return nil
	}
	out := make([]Move, n)
	i := n - 1
	for cur := node; cur != nil && i >= 0; cur = cur.Prev {
		out[i] = cur.Move
		i--
	}
	return out
}

func contains(xs []byte, p byte) bool {
	for _, x := range xs {
		if x == p {
			return true
		}
	}
	return false
}
func removePiece(xs []byte, p byte) []byte {
	out := []byte{}
	removed := false
	for _, x := range xs {
		if x == p && !removed {
			removed = true
			continue
		}
		out = append(out, x)
	}
	return out
}
func uniqueBytes(xs []byte) []byte {
	m := map[byte]bool{}
	out := []byte{}
	for _, x := range xs {
		if !m[x] {
			m[x] = true
			out = append(out, x)
		}
	}
	return out
}
func keysString(m map[byte]bool) string {
	out := []byte{}
	for _, p := range AllPieces {
		if m[p] {
			out = append(out, p)
		}
	}
	return string(out)
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func handleRecommend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req AdvisorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	rows, err := boardFromStrings(req.Board)
	if err != nil {
		json.NewEncoder(w).Encode(AdvisorResponse{Ok: false, Error: err.Error()})
		return
	}
	cur := cleanPieceString(req.Current)
	if len(cur) != 1 {
		json.NewEncoder(w).Encode(AdvisorResponse{Ok: false, Error: "Current piece must be one of I,J,L,O,S,T,Z"})
		return
	}
	next := cleanPieceString(req.Next)
	if len(next) != 5 {
		json.NewEncoder(w).Encode(AdvisorResponse{Ok: false, Error: "Next must contain exactly 5 valid pieces, e.g. SZILO"})
		return
	}
	holdPieces := cleanPieceString(req.Hold)
	hold := byte(0)
	if len(holdPieces) > 0 {
		hold = holdPieces[0]
	}
	resp := SearchBestWithBook(rows, cur[0], next, hold, req.Depth, req.Beam, req.PCMode, req.OpeningID, req.OpeningOrder, req.FinishOrder)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

func main() {
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/api/recommend", handleRecommend)
	addr := "127.0.0.1:8787"
	url := "http://" + addr
	go func() {
		time.Sleep(400 * time.Millisecond)
		openBrowser(url)
	}()
	log.Printf("TETR.IO Go Smart Advisor running at %s", url)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

const indexHTML = `<!doctype html>
<html>
<head>
<meta charset="utf-8" />
<title>TETR.IO Go Smart Advisor</title>
<style>
:root{--bg:#0f1220;--panel:#171a2b;--cell:#24283d;--filled:#8e95b8;--rec:#67e8a5;--text:#e8ecff;--muted:#9aa3c7;--accent:#7aa2ff;--bad:#ff7a90;--piece-i:#3ba7ff;--piece-o:#ffd84d;--piece-z:#ff5a5f;--piece-s:#4cd964;--piece-t:#ff6bcf;--piece-l:#ff9f3f;--piece-j:#3ba7ff;}
*{box-sizing:border-box}
body{margin:0;font-family:Inter,system-ui,Segoe UI,Arial,sans-serif;background:linear-gradient(120deg,#0d1020,#151936);color:var(--text)}
header{padding:18px 24px;border-bottom:1px solid #2b3151;background:rgba(10,12,24,.7);position:sticky;top:0;backdrop-filter:blur(8px)}
h1{font-size:22px;margin:0 0 4px}
h3{margin:18px 0 10px}
.sub{color:var(--muted);font-size:13px}
.wrap{display:grid;grid-template-columns:360px 1fr;gap:22px;padding:22px;max-width:1280px;margin:0 auto}
.panel{background:rgba(23,26,43,.92);border:1px solid #2b3151;border-radius:16px;padding:16px;box-shadow:0 12px 40px rgba(0,0,0,.22)}
.board{display:grid;grid-template-columns:repeat(10,30px);grid-template-rows:repeat(20,30px);gap:2px;user-select:none}
.cell{width:30px;height:30px;background:var(--cell);border:1px solid #343a5c;border-radius:4px;cursor:pointer;transition:.08s}
.cell:hover{filter:brightness(1.15)}
.cell.filled{background:var(--filled)}
.cell.rec{background:var(--rec);box-shadow:0 0 12px rgba(103,232,165,.45)}
.cell.conflict{background:#fbbf24}
.controls{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin-top:12px}
.field{display:flex;flex-direction:column;gap:6px}
.field.full{grid-column:1/-1}
label{font-size:12px;color:var(--muted);font-weight:700;letter-spacing:.02em;text-transform:uppercase}
input,select{background:#0f1326;color:var(--text);border:1px solid #374061;border-radius:10px;padding:10px;font-size:14px;text-transform:uppercase}
input[type=checkbox]{transform:scale(1.2);accent-color:#7aa2ff}
.checkrow{display:flex;gap:8px;align-items:center;padding-top:21px}
.btns{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin-top:14px}
.btns.single{grid-template-columns:1fr}
button{border:0;border-radius:12px;padding:11px 12px;font-weight:700;cursor:pointer;background:var(--accent);color:#061026;transition:transform .08s ease,filter .08s ease}
button:hover{transform:translateY(-1px);filter:brightness(1.05)}
button.secondary{background:#2b3151;color:var(--text)}
button.danger{background:#ff7a90;color:#26070d}
.queueeditor{display:grid;grid-template-columns:120px 120px 1fr;gap:12px;align-items:start}
.piecegroup,.nextgroup{background:#10162c;border:1px solid #313858;border-radius:14px;padding:10px}
.queuelabel{font-size:11px;color:var(--muted);font-weight:800;letter-spacing:.08em;text-transform:uppercase;margin-bottom:8px}
.piece-slot{width:100%;min-height:84px;display:flex;align-items:center;justify-content:center;background:#0b1021;border:1px solid #394262;border-radius:12px;padding:8px;position:relative;color:var(--text)}
.piece-slot.big{min-height:96px}
.piece-slot.small{min-height:70px}
.piece-slot.active{box-shadow:0 0 0 2px var(--accent) inset,0 0 16px rgba(122,162,255,.18)}
.piece-slot.placeholder{color:var(--muted)}
.slotclear{margin-top:8px;width:100%;background:#2b3151;color:var(--text);padding:8px 10px;font-size:12px}
.nextslots{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:8px}
.targetbadge{margin-top:10px;padding:9px 10px;border-radius:10px;background:#0f1326;border:1px solid #313858;color:#cfd7ff;font-size:12px}
.piecepad{display:grid;grid-template-columns:repeat(9,minmax(0,1fr));gap:8px}
.piecebtn{min-height:80px;background:#10162c;color:var(--text);border:1px solid #394262;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:8px;padding:8px}
.piecebtn span{font-size:11px;font-weight:800;letter-spacing:.06em}
.piecebtn.alt{background:#2b3151;color:var(--text)}
.piece-grid{display:grid;grid-template-columns:repeat(4,12px);grid-template-rows:repeat(4,12px);gap:2px}
.piece-grid.small{grid-template-columns:repeat(4,10px);grid-template-rows:repeat(4,10px)}
.piece-grid.tiny{grid-template-columns:repeat(4,9px);grid-template-rows:repeat(4,9px)}
.mino{border-radius:3px;background:transparent}
.mino.fill{border:1px solid rgba(255,255,255,.18);box-shadow:inset 0 1px 0 rgba(255,255,255,.18)}
.piece-I .mino.fill{background:var(--piece-i)}
.piece-O .mino.fill{background:var(--piece-o)}
.piece-Z .mino.fill{background:var(--piece-z)}
.piece-S .mino.fill{background:var(--piece-s)}
.piece-T .mino.fill{background:var(--piece-t)}
.piece-L .mino.fill{background:var(--piece-l)}
.piece-J .mino.fill{background:var(--piece-j)}
.slot-placeholder{font-weight:900;font-size:24px;color:var(--muted)}
.slot-placeholder.empty{font-size:11px;letter-spacing:.08em;text-transform:uppercase}
.small{font-size:12px;color:var(--muted);line-height:1.45}
.result{white-space:pre-wrap;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;background:#0f1326;border:1px solid #313858;border-radius:12px;padding:12px;min-height:150px;color:#d8e0ff}
.plan{display:grid;gap:8px;margin-top:12px}
.move{padding:10px;border:1px solid #303756;border-radius:12px;background:#11162b}
.move b{color:#9fe4ff}
.error{color:var(--bad);font-weight:700}
.legend{display:flex;gap:12px;flex-wrap:wrap;margin-top:10px}
.leg{display:flex;gap:6px;align-items:center;color:var(--muted);font-size:12px}
.sw{width:14px;height:14px;border-radius:3px;background:var(--cell);border:1px solid #343a5c}
.sw.f{background:var(--filled)}
.sw.r{background:var(--rec)}
@media(max-width:980px){
  .wrap{grid-template-columns:1fr}
  .queueeditor{grid-template-columns:1fr}
}
@media(max-width:850px){
  .board{grid-template-columns:repeat(10,28px);grid-template-rows:repeat(20,28px)}
  .cell{width:28px;height:28px}
  .piecepad{grid-template-columns:repeat(3,minmax(0,1fr))}
  .nextslots{grid-template-columns:repeat(5,minmax(0,1fr))}
}
</style>
</head>
<body>
<header><h1>TETR.IO Go Smart Advisor</h1><div class="sub">Offline practice tool: current + 5 next preview, 7-bag inference, beam search, PC/DPC-oriented heuristic.</div></header>
<div class="wrap">
  <div class="panel">
    <div id="board" class="board"></div>
    <div class="legend"><span class="leg"><span class="sw"></span> Empty</span><span class="leg"><span class="sw f"></span> Filled</span><span class="leg"><span class="sw r"></span> Last placed</span></div>
    <div class="btns"><button class="secondary" onclick="clearBoard()">Clear Board</button><button class="danger" onclick="sampleBoard()">Sample Board</button></div>
  </div>
  <div class="panel">
    <div class="controls">
      <div class="field full">
        <label>Queue Input</label>
        <div class="queueeditor">
          <div class="piecegroup">
            <div class="queuelabel">Current</div>
            <button type="button" id="currentSlot" class="piece-slot big" onclick="setActiveTarget('current')"></button>
          </div>
          <div class="piecegroup">
            <div class="queuelabel">Hold</div>
            <button type="button" id="holdSlot" class="piece-slot big" onclick="setActiveTarget('hold')"></button>
            <button type="button" class="slotclear" onclick="clearHoldPiece()">Clear Hold</button>
          </div>
          <div class="nextgroup">
            <div class="queuelabel">Next 5</div>
            <div class="nextslots">
              <button type="button" id="nextSlot0" class="piece-slot small" onclick="setActiveTarget('next:0')"></button>
              <button type="button" id="nextSlot1" class="piece-slot small" onclick="setActiveTarget('next:1')"></button>
              <button type="button" id="nextSlot2" class="piece-slot small" onclick="setActiveTarget('next:2')"></button>
              <button type="button" id="nextSlot3" class="piece-slot small" onclick="setActiveTarget('next:3')"></button>
              <button type="button" id="nextSlot4" class="piece-slot small" onclick="setActiveTarget('next:4')"></button>
            </div>
            <div id="targetbadge" class="targetbadge"></div>
          </div>
        </div>
        <input id="current" type="hidden" value="T">
        <input id="next" type="hidden" value="SZILO">
        <input id="hold" type="hidden" value="">
      </div>
      <div class="field full"><label>Piece Buttons</label><div id="piecepad" class="piecepad"></div></div>
      <div class="field"><label>Depth</label><input id="depth" type="number" min="6" max="14" value="9"></div>
      <div class="field"><label>Beam Width</label><input id="beam" type="number" min="50" max="1200" value="160"></div>
      <div class="checkrow"><input id="pcmode" type="checkbox" checked><label for="pcmode">PC/DPC mode</label></div>
      <div class="field full"><label>Tip</label><div class="small">Klik cell untuk mengisi board. Klik slot Current, Hold, atau slot Next kalau mau ubah target input. Secara default tombol piece akan langsung mengisi slot Next kosong berikutnya. Tombol utama sekarang langsung mencari lalu menaruh piece ke board tanpa langkah Apply terpisah.</div></div>
    </div>
    <div class="btns single"><button onclick="recommend()">Recommend + Place</button></div>
    <h3>Result</h3><div id="result" class="result">Belum ada rekomendasi.</div>
    <div id="plan" class="plan"></div>
  </div>
</div>
<script>
const H=20,W=10, PIECES='IJLOSTZ';
const PIECE_SHAPES={
  I:[[0,1],[1,1],[2,1],[3,1]],
  J:[[0,0],[0,1],[1,1],[2,1]],
  L:[[2,0],[0,1],[1,1],[2,1]],
  O:[[1,0],[2,0],[1,1],[2,1]],
  S:[[1,0],[2,0],[0,1],[1,1]],
  T:[[1,0],[0,1],[1,1],[2,1]],
  Z:[[0,0],[1,0],[1,1],[2,1]]
};
let board=[], recCells=[], openingState={id:0,order:'',finishOrder:''}, activeTarget='next-auto';
function init(){
  board=Array.from({length:H},()=>Array(W).fill('.'));
  renderPalette();
  syncPieceInputs();
  render();
}
function render(){
  const el=document.getElementById('board');
  el.innerHTML='';
  for(let y=0;y<H;y++){
    for(let x=0;x<W;x++){
      const c=document.createElement('div');
      c.className='cell';
      if(board[y][x]=='#') c.classList.add('filled');
      if(recCells.some(p=>p.X==x&&p.Y==y)) c.classList.add(board[y][x]=='#'?'conflict':'rec');
      c.onclick=()=>{
        board[y][x]=board[y][x]=='#'?'.':'#';
        recCells=[];
        openingState={id:0,order:'',finishOrder:''};
        render();
      };
      el.appendChild(c);
    }
  }
}
function boardStrings(){return board.map(r=>r.join(''));}
function setBoardFromStrings(rows){board=rows.map(s=>s.split('').map(ch=>ch=='#'?'#':'.'));}
function cleanPieceInput(value, allowPlaceholder){return (value||'').toUpperCase().replace(allowPlaceholder?/[^IJLOSTZ?]/g:/[^IJLOSTZ]/g,'');}
function normalizedNextValue(){
  const next=document.getElementById('next');
  let value=cleanPieceInput(next.value,true).slice(0,5);
  if(next.dataset.autofill==='1'&&value.length<5){ value=(value+'?????').slice(0,5); }
  return value;
}
function syncPieceInputs(){
  const current=document.getElementById('current');
  const next=document.getElementById('next');
  const hold=document.getElementById('hold');
  current.value=cleanPieceInput(current.value,false).slice(0,1);
  next.value=cleanPieceInput(next.value,true).slice(0,5);
  hold.value=cleanPieceInput(hold.value,false).slice(0,1);
  if(next.value.indexOf('?')===-1){ next.dataset.autofill='0'; }
  renderQueueInputs();
}
function renderPalette(){
  const pad=document.getElementById('piecepad');
  pad.innerHTML=PIECES.split('').map(function(piece){
    return '<button type="button" class="piecebtn" onclick="handlePiecePaletteInput(\''+piece+'\')">'+renderPieceMarkup(piece,'small')+'<span>'+piece+'</span></button>';
  }).join('')+
  '<button type="button" class="piecebtn alt" onclick="backspaceNextPiece()"><span style="font-size:22px;line-height:1">⌫</span><span>BACK</span></button>'+
  '<button type="button" class="piecebtn alt" onclick="clearNextPiece()"><span style="font-size:20px;line-height:1">×</span><span>CLEAR</span></button>';
}
function renderPieceMarkup(piece,sizeClass){
  const shape=PIECE_SHAPES[piece];
  if(!shape){ return ''; }
  const filled=new Set(shape.map(function(p){ return p[0]+','+p[1]; }));
  let html='';
  for(let y=0;y<4;y++){
    for(let x=0;x<4;x++){
      html+='<span class="mino'+(filled.has(x+','+y)?' fill':'')+'"></span>';
    }
  }
  return '<span class="piece-grid '+(sizeClass||'')+' piece-'+piece+'">'+html+'</span>';
}
function slotMarkup(value,sizeClass,emptyLabel){
  if(PIECE_SHAPES[value]){ return renderPieceMarkup(value,sizeClass); }
  if(value==='?'){ return '<span class="slot-placeholder">?</span>'; }
  return '<span class="slot-placeholder empty">'+emptyLabel+'</span>';
}
function resolveVisualTarget(nextValue){
  if(activeTarget==='current'||activeTarget==='hold'||activeTarget.startsWith('next:')){ return activeTarget; }
  const unknown=nextValue.indexOf('?');
  if(unknown>=0){ return 'next:'+unknown; }
  if(nextValue.length<5){ return 'next:'+nextValue.length; }
  return '';
}
function describeTarget(target){
  if(target==='current'){ return 'Target input: Current Piece'; }
  if(target==='hold'){ return 'Target input: Hold Piece'; }
  if(target.startsWith('next:')){ return 'Target input: Next slot '+(parseInt(target.split(':')[1],10)+1); }
  return 'Target input: Next queue sudah penuh. Klik slot mana yang mau diganti.';
}
function renderQueueInputs(){
  const currentValue=cleanPieceInput(document.getElementById('current').value,false).slice(0,1);
  const holdValue=cleanPieceInput(document.getElementById('hold').value,false).slice(0,1);
  const nextValue=normalizedNextValue();
  const target=resolveVisualTarget(nextValue);

  const currentSlot=document.getElementById('currentSlot');
  currentSlot.className='piece-slot big'+(target==='current'?' active':'')+(!currentValue?' placeholder':'');
  currentSlot.innerHTML=slotMarkup(currentValue,'','set');

  const holdSlot=document.getElementById('holdSlot');
  holdSlot.className='piece-slot big'+(target==='hold'?' active':'')+(!holdValue?' placeholder':'');
  holdSlot.innerHTML=slotMarkup(holdValue,'','hold');

  for(let i=0;i<5;i++){
    const ch=nextValue[i]||'';
    const slot=document.getElementById('nextSlot'+i);
    slot.className='piece-slot small'+(target==='next:'+i?' active':'')+(!ch?' placeholder':'');
    slot.innerHTML=slotMarkup(ch,'tiny','+');
  }
  document.getElementById('targetbadge').textContent=describeTarget(target);
}
function setActiveTarget(target){
  activeTarget=target;
  renderQueueInputs();
}
function focusNextUnknown(){
  const value=normalizedNextValue();
  const idx=value.indexOf('?');
  if(idx>=0){
    activeTarget='next:'+idx;
    renderQueueInputs();
    return true;
  }
  if(value.length<5){
    activeTarget='next:'+value.length;
    renderQueueInputs();
    return true;
  }
  return false;
}
function setNextSlot(idx,piece){
  const next=document.getElementById('next');
  const chars=normalizedNextValue().split('');
  while(chars.length<5){ chars.push('?'); }
  chars[idx]=piece;
  next.value=chars.join('').slice(0,5);
  next.dataset.autofill='1';
}
function handlePiecePaletteInput(piece){
  if(activeTarget==='current'){
    document.getElementById('current').value=piece;
    activeTarget='next-auto';
    syncPieceInputs();
    return;
  }
  if(activeTarget==='hold'){
    document.getElementById('hold').value=piece;
    activeTarget='next-auto';
    syncPieceInputs();
    return;
  }
  if(activeTarget.startsWith('next:')){
    setNextSlot(parseInt(activeTarget.split(':')[1],10),piece);
    activeTarget='next-auto';
    syncPieceInputs();
    return;
  }
  appendNextPiece(piece);
}
function appendNextPiece(piece){
  const next=document.getElementById('next');
  let value=normalizedNextValue();
  const idx=value.indexOf('?');
  if(idx>=0){
    value=value.slice(0,idx)+piece+value.slice(idx+1);
  } else if(value.length<5){
    value+=piece;
  } else {
    return;
  }
  next.value=value.slice(0,5);
  if(next.value.indexOf('?')===-1){ next.dataset.autofill='0'; }
  activeTarget='next-auto';
  syncPieceInputs();
}
function clearHoldPiece(){
  document.getElementById('hold').value='';
  activeTarget='hold';
  syncPieceInputs();
}
function backspaceNextPiece(){
  if(activeTarget==='current'){
    document.getElementById('current').value='';
    syncPieceInputs();
    return;
  }
  if(activeTarget==='hold'){
    document.getElementById('hold').value='';
    syncPieceInputs();
    return;
  }
  if(activeTarget.startsWith('next:')){
    const idx=parseInt(activeTarget.split(':')[1],10);
    const next=document.getElementById('next');
    const chars=normalizedNextValue().split('');
    while(chars.length<5){ chars.push('?'); }
    chars[idx]='?';
    next.value=chars.join('').slice(0,5);
    next.dataset.autofill='1';
    syncPieceInputs();
    return;
  }
  const next=document.getElementById('next');
  let value=normalizedNextValue();
  if(next.dataset.autofill==='1'){
    const firstUnknown=value.indexOf('?');
    const knownEnd=firstUnknown>=0 ? firstUnknown : value.length;
    if(knownEnd===0){ return; }
    value=value.slice(0,knownEnd-1)+'?'+value.slice(knownEnd);
  } else {
    if(value.length===0){ return; }
    value=value.slice(0,value.length-1);
  }
  next.value=value;
  syncPieceInputs();
}
function clearNextPiece(){
  if(activeTarget==='current'){
    document.getElementById('current').value='';
    syncPieceInputs();
    return;
  }
  if(activeTarget==='hold'){
    document.getElementById('hold').value='';
    syncPieceInputs();
    return;
  }
  if(activeTarget.startsWith('next:')){
    const idx=parseInt(activeTarget.split(':')[1],10);
    const next=document.getElementById('next');
    const chars=normalizedNextValue().split('');
    while(chars.length<5){ chars.push('?'); }
    chars[idx]='?';
    next.value=chars.join('').slice(0,5);
    next.dataset.autofill='1';
    syncPieceInputs();
    return;
  }
  const next=document.getElementById('next');
  next.dataset.autofill='0';
  next.value='';
  activeTarget='next-auto';
  syncPieceInputs();
}
function inferResidueFromVisible(visible){
  let seen=new Set();
  for(const p of visible){
    if(seen.has(p)){ seen=new Set(); }
    seen.add(p);
  }
  let residue='';
  for(const p of PIECES){ if(!seen.has(p)){ residue+=p; } }
  return residue;
}
function buildAutoAdvancedInputs(ctx){
  const current=cleanPieceInput(ctx.current,false);
  const next=cleanPieceInput(ctx.next,false);
  const hold=cleanPieceInput(ctx.hold,false);
  if(current.length!==1||next.length!==5){ return null; }
  let nextCurrent='', nextKnown='', nextMissing=0, nextHold=hold;
  switch(ctx.source){
    case 'ACTIVE':
      nextCurrent=next.slice(0,1);
      nextKnown=next.slice(1);
      nextMissing=1;
      break;
    case 'HOLD_SWAP':
      nextCurrent=next.slice(0,1);
      nextKnown=next.slice(1);
      nextHold=current;
      nextMissing=1;
      break;
    case 'HOLD_EMPTY':
      nextCurrent=next.slice(1,2);
      nextKnown=next.slice(2);
      nextHold=current;
      nextMissing=2;
      break;
    default:
      return null;
  }
  const residue=inferResidueFromVisible(current+next);
  if(residue.length===1&&nextMissing>0&&nextKnown.length<5){
    nextKnown+=residue;
    nextMissing--;
  }
  return {current:nextCurrent, next:(nextKnown+'?'.repeat(nextMissing)).slice(0,5), hold:nextHold, nextMissing:nextMissing, source:ctx.source};
}
function applyAutoAdvancedInputs(state){
  if(!state){ return; }
  document.getElementById('current').value=state.current||'';
  document.getElementById('hold').value=state.hold||'';
  const next=document.getElementById('next');
  next.value=state.next||'';
  next.dataset.autofill=next.value.indexOf('?')>=0?'1':'0';
  activeTarget='next-auto';
  syncPieceInputs();
  focusNextUnknown();
}
function buildAppliedMessage(state){
  if(!state){ return 'Move langsung ditempatkan ke board.'; }
  if(state.nextMissing===0){ return 'Move langsung ditempatkan. Queue maju otomatis; current/next/hold sudah diperbarui dan preview berikutnya sudah lengkap.'; }
  if(state.nextMissing===1){ return 'Move langsung ditempatkan. Queue maju otomatis; isi 1 slot ? di Next 5.'; }
  return 'Move langsung ditempatkan. Queue maju otomatis; move ini memakai hold kosong, jadi isi 2 slot ? di Next 5.';
}
function clearBoard(){board=Array.from({length:H},()=>Array(W).fill('.')); recCells=[]; openingState={id:0,order:'',finishOrder:''}; render(); document.getElementById('result').textContent='Board cleared.'; document.getElementById('plan').innerHTML='';}
function sampleBoard(){clearBoard(); const rows=['..........','..........','..........','..........','..........','..........','..........','..........','..........','..........','..........','..........','..........','..........','..........','..........','........#.','.......###','######.###','######.###']; setBoardFromStrings(rows); render();}
async function recommend(){
  syncPieceInputs();
  recCells=[]; render();
  const current=cleanPieceInput(document.getElementById('current').value,false).slice(0,1);
  const nextRaw=cleanPieceInput(document.getElementById('next').value,true).slice(0,5);
  const next=cleanPieceInput(nextRaw,false).slice(0,5);
  const hold=cleanPieceInput(document.getElementById('hold').value,false).slice(0,1);
  if(current.length!==1){ document.getElementById('result').innerHTML='<span class="error">Current Piece harus 1 block valid.</span>'; document.getElementById('plan').innerHTML=''; return; }
  if(nextRaw.indexOf('?')>=0||next.length!==5){ document.getElementById('result').innerHTML='<span class="error">Isi dulu semua slot 5 Next Pieces sebelum recommend.</span>'; document.getElementById('plan').innerHTML=''; focusNextUnknown(); return; }
  const payload={board:boardStrings(), current:current, next:next, hold:hold, depth:parseInt(document.getElementById('depth').value||'10'), beam:parseInt(document.getElementById('beam').value||'220'), pcMode:document.getElementById('pcmode').checked, openingId:openingState.id||0, openingOrder:openingState.order||'', finishOrder:openingState.finishOrder||''};
  document.getElementById('result').textContent='Searching...'; document.getElementById('plan').innerHTML='';
  const res=await fetch('/api/recommend',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
  const data=await res.json();
  if(!data.ok){document.getElementById('result').innerHTML='<span class="error">'+data.error+'</span>'; return;}
  openingState={id:data.openingId||0,order:data.openingOrder||'',finishOrder:data.finishOrder||''};
  const m=data.move;
  const advanced=buildAutoAdvancedInputs({current:payload.current,next:payload.next,hold:payload.hold,source:m.source});
  setBoardFromStrings(data.boardAfter||boardStrings());
  recCells=(m.lines===0&&!m.perfectClear)?(m.cells||[]):[];
  render();
  applyAutoAdvancedInputs(advanced);
  document.getElementById('result').textContent =
    'PLACED MOVE\n' +
    'Piece       : ' + m.piece + '\n' +
    'Source      : ' + m.source + '\n' +
    'Rotation    : ' + m.rotation + '\n' +
    'X, Y        : ' + m.x + ', ' + m.y + '\n' +
    'Lines       : ' + m.lines + '\n' +
    'PerfectClear: ' + m.perfectClear + '\n' +
    'Immediate   : ' + m.immediate + '\n' +
    'Eval        : ' + (m.eval && m.eval.toFixed ? m.eval.toFixed(2) : m.eval) + '\n' +
    'Explored    : ' + data.exploredStates + '\n\n' +
    'STATUS\n' + buildAppliedMessage(advanced) + '\n\n' +
    '7-BAG INFO\n' + data.bagInfo + '\n\n' +
    'WHY\n' + data.reason;
  const plan=document.getElementById('plan');
  plan.innerHTML='<h3>Planned line</h3>'+(data.plan||[]).slice(0,8).map(function(p,i){
    return '<div class="move"><b>'+(i+1)+'. '+p.piece+'</b> '+p.source+', rot='+p.rotation+', x='+p.x+', y='+p.y+', lines='+p.lines+', PC='+p.perfectClear+', +'+p.immediate+'</div>';
  }).join('');
}
init();
</script>
</body>
</html>`
