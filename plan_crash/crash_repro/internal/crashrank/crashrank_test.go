package crashrank

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gofl/internal/crashsig"
)

const (
	stackDir = "../llm/testdata/stacks"
	sampleFS = "D:/NL&TKTT/gofl/testdata/goflsample"
)

func event(t *testing.T, id string) Event {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(stackDir, id+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := crashsig.Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return Event{Signature: crashsig.Relativize(s, sampleFS, "goflsample"), Source: id}
}

func sig(exc string, frames ...string) crashsig.Signature {
	s := crashsig.Signature{Exception: exc}
	for _, f := range frames {
		s.Frames = append(s.Frames, crashsig.Frame{Func: f, File: "pkg/" + f + ".go", Line: 1})
	}
	return s
}

func TestGroupIdenticalAndEquivalent(t *testing.T) {
	a := event(t, "C2")
	groups, err := GroupEvents([]Event{a, a, a})
	if err != nil || len(groups) != 1 || groups[0].Level != 1 || len(groups[0].Events) != 3 {
		t.Fatalf("identical: %+v %v", groups, err)
	}
	// Same fault, different numbers in the message and a generic suffix: level 2.
	x := Event{Signature: sig("index out of range [3] with length 3", "p.f[...]", "p.g")}
	y := Event{Signature: sig("index out of range [9] with length 9", "p.f[...]", "p.g")}
	y.Signature.Frames[0].Func = "p.f[int]"
	groups, _ = GroupEvents([]Event{x, y})
	if len(groups) != 1 || groups[0].Level != 2 {
		t.Fatalf("equivalent: %+v", groups)
	}
}

func TestGroupContainmentLevel3(t *testing.T) {
	x := Event{Signature: sig("boom", "a", "b")}
	y := Event{Signature: sig("boom", "z", "a", "b", "c")}
	// different files at the crash point so level 4 cannot be the reason
	x.Signature.Frames[0].File = "one/a.go"
	y.Signature.Frames[0].File = "two/z.go"
	groups, _ := GroupEvents([]Event{x, y})
	if len(groups) != 1 || groups[0].Level != 3 {
		t.Fatalf("containment: %+v", groups)
	}
	// a gap breaks level 3
	g := Event{Signature: sig("boom", "a", "q", "b")}
	g.Signature.Frames[0].File = "three/a.go"
	groups, _ = GroupEvents([]Event{x, g})
	if len(groups) != 2 {
		t.Fatalf("gap should not merge: %+v", groups)
	}
}

func TestGroupSameFileLevel4(t *testing.T) {
	// C9: three entry points, same fault; they merge only at level 4.
	// The sample stacks hold one entry point, so build the other two by
	// changing the second frame.
	a := event(t, "C9")
	b, c := a, a
	b.Signature = cloneWithSecond(a.Signature, "goflsample/crash.(*Session).Logout")
	c.Signature = cloneWithSecond(a.Signature, "goflsample/crash.(*Session).Refresh")
	groups, _ := GroupEvents([]Event{a, b, c})
	if len(groups) != 1 || groups[0].Level != 4 || len(groups[0].Events) != 3 {
		t.Fatalf("C9 variants: %d groups, level %d", len(groups), groups[0].Level)
	}
}

func cloneWithSecond(s crashsig.Signature, fn string) crashsig.Signature {
	out := s
	out.Frames = append([]crashsig.Frame(nil), s.Frames...)
	out.Frames[1].Func = fn
	out.Frames[1].Line += 7
	return out
}

func TestDifferentFaultsStaySeparate(t *testing.T) {
	groups, _ := GroupEvents([]Event{event(t, "C2"), event(t, "C3"), event(t, "C9"), event(t, "C13")})
	// C2 and C3 share crash/crash.go (level 4); C9 and C13 are in hard.go (level 4).
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2 (one per crash-point file)", len(groups))
	}
	groups, _ = GroupEvents([]Event{event(t, "C2"), event(t, "C9")})
	if len(groups) != 2 {
		t.Fatalf("faults in different files merged: %d groups", len(groups))
	}
}

