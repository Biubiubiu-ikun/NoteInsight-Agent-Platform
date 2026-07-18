package retrievaleval

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"

	"creatorinsight/backend-go/internal/evalbench"
	"creatorinsight/backend-go/internal/retrieval"
)

func evaluateCase(evalCase evalbench.Case, response retrieval.SearchResponse, unauthorizedResultCount int, unauthorizedMetadataLeak bool, topK int) CaseResult {
	answerable, _ := evalCase.Metadata["answerable"].(bool)
	result := CaseResult{
		CaseChecksum:             evalCase.CaseChecksum,
		TaskType:                 evalCase.TaskType,
		Answerable:               answerable,
		GoldSources:              append([]evalbench.GoldSource(nil), evalCase.GoldSources...),
		RetrievedSources:         []RetrievedSource{},
		LatencyMilliseconds:      response.TookMilliseconds,
		ResultCount:              len(response.Results),
		CandidateCount:           response.CandidateCount,
		DecisionStatus:           response.Decision.Status,
		EmbeddingCalls:           response.EmbeddingCalls,
		UnauthorizedResultCount:  unauthorizedResultCount,
		UnauthorizedMetadataLeak: unauthorizedMetadataLeak,
	}
	result.Metrics.TopConfidence = response.Decision.TopConfidence
	goldFound := make(map[string]struct{}, len(evalCase.GoldSources))
	relevance := make([]float64, 0, min(topK, len(response.Results)))
	relevantCitations := 0
	totalCitations := 0
	validCitations := 0
	for index, item := range response.Results {
		if index >= topK {
			break
		}
		itemRelevant := false
		for _, citation := range item.Citations {
			totalCitations++
			if citationIsValid(citation) {
				validCitations++
			}
			retrieved := RetrievedSource{
				Rank:          index + 1,
				Confidence:    item.Confidence,
				SourceType:    citation.SourceType,
				SourceID:      citation.SourceID,
				SourceVersion: citation.SourceVersion,
				CitationKey:   citation.CitationKey,
			}
			if citation.NoteID != nil {
				retrieved.NoteID = *citation.NoteID
			}
			if citation.MediaPosition != nil {
				retrieved.Position = *citation.MediaPosition
			}
			result.RetrievedSources = append(result.RetrievedSources, retrieved)
			for _, gold := range evalCase.GoldSources {
				if sourceMatches(gold, retrieved) {
					itemRelevant = true
					relevantCitations++
					goldFound[goldKey(gold)] = struct{}{}
					break
				}
			}
		}
		if itemRelevant {
			relevance = append(relevance, 1)
			if result.Metrics.ReciprocalRank == 0 {
				result.Metrics.ReciprocalRank = 1 / float64(index+1)
			}
		} else {
			relevance = append(relevance, 0)
		}
	}
	if len(evalCase.GoldSources) > 0 {
		result.Metrics.RecallAtK = float64(len(goldFound)) / float64(len(uniqueGold(evalCase.GoldSources)))
		result.Metrics.NDCGAtK = ndcg(relevance, min(len(uniqueGold(evalCase.GoldSources)), topK))
	}
	if totalCitations > 0 {
		result.Metrics.CitationPrecision = float64(validCitations) / float64(totalCitations)
		result.Metrics.SourcePrecisionAtK = float64(relevantCitations) / float64(totalCitations)
	} else if len(evalCase.GoldSources) == 0 {
		result.Metrics.CitationPrecision = 1
		result.Metrics.SourcePrecisionAtK = 1
	}
	result.Metrics.Rejected = len(response.Results) == 0
	result.Metrics.AuthorizationLeak = unauthorizedResultCount > 0 || unauthorizedMetadataLeak
	result.FailureCategory = classifyFailure(result)
	return result
}

