package evalbench

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestWritePublicArtifactsSealsHoldoutContent(t *testing.T) {
	benchmark, err := Generate(testConfig(), testDocuments(13))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := WritePublicArtifacts(directory, benchmark); err != nil {
		t.Fatalf("WritePublicArtifacts() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "cases.jsonl")); !os.IsNotExist(err) {
		t.Fatal("public artifacts must not contain the full cases file")
	}

	developmentRaw, err := os.ReadFile(filepath.Join(directory, "development.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := bytes.Count(developmentRaw, []byte{'\n'}); lines != benchmark.Manifest.SplitCounts["development"] {
		t.Fatalf("development lines = %d, want %d", lines, benchmark.Manifest.SplitCounts["development"])
	}
	commitmentsRaw, err := os.ReadFile(filepath.Join(directory, "case_commitments.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(commitmentsRaw, []byte(`"query"`)) || bytes.Contains(commitmentsRaw, []byte(`"expected_answer"`)) {
		t.Fatal("public commitments exposed holdout question or answer fields")
	}
	if lines := bytes.Count(commitmentsRaw, []byte{'\n'}); lines != benchmark.Manifest.CaseCount {
		t.Fatalf("commitment lines = %d, want %d", lines, benchmark.Manifest.CaseCount)
	}

	verified, err := VerifyArtifacts(directory)
	if err != nil {
		t.Fatalf("VerifyArtifacts() error = %v", err)
	}
	if verified.ManifestChecksum != benchmark.Manifest.ManifestChecksum {
		t.Fatalf("verified checksum = %s, want %s", verified.ManifestChecksum, benchmark.Manifest.ManifestChecksum)
	}
}

func TestVerifyPublicArtifactsRejectsTamperedCommitment(t *testing.T) {
	benchmark, err := Generate(testConfig(), testDocuments(13))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := WritePublicArtifacts(directory, benchmark); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "case_commitments.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(benchmark.Cases[0].CaseChecksum), []byte(strings.Repeat("0", 64)), 1)
	if err := os.WriteFile(path, tampered, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyArtifacts(directory); err == nil {
		t.Fatal("VerifyArtifacts() accepted a tampered public commitment")
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

func TestStageArtifactsVerifiesBeforeAtomicPublish(t *testing.T) {
	benchmark, err := FreezeAuthored(Config{
		BenchmarkID:      "retrieval_staged_v4",
		BenchmarkVersion: "retrieval_staged_v4",
		SourceRunID:      "quality_test_run",
		GeneratorVersion: AuthoredGeneratorVersion,
		CaseCount:        6,
		DevelopmentCases: 2,
		DatasetVersionID: 9,
		CommitmentScheme: NonceCommitmentScheme,
	}, authoredTestCases())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	publicTarget := filepath.Join(root, "public", "retrieval_v4")
	privateTarget := filepath.Join(root, "private", "retrieval_v4")
	stage, err := StageArtifacts(publicTarget, privateTarget, benchmark)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(publicTarget); !os.IsNotExist(err) {
		t.Fatalf("public target exists before publish: %v", err)
	}
	if _, err := VerifyArtifacts(stage.PublicDirectory); err != nil {
		t.Fatalf("staged public artifacts are invalid: %v", err)
	}
	if err := stage.Publish(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyArtifacts(publicTarget); err != nil {
		t.Fatalf("published public artifacts are invalid: %v", err)
	}
	if _, err := VerifyArtifacts(privateTarget); err != nil {
		t.Fatalf("published private artifacts are invalid: %v", err)
	}
	if _, err := StageArtifacts(publicTarget, filepath.Join(root, "another-private"), benchmark); err == nil {
		t.Fatal("staging unexpectedly accepted an existing target")
	}
}

func TestFreezeAuthoredUsesNonceCommitmentsAndSeparatesNoAnswerSemantics(t *testing.T) {
	config := Config{
		BenchmarkID:      "retrieval_authored_v4",
		BenchmarkVersion: "retrieval_authored_v4",
		SourceRunID:      "quality_test_run",
		GeneratorVersion: AuthoredGeneratorVersion,
		CaseCount:        6,
		DevelopmentCases: 2,
		DatasetVersionID: 9,
		CommitmentScheme: NonceCommitmentScheme,
	}
	benchmark, err := FreezeAuthored(config, authoredTestCases())
	if err != nil {
		t.Fatalf("FreezeAuthored() error = %v", err)
	}
	if benchmark.Manifest.CommitmentScheme != NonceCommitmentScheme || benchmark.Manifest.DatasetVersionID != 9 {
		t.Fatalf("manifest = %+v", benchmark.Manifest)
	}
	for _, evalCase := range benchmark.Cases {
		if evalCase.CommitmentNonce == "" || len(evalCase.CommitmentHash) != 64 {
			t.Fatalf("case has incomplete commitment: %+v", evalCase)
		}
		if evalCase.CommitmentHash != commitmentHash(evalCase.CommitmentNonce, evalCase.CaseChecksum) {
			t.Fatalf("case commitment mismatch: %+v", evalCase)
		}
	}

	privateDirectory := t.TempDir()
	if err := WriteArtifacts(privateDirectory, benchmark); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyArtifacts(privateDirectory); err != nil {
		t.Fatalf("private VerifyArtifacts() error = %v", err)
	}

	publicDirectory := t.TempDir()
	if err := WritePublicArtifacts(publicDirectory, benchmark); err != nil {
		t.Fatal(err)
	}
	commitments, err := os.ReadFile(filepath.Join(publicDirectory, "case_commitments.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(commitments, []byte(`"case_checksum"`)) || bytes.Contains(commitments, []byte(`"commitment_nonce"`)) {
		t.Fatal("public v4 commitments exposed a case checksum or nonce")
	}
	if _, err := VerifyArtifacts(publicDirectory); err != nil {
		t.Fatalf("public VerifyArtifacts() error = %v", err)
	}
}

func TestFreezeAuthoredRejectsConflatedNoRelevantDocumentCase(t *testing.T) {
	cases := authoredTestCases()
	cases[4].GoldSources = []GoldSource{{SourceType: "note_body", NoteID: 1}}
	_, err := FreezeAuthored(Config{
		BenchmarkID:      "retrieval_authored_invalid",
		BenchmarkVersion: "retrieval_authored_invalid",
		SourceRunID:      "quality_test_run",
		GeneratorVersion: AuthoredGeneratorVersion,
		CaseCount:        6,
		DevelopmentCases: 2,
		DatasetVersionID: 9,
		CommitmentScheme: NonceCommitmentScheme,
	}, cases)
	if err == nil || !strings.Contains(err.Error(), "no_relevant_document") {
		t.Fatalf("FreezeAuthored() error = %v, want no_relevant_document validation", err)
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

func authoredTestCases() []Case {
	return []Case{
		{Split: "development", TaskType: "semantic_paraphrase", Query: "改写问题一", ExpectedAnswer: "答案一", GoldSources: []GoldSource{{SourceType: "note_body", NoteID: 1}}},
		{Split: "development", TaskType: "typo_robustness", Query: "错别字问题二", ExpectedAnswer: "答案二", GoldSources: []GoldSource{{SourceType: "note_body", NoteID: 2}}},
		{Split: "holdout", TaskType: "temporal_conflict", Query: "时间问题三", ExpectedAnswer: "答案三", GoldSources: []GoldSource{{SourceType: "note_body", NoteID: 3}}},
		{Split: "holdout", TaskType: "cross_note_compare", Query: "比较问题四", ExpectedAnswer: "答案四", GoldSources: []GoldSource{{SourceType: "note_body", NoteID: 4}, {SourceType: "note_body", NoteID: 5}}},
		{Split: "holdout", TaskType: "no_relevant_document", Query: "无相关文档问题五", ExpectedAnswer: "当前数据集没有相关材料。", Metadata: map[string]any{"answerable": false}},
		{Split: "holdout", TaskType: "insufficient_evidence", Query: "证据不足问题六", ExpectedAnswer: "现有材料不足以支持该结论。", GoldSources: []GoldSource{{SourceType: "note_body", NoteID: 6}}, Metadata: map[string]any{"answerable": false}},
	}
}
