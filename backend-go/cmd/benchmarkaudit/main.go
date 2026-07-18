package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"creatorinsight/backend-go/internal/config"
	"creatorinsight/backend-go/internal/evalaudit"
	"creatorinsight/backend-go/internal/evalbench"
	"creatorinsight/backend-go/internal/platform/database"
)

func main() {
	benchmarkRoot := flag.String("benchmark-root", "../evaluation/benchmarks/retrieval_v4", "public benchmark artifact directory")
	output := flag.String("output", "", "optional JSON audit report path")
	timeout := flag.Duration("timeout", 2*time.Minute, "maximum audit duration")
	flag.Parse()

	manifest, err := evalbench.VerifyArtifacts(*benchmarkRoot)
	if err != nil {
		fatal("verify public benchmark", err)
	}
	if manifest.DevelopmentFile == "" || manifest.CasesFile != "" {
		fatal("verify public benchmark", fmt.Errorf("audit accepts public development artifacts only"))
	}
	cases, err := evalbench.ReadVerifiedCases(filepath.Join(*benchmarkRoot, manifest.DevelopmentFile), "development")
	if err != nil {
		fatal("read development cases", err)
	}
	appConfig, err := config.Load()
	if err != nil {
		fatal("load config", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := database.NewPostgresDB(ctx, appConfig.Postgres)
	if err != nil {
		fatal("connect postgres", err)
	}
	defer db.Close()
	scenarios, err := evalaudit.NewRepository(db).LoadScenarios(ctx, manifest.DatasetVersionID, manifest.SourceRunID)
	if err != nil {
		fatal("load audit corpus", err)
	}
	report, err := evalaudit.Analyze(manifest, cases, scenarios)
	if err != nil {
		fatal("analyze benchmark", err)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal("encode audit report", err)
	}
	raw = append(raw, '\n')
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
			fatal("create audit output directory", err)
		}
		temporary := *output + ".tmp"
		if err := os.WriteFile(temporary, raw, 0644); err != nil {
			fatal("write audit report", err)
		}
		if err := os.Rename(temporary, *output); err != nil {
			_ = os.Remove(temporary)
			fatal("publish audit report", err)
		}
	}
	_, _ = os.Stdout.Write(raw)
}

func fatal(action string, err error) {
	slog.Error(action+" failed", "error", err)
	os.Exit(1)
}
