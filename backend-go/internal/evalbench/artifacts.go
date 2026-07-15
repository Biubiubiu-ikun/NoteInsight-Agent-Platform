package evalbench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
)

func WriteArtifacts(root string, benchmark Benchmark) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("create benchmark artifact directory: %w", err)
	}
	manifestRaw, err := json.MarshalIndent(benchmark.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode benchmark manifest: %w", err)
	}
	manifestRaw = append(manifestRaw, '\n')
	if err := writeAtomic(filepath.Join(root, "manifest.json"), manifestRaw); err != nil {
		return err
	}

	temporary := filepath.Join(root, "cases.jsonl.tmp")
	file, err := os.Create(temporary)
	if err != nil {
		return fmt.Errorf("create benchmark cases artifact: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, evalCase := range benchmark.Cases {
		raw, err := json.Marshal(evalCase)
		if err != nil {
			_ = file.Close()
			_ = os.Remove(temporary)
			return fmt.Errorf("encode benchmark case: %w", err)
		}
		if _, err := writer.Write(append(raw, '\n')); err != nil {
			_ = file.Close()
			_ = os.Remove(temporary)
			return fmt.Errorf("write benchmark case: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return fmt.Errorf("flush benchmark cases: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("close benchmark cases: %w", err)
	}
	final := filepath.Join(root, "cases.jsonl")
	if err := os.Rename(temporary, final); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish benchmark cases: %w", err)
	}
	return nil
}

func VerifyArtifacts(root string) (Manifest, error) {
	manifestPath := filepath.Join(root, "manifest.json")
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read benchmark manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode benchmark manifest: %w", err)
	}
	if manifest.Status != "frozen" {
		return Manifest{}, fmt.Errorf("benchmark status must be frozen, got %q", manifest.Status)
	}
	if manifest.CasesFile == "" || filepath.Base(manifest.CasesFile) != manifest.CasesFile {
		return Manifest{}, fmt.Errorf("cases_file must be a local filename")
	}

	casesPath := filepath.Join(root, manifest.CasesFile)
	file, err := os.Open(casesPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("open benchmark cases: %w", err)
	}
	defer file.Close()

	cases := make([]Case, 0, manifest.CaseCount)
	checksums := make(map[string]struct{}, manifest.CaseCount)
	queries := make(map[string]struct{}, manifest.CaseCount)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		var evalCase Case
		if err := json.Unmarshal(scanner.Bytes(), &evalCase); err != nil {
			return Manifest{}, fmt.Errorf("decode benchmark case line %d: %w", line, err)
		}
		if actual := checksumCase(evalCase); actual != evalCase.CaseChecksum {
			return Manifest{}, fmt.Errorf("case checksum mismatch at line %d", line)
		}
		if _, exists := checksums[evalCase.CaseChecksum]; exists {
			return Manifest{}, fmt.Errorf("duplicate case checksum at line %d", line)
		}
		if _, exists := queries[evalCase.Query]; exists {
			return Manifest{}, fmt.Errorf("duplicate benchmark query at line %d", line)
		}
		checksums[evalCase.CaseChecksum] = struct{}{}
		queries[evalCase.Query] = struct{}{}
		cases = append(cases, evalCase)
	}
	if err := scanner.Err(); err != nil {
		return Manifest{}, fmt.Errorf("scan benchmark cases: %w", err)
	}

	rebuilt := buildManifest(Config{
		BenchmarkID:      manifest.BenchmarkID,
		BenchmarkVersion: manifest.BenchmarkVersion,
		SourceRunID:      manifest.SourceRunID,
		GeneratorVersion: manifest.GeneratorVersion,
		Seed:             manifest.Seed,
	}, cases)
	if rebuilt.ManifestChecksum != manifest.ManifestChecksum {
		return Manifest{}, fmt.Errorf("manifest checksum mismatch: got %s, want %s", rebuilt.ManifestChecksum, manifest.ManifestChecksum)
	}
	if rebuilt.CaseCount != manifest.CaseCount ||
		!maps.Equal(rebuilt.SplitCounts, manifest.SplitCounts) ||
		!maps.Equal(rebuilt.TaskCounts, manifest.TaskCounts) ||
		!maps.Equal(rebuilt.ReviewCounts, manifest.ReviewCounts) {
		return Manifest{}, fmt.Errorf("manifest counts do not match cases file")
	}
	return manifest, nil
}

func writeAtomic(path string, contents []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0644); err != nil {
		return fmt.Errorf("write benchmark artifact: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish benchmark artifact: %w", err)
	}
	return nil
}
