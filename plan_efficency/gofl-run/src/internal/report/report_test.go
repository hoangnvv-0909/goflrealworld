package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateReports(t *testing.T) {
	input := filepath.Join("testdata", "stage2")
	dir := t.TempDir()
	for _, hash := range []bool{false, true} {
		name := "plain"
		if hash {
			name = "hashed"
		}
		t.Run(name, func(t *testing.T) {
			output := filepath.Join(dir, name)
			if err := Generate(input, output, hash); err != nil {
				t.Fatal(err)
			}
			jsonText := loadFile(t, filepath.Join(output, "report.json"))
			markdown := loadFile(t, filepath.Join(output, "report.md"))
			var doc document
			if err := json.Unmarshal([]byte(jsonText), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.NamesHashed != hash || doc.Run.PathsMethod != "paper" ||
				len(doc.Hotspots) != 2 || len(doc.CriticalPaths) != 2 {
				t.Fatalf("unexpected report contents: %+v", doc)
			}
			hotspot := doc.Hotspots[0]
			path := doc.CriticalPaths[0]
			if hotspot.DeltaSelfSeconds != 3.9 || hotspot.NDC != 0.71 || path.Score != 3.7 {
				t.Fatalf("unexpected metrics: %+v %+v", hotspot, path)
			}
			if path.Target != hotspot.Function || path.Frames[1].File != hotspot.File {
				t.Fatal("related identifiers do not match")
			}
			for _, raw := range []string{"BuildRows", "SortRows", "main.run", "internal/report/build.go", "cmd/api/main.go"} {
				if strings.Contains(jsonText, raw) == hash || strings.Contains(markdown, raw) == hash {
					t.Errorf("unexpected visibility for %q", raw)
				}
			}
			if hash && (doc.Run.BaseProfile != "" || doc.Run.ScaleProfile != "" || !strings.Contains(path.Signature, "file_")) {
				t.Fatal("profile paths or call signature were not redacted correctly")
			}
			if err := Generate(input, output, hash); err == nil {
				t.Fatal("expected error when report directory already exists")
			}
			if jsonText != loadFile(t, filepath.Join(output, "report.json")) {
				t.Fatal("report changed after retry")
			}
		})
	}
}

func TestGenerateWithEmptyResults(t *testing.T) {
	dir := t.TempDir()
	inputs := map[string]string{
		"run.json":            `{"status":"error","error_code":"NO_POSITIVE_DELTA","paths_method":"stack"}`,
		"hotspots.json":       "[]",
		"critical_paths.json": "[]",
	}
	for name, data := range inputs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(dir, "out")
	if err := Generate(dir, output, false); err != nil {
		t.Fatal(err)
	}
	markdown := loadFile(t, filepath.Join(output, "report.md"))
	for _, want := range []string{"NO&#95;POSITIVE&#95;DELTA", "`stack`", "Không có hotspot"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("missing %q", want)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "hotspots.json"), []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	badOutput := filepath.Join(dir, "bad")
	if err := Generate(dir, badOutput, false); err == nil {
		t.Fatal("expected invalid array error")
	}
	if _, err := os.Stat(badOutput); !os.IsNotExist(err) {
		t.Fatalf("unexpected partial report directory: %v", err)
	}
}

func loadFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Hotspots ranked per operation carry four extra fields (hotspots.json of the
// benchmark mode). The report must show those, not the raw columns that can
// be negative or zero for the very function that was ranked first.
func TestGeneratePerOperationColumns(t *testing.T) {
	dir := t.TempDir()
	inputs := map[string]string{
		"run.json": `{"status":"ok","paths_method":"paper","mode":"bench"}`,
		"hotspots.json": `[{"rank":1,"function":"app.Slow","file":"a.go","baseline_self_seconds":0.9,"scaled_self_seconds":0.5,
"delta_self_seconds":-0.4,"ndc":0,"baseline_samples":9,"scaled_samples":5,"baseline_occurrences":1,"scaled_occurrences":1,
"baseline_per_op_seconds":0.0000017,"scaled_per_op_seconds":0.0117,"delta_per_op_seconds":0.011698,"ndc_per_op":0.98}]`,
		"critical_paths.json": "[]",
	}
	for name, data := range inputs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(dir, "out")
	if err := Generate(dir, output, false); err != nil {
		t.Fatal(err)
	}
	md := loadFile(t, filepath.Join(output, "report.md"))
	for _, want := range []string{"Δ mỗi thao tác", "0.011698", "98.00%"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in\n%s", want, md)
		}
	}
	if strings.Contains(md, "-0.4") {
		t.Errorf("raw delta shown next to per-operation ranking:\n%s", md)
	}
	if js := loadFile(t, filepath.Join(output, "report.json")); !strings.Contains(js, "ndc_per_op") {
		t.Errorf("report.json lost the per-operation fields:\n%s", js)
	}
}
