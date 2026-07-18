package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"creatorinsight/backend-go/internal/platform/observability"
)

type CandidateRetriever interface {
	Retrieve(context.Context, Scope, Principal, QueryPlan, Filters, int) ([]Candidate, error)
}

type lexicalRetriever struct {
	repository *Repository
}

func (r lexicalRetriever) Retrieve(ctx context.Context, scope Scope, principal Principal, plan QueryPlan, filters Filters, limit int) ([]Candidate, error) {
	return r.repository.SearchLexicalCandidates(ctx, scope, principal, plan, filters, limit)
}

type Service struct {
	repository *Repository
	retrievers map[string]CandidateRetriever
}

func NewService(repository *Repository) *Service {
	return &Service{
		repository: repository,
		retrievers: map[string]CandidateRetriever{
			ModeLexical: lexicalRetriever{repository: repository},
		},
	}
}

func (s *Service) EnableVector(embedder Embedder, store VectorStore) error {
	if err := validateVectorDependencies(embedder, store); err != nil {
		return err
	}
	lexical := s.retrievers[ModeLexical]
	vector := vectorRetriever{repository: s.repository, embedder: embedder, store: store}
	s.retrievers[ModeVector] = vector
	s.retrievers[ModeHybrid] = hybridRetriever{lexical: lexical, vector: vector}
	return nil
}

func (s *Service) BuildLexicalIndex(ctx context.Context, ingestionRunID string) (LexicalIndex, error) {
	return s.repository.BuildLexicalIndex(ctx, ingestionRunID)
}

