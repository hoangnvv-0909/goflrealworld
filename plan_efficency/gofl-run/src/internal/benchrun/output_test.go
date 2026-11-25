package benchrun

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gofl/internal/contract"
)

func scan(text string) *scanner {
	var s scanner
	// Feed it in odd chunks: lines arrive split across writes.
	for len(text) > 0 {
		n := 7
		if n > len(text) {
			n = len(text)
		}
		s.Write([]byte(text[:n]))
		text = text[n:]
	}
	s.flush()
	return &s
}

func TestScannerVerbose(t *testing.T) {
	s := scan("goos: windows\r\ngoarch: amd64\r\npkg: sample/slow\r\nBenchmarkDedup\r\nBenchmarkDedup/n=100\r\nBenchmarkDedup/n=100-8         \t     100\t      1861 ns/op\r\nPASS\r\n")
	want := []result{{"BenchmarkDedup/n=100", 100, 1861}}
	if !reflect.DeepEqual(s.results, want) {
		t.Errorf("got %+v", s.results)
	}
}

func TestScannerLogsBetweenNameAndResult(t *testing.T) {
	s := scan("starting 100\nBenchmarkLogs\nBenchmarkLogs/n=100\nBenchmarkLogs/n=100-8         \tstarting 100\nstarting 100\n     100\t      2336 ns/op\t  16 B/op\nPASS\n")
	want := []result{{"BenchmarkLogs/n=100", 100, 2336}}
	if !reflect.DeepEqual(s.results, want) {
		t.Errorf("got %+v", s.results)
	}
}

func TestScannerPlainAndCPU1(t *testing.T) {
	// Without -test.v, and with GOMAXPROCS=1 (no -N suffix).
	s := scan("BenchmarkA-16   \t 2000\t 512.5 ns/op\nBenchmarkB/size_100 \t 30\t 1.5e+06 ns/op\nBenchmarkC/n=5-2   \t 7\t 9 ns/op\n")
	want := []result{{"BenchmarkA", 2000, 512.5}, {"BenchmarkB/size_100", 30, 1.5e6}, {"BenchmarkC/n=5", 7, 9}}
	if !reflect.DeepEqual(s.results, want) {
		t.Errorf("got %+v", s.results)
	}
}

func TestScannerSkipAndFail(t *testing.T) {
	s := scan("BenchmarkSkips\n    slow_test.go:62: SAMPLE_DB is not set\n--- SKIP: BenchmarkSkips\nPASS\n")
	if !s.skipped || len(s.results) != 0 {
		t.Errorf("skip: %+v", s)
	}
	s = scan("--- FAIL: BenchmarkNeedsEnv\n    slow_test.go:50: SAMPLE_DB is not set\nFAIL\n")
	if len(s.failed) != 1 || s.failed[0] != "--- FAIL: BenchmarkNeedsEnv | slow_test.go:50: SAMPLE_DB is not set" {
		t.Errorf("fail: %+v", s.failed)
	}
	// -test.v prints the message first.
	s = scan("BenchmarkNeedsEnv\n    slow_test.go:50: SAMPLE_DB is not set\n--- FAIL: BenchmarkNeedsEnv\nFAIL\n")
	if len(s.failed) != 1 || s.failed[0] != "--- FAIL: BenchmarkNeedsEnv | slow_test.go:50: SAMPLE_DB is not set" {
		t.Errorf("fail -v: %+v", s.failed)
	}
	s = scan("panic: runtime error: index out of range\n\ngoroutine 1 [running]:\n")
	if len(s.failed) != 1 {
		t.Errorf("panic: %+v", s.failed)
	}
}

func TestScannerTailIsBounded(t *testing.T) {
	s := scan(strings.Repeat("line\n", 500) + "last")
	if len(s.tail) != tailLines || s.tail[len(s.tail)-1] != "last" {
		t.Errorf("tail has %d lines, last %q", len(s.tail), s.tail[len(s.tail)-1])
	}
}

// matchSub applies the second element of a -test.bench pattern, the way the
// testing package matches the name of a sub-benchmark.
func matchSub(t *testing.T, pattern, name string) bool {
	t.Helper()
	parts := strings.SplitN(pattern, "/", 2)
	return regexp.MustCompile(parts[1]).MatchString(name)
}

