package evalreview

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"

	"creatorinsight/backend-go/internal/evalbench"
)

func WriteAuditArtifacts(root string, result AuditResult, inputChecksums map[string]string) (ReviewSummary, error) {
	ledgerPath := filepath.Join(root, "review_ledger.jsonl")
	queuePath := filepath.Join(root, "adjudication_queue.jsonl")
	if err := WriteJSONLines(ledgerPath, result.Ledger); err != nil {
		return ReviewSummary{}, err
	}
	if err := WriteJSONLines(queuePath, result.AdjudicationQueue); err != nil {
		return ReviewSummary{}, err
	}
	checksums := maps.Clone(inputChecksums)
	if checksums == nil {
		checksums = map[string]string{}
	}
	for name, path := range map[string]string{"review_ledger.jsonl": ledgerPath, "adjudication_queue.jsonl": queuePath} {
		checksum, err := FileChecksum(path)
		if err != nil {
			return ReviewSummary{}, err
		}
		checksums[name] = checksum
	}
	summary := result.Summary
	summary.ArtifactChecksums = checksums
	summary.SummaryChecksum = ""
	checksum, err := valueChecksum(summary)
	if err != nil {
		return ReviewSummary{}, fmt.Errorf("checksum review summary: %w", err)
	}
	summary.SummaryChecksum = checksum
	if err := WriteJSON(filepath.Join(root, "review_summary.private.json"), summary); err != nil {
		return ReviewSummary{}, err
	}
	return summary, nil
}

func FreezeApprovedCases(root string, publicRoot string, authored []AuthoredCase, sources []CandidateSource, result AuditResult, inputChecksums map[string]string) (ReviewSummary, []evalbench.Case, error) {
	if result.Summary.Status != "ready_to_freeze" {
		return ReviewSummary{}, nil, fmt.Errorf("review status is %q; every review, adjudication, agreement, and semantic gate must pass", result.Summary.Status)
	}
	approvedPath := filepath.Join(root, "approved_cases.jsonl")
	publicSummaryPath := filepath.Join(publicRoot, "review_summary.json")
	for _, path := range []string{approvedPath, publicSummaryPath} {
		if _, err := os.Stat(path); err == nil {
			return ReviewSummary{}, nil, fmt.Errorf("frozen review artifact already exists and is immutable: %s", path)
		} else if !os.IsNotExist(err) {
			return ReviewSummary{}, nil, fmt.Errorf("inspect frozen review artifact %s: %w", path, err)
		}
	}
	sourceMap := make(map[string]CandidateSource, len(sources))
	for _, source := range sources {
		sourceMap[refKey(source.Ref())] = source
	}
	adjudications := make(map[string]Adjudication, len(result.Ledger))
	for _, record := range result.Ledger {
		adjudications[record.CaseID] = record.Adjudication
	}
	approved := make([]evalbench.Case, 0, len(authored))
	for _, current := range authored {
		adjudication, ok := adjudications[current.CaseID]
		if !ok {
			return ReviewSummary{}, nil, fmt.Errorf("case %s has no final adjudication", current.CaseID)
		}
		if err := validateFinalSemantics(current, adjudication); err != nil {
			return ReviewSummary{}, nil, err
		}
		metadata := maps.Clone(current.Metadata)
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["answerable"] = adjudication.Answerability == "answerable"
		metadata["answerability"] = adjudication.Answerability
		metadata["case_id"] = current.CaseID
		metadata["rubric_version"] = RubricVersion
		metadata["review_decision_checksum"] = result.Summary.DecisionChecksum

		gold := make([]evalbench.GoldSource, 0, len(adjudication.Judgments))
		seenGold := map[string]struct{}{}
		for _, judgment := range adjudication.Judgments {
			minimumGrade := 2
			if current.TaskType == "insufficient_evidence" {
				minimumGrade = 1
			}
			if judgment.RelevanceGrade < minimumGrade || current.TaskType == "no_relevant_document" || current.TaskType == "out_of_domain_noise" {
				continue
			}
			source, ok := sourceMap[refKey(judgment.Ref())]
			if !ok {
				return ReviewSummary{}, nil, fmt.Errorf("case %s adjudication references unresolved source %s", current.CaseID, refKey(judgment.Ref()))
			}
			item := evalbench.GoldSource{SourceType: source.SourceType, NoteID: source.NoteID, Position: source.Position}
			key := fmt.Sprintf("%s:%d:%d", item.SourceType, item.NoteID, item.Position)
			if _, duplicate := seenGold[key]; duplicate {
				continue
			}
			seenGold[key] = struct{}{}
			gold = append(gold, item)
		}
		sort.Slice(gold, func(i, j int) bool {
			left := fmt.Sprintf("%s:%020d:%06d", gold[i].SourceType, gold[i].NoteID, gold[i].Position)
			right := fmt.Sprintf("%s:%020d:%06d", gold[j].SourceType, gold[j].NoteID, gold[j].Position)
			return left < right
		})
		approved = append(approved, evalbench.Case{
			Split: current.Split, TaskType: current.TaskType, Query: current.Query,
			ExpectedAnswer: current.ExpectedAnswer, GoldSources: gold,
			AdversarialTags: append([]string(nil), current.AdversarialTags...),
			Provenance:      "independent_human_review_v5", ReviewStatus: "human_approved",
			CommitmentNonce: current.CommitmentNonce, Metadata: metadata,
		})
	}
	if err := WriteJSONLines(approvedPath, approved); err != nil {
		return ReviewSummary{}, nil, err
	}
	checksums := maps.Clone(inputChecksums)
	if checksums == nil {
		checksums = map[string]string{}
	}
	approvedChecksum, err := FileChecksum(approvedPath)
	if err != nil {
		return ReviewSummary{}, nil, err
	}
	checksums["approved_cases.jsonl"] = approvedChecksum
	summary, err := WriteAuditArtifacts(root, result, checksums)
	if err != nil {
		return ReviewSummary{}, nil, err
	}
	summary.Status = "review_frozen"
	summary.SummaryChecksum = ""
	summaryChecksum, err := valueChecksum(summary)
	if err != nil {
		return ReviewSummary{}, nil, err
	}
	summary.SummaryChecksum = summaryChecksum
	if err := WriteJSON(filepath.Join(root, "review_summary.private.json"), summary); err != nil {
		return ReviewSummary{}, nil, err
	}
	if err := WriteJSON(publicSummaryPath, summary); err != nil {
		return ReviewSummary{}, nil, err
	}
	return summary, approved, nil
}
