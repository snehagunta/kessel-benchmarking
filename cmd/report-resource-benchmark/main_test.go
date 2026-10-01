package main

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendCSVWritesHeaderToEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "result.csv")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	result := benchmarkResult{RunID: "test-run", Workload: workloadCreate, Requests: 1, Successes: 1, Concurrency: 1, ResourcePool: 1}
	if err := appendCSV(path, 1, "localhost:9000", result); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0][0] != "timestamp" {
		t.Fatalf("CSV records = %v, want header and one result", records)
	}
}

func TestAppendCSVCreatesOutputDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results", "result.csv")
	result := benchmarkResult{RunID: "test-run", Workload: workloadCreate, Requests: 1, Successes: 1, Concurrency: 1, ResourcePool: 1}

	if err := appendCSV(path, 1, "localhost:9000", result); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat output CSV: %v", err)
	}
}

func TestRunCommandDoesNotWriteSetupFailure(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "result.csv")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runCommand([]string{
		"-endpoint=unused.invalid:9000",
		"-requests=0",
		"-output=" + outputPath,
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected setup error")
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("output file was created for setup failure: %v", statErr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty output", stdout.String())
	}
}
