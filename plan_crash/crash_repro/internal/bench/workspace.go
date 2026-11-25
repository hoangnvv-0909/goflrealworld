package bench

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// AgentWorkspace copies the project at src into dst for a repair Agent, minus
// everything that would give the answer away. The Agent reads the whole tree,
// so this matters even more than for the LLM prompt.
//
//   - bugs.json is not copied;
//   - "// BUG:" tags are removed from .go files (lines are kept, so line
//     numbers still match the stacks);
//   - any other file that still mentions "BUG:" (README, NOTICE) is not copied;
//   - .git, results and stacks directories are not copied.
//
// dst must not exist or must be empty.
func AgentWorkspace(src, dst string) error {
	if ents, err := os.ReadDir(dst); err == nil && len(ents) > 0 {
		return &fs.PathError{Op: "agent workspace", Path: dst, Err: fs.ErrExist}
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "results", "stacks":
				if rel != "." {
					return filepath.SkipDir
				}
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if d.Name() == "bugs.json" || d.Name() == "gofl-localize.json" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) < 0 { // text
			if strings.HasSuffix(d.Name(), ".go") {
				data = []byte(StripAnswerTags(string(data)))
			} else if bytes.Contains(data, []byte("BUG:")) {
				return nil
			}
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
}
