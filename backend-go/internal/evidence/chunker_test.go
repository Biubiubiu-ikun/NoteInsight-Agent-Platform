package evidence

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitCanonicalTextUsesValidUTF8HalfOpenRanges(t *testing.T) {
	text := strings.Repeat("中文检索证据。alpha-123\n", 80)
	first, err := splitCanonicalText(text, 180, 32)
	if err != nil {
		t.Fatal(err)
	}
	second, err := splitCanonicalText(text, 180, 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 2 || len(first) != len(second) {
		t.Fatalf("chunk counts = %d/%d", len(first), len(second))
	}
	data := []byte(text)
	for index, current := range first {
		if current != second[index] {
			t.Fatalf("chunk %d is not deterministic", index)
		}
		if current.EndByte-current.StartByte > 180 {
			t.Fatalf("chunk %d exceeds byte limit: %+v", index, current)
		}
		if !utf8.Valid(data[current.StartByte:current.EndByte]) || current.Content != string(data[current.StartByte:current.EndByte]) {
			t.Fatalf("chunk %d does not match valid source bytes", index)
		}
		if current.StartRune != utf8.RuneCount(data[:current.StartByte]) || current.EndRune != utf8.RuneCount(data[:current.EndByte]) {
			t.Fatalf("chunk %d rune offsets are incorrect", index)
		}
	}
	if first[0].StartByte != 0 || first[len(first)-1].EndByte != len(data) {
		t.Fatalf("chunks do not cover source endpoints: first=%+v last=%+v", first[0], first[len(first)-1])
	}
}

func TestSplitCanonicalTextRejectsInvalidBounds(t *testing.T) {
	if _, err := splitCanonicalText("text", 100, 100); err == nil {
		t.Fatal("expected overlap validation error")
	}
}