func (s *Service) Search(ctx context.Context, principal Principal, input SearchInput) (response SearchResponse, err error) {
	startedAt := time.Now()
	observedMode := input.Mode
	if observedMode == "" {
		observedMode = ModeLexical
	}
	defer func() {
		status := retrievalMetricStatus(err, response.Decision.Status)
		observability.ObserveRetrieval(observedMode, status, startedAt, response.CandidateCount, len(response.Results))
	}()
	input.Query = strings.TrimSpace(input.Query)
	input.IngestionRunID = strings.TrimSpace(input.IngestionRunID)
	if input.ProjectID < 0 || input.DatasetVersionID < 0 {
		return SearchResponse{}, fmt.Errorf("%w: scope identifiers cannot be negative", ErrInvalidInput)
	}
	if input.ProjectID == 0 {
		input.ProjectID = 1
	}
	if input.Mode == "" {
		input.Mode = ModeLexical
	}
	retriever, found := s.retrievers[input.Mode]
	if !found {
		return SearchResponse{}, fmt.Errorf("%w: mode %q", ErrUnsupportedMode, input.Mode)
	}
	if input.Limit == 0 {
		input.Limit = DefaultLimit
	}
	if input.Limit < 1 || input.Limit > MaxLimit {
		return SearchResponse{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, MaxLimit)
	}
	if err := validateFilters(input.Filters); err != nil {
		return SearchResponse{}, err
	}
	plan, err := BuildQueryPlan(input.Query)
	if err != nil {
		return SearchResponse{}, err
	}
	scope, err := s.repository.ResolveScope(ctx, input.ProjectID, input.DatasetVersionID, input.IngestionRunID)
	if err != nil {
		return SearchResponse{}, err
	}
	accessScope, err := s.repository.ResolveAccess(ctx, scope, principal)
	if err != nil {
		return SearchResponse{}, err
	}
	if accessScope == "none" {
		return redactedNoAccessResponse(input.Mode, input, plan, startedAt), nil
	}
	scope.AccessScope = accessScope
	if err := validateScopeIndex(scope, input.Mode); err != nil {
		return SearchResponse{}, err
	}

	stats := make(map[string]TermStat, len(plan.Terms))
	if input.Mode != ModeVector {
		stats, err = s.repository.GetTermStats(ctx, scope.IngestionRunID, accessScope, plan.Terms)
		if err != nil {
			return SearchResponse{}, err
		}
	}
	indexedTerms := make([]string, 0, len(plan.Terms))
	for _, term := range plan.Terms {
		if _, exists := stats[term]; exists {
			indexedTerms = append(indexedTerms, term)
		}
	}
	candidateTerms := selectCandidateTerms(plan, indexedTerms, stats, 12)
	response = SearchResponse{
		Mode:             input.Mode,
		RetrieverVersion: retrieverVersion(input.Mode),
		RerankerVersion:  RerankerVersion,
		Scope:            scope,
		Query: QuerySummary{
			Original:       plan.Original,
			Terms:          append([]string(nil), plan.Terms...),
			IndexedTerms:   append([]string(nil), indexedTerms...),
			CandidateTerms: append([]string(nil), candidateTerms...),
			HintedNoteIDs:  append([]int64(nil), plan.HintedNoteIDs...),
			PreferredType:  plan.PreferredType,
		},
		Decision: SearchDecision{
			Status:    "no_relevant_document",
			Reason:    "no candidates passed the retrieval threshold",
			Threshold: thresholdForMode(input.Mode),
		},
		Results:            []Result{},
		ExternalModelCalls: embeddingCallsForMode(input.Mode),
		EmbeddingCalls:     embeddingCallsForMode(input.Mode),
	}
	if input.Mode == ModeLexical && len(candidateTerms) == 0 {
		response.TookMilliseconds = millisecondsSince(startedAt)
		return response, nil
	}

	retrievalPlan := plan
	retrievalPlan.AllTerms = append([]string(nil), plan.Terms...)
	retrievalPlan.Terms = candidateTerms
	effectiveFilters := input.Filters
	if len(effectiveFilters.DocumentTypes) == 0 && plan.PreferredType != "" {
		effectiveFilters.DocumentTypes = []string{plan.PreferredType}
	}
	candidates, err := retriever.Retrieve(ctx, scope, principal, retrievalPlan, effectiveFilters, DefaultCandidateLimit)
	if err != nil {
		return SearchResponse{}, err
	}
	response.CandidateCount = len(candidates)
	var ranked []Result
	switch input.Mode {
	case ModeVector:
		ranked = rankVectorCandidates(candidates)
	case ModeHybrid:
		ranked = rankHybridCandidates(candidates, plan, stats)
	default:
		ranked = rankCandidates(candidates, plan, stats)
	}
	minimumConfidence := thresholdForMode(input.Mode)
	if len(ranked) == 0 || ranked[0].Confidence < minimumConfidence {
		if len(ranked) > 0 {
			response.Decision.TopConfidence = ranked[0].Confidence
		}
		response.Decision.Reason = "top candidate is below the retrieval confidence threshold"
		response.TookMilliseconds = millisecondsSince(startedAt)
		return response, nil
	}

	selected := selectDiverse(ranked, input.Limit, minimumConfidence)
	chunkIDs := make([]int64, 0, len(selected))
	for _, result := range selected {
		chunkIDs = append(chunkIDs, result.ChunkID)
	}
	citations, err := s.repository.LoadCitations(ctx, chunkIDs)
	if err != nil {
		return SearchResponse{}, err
	}
	for index := range selected {
		selected[index].Citations = citations[selected[index].ChunkID]
		if len(selected[index].Citations) == 0 {
			return SearchResponse{}, fmt.Errorf("retrieval invariant violation: chunk %d has no source citation", selected[index].ChunkID)
		}
		selected[index].Rank = index + 1
	}
	response.Decision = SearchDecision{
		Status:        "candidates",
		Reason:        "top candidate passed the retrieval confidence threshold",
		Threshold:     minimumConfidence,
		TopConfidence: selected[0].Confidence,
	}
	response.Results = selected
	response.TookMilliseconds = millisecondsSince(startedAt)
	return response, nil
}

func retrievalMetricStatus(err error, decisionStatus string) string {
	if err == nil {
		return decisionStatus
	}
	switch {
	case errors.Is(err, ErrDependencyUnavailable):
		return "dependency_error"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timeout_error"
	case errors.Is(err, ErrIndexNotReady), errors.Is(err, ErrIndexVersionMismatch):
		return "index_error"
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrUnsupportedMode):
		return "request_error"
	default:
		return "internal_error"
	}
}