func TestSizePattern(t *testing.T) {
	p := sizePattern("BenchmarkX", "100")
	if !strings.HasPrefix(p, "^BenchmarkX$/") {
		t.Fatalf("pattern %q", p)
	}
	for _, name := range []string{"100", "n=100", "size_100", "100rows", "rows=100,k=3", "n-100"} {
		if !matchSub(t, p, name) {
			t.Errorf("%q should match size 100", name)
		}
	}
	for _, name := range []string{"1000", "n=1000", "2100", "n=10", "x"} {
		if matchSub(t, p, name) {
			t.Errorf("%q should not match size 100", name)
		}
	}
	p = sizePattern("BenchmarkX", "small")
	for name, want := range map[string]bool{"small": true, "size=small": true, "smaller": false, "nosmall": false, "small_2": true} {
		if got := matchSub(t, p, name); got != want {
			t.Errorf("small vs %q: got %v", name, got)
		}
	}
	p = sizePattern("BenchmarkX", "1.5")
	if matchSub(t, p, "n=105") || !matchSub(t, p, "f=1.5") {
		t.Error("the size must be matched literally")
	}
}

func TestClassifyBuild(t *testing.T) {
	cases := map[string]contract.Code{
		"go: go.mod file not found in current directory or any parent directory":                    contract.NoModule,
		"main_test.go:5:2: missing go.sum entry for module providing package github.com/x/y":        contract.NoModule,
		"no required module provides package github.com/x/y; to add it:":                            contract.NoModule,
		"dial tcp: lookup proxy.golang.org: no such host":                                           contract.NoModule,
		"go: go.mod requires go >= 1.30 (running go 1.27.4; GOTOOLCHAIN=local)":                     contract.NoGo,
		"# sample/broken\n./broken_test.go:6:14: cannot use \"x\" (untyped string constant) as int": contract.BuildFail,
		"": contract.BuildFail,
	}
	for in, want := range cases {
		if got := classifyBuild(in); got != want {
			t.Errorf("%q: got %s, want %s", in, got, want)
		}
	}
}

func TestFirstError(t *testing.T) {
	got := firstError("go: downloading x v1\n# sample/broken\n\n./broken_test.go:6:14: cannot use\n")
	if got != "./broken_test.go:6:14: cannot use" {
		t.Errorf("got %q", got)
	}
}

func TestSizesAndOptions(t *testing.T) {
	sz, err := ParseSizes([]string{"100, 10000"})
	if err != nil || sz != [2]string{"100", "10000"} {
		t.Errorf("ParseSizes: %v %v", sz, err)
	}
	if sz, err := ParseSizes([]string{"small", "large"}); err != nil || sz != [2]string{"small", "large"} {
		t.Errorf("ParseSizes list: %v %v", sz, err)
	}
	for _, bad := range [][]string{{"100"}, {"1,2,3"}, {""}} {
		if _, err := ParseSizes(bad); err == nil {
			t.Errorf("ParseSizes(%q) passed", bad)
		}
	}
	bad := []Options{
		{Bench: "", Sizes: [2]string{"1", "2"}},
		{Bench: "X", Sizes: [2]string{"2", "1"}},
		{Bench: "X", Sizes: [2]string{"1", "1"}},
		{Bench: "X", Sizes: [2]string{"1", ""}},
		{Bench: "X", Sizes: [2]string{"a/b", "c"}},
		{Bench: "X/n=1", Sizes: [2]string{"1", "2"}},
		{Bench: "X", Sizes: [2]string{"1", "2"}, BenchTime: "forever"},
		{Bench: "X", Sizes: [2]string{"1", "2"}, CPU: "1,2"},
		{Bench: "X", Sizes: [2]string{"1", "2"}, PathsMethod: "fast"},
		{Bench: "X", Sizes: [2]string{"1", "2"}, Env: []string{"NOEQUALS"}},
		{Bench: "X", Sizes: [2]string{"1", "2"}, Dir: "no/such/dir"},
	}
	for _, o := range bad {
		if err := o.normalize(true); err == nil {
			t.Errorf("%+v passed", o)
		} else if _, ok := err.(*UsageError); !ok {
			t.Errorf("%+v: %T is not a UsageError", o, err)
		}
	}
	o := Options{Bench: "Dedup", Sizes: [2]string{"small", "large"}}
	if err := o.normalize(true); err != nil {
		t.Fatal(err)
	}
	if o.Bench != "BenchmarkDedup" || o.Timeout != DefaultTimeout || o.BenchTime != "1s" || o.Count != 1 || o.Out != "out" || o.PathsMethod != "paper" {
		t.Errorf("defaults: %+v", o)
	}
}
