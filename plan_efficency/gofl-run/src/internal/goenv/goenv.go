// Package goenv finds the Go toolchain gofl drives and asks it about the
// project: its version, the target OS and whether the project is a module.
package goenv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gofl/internal/proc"
)

// Info describes a working Go toolchain.
type Info struct {
	Path    string // the go binary
	Version string // go1.26.1
	OS      string // linux/amd64 (the target GOOS/GOARCH)
	GoMod   string // go.mod of the project, "" if there is none
}

// ErrNotFound means no go binary was found.
var ErrNotFound = errors.New("go not found: install Go, put it on PATH or set GOFL_GO")

// Find returns the go binary to use. An explicit path (the -go flag or the
// go key of gofl.yaml) must work; otherwise $GOFL_GO, PATH, $GOROOT/bin and
// the usual install directories are tried in that order.
func Find(explicit string) (string, error) {
	if explicit != "" {
		if isFile(explicit) {
			return explicit, nil
		}
		if p, err := exec.LookPath(explicit); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("go binary %q not found", explicit)
	}
	if p := os.Getenv("GOFL_GO"); p != "" {
		if isFile(p) {
			return p, nil
		}
		return "", fmt.Errorf("GOFL_GO=%q does not exist", p)
	}
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}
	exe := "go"
	if runtime.GOOS == "windows" {
		exe = "go.exe"
	}
	var dirs []string
	if root := os.Getenv("GOROOT"); root != "" {
		dirs = append(dirs, filepath.Join(root, "bin"))
	}
	if runtime.GOOS == "windows" {
		dirs = append(dirs, `C:\Program Files\Go\bin`, `C:\Go\bin`)
	} else {
		dirs = append(dirs, "/usr/local/go/bin", "/usr/lib/go/bin", "/opt/homebrew/bin", "/usr/local/bin", "/snap/bin")
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "go", "bin"), filepath.Join(home, "sdk", "go", "bin"))
	}
	for _, d := range dirs {
		if p := filepath.Join(d, exe); isFile(p) {
			return p, nil
		}
	}
	return "", ErrNotFound
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Probe runs `go env` in dir and returns what it reports. env is the full
// environment for the command (nil: the parent's).
func Probe(ctx context.Context, goBin, dir string, env []string) (*Info, error) {
	var out bytes.Buffer
	res := proc.Run(ctx, proc.Spec{
		Name:    goBin,
		Args:    []string{"env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOMOD"},
		Dir:     dir,
		Env:     env,
		Timeout: time.Minute,
		Out:     &out,
	})
	if !res.OK() {
		msg := strings.TrimSpace(out.String())
		if res.Err != nil {
			msg = res.Err.Error()
		} else if res.TimedOut {
			msg = "go env did not answer within a minute"
		}
		return nil, fmt.Errorf("%s env: %s", goBin, msg)
	}
	var v struct{ GOVERSION, GOOS, GOARCH, GOMOD string }
	// go env may print warnings before the JSON object.
	text := out.String()
	if i := strings.Index(text, "{"); i > 0 {
		text = text[i:]
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, fmt.Errorf("%s env: unexpected output: %v", goBin, err)
	}
	info := &Info{Path: goBin, Version: v.GOVERSION, OS: v.GOOS + "/" + v.GOARCH, GoMod: v.GOMOD}
	// GOMOD is empty outside module mode and os.DevNull when there is no go.mod.
	if info.GoMod == os.DevNull || strings.EqualFold(info.GoMod, "NUL") || info.GoMod == "/dev/null" {
		info.GoMod = ""
	}
	return info, nil
}

// HostOS is the OS of the gofl binary itself, used when Go is not available.
func HostOS() string { return runtime.GOOS + "/" + runtime.GOARCH }
