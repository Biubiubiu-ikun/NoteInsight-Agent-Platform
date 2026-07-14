package simulator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type FileSink struct {
	dir             string
	profilesFile    *os.File
	eventsFile      *os.File
	profilesWriter  *bufio.Writer
	eventsWriter    *bufio.Writer
	profilesEncoder *json.Encoder
	eventsEncoder   *json.Encoder
	closed          bool
}

func NewFileSink(root string, runID string, replace bool) (*FileSink, error) {
	if root == "" {
		return nil, fmt.Errorf("output root is required")
	}
	if !validIdentifier(runID) {
		return nil, fmt.Errorf("invalid output run_id")
	}
	dir := filepath.Join(root, runID)
	if _, err := os.Stat(dir); err == nil {
		if !replace {
			return nil, fmt.Errorf("output directory %s already exists; use --replace", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("replace output directory: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect output directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	profilesFile, err := os.Create(filepath.Join(dir, "profiles.ndjson.tmp"))
	if err != nil {
		return nil, fmt.Errorf("create profiles output: %w", err)
	}
	eventsFile, err := os.Create(filepath.Join(dir, "events.ndjson.tmp"))
	if err != nil {
		_ = profilesFile.Close()
		return nil, fmt.Errorf("create events output: %w", err)
	}
	profilesWriter := bufio.NewWriterSize(profilesFile, 256*1024)
	eventsWriter := bufio.NewWriterSize(eventsFile, 1024*1024)
	return &FileSink{
		dir:             dir,
		profilesFile:    profilesFile,
		eventsFile:      eventsFile,
		profilesWriter:  profilesWriter,
		eventsWriter:    eventsWriter,
		profilesEncoder: json.NewEncoder(profilesWriter),
		eventsEncoder:   json.NewEncoder(eventsWriter),
	}, nil
}

func (s *FileSink) WriteProfiles(_ context.Context, profiles []UserProfile) error {
	for _, profile := range profiles {
		if err := s.profilesEncoder.Encode(profile); err != nil {
			return fmt.Errorf("write profile output: %w", err)
		}
	}
	return nil
}

func (s *FileSink) WriteEvent(_ context.Context, event Event) error {
	if err := s.eventsEncoder.Encode(event); err != nil {
		return fmt.Errorf("write event output: %w", err)
	}
	return nil
}

func (s *FileSink) Complete(_ context.Context, report Report) error {
	if err := s.closeDataFiles(); err != nil {
		return err
	}
	reportPath := filepath.Join(s.dir, "report.json.tmp")
	reportFile, err := os.Create(reportPath)
	if err != nil {
		return fmt.Errorf("create simulator report: %w", err)
	}
	encoder := json.NewEncoder(reportFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		_ = reportFile.Close()
		return fmt.Errorf("write simulator report: %w", err)
	}
	if err := reportFile.Close(); err != nil {
		return fmt.Errorf("close simulator report: %w", err)
	}

	for _, name := range []string{"profiles.ndjson", "events.ndjson", "report.json"} {
		if err := os.Rename(filepath.Join(s.dir, name+".tmp"), filepath.Join(s.dir, name)); err != nil {
			return fmt.Errorf("finalize %s: %w", name, err)
		}
	}
	return nil
}

func (s *FileSink) Abort(_ context.Context, cause error) {
	_ = s.closeDataFiles()
	if cause != nil {
		_ = os.WriteFile(filepath.Join(s.dir, "failure.txt"), []byte(cause.Error()+"\n"), 0o644)
	}
}

func (s *FileSink) Directory() string {
	return s.dir
}

func (s *FileSink) closeDataFiles() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.profilesWriter.Flush(); err != nil {
		return fmt.Errorf("flush profiles output: %w", err)
	}
	if err := s.eventsWriter.Flush(); err != nil {
		return fmt.Errorf("flush events output: %w", err)
	}
	if err := s.profilesFile.Close(); err != nil {
		return fmt.Errorf("close profiles output: %w", err)
	}
	if err := s.eventsFile.Close(); err != nil {
		return fmt.Errorf("close events output: %w", err)
	}
	return nil
}
