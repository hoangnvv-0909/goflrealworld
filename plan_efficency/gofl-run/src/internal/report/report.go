package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Generate reads the three v0 input files and publishes both reports in a new directory.
func Generate(inputDir, outputDir string, hashNames bool) error {
	doc := document{SchemaVersion: 1, NamesHashed: hashNames}
	if err := readJSON(inputDir, "run.json", &doc.Run); err != nil {
		return err
	}
	if doc.Run.Status != "ok" && doc.Run.Status != "error" {
		return errors.New("run.json: status must be ok or error")
	}
	if err := readJSON(inputDir, "hotspots.json", &doc.Hotspots); err != nil {
		return err
	}
	if err := readJSON(inputDir, "critical_paths.json", &doc.CriticalPaths); err != nil {
		return err
	}
	if doc.Hotspots == nil || doc.CriticalPaths == nil {
		return errors.New("hotspots.json and critical_paths.json must contain arrays (use [] if empty)")
	}
	if hashNames {
		if err := hashIdentifiers(&doc); err != nil {
			return err
		}
	}
	reportJSON, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report.json: %w", err)
	}

	outputDir = filepath.Clean(outputDir)
	if _, err := os.Lstat(outputDir); err == nil {
		return fmt.Errorf("output directory already exists: %s", outputDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create output parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".gofl-report-")
	if err != nil {
		return fmt.Errorf("create report staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "report.json"), append(reportJSON, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "report.md"), markdown(doc), 0o600); err != nil {
		return err
	}
	if err := os.Rename(stage, outputDir); err != nil {
		return fmt.Errorf("publish reports: %w", err)
	}
	return nil
}

func readJSON(dir, name string, target any) error {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	return nil
}
