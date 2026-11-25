package goenv

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindExplicitMustExist(t *testing.T) {
	if _, err := Find(filepath.Join(t.TempDir(), "go")); err == nil {
		t.Error("a missing explicit go binary was accepted")
	}
	t.Setenv("GOFL_GO", filepath.Join(t.TempDir(), "go"))
	if _, err := Find(""); err == nil {
		t.Error("a missing GOFL_GO was accepted")
	}
}

func TestProbe(t *testing.T) {
	bin, err := Find("")
	if err != nil {
		t.Skip("go not found")
	}
	info, err := Probe(context.Background(), bin, filepath.Join("..", "..", "testdata", "sample"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.Version, "go") || !strings.Contains(info.OS, "/") || !strings.HasSuffix(info.GoMod, "go.mod") {
		t.Errorf("sample: %+v", info)
	}
	info, err = Probe(context.Background(), bin, t.TempDir(), []string{"GO111MODULE=on", "GOWORK=off"})
	if err != nil {
		t.Fatal(err)
	}
	if info.GoMod != "" {
		t.Errorf("temp dir has go.mod %q", info.GoMod)
	}
}
