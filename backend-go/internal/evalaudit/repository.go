package evalaudit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) LoadScenarios(ctx context.Context, datasetVersionID int64, sourceRunID string) ([]Scenario, error) {
	rows, err := r.db.QueryxContext(ctx, `
SELECT cs.note_id,cs.subject,cs.scenario
FROM content_scenarios cs
JOIN dataset_version_sources dvs
  ON dvs.dataset_version_id=$1 AND dvs.source_type='note' AND dvs.source_id=cs.note_id
WHERE cs.run_id=$2
ORDER BY cs.note_id`, datasetVersionID, sourceRunID)
	if err != nil {
		return nil, fmt.Errorf("load benchmark corpus scenarios: %w", err)
	}
	defer rows.Close()
	scenarios := make([]Scenario, 0)
	for rows.Next() {
		var scenario Scenario
		var raw []byte
		if err := rows.Scan(&scenario.NoteID, &scenario.Subject, &raw); err != nil {
			return nil, fmt.Errorf("scan benchmark corpus scenario: %w", err)
		}
		if err := json.Unmarshal(raw, &scenario.Payload); err != nil {
			return nil, fmt.Errorf("decode scenario for note %d: %w", scenario.NoteID, err)
		}
		scenarios = append(scenarios, scenario)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate benchmark corpus scenarios: %w", err)
	}
	return scenarios, nil
}
