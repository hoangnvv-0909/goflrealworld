package llm

import (
	"testing"

	"gofl/internal/finding"
)

func TestNormFunc(t *testing.T) {
	for in, want := range map[string]string{
		"EmailOf":                         "EmailOf",
		"crash.EmailOf":                   "EmailOf",
		"goflsample/crash.EmailOf":        "EmailOf",
		"crash.(*Registry).Inc":           "Registry.Inc",
		"goflsample/crash.(*Node).Total":  "Node.Total",
		"Registry.Inc":                    "Registry.Inc",
		"Fanout.func1":                    "Fanout",
		"goflsample/crash.Fanout.func1":   "Fanout",
		"goflsample/crash.Fanout.func1.2": "Fanout",
		"goflsample/crash.must[...]":      "must",
		"crash.ParsePort()":               "ParsePort",
		"(*Cache).evict":                  "Cache.evict",
		"":                                "",
	} {
		if got := NormFunc(in); got != want {
			t.Errorf("NormFunc(%q) = %q, want %q", in, got, want)
		}
	}
}

var truth = Truth{File: "crash/hard.go", Func: "parseKV", Lines: []int{42}}

func TestMatchLevels(t *testing.T) {
	good := finding.Location{File: "D:/x/goflsample/crash/hard.go", Func: "goflsample/crash.parseKV", Line: 42}
	for lvl, want := range map[Level]bool{LevelFile: true, LevelFunc: true, LevelLine: true} {
		if got := Match(good, truth, lvl, 0); got != want {
			t.Errorf("%v: %v", lvl, got)
		}
	}
	wrongLine := good
	wrongLine.Line = 44
	if Match(wrongLine, truth, LevelLine, 0) || !Match(wrongLine, truth, LevelLine, 2) || Match(wrongLine, truth, LevelLine, 1) {
		t.Error("line tolerance")
	}
	wrongFunc := good
	wrongFunc.Func = "crash.stageBuild"
	if Match(wrongFunc, truth, LevelFunc, 0) || !Match(wrongFunc, truth, LevelFile, 0) {
		t.Error("function level")
	}
	noLine := good
	noLine.Line = 0
	if Match(noLine, truth, LevelLine, 5) {
		t.Error("unknown line must not match")
	}
	otherFile := good
	otherFile.File = "crash/crash.go"
	if Match(otherFile, truth, LevelFile, 0) {
		t.Error("other file matched")
	}
}

func TestHitAtDistinct(t *testing.T) {
	locs := []finding.Location{
		{File: "a.go", Func: "A"},
		{File: "a.go", Func: "B"}, // same file again: not a new file
		{File: "crash/hard.go", Func: "crash.parseKV", Line: 42},
	}
	if HitAt(locs, truth, LevelFile, 1, 0) {
		t.Error("@1 file")
	}
	if !HitAt(locs, truth, LevelFile, 2, 0) {
		t.Error("a file named twice must count once, so hard.go is the 2nd file")
	}
	if HitAt(locs, truth, LevelFunc, 2, 0) || !HitAt(locs, truth, LevelFunc, 3, 0) {
		t.Error("function level counts distinct (file, func) pairs")
	}
	if HitAt(nil, truth, LevelFile, 5, 0) {
		t.Error("nothing found")
	}
}

func TestSolvedQuorum(t *testing.T) {
	hit := Result{Locations: []finding.Location{{File: "crash/hard.go", Func: "parseKV", Line: 42}}}
	miss := Result{Locations: []finding.Location{{File: "crash/crash.go", Func: "Nth", Line: 21}}}
	if !Solved([]Result{hit, hit, hit, miss, miss}, 5, truth, LevelFile, 1, 0) {
		t.Error("3 of 5 is solved")
	}
	if Solved([]Result{hit, hit, miss, miss, miss}, 5, truth, LevelFile, 1, 0) {
		t.Error("2 of 5 is not")
	}
	// Failed requests count as wrong: only 3 usable answers out of 5 runs.
	if Solved([]Result{hit, hit, miss}, 5, truth, LevelFile, 1, 0) {
		t.Error("2 hits out of 5 requests is not a majority")
	}
	if !Solved([]Result{hit, hit, hit}, 5, truth, LevelFile, 1, 0) {
		t.Error("3 hits out of 5 requests is")
	}
}

func TestMetric(t *testing.T) {
	var m Metric
	if m.Rate() != 0 {
		t.Error("empty metric")
	}
	m.Add(true)
	m.Add(false)
	m.Add(true)
	m.Add(true)
	if m.Hit != 3 || m.Total != 4 || m.Rate() != 75 {
		t.Errorf("%+v %v", m, m.Rate())
	}
}
