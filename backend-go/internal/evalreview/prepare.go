package evalreview

import (
	"context"
	"fmt"
	"sort"
)

type PreparedReview struct {
	ReviewerA []Assignment
	ReviewerB []Assignment
	Sources   []CandidateSource
}

func Prepare(ctx context.Context, authored []AuthoredCase, reviewerA string, reviewerB string, datasetVersionID int64, ingestionRunID string, resolver SourceResolver) (PreparedReview, error) {
	if err := ValidateAuthoredMatrix(authored); err != nil {
		return PreparedReview{}, err
	}
	if !validRoleID(reviewerA) || !validRoleID(reviewerB) || reviewerA == reviewerB {
		return PreparedReview{}, fmt.Errorf("two distinct reviewer identifiers are required")
	}
	if datasetVersionID <= 0 || !validIdentifier(ingestionRunID) {
		return PreparedReview{}, fmt.Errorf("a frozen dataset version and ingestion run are required")
	}

	uniqueRefs := make(map[string]CandidateRef)
	for _, authoredCase := range authored {
		if authoredCase.AuthorID == reviewerA || authoredCase.AuthorID == reviewerB {
			return PreparedReview{}, fmt.Errorf("case %s author cannot review their own case", authoredCase.CaseID)
		}
		for _, ref := range authoredCase.CandidateRefs {
			uniqueRefs[refKey(ref)] = ref
		}
	}
	refs := make([]CandidateRef, 0, len(uniqueRefs))
	for _, ref := range uniqueRefs {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refKey(refs[i]) < refKey(refs[j]) })
	resolved, err := resolver.ResolveSources(ctx, datasetVersionID, ingestionRunID, refs)
	if err != nil {
		return PreparedReview{}, err
	}
	byRef := make(map[string]CandidateSource, len(resolved))
	for _, source := range resolved {
		key := refKey(source.Ref())
		if _, duplicate := byRef[key]; duplicate {
			return PreparedReview{}, fmt.Errorf("source resolver returned duplicate %s", key)
		}
		byRef[key] = source
	}

	result := PreparedReview{
		ReviewerA: make([]Assignment, 0, len(authored)),
		ReviewerB: make([]Assignment, 0, len(authored)),
		Sources:   resolved,
	}
	for _, authoredCase := range authored {
		pool := make([]CandidateSource, 0, len(authoredCase.CandidateRefs))
		for _, ref := range authoredCase.CandidateRefs {
			source, ok := byRef[refKey(ref)]
			if !ok {
				return PreparedReview{}, fmt.Errorf("case %s source %s was not resolved", authoredCase.CaseID, refKey(ref))
			}
			pool = append(pool, source)
		}
		base := Assignment{
			CaseID:        authoredCase.CaseID,
			TaskType:      authoredCase.TaskType,
			Split:         authoredCase.Split,
			RubricVersion: RubricVersion,
			Query:         authoredCase.Query,
			ReviewContext: blindReviewContext(authoredCase.Metadata),
			CandidatePool: pool,
			ReviewBlind:   true,
		}
		assignmentA := base
		assignmentA.ReviewerID = reviewerA
		assignmentB := base
		assignmentB.ReviewerID = reviewerB
		result.ReviewerA = append(result.ReviewerA, assignmentA)
		result.ReviewerB = append(result.ReviewerB, assignmentB)
	}
	return result, nil
}

func blindReviewContext(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	result := map[string]any{}
	for _, key := range []string{"required_project_id", "allowed_user_id", "denied_user_id", "as_of", "comparison_dimensions"} {
		if value, ok := metadata[key]; ok {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
