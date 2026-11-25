package diffprof_test

import (
	"gofl/internal/contract"
	"gofl/internal/diffprof"
	"math"
	"sort"

	"gofl/internal/pprofread"
)

// ReferenceHotspots là bản Compute TỐI GIẢN theo mục 4.1, chỉ để Đức Anh tự
// sinh hotspots.json khi kiểm thử Paths mà không phải chờ Minh Long.
// KHÔNG phải sản phẩm giao; khi ghép dùng diffprof.Compute của Minh Long.
func ReferenceHotspots(base, scaled *pprofread.Profile, k int, extraNoise ...string) ([]diffprof.Hotspot, error) {
	if k <= 0 {
		k = 5
	}
	type row struct {
		key        pprofread.Key
		bs, ss     int64
		bsec, ssec float64
	}
	m := map[pprofread.Key]*row{}
	get := func(key pprofread.Key) *row {
		r, ok := m[key]
		if !ok {
			r = &row{key: key}
			m[key] = r
		}
		return r
	}
	for _, f := range base.SelfTable() {
		if !pprofread.IsNoiseWith(f.Function, extraNoise) {
			r := get(pprofread.Key{Name: f.Function, File: f.File})
			r.bs, r.bsec = f.Samples, f.Seconds
		}
	}
	for _, f := range scaled.SelfTable() {
		if !pprofread.IsNoiseWith(f.Function, extraNoise) {
			r := get(pprofread.Key{Name: f.Function, File: f.File})
			r.ss, r.ssec = f.Samples, f.Seconds
		}
	}
	var pos []*row
	var denom float64
	for _, r := range m {
		if d := r.ssec - r.bsec; d > 0 {
			pos = append(pos, r)
			denom += d
		}
	}
	if len(pos) == 0 {
		return nil, &diffprof.Error{Code: contract.NoPositiveDelta, Msg: "không có hàm nào có Δ > 0"}
	}
	sort.Slice(pos, func(i, j int) bool {
		di, dj := pos[i].ssec-pos[i].bsec, pos[j].ssec-pos[j].bsec
		if di != dj {
			return di > dj
		}
		if pos[i].key.Name != pos[j].key.Name {
			return pos[i].key.Name < pos[j].key.Name
		}
		return pos[i].key.File < pos[j].key.File
	})
	if len(pos) > k {
		pos = pos[:k]
	}
	tb, ts := base.Tree(), scaled.Tree()
	out := make([]diffprof.Hotspot, len(pos))
	for i, r := range pos {
		bo, so := tb.Occurrences(r.key), ts.Occurrences(r.key)
		d := r.ssec - r.bsec
		out[i] = diffprof.Hotspot{
			Rank: i + 1, Function: r.key.Name, File: r.key.File,
			BaselineSelfSeconds: round6x(r.bsec), ScaledSelfSeconds: round6x(r.ssec),
			DeltaSelfSeconds: round6x(d), NDC: round6x(d / denom),
			BaselineSamples: r.bs, ScaledSamples: r.ss,
			BaselineOccurrences: bo, ScaledOccurrences: so,
		}
	}
	return out, nil
}

func round6x(x float64) float64 { return math.Round(x*1e6) / 1e6 }
