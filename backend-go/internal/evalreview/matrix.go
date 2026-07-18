package evalreview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"
)

func BuildMatrix() ([]MatrixSlot, MatrixManifest, error) {
	slots := make([]MatrixSlot, 0, len(TaskOrder)*TargetCasesPerTask)
	for _, task := range TaskOrder {
		for _, split := range []string{"development", "holdout"} {
			shortSplit := "dev"
			if split == "holdout" {
				shortSplit = "holdout"
			}
			for ordinal := 1; ordinal <= TargetCasesPerTaskSplit; ordinal++ {
				slots = append(slots, MatrixSlot{
					CaseID:        fmt.Sprintf("rv5-%s-%s-%03d", task, shortSplit, ordinal),
					TaskType:      task,
					Split:         split,
					RubricVersion: RubricVersion,
					Status:        "awaiting_author",
				})
			}
		}
	}
	checksum, err := valueChecksum(slots)
	if err != nil {
		return nil, MatrixManifest{}, fmt.Errorf("checksum review matrix: %w", err)
	}
	manifest := MatrixManifest{
		MatrixVersion:  MatrixVersion,
		RubricVersion:  RubricVersion,
		Status:         "authoring",
		CaseCount:      len(slots),
		SplitCounts:    map[string]int{"development": len(TaskOrder) * TargetCasesPerTaskSplit, "holdout": len(TaskOrder) * TargetCasesPerTaskSplit},
		TaskCounts:     map[string]int{},
		MatrixChecksum: checksum,
	}
	for _, task := range TaskOrder {
		manifest.TaskCounts[task] = TargetCasesPerTask
	}
	return slots, manifest, nil
}

func InitializeWorkspace(root string) (MatrixManifest, error) {
	if root == "" {
		return MatrixManifest{}, fmt.Errorf("workspace root is required")
	}
	if _, err := os.Stat(root); err == nil {
		return MatrixManifest{}, fmt.Errorf("review workspace already exists: %s", root)
	} else if !os.IsNotExist(err) {
		return MatrixManifest{}, fmt.Errorf("inspect review workspace: %w", err)
	}
	slots, manifest, err := BuildMatrix()
	if err != nil {
		return MatrixManifest{}, err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return MatrixManifest{}, fmt.Errorf("create review workspace: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(root)
		}
	}()
	if err := WriteJSONLines(filepath.Join(root, "authoring_matrix.jsonl"), slots); err != nil {
		return MatrixManifest{}, err
	}
	templates := make([]AuthoredCase, 0, len(slots))
	for _, slot := range slots {
		templates = append(templates, AuthoredCase{
			CaseID: slot.CaseID, TaskType: slot.TaskType, Split: slot.Split,
			RubricVersion: RubricVersion, CandidateRefs: []CandidateRef{},
		})
	}
	if err := WriteJSONLines(filepath.Join(root, "authored_cases.template.jsonl"), templates); err != nil {
		return MatrixManifest{}, err
	}
	if err := WriteJSON(filepath.Join(root, "review_plan.json"), manifest); err != nil {
		return MatrixManifest{}, err
	}
	cleanup = false
	return manifest, nil
}

func VerifyWorkspace(root string) (MatrixManifest, error) {
	slots, err := ReadJSONLines[MatrixSlot](filepath.Join(root, "authoring_matrix.jsonl"))
	if err != nil {
		return MatrixManifest{}, err
	}
	rawManifest, err := os.ReadFile(filepath.Join(root, "review_plan.json"))
	if err != nil {
		return MatrixManifest{}, fmt.Errorf("read review plan: %w", err)
	}
	var manifest MatrixManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return MatrixManifest{}, fmt.Errorf("decode review plan: %w", err)
	}
	expectedSlots, expectedManifest, err := BuildMatrix()
	if err != nil {
		return MatrixManifest{}, err
	}
	if !reflect.DeepEqual(slots, expectedSlots) {
		return MatrixManifest{}, fmt.Errorf("authoring matrix does not match deterministic %s", MatrixVersion)
	}
	if !reflect.DeepEqual(manifest, expectedManifest) {
		return MatrixManifest{}, fmt.Errorf("review plan checksum or matrix counts do not match deterministic %s", MatrixVersion)
	}
	return manifest, nil
}

