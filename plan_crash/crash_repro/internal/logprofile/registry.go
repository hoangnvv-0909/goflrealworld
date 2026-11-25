package logprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var (
	regMu    sync.RWMutex
	profiles = map[string]Profile{}
)

// DefaultName is the profile used when -profile is not given.
const DefaultName = "default"

func init() {
	if err := Register(NewDefault()); err != nil {
		panic(err)
	}
}

// Register adds a profile under its Name. Names are unique and never empty.
func Register(p Profile) error {
	name := p.Name()
	if name == "" {
		return fmt.Errorf("logprofile: profile with empty name")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := profiles[name]; dup {
		return fmt.Errorf("logprofile: profile %q already registered", name)
	}
	profiles[name] = p
	return nil
}

// Get returns the profile registered under name.
func Get(name string) (Profile, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	p, ok := profiles[name]
	return p, ok
}

// Names lists the registered profiles, sorted, for usage messages.
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(profiles))
	for n := range profiles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Lookup returns the profile for -profile: a registered name, or else the
// path of a JSON file describing a custom profile (see Load).
func Lookup(nameOrPath string) (Profile, error) {
	if nameOrPath == "" {
		nameOrPath = DefaultName
	}
	if p, ok := Get(nameOrPath); ok {
		return p, nil
	}
	if _, err := os.Stat(nameOrPath); err == nil {
		return Load(nameOrPath)
	}
	return nil, fmt.Errorf("logprofile: unknown profile %q (have: %s, or the path of a JSON profile file)",
		nameOrPath, strings.Join(Names(), ", "))
}

// FileProfile is the content of a custom profile file. Users describe the
// decoration their logger puts in front of each line instead of writing Go:
//
//	{"name": "svc", "strip_prefix": "^svc\\[\\d+\\]: "}
//
// Everything else (timestamps, levels, docker compose, JSON lines) works as
// in the default profile.
type FileProfile struct {
	Name          string `json:"name"`
	StripPrefix   string `json:"strip_prefix"`
	MaxBlockLines int    `json:"max_block_lines"`
}

// Load reads a custom profile from a JSON file.
func Load(path string) (*Default, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("logprofile: %w", err)
	}
	var fp FileProfile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fp); err != nil {
		return nil, fmt.Errorf("logprofile %s: %w", path, err)
	}
	d := NewDefault()
	d.ID = fp.Name
	if d.ID == "" {
		d.ID = path
	}
	if fp.MaxBlockLines > 0 {
		d.MaxBlockLines = fp.MaxBlockLines
	}
	if fp.StripPrefix != "" {
		re, err := regexp.Compile(fp.StripPrefix)
		if err != nil {
			return nil, fmt.Errorf("logprofile %s: strip_prefix: %w", path, err)
		}
		d.StripPrefix = re
	}
	return d, nil
}
