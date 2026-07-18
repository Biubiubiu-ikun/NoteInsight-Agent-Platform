package retrieval

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"
)

type vectorRetriever struct {
	repository *Repository
	embedder   Embedder
	store      VectorStore
}

func (r vectorRetriever) Retrieve(ctx context.Context, scope Scope, principal Principal, plan QueryPlan, filters Filters, limit int) ([]Candidate, error) {
	if scope.VectorIndexVersion != VectorIndexVersion || scope.VectorCollection == "" {
		return nil, ErrIndexNotReady
	}
	vector, err := r.embedder.EmbedQuery(ctx, plan.Original)
	if err != nil {
		return nil, fmt.Errorf("%w: embed retrieval query: %v", ErrDependencyUnavailable, err)
	}
	hits, err := r.store.Query(ctx, scope.VectorCollection, vector, vectorFilter(scope, principal, plan, filters), limit)
	if err != nil {
		return nil, fmt.Errorf("%w: query vector store: %v", ErrDependencyUnavailable, err)
	}
	chunkIDs := make([]int64, 0, len(hits))
	scores := make(map[int64]float64, len(hits))
	for _, hit := range hits {
		chunkIDs = append(chunkIDs, hit.ID)
		scores[hit.ID] = hit.Score
	}
	candidates, err := r.repository.LoadAuthorizedCandidates(ctx, scope, principal, chunkIDs, filters)
	if err != nil {
		return nil, err
	}
	for index := range candidates {
		candidates[index].VectorScore = scores[candidates[index].ChunkID]
	}
	return candidates, nil
}

type hybridRetriever struct {
	lexical CandidateRetriever
	vector  CandidateRetriever
}

func (r hybridRetriever) Retrieve(ctx context.Context, scope Scope, principal Principal, plan QueryPlan, filters Filters, limit int) ([]Candidate, error) {
	var lexicalCandidates, vectorCandidates []Candidate
	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		var err error
		lexicalCandidates, err = r.lexical.Retrieve(groupContext, scope, principal, plan, filters, limit)
		return err
	})
	group.Go(func() error {
		var err error
		vectorCandidates, err = r.vector.Retrieve(groupContext, scope, principal, plan, filters, limit)
		return err
	})
	if err := group.Wait(); err != nil {
		return nil, err
	}
	merged := make([]Candidate, 0, len(lexicalCandidates)+len(vectorCandidates))
	positions := make(map[int64]int, cap(merged))
	appendRanked := func(candidates []Candidate) {
		for rank, candidate := range candidates {
			rrf := 1.0 / float64(61+rank)
			if position, found := positions[candidate.ChunkID]; found {
				merged[position].HybridScore += rrf
				if candidate.VectorScore > 0 {
					merged[position].VectorScore = candidate.VectorScore
				}
				if candidate.FTSScore > 0 {
					merged[position].FTSScore = candidate.FTSScore
					merged[position].TrigramScore = candidate.TrigramScore
				}
				continue
			}
			candidate.HybridScore = rrf
			positions[candidate.ChunkID] = len(merged)
			merged = append(merged, candidate)
		}
	}
	appendRanked(lexicalCandidates)
	appendRanked(vectorCandidates)
	return merged, nil
}

func vectorFilter(scope Scope, principal Principal, plan QueryPlan, filters Filters) map[string]any {
	must := []any{
		fieldMatch("project_id", scope.ProjectID),
		fieldMatchAny("document_lifecycle", []any{"ready", "superseded"}),
	}
	if scope.AccessScope == "public" {
		must = append(must, fieldMatch("project_visibility", "public"), fieldMatch("document_visibility", "public"))
	}
	if len(filters.DocumentTypes) > 0 {
		must = append(must, fieldMatchAny("document_type", stringsToAny(filters.DocumentTypes)))
	}
	if len(filters.SourceTypes) > 0 {
		must = append(must, fieldMatchAny("source_type", stringsToAny(filters.SourceTypes)))
	}
	if len(plan.HintedNoteIDs) > 0 {
		values := make([]any, 0, len(plan.HintedNoteIDs))
		for _, noteID := range plan.HintedNoteIDs {
			values = append(values, noteID)
		}
		must = append(must, fieldMatchAny("note_id", values))
	}
	if plan.PreferredPosition != nil {
		must = append(must, fieldMatch("media_position", *plan.PreferredPosition))
	}
	return map[string]any{"must": must}
}

func fieldMatch(field string, value any) map[string]any {
	return map[string]any{"key": field, "match": map[string]any{"value": value}}
}

func fieldMatchAny(field string, values []any) map[string]any {
	return map[string]any{"key": field, "match": map[string]any{"any": values}}
}

func stringsToAny(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

var _ CandidateRetriever = vectorRetriever{}
var _ CandidateRetriever = hybridRetriever{}

func validateVectorDependencies(embedder Embedder, store VectorStore) error {
	if embedder == nil || store == nil {
		return fmt.Errorf("%w: vector dependencies are not configured", ErrUnsupportedMode)
	}
	return nil
}
