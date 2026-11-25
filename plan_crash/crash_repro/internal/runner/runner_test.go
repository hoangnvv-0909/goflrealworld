package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module tmpmod\n\ngo 1.21\n"
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRunCollectsPassAndPanic(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"a_test.go": `package tmpmod
import "testing"
func TestOK(t *testing.T) {}
func TestBoom(t *testing.T) { var m map[string]int; m["x"] = 1 }
`,
	})
	res, err := Run(context.Background(), Spec{Dir: dir, Packages: []string{"./..."}, Timeout: time.Minute})
	if err != nil || res.Unrunnable != nil {
		t.Fatalf("err=%v unrunnable=%+v", err, res.Unrunnable)
	}
	var ok, boom *TestResult
	for i := range res.Tests {
		switch res.Tests[i].Test {
		case "TestOK":
			ok = &res.Tests[i]
		case "TestBoom":
			boom = &res.Tests[i]
		}
	}
	if ok == nil || !ok.Passed || boom == nil || boom.Passed {
		t.Fatalf("results: %+v", res.Tests)
	}
	if !strings.Contains(res.Output(), "panic: assignment to entry in nil map") {
		t.Errorf("panic text not captured:\n%s", res.Output())
	}
}

func TestRunBuildFailure(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"a_test.go": "package tmpmod\nimport \"testing\"\nfunc TestX(t *testing.T) { undefined() }\n",
	})
	res, err := Run(context.Background(), Spec{Dir: dir, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unrunnable == nil || res.Unrunnable.Kind != "build" {
		t.Fatalf("want build failure, got %+v", res.Unrunnable)
	}
	if !strings.Contains(res.Unrunnable.Detail, "undefined") {
		t.Errorf("detail lacks the compiler message: %q", res.Unrunnable.Detail)
	}
}

func TestRunTimeout(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"a_test.go": "package tmpmod\nimport (\"testing\";\"time\")\nfunc TestSlow(t *testing.T) { time.Sleep(time.Minute) }\n",
	})
	start := time.Now()
	res, _ := Run(context.Background(), Spec{Dir: dir, Timeout: 20 * time.Second})
	if res.Unrunnable == nil || res.Unrunnable.Kind != "timeout" {
		t.Fatalf("want timeout, got %+v", res.Unrunnable)
	}
	if time.Since(start) > 50*time.Second {
		t.Error("timeout not honoured")
	}
}

func TestRunMisuseAndEnv(t *testing.T) {
	if _, err := Run(context.Background(), Spec{}); err == nil {
		t.Error("empty Dir should be an error")
	}
	res, _ := Run(context.Background(), Spec{Dir: filepath.Join(t.TempDir(), "missing")})
	if res.Unrunnable == nil || res.Unrunnable.Kind != "env" {
		t.Errorf("missing dir: %+v", res.Unrunnable)
	}
}

func TestRunSetupFailureAndEnv(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"a_test.go": `package tmpmod
import ("os";"testing")
func TestEnv(t *testing.T) { if os.Getenv("GOFL_X") != "1" { t.Fatal("env missing") } }
`,
	})
	res, _ := Run(context.Background(), Spec{Dir: dir, Env: []string{"GOFL_X=1"}, Timeout: time.Minute})
	if res.Unrunnable != nil || len(res.Tests) == 0 {
		t.Fatalf("%+v", res)
	}
	for _, tr := range res.Tests {
		if tr.Test == "TestEnv" && !tr.Passed {
			t.Error("env not passed to the test process")
		}
	}
	res, _ = Run(context.Background(), Spec{Dir: dir, Setup: []string{"gofl-no-such-command"}, Timeout: time.Minute})
	if res.Unrunnable == nil || res.Unrunnable.Kind != "setup" {
		t.Errorf("setup failure: %+v", res.Unrunnable)
	}
}

func TestParseJSONKeepsNonJSON(t *testing.T) {
	p := parseJSON([]byte("# pkg\nsome plain line\n" +
		`{"Action":"output","Package":"p","Test":"T","Output":"hello\n"}` + "\n" +
		`{"Action":"fail","Package":"p","Test":"T","Elapsed":0.5}` + "\n"))
	if len(p.Tests) != 2 || p.Tests[0].Package != "" || !strings.Contains(p.Tests[0].Output, "plain line") {
		t.Fatalf("%+v", p.Tests)
	}
	if p.Tests[1].Passed || p.Tests[1].Elapsed != 500*time.Millisecond {
		t.Errorf("%+v", p.Tests[1])
	}
}
