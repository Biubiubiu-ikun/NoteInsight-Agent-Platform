package evalreview

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

func Audit(authored []AuthoredCase, sources []CandidateSource, reviewsA []ReviewSubmission, reviewsB []ReviewSubmission, adjudications []Adjudication) (AuditResult, error) {
	if err := ValidateAuthoredMatrix(authored); err != nil {
		return AuditResult{}, err
	}
	sourceMap := make(map[string]CandidateSource, len(sources))
	for _, source := range sources {
		key := refKey(source.Ref())
		if source.DatasetVersionID <= 0 || source.IngestionRunID == "" || source.CanonicalText == "" || source.ContentHash == "" {
			return AuditResult{}, fmt.Errorf("resolved source %s is incomplete", key)
		}
		if _, duplicate := sourceMap[key]; duplicate {
			return AuditResult{}, fmt.Errorf("duplicate resolved source %s", key)
		}
		sourceMap[key] = source
	}

	authoredMap := make(map[string]AuthoredCase, len(authored))
	for _, current := range authored {
		for _, ref := range current.CandidateRefs {
			if _, ok := sourceMap[refKey(ref)]; !ok {
				return AuditResult{}, fmt.Errorf("case %s candidate %s is not in the resolved frozen source set", current.CaseID, refKey(ref))
			}
		}
		authoredMap[current.CaseID] = current
	}

	mapA, reviewerA, err := validateReviewSet(authoredMap, reviewsA, "reviewer A")
	if err != nil {
		return AuditResult{}, err
	}
	mapB, reviewerB, err := validateReviewSet(authoredMap, reviewsB, "reviewer B")
	if err != nil {
		return AuditResult{}, err
	}
	if reviewerA == reviewerB {
		return AuditResult{}, fmt.Errorf("reviewer A and reviewer B must be distinct")
	}

	adjudicationMap := make(map[string]Adjudication, len(adjudications))
	for _, adjudication := range adjudications {
		if _, duplicate := adjudicationMap[adjudication.CaseID]; duplicate {
			return AuditResult{}, fmt.Errorf("duplicate adjudication for case %s", adjudication.CaseID)
		}
		current, ok := authoredMap[adjudication.CaseID]
		if !ok {
			return AuditResult{}, fmt.Errorf("adjudication references unknown case %s", adjudication.CaseID)
		}
		if err := validateAdjudication(current, adjudication, reviewerA, reviewerB); err != nil {
			return AuditResult{}, err
		}
		adjudicationMap[adjudication.CaseID] = adjudication
	}

	pairs := make([]reviewPair, 0, len(authored))
	pairsByTask := make(map[string][]reviewPair, len(TaskOrder))
	ledger := make([]ReviewLedgerRecord, 0, len(adjudications))
	queue := make([]MatrixSlot, 0, len(authored)-len(adjudications))
	disagreements := 0
	finalSemanticsValid := true
	for _, current := range authored {
		reviewA := mapA[current.CaseID]
		reviewB := mapB[current.CaseID]
		pair := makeReviewPair(reviewA, reviewB)
		pairs = append(pairs, pair)
		pairsByTask[current.TaskType] = append(pairsByTask[current.TaskType], pair)
		if pair.disagrees {
			disagreements++
		}
		adjudication, adjudicated := adjudicationMap[current.CaseID]
		if !adjudicated {
			status := "confirmation_required"
			if pair.disagrees {
				status = "resolution_required"
			}
			queue = append(queue, MatrixSlot{
				CaseID: current.CaseID, TaskType: current.TaskType, Split: current.Split,
				RubricVersion: RubricVersion, Status: status,
			})
			continue
		}
		if err := validateFinalSemantics(current, adjudication); err != nil {
			finalSemanticsValid = false
		}
		ledger = append(ledger, ReviewLedgerRecord{
			CaseID: current.CaseID, AuthorID: current.AuthorID, TaskType: current.TaskType,
			Split: current.Split, RubricVersion: RubricVersion, DraftAssistance: current.DraftAssistance,
			Reviews: []ReviewSubmission{reviewA, reviewB}, Adjudication: adjudication,
		})
	}

	agreement := calculateAgreement(pairs)
	taskAgreement := make(map[string]Agreement, len(TaskOrder))
	perTaskPassed := true
	for _, task := range TaskOrder {
		metrics := calculateAgreement(pairsByTask[task])
		taskAgreement[task] = metrics
		if metrics.AnswerabilityBinaryKappa < 0.80 || metrics.RelevanceQuadraticKappa < 0.70 {
			perTaskPassed = false
		}
	}
	splitCounts := map[string]int{"development": 0, "holdout": 0}
	taskCounts := make(map[string]int, len(TaskOrder))
	for _, current := range authored {
		splitCounts[current.Split]++
		taskCounts[current.TaskType]++
	}
	gates := map[string]bool{
		"matrix_288_balanced":                 len(authored) == len(TaskOrder)*TargetCasesPerTask,
		"two_independent_reviews_complete":    len(mapA) == len(authored) && len(mapB) == len(authored),
		"all_cases_adjudicated":               len(adjudicationMap) == len(authored),
		"answerability_binary_kappa_gte_0_80": agreement.AnswerabilityBinaryKappa >= 0.80,
		"relevance_quadratic_kappa_gte_0_70":  agreement.RelevanceQuadraticKappa >= 0.70,
		"per_task_agreement_gates_pass":       perTaskPassed,
		"adjudicated_semantics_valid":         finalSemanticsValid && len(adjudicationMap) == len(authored),
		"frozen_source_membership_resolved":   len(sourceMap) > 0,
	}
	status := "ready_to_freeze"
	for _, passed := range gates {
		if !passed {
			status = "review_in_progress"
			break
		}
	}
	summary := ReviewSummary{
		BenchmarkVersion: "retrieval_v5", RubricVersion: RubricVersion, Status: status,
		CaseCount: len(authored), SplitCounts: splitCounts, TaskCounts: taskCounts,
		ReviewedCaseCount: min(len(mapA), len(mapB)), AdjudicatedCaseCount: len(adjudicationMap),
		DisagreementCaseCount: disagreements, Agreement: agreement, TaskAgreement: taskAgreement, Gates: gates,
	}
	decisionChecksum, err := valueChecksum(ledger)
	if err != nil {
		return AuditResult{}, fmt.Errorf("checksum review decisions: %w", err)
	}
	summary.DecisionChecksum = decisionChecksum
	checksum, err := valueChecksum(summary)
	if err != nil {
		return AuditResult{}, fmt.Errorf("checksum review summary: %w", err)
	}
	summary.SummaryChecksum = checksum
	return AuditResult{Summary: summary, Ledger: ledger, AdjudicationQueue: queue}, nil
}

