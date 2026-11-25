// Package crashrank groups crashes and ranks suspicious files and functions.
//
// Owner: TV2. Paper: C, sections II-A and II-B (claims C1-C7).
// The concrete IAD, IBF and FF formulas are NOT in the paper (claim C5):
// they come from the CrashLocator family (reference [21] of paper C) and are
// fixed here. For a crash group B, a file f, G groups in total, G_f groups
// that contain f, and d_i(f) the distance (number of frames after
// normalisation) from the crash point to the nearest frame of f in trace i:
//
//	FF(f,B)  = |{traces of B that contain f}| / |B|
//	IBF(f)   = ln(1 + G / G_f)
//	IAD(f,B) = 1 / (1 + mean of d_i(f) over the traces of B that contain f)
//	Score    = IAD * IBF * FF
//
// With one group IBF is a constant and with one trace FF is 1, so the order
// is then the order of the stack (claim C15: ranking pays off with many
// crashes, or when the stack is incomplete). Frames of the Go installation
// are never ranked. Frames of *_test.go files are not ranked either (a test
// is almost never the place to fix), unless no trace of the group has any
// other user frame: then the test files are all there is, and they are ranked.
//
// Functions (C7): for a function fn of file f, with share(fn) the fraction of
// the traces of B that contain fn and dist(fn) its nearest distance to the
// crash point,
//
//	w(fn)     = share(fn) / (1 + dist(fn)) / max of that over the functions of f
//	Score(fn) = Score(f) * w(fn)
//
// so the best function of a file carries the file score and the others less.
// Findings are sorted by Score(fn); since the first finding of every file has
// the file score, the order of files is that of the file ranking. At most
// MaxFuncsPerFile functions are kept per file, plus any function tied with
// the last one kept (a cut inside a tie would depend on function names).
package crashrank

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gofl/internal/crashsig"
	"gofl/internal/finding"
)

var ErrNotImplemented = errors.New("crashrank: not implemented yet")

// Limits on the size of a ranking.
const (
	MaxFiles         = 10 // files reported per group
	MaxFuncsPerFile  = 3  // functions reported per file
	EvidenceStackRnk = "stack-rank"
	EvidenceGroup    = "crash-group"
)

// Event is one observed crash.
type Event struct {
	Time      time.Time
	Signature crashsig.Signature
	Source    string // e.g. "go test", "stderr", log file name
}

// Group is a set of crashes considered the same underlying fault.
type Group struct {
	ID     string
	Level  int // 1..4, the grouping level that merged them
	Events []Event
}

// GroupEvents applies the four grouping levels (C1-C4):
//
//	1  identical stacks (same frames with lines, same message)
//	2  equal after crashsig.Normalize
//	3  one normalised stack contains the other (crashsig.Subsumes)
//	4  same file at the crash point
//
// Groups are merged level by level with union-find. Group.Level is the
// highest level that merged anything into the group (1 for a single event).
// Groups come out largest first; ties by ID, so the result is deterministic.
func GroupEvents(events []Event) ([]Group, error) {
	n := len(events)
	if n == 0 {
		return nil, nil
	}
	norm := make([]crashsig.Signature, n)
	for i, e := range events {
		norm[i] = crashsig.Normalize(e.Signature)
	}
	uf := newUF(n)
	level := make([]int, n) // level of the last merge, per root (resolved at the end)
	merge := func(a, b, lv int) {
		ra, rb := uf.find(a), uf.find(b)
		if ra == rb {
			return
		}
		la, lb := level[ra], level[rb]
		root := uf.union(ra, rb)
		level[root] = max(la, lb, lv)
	}
	for i := range level {
		level[i] = 1
	}

	// Level 1: identical raw signature.
	byKey(events, func(i int) string { return "1|" + crashsig.Hash(events[i].Signature) }, func(a, b int) { merge(a, b, 1) })
	// Level 2: identical normalised signature.
	byKey(events, func(i int) string { return "2|" + crashsig.Hash(norm[i]) }, func(a, b int) { merge(a, b, 2) })
	// Level 3: containment, function names only. Distinct normalised
	// signatures are compared once each.
	reps := map[string]int{}
	var order []int
	for i := range norm {
		h := crashsig.Hash(norm[i])
		if _, ok := reps[h]; !ok {
			reps[h] = i
			order = append(order, i)
		}
	}
	for x := 0; x < len(order); x++ {
		for y := x + 1; y < len(order); y++ {
			a, b := order[x], order[y]
			if crashsig.Subsumes(norm[a], norm[b]) || crashsig.Subsumes(norm[b], norm[a]) {
				merge(a, b, 3)
			}
		}
	}
	// Level 4: same crash-point file.
	byKey(events, func(i int) string {
		cp, ok := norm[i].CrashPoint()
		if !ok || cp.File == "" {
			return ""
		}
		return "4|" + cp.File
	}, func(a, b int) { merge(a, b, 4) })

	idx := map[int][]int{}
	for i := 0; i < n; i++ {
		r := uf.find(i)
		idx[r] = append(idx[r], i)
	}
	groups := make([]Group, 0, len(idx))
	for r, members := range idx {
		g := Group{Level: level[r]}
		for _, i := range members {
			g.Events = append(g.Events, events[i])
		}
		g.ID = crashsig.Hash(norm[members[0]])
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Events) != len(groups[j].Events) {
			return len(groups[i].Events) > len(groups[j].Events)
		}
		return groups[i].ID < groups[j].ID
	})
	return groups, nil
}