func redactedNoAccessResponse(mode string, input SearchInput, plan QueryPlan, startedAt time.Time) SearchResponse {
	return SearchResponse{
		Mode:             mode,
		RetrieverVersion: retrieverVersion(mode),
		RerankerVersion:  RerankerVersion,
		Scope: Scope{
			ProjectID:        input.ProjectID,
			DatasetVersionID: input.DatasetVersionID,
		},
		Query: QuerySummary{
			Original:       plan.Original,
			Terms:          append([]string(nil), plan.Terms...),
			IndexedTerms:   []string{},
			CandidateTerms: []string{},
		},
		Decision: SearchDecision{
			Status:    "no_relevant_document",
			Reason:    "no authorized evidence",
			Threshold: thresholdForMode(mode),
		},
		Results:            []Result{},
		TookMilliseconds:   millisecondsSince(startedAt),
		ExternalModelCalls: 0,
	}
}

func selectCandidateTerms(plan QueryPlan, indexedTerms []string, stats map[string]TermStat, limit int) []string {
	type weightedTerm struct {
		value string
		idf   float64
		order int
	}
	weighted := make([]weightedTerm, 0, len(indexedTerms))
	for index, term := range indexedTerms {
		weighted = append(weighted, weightedTerm{value: term, idf: stats[term].InverseDocumentFrequency, order: index})
	}
	sort.SliceStable(weighted, func(left int, right int) bool {
		if weighted[left].idf != weighted[right].idf {
			return weighted[left].idf > weighted[right].idf
		}
		return weighted[left].order < weighted[right].order
	})
	result := make([]string, 0, min(limit, len(weighted)))
	seen := make(map[string]struct{}, limit)
	appendWeighted := func(values []string, maximum int) {
		preferred := make([]weightedTerm, 0, len(values))
		for _, value := range values {
			if stat, found := stats[value]; found {
				preferred = append(preferred, weightedTerm{value: value, idf: stat.InverseDocumentFrequency})
			}
		}
		sort.SliceStable(preferred, func(left int, right int) bool { return preferred[left].idf > preferred[right].idf })
		for _, term := range preferred {
			if len(result) == limit || maximum == 0 {
				return
			}
			if _, found := seen[term.value]; found {
				continue
			}
			seen[term.value] = struct{}{}
			result = append(result, term.value)
			maximum--
		}
	}
	appendWeighted(plan.SubjectTerms, 4)
	numeric := make([]string, 0)
	for _, term := range indexedTerms {
		if isNumeric(term) {
			numeric = append(numeric, term)
		}
	}
	appendWeighted(numeric, 4)
	for _, term := range weighted {
		if len(result) == limit {
			break
		}
		if _, found := seen[term.value]; found {
			continue
		}
		seen[term.value] = struct{}{}
		result = append(result, term.value)
	}
	return result
}

