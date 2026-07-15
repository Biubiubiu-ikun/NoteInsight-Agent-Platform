package evalbench

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGenerateCreatesBalancedFrozenSplits(t *testing.T) {
	config := testConfig()
	benchmark, err := Generate(config, testDocuments(13))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if benchmark.Manifest.Status != "frozen" || benchmark.Manifest.CaseCount != 24 {
		t.Fatalf("manifest = %+v", benchmark.Manifest)
	}
	if benchmark.Manifest.SplitCounts["development"] != 8 || benchmark.Manifest.SplitCounts["holdout"] != 16 {
		t.Fatalf("split counts = %+v", benchmark.Manifest.SplitCounts)
	}
	for _, taskType := range taskOrder {
		if benchmark.Manifest.TaskCounts[taskType] != 4 {
			t.Fatalf("task %s count = %d, want 4", taskType, benchmark.Manifest.TaskCounts[taskType])
		}
	}
	checksums := map[string]struct{}{}
	for _, evalCase := range benchmark.Cases {
		if len(evalCase.CaseChecksum) != 64 {
			t.Fatalf("case checksum = %q", evalCase.CaseChecksum)
		}
		if _, duplicate := checksums[evalCase.CaseChecksum]; duplicate {
			t.Fatalf("duplicate checksum %s", evalCase.CaseChecksum)
		}
		checksums[evalCase.CaseChecksum] = struct{}{}
		if evalCase.TaskType == "no_answer" && len(evalCase.GoldSources) != 0 {
			t.Fatal("no-answer case must not contain a gold source")
		}
		if evalCase.TaskType == "authorization_boundary" && evalCase.Metadata["unauthorized_expected_results"] != 0 {
			t.Fatalf("authorization case metadata = %+v", evalCase.Metadata)
		}
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	first, err := Generate(testConfig(), testDocuments(13))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(testConfig(), testDocuments(13))
	if err != nil {
		t.Fatal(err)
	}
	if first.Manifest.ManifestChecksum != second.Manifest.ManifestChecksum || !reflect.DeepEqual(first.Cases, second.Cases) {
		t.Fatal("same config and source documents must produce identical benchmark artifacts")
	}
}

func TestWriteArtifactsCreatesReviewableManifestAndJSONL(t *testing.T) {
	benchmark, err := Generate(testConfig(), testDocuments(13))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := WriteArtifacts(directory, benchmark); err != nil {
		t.Fatalf("WriteArtifacts() error = %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil || len(manifest) == 0 {
		t.Fatalf("manifest read error = %v", err)
	}
	file, err := os.Open(filepath.Join(directory, "cases.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	lines := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != len(benchmark.Cases) {
		t.Fatalf("JSONL lines = %d, want %d", lines, len(benchmark.Cases))
	}
	verified, err := VerifyArtifacts(directory)
	if err != nil {
		t.Fatalf("VerifyArtifacts() error = %v", err)
	}
	if verified.ManifestChecksum != benchmark.Manifest.ManifestChecksum {
		t.Fatalf("verified checksum = %s, want %s", verified.ManifestChecksum, benchmark.Manifest.ManifestChecksum)
	}
}

func TestVerifyArtifactsRejectsTamperedCase(t *testing.T) {
	benchmark, err := Generate(testConfig(), testDocuments(13))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := WriteArtifacts(directory, benchmark); err != nil {
		t.Fatal(err)
	}
	casesPath := filepath.Join(directory, "cases.jsonl")
	raw, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[20] ^= 1
	if err := os.WriteFile(casesPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyArtifacts(directory); err == nil {
		t.Fatal("VerifyArtifacts() accepted a tampered cases file")
	}
}

func testConfig() Config {
	return Config{
		BenchmarkID:      "retrieval_test_v1",
		BenchmarkVersion: "retrieval_test_v1",
		SourceRunID:      "quality_test_run",
		GeneratorVersion: DefaultGeneratorVersion,
		Seed:             20260715,
		CaseCount:        24,
		DevelopmentCases: 8,
	}
}

func testDocuments(count int) []SourceDocument {
	documents := make([]SourceDocument, 0, count)
	for index := 0; index < count; index++ {
		documents = append(documents, SourceDocument{
			NoteID:    int64(1000 + index),
			ProjectID: int64(1 + index%2),
			Title:     fmt.Sprintf("测试笔记 %d", index),
			Body:      fmt.Sprintf("正文 %d", index),
			Scenario: Scenario{
				Subject:        fmt.Sprintf("主题%d", index),
				Audience:       fmt.Sprintf("人群%d", index),
				KeyMetric:      fmt.Sprintf("观察%d天", index+1),
				Conclusion:     fmt.Sprintf("结论%d", index),
				NotSuitableFor: fmt.Sprintf("不适合人群%d", index),
			},
		})
	}
	return documents
}
