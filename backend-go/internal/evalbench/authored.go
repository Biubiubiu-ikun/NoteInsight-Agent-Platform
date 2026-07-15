package evalbench

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func ReadAuthoredCases(path string) ([]Case, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open authored benchmark input: %w", err)
	}
	defer file.Close()

	cases := make([]Case, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		var evalCase Case
		if err := json.Unmarshal(scanner.Bytes(), &evalCase); err != nil {
			return nil, fmt.Errorf("decode authored benchmark line %d: %w", line, err)
		}
		evalCase.CaseChecksum = ""
		evalCase.CommitmentHash = ""
		cases = append(cases, evalCase)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan authored benchmark input: %w", err)
	}
	return cases, nil
}

func FreezeAuthored(config Config, authored []Case) (Benchmark, error) {
	config.Normalize()
	if config.GeneratorVersion == DefaultGeneratorVersion {
		config.GeneratorVersion = AuthoredGeneratorVersion
	}
	config.CommitmentScheme = NonceCommitmentScheme
	if err := config.Validate(); err != nil {
		return Benchmark{}, err
	}
	if len(authored) != config.CaseCount {
		return Benchmark{}, fmt.Errorf("authored case count %d does not match configured count %d", len(authored), config.CaseCount)
	}

	cases := make([]Case, 0, len(authored))
	seenQueries := make(map[string]struct{}, len(authored))
	seenChecksums := make(map[string]struct{}, len(authored))
	seenCommitments := make(map[string]struct{}, len(authored))
	developmentCount := 0
	for index, source := range authored {
		evalCase := source
		evalCase.Split = strings.TrimSpace(evalCase.Split)
		evalCase.TaskType = strings.TrimSpace(evalCase.TaskType)
		evalCase.Query = strings.TrimSpace(evalCase.Query)
		evalCase.ExpectedAnswer = strings.TrimSpace(evalCase.ExpectedAnswer)
		if evalCase.Split == "development" {
			developmentCount++
		} else if evalCase.Split != "holdout" {
			return Benchmark{}, fmt.Errorf("authored case %d has invalid split %q", index+1, evalCase.Split)
		}
		if evalCase.Query == "" || evalCase.ExpectedAnswer == "" || evalCase.TaskType == "" {
			return Benchmark{}, fmt.Errorf("authored case %d requires task_type, query, and expected_answer", index+1)
		}
		if _, duplicate := seenQueries[evalCase.Query]; duplicate {
			return Benchmark{}, fmt.Errorf("authored case %d duplicates a query", index+1)
		}
		if err := validateAuthoredSemantics(index+1, evalCase); err != nil {
			return Benchmark{}, err
		}

		if evalCase.Metadata == nil {
			evalCase.Metadata = map[string]any{}
		}
		evalCase.Metadata["benchmark_version"] = config.BenchmarkVersion
		evalCase.Metadata["generator_version"] = config.GeneratorVersion
		evalCase.Metadata["source_run_id"] = config.SourceRunID
		evalCase.Metadata["dataset_version_id"] = config.DatasetVersionID
		if strings.TrimSpace(evalCase.Provenance) == "" {
			evalCase.Provenance = AuthoredProvenance
		}
		if strings.TrimSpace(evalCase.ReviewStatus) == "" {
			evalCase.ReviewStatus = "machine_validated"
		}
		if evalCase.ReviewStatus != "machine_validated" && evalCase.ReviewStatus != "human_approved" {
			return Benchmark{}, fmt.Errorf("authored case %d has invalid review_status %q", index+1, evalCase.ReviewStatus)
		}

		nonce, err := normalizeCommitmentNonce(evalCase.CommitmentNonce)
		if err != nil {
			return Benchmark{}, fmt.Errorf("authored case %d commitment nonce: %w", index+1, err)
		}
		evalCase.CommitmentNonce = nonce
		evalCase.CaseChecksum = checksumCase(evalCase)
		evalCase.CommitmentHash = commitmentHash(nonce, evalCase.CaseChecksum)
		if _, duplicate := seenChecksums[evalCase.CaseChecksum]; duplicate {
			return Benchmark{}, fmt.Errorf("authored case %d duplicates a case checksum", index+1)
		}
		if _, duplicate := seenCommitments[evalCase.CommitmentHash]; duplicate {
			return Benchmark{}, fmt.Errorf("authored case %d duplicates a commitment", index+1)
		}
		seenQueries[evalCase.Query] = struct{}{}
		seenChecksums[evalCase.CaseChecksum] = struct{}{}
		seenCommitments[evalCase.CommitmentHash] = struct{}{}
		cases = append(cases, evalCase)
	}
	if developmentCount != config.DevelopmentCases {
		return Benchmark{}, fmt.Errorf("development case count %d does not match configured count %d", developmentCount, config.DevelopmentCases)
	}

	manifest := buildManifest(config, cases)
	return Benchmark{Config: config, Cases: cases, Manifest: manifest}, nil
}

