package evalreview

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func ReadJSONLines[T any](path string) ([]T, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open JSONL %s: %w", path, err)
	}
	defer file.Close()

	values := make([]T, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		var value T
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			return nil, fmt.Errorf("decode JSONL %s line %d: %w", path, line, err)
		}
		values = append(values, value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan JSONL %s: %w", path, err)
	}
	return values, nil
}

func WriteJSONLines[T any](path string, values []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create JSONL parent: %w", err)
	}
	temporary := path + ".tmp"
	file, err := os.Create(temporary)
	if err != nil {
		return fmt.Errorf("create JSONL %s: %w", path, err)
	}
	writer := bufio.NewWriter(file)
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(temporary)
	}
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			cleanup()
			return fmt.Errorf("encode JSONL %s: %w", path, err)
		}
		if _, err := writer.Write(append(raw, '\n')); err != nil {
			cleanup()
			return fmt.Errorf("write JSONL %s: %w", path, err)
		}
	}
	if err := writer.Flush(); err != nil {
		cleanup()
		return fmt.Errorf("flush JSONL %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("close JSONL %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish JSONL %s: %w", path, err)
	}
	return nil
}

func WriteJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create JSON parent: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(raw, '\n'), 0644); err != nil {
		return fmt.Errorf("write JSON %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish JSON %s: %w", path, err)
	}
	return nil
}

func FileChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open checksum input %s: %w", path, err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := file.WriteTo(hasher); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func valueChecksum(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
