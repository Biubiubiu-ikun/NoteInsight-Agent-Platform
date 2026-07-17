package evidence

import (
	"slices"
	"testing"
)

func TestTokenizeProducesChineseUnigramsBigramsAndLatinTerms(t *testing.T) {
	tokens := Tokenize("护肤步骤 Alpha123，敏感肌")
	for _, want := range []string{"护", "护肤", "步骤", "alpha123", "敏感", "感肌"} {
		if !slices.Contains(tokens, want) {
			t.Fatalf("tokens %v do not contain %q", tokens, want)
		}
	}
}
