//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"creatorinsight/backend-go/internal/agent"
	"creatorinsight/backend-go/internal/auth"
	"creatorinsight/backend-go/internal/dataset"
	"creatorinsight/backend-go/internal/evidence"
	"creatorinsight/backend-go/internal/platform/requestmeta"
	"creatorinsight/backend-go/internal/retrieval"
)

func TestPhase8AAgentRunLineageIdempotencyAndIsolation(t *testing.T) {
	ctx := requestmeta.With(context.Background(), requestmeta.Metadata{
		RequestID: "phase8a-integration-request", TraceID: "phase8a-integration-trace",
	})
	owner := registerIntegrationUser(t, integrationAuthService())
	other := registerIntegrationUser(t, integrationAuthService())
	var projectID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO projects (slug,name,visibility,status)
VALUES ($1,'Phase 8A agent integration','private','active')
RETURNING id`, fmt.Sprintf("phase8a-%d", time.Now().UnixNano())).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO project_members (project_id,user_id,role,status)
VALUES ($1,$2,'owner','active')`, projectID, owner.User.ID); err != nil {
		t.Fatal(err)
	}
	var datasetID int64
	if err := integrationDB.QueryRowContext(ctx, `
SELECT id FROM datasets WHERE project_id=$1 AND slug='community'`, projectID).Scan(&datasetID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO notes (project_id,author_id,title,body,category,visibility)
VALUES ($1,$2,'Phase 8A source','Audience evidence for a bounded Agent contract.','study','project')`,
		projectID, owner.User.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := dataset.NewService(dataset.NewRepository(integrationDB)).Freeze(ctx, datasetID, owner.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	ingestionRunID := fmt.Sprintf("phase8a_ingest_%d", time.Now().UnixNano())
	if _, err := evidence.NewService(evidence.NewRepository(integrationDB)).Ingest(ctx, evidence.IngestRequest{
		RunID: ingestionRunID, DatasetVersionID: snapshot.ID, Mode: "incremental",
	}); err != nil {
		t.Fatal(err)
	}
	retrievalRepository := retrieval.NewRepository(integrationDB)
	if _, err := retrieval.NewService(retrievalRepository).BuildLexicalIndex(ctx, ingestionRunID); err != nil {
		t.Fatal(err)
	}
	service := agent.NewService(agent.NewRepository(integrationDB), retrievalRepository)
	currentOwner := auth.CurrentUser{ID: owner.User.ID, Username: owner.User.Username, Role: owner.User.Role, Status: owner.User.Status}
	input := agent.CreateRunInput{
		ProjectID: projectID, DatasetVersionID: snapshot.ID, IngestionRunID: ingestionRunID,
		Query: "Summarize the audience evidence", Mode: retrieval.ModeLexical,
		IdempotencyKey: fmt.Sprintf("phase8a-%d", projectID),
	}
	first, err := service.CreateRun(ctx, currentOwner, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Replayed || first.Run.Status != agent.StatusQueued ||
		first.Run.RetrievalPlan.DatasetManifestChecksum != snapshot.ManifestChecksum ||
		first.Run.RequestID != "phase8a-integration-request" || first.Run.TraceID != "phase8a-integration-trace" {
		t.Fatalf("first agent run = %+v", first)
	}
	second, err := service.CreateRun(ctx, currentOwner, input)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.Run.ID != first.Run.ID {
		t.Fatalf("idempotent replay = %+v, first id = %s", second, first.Run.ID)
	}
	conflicting := input
	conflicting.Query = "A different request under the same key"
	if _, err := service.CreateRun(ctx, currentOwner, conflicting); !errors.Is(err, agent.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict error = %v", err)
	}

	otherUser := auth.CurrentUser{ID: other.User.ID, Username: other.User.Username, Role: other.User.Role, Status: other.User.Status}
	if _, err := service.GetRun(ctx, otherUser, first.Run.ID); !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("cross-user get error = %v, want not found", err)
	}
	page, err := service.ListRuns(ctx, currentOwner, agent.ListRunsInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != first.Run.ID {
		t.Fatalf("owner run page = %+v", page)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_runs SET status='running', started_at=clock_timestamp() WHERE id=$1`, first.Run.ID); err == nil {
		t.Fatal("Agent run entered running state without a frozen model binding")
	}
	cancelled, err := service.CancelRun(ctx, currentOwner, first.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != agent.StatusCancelled || !cancelled.CancellationRequested || cancelled.CompletedAt == nil {
		t.Fatalf("cancelled run = %+v", cancelled)
	}
	if _, err := service.CancelRun(ctx, currentOwner, first.Run.ID); !errors.Is(err, agent.ErrConflict) {
		t.Fatalf("second cancel error = %v, want conflict", err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_runs SET query='tampered' WHERE id=$1`, first.Run.ID); err == nil {
		t.Fatal("terminal Agent request lineage mutation unexpectedly succeeded")
	}
	if _, err := integrationDB.ExecContext(ctx, `
DELETE FROM agent_runs WHERE id=$1`, first.Run.ID); err == nil {
		t.Fatal("Agent audit lineage deletion unexpectedly succeeded")
	}

	claimInput := input
	claimInput.IdempotencyKey = fmt.Sprintf("phase8a-claim-%d", projectID)
	claimRun, err := service.CreateRun(ctx, currentOwner, claimInput)
	if err != nil {
		t.Fatal(err)
	}
	var modelVersionID int64
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO agent_model_versions (provider,model,revision,parameters,status)
VALUES ('integration','bounded-agent','revision-1','{}','frozen')
RETURNING id`).Scan(&modelVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_runs
SET status='running', model_version_id=$2, started_at=clock_timestamp()
WHERE id=$1`, claimRun.Run.ID, modelVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_runs SET used_input_tokens=max_input_tokens+1 WHERE id=$1`, claimRun.Run.ID); err == nil {
		t.Fatal("Agent run exceeded its input token budget")
	}
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO agent_claims (run_id,ordinal,claim_type,claim_text,support_status,confidence)
VALUES ($1,99,'untracked','Invalid claim type','proposed',0)`, claimRun.Run.ID); err == nil {
		t.Fatal("Agent claim accepted an unsupported claim type")
	}
	var claimID string
	if err := integrationDB.QueryRowContext(ctx, `
INSERT INTO agent_claims (run_id,ordinal,claim_type,claim_text,support_status,confidence)
VALUES ($1,1,'factual','The source describes bounded audience evidence.','proposed',0.9)
RETURNING id::text`, claimRun.Run.ID).Scan(&claimID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_runs
SET status='succeeded', report='{"summary":"unsupported"}', completed_at=clock_timestamp()
WHERE id=$1`, claimRun.Run.ID); err == nil {
		t.Fatal("Agent run succeeded with an unsupported factual claim")
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_claims SET support_status='supported' WHERE id=$1`, claimID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_runs
SET status='succeeded', report='{"summary":"uncited"}', completed_at=clock_timestamp()
WHERE id=$1`, claimRun.Run.ID); err == nil {
		t.Fatal("Agent run succeeded with an uncited factual claim")
	}
	var citationID int64
	var quoteHash string
	if err := integrationDB.QueryRowContext(ctx, `
SELECT sc.id, sc.quote_hash
FROM source_citations sc
JOIN ingestion_run_documents rd ON rd.document_id=sc.document_id
WHERE rd.run_id=$1
ORDER BY sc.id
LIMIT 1`, ingestionRunID).Scan(&citationID, &quoteHash); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
INSERT INTO agent_claim_citations (
    claim_id,source_citation_id,citation_order,relationship,quote_hash_snapshot
) VALUES ($1,$2,1,'supports',$3)`, claimID, citationID, quoteHash); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_model_versions SET status='retired' WHERE id=$1`, modelVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_prompt_versions
SET status='retired'
WHERE id=(SELECT prompt_version_id FROM agent_runs WHERE id=$1)`, claimRun.Run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationDB.ExecContext(ctx, `
UPDATE agent_runs
SET status='succeeded', report='{"summary":"supported"}', completed_at=clock_timestamp()
WHERE id=$1`, claimRun.Run.ID); err != nil {
		t.Fatalf("complete supported Agent run after definition retirement: %v", err)
	}

	var promptIntegrity bool
	if err := integrationDB.QueryRowContext(ctx, `
SELECT template_sha256 = encode(digest(convert_to(template_text,'UTF8'),'sha256'),'hex')
FROM agent_prompt_versions WHERE prompt_key=$1 AND version=$2`,
		agent.DefaultPromptKey, agent.DefaultPromptVersion).Scan(&promptIntegrity); err != nil {
		t.Fatal(err)
	}
	if !promptIntegrity {
		t.Fatal("seeded Agent prompt checksum does not match its template")
	}
}