func aggregate(cases []CaseResult) Metrics {
	metrics := Metrics{CaseCount: len(cases)}
	latencies := make([]float64, 0, len(cases))
	relevantCitations := 0
	validCitations := 0
	totalCitations := 0
	noRelevantRejected := 0
	authorizationSafe := 0
	for _, current := range cases {
		latencies = append(latencies, current.LatencyMilliseconds)
		metrics.EmbeddingCalls += current.EmbeddingCalls
		metrics.ExternalModelCalls += current.EmbeddingCalls
		if len(current.GoldSources) > 0 {
			metrics.GoldCaseCount++
			metrics.RecallAtK += current.Metrics.RecallAtK
			metrics.MRRAtK += current.Metrics.ReciprocalRank
			metrics.NDCGAtK += current.Metrics.NDCGAtK
		}
		for _, retrieved := range current.RetrievedSources {
			totalCitations++
			for _, gold := range current.GoldSources {
				if sourceMatches(gold, retrieved) {
					relevantCitations++
					break
				}
			}
		}
		if current.ResultCount > 0 {
			validCitations += int(math.Round(current.Metrics.CitationPrecision * float64(len(current.RetrievedSources))))
		}
		if isNoRelevantTask(current.TaskType) {
			metrics.NoRelevantCaseCount++
			if current.Metrics.Rejected {
				noRelevantRejected++
			}
		}
		if current.TaskType == "insufficient_evidence" {
			metrics.InsufficientEvidenceCaseCount++
			metrics.InsufficientEvidenceSourceRecall += current.Metrics.RecallAtK
		}
		if current.TaskType == "authorization_boundary" {
			metrics.AuthorizationCaseCount++
			if !current.Metrics.AuthorizationLeak {
				authorizationSafe++
			}
		}
	}
	if metrics.GoldCaseCount > 0 {
		metrics.RecallAtK /= float64(metrics.GoldCaseCount)
		metrics.MRRAtK /= float64(metrics.GoldCaseCount)
		metrics.NDCGAtK /= float64(metrics.GoldCaseCount)
	}
	if totalCitations > 0 {
		metrics.CitationPrecision = float64(validCitations) / float64(totalCitations)
		metrics.SourcePrecisionAtK = float64(relevantCitations) / float64(totalCitations)
	} else {
		metrics.CitationPrecision = 1
		metrics.SourcePrecisionAtK = 1
	}
	if metrics.NoRelevantCaseCount > 0 {
		metrics.NoRelevantRejectionAccuracy = float64(noRelevantRejected) / float64(metrics.NoRelevantCaseCount)
	}
	if metrics.InsufficientEvidenceCaseCount > 0 {
		metrics.InsufficientEvidenceSourceRecall /= float64(metrics.InsufficientEvidenceCaseCount)
	}
	if metrics.AuthorizationCaseCount > 0 {
		metrics.AuthorizationNonLeakageAccuracy = float64(authorizationSafe) / float64(metrics.AuthorizationCaseCount)
	}
	sort.Float64s(latencies)
	metrics.LatencyP50Milliseconds = percentile(latencies, 0.50)
	metrics.LatencyP95Milliseconds = percentile(latencies, 0.95)
	metrics.LatencyP99Milliseconds = percentile(latencies, 0.99)
	roundMetrics(&metrics)
	return metrics
}

func developmentGate(metrics Metrics) GateResult {
	checks := map[string]bool{
		"recall_at_10_gte_0_85":                   metrics.RecallAtK >= 0.85,
		"mrr_at_10_gte_0_70":                      metrics.MRRAtK >= 0.70,
		"no_relevant_rejection_accuracy_gte_0_85": metrics.NoRelevantRejectionAccuracy >= 0.85,
		"citation_precision_gte_0_90":             metrics.CitationPrecision >= 0.90,
	}
	passed := true
	for _, value := range checks {
		passed = passed && value
	}
	return GateResult{
		Passed: passed,
		Checks: checks,
		Values: map[string]float64{
			"recall_at_10":                   metrics.RecallAtK,
			"mrr_at_10":                      metrics.MRRAtK,
			"no_relevant_rejection_accuracy": metrics.NoRelevantRejectionAccuracy,
			"citation_precision":             metrics.CitationPrecision,
		},
	}
}

