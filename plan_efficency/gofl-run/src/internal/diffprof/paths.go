package diffprof

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"gofl/internal/pprofread"
)

// PathsOptions cấu hình Paths.
type PathsOptions struct {
	Method   string // paper | stack | auto (mặc định paper)
	Entry    string // chuỗi điểm vào; rỗng = gốc cây (chế độ 2 pprof không có benchmark)
	MaxPaths int    // N: số đường tối đa mỗi hotspot (mặc định 10)

	// BaseFactor and ScaledFactor turn the seconds of each profile into
	// seconds per benchmark operation (diffprof.Scale) before the scores are
	// taken, so that they have the unit of hotspots ranked per operation.
	// Both 0 means raw seconds (contract v0). Scores are then rounded to 9
	// places instead of 6, because per-operation values are small.
	BaseFactor, ScaledFactor float64
}

// factors returns the factors to use and the places to round scores to.
func (o PathsOptions) factors() (fb, fs float64, places int) {
	if o.BaseFactor == 0 && o.ScaledFactor == 0 {
		return 1, 1, 6
	}
	return o.BaseFactor, o.ScaledFactor, 9
}

// PathsResult là kết quả của Paths.
type PathsResult struct {
	Method string         // phương pháp thực sự đã dùng (ghi vào run.json: paths_method)
	Paths  []CriticalPath // nội dung critical_paths.json
	Stats  PathsStats
}

// PathsStats: số liệu phụ để quyết định có cần đổi sang cách 2 không.
type PathsStats struct {
	EntryUsed       string         `json:"entry_used"`                // tên đầy đủ nút điểm vào, hoặc "<root>"
	EntryNodes      int            `json:"entry_nodes,omitempty"`     // cách 1: số nút điểm vào được dùng
	Candidates      map[string]int `json:"candidates_per_target"`     // số đường (sau gộp, trước khi cắt N)
	TruncatedTarget int            `json:"truncated_targets"`         // số hotspot bị cắt bớt
	OutsideEntry    int            `json:"stacks_outside_entry"`      // cách 2: stack không chứa điểm vào
	FallbackReason  string         `json:"fallback_reason,omitempty"` // auto: lý do đổi sang cách 2
	Occurrences     map[string]int `json:"occurrences_per_target"`    // cách 1: số lần xuất hiện ở scaled
}

// Paths trích các đường đi quan trọng từ điểm vào xuống mỗi hotspot.
func Paths(base, scaled *pprofread.Profile, hotspots []Hotspot, opt PathsOptions) (*PathsResult, error) {
	if opt.MaxPaths <= 0 {
		opt.MaxPaths = 10
	}
	switch opt.Method {
	case "", MethodPaper:
		return pathsPaper(base, scaled, hotspots, opt)
	case MethodStack:
		return pathsStack(base, scaled, hotspots, opt)
	case MethodAuto:
		r, err := pathsPaper(base, scaled, hotspots, opt)
		var pe *Error
		if errors.As(err, &pe) && pe.Code == CodeEntryNotCovering {
			r2, err2 := pathsStack(base, scaled, hotspots, opt)
			if err2 != nil {
				return nil, err2
			}
			r2.Stats.FallbackReason = pe.Error()
			return r2, nil
		}
		return r, err
	}
	return nil, fmt.Errorf("phương pháp không hợp lệ: %q (paper|stack|auto)", opt.Method)
}

// candidate là một đường trước khi gộp.
type candidate struct {
	target    string
	rank      int
	signature string
	score     float64
	frames    []PathFrame
	tiebreak  string // phá hoà trong cùng chữ ký (thay cho "thời điểm sớm nhất" của paper)
}

func hotKey(h Hotspot) pprofread.Key { return pprofread.Key{Name: h.Function, File: h.File} }

// ---------- Cách 1: giống paper ----------

