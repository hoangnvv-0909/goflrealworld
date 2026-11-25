package crashsig

// Golden files for plan step 2: the normalised signature and Hash of every
// real stack in testdata/. Regenerate with: go test ./internal/crashsig -update

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*.json")

func TestGolden(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.txt")
	if len(files) == 0 {
		t.Fatal("no stacks in testdata")
	}
	for _, p := range files {
		name := strings.TrimSuffix(filepath.Base(p), ".txt")
		t.Run(name, func(t *testing.T) {
			s := Normalize(load(t, p))
			out := struct {
				Hash         string
				Exception    string
				NoCrashPoint bool
				Frames       []Frame
			}{Hash(s), s.Exception, s.NoCrashPoint, s.Frames}
			got, _ := json.MarshalIndent(out, "", "  ")
			got = append(got, '\n')
			g := filepath.Join("testdata", "golden", name+".json")
			if *update {
				_ = os.MkdirAll(filepath.Dir(g), 0o755)
				if err := os.WriteFile(g, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(g)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if strings.ReplaceAll(string(want), "\r\n", "\n") != string(got) {
				t.Errorf("signature changed for %s:\n%s\nwant:\n%s", name, got, want)
			}
		})
	}
}