func classifyFailure(result CaseResult) string {
	switch {
	case result.TaskType == "authorization_boundary" && result.Metrics.AuthorizationLeak:
		return "authorization_leak"
	case isNoRelevantTask(result.TaskType) && !result.Metrics.Rejected:
		return "false_positive_no_relevant"
	case len(result.GoldSources) > 0 && result.Metrics.RecallAtK == 0:
		return "missed_all_gold"
	case len(result.GoldSources) > 1 && result.Metrics.RecallAtK < 1:
		return "partial_gold_recall"
	case len(result.GoldSources) > 0 && result.Metrics.ReciprocalRank < 1:
		return "ranking_error"
	case len(result.RetrievedSources) > 0 && result.Metrics.SourcePrecisionAtK < 1:
		return "citation_contamination"
	default:
		return ""
	}
}

func isNoRelevantTask(taskType string) bool {
	return taskType == "no_relevant_document" || taskType == "out_of_domain_noise"
}

func sourceMatches(gold evalbench.GoldSource, retrieved RetrievedSource) bool {
	if gold.SourceType != retrieved.SourceType || gold.NoteID != retrieved.NoteID {
		return false
	}
	return gold.SourceType != "note_media" || gold.Position == retrieved.Position
}

func goldKey(gold evalbench.GoldSource) string {
	return gold.SourceType + ":" + fmtInt(gold.NoteID) + ":" + fmtInt(int64(gold.Position))
}

func uniqueGold(sources []evalbench.GoldSource) []evalbench.GoldSource {
	seen := make(map[string]struct{}, len(sources))
	result := make([]evalbench.GoldSource, 0, len(sources))
	for _, source := range sources {
		key := goldKey(source)
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, source)
	}
	return result
}

func ndcg(relevance []float64, idealRelevant int) float64 {
	if idealRelevant == 0 {
		return 0
	}
	dcg := 0.0
	for index, value := range relevance {
		dcg += value / math.Log2(float64(index)+2)
	}
	idcg := 0.0
	for index := 0; index < idealRelevant; index++ {
		idcg += 1 / math.Log2(float64(index)+2)
	}
	return dcg / idcg
}

func percentile(sorted []float64, quantile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return math.Round(sorted[index]*100) / 100
}

func roundMetrics(metrics *Metrics) {
	metrics.RecallAtK = round4(metrics.RecallAtK)
	metrics.MRRAtK = round4(metrics.MRRAtK)
	metrics.NDCGAtK = round4(metrics.NDCGAtK)
	metrics.CitationPrecision = round4(metrics.CitationPrecision)
	metrics.SourcePrecisionAtK = round4(metrics.SourcePrecisionAtK)
	metrics.NoRelevantRejectionAccuracy = round4(metrics.NoRelevantRejectionAccuracy)
	metrics.InsufficientEvidenceSourceRecall = round4(metrics.InsufficientEvidenceSourceRecall)
	metrics.AuthorizationNonLeakageAccuracy = round4(metrics.AuthorizationNonLeakageAccuracy)
}

func citationIsValid(citation retrieval.Citation) bool {
	if citation.CitationKey == "" || citation.Quote == "" || citation.QuoteHash == "" ||
		citation.DocumentEndByte <= citation.DocumentStartByte || citation.SourceEndByte <= citation.SourceStartByte {
		return false
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(citation.Quote)))
	return digest == citation.QuoteHash && len([]byte(citation.Quote)) == citation.DocumentEndByte-citation.DocumentStartByte
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func fmtInt(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buffer := [20]byte{}
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buffer[index] = '-'
	}
	return string(buffer[index:])
}