// byKey calls join(first, i) for every event i whose key equals the key of an
// earlier event first. Empty keys never match.
func byKey(events []Event, key func(i int) string, join func(a, b int)) {
	first := map[string]int{}
	for i := range events {
		k := key(i)
		if k == "" {
			continue
		}
		if f, ok := first[k]; ok {
			join(f, i)
		} else {
			first[k] = i
		}
	}
}

type unionFind struct{ parent []int }

func newUF(n int) *unionFind {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &unionFind{p}
}

func (u *unionFind) find(x int) int {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b int) int {
	u.parent[b] = a
	return a
}

// frameInfo is what one trace says about one function of one file.
type frameInfo struct {
	dist int // frames from the crash point to the nearest occurrence
	line int
}

// trace is one event reduced to what ranking needs.
type trace struct {
	files map[string]int                  // file -> nearest distance
	funcs map[string]map[string]frameInfo // file -> function -> info
}

// isTestFile reports Go test files (frames of the test functions themselves).
func isTestFile(file string) bool { return strings.HasSuffix(file, "_test.go") }

// toTrace keeps the user frames of sig. Frames of test files are dropped
// unless keepTests is set. Distances still count every normalised frame, so
// dropping a frame does not move the others closer to the crash point.
func toTrace(sig crashsig.Signature, keepTests bool) trace {
	t := trace{files: map[string]int{}, funcs: map[string]map[string]frameInfo{}}
	for d, f := range crashsig.Normalize(sig).Frames {
		if f.File == "" || crashsig.IsStdlibFile(f.File) || (!keepTests && isTestFile(f.File)) {
			continue
		}
		if cur, ok := t.files[f.File]; !ok || d < cur {
			t.files[f.File] = d
		}
		m := t.funcs[f.File]
		if m == nil {
			m = map[string]frameInfo{}
			t.funcs[f.File] = m
		}
		if cur, ok := m[f.Func]; !ok || d < cur.dist {
			m[f.Func] = frameInfo{dist: d, line: f.Line}
		}
	}
	return t
}

