package reconcile

import "testing"

func TestBuildNoteSetsKeepsGlobalAndPerCategoryLimits(t *testing.T) {
	sets := buildNoteSets([]NoteRankingEntry{
		{ID: 1, Category: "beauty", Score: 100},
		{ID: 2, Category: "food", Score: 90},
		{ID: 3, Category: "beauty", Score: 80},
		{ID: 4, Category: "beauty", Score: 70},
	}, 2)

	if got := len(sets[globalNoteRankingKey]); got != 2 {
		t.Fatalf("global ranking size = %d, want 2", got)
	}
	if got := len(sets["ranking:notes:beauty:daily"]); got != 2 {
		t.Fatalf("beauty ranking size = %d, want 2", got)
	}
	if got := len(sets["ranking:notes:food:daily"]); got != 1 {
		t.Fatalf("food ranking size = %d, want 1", got)
	}
}

func TestBuildCommentSetsKeepsPerNoteLimit(t *testing.T) {
	sets := buildCommentSets([]CommentRankingEntry{
		{ID: 10, NoteID: 1, Score: 20},
		{ID: 11, NoteID: 1, Score: 10},
		{ID: 12, NoteID: 1, Score: 5},
		{ID: 20, NoteID: 2, Score: 15},
	}, 2)

	if got := len(sets["note:1:hot_comments"]); got != 2 {
		t.Fatalf("note 1 ranking size = %d, want 2", got)
	}
	if got := len(sets["note:2:hot_comments"]); got != 1 {
		t.Fatalf("note 2 ranking size = %d, want 1", got)
	}
}
