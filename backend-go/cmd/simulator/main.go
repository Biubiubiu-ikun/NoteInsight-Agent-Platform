package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
	"creatorinsight/backend-go/internal/simulator"

	"github.com/jmoiron/sqlx"
)

func main() {
	var (
		profile         = flag.String("profile", "smoke", "simulation profile: smoke, dev, or scale")
		scenario        = flag.String("scenario", "mixed", "traffic scenario: organic, viral, controversy, or mixed")
		seed            = flag.Int64("seed", 20260714, "deterministic random seed")
		runID           = flag.String("run-id", "", "stable simulation run identifier")
		sessions        = flag.Int("sessions", 0, "override profile session count")
		maxSteps        = flag.Int("max-steps", 0, "override maximum events per session")
		users           = flag.Int("users", 0, "override profile user count")
		notes           = flag.Int("notes", 0, "override profile note count")
		commentsPerNote = flag.Int("comments-per-note", 20, "comments retained per note for comment-like behavior")
		startAt         = flag.String("start-at", "", "override simulated start time in RFC3339")
		duration        = flag.Duration("duration", 0, "override simulated duration")
		datasetSource   = flag.String("dataset", "synthetic", "dataset source: synthetic or database")
		writeDB         = flag.Bool("write-db", false, "persist profiles, run metadata, and behavior events")
		outputDir       = flag.String("output-dir", "tmp/simulator", "root directory for NDJSON and report output")
		noEventFiles    = flag.Bool("no-event-files", false, "generate only database rows and console report")
		replace         = flag.Bool("replace", false, "replace an existing output/database run with the same run_id")
		dryRun          = flag.Bool("dry-run", false, "print resolved configuration without generating events")
		strict          = flag.Bool("strict", true, "exit non-zero when a distribution quality check fails")
	)
	flag.Parse()

	appCfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.NewForService("noteinsight-simulator", appCfg.App.Env, appCfg.Log.Level)
	slog.SetDefault(logger)

	preset, err := simulator.PresetFor(*profile, *seed, simulator.Scenario(strings.ToLower(strings.TrimSpace(*scenario))))
	if err != nil {
		logger.Error("resolve simulator profile failed", "error", err)
		os.Exit(1)
	}
	resolved := preset.Config
	if *runID != "" {
		resolved.RunID = strings.TrimSpace(*runID)
	}
	if *sessions > 0 {
		resolved.Sessions = *sessions
	}
	if *maxSteps > 0 {
		resolved.MaxSteps = *maxSteps
	}
	if *users > 0 {
		preset.UserLimit = *users
	}
	if *notes > 0 {
		preset.NoteLimit = *notes
	}
	if *startAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, *startAt)
		if parseErr != nil {
			logger.Error("parse start-at failed", "error", parseErr)
			os.Exit(1)
		}
		resolved.StartAt = parsed
	}
	if *duration > 0 {
		resolved.Duration = *duration
	}
	resolved.Normalize()
	if err := resolved.Validate(); err != nil {
		logger.Error("validate simulator config failed", "error", err)
		os.Exit(1)
	}

	if *dryRun {
		printJSON(map[string]any{
			"config":               resolved,
			"dataset":              *datasetSource,
			"users":                preset.UserLimit,
			"notes":                preset.NoteLimit,
			"comments_per_note":    *commentsPerNote,
			"estimated_max_events": resolved.Sessions * resolved.MaxSteps,
			"write_db":             *writeDB,
			"write_event_files":    !*noEventFiles,
		})
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	source := strings.ToLower(strings.TrimSpace(*datasetSource))
	if *writeDB {
		source = "database"
	}
	var db *sqlx.DB
	if source == "database" {
		db, err = database.NewPostgresDB(ctx, appCfg.Postgres)
		if err != nil {
			logger.Error("connect postgres failed", "error", err)
			os.Exit(1)
		}
		defer db.Close()
	} else if source != "synthetic" {
		logger.Error("invalid dataset source", "dataset", source)
		os.Exit(1)
	}

	var dataset simulator.Dataset
	var repository *simulator.Repository
	if db != nil {
		repository = simulator.NewRepository(db)
		dataset, err = repository.LoadDataset(ctx, preset.UserLimit, preset.NoteLimit, *commentsPerNote)
	} else {
		dataset = simulator.SyntheticDataset(preset.UserLimit, preset.NoteLimit, *commentsPerNote)
	}
	if err != nil {
		logger.Error("load simulator dataset failed", "error", err)
		os.Exit(1)
	}

	var sinks []simulator.Sink
	var fileSink *simulator.FileSink
	if !*noEventFiles {
		fileSink, err = simulator.NewFileSink(*outputDir, resolved.RunID, *replace)
		if err != nil {
			logger.Error("create simulator output failed", "error", err)
			os.Exit(1)
		}
		sinks = append(sinks, fileSink)
	}
	if *writeDB {
		databaseSink, sinkErr := repository.NewDatabaseSink(ctx, resolved, *replace)
		if sinkErr != nil {
			logger.Error("create simulator database run failed", "error", sinkErr)
			if fileSink != nil {
				fileSink.Abort(ctx, sinkErr)
			}
			os.Exit(1)
		}
		sinks = append(sinks, databaseSink)
	}

	startedAt := time.Now()
	report, err := simulator.NewEngine().Generate(ctx, resolved, dataset, simulator.NewMultiSink(sinks...))
	if err != nil {
		logger.Error("behavior simulation failed", "error", err)
		os.Exit(1)
	}
	logger.Info("behavior simulation completed",
		"run_id", report.RunID,
		"events", report.Events,
		"sessions", report.Sessions,
		"duration", time.Since(startedAt).String(),
		"top_1pct_note_share", report.TopOnePercentNoteShare,
		"top_10pct_user_share", report.TopTenPercentUserShare,
	)
	printJSON(report)

	if *strict {
		for _, check := range report.Checks {
			if !check.Passed {
				logger.Error("distribution quality check failed", "check", check.Name, "value", check.Value, "target", check.Target)
				os.Exit(2)
			}
		}
	}
}

func printJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "encode output: %v\n", err)
	}
}
