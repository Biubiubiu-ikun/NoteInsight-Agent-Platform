package dataset

import "time"

const ManifestScheme = "evidence_source_snapshot_v1"

type SourceRef struct {
	EvidenceSourceID int64
	ProjectID        int64
	SourceType       string
	SourceID         int64
	SourceVersion    int64
	ContentHash      string
	Visibility       string
}

type Snapshot struct {
	ID               int64     `json:"id"`
	DatasetID        int64     `json:"dataset_id"`
	ProjectID        int64     `json:"project_id"`
	Version          int64     `json:"version"`
	Status           string    `json:"status"`
	SourceCount      int64     `json:"source_count"`
	ManifestChecksum string    `json:"manifest_checksum"`
	CreatedBy        int64     `json:"created_by,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	FrozenAt         time.Time `json:"frozen_at"`
	Reused           bool      `json:"reused"`
}
