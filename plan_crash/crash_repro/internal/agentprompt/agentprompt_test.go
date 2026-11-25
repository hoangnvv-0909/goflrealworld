package agentprompt

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gofl/internal/crashrank"
	"gofl/internal/crashsig"
	"gofl/internal/finding"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

// c9 is the hard case of the sample: three entry points, one root cause
// (NewSession) that is not on any stack.
func c9(t *testing.T) Input {
	t.Helper()
	var events []crashrank.Event
	for _, n := range []string{"C9_0", "C9_1", "C9_2"} {
		b, err := os.ReadFile(filepath.Join("..", "crashsig", "testdata", n+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		s, err := crashsig.Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, crashrank.Event{Signature: crashsig.Relativize(s, "", "goflsample"), Source: "test"})
	}
	groups, err := crashrank.GroupEvents(events)
	if err != nil || len(groups) != 1 {
		t.Fatalf("want one group, got %d (%v)", len(groups), err)
	}
	ranked, err := crashrank.Rank(groups[0], groups)
	if err != nil {
		t.Fatal(err)
	}
	return Input{
		Group: groups[0], Ranked: ranked,
		Repro: []string{
			"go test ./crash -run TestLoginAnon",
			"go test ./crash -run TestLogoutAnon",
			"go test ./crash -run TestRefreshAnon",
		},
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (create it with: go test ./internal/agentprompt -update)", err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("%s differs from the golden file; if intended: go test ./internal/agentprompt -update\n----\n%s", name, got)
	}
}

func TestGoldenC9(t *testing.T) {
	in := c9(t)
	for _, m := range []Mode{E0, E1} {
		t.Run(m.String(), func(t *testing.T) {
			got, err := Build(in, m)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "C9_"+m.String(), got)
		})
	}
}

// E0 must carry no trace of the ranking, or E1 - E0 would not measure it.
func TestE0HasNoRanking(t *testing.T) {
	in := c9(t)
	e0, _ := Build(in, E0)
	e1, _ := Build(in, E1)
	for _, bad := range []string{"ranks the suspicion", "IAD", "score ", "hint"} {
		if strings.Contains(e0, bad) {
			t.Errorf("E0 mentions %q", bad)
		}
	}
	if !strings.Contains(e1, "crash/hard.go") || !strings.Contains(e1, "(*Session).Token") {
		t.Errorf("E1 lacks the top file or function:\n%s", e1)
	}
	// Apart from the ranking section and the step number, E0 is E1.
	for _, must := range []string{"TestLoginAnon", "TestLogoutAnon", "TestRefreshAnon", LocalizeFile, "invalid memory address"} {
		if !strings.Contains(e0, must) || !strings.Contains(e1, must) {
			t.Errorf("both modes must contain %q", must)
		}
	}
}

func f(file, fn string, score float64) finding.Finding {
	return finding.Finding{Location: finding.Location{File: file, Func: fn, Line: 1}, Score: score}
}

func TestHints(t *testing.T) {
	ranked := []finding.Finding{
		f("a.go", "A1", 9), f("a.go", "A2", 8), f("b.go", "B1", 7), f("a.go", "A3", 6),
		f("a.go", "A4", 5), f("c.go", "C1", 4), f("d.go", "D1", 3),
	}
	got := Hints(ranked, 3, 3)
	if len(got) != 3 || got[0].File != "a.go" || got[1].File != "b.go" || got[2].File != "c.go" {
		t.Fatalf("files = %+v", got)
	}
	if n := len(got[0].Funcs); n != 3 || got[0].Funcs[2].Func != "A3" {
		t.Errorf("a.go keeps %d funcs, want A1 A2 A3: %+v", n, got[0].Funcs)
	}
	if got[0].Score != 9 || got[1].Score != 7 {
		t.Errorf("file score must be that of its best function: %+v", got)
	}
}

func TestHintsFewerThanThree(t *testing.T) {
	got := Hints([]finding.Finding{f("only.go", "F", 1)}, 3, 3)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if h := Hints(nil, 3, 3); len(h) != 0 {
		t.Fatalf("nil ranking gave %+v", h)
	}
}

func TestBuildEmptyGroup(t *testing.T) {
	if _, err := Build(Input{}, E1); err == nil {
		t.Fatal("want an error for a group without events")
	}
}

func TestBuildNoRepro(t *testing.T) {
	in := c9(t)
	in.Repro = nil
	got, _ := Build(in, E0)
	if !strings.Contains(got, "no command is known") {
		t.Errorf("missing fallback text:\n%s", got)
	}
}
