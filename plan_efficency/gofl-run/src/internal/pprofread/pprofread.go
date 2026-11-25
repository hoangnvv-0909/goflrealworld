// Package pprofread đọc file pprof (CPU profile của Go) bằng
// github.com/google/pprof/profile và chuyển thành các cấu trúc mà
// diffprof cần: danh sách stack (gốc → lá), bảng self-time theo hàm,
// và cây ngữ cảnh gọi (calling-context tree).
//
// Quy ước (hợp đồng v0, mục 3):
//   - giây = số mẫu × chu kỳ lấy mẫu của pprof;
//   - tên hàm dạng đầy đủ của Go; đường dẫn tệp tương đối từ gốc dự án
//     (khi truyền Options.Root).
package pprofread

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/pprof/profile"
)

// ErrNoProfile được trả khi không đọc được file hoặc file không có mẫu.
// Tầng CLI ánh xạ lỗi này sang mã lỗi NO_PROFILE.
var ErrNoProfile = errors.New("NO_PROFILE")

// Frame là một khung trong stack.
type Frame struct {
	Name string `json:"name"` // tên đầy đủ, ví dụ example.com/app/internal/report.BuildRows
	File string `json:"file"` // tương đối từ gốc dự án nếu được
	Line int64  `json:"line"` // dòng đang chạy trong hàm (call site hoặc dòng nóng ở lá)
}

// Key định danh một hàm: (tên hàm, tệp). Đây là khoá dùng ở mọi nơi.
type Key struct {
	Name string
	File string
}

func (f Frame) Key() Key { return Key{f.Name, f.File} }

// Stack là một stack đã gộp: các khung theo thứ tự gốc → lá.
type Stack struct {
	Frames  []Frame
	Samples int64
	Seconds float64
}

// Leaf trả khung lá (hàm đang chạy khi lấy mẫu).
func (s Stack) Leaf() Frame { return s.Frames[len(s.Frames)-1] }

// FuncSelf là một dòng của bảng self-time.
type FuncSelf struct {
	Function string  `json:"function"`
	File     string  `json:"file"`
	Samples  int64   `json:"samples"`
	Seconds  float64 `json:"seconds"`
	Noise    bool    `json:"noise,omitempty"` // runtime.* hoặc testing.*
}

// Profile là kết quả đọc một file pprof.
type Profile struct {
	Path             string
	SecondsPerSample float64
	TotalSamples     int64
	Stacks           []Stack

	tree *Tree // bộ đệm của Tree()
}

// Options điều chỉnh cách đọc.
type Options struct {
	// Root: gốc dự án. Nếu khác rỗng, đường dẫn tệp nằm dưới Root được
	// đổi thành đường dẫn tương đối (dấu /).
	Root string
}

// Load đọc một file pprof (gzip hoặc không).
func Load(path string, opt Options) (*Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoProfile, err)
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrNoProfile, path, err)
	}
	out, err := FromProfile(p, opt)
	if err != nil {
		return nil, err
	}
	out.Path = path
	return out, nil
}

// FromProfile chuyển một *profile.Profile đã có sẵn (dùng cho kiểm thử).
func FromProfile(p *profile.Profile, opt Options) (*Profile, error) {
	countIdx, secPerSample, err := sampleLayout(p)
	if err != nil {
		return nil, err
	}
	root := ""
	if opt.Root != "" {
		if abs, err := filepath.Abs(opt.Root); err == nil {
			root = filepath.ToSlash(abs)
		} else {
			root = filepath.ToSlash(opt.Root)
		}
	}

	agg := map[string]*Stack{}
	var order []string
	var total int64
	for _, s := range p.Sample {
		var n int64
		if countIdx >= 0 {
			n = s.Value[countIdx]
		} else {
			// Không có kiểu "samples": đổi thời gian về số mẫu.
			n = int64(float64(s.Value[0])*unitScale(p.SampleType[0].Unit)/secPerSample + 0.5)
		}
		if n <= 0 {
			continue
		}
		frames := expand(s, root)
		if len(frames) == 0 {
			continue
		}
		k := stackKey(frames)
		st, ok := agg[k]
		if !ok {
			st = &Stack{Frames: frames}
			agg[k] = st
			order = append(order, k)
		}
		st.Samples += n
		total += n
	}
	// total == 0 vẫn hợp lệ: ở tải nhỏ, benchmark có thể chạy quá nhanh để
	// bắt được mẫu nào. Người gọi tự quyết (scaled rỗng → NO_POSITIVE_DELTA).
	out := &Profile{SecondsPerSample: secPerSample, TotalSamples: total}
	for _, k := range order {
		st := agg[k]
		st.Seconds = float64(st.Samples) * secPerSample
		out.Stacks = append(out.Stacks, *st)
	}
	return out, nil
}

