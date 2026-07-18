package retrievaleval

import (
	"testing"

	"creatorinsight/backend-go/internal/retrieval"
)

func TestLeaksUnauthorizedVectorMetadata(t *testing.T) {
	clean := retrieval.SearchResponse{Scope: retrieval.Scope{ProjectID: 1, DatasetVersionID: 2}}
	if leaksUnauthorizedMetadata(clean) {
		t.Fatal("redacted scope was classified as a metadata leak")
	}
	for name, response := range map[string]retrieval.SearchResponse{
		"dataset manifest": {Scope: retrieval.Scope{DatasetManifestChecksum: "secret"}},
		"vector checksum":  {Scope: retrieval.Scope{VectorIndexChecksum: "secret"}},
		"collection":       {Scope: retrieval.Scope{VectorCollection: "secret"}},
		"embedding model":  {Scope: retrieval.Scope{EmbeddingModel: "secret"}},
	} {
		t.Run(name, func(t *testing.T) {
			if !leaksUnauthorizedMetadata(response) {
				t.Fatal("vector metadata leak was not detected")
			}
		})
	}
}
