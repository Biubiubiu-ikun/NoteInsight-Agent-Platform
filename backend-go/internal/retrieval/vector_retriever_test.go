package retrieval

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVectorFilterKeepsPublicPrincipalInsidePublicEvidence(t *testing.T) {
	filter := vectorFilter(Scope{ProjectID: 7, ProjectVisibility: "public", AccessScope: "public"}, Principal{UserID: 42}, QueryPlan{}, Filters{})
	encoded, err := json.Marshal(filter)
	if err != nil {
		t.Fatal(err)
	}
	value := string(encoded)
	for _, expected := range []string{`"project_id"`, `"project_visibility"`, `"document_visibility"`, `"public"`} {
		if !strings.Contains(value, expected) {
			t.Fatalf("public filter %s missing %s", value, expected)
		}
	}
}

func TestVectorFilterAllowsProjectEvidenceOnlyForMemberScope(t *testing.T) {
	filter := vectorFilter(Scope{ProjectID: 7, ProjectVisibility: "public", AccessScope: "member"}, Principal{UserID: 42}, QueryPlan{}, Filters{})
	encoded, err := json.Marshal(filter)
	if err != nil {
		t.Fatal(err)
	}
	value := string(encoded)
	if strings.Contains(value, "document_visibility") || strings.Contains(value, "project_visibility") {
		t.Fatalf("member filter unexpectedly constrained document visibility: %s", value)
	}
}

func TestRankVectorCandidatesUsesCosineScore(t *testing.T) {
	ranked := rankVectorCandidates([]Candidate{
		{ChunkID: 2, DocumentID: 2, VectorScore: 0.61},
		{ChunkID: 1, DocumentID: 1, VectorScore: 0.83},
	})
	if len(ranked) != 2 || ranked[0].ChunkID != 1 || ranked[0].Confidence != 0.83 {
		t.Fatalf("vector ranking = %+v", ranked)
	}
}