func ValidateAuthoredMatrix(cases []AuthoredCase) error {
	slots, _, err := BuildMatrix()
	if err != nil {
		return err
	}
	if len(cases) != len(slots) {
		return fmt.Errorf("authored case count %d does not match matrix count %d", len(cases), len(slots))
	}
	want := make(map[string]MatrixSlot, len(slots))
	for _, slot := range slots {
		want[slot.CaseID] = slot
	}
	seenQueries := make(map[string]string, len(cases))
	seenCases := make(map[string]struct{}, len(cases))
	for index := range cases {
		current := &cases[index]
		current.CaseID = stringsTrim(current.CaseID)
		current.AuthorID = stringsTrim(current.AuthorID)
		current.Query = stringsTrim(current.Query)
		current.ExpectedAnswer = stringsTrim(current.ExpectedAnswer)
		slot, ok := want[current.CaseID]
		if !ok {
			return fmt.Errorf("authored case %d has unknown case_id %q", index+1, current.CaseID)
		}
		if _, duplicate := seenCases[current.CaseID]; duplicate {
			return fmt.Errorf("duplicate authored case_id %q", current.CaseID)
		}
		if current.TaskType != slot.TaskType || current.Split != slot.Split || current.RubricVersion != RubricVersion {
			return fmt.Errorf("authored case %s does not match its frozen matrix slot", current.CaseID)
		}
		if !validRoleID(current.AuthorID) || current.Query == "" || current.ExpectedAnswer == "" {
			return fmt.Errorf("authored case %s requires a valid author, query, and expected answer", current.CaseID)
		}
		if current.DraftAssistance != "none" && current.DraftAssistance != "model_assisted" {
			return fmt.Errorf("authored case %s has invalid draft_assistance", current.CaseID)
		}
		if len(current.CandidateRefs) == 0 {
			return fmt.Errorf("authored case %s requires a non-empty candidate pool", current.CaseID)
		}
		if current.TaskType == "authorization_boundary" && !validAuthorizationContext(current.Metadata) {
			return fmt.Errorf("authored case %s requires distinct allowed/denied users and a project boundary", current.CaseID)
		}
		candidateSeen := map[string]struct{}{}
		for _, ref := range current.CandidateRefs {
			if ref.SourceID <= 0 || ref.SourceVersion <= 0 || !validSourceType(ref.SourceType) {
				return fmt.Errorf("authored case %s has an invalid candidate source", current.CaseID)
			}
			key := refKey(ref)
			if _, duplicate := candidateSeen[key]; duplicate {
				return fmt.Errorf("authored case %s duplicates candidate %s", current.CaseID, key)
			}
			candidateSeen[key] = struct{}{}
		}
		normalized := normalizeQuery(current.Query)
		if normalized == "" {
			return fmt.Errorf("authored case %s query has no searchable letters or numbers", current.CaseID)
		}
		if previous, duplicate := seenQueries[normalized]; duplicate {
			return fmt.Errorf("authored cases %s and %s duplicate a normalized query", previous, current.CaseID)
		}
		seenQueries[normalized] = current.CaseID
		seenCases[current.CaseID] = struct{}{}
	}
	return nil
}

func validAuthorizationContext(metadata map[string]any) bool {
	projectID, projectOK := positiveMetadataInt(metadata, "required_project_id")
	allowedID, allowedOK := positiveMetadataInt(metadata, "allowed_user_id")
	deniedID, deniedOK := positiveMetadataInt(metadata, "denied_user_id")
	return projectOK && allowedOK && deniedOK && projectID > 0 && allowedID != deniedID
}

func positiveMetadataInt(metadata map[string]any, key string) (int64, bool) {
	if metadata == nil {
		return 0, false
	}
	switch value := metadata[key].(type) {
	case int:
		return int64(value), value > 0
	case int64:
		return value, value > 0
	case float64:
		return int64(value), value > 0 && value == float64(int64(value))
	default:
		return 0, false
	}
}

func validSourceType(value string) bool {
	switch value {
	case "note", "note_body", "note_media", "note_comment":
		return true
	default:
		return false
	}
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}

func normalizeQuery(value string) string {
	var normalized strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			normalized.WriteRune(char)
		}
	}
	return normalized.String()
}