func rankCandidates(candidates []Candidate, plan QueryPlan, stats map[string]TermStat) []Result {
	const missingTermWeight = 8.0
	totalWeight := 0.0
	scoringTerms := plan.AllTerms
	if len(scoringTerms) == 0 {
		scoringTerms = plan.Terms
	}
	for _, term := range scoringTerms {
		if stat, found := stats[term]; found {
			totalWeight += stat.InverseDocumentFrequency
		} else {
			totalWeight += missingTermWeight
		}
	}
	if totalWeight == 0 {
		totalWeight = 1
	}
	hinted := make(map[int64]struct{}, len(plan.HintedNoteIDs))
	for _, noteID := range plan.HintedNoteIDs {
		hinted[noteID] = struct{}{}
	}
	results := make([]Result, 0, len(candidates))
	for _, candidate := range candidates {
		candidateTerms := make(map[string]struct{})
		for _, term := range strings.Fields(candidate.Lexemes) {
			candidateTerms[term] = struct{}{}
		}
		matchedWeight := 0.0
		for _, term := range scoringTerms {
			if _, matches := candidateTerms[term]; !matches {
				continue
			}
			if stat, found := stats[term]; found {
				matchedWeight += stat.InverseDocumentFrequency
			}
		}
		coverage := matchedWeight / totalWeight
		ftsNormalized := candidate.FTSScore / (candidate.FTSScore + 0.25)
		score := coverage*0.78 + ftsNormalized*0.14 + clamp(candidate.TrigramScore, 0, 1)*0.08
		if candidate.NoteID != nil {
			if _, matchesHint := hinted[*candidate.NoteID]; matchesHint {
				score += 0.34
			}
		}
		if plan.PreferredType != "" && candidate.DocumentType == plan.PreferredType {
			score += 0.14
		}
		if plan.PreferredPosition != nil && candidate.MediaPosition != nil && *plan.PreferredPosition == *candidate.MediaPosition {
			score += 0.08
		}
		confidence := clamp(score, 0, 1)
		results = append(results, Result{
			Score:            score,
			Confidence:       confidence,
			FTSScore:         candidate.FTSScore,
			WeightedCoverage: coverage,
			DocumentID:       candidate.DocumentID,
			DocumentKey:      candidate.DocumentKey,
			DocumentType:     candidate.DocumentType,
			SourceType:       candidate.SourceType,
			SourceID:         candidate.SourceID,
			SourceVersion:    candidate.SourceVersion,
			NoteID:           candidate.NoteID,
			MediaPosition:    candidate.MediaPosition,
			ChunkID:          candidate.ChunkID,
			ChunkKey:         candidate.ChunkKey,
			ChunkIndex:       candidate.ChunkIndex,
			Content:          candidate.Content,
			ContentHash:      candidate.ContentHash,
			StartByte:        candidate.StartByte,
			EndByte:          candidate.EndByte,
			Citations:        []Citation{},
		})
	}
	sort.SliceStable(results, func(left int, right int) bool {
		if results[left].Score != results[right].Score {
			return results[left].Score > results[right].Score
		}
		if results[left].FTSScore != results[right].FTSScore {
			return results[left].FTSScore > results[right].FTSScore
		}
		return results[left].ChunkID < results[right].ChunkID
	})
	return results
}

func rankVectorCandidates(candidates []Candidate) []Result {
	results := make([]Result, 0, len(candidates))
	for _, candidate := range candidates {
		confidence := clamp(candidate.VectorScore, 0, 1)
		result := resultFromCandidate(candidate)
		result.Score = candidate.VectorScore
		result.Confidence = confidence
		result.VectorScore = candidate.VectorScore
		results = append(results, result)
	}
	sort.SliceStable(results, func(left int, right int) bool {
		if results[left].VectorScore != results[right].VectorScore {
			return results[left].VectorScore > results[right].VectorScore
		}
		return results[left].ChunkID < results[right].ChunkID
	})
	return results
}

func rankHybridCandidates(candidates []Candidate, plan QueryPlan, stats map[string]TermStat) []Result {
	lexicalResults := rankCandidates(candidates, plan, stats)
	byChunk := make(map[int64]Candidate, len(candidates))
	for _, candidate := range candidates {
		byChunk[candidate.ChunkID] = candidate
	}
	for index := range lexicalResults {
		candidate := byChunk[lexicalResults[index].ChunkID]
		normalizedRRF := clamp(candidate.HybridScore*30.5, 0, 1)
		lexicalConfidence := lexicalResults[index].Confidence
		confidence := normalizedRRF*0.45 + clamp(candidate.VectorScore, 0, 1)*0.35 + lexicalConfidence*0.20
		lexicalResults[index].Score = confidence
		lexicalResults[index].Confidence = clamp(confidence, 0, 1)
		lexicalResults[index].VectorScore = candidate.VectorScore
		lexicalResults[index].HybridScore = normalizedRRF
	}
	sort.SliceStable(lexicalResults, func(left int, right int) bool {
		if lexicalResults[left].Score != lexicalResults[right].Score {
			return lexicalResults[left].Score > lexicalResults[right].Score
		}
		return lexicalResults[left].ChunkID < lexicalResults[right].ChunkID
	})
	return lexicalResults
}

