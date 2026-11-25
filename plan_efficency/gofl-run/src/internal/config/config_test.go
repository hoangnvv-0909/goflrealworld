package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const full = "\uFEFF" + `# gofl.yaml filled in by the team leader
dir: .
package: ./internal/report
bench: BenchmarkBuildRows   # the benchmark
sizes: [100, 10000]
timeout: 15m
benchtime: 2s
count: 3
cpu: "1"
setup:
  - docker compose up -d db
  - "echo 'ready # not a comment'"
env:
  DATABASE_URL: "postgres://u:p@localhost/db?sslmode=disable#frag"
  EMPTY:
  GOFLAGS: -mod=mod
out: out
paths_method: stack
go: /usr/local/go/bin/go
`

func TestParseFull(t *testing.T) {
	c, err := Parse(strings.NewReader(full))
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		Dir:       ".",
		Package:   "./internal/report",
		Bench:     "BenchmarkBuildRows",
		Sizes:     []string{"100", "10000"},
		Timeout:   15 * time.Minute,
		BenchTime: "2s",
		Count:     3,
		CPU:       "1",
		Setup:     []string{"docker compose up -d db", "echo 'ready # not a comment'"},
		Env: []string{
			"DATABASE_URL=postgres://u:p@localhost/db?sslmode=disable#frag",
			"EMPTY=",
			"GOFLAGS=-mod=mod",
		},
		Out:         "out",
		PathsMethod: "stack",
		Go:          "/usr/local/go/bin/go",
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got  %+v\nwant %+v", c, want)
	}
}

func TestParseForms(t *testing.T) {
	cases := []struct {
		in    string
		check func(*Config) bool
	}{
		{"sizes: 100,10000", func(c *Config) bool { return reflect.DeepEqual(c.Sizes, []string{"100", "10000"}) }},
		{"sizes: \"small, large\"", func(c *Config) bool { return reflect.DeepEqual(c.Sizes, []string{"small", "large"}) }},
		{"sizes:\n  - 100\n  - 10000", func(c *Config) bool { return reflect.DeepEqual(c.Sizes, []string{"100", "10000"}) }},
		{"setup: make db", func(c *Config) bool { return reflect.DeepEqual(c.Setup, []string{"make db"}) }},
		{"timeout: 90", func(c *Config) bool { return c.Timeout == 90*time.Second }},
		{"bench: 'it''s'", func(c *Config) bool { return c.Bench == "it's" }},
		{"\n# only a comment\n\nbench: X\r\n", func(c *Config) bool { return c.Bench == "X" }},
	}
	for _, tc := range cases {
		c, err := Parse(strings.NewReader(tc.in))
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if !tc.check(c) {
			t.Errorf("%q: got %+v", tc.in, c)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"benhc: X":                    "unknown key",
		"bench: X\nbench: Y":          "set twice",
		"env:\n\tA: 1":                "tabs",
		"env:\n  - A":                 "map",
		"setup:\n  - a\n  b: c":       "mixes",
		"setup: |\n  a":               "block text",
		"  bench: X":                  "unexpected indented",
		"bench X":                     "key: value",
		"count: 0":                    "positive integer",
		"timeout: soon":               "not a duration",
		"timeout: -5s":                "positive",
		"bench: \"open":               "quote",
		"sizes: [1, 2":                "unclosed",
		"bench:\n  - X":               "same line",
		"env: A=1":                    "env must be a map",
		"dir: .\n  extra: 1":          "unexpected indented",
		"env:\n  1BAD: x":             "expected",
		"sizes:\n  A: 1":              "expected a list",
		"paths_method: \"\\q\"":       "bad quoted",
		"bench: X\ngo:\n  - a\n  - b": "same line",
	}
	for in, want := range cases {
		_, err := Parse(strings.NewReader(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", in, err, want)
		}
	}
}

func TestLoadRelativeToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gofl.yaml")
	if err := os.WriteFile(path, []byte("dir: proj\nout: results\nbench: X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Dir != filepath.Join(dir, "proj") || c.Out != filepath.Join(dir, "results") {
		t.Errorf("dir %q out %q: want them relative to %s", c.Dir, c.Out, dir)
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("a missing file loaded")
	}
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("nope: 1\n"), 0o644)
	if _, err := Load(bad); err == nil || !strings.Contains(err.Error(), "bad.yaml: line 1") {
		t.Errorf("error %v should name the file and line", err)
	}
}

func TestExampleFileParses(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "gofl.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Bench == "" || len(c.Sizes) != 2 {
		t.Errorf("example: %+v", c)
	}
}