// sampleLayout tìm cột "samples/count" và số giây mỗi mẫu.
func sampleLayout(p *profile.Profile) (int, float64, error) {
	if len(p.SampleType) == 0 {
		return -1, 0, fmt.Errorf("%w: thiếu sample_type", ErrNoProfile)
	}
	countIdx := -1
	for i, st := range p.SampleType {
		if st.Type == "samples" && st.Unit == "count" {
			countIdx = i
			break
		}
	}
	if p.Period <= 0 || p.PeriodType == nil {
		return -1, 0, fmt.Errorf("%w: thiếu period/period_type", ErrNoProfile)
	}
	scale := unitScale(p.PeriodType.Unit)
	if scale == 0 {
		return -1, 0, fmt.Errorf("%w: đơn vị chu kỳ %q không phải thời gian", ErrNoProfile, p.PeriodType.Unit)
	}
	if countIdx < 0 && unitScale(p.SampleType[0].Unit) == 0 {
		return -1, 0, fmt.Errorf("%w: không có cột samples/count hay cột thời gian", ErrNoProfile)
	}
	return countIdx, float64(p.Period) * scale, nil
}

func unitScale(u string) float64 {
	switch u {
	case "nanoseconds", "ns":
		return 1e-9
	case "microseconds", "us":
		return 1e-6
	case "milliseconds", "ms":
		return 1e-3
	case "seconds", "s":
		return 1
	}
	return 0
}

// expand đổi một sample thành danh sách khung gốc → lá. Trong pprof,
// Location[0] là lá; trong mỗi Location, Line[0] là hàm inline trong cùng,
// dòng cuối là hàm chứa nó. Ta giữ các hàm inline thành khung riêng.
func expand(s *profile.Sample, root string) []Frame {
	var leafFirst []Frame
	for _, loc := range s.Location {
		if len(loc.Line) == 0 {
			leafFirst = append(leafFirst, Frame{Name: fmt.Sprintf("0x%x", loc.Address)})
			continue
		}
		for _, ln := range loc.Line {
			name, file := "?", ""
			if ln.Function != nil {
				name = ln.Function.Name
				file = RelFile(ln.Function.Filename, root)
			}
			leafFirst = append(leafFirst, Frame{Name: name, File: file, Line: ln.Line})
		}
	}
	n := len(leafFirst)
	out := make([]Frame, n)
	for i, f := range leafFirst {
		out[n-1-i] = f
	}
	return out
}

// RelFile turns the file of a function into the form of hotspots.json: slash
// separators, relative to root when the file is inside it, absolute otherwise
// (GOROOT, module cache). It works on strings only, so Windows paths
// ("C:\proj\a.go") give the same result on every host.
func RelFile(file, root string) string {
	if file == "" {
		return ""
	}
	f := slashClean(file)
	if root == "" {
		return f
	}
	r := slashClean(root)
	if !isAbs(f) {
		return f
	}
	fc, rc := f, r
	if hasDrive(f) && hasDrive(r) { // Windows paths are not case sensitive
		fc, rc = strings.ToLower(f), strings.ToLower(r)
	}
	rc = strings.TrimSuffix(rc, "/")
	if rc != "" && strings.HasPrefix(fc, rc+"/") {
		return f[len(rc)+1:]
	}
	return f
}

