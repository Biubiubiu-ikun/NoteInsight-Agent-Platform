package dataset

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"

	"github.com/jmoiron/sqlx"
)

type Repository struct {
	db *sqlx.DB
}

func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Freeze(ctx context.Context, datasetID int64, createdBy int64) (Snapshot, error) {
	tx, err := r.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin dataset freeze: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Freezes are infrequent administrative operations. Serialize them per dataset,
	// then hold the evidence registry stable while hashing and copying its rows.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(-($1::bigint))`, datasetID); err != nil {
		return Snapshot{}, fmt.Errorf("lock dataset freeze: %w", err)
	}

	var projectID int64
	var status string
	if err := tx.QueryRowxContext(ctx, `
SELECT project_id, status
FROM datasets
WHERE id = $1
FOR UPDATE`, datasetID).Scan(&projectID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{}, fmt.Errorf("lock dataset: %w", err)
	}
	if status != "active" {
		return Snapshot{}, ErrNotActive
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE evidence_sources IN SHARE MODE`); err != nil {
		return Snapshot{}, fmt.Errorf("lock evidence registry: %w", err)
	}

	sources, err := loadActiveSources(ctx, tx, datasetID)
	if err != nil {
		return Snapshot{}, err
	}
	if len(sources) == 0 {
		return Snapshot{}, ErrEmpty
	}
	checksum := snapshotChecksum(datasetID, projectID, sources)

	latest, found, err := latestFrozenSnapshot(ctx, tx, datasetID)
	if err != nil {
		return Snapshot{}, err
	}
	if found && latest.ManifestChecksum == checksum {
		latest.Reused = true
		return latest, nil
	}

	var nextVersion int64
	if err := tx.GetContext(ctx, &nextVersion, `
SELECT COALESCE(MAX(version), 0) + 1
FROM dataset_versions
WHERE dataset_id = $1`, datasetID); err != nil {
		return Snapshot{}, fmt.Errorf("allocate dataset version: %w", err)
	}

	var snapshot Snapshot
	if err := tx.QueryRowxContext(ctx, `
INSERT INTO dataset_versions (
    dataset_id, project_id, version, status, created_by, created_at
)
VALUES ($1, $2, $3, 'building', NULLIF($4, 0), now())
RETURNING id, dataset_id, project_id, version, status,
          COALESCE(created_by, 0), created_at`,
		datasetID, projectID, nextVersion, createdBy,
	).Scan(
		&snapshot.ID,
		&snapshot.DatasetID,
		&snapshot.ProjectID,
		&snapshot.Version,
		&snapshot.Status,
		&snapshot.CreatedBy,
		&snapshot.CreatedAt,
	); err != nil {
		return Snapshot{}, fmt.Errorf("create dataset version: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
INSERT INTO dataset_version_sources (
    dataset_version_id, evidence_source_id, project_id, source_type,
    source_id, source_version, content_hash, visibility
)
SELECT $1, es.id, es.project_id, es.source_type, es.source_id, es.source_version, es.content_hash, es.visibility
FROM evidence_sources es
JOIN evidence_source_payloads esp ON esp.evidence_source_id = es.id
WHERE es.dataset_id = $2
  AND es.index_status <> 'deleted'
  AND es.deleted_at IS NULL`, snapshot.ID, datasetID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("copy dataset version sources: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Snapshot{}, fmt.Errorf("count dataset version sources: %w", err)
	}
	if inserted != int64(len(sources)) {
		return Snapshot{}, fmt.Errorf("dataset source count changed during freeze: selected %d inserted %d", len(sources), inserted)
	}

	if err := tx.QueryRowxContext(ctx, `
UPDATE dataset_versions
SET status = 'frozen',
    source_count = $2,
    manifest_checksum = $3,
    frozen_at = now()
WHERE id = $1 AND status = 'building'
RETURNING status, source_count, manifest_checksum, frozen_at`,
		snapshot.ID, inserted, checksum,
	).Scan(
		&snapshot.Status,
		&snapshot.SourceCount,
		&snapshot.ManifestChecksum,
		&snapshot.FrozenAt,
	); err != nil {
		return Snapshot{}, fmt.Errorf("publish dataset version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("commit dataset freeze: %w", err)
	}
	return snapshot, nil
}

func loadActiveSources(ctx context.Context, tx *sqlx.Tx, datasetID int64) ([]SourceRef, error) {
	rows, err := tx.QueryxContext(ctx, `
SELECT es.id, es.project_id, es.source_type, es.source_id, es.source_version, es.content_hash, es.visibility
FROM evidence_sources es
JOIN evidence_source_payloads esp ON esp.evidence_source_id = es.id
WHERE es.dataset_id = $1
  AND es.index_status <> 'deleted'
  AND es.deleted_at IS NULL
ORDER BY es.source_type, es.source_id, es.source_version, es.id`, datasetID)
	if err != nil {
		return nil, fmt.Errorf("query active dataset sources: %w", err)
	}
	defer rows.Close()

	sources := make([]SourceRef, 0)
	for rows.Next() {
		var source SourceRef
		if err := rows.Scan(
			&source.EvidenceSourceID,
			&source.ProjectID,
			&source.SourceType,
			&source.SourceID,
			&source.SourceVersion,
			&source.ContentHash,
			&source.Visibility,
		); err != nil {
			return nil, fmt.Errorf("scan active dataset source: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active dataset sources: %w", err)
	}
	return sources, nil
}

func latestFrozenSnapshot(ctx context.Context, tx *sqlx.Tx, datasetID int64) (Snapshot, bool, error) {
	var snapshot Snapshot
	err := tx.QueryRowxContext(ctx, `
SELECT id, dataset_id, project_id, version, status, source_count,
       manifest_checksum, COALESCE(created_by, 0), created_at, frozen_at
FROM dataset_versions
WHERE dataset_id = $1 AND status = 'frozen'
ORDER BY version DESC
LIMIT 1`, datasetID).Scan(
		&snapshot.ID,
		&snapshot.DatasetID,
		&snapshot.ProjectID,
		&snapshot.Version,
		&snapshot.Status,
		&snapshot.SourceCount,
		&snapshot.ManifestChecksum,
		&snapshot.CreatedBy,
		&snapshot.CreatedAt,
		&snapshot.FrozenAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("query latest dataset version: %w", err)
	}
	return snapshot, true, nil
}

func snapshotChecksum(datasetID int64, projectID int64, sources []SourceRef) string {
	hasher := sha256.New()
	writeManifestHeader(hasher, datasetID, projectID)
	for _, source := range sources {
		_, _ = fmt.Fprintf(
			hasher,
			"%d\x1f%s\x1f%d\x1f%d\x1f%s\x1f%s\n",
			source.ProjectID,
			source.SourceType,
			source.SourceID,
			source.SourceVersion,
			source.ContentHash,
			source.Visibility,
		)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func writeManifestHeader(hasher hash.Hash, datasetID int64, projectID int64) {
	_, _ = fmt.Fprintf(hasher, "%s\n%d\n%d\n", ManifestScheme, datasetID, projectID)
}
