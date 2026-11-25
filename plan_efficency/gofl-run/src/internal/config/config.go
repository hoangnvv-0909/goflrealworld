// Package config reads gofl.yaml, the small file the team leader fills in.
//
// Only the part of YAML the file needs is accepted: comments, top-level
// "key: value" lines, inline lists ([100, 10000]), and indented "- item"
// lists or "name: value" maps under a key with no value. Anything else is an
// error with its line number, so a typo never goes unnoticed.
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config is gofl.yaml. Empty fields mean "not set".
type Config struct {
	Dir         string        // project root (the directory with go.mod)
	Package     string        // package holding the benchmark: ./internal/report or an import path
	Bench       string        // benchmark function, e.g. BenchmarkBuildRows
	Sizes       []string      // the small and the large size, e.g. 100, 10000
	Timeout     time.Duration // limit for each setup command and each benchmark run
	BenchTime   string        // -benchtime for each size: 1s, 3s, 100x
	Count       int           // -count
	CPU         string        // -cpu, e.g. 1
	Setup       []string      // commands run before the benchmark (start a database...)
	Env         []string      // KEY=VALUE pairs for setup, build and benchmark
	Out         string        // output directory
	PathsMethod string        // paper or stack
	Go          string        // go binary to use
}

// Keys lists the accepted keys, in the order gofl.example.yaml uses.
var Keys = []string{"dir", "package", "bench", "sizes", "timeout", "benchtime", "count", "cpu", "setup", "env", "out", "paths_method", "go"}

// Load reads a gofl.yaml file. Relative dir and out values are taken
// relative to the directory of the file.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	base := filepath.Dir(path)
	for _, p := range []*string{&c.Dir, &c.Out} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	return c, nil
}

var keyLine = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*:(.*)$`)

type entry struct {
	line  int
	value string   // inline value
	items []string // "- item" lines
	pairs []string // "name: value" lines, as name=value
	block bool     // the key had no inline value
}

// Parse reads gofl.yaml from r.
func Parse(r io.Reader) (*Config, error) {
	entries := map[string]*entry{}
	var cur *entry
	var curKey string
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		raw := strings.TrimRight(sc.Text(), " \r")
		if n == 1 {
			raw = strings.TrimPrefix(raw, string(rune(0xFEFF))) // byte order mark
		}
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if strings.Contains(line[:indent], "\t") {
			return nil, fmt.Errorf("line %d: indent with spaces, not tabs", n)
		}
		text := strings.TrimSpace(line)
		if indent > 0 {
			if cur == nil || !cur.block {
				return nil, fmt.Errorf("line %d: unexpected indented line", n)
			}
			if strings.HasPrefix(text, "- ") || text == "-" {
				if len(cur.pairs) > 0 {
					return nil, fmt.Errorf("line %d: %s mixes a list and a map", n, curKey)
				}
				v, err := scalar(strings.TrimSpace(strings.TrimPrefix(text, "-")))
				if err != nil {
					return nil, fmt.Errorf("line %d: %v", n, err)
				}
				cur.items = append(cur.items, v)
				continue
			}
			m := keyLine.FindStringSubmatch(text)
			if m == nil {
				return nil, fmt.Errorf("line %d: expected \"- item\" or \"NAME: value\"", n)
			}
			if len(cur.items) > 0 {
				return nil, fmt.Errorf("line %d: %s mixes a list and a map", n, curKey)
			}
			v, err := scalar(strings.TrimSpace(m[2]))
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", n, err)
			}
			cur.pairs = append(cur.pairs, m[1]+"="+v)
			continue
		}
		m := keyLine.FindStringSubmatch(text)
		if m == nil {
			return nil, fmt.Errorf("line %d: expected \"key: value\"", n)
		}
		key, val := m[1], strings.TrimSpace(m[2])
		if !known(key) {
			return nil, fmt.Errorf("line %d: unknown key %q (known: %s)", n, key, strings.Join(Keys, ", "))
		}
		if entries[key] != nil {
			return nil, fmt.Errorf("line %d: %s is set twice", n, key)
		}
		if val == "|" || val == ">" || strings.HasPrefix(val, "|") || strings.HasPrefix(val, ">") {
			return nil, fmt.Errorf("line %d: block text is not supported; use a list of \"- command\" lines", n)
		}
		cur = &entry{line: n, value: val, block: val == ""}
		curKey = key
		entries[key] = cur
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return build(entries)
}

func known(k string) bool {
	for _, x := range Keys {
		if x == k {
			return true
		}
	}
	return false
}

// stripComment removes a # comment that is not inside quotes.
func stripComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return s[:i]
		}
	}
	return s
}

// scalar unquotes a value.
func scalar(s string) (string, error) {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		v, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("bad quoted value %s", s)
		}
		return v, nil
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
	}
	if strings.HasPrefix(s, "\"") || strings.HasPrefix(s, "'") {
		return "", fmt.Errorf("unclosed quote in %s", s)
	}
	return s, nil
}

// list reads an inline list, a comma separated value or "- item" lines.
func (e *entry) list() ([]string, error) {
	if e.block {
		if len(e.pairs) > 0 {
			return nil, fmt.Errorf("line %d: expected a list", e.line)
		}
		return e.items, nil
	}
	v := e.value
	if strings.HasPrefix(v, "[") {
		if !strings.HasSuffix(v, "]") {
			return nil, fmt.Errorf("line %d: unclosed [", e.line)
		}
		v = v[1 : len(v)-1]
	} else if q, err := scalar(v); err == nil {
		v = q
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		s, err := scalar(part)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", e.line, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func (e *entry) text() (string, error) {
	if e.block {
		return "", fmt.Errorf("line %d: expected a value on the same line", e.line)
	}
	v, err := scalar(e.value)
	if err != nil {
		return "", fmt.Errorf("line %d: %v", e.line, err)
	}
	return v, nil
}

func build(entries map[string]*entry) (*Config, error) {
	c := &Config{}
	strs := map[string]*string{
		"dir": &c.Dir, "package": &c.Package, "bench": &c.Bench, "benchtime": &c.BenchTime,
		"cpu": &c.CPU, "out": &c.Out, "paths_method": &c.PathsMethod, "go": &c.Go,
	}
	for k, p := range strs {
		if e := entries[k]; e != nil {
			v, err := e.text()
			if err != nil {
				return nil, err
			}
			*p = v
		}
	}
	if e := entries["sizes"]; e != nil {
		v, err := e.list()
		if err != nil {
			return nil, err
		}
		c.Sizes = v
	}
	if e := entries["setup"]; e != nil {
		if e.block {
			v, err := e.list()
			if err != nil {
				return nil, err
			}
			c.Setup = v
		} else {
			v, err := e.text()
			if err != nil {
				return nil, err
			}
			if v != "" {
				c.Setup = []string{v}
			}
		}
	}
	if e := entries["env"]; e != nil {
		if !e.block || len(e.items) > 0 {
			return nil, fmt.Errorf("line %d: env must be a map of indented \"NAME: value\" lines", e.line)
		}
		c.Env = e.pairs
	}
	if e := entries["timeout"]; e != nil {
		v, err := e.text()
		if err != nil {
			return nil, err
		}
		d, err := ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("line %d: timeout: %v", e.line, err)
		}
		c.Timeout = d
	}
	if e := entries["count"]; e != nil {
		v, err := e.text()
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("line %d: count must be a positive integer", e.line)
		}
		c.Count = n
	}
	return c, nil
}

// ParseDuration accepts Go durations (90s, 10m, 1h30m) and plain seconds.
func ParseDuration(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("must be positive")
		}
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 90s or 10m", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return d, nil
}
