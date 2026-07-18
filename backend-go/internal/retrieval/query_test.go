package retrieval

import (
	"slices"
	"testing"
)

func TestBuildQueryPlanExtractsEvidenceIntent(t *testing.T) {
	plan, err := BuildQueryPlan("笔记 5440 的图片文字（位置 2）写了什么？只引用 OCR。")
	if err != nil {
		t.Fatal(err)
	}
	if plan.PreferredType != "note_media" {
		t.Fatalf("preferred type = %q, want note_media", plan.PreferredType)
	}
	if plan.PreferredPosition == nil || *plan.PreferredPosition != 2 {
		t.Fatalf("preferred position = %v, want 2", plan.PreferredPosition)
	}
	if !slices.Equal(plan.HintedNoteIDs, []int64{5440}) {
		t.Fatalf("hinted note ids = %v", plan.HintedNoteIDs)
	}
	if slices.Contains(plan.Terms, "笔记") || slices.Contains(plan.Terms, "什么") {
		t.Fatalf("generic bigrams leaked into query terms: %v", plan.Terms)
	}
	if !slices.Contains(plan.Terms, "5440") {
		t.Fatalf("numeric evidence hint missing from terms: %v", plan.Terms)
	}
}

func TestBuildQueryPlanExtractsAllExplicitNoteIDs(t *testing.T) {
	plan, err := BuildQueryPlan("比较笔记 5254 和 5257 的边界")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.HintedNoteIDs, []int64{5254, 5257}) {
		t.Fatalf("hinted note ids = %v", plan.HintedNoteIDs)
	}
	if plan.PreferredType != "note" {
		t.Fatalf("preferred type = %q, want note", plan.PreferredType)
	}
}

func TestBuildQueryPlanExtractsSubjectTerms(t *testing.T) {
	plan, err := BuildQueryPlan("根据“办公室肩颈活动计划”这份记录，能否证明长期效果？")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(plan.SubjectTerms, "肩颈") || !slices.Contains(plan.SubjectTerms, "活动") {
		t.Fatalf("subject terms = %v", plan.SubjectTerms)
	}
}

func TestBuildQueryPlanRejectsBlankAndOversizedQueries(t *testing.T) {
	if _, err := BuildQueryPlan("  "); err == nil {
		t.Fatal("blank query was accepted")
	}
	oversized := make([]rune, MaxQueryRunes+1)
	for index := range oversized {
		oversized[index] = '检'
	}
	if _, err := BuildQueryPlan(string(oversized)); err == nil {
		t.Fatal("oversized query was accepted")
	}
}

func TestRankCandidatesBoostsExactMediaEvidence(t *testing.T) {
	position := 2
	noteID := int64(5440)
	plan := QueryPlan{
		Terms:             []string{"图片", "文字", "5440"},
		HintedNoteIDs:     []int64{5440},
		PreferredType:     "note_media",
		PreferredPosition: &position,
	}
	stats := map[string]TermStat{
		"图片":   {Lexeme: "图片", InverseDocumentFrequency: 2},
		"文字":   {Lexeme: "文字", InverseDocumentFrequency: 2},
		"5440": {Lexeme: "5440", InverseDocumentFrequency: 6},
	}
	candidates := []Candidate{
		{DocumentID: 1, DocumentType: "note", NoteID: &noteID, ChunkID: 1, Lexemes: "图片 文字 5440", FTSScore: 0.4},
		{DocumentID: 2, DocumentType: "note_media", NoteID: &noteID, MediaPosition: &position, ChunkID: 2, Lexemes: "图片 文字 5440", FTSScore: 0.3},
	}
	ranked := rankCandidates(candidates, plan, stats)
	if ranked[0].DocumentType != "note_media" {
		t.Fatalf("top document type = %q, want note_media", ranked[0].DocumentType)
	}
}

func TestSelectDiverseLimitsDocumentsAndNotes(t *testing.T) {
	noteID := int64(7)
	ranked := []Result{
		{DocumentID: 1, NoteID: &noteID, ChunkID: 1, Confidence: 0.9},
		{DocumentID: 1, NoteID: &noteID, ChunkID: 2, Confidence: 0.8},
		{DocumentID: 2, NoteID: &noteID, ChunkID: 3, Confidence: 0.7},
		{DocumentID: 3, NoteID: &noteID, ChunkID: 4, Confidence: 0.6},
	}
	selected := selectDiverse(ranked, 10, MinimumConfidence)
	if len(selected) != MaxChunksPerNote {
		t.Fatalf("selected %d results, want %d", len(selected), MaxChunksPerNote)
	}
	if selected[0].ChunkID != 1 || selected[1].ChunkID != 3 {
		t.Fatalf("selected chunks = %d,%d", selected[0].ChunkID, selected[1].ChunkID)
	}
}
