package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/evalbench"
	"creatorinsight/backend-go/internal/platform/database"
	"creatorinsight/backend-go/internal/platform/logging"
)

func main() {
	benchmarkID := flag.String("benchmark-id", "retrieval_v4_20260716", "immutable retrieval benchmark identifier")
	benchmarkVersion := flag.String("version", "retrieval_v4", "unique benchmark version")
	sourceRunID := flag.String("source-run-id", "phase6c_quality_v2_20260715", "quality corpus run used only as source material")
	seed := flag.Int64("seed", 0, "legacy deterministic split seed; authored v4 keeps this at zero")
	caseCount := flag.Int("cases", 240, "total authored benchmark cases")
	developmentCases := flag.Int("development-cases", 80, "cases visible for retrieval development")
	datasetVersionID := flag.Int64("dataset-version-id", 0, "immutable dataset snapshot used by the benchmark")
	inputFile := flag.String("input-file", "", "private authored JSONL input; required unless legacy generation is explicitly enabled")
	legacyGenerate := flag.Bool("legacy-generate-v3", false, "allow retired deterministic v3 generation for historical audit only")
	outputDir := flag.String("output-dir", "../evaluation/benchmarks/retrieval_v4", "public development and nonce commitment directory")
	privateOutputDir := flag.String("private-output-dir", "../evaluation/private/retrieval_v4", "git-ignored full benchmark artifact directory")
	verifyOnly := flag.Bool("verify-only", false, "verify committed artifacts without connecting to PostgreSQL")
	flag.Parse()
	if *verifyOnly {
		manifest, err := evalbench.VerifyArtifacts(*outputDir)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "verify benchmark artifacts: %v\n", err)
			os.Exit(1)
		}
		printJSON(manifest)
		return
	}

	appConfig, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.NewForService("noteinsight-evalfreeze", appConfig.App.Env, appConfig.Log.Level)
	if appConfig.App.Env == "prod" {
		logger.Error("evaluation benchmark writes are disabled in production")
		os.Exit(1)
	}

	benchmarkConfig := evalbench.Config{
		BenchmarkID:      *benchmarkID,
		BenchmarkVersion: *benchmarkVersion,
		SourceRunID:      *sourceRunID,
		GeneratorVersion: evalbench.DefaultGeneratorVersion,
		Seed:             *seed,
		CaseCount:        *caseCount,
		DevelopmentCases: *developmentCases,
		DatasetVersionID: *datasetVersionID,
	}
	if *legacyGenerate {
		benchmarkConfig.CommitmentScheme = evalbench.LegacyCommitmentScheme
	} else {
		benchmarkConfig.GeneratorVersion = evalbench.AuthoredGeneratorVersion
		benchmarkConfig.CommitmentScheme = evalbench.NonceCommitmentScheme
	}
	benchmarkConfig.Normalize()
	if err := benchmarkConfig.Validate(); err != nil {
		logger.Error("invalid benchmark config", "error", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	db, err := database.NewPostgresDB(ctx, appConfig.Postgres)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	repository := evalbench.NewRepository(db)
	var benchmark evalbench.Benchmark
	if *legacyGenerate {
		documents, loadErr := repository.LoadSourceDocuments(ctx, benchmarkConfig.SourceRunID)
		if loadErr != nil {
			logger.Error("load legacy benchmark source documents failed", "error", loadErr)
			os.Exit(1)
		}
		benchmark, err = evalbench.Generate(benchmarkConfig, documents)
	} else {
		if *inputFile == "" {
			logger.Error("private authored input is required", "flag", "input-file")
			os.Exit(2)
		}
		authored, readErr := evalbench.ReadAuthoredCases(*inputFile)
		if readErr != nil {
			logger.Error("read private authored benchmark failed", "error", readErr)
			os.Exit(1)
		}
		benchmark, err = evalbench.FreezeAuthored(benchmarkConfig, authored)
	}
	if err != nil {
		logger.Error("build benchmark failed", "error", err)
		os.Exit(1)
	}
	artifacts, err := evalbench.StageArtifacts(*outputDir, *privateOutputDir, benchmark)
	if err != nil {
		logger.Error("stage benchmark artifacts failed", "error", err)
		os.Exit(1)
	}
	if err := repository.SaveFrozen(ctx, benchmark); err != nil {
		if errors.Is(err, evalbench.ErrBenchmarkExists) {
			logger.Error("benchmark version already exists and cannot be replaced",
				"benchmark_id", benchmarkConfig.BenchmarkID,
				"public_stage", artifacts.PublicDirectory,
				"private_stage", artifacts.PrivateDirectory,
			)
		} else {
			logger.Error("freeze benchmark failed; verified staging artifacts were preserved for recovery",
				"error", err,
				"public_stage", artifacts.PublicDirectory,
				"private_stage", artifacts.PrivateDirectory,
			)
		}
		os.Exit(1)
	}
	if err := artifacts.Publish(); err != nil {
		logger.Error("publish benchmark artifacts failed",
			"error", err,
			"public_stage", artifacts.PublicDirectory,
			"private_stage", artifacts.PrivateDirectory,
		)
		os.Exit(1)
	}
	logger.Info("retrieval benchmark frozen",
		"benchmark_id", benchmark.Manifest.BenchmarkID,
		"cases", benchmark.Manifest.CaseCount,
		"development", benchmark.Manifest.SplitCounts["development"],
		"holdout", benchmark.Manifest.SplitCounts["holdout"],
		"checksum", benchmark.Manifest.ManifestChecksum,
	)
	printJSON(benchmark.Manifest)
}

func printJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "encode output: %v\n", err)
	}
}
