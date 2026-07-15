package evalbench

import (
	"fmt"
	"os"
	"path/filepath"
)

type ArtifactStage struct {
	PublicDirectory  string
	PrivateDirectory string
	publicTarget     string
	privateTarget    string
}

func StageArtifacts(publicTarget string, privateTarget string, benchmark Benchmark) (*ArtifactStage, error) {
	if err := requireAbsentArtifactTarget(publicTarget); err != nil {
		return nil, err
	}
	if err := requireAbsentArtifactTarget(privateTarget); err != nil {
		return nil, err
	}
	publicStage, err := createArtifactStage(publicTarget)
	if err != nil {
		return nil, err
	}
	privateStage, err := createArtifactStage(privateTarget)
	if err != nil {
		_ = os.RemoveAll(publicStage)
		return nil, err
	}
	stage := &ArtifactStage{
		PublicDirectory:  publicStage,
		PrivateDirectory: privateStage,
		publicTarget:     publicTarget,
		privateTarget:    privateTarget,
	}
	cleanupOnFailure := true
	defer func() {
		if cleanupOnFailure {
			_ = stage.Cleanup()
		}
	}()

	if err := WriteArtifacts(privateStage, benchmark); err != nil {
		return nil, fmt.Errorf("stage private benchmark: %w", err)
	}
	if err := WritePublicArtifacts(publicStage, benchmark); err != nil {
		return nil, fmt.Errorf("stage public benchmark: %w", err)
	}
	if _, err := VerifyArtifacts(privateStage); err != nil {
		return nil, fmt.Errorf("verify staged private benchmark: %w", err)
	}
	if _, err := VerifyArtifacts(publicStage); err != nil {
		return nil, fmt.Errorf("verify staged public benchmark: %w", err)
	}
	cleanupOnFailure = false
	return stage, nil
}

func (s *ArtifactStage) Publish() error {
	if err := os.Rename(s.PrivateDirectory, s.privateTarget); err != nil {
		return fmt.Errorf("publish private benchmark from %s: %w", s.PrivateDirectory, err)
	}
	s.PrivateDirectory = ""
	if err := os.Rename(s.PublicDirectory, s.publicTarget); err != nil {
		return fmt.Errorf("publish public benchmark from %s: %w", s.PublicDirectory, err)
	}
	s.PublicDirectory = ""
	return nil
}

func (s *ArtifactStage) Cleanup() error {
	var firstErr error
	for _, directory := range []string{s.PublicDirectory, s.PrivateDirectory} {
		if directory == "" {
			continue
		}
		if err := os.RemoveAll(directory); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func requireAbsentArtifactTarget(target string) error {
	if target == "" {
		return fmt.Errorf("benchmark artifact target is required")
	}
	_, err := os.Stat(target)
	if err == nil {
		return fmt.Errorf("benchmark artifact target already exists: %s", target)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("inspect benchmark artifact target %s: %w", target, err)
	}
	return nil
}

func createArtifactStage(target string) (string, error) {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return "", fmt.Errorf("create benchmark artifact parent %s: %w", parent, err)
	}
	directory, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".staging-")
	if err != nil {
		return "", fmt.Errorf("create benchmark artifact stage for %s: %w", target, err)
	}
	return directory, nil
}
