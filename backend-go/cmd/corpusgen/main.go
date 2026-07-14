package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/contentgen"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
)

func main() {
	var (
		profile          = flag.String("profile", "quality", "corpus profile: smoke or quality")
		seed             = flag.Int64("seed", 20260714, "deterministic content seed")
		runID            = flag.String("run-id", "", "stable content corpus run identifier")
		notes            = flag.Int("notes", 0, "override profile note count")
		commentsPerNote  = flag.Int("comments-per-note", 0, "override comments generated for each note")
		mediaPerNote     = flag.Int("media-per-note", 0, "override OCR-rich media entries for each note")
		evalCasesPerNote = flag.Int("eval-cases-per-note", 0, "override ground-truth cases per note, max 5")
		projectID        = flag.Int64("project-id", 0, "project identifier assigned to generated notes")
		startAt          = flag.String("start-at", "", "override deterministic content start time in RFC3339")
		replace          = flag.Bool("replace", false, "replace an existing run and its generated notes")
		dryRun           = flag.Bool("dry-run", false, "print resolved volume without connecting to PostgreSQL")
		strict           = flag.Bool("strict", true, "reject corpus when a text quality check fails")
		outputDir        = flag.String("output-dir", "tmp/corpus", "directory for the generated quality report")
		noReportFile     = flag.Bool("no-report-file", false, "keep the report in PostgreSQL/stdout without writing a local file")
	)
	flag.Parse()

	appCfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.NewForService("noteinsight-corpusgen", appCfg.App.Env, appCfg.Log.Level)
	slog.SetDefault(logger)
	if appCfg.App.Env == "prod" && !*dryRun {
		logger.Error("corpusgen database writes are disabled in production")
		os.Exit(1)
	}

	resolved, err := contentgen.PresetFor(*profile, *seed)
	if err != nil {
		logger.Error("resolve corpus profile failed", "error", err)
		os.Exit(1)
	}
	if value := strings.TrimSpace(*runID); value != "" {
		resolved.RunID = value
	}
	if *notes > 0 {
		resolved.Notes = *notes
	}
	if *commentsPerNote > 0 {
		resolved.CommentsPerNote = *commentsPerNote
	}
	if *mediaPerNote > 0 {
		resolved.MediaPerNote = *mediaPerNote
	}
	if *evalCasesPerNote > 0 {
		resolved.EvalCasesPerNote = *evalCasesPerNote
	}
	resolved.ProjectID = *projectID
	if *startAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, *startAt)
		if parseErr != nil {
			logger.Error("parse start-at failed", "error", parseErr)
			os.Exit(1)
		}
		resolved.StartAt = parsed
	}
	resolved.Normalize()

	if *dryRun {
		printJSON(map[string]any{
			"config":                     resolved,
			"expected_media":             resolved.Notes * resolved.MediaPerNote,
			"expected_comments":          resolved.Notes * resolved.CommentsPerNote,
			"expected_eval_cases":        resolved.Notes * resolved.EvalCasesPerNote,
			"generation_method":          "deterministic_templates_and_hidden_scenarios",
			"requires_llm_api":           false,
			"requires_existing_users":    true,
			"note_and_comment_id_starts": "resolved from PostgreSQL at write time",
			"write_report_file":          !*noReportFile,
		})
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.NewPostgresDB(ctx, appCfg.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	repository := contentgen.NewRepository(db)
	users, creators, err := repository.LoadActorIDs(ctx)
	if err != nil {
		logger.Error("load corpus actors failed", "error", err)
		os.Exit(1)
	}
	resolved, err = repository.ResolveIDs(ctx, resolved, *replace)
	if err != nil {
		logger.Error("resolve corpus IDs failed", "error", err)
		os.Exit(1)
	}

	startedAt := time.Now()
	corpus, report, err := contentgen.Generate(resolved, users, creators)
	if err != nil {
		logger.Error("generate quality corpus failed", "error", err)
		os.Exit(1)
	}
	if *strict {
		for _, check := range report.Checks {
			if !check.Passed {
				logger.Error("corpus quality check failed", "check", check.Name, "value", check.Value, "target", check.Target)
				os.Exit(2)
			}
		}
	}
	if err := repository.Save(ctx, corpus, report, *replace); err != nil {
		logger.Error("persist quality corpus failed", "error", err)
		os.Exit(1)
	}
	if !*noReportFile {
		if err := writeReport(*outputDir, resolved.RunID, report); err != nil {
			logger.Error("write corpus report failed", "error", err)
			os.Exit(1)
		}
	}
	logger.Info("quality corpus completed",
		"run_id", report.RunID,
		"notes", report.Notes,
		"media", report.Media,
		"comments", report.Comments,
		"eval_cases", report.EvalCases,
		"duration", time.Since(startedAt).String(),
	)
	printJSON(report)
}

func writeReport(root string, runID string, report contentgen.Report) error {
	directory := filepath.Join(root, runID)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary := filepath.Join(directory, "report.json.tmp")
	final := filepath.Join(directory, "report.json")
	if err := os.WriteFile(temporary, raw, 0644); err != nil {
		return err
	}
	if err := os.Rename(temporary, final); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func printJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "encode output: %v\n", err)
	}
}
