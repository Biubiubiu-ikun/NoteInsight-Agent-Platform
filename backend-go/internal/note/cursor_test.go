package note

import (
	"testing"
	"time"
)

func TestNoteCursorRoundTrip(t *testing.T) {
	item := Note{
		ID:        42,
		CreatedAt: time.Date(2026, 7, 6, 10, 0, 0, 123, time.UTC),
	}

	encoded, err := encodeNoteCursor(item)
	if err != nil {
		t.Fatalf("encodeNoteCursor() error = %v", err)
	}

	decoded, err := decodeNoteCursor(encoded)
	if err != nil {
		t.Fatalf("decodeNoteCursor() error = %v", err)
	}

	if decoded.ID != item.ID {
		t.Fatalf("decoded id = %d, want %d", decoded.ID, item.ID)
	}
	if !decoded.CreatedAt.Equal(item.CreatedAt) {
		t.Fatalf("decoded created_at = %s, want %s", decoded.CreatedAt, item.CreatedAt)
	}
}

func TestDecodeCursorRejectsInvalidValue(t *testing.T) {
	if _, err := decodeNoteCursor("not-base64"); err == nil {
		t.Fatal("decodeNoteCursor() expected error")
	}
}
