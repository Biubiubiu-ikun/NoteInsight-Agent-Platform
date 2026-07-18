package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/evalreview"
	"creatorinsight/backend-go/internal/platform/database"
)

func main() {
	operation := flag.String("operation", "status", "init, prepare, audit, freeze, or status")
	workspace := flag.String("workspace", "../evaluation/private/retrieval_v5", "private, git-ignored review workspace")
	publicRoot := flag.String("public-root", "../evaluation/benchmarks/retrieval_v5", "public benchmark scaffold")
	datasetVersionID := flag.Int64("dataset-version-id", 2, "frozen evidence dataset version")
	ingestionRunID := flag.String("ingestion-run-id", "phase7a_dv2_rebuild_v2_20260718", "completed evidence ingestion run")
	reviewerA := flag.String("reviewer-a", "", "stable pseudonym for independent reviewer A")
	reviewerB := flag.String("reviewer-b", "", "stable pseudonym for independent reviewer B")
	flag.Parse()

	var err error
	switch *operation {
	case "init":
		var manifest evalreview.MatrixManifest
		manifest, err = evalreview.InitializeWorkspace(*workspace)
		if err == nil {
			printJSON(manifest)
		}
	case "prepare":
		err = prepare(*workspace, *reviewerA, *reviewerB, *datasetVersionID, *ingestionRunID)
	case "audit":
		var summary evalreview.ReviewSummary
		summary, err = audit(*workspace, *publicRoot, false)
		if err == nil {
			printJSON(summary)
		}
	case "freeze":
		var summary evalreview.ReviewSummary
		summary, err = audit(*workspace, *publicRoot, true)
		if err == nil {
			printJSON(summary)
		}
	case "status":
		err = status(*workspace)
	default:
		err = fmt.Errorf("unsupported operation %q", *operation)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func prepare(workspace string, reviewerA string, reviewerB string, datasetVersionID int64, ingestionRunID string) error {
	if _, err := evalreview.VerifyWorkspace(workspace); err != nil {
		return err
	}
	authoredPath := filepath.Join(workspace, "authored_cases.jsonl")
	for _, path := range []string{
		filepath.Join(workspace, "resolved_sources.jsonl"),
		filepath.Join(workspace, "reviewer_a", "assignments.jsonl"),
		filepath.Join(workspace, "reviewer_b", "assignments.jsonl"),
		filepath.Join(workspace, "reviewer_a", "submissions.jsonl"),
		filepath.Join(workspace, "reviewer_b", "submissions.jsonl"),
	} {
		if _, statErr := os.Stat(path); statErr == nil {
			return fmt.Errorf("review preparation is immutable once distributed; artifact already exists: %s", path)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
	}
	authored, err := evalreview.ReadJSONLines[evalreview.AuthoredCase](authoredPath)
	if err != nil {
		return err
	}
	appConfig, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := database.NewPostgresDB(ctx, appConfig.Postgres)
	if err != nil {
		return fmt.Errorf("connect PostgreSQL: %w", err)
	}
	defer db.Close()
	prepared, err := evalreview.Prepare(ctx, authored, reviewerA, reviewerB, datasetVersionID, ingestionRunID, evalreview.NewRepository(db))
	if err != nil {
		return err
	}
	if err := evalreview.WriteJSONLines(filepath.Join(workspace, "resolved_sources.jsonl"), prepared.Sources); err != nil {
		return err
	}
	for reviewer, assignments := range map[string][]evalreview.Assignment{"reviewer_a": prepared.ReviewerA, "reviewer_b": prepared.ReviewerB} {
		directory := filepath.Join(workspace, reviewer)
		if err := evalreview.WriteJSONLines(filepath.Join(directory, "assignments.jsonl"), assignments); err != nil {
			return err
		}
		templates := make([]evalreview.ReviewSubmission, 0, len(assignments))
		for _, assignment := range assignments {
			judgments := make([]evalreview.Judgment, 0, len(assignment.CandidatePool))
			for _, source := range assignment.CandidatePool {
				judgments = append(judgments, evalreview.Judgment{
					SourceType: source.SourceType, SourceID: source.SourceID, SourceVersion: source.SourceVersion, RelevanceGrade: -1,
				})
			}
			templates = append(templates, evalreview.ReviewSubmission{CaseID: assignment.CaseID, ReviewerID: assignment.ReviewerID, Judgments: judgments})
		}
		if err := evalreview.WriteJSONLines(filepath.Join(directory, "submissions.template.jsonl"), templates); err != nil {
			return err
		}
	}
	return nil
}

func audit(workspace string, publicRoot string, freeze bool) (evalreview.ReviewSummary, error) {
	if _, err := evalreview.VerifyWorkspace(workspace); err != nil {
		return evalreview.ReviewSummary{}, err
	}
	authoredPath := filepath.Join(workspace, "authored_cases.jsonl")
	sourcesPath := filepath.Join(workspace, "resolved_sources.jsonl")
	reviewAPath := filepath.Join(workspace, "reviewer_a", "submissions.jsonl")
	reviewBPath := filepath.Join(workspace, "reviewer_b", "submissions.jsonl")
	adjudicationPath := filepath.Join(workspace, "adjudications.jsonl")
	authored, err := evalreview.ReadJSONLines[evalreview.AuthoredCase](authoredPath)
	if err != nil {
		return evalreview.ReviewSummary{}, err
	}
	sources, err := evalreview.ReadJSONLines[evalreview.CandidateSource](sourcesPath)
	if err != nil {
		return evalreview.ReviewSummary{}, err
	}
	reviewsA, err := evalreview.ReadJSONLines[evalreview.ReviewSubmission](reviewAPath)
	if err != nil {
		return evalreview.ReviewSummary{}, err
	}
	reviewsB, err := evalreview.ReadJSONLines[evalreview.ReviewSubmission](reviewBPath)
	if err != nil {
		return evalreview.ReviewSummary{}, err
	}
	adjudications := []evalreview.Adjudication{}
	if _, statErr := os.Stat(adjudicationPath); statErr == nil {
		adjudications, err = evalreview.ReadJSONLines[evalreview.Adjudication](adjudicationPath)
		if err != nil {
			return evalreview.ReviewSummary{}, err
		}
	} else if !os.IsNotExist(statErr) {
		return evalreview.ReviewSummary{}, statErr
	}
	result, err := evalreview.Audit(authored, sources, reviewsA, reviewsB, adjudications)
	if err != nil {
		return evalreview.ReviewSummary{}, err
	}
	checksums := map[string]string{}
	for name, path := range map[string]string{
		"authored_cases.jsonl": authoredPath, "resolved_sources.jsonl": sourcesPath,
		"reviewer_a/submissions.jsonl": reviewAPath, "reviewer_b/submissions.jsonl": reviewBPath,
		"authoring_matrix.jsonl": filepath.Join(workspace, "authoring_matrix.jsonl"),
		"review_plan.json":       filepath.Join(workspace, "review_plan.json"),
		"rubric.md":              filepath.Join(publicRoot, "rubric.md"),
		"review.schema.json":     filepath.Join(publicRoot, "review.schema.json"),
	} {
		checksum, checksumErr := evalreview.FileChecksum(path)
		if checksumErr != nil {
			return evalreview.ReviewSummary{}, checksumErr
		}
		checksums[name] = checksum
	}
	if len(adjudications) > 0 {
		checksum, checksumErr := evalreview.FileChecksum(adjudicationPath)
		if checksumErr != nil {
			return evalreview.ReviewSummary{}, checksumErr
		}
		checksums["adjudications.jsonl"] = checksum
	}
	if freeze {
		summary, _, freezeErr := evalreview.FreezeApprovedCases(workspace, publicRoot, authored, sources, result, checksums)
		return summary, freezeErr
	}
	return evalreview.WriteAuditArtifacts(workspace, result, checksums)
}

func status(workspace string) error {
	manifest, err := evalreview.VerifyWorkspace(workspace)
	if err != nil {
		return err
	}
	fmt.Printf("matrix verified: %d cases checksum=%s\n", manifest.CaseCount, manifest.MatrixChecksum)
	for _, name := range []string{"review_plan.json", "authoring_matrix.jsonl", "authored_cases.template.jsonl", "authored_cases.jsonl", "resolved_sources.jsonl", "review_summary.private.json", "approved_cases.jsonl"} {
		path := filepath.Join(workspace, name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			fmt.Printf("%-30s missing\n", name)
			continue
		}
		if err != nil {
			return err
		}
		fmt.Printf("%-30s present (%d bytes)\n", name, info.Size())
	}
	return nil
}

func printJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}