func pathsPaper(base, scaled *pprofread.Profile, hotspots []Hotspot, opt PathsOptions) (*PathsResult, error) {
	ts, tb := scaled.Tree(), base.Tree()
	fb, fs, places := opt.factors()
	res := &PathsResult{Method: MethodPaper, Stats: PathsStats{
		Candidates: map[string]int{}, Occurrences: map[string]int{}}}

	// 1. Lần xuất hiện: mọi nút của cây scaled trùng một hotspot.
	var all []*pprofread.Node
	occ := map[string][]*pprofread.Node{}
	for _, h := range hotspots {
		nodes := ts.ByFunc[hotKey(h)]
		occ[h.Function] = nodes
		res.Stats.Occurrences[h.Function] = len(nodes)
		all = append(all, nodes...)
	}

	// 2. Chọn điểm vào (như _pick_entry khi có entry_match): trong các nút
	// khớp, lấy nút có tổng thời gian lớn nhất mà bao trùm mọi lần xuất hiện.
	entries, err := pickEntry(ts, opt.Entry, all)
	if err != nil {
		return nil, err
	}
	switch {
	case entries[0].IsRoot():
		res.Stats.EntryUsed = "<root>"
	case len(entries) == 1:
		res.Stats.EntryUsed = entries[0].Name
	default:
		res.Stats.EntryUsed = fmt.Sprintf("%s (hợp của %d nút)", entries[0].Name, len(entries))
	}
	res.Stats.EntryNodes = len(entries)

	meanCache := map[pprofread.Key]float64{}
	baseMean := func(k pprofread.Key) float64 {
		v, ok := meanCache[k]
		if !ok {
			v = tb.MeanSelf(k)
			meanCache[k] = v
		}
		return v
	}

	var cands []candidate
	for _, h := range hotspots {
		for _, n := range occ[h.Function] {
			entry := outermostEntry(entries, n)
			path := pprofread.PathFrom(entry, n)
			if entry.IsRoot() {
				path = trimLeadingNoise(path)
			}
			if len(path) == 0 {
				continue
			}
			// 4. score = Σ (self(nút) − trung bình self của hàm ở baseline)
			var score float64
			frames := make([]PathFrame, len(path))
			var lines []string
			for i, p := range path {
				score += p.Self*fs - baseMean(p.Key())*fb
				frames[i] = PathFrame{Name: pprofread.ShortName(p.Name), File: p.File, Line: p.Line()}
				lines = append(lines, fmt.Sprint(frames[i].Line))
			}
			cands = append(cands, candidate{
				target: h.Function, rank: h.Rank, signature: signature(frames),
				score: score, frames: frames, tiebreak: strings.Join(lines, ","),
			})
		}
	}
	res.Paths = groupSortLimit(cands, opt.MaxPaths, places, &res.Stats)
	return res, nil
}

// pickEntry chọn (các) nút điểm vào.
//
//  1. Giống paper: trong các nút khớp, nút có tổng thời gian lớn nhất mà một
//     mình bao trùm mọi lần xuất hiện.
//  2. Bổ sung cho Go: gói testing gọi closure benchmark từ HAI ngữ cảnh
//     (lần dò b.N=1 qua (*B).run1 và lần chạy chính qua (*B).launch), nên
//     hàm điểm vào có nhiều nút. Nếu bước 1 thất bại, dùng HỢP các nút khớp,
//     miễn mỗi lần xuất hiện nằm dưới ít nhất một nút.
//  3. Không thoả → ENTRY_NOT_COVERING.
func pickEntry(ts *pprofread.Tree, entry string, occ []*pprofread.Node) ([]*pprofread.Node, error) {
	if entry == "" {
		return []*pprofread.Node{ts.Root}, nil
	}
	cands := ts.FindEntries(entry)
	if len(cands) == 0 {
		return nil, &Error{CodeEntryNotCovering, fmt.Sprintf("không có nút nào khớp điểm vào %q (hàm nóng có thể chạy trong goroutine riêng)", entry)}
	}
	for _, c := range cands {
		covered := true
		for _, o := range occ {
			if !c.IsAncestorOf(o) {
				covered = false
				break
			}
		}
		if covered {
			return []*pprofread.Node{c}, nil
		}
	}
	outside := 0
	for _, o := range occ {
		if outermostEntry(cands, o) == nil {
			outside++
		}
	}
	if outside == 0 {
		return cands, nil
	}
	return nil, &Error{CodeEntryNotCovering, fmt.Sprintf(
		"điểm vào %q (%d nút khớp) không bao trùm mọi lần xuất hiện: %d/%d lần xuất hiện nằm ngoài",
		entry, len(cands), outside, len(occ))}
}

// outermostEntry: nút điểm vào ngoài cùng (gần gốc nhất) là tổ tiên của n.
func outermostEntry(entries []*pprofread.Node, n *pprofread.Node) *pprofread.Node {
	set := make(map[*pprofread.Node]bool, len(entries))
	for _, e := range entries {
		set[e] = true
	}
	var found *pprofread.Node
	for x := n; x != nil; x = x.Parent {
		if set[x] {
			found = x
		}
	}
	return found
}

func trimLeadingNoise(path []*pprofread.Node) []*pprofread.Node {
	i := 0
	for i < len(path)-1 && pprofread.IsNoise(path[i].Name) {
		i++
	}
	return path[i:]
}

// ---------- Cách 2: theo stack, dự phòng ----------

