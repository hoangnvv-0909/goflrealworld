//go:build ignore

// Build gofl for every supported system into bin/.
//
//	go run scripts/build.go            # all targets, into ./bin
//	go run scripts/build.go -out dist  # another directory
//	go run scripts/build.go -only linux/amd64
//
// Needs only Go (1.26 or later) and the module's own files: no network
// beyond the first download of github.com/google/pprof.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var targets = []string{
	"linux/amd64", "linux/arm64",
	"darwin/amd64", "darwin/arm64",
	"windows/amd64", "windows/arm64",
}

func main() {
	out := flag.String("out", "bin", "output directory")
	only := flag.String("only", "", "build one target, e.g. linux/amd64")
	flag.Parse()
	list := targets
	if *only != "" {
		list = []string{*only}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	for _, t := range list {
		goos, goarch, ok := strings.Cut(t, "/")
		if !ok || goos == "" || goarch == "" {
			fail(fmt.Errorf("bad target %q, want os/arch", t))
		}
		name := "gofl-" + goos + "-" + goarch
		if goos == "windows" {
			name += ".exe"
		}
		fmt.Fprintln(os.Stderr, "building", name)
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w",
			"-o", filepath.Join(*out, name), "./cmd/gofl")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fail(fmt.Errorf("%s: %w", t, err))
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "build:", err)
	os.Exit(1)
}