// Rank scores files of one group by IAD * IBF * FF against all groups (C5, C6)
// and suggests functions by frequency inside the top files (C7).
func Rank(g Group, all []Group) ([]finding.Finding, error) {
	if len(g.Events) == 0 {
		return nil, errors.New("crashrank: empty group")
	}
	traces := make([]trace, len(g.Events))
	keepTests := false
	for i, e := range g.Events {
		traces[i] = toTrace(e.Signature, false)
	}
	if !anyFile(traces) { // only test files (or nothing) above the stdlib
		keepTests = true
		for i, e := range g.Events {
			traces[i] = toTrace(e.Signature, true)
		}
	}

	// Number of groups that contain each file (any event of the group).
	total := len(all)
	groupsWith := map[string]int{}
	seenSelf := false
	for _, h := range all {
		if h.ID == g.ID {
			seenSelf = true
		}
		in := map[string]bool{}
		for _, e := range h.Events {
			// test files included: G_f of a file only counts that file
			for f := range toTrace(e.Signature, true).files {
				in[f] = true
			}
		}
		for f := range in {
			groupsWith[f]++
		}
	}
	if !seenSelf { // g is not part of all: count it, so G_f >= 1 for its files
		total++
		in := map[string]bool{}
		for _, t := range traces {
			for f := range t.files {
				in[f] = true
			}
		}
		for f := range in {
			groupsWith[f]++
		}
	}

	type fileScore struct {
		file           string
		score, ff, ibf float64
		iad, minDist   float64
		traces         int
		funcs          []funcScore
	}
	files := map[string]*fileScore{}
	for _, t := range traces {
		for f, d := range t.files {
			fs := files[f]
			if fs == nil {
				fs = &fileScore{file: f, minDist: math.MaxFloat64}
				files[f] = fs
			}
			fs.traces++
			fs.iad += float64(d) // sum now, mean below
			fs.minDist = math.Min(fs.minDist, float64(d))
		}
	}
	var ranked []*fileScore
	for f, fs := range files {
		mean := fs.iad / float64(fs.traces)
		fs.iad = 1 / (1 + mean)
		fs.ff = float64(fs.traces) / float64(len(traces))
		gf := groupsWith[f]
		if gf < 1 {
			gf = 1
		}
		fs.ibf = math.Log(1 + float64(total)/float64(gf))
		fs.score = fs.iad * fs.ibf * fs.ff
		fs.funcs = rankFuncs(f, traces)
		ranked = append(ranked, fs)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if d := a.score - b.score; math.Abs(d) > 1e-12 {
			return d > 0
		}
		if a.minDist != b.minDist {
			return a.minDist < b.minDist
		}
		return a.file < b.file
	})
	if len(ranked) > MaxFiles {
		ranked = ranked[:MaxFiles]
	}

	type cand struct {
		f        finding.Finding
		fileRank int
		funcRank int
	}
	var cands []cand
	for fi, fs := range ranked {
		for k, fn := range keepFuncs(fs.funcs) {
			score := fs.score * fn.weight
			detail := fmt.Sprintf(
				"score %.3f = file score %.3f (IAD %.2f x IBF %.2f x FF %.2f) x function weight %.2f; function in %d/%d traces, %d frame(s) from the crash point",
				score, fs.score, fs.iad, fs.ibf, fs.ff, fn.weight, fn.traces, len(traces), fn.dist)
			if keepTests {
				detail += "; only test files on the stack"
			}
			cands = append(cands, cand{
				f: finding.Finding{
					Location: finding.Location{File: fs.file, Line: fn.line, Func: fn.name},
					Score:    score,
					Evidence: []finding.Evidence{
						{Kind: EvidenceStackRnk, Detail: detail},
						{Kind: EvidenceGroup, Detail: fmt.Sprintf("group %s: %d event(s), merged at level %d", g.ID, len(g.Events), g.Level)},
					},
				},
				fileRank: fi, funcRank: k,
			})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if d := a.f.Score - b.f.Score; math.Abs(d) > 1e-12 {
			return d > 0
		}
		if a.fileRank != b.fileRank {
			return a.fileRank < b.fileRank
		}
		return a.funcRank < b.funcRank
	})
	out := make([]finding.Finding, len(cands))
	for i, c := range cands {
		c.f.Rank = i + 1
		out[i] = c.f
	}
	return out, nil
}

// anyFile reports whether some trace kept at least one file.
func anyFile(traces []trace) bool {
	for _, t := range traces {
		if len(t.files) > 0 {
			return true
		}
	}
	return false
}

// keepFuncs returns the first MaxFuncsPerFile functions of an ordered list,
// plus every later one tied with the last kept.
func keepFuncs(fns []funcScore) []funcScore {
	n := min(MaxFuncsPerFile, len(fns))
	for n > 0 && n < len(fns) && math.Abs(fns[n].weight-fns[n-1].weight) <= 1e-12 {
		n++
	}
	return fns[:n]
}

type funcScore struct {
	name   string
	traces int
	dist   int
	line   int
	weight float64 // share / (1 + dist), normalised to 1 for the best function
}

// rankFuncs weights the functions of file f (see the package doc) and orders
// them by weight, then trace share, then distance, then name.
func rankFuncs(f string, traces []trace) []funcScore {
	agg := map[string]*funcScore{}
	for _, t := range traces {
		for name, info := range t.funcs[f] {
			fs := agg[name]
			if fs == nil {
				fs = &funcScore{name: name, dist: info.dist, line: info.line}
				agg[name] = fs
			}
			fs.traces++
			if info.dist < fs.dist {
				fs.dist, fs.line = info.dist, info.line
			}
		}
	}
	out := make([]funcScore, 0, len(agg))
	best := 0.0
	for _, fs := range agg {
		fs.weight = float64(fs.traces) / float64(len(traces)) / float64(1+fs.dist)
		best = math.Max(best, fs.weight)
		out = append(out, *fs)
	}
	for i := range out {
		if best > 0 {
			out[i].weight /= best
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if d := out[i].weight - out[j].weight; math.Abs(d) > 1e-12 {
			return d > 0
		}
		if out[i].traces != out[j].traces {
			return out[i].traces > out[j].traces
		}
		if out[i].dist != out[j].dist {
			return out[i].dist < out[j].dist
		}
		return out[i].name < out[j].name
	})
	return out
}
