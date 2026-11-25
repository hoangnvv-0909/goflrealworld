// Package pproftest dựng file pprof viết tay cho kiểm thử.
package pproftest

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/pprof/profile"
)

// Sample: stack viết dạng "hàm@tệp:dòng" theo thứ tự gốc → lá. Một phần tử
// có thể chứa nhiều khung inline nối bằng "|" (hàm ngoài trước, hàm inline sau).
type Sample struct {
	Stack []string
	Count int64
}

// PeriodNS: chu kỳ lấy mẫu 10ms như Go CPU profile mặc định.
const PeriodNS = 10_000_000

// Build tạo *profile.Profile giống CPU profile của Go.
func Build(samples []Sample) *profile.Profile {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
		PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     PeriodNS,
	}
	funcs := map[string]*profile.Function{}
	locs := map[string]*profile.Location{}
	fn := func(name, file string) *profile.Function {
		k := name + "@" + file
		if f, ok := funcs[k]; ok {
			return f
		}
		f := &profile.Function{ID: uint64(len(funcs) + 1), Name: name, SystemName: name, Filename: file}
		funcs[k] = f
		p.Function = append(p.Function, f)
		return f
	}
	parse := func(s string) (string, string, int64) {
		at := strings.LastIndexByte(s, '@')
		name, rest := s[:at], s[at+1:]
		colon := strings.LastIndexByte(rest, ':')
		line, _ := strconv.ParseInt(rest[colon+1:], 10, 64)
		return name, rest[:colon], line
	}
	loc := func(spec string) *profile.Location {
		if l, ok := locs[spec]; ok {
			return l
		}
		parts := strings.Split(spec, "|")
		l := &profile.Location{ID: uint64(len(locs) + 1), Address: uint64(0x1000 + len(locs))}
		// Line[0] là hàm inline trong cùng → đảo thứ tự.
		for i := len(parts) - 1; i >= 0; i-- {
			n, f, ln := parse(parts[i])
			l.Line = append(l.Line, profile.Line{Function: fn(n, f), Line: ln})
		}
		locs[spec] = l
		p.Location = append(p.Location, l)
		return l
	}
	for _, s := range samples {
		var ls []*profile.Location
		for i := len(s.Stack) - 1; i >= 0; i-- { // pprof: lá trước
			ls = append(ls, loc(s.Stack[i]))
		}
		p.Sample = append(p.Sample, &profile.Sample{Location: ls, Value: []int64{s.Count, s.Count * PeriodNS}})
	}
	return p
}

// Write ghi profile ra file tạm (gzip, đúng định dạng go tool pprof đọc được).
func Write(t testing.TB, samples []Sample) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.pprof")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := Build(samples).Write(f); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}