func pathsStack(base, scaled *pprofread.Profile, hotspots []Hotspot, opt PathsOptions) (*PathsResult, error) {
	fb, fs, places := opt.factors()
	res := &PathsResult{Method: MethodStack, Stats: PathsStats{Candidates: map[string]int{}}}
	if opt.Entry == "" {
		res.Stats.EntryUsed = "<root>"
	} else {
		res.Stats.EntryUsed = opt.Entry
	}
	type agg struct {
		seconds    float64
		rep        []pprofread.Frame
		repSamples int64
	}
	collect := func(p *pprofread.Profile, k pprofread.Key, countOutside bool) map[string]*agg {
		m := map[string]*agg{}
		for _, s := range p.Stacks {
			if s.Leaf().Key() != k {
				continue
			}
			fr, inside := cutAtEntry(s.Frames, opt.Entry)
			if countOutside && !inside {
				res.Stats.OutsideEntry++
			}
			sig := signature(toPathFrames(fr))
			a, ok := m[sig]
			if !ok {
				a = &agg{}
				m[sig] = a
			}
			a.seconds += s.Seconds
			if s.Samples > a.repSamples {
				a.rep, a.repSamples = fr, s.Samples
			}
		}
		return m
	}
	var cands []candidate
	for _, h := range hotspots {
		sm := collect(scaled, hotKey(h), true)
		bm := collect(base, hotKey(h), false)
		for sig, a := range sm {
			var b float64
			if x, ok := bm[sig]; ok {
				b = x.seconds
			}
			cands = append(cands, candidate{
				target: h.Function, rank: h.Rank, signature: sig,
				score: a.seconds*fs - b*fb, frames: toPathFrames(a.rep),
			})
		}
	}
	res.Paths = groupSortLimit(cands, opt.MaxPaths, places, &res.Stats)
	return res, nil
}

// cutAtEntry cắt stack từ khung điểm vào (khung khớp ngoài cùng) xuống lá.
// Không có điểm vào trong stack (hoặc entry rỗng): bắt đầu từ khung đầu
// tiên không phải runtime/testing.
func cutAtEntry(fr []pprofread.Frame, entry string) ([]pprofread.Frame, bool) {
	if entry != "" {
		for i, f := range fr {
			if pprofread.MatchEntry(f.Name, entry) {
				return fr[i:], true
			}
		}
	}
	i := 0
	for i < len(fr)-1 && pprofread.IsNoise(fr[i].Name) {
		i++
	}
	return fr[i:], entry == ""
}

func toPathFrames(fr []pprofread.Frame) []PathFrame {
	out := make([]PathFrame, len(fr))
	for i, f := range fr {
		out[i] = PathFrame{Name: pprofread.ShortName(f.Name), File: f.File, Line: f.Line}
	}
	return out
}

// ---------- Dùng chung ----------

// signature = nối "tệp(hàm)" các khung bằng " -> " (mục 3.3).
func signature(fr []PathFrame) string {
	parts := make([]string, len(fr))
	for i, f := range fr {
		parts[i] = f.File + "(" + f.Name + ")"
	}
	return strings.Join(parts, " -> ")
}

// groupSortLimit: gộp cùng (target, signature) giữ score cao nhất; sắp theo
// (target_rank, score giảm dần, signature); giữ tối đa N đường mỗi target.
func groupSortLimit(cands []candidate, maxN, places int, st *PathsStats) []CriticalPath {
	type gk struct{ target, sig string }
	best := map[gk]candidate{}
	var keys []gk
	for _, c := range cands {
		k := gk{c.target, c.signature}
		b, ok := best[k]
		if !ok {
			keys = append(keys, k)
			best[k] = c
			continue
		}
		if c.score > b.score || (c.score == b.score && c.tiebreak < b.tiebreak) {
			best[k] = c
		}
	}
	list := make([]candidate, 0, len(keys))
	for _, k := range keys {
		list = append(list, best[k])
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.score != b.score {
			return a.score > b.score
		}
		return a.signature < b.signature
	})
	count := map[string]int{}
	for _, c := range list {
		count[c.target]++
	}
	var out []CriticalPath
	kept := map[string]int{}
	for _, c := range list {
		st.Candidates[c.target] = count[c.target]
		if kept[c.target] >= maxN {
			continue
		}
		kept[c.target]++
		out = append(out, CriticalPath{
			Target: c.target, TargetRank: c.rank, Signature: c.signature,
			Score: roundTo(c.score, places), Truncated: count[c.target] > maxN, Frames: c.frames,
		})
	}
	for t, n := range count {
		if n > maxN {
			st.TruncatedTarget++
		}
		_ = t
	}
	return out
}

func roundTo(x float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(x*p) / p
}
