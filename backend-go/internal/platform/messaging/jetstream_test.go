package messaging

import "testing"

func TestSubjectSuffix(t *testing.T) {
	tests := map[string]string{
		"note.created":        "note.created",
		" Comment Liked ":     "comment_liked",
		"note/unsafe.created": "note_unsafe.created",
		"...":                 "unknown",
	}
	for input, want := range tests {
		if got := subjectSuffix(input); got != want {
			t.Fatalf("subjectSuffix(%q) = %q, want %q", input, got, want)
		}
	}
}
