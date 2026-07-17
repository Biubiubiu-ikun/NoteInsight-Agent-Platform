package evidence

import "testing"

func TestNormalizeLineEndingsOnlyChangesLineEndings(t *testing.T) {
	input := "  title\r\nbody\rnext\nend  "
	want := "  title\nbody\nnext\nend  "
	if got := NormalizeLineEndings(input); got != want {
		t.Fatalf("NormalizeLineEndings() = %q, want %q", got, want)
	}
}

func TestContractHashIsStableAndParserSensitive(t *testing.T) {
	first := ContractHash("parser-v1", "same text")
	if first != ContractHash("parser-v1", "same text") {
		t.Fatal("contract hash is not deterministic")
	}
	if first == ContractHash("parser-v2", "same text") {
		t.Fatal("parser version did not affect contract hash")
	}
}
