package dataset

import "errors"

var (
	ErrNotFound  = errors.New("dataset not found")
	ErrNotActive = errors.New("dataset is not active")
	ErrEmpty     = errors.New("dataset has no active evidence sources")
)
