// Package benchcheck keeps the ground truth of the sample project honest:
// bugs.json must agree with the "BUG:<id>" tags in the source files.
// Owner: TV1. Without this, benchmark numbers (Acc@k) would silently measure
// against wrong answers.
package benchcheck

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

const sampleDir = "../../testdata/goflsample"

// realworldDir is the forked RealWorld backend with seeded bugs (benchmark layer 2).
const realworldDir = "../../testdata/goflrealworld"

type loc struct {
	File  string `json:"file"`
	Func  string `json:"func"`
	Lines []int  `json:"lines"`
}

type bug struct {
	ID         string `json:"id"`
	Cmd        string `json:"cmd"`
	Kind       string `json:"kind"`
	loc               // fix location
	Trigger    string `json:"trigger"`
	CrashPoint *loc   `json:"crash_point"`
}

// UnmarshalJSON is needed because loc is embedded but unexported.
func (b *bug) UnmarshalJSON(data []byte) error {
	type plain struct {
		ID         string `json:"id"`
		Cmd        string `json:"cmd"`
		Kind       string `json:"kind"`
		File       string `json:"file"`
		Func       string `json:"func"`
		Lines      []int  `json:"lines"`
		Trigger    string `json:"trigger"`
		CrashPoint *loc   `json:"crash_point"`
	}
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*b = bug{ID: p.ID, Cmd: p.Cmd, Kind: p.Kind, Trigger: p.Trigger, CrashPoint: p.CrashPoint,
		loc: loc{File: p.File, Func: p.Func, Lines: p.Lines}}
	return nil
}

func readBugs(t *testing.T, dir string) []bug {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "bugs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var bugs []bug
	if err := json.Unmarshal(data, &bugs); err != nil {
		t.Fatalf("bugs.json: %v", err)
	}
	return bugs
}

// taggedLines returns the line numbers of file that carry "BUG:<id>".
func taggedLines(t *testing.T, dir, file, id string) []int {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(file)))
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	defer f.Close()
	re := regexp.MustCompile(`BUG:` + regexp.QuoteMeta(id) + `\b`)
	var lines []int
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		if re.MatchString(sc.Text()) {
			lines = append(lines, n)
		}
	}
	return lines
}

func lineCount(t *testing.T, dir, file string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n
}

func TestBugsJSONMatchesSource(t *testing.T) {
	for _, dir := range []string{sampleDir, realworldDir} {
		t.Run(filepath.Base(dir), func(t *testing.T) { checkBugsJSON(t, dir) })
	}
}

func checkBugsJSON(t *testing.T, dir string) {
	seen := map[string]bool{}
	for _, b := range readBugs(t, dir) {
		if b.ID == "" || b.File == "" || b.Func == "" || len(b.Lines) == 0 {
			t.Errorf("%s: id, file, func and lines are required", b.ID)
			continue
		}
		if seen[b.ID] {
			t.Errorf("%s: duplicate id", b.ID)
		}
		seen[b.ID] = true
		if b.Cmd != "crash" && b.Cmd != "perf" && b.Cmd != "sem" {
			t.Errorf("%s: unknown cmd %q", b.ID, b.Cmd)
		}

		tagged := taggedLines(t, dir, b.File, b.ID)
		if len(tagged) == 0 {
			t.Errorf("%s: no BUG:%s tag in %s", b.ID, b.ID, b.File)
		}
		for _, n := range tagged {
			if !slices.Contains(b.Lines, n) {
				t.Errorf("%s: tag at %s:%d is not listed in bugs.json lines %v", b.ID, b.File, n, b.Lines)
			}
		}
		for _, n := range b.Lines {
			if !slices.Contains(tagged, n) {
				t.Errorf("%s: bugs.json says line %d but %s has no BUG:%s tag there (tags: %v)",
					b.ID, n, b.File, b.ID, tagged)
			}
		}
		if cp := b.CrashPoint; cp != nil {
			if cp.File == "" || cp.Func == "" || len(cp.Lines) == 0 {
				t.Errorf("%s: crash_point needs file, func and lines", b.ID)
				continue
			}
			max := lineCount(t, dir, cp.File)
			for _, n := range cp.Lines {
				if n < 1 || n > max {
					t.Errorf("%s: crash_point line %d outside %s (1..%d)", b.ID, n, cp.File, max)
				}
			}
		}
	}
	// Every tag in the sources must belong to a known bug.
	re := regexp.MustCompile(`BUG:([A-Z]+\d+)\b`)
	err := filepath.WalkDir(dir, func(f string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(f) != ".go" {
			return err
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			if !seen[m[1]] {
				t.Errorf("%s: tag BUG:%s has no entry in bugs.json", f, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Group C needs 7 original + at least 5 hard crash cases (plan, week 1 day 4-5).
func TestCrashBugCount(t *testing.T) {
	n := 0
	for _, b := range readBugs(t, sampleDir) {
		if b.Cmd == "crash" {
			n++
		}
	}
	if n < 12 {
		t.Errorf("crash bugs in sample: %d, want at least 12 (C1-C7 plus 5 hard)", n)
	}
}