func validateAuthoredSemantics(ordinal int, evalCase Case) error {
	for _, source := range evalCase.GoldSources {
		if source.NoteID <= 0 {
			return fmt.Errorf("authored case %d has a non-positive gold note id", ordinal)
		}
		if source.SourceType != "note" && source.SourceType != "note_body" && source.SourceType != "note_media" && source.SourceType != "note_comment" {
			return fmt.Errorf("authored case %d has unsupported gold source type %q", ordinal, source.SourceType)
		}
	}
	answerable, hasAnswerable := metadataBool(evalCase.Metadata, "answerable")
	switch evalCase.TaskType {
	case "no_relevant_document":
		if len(evalCase.GoldSources) != 0 || !hasAnswerable || answerable {
			return fmt.Errorf("authored case %d no_relevant_document requires no gold sources and answerable=false", ordinal)
		}
	case "insufficient_evidence":
		if len(evalCase.GoldSources) == 0 || !hasAnswerable || answerable {
			return fmt.Errorf("authored case %d insufficient_evidence requires gold sources and answerable=false", ordinal)
		}
	case "authorization_boundary":
		if len(evalCase.GoldSources) == 0 || !metadataPositiveNumber(evalCase.Metadata, "required_project_id") || !metadataPositiveNumber(evalCase.Metadata, "authorized_expected_results") || !metadataZero(evalCase.Metadata, "unauthorized_expected_results") {
			return fmt.Errorf("authored case %d authorization_boundary requires a gold source and dual-principal metadata", ordinal)
		}
	case "ocr_detail":
		if !hasSourceType(evalCase.GoldSources, "note_media") {
			return fmt.Errorf("authored case %d ocr_detail requires a note_media gold source", ordinal)
		}
	default:
		if len(evalCase.GoldSources) == 0 {
			return fmt.Errorf("authored case %d task %q requires at least one gold source", ordinal, evalCase.TaskType)
		}
		if hasAnswerable && !answerable {
			return fmt.Errorf("authored case %d task %q must not set answerable=false", ordinal, evalCase.TaskType)
		}
	}
	return nil
}

func hasSourceType(sources []GoldSource, target string) bool {
	for _, source := range sources {
		if source.SourceType == target {
			return true
		}
	}
	return false
}

func normalizeCommitmentNonce(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return "", fmt.Errorf("generate random nonce: %w", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw), nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < 16 {
		return "", fmt.Errorf("must be base64url without padding and contain at least 16 random bytes")
	}
	return value, nil
}

func commitmentHash(nonce string, caseChecksum string) string {
	digest := sha256.Sum256([]byte(nonce + "\x1f" + caseChecksum))
	return hex.EncodeToString(digest[:])
}

func metadataBool(metadata map[string]any, key string) (bool, bool) {
	if metadata == nil {
		return false, false
	}
	value, ok := metadata[key].(bool)
	return value, ok
}

func metadataPositiveNumber(metadata map[string]any, key string) bool {
	value, ok := metadata[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case float64:
		return typed > 0
	case int:
		return typed > 0
	case int64:
		return typed > 0
	default:
		return false
	}
}

func metadataZero(metadata map[string]any, key string) bool {
	value, ok := metadata[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case float64:
		return typed == 0
	case int:
		return typed == 0
	case int64:
		return typed == 0
	default:
		return false
	}
}
