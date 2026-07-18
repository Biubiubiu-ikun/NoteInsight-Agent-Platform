package evalreview

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type DraftSource struct {
	CandidateRef
	ProjectID  int64
	Visibility string
	Canonical  string
	Title      string
	Body       string
	Caption    string
	OCRText    string
	Category   string
	Tags       []string
	Topics     []string
	NoteID     int64
	Position   int
	CreatedAt  time.Time
}

type DraftCorpus struct {
	Sources []DraftSource
}

type DraftSourceLoader interface {
	LoadDraftCorpus(ctx context.Context, datasetVersionID int64, ingestionRunID string) (DraftCorpus, error)
}

func (r *Repository) LoadDraftCorpus(ctx context.Context, datasetVersionID int64, ingestionRunID string) (DraftCorpus, error) {
	if datasetVersionID <= 0 || !validIdentifier(ingestionRunID) {
		return DraftCorpus{}, fmt.Errorf("dataset version and ingestion run are required")
	}
	rows, err := r.db.QueryxContext(ctx, `
SELECT dvs.source_type, dvs.source_id, dvs.source_version, dvs.project_id,
       dvs.visibility, esp.canonical_text, esp.source_payload
FROM dataset_version_sources dvs
JOIN evidence_source_payloads esp ON esp.evidence_source_id=dvs.evidence_source_id
WHERE dvs.dataset_version_id=$1
  AND dvs.source_type IN ('note', 'note_media')
  AND EXISTS (
      SELECT 1
      FROM ingestion_runs ir
      JOIN ingestion_run_documents ird ON ird.run_id=ir.run_id
      JOIN source_citations sc ON sc.document_id=ird.document_id
      WHERE ir.run_id=$2
        AND ir.dataset_version_id=$1
        AND ir.status='completed'
        AND sc.evidence_source_id=dvs.evidence_source_id
  )
ORDER BY dvs.source_type, dvs.source_id, dvs.source_version`, datasetVersionID, ingestionRunID)
	if err != nil {
		return DraftCorpus{}, fmt.Errorf("load frozen draft corpus: %w", err)
	}
	defer rows.Close()

	corpus := DraftCorpus{Sources: make([]DraftSource, 0, 12000)}
	for rows.Next() {
		var source DraftSource
		var payloadRaw []byte
		if err := rows.Scan(
			&source.SourceType, &source.SourceID, &source.SourceVersion,
			&source.ProjectID, &source.Visibility, &source.Canonical, &payloadRaw,
		); err != nil {
			return DraftCorpus{}, fmt.Errorf("scan frozen draft source: %w", err)
		}
		var payload struct {
			Title     string    `json:"title"`
			Body      string    `json:"body"`
			Caption   string    `json:"caption"`
			OCRText   string    `json:"ocr_text"`
			Category  string    `json:"category"`
			Tags      []string  `json:"tags"`
			Topics    []string  `json:"topics"`
			NoteID    int64     `json:"note_id"`
			Position  int       `json:"position"`
			CreatedAt time.Time `json:"created_at"`
		}
		if err := json.Unmarshal(payloadRaw, &payload); err != nil {
			return DraftCorpus{}, fmt.Errorf("decode source payload %s:%d:%d: %w", source.SourceType, source.SourceID, source.SourceVersion, err)
		}
		source.Title = strings.TrimSpace(payload.Title)
		source.Body = strings.TrimSpace(payload.Body)
		source.Caption = strings.TrimSpace(payload.Caption)
		source.OCRText = strings.TrimSpace(payload.OCRText)
		source.Category = strings.TrimSpace(payload.Category)
		source.Tags = payload.Tags
		source.Topics = payload.Topics
		source.Position = payload.Position
		source.CreatedAt = payload.CreatedAt
		if source.SourceType == "note" {
			source.NoteID = source.SourceID
		} else {
			source.NoteID = payload.NoteID
		}
		if source.NoteID <= 0 || strings.TrimSpace(source.Canonical) == "" {
			return DraftCorpus{}, fmt.Errorf("source %s:%d:%d has incomplete canonical identity", source.SourceType, source.SourceID, source.SourceVersion)
		}
		corpus.Sources = append(corpus.Sources, source)
	}
	if err := rows.Err(); err != nil {
		return DraftCorpus{}, fmt.Errorf("iterate frozen draft corpus: %w", err)
	}
	if len(corpus.Sources) == 0 {
		return DraftCorpus{}, fmt.Errorf("frozen draft corpus is empty")
	}
	sort.Slice(corpus.Sources, func(i, j int) bool {
		return refKey(corpus.Sources[i].CandidateRef) < refKey(corpus.Sources[j].CandidateRef)
	})
	return corpus, nil
}
