package bench

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentWorkspaceHidesAnswers(t *testing.T) {
	for _, proj := range []string{"goflsample", "goflrealworld"} {
		t.Run(proj, func(t *testing.T) {
			src := filepath.Join("..", "..", "testdata", proj)
			dst := filepath.Join(t.TempDir(), "ws")
			if err := AgentWorkspace(src, dst); err != nil {
				t.Fatal(err)
			}
			files := 0
			filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				files++
				if d.Name() == "bugs.json" {
					t.Errorf("bugs.json was copied: %s", p)
				}
				if data, _ := os.ReadFile(p); strings.Contains(string(data), "BUG:") {
					t.Errorf("%s still mentions BUG:", p)
				}
				return nil
			})
			if files == 0 {
				t.Fatal("nothing was copied")
			}
		})
	}
}

// Stripping tags must not move lines, or the stacks stop matching the code.
func TestAgentWorkspaceKeepsLineNumbers(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "goflsample")
	dst := filepath.Join(t.TempDir(), "ws")
	if err := AgentWorkspace(src, dst); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(src, "crash", "hard.go"))
	b, _ := os.ReadFile(filepath.Join(dst, "crash", "hard.go"))
	if strings.Count(string(a), "\n") != strings.Count(string(b), "\n") {
		t.Errorf("line count changed: %d -> %d", strings.Count(string(a), "\n"), strings.Count(string(b), "\n"))
	}
}

func TestAgentWorkspaceRefusesNonEmptyDst(t *testing.T) {
	dst := t.TempDir()
	os.WriteFile(filepath.Join(dst, "x"), []byte("x"), 0o644)
	if err := AgentWorkspace(filepath.Join("..", "..", "testdata", "goflsample"), dst); err == nil {
		t.Fatal("want an error for a non-empty destination")
	}
}