func TestGroupEmptyAndDeterministic(t *testing.T) {
	if g, err := GroupEvents(nil); g != nil || err != nil {
		t.Errorf("empty: %v %v", g, err)
	}
	ev := []Event{event(t, "C2"), event(t, "C9"), event(t, "C2")}
	a, _ := GroupEvents(ev)
	b, _ := GroupEvents(ev)
	if fmt.Sprint(ids(a)) != fmt.Sprint(ids(b)) {
		t.Error("order not deterministic")
	}
	if len(a[0].Events) != 2 {
		t.Error("largest group first")
	}
}

func ids(gs []Group) []string {
	var out []string
	for _, g := range gs {
		out = append(out, g.ID)
	}
	return out
}

func TestRankSingleCrashFollowsStack(t *testing.T) {
	groups, _ := GroupEvents([]Event{event(t, "C9")})
	fs, err := Rank(groups[0], groups)
	if err != nil || len(fs) == 0 {
		t.Fatalf("%v %v", fs, err)
	}
	if fs[0].Location.File != "crash/hard.go" || fs[0].Location.Func != "goflsample/crash.(*Session).Token" {
		t.Errorf("top = %+v", fs[0].Location)
	}
	for i, f := range fs {
		if f.Rank != i+1 {
			t.Errorf("rank %d at index %d", f.Rank, i)
		}
		if i > 0 && f.Score > fs[i-1].Score+1e-12 {
			t.Errorf("scores not descending at %d", i)
		}
	}
	// test files are not ranked when code under test is on the stack
	for _, f := range fs {
		if f.Location.File == "crash/hard_test.go" {
			t.Errorf("test file ranked: %+v", f.Location)
		}
	}
}

func TestRankC9FunctionScoresAndTies(t *testing.T) {
	// C9 with its three entry points: Token is in every trace at the crash
	// point; Login, Logout and Refresh are tied below it, and none of the
	// three may be cut by the per-file limit.
	a := event(t, "C9")
	b, c := a, a
	b.Signature = cloneWithSecond(a.Signature, "goflsample/crash.(*Session).Logout")
	c.Signature = cloneWithSecond(a.Signature, "goflsample/crash.(*Session).Refresh")
	groups, _ := GroupEvents([]Event{a, b, c})
	fs, err := Rank(groups[0], groups)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 4 {
		t.Fatalf("want Token + 3 callers, got %d: %+v", len(fs), fs)
	}
	if fs[0].Location.Func != "goflsample/crash.(*Session).Token" {
		t.Errorf("top = %s", fs[0].Location.Func)
	}
	if fs[1].Score >= fs[0].Score {
		t.Errorf("callers (%.3f) should score below Token (%.3f)", fs[1].Score, fs[0].Score)
	}
	seen := map[string]bool{}
	for _, f := range fs[1:] {
		if f.Score != fs[1].Score {
			t.Errorf("callers not tied: %s %.3f vs %.3f", f.Location.Func, f.Score, fs[1].Score)
		}
		seen[f.Location.Func] = true
	}
	for _, m := range []string{"Login", "Logout", "Refresh"} {
		if !seen["goflsample/crash.(*Session)."+m] {
			t.Errorf("%s missing", m)
		}
	}
}

func TestRankOnlyTestFiles(t *testing.T) {
	// A panic inside a test helper: the test file is all there is.
	s := crashsig.Signature{Exception: "e", Frames: []crashsig.Frame{
		{Func: "p.helper", File: "p/a_test.go", Line: 3},
		{Func: "p.TestA", File: "p/a_test.go", Line: 7},
	}}
	g := Group{ID: "g", Events: []Event{{Signature: s}}}
	fs, _ := Rank(g, []Group{g})
	if len(fs) != 2 || fs[0].Location.Func != "p.helper" || fs[0].Score <= fs[1].Score {
		t.Errorf("fs = %+v", fs)
	}
}