func slashClean(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if hasDrive(p) {
		// path.Clean("C:/a/../b") keeps the drive as a first element.
		return p[:2] + path.Clean(p[2:])
	}
	return path.Clean(p)
}

func hasDrive(p string) bool {
	return len(p) >= 3 && p[1] == ':' && p[2] == '/' &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

func isAbs(p string) bool { return strings.HasPrefix(p, "/") || hasDrive(p) }

func stackKey(fr []Frame) string {
	var b strings.Builder
	for _, f := range fr {
		fmt.Fprintf(&b, "%s\x00%s\x00%d\x01", f.Name, f.File, f.Line)
	}
	return b.String()
}

// SelfTable trả bảng self-time: tổng mẫu của các stack mà hàm là khung lá.
// Có cả khung nhiễu (đánh dấu Noise); người gọi tự lọc. Sắp theo giây giảm
// dần, hoà thì theo tên rồi tệp.
func (p *Profile) SelfTable() []FuncSelf {
	m := map[Key]*FuncSelf{}
	for _, s := range p.Stacks {
		lf := s.Leaf()
		r, ok := m[lf.Key()]
		if !ok {
			r = &FuncSelf{Function: lf.Name, File: lf.File, Noise: IsNoise(lf.Name)}
			m[lf.Key()] = r
		}
		r.Samples += s.Samples
	}
	out := make([]FuncSelf, 0, len(m))
	for _, r := range m {
		r.Seconds = float64(r.Samples) * p.SecondsPerSample
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Samples != out[j].Samples {
			return out[i].Samples > out[j].Samples
		}
		if out[i].Function != out[j].Function {
			return out[i].Function < out[j].Function
		}
		return out[i].File < out[j].File
	})
	return out
}

// IsNoise: khung runtime.* hoặc testing.* (thay cho _is_trace_harness_key
// của paper, vốn lọc khung VizTracer và script chạy).
func IsNoise(name string) bool {
	return strings.HasPrefix(name, "runtime.") || strings.HasPrefix(name, "internal/runtime/") || strings.HasPrefix(name, "testing.")
}

// IsNoiseWith: như IsNoise, thêm các tiền tố bổ sung (đề xuất cho Go ≥ 1.21:
// "internal/runtime/" — gói nội bộ của runtime, chạy cả trong goroutine GC).
func IsNoiseWith(name string, extra []string) bool {
	if IsNoise(name) {
		return true
	}
	for _, p := range extra {
		if p != "" && strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ShortName bỏ đường dẫn gói, giữ "gói.Hàm":
// example.com/app/internal/report.(*T).M → report.(*T).M
// Không cắt bên trong tham số kiểu generic [...].
func ShortName(name string) string {
	head := name
	if i := strings.IndexByte(name, '['); i >= 0 {
		head = name[:i]
	}
	if i := strings.LastIndexByte(head, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// MatchEntry: tên hàm có khớp chuỗi điểm vào người dùng khai không.
// Khớp khi: trùng tên đầy đủ; trùng tên ngắn (gói.Hàm); trùng phần sau dấu
// chấm cuối của gói (ví dụ "BenchmarkX"); hoặc là closure của hàm đó
// ("BenchmarkX.func1", sinh ra bởi b.Run cho sub-benchmark).
func MatchEntry(name, entry string) bool {
	if entry == "" {
		return false
	}
	if name == entry {
		return true
	}
	short := ShortName(name)
	if short == entry || strings.HasPrefix(short, entry+".func") {
		return true
	}
	// bỏ tên gói: report.BenchmarkX → BenchmarkX
	if i := strings.IndexByte(short, '.'); i >= 0 {
		local := short[i+1:]
		if local == entry || strings.HasPrefix(local, entry+".func") {
			return true
		}
	}
	return strings.HasSuffix(name, "."+entry) || strings.HasSuffix(name, "/"+entry)
}
