package proc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExitCodeAndOutput(t *testing.T) {
	var out bytes.Buffer
	res := Run(context.Background(), Spec{Shell: "echo hello && exit 3", Out: &out})
	if res.ExitCode != 3 || res.TimedOut || res.Err != nil || res.OK() {
		t.Errorf("got %+v", res)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("output %q", out.String())
	}
	if res := Run(context.Background(), Spec{Shell: "exit 0"}); !res.OK() {
		t.Errorf("exit 0: %+v", res)
	}
}

func TestQuotesSurviveTheShell(t *testing.T) {
	var out bytes.Buffer
	Run(context.Background(), Spec{Shell: `echo "a  b"`, Out: &out})
	if !strings.Contains(out.String(), "a  b") {
		t.Errorf("output %q", out.String())
	}
}

func TestEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	line := "echo $GOFL_X && pwd"
	if runtime.GOOS == "windows" {
		line = "echo %GOFL_X% && cd"
	}
	Run(context.Background(), Spec{Shell: line, Dir: dir, Env: append(os.Environ(), "GOFL_X=value"), Out: &out})
	if !strings.Contains(out.String(), "value") || !strings.Contains(strings.ToLower(out.String()), strings.ToLower(filepath.Base(dir))) {
		t.Errorf("output %q", out.String())
	}
}

func TestTimeoutKillsTheTree(t *testing.T) {
	// The shell starts a child that would run for a minute.
	line := "sleep 60"
	if runtime.GOOS == "windows" {
		line = "ping -n 60 127.0.0.1 >NUL"
	}
	start := time.Now()
	res := Run(context.Background(), Spec{Shell: line, Timeout: time.Second})
	if !res.TimedOut || res.OK() {
		t.Errorf("got %+v", res)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("took %s: the child outlived the timeout", d)
	}
}

func TestMissingBinary(t *testing.T) {
	res := Run(context.Background(), Spec{Name: filepath.Join(t.TempDir(), "nothing")})
	if res.Err == nil || res.OK() {
		t.Errorf("got %+v", res)
	}
}

func TestCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := Run(ctx, Spec{Shell: "exit 0"})
	if res.Err == nil {
		t.Errorf("got %+v", res)
	}
}
