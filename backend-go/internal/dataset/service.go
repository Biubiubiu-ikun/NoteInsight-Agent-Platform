package dataset

import (
	"context"
	"fmt"
)

type Freezer interface {
	Freeze(ctx context.Context, datasetID int64, createdBy int64) (Snapshot, error)
}

type Service struct {
	repo Freezer
}

func NewService(repo Freezer) *Service {
	return &Service{repo: repo}
}

func (s *Service) Freeze(ctx context.Context, datasetID int64, createdBy int64) (Snapshot, error) {
	if datasetID <= 0 {
		return Snapshot{}, fmt.Errorf("dataset_id must be positive")
	}
	if createdBy < 0 {
		return Snapshot{}, fmt.Errorf("created_by must not be negative")
	}
	return s.repo.Freeze(ctx, datasetID, createdBy)
}