func validateReviewSet(authored map[string]AuthoredCase, reviews []ReviewSubmission, label string) (map[string]ReviewSubmission, string, error) {
	if len(reviews) != len(authored) {
		return nil, "", fmt.Errorf("%s submission count %d does not match case count %d", label, len(reviews), len(authored))
	}
	result := make(map[string]ReviewSubmission, len(reviews))
	reviewerID := ""
	for _, review := range reviews {
		current, ok := authored[review.CaseID]
		if !ok {
			return nil, "", fmt.Errorf("%s references unknown case %s", label, review.CaseID)
		}
		if _, duplicate := result[review.CaseID]; duplicate {
			return nil, "", fmt.Errorf("%s duplicates case %s", label, review.CaseID)
		}
		if !validRoleID(review.ReviewerID) || review.ReviewerID == current.AuthorID {
			return nil, "", fmt.Errorf("case %s has an invalid or conflicted reviewer", review.CaseID)
		}
		if reviewerID == "" {
			reviewerID = review.ReviewerID
		} else if reviewerID != review.ReviewerID {
			return nil, "", fmt.Errorf("%s file mixes reviewer identities", label)
		}
		if !validAnswerability(review.Answerability) || review.ReviewedAt.IsZero() {
			return nil, "", fmt.Errorf("case %s has an incomplete review", review.CaseID)
		}
		if err := validateJudgments(current, review.Judgments); err != nil {
			return nil, "", fmt.Errorf("case %s review: %w", review.CaseID, err)
		}
		result[review.CaseID] = review
	}
	return result, reviewerID, nil
}

