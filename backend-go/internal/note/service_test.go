package note

import (
	"context"
	"testing"
)

func TestServiceCreateNoteValidation(t *testing.T) {
	service := NewService(nil)

	_, err := service.CreateNote(context.Background(), CreateNoteInput{
		AuthorID: 10001,
		Title:    "",
		Body:     "body",
		Category: "beauty",
	})
	if err == nil {
		t.Fatal("CreateNote() expected validation error")
	}
}

func TestServiceParseIDValidation(t *testing.T) {
	service := NewService(nil)

	if _, err := service.GetNote(context.Background(), "not-an-id"); err == nil {
		t.Fatal("GetNote() expected validation error")
	}
}

func TestServiceListNotesDefaultsLimit(t *testing.T) {
	cursor, err := decodeNoteCursor("")
	if err != nil {
		t.Fatalf("decodeNoteCursor() error = %v", err)
	}
	if cursor.ID != 0 {
		t.Fatalf("empty cursor id = %d, want 0", cursor.ID)
	}
}