func resultFromCandidate(candidate Candidate) Result {
	return Result{
		FTSScore: candidate.FTSScore, VectorScore: candidate.VectorScore, HybridScore: candidate.HybridScore,
		DocumentID: candidate.DocumentID, DocumentKey: candidate.DocumentKey,
		DocumentType: candidate.DocumentType, SourceType: candidate.SourceType,
		SourceID: candidate.SourceID, SourceVersion: candidate.SourceVersion,
		NoteID: candidate.NoteID, MediaPosition: candidate.MediaPosition,
		ChunkID: candidate.ChunkID, ChunkKey: candidate.ChunkKey, ChunkIndex: candidate.ChunkIndex,
		Content: candidate.Content, ContentHash: candidate.ContentHash,
		StartByte: candidate.StartByte, EndByte: candidate.EndByte, Citations: []Citation{},
	}
}

func selectDiverse(ranked []Result, limit int, minimumConfidence float64) []Result {
	selected := make([]Result, 0, limit)
	if len(ranked) == 0 {
		return selected
	}
	resultThreshold := math.Max(minimumConfidence, ranked[0].Confidence*0.72)
	seenDocuments := make(map[int64]struct{}, limit)
	noteCounts := make(map[int64]int, limit)
	for _, result := range ranked {
		if result.Confidence < resultThreshold {
			continue
		}
		if _, found := seenDocuments[result.DocumentID]; found {
			continue
		}
		if result.NoteID != nil && noteCounts[*result.NoteID] >= MaxChunksPerNote {
			continue
		}
		seenDocuments[result.DocumentID] = struct{}{}
		if result.NoteID != nil {
			noteCounts[*result.NoteID]++
		}
		selected = append(selected, result)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func validateFilters(filters Filters) error {
	allowed := map[string]struct{}{
		"note": {}, "note_media": {}, "note_comment_cluster": {},
		"note_daily_fact": {}, "user_daily_fact": {},
	}
	for _, value := range append(append([]string{}, filters.DocumentTypes...), filters.SourceTypes...) {
		if _, found := allowed[value]; !found {
			return fmt.Errorf("%w: unsupported retrieval filter %q", ErrInvalidInput, value)
		}
	}
	return nil
}

func validateScopeIndex(scope Scope, mode string) error {
	if (mode == ModeLexical || mode == ModeHybrid) && scope.LexicalIndexVersion == "" {
		return ErrIndexNotReady
	}
	if (mode == ModeLexical || mode == ModeHybrid) && scope.LexicalIndexVersion != LexicalIndexVersion {
		return ErrIndexVersionMismatch
	}
	if (mode == ModeVector || mode == ModeHybrid) && scope.VectorIndexVersion == "" {
		return ErrIndexNotReady
	}
	if (mode == ModeVector || mode == ModeHybrid) && scope.VectorIndexVersion != VectorIndexVersion {
		return ErrIndexVersionMismatch
	}
	return nil
}

func retrieverVersion(mode string) string {
	switch mode {
	case ModeVector:
		return VectorRetrieverVersion
	case ModeHybrid:
		return HybridRetrieverVersion
	default:
		return RetrieverVersion
	}
}

func thresholdForMode(mode string) float64 {
	switch mode {
	case ModeVector:
		return MinimumVectorScore
	case ModeHybrid:
		return MinimumHybridScore
	default:
		return MinimumConfidence
	}
}

func embeddingCallsForMode(mode string) int {
	if mode == ModeVector || mode == ModeHybrid {
		return 1
	}
	return 0
}

func millisecondsSince(startedAt time.Time) float64 {
	return math.Round(float64(time.Since(startedAt).Microseconds())/10) / 100
}

func clamp(value float64, minimum float64, maximum float64) float64 {
	return math.Max(minimum, math.Min(maximum, value))
}