func validateAdjudication(current AuthoredCase, adjudication Adjudication, reviewerA string, reviewerB string) error {
	if adjudication.Status != "resolved" || !validRoleID(adjudication.AdjudicatorID) ||
		adjudication.AdjudicatorID == current.AuthorID || adjudication.AdjudicatorID == reviewerA || adjudication.AdjudicatorID == reviewerB {
		return fmt.Errorf("case %s requires a resolved, independent third-party adjudication", current.CaseID)
	}
	if !validAnswerability(adjudication.Answerability) || adjudication.AdjudicatedAt.IsZero() {
		return fmt.Errorf("case %s adjudication is incomplete", current.CaseID)
	}
	rationale := strings.TrimSpace(adjudication.Rationale)
	if len(rationale) == 0 || len(rationale) > 2000 {
		return fmt.Errorf("case %s adjudication rationale must contain 1-2000 characters", current.CaseID)
	}
	if err := validateJudgments(current, adjudication.Judgments); err != nil {
		return fmt.Errorf("case %s adjudication: %w", current.CaseID, err)
	}
	return nil
}

func validateJudgments(current AuthoredCase, judgments []Judgment) error {
	if len(judgments) != len(current.CandidateRefs) {
		return fmt.Errorf("judgment count %d does not match candidate count %d", len(judgments), len(current.CandidateRefs))
	}
	want := make(map[string]struct{}, len(current.CandidateRefs))
	for _, ref := range current.CandidateRefs {
		want[refKey(ref)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(judgments))
	for _, judgment := range judgments {
		key := refKey(judgment.Ref())
		if _, ok := want[key]; !ok {
			return fmt.Errorf("judgment source %s is outside the authored candidate pool", key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate judgment source %s", key)
		}
		if judgment.RelevanceGrade < 0 || judgment.RelevanceGrade > 3 {
			return fmt.Errorf("source %s has invalid relevance grade %d", key, judgment.RelevanceGrade)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateFinalSemantics(current AuthoredCase, adjudication Adjudication) error {
	highGrade := 0
	gradeOneOrHigher := 0
	for _, judgment := range adjudication.Judgments {
		if judgment.RelevanceGrade >= 2 {
			highGrade++
		}
		if judgment.RelevanceGrade >= 1 {
			gradeOneOrHigher++
		}
	}
	switch current.TaskType {
	case "no_relevant_document", "out_of_domain_noise":
		if adjudication.Answerability != "no_relevant_document" || highGrade > 0 {
			return fmt.Errorf("case %s requires no_relevant_document and no grade-2-or-3 source", current.CaseID)
		}
	case "insufficient_evidence":
		if adjudication.Answerability != "insufficient_evidence" || highGrade > 0 || gradeOneOrHigher == 0 {
			return fmt.Errorf("case %s requires related grade-1 evidence but no sufficient source", current.CaseID)
		}
	default:
		if adjudication.Answerability != "answerable" || highGrade == 0 {
			return fmt.Errorf("case %s requires an answerable label and at least one grade-2-or-3 source", current.CaseID)
		}
	}
	return nil
}

type reviewPair struct {
	answerA   string
	answerB   string
	gradesA   []int
	gradesB   []int
	disagrees bool
}

func makeReviewPair(a ReviewSubmission, b ReviewSubmission) reviewPair {
	gradesA := gradeMap(a.Judgments)
	gradesB := gradeMap(b.Judgments)
	keys := make([]string, 0, len(gradesA))
	for key := range gradesA {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pair := reviewPair{answerA: a.Answerability, answerB: b.Answerability, disagrees: a.Answerability != b.Answerability}
	for _, key := range keys {
		pair.gradesA = append(pair.gradesA, gradesA[key])
		pair.gradesB = append(pair.gradesB, gradesB[key])
		if gradesA[key] != gradesB[key] {
			pair.disagrees = true
		}
	}
	return pair
}

func gradeMap(judgments []Judgment) map[string]int {
	result := make(map[string]int, len(judgments))
	for _, judgment := range judgments {
		result[refKey(judgment.Ref())] = judgment.RelevanceGrade
	}
	return result
}

func calculateAgreement(pairs []reviewPair) Agreement {
	result := Agreement{CaseCount: len(pairs)}
	if len(pairs) == 0 {
		return result
	}
	exactAnswers := 0
	binaryA := make([]int, 0, len(pairs))
	binaryB := make([]int, 0, len(pairs))
	gradesA := make([]int, 0)
	gradesB := make([]int, 0)
	exactGrades := 0
	for _, pair := range pairs {
		if pair.answerA == pair.answerB {
			exactAnswers++
		}
		binaryA = append(binaryA, boolInt(pair.answerA == "answerable"))
		binaryB = append(binaryB, boolInt(pair.answerB == "answerable"))
		for index := range pair.gradesA {
			gradesA = append(gradesA, pair.gradesA[index])
			gradesB = append(gradesB, pair.gradesB[index])
			if pair.gradesA[index] == pair.gradesB[index] {
				exactGrades++
			}
		}
	}
	result.JudgmentCount = len(gradesA)
	result.AnswerabilityExactAgreement = round4(float64(exactAnswers) / float64(len(pairs)))
	result.AnswerabilityBinaryKappa = round4(cohenKappa(binaryA, binaryB, 2))
	if len(gradesA) > 0 {
		result.RelevanceExactAgreement = round4(float64(exactGrades) / float64(len(gradesA)))
		result.RelevanceQuadraticKappa = round4(quadraticWeightedKappa(gradesA, gradesB, 4))
	}
	return result
}

func cohenKappa(a []int, b []int, categories int) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	countsA := make([]float64, categories)
	countsB := make([]float64, categories)
	agree := 0.0
	for index := range a {
		countsA[a[index]]++
		countsB[b[index]]++
		if a[index] == b[index] {
			agree++
		}
	}
	n := float64(len(a))
	observed := agree / n
	expected := 0.0
	for category := 0; category < categories; category++ {
		expected += (countsA[category] / n) * (countsB[category] / n)
	}
	if expected == 1 {
		if observed == 1 {
			return 1
		}
		return 0
	}
	return (observed - expected) / (1 - expected)
}

func quadraticWeightedKappa(a []int, b []int, categories int) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	countsA := make([]float64, categories)
	countsB := make([]float64, categories)
	observedDisagreement := 0.0
	denominator := float64((categories - 1) * (categories - 1))
	for index := range a {
		countsA[a[index]]++
		countsB[b[index]]++
		difference := float64(a[index] - b[index])
		observedDisagreement += difference * difference / denominator
	}
	n := float64(len(a))
	observedDisagreement /= n
	expectedDisagreement := 0.0
	for left := 0; left < categories; left++ {
		for right := 0; right < categories; right++ {
			difference := float64(left - right)
			weight := difference * difference / denominator
			expectedDisagreement += weight * (countsA[left] / n) * (countsB[right] / n)
		}
	}
	if expectedDisagreement == 0 {
		if observedDisagreement == 0 {
			return 1
		}
		return 0
	}
	return 1 - observedDisagreement/expectedDisagreement
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}
