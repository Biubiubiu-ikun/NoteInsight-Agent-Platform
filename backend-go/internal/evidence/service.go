package evidence

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

type Service struct {
	repository *Repository
}

func NewService(repository *Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Ingest(ctx context.Context, request IngestRequest) (Run, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.Mode = strings.TrimSpace(request.Mode)
	if request.RunID == "" {
		return Run{}, fmt.Errorf("run_id is required")
	}
	if request.DatasetVersionID <= 0 {
		return Run{}, fmt.Errorf("dataset_version_id must be positive")
	}
	if request.Mode != "incremental" && request.Mode != "rebuild" {
		return Run{}, fmt.Errorf("mode must be incremental or rebuild")
	}
	run, err := s.repository.BeginRun(ctx, request)
	if err != nil {
		return Run{}, err
	}
	if run.Status == "completed" {
		return run, nil
	}
	fail := func(cause error) (Run, error) {
		failureContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.repository.MarkFailed(failureContext, request.RunID, cause)
		return Run{}, cause
	}
	if _, err := s.repository.ReconcileLifecycle(ctx); err != nil {
		return fail(err)
	}
	sources, err := s.repository.ListSources(ctx, run)
	if err != nil {
		return fail(err)
	}
	if int64(len(sources)) != run.SourceCount {
		return fail(fmt.Errorf("snapshot source count changed: loaded %d want %d", len(sources), run.SourceCount))
	}
	documents, err := BuildSourceDocuments(run, sources)
	if err != nil {
		return fail(err)
	}
	facts, err := s.repository.ListFacts(ctx, run)
	if err != nil {
		return fail(err)
	}
	if int64(len(facts)) != run.FactSourceCount {
		return fail(fmt.Errorf("fact source count changed: loaded %d want %d", len(facts), run.FactSourceCount))
	}
	factDocuments, err := BuildFactDocuments(run, facts)
	if err != nil {
		return fail(err)
	}
	documents = append(documents, factDocuments...)
	reused, err := s.repository.LinkReusableDocuments(ctx, run.RunID, documents)
	if err != nil {
		return fail(err)
	}
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(IngestConcurrency)
	var completedDocuments atomic.Int64
	completedDocuments.Store(int64(len(reused)))
	if request.Progress != nil && len(reused) > 0 {
		request.Progress(len(reused), len(documents))
	}
	for index := range documents {
		document := documents[index]
		if _, found := reused[document.DocumentKey]; found {
			continue
		}
		group.Go(func() error {
			if _, err := s.repository.SaveDocument(groupContext, run.RunID, document); err != nil {
				return err
			}
			completed := int(completedDocuments.Add(1))
			if request.Progress != nil && (completed%250 == 0 || completed == len(documents)) {
				request.Progress(completed, len(documents))
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return fail(err)
	}
	completed, err := s.repository.CompleteRun(ctx, run.RunID)
	if err != nil {
		return fail(err)
	}
	report, err := s.repository.Audit(ctx, run.RunID)
	if err != nil {
		return Run{}, err
	}
	if !report.Healthy {
		return Run{}, fmt.Errorf("completed ingestion run %s failed consistency audit: %+v", run.RunID, report.Violations)
	}
	return completed, nil
}

func (s *Service) Reconcile(ctx context.Context) (ReconcileResult, error) {
	return s.repository.ReconcileLifecycle(ctx)
}

func (s *Service) Audit(ctx context.Context, runID string) (AuditReport, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return AuditReport{}, fmt.Errorf("run_id is required")
	}
	return s.repository.Audit(ctx, runID)
}
