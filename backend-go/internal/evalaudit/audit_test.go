package evalaudit

import (
	"testing"

	"creatorinsight/backend-go/internal/evalbench"
)

func TestAnalyzeDistinguishability(t *testing.T) {
	manifest := evalbench.Manifest{
		BenchmarkID: "benchmark", BenchmarkVersion: "v1", ManifestChecksum: "checksum",
		DatasetVersionID: 7, SourceRunID: "run",
	}
	cases := []evalbench.Case{
		{TaskType: "semantic", Query: "预算80元且需要小范围耐受测试", CaseChecksum: "a", GoldSources: []evalbench.GoldSource{{NoteID: 10, Topic: "防晒"}}},
		{TaskType: "insufficient", Query: "防晒怎么选", CaseChecksum: "b", GoldSources: []evalbench.GoldSource{{NoteID: 10, Topic: "防晒"}}},
		{TaskType: "missing", Query: "不存在", CaseChecksum: "c", GoldSources: []evalbench.GoldSource{{NoteID: 99, Topic: "其他"}}},
		{TaskType: "no_answer", Query: "没有答案", CaseChecksum: "d"},
	}
	scenarios := []Scenario{
		{NoteID: 10, Subject: "防晒", Payload: map[string]any{"budget": "预算80元", "step": "小范围耐受测试"}},
		{NoteID: 11, Subject: "防晒", Payload: map[string]any{"budget": "预算120元", "step": "先做补涂测试"}},
	}

	report, err := Analyze(manifest, cases, scenarios)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.PassCount != 1 || report.Summary.ReviewCount != 1 ||
		report.Summary.InvalidCount != 1 || report.Summary.NotApplicable != 1 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	if report.Cases[0].MaxTopicCohort != 2 || report.Cases[0].UniqueMatchedAnchors != 2 {
		t.Fatalf("unexpected distinguishability result: %+v", report.Cases[0])
	}
	if len(report.ReportChecksum) != 64 {
		t.Fatalf("unexpected report checksum: %q", report.ReportChecksum)
	}

	repeated, err := Analyze(manifest, cases, scenarios)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReportChecksum != repeated.ReportChecksum {
		t.Fatalf("report checksum changed: %s != %s", report.ReportChecksum, repeated.ReportChecksum)
	}
}