func TestRankIBFLowersSharedFiles(t *testing.T) {
	// Four groups all pass through lib/common.go; only group A also goes
	// through app/a.go. For A, the unique file must beat the shared one even
	// though common.go is nearer to the crash point.
	mk := func(extra string) Event {
		s := crashsig.Signature{Exception: "boom"}
		s.Frames = []crashsig.Frame{{Func: "lib.Common", File: "lib/common.go", Line: 1}}
		if extra != "" {
			s.Frames = append(s.Frames, crashsig.Frame{Func: "app." + extra, File: "app/" + extra + ".go", Line: 2})
		}
		s.Frames = append(s.Frames, crashsig.Frame{Func: "main.main", File: "main.go", Line: 3})
		return Event{Signature: s}
	}
	a := Group{ID: "A", Events: []Event{mk("a")}}
	b := Group{ID: "B", Events: []Event{mk("b")}}
	c := Group{ID: "C", Events: []Event{mk("c")}}
	d := Group{ID: "D", Events: []Event{mk("d")}}
	all := []Group{a, b, c, d}
	fs, _ := Rank(a, all)
	pos := map[string]int{}
	for _, f := range fs {
		if _, ok := pos[f.Location.File]; !ok {
			pos[f.Location.File] = f.Rank
		}
	}
	if pos["app/a.go"] >= pos["lib/common.go"] {
		t.Errorf("IBF should favour app/a.go: %v", pos)
	}
}

func TestRankFrequencyAcrossTraces(t *testing.T) {
	// File x.go appears in all 3 traces, y.go in only 1, at equal distance.
	mk := func(withY bool) Event {
		s := crashsig.Signature{Exception: "e"}
		s.Frames = []crashsig.Frame{{Func: "p.top", File: "top.go", Line: 1}}
		if withY {
			s.Frames = append(s.Frames, crashsig.Frame{Func: "p.y", File: "y.go", Line: 1})
		} else {
			s.Frames = append(s.Frames, crashsig.Frame{Func: "p.other", File: "o.go", Line: 1})
		}
		s.Frames = append(s.Frames, crashsig.Frame{Func: "p.x", File: "x.go", Line: 1})
		return Event{Signature: s}
	}
	g := Group{ID: "G", Events: []Event{mk(true), mk(false), mk(false)}}
	fs, _ := Rank(g, []Group{g})
	order := []string{}
	for _, f := range fs {
		order = append(order, f.Location.File)
	}
	// x.go: FF 1, mean distance 2 ; o.go: FF 2/3, distance 1 ; y.go: FF 1/3, distance 1
	if len(order) < 4 || order[0] != "top.go" {
		t.Fatalf("order = %v", order)
	}
	idx := func(f string) int {
		for i, o := range order {
			if o == f {
				return i
			}
		}
		return -1
	}
	if idx("y.go") < idx("o.go") {
		t.Errorf("rarer file ranked above commoner at equal distance: %v", order)
	}
}

func TestRankSkipsStdlibAndEmpty(t *testing.T) {
	s := crashsig.Signature{Exception: "e", Frames: []crashsig.Frame{
		{Func: "strings.Repeat", File: "/usr/local/go/src/strings/strings.go", Line: 1},
		{Func: "app.F", File: "app/f.go", Line: 4},
	}}
	g := Group{ID: "g", Events: []Event{{Signature: s}}}
	fs, _ := Rank(g, []Group{g})
	if len(fs) != 1 || fs[0].Location.File != "app/f.go" {
		t.Errorf("fs = %+v", fs)
	}
	if _, err := Rank(Group{}, nil); err == nil {
		t.Error("empty group should be an error")
	}
	// no usable frame: empty ranking, no error
	g2 := Group{ID: "h", Events: []Event{{Signature: crashsig.Signature{Exception: "x", NoCrashPoint: true}}}}
	if fs, err := Rank(g2, []Group{g2}); err != nil || len(fs) != 0 {
		t.Errorf("no frames: %v %v", fs, err)
	}
}
