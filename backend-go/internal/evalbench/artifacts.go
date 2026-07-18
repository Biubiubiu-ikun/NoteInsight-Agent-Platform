package evalbench

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
)

const publicArtifactScope = "public_development_with_sealed_holdout"

func WriteArtifacts(root string, benchmark Benchmark) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("create benchmark artifact directory: %w", err)
	}
	if err := writeJSONLines(filepath.Join(root, "cases.jsonl"), benchmark.Cases); err != nil {
		return err
	}
	return writeManifest(filepath.Join(root, "manifest.json"), benchmark.Manifest)
}

func WritePublicArtifacts(root string, benchmark Benchmark) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("create public benchmark artifact directory: %w", err)
	}

	development := make([]Case, 0, benchmark.Manifest.SplitCounts["development"])
	commitments := make([]CaseCommitment, 0, len(benchmark.Cases))
	for index, evalCase := range benchmark.Cases {
		if evalCase.Split == "development" {
			development = append(development, evalCase)
		}
		commitment := CaseCommitment{
			Ordinal:      index + 1,
			Split:        evalCase.Split,
			TaskType:     evalCase.TaskType,
			ReviewStatus: evalCase.ReviewStatus,
		}
		if normalizedCommitmentScheme(benchmark.Manifest.CommitmentScheme) == NonceCommitmentScheme {
			commitment.CommitmentHash = evalCase.CommitmentHash
		} else {
			commitment.CaseChecksum = evalCase.CaseChecksum
		}
		commitments = append(commitments, commitment)
	}

	publicManifest := benchmark.Manifest
	publicManifest.ArtifactScope = publicArtifactScope
	publicManifest.CasesFile = ""
	publicManifest.DevelopmentFile = "development.jsonl"
	publicManifest.CommitmentsFile = "case_commitments.jsonl"
	if err := writeJSONLines(filepath.Join(root, publicManifest.DevelopmentFile), development); err != nil {
		return err
	}
	if err := writeJSONLines(filepath.Join(root, publicManifest.CommitmentsFile), commitments); err != nil {
		return err
	}
	return writeManifest(filepath.Join(root, "manifest.json"), publicManifest)
}

func VerifyArtifacts(root string) (Manifest, error) {
	manifestRaw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return Manifest{}, fmt.Errorf("read benchmark manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode benchmark manifest: %w", err)
	}
	if manifest.Status != "frozen" {
		return Manifest{}, fmt.Errorf("benchmark status must be frozen, got %q", manifest.Status)
	}
	scheme := normalizedCommitmentScheme(manifest.CommitmentScheme)
	if scheme != LegacyCommitmentScheme && scheme != NonceCommitmentScheme {
		return Manifest{}, fmt.Errorf("unsupported commitment scheme %q", manifest.CommitmentScheme)
	}
	if scheme == NonceCommitmentScheme && (manifest.DatasetVersionID <= 0 || manifest.ApprovalStatus != "approved") {
		return Manifest{}, fmt.Errorf("nonce-committed benchmark requires a dataset version and approved status")
	}
	if manifest.CasesFile != "" {
		return verifyFullArtifacts(root, manifest)
	}
	return verifyPublicArtifacts(root, manifest)
}

// ReadVerifiedCases validates immutable case checksums and optional nonce commitments.
// Holdout callers must separately prove membership with VerifySealedCases.
func ReadVerifiedCases(path string, requiredSplit string) ([]Case, error) {
	return readVerifiedCases(path, requiredSplit)
}

// VerifySealedCases proves that private holdout cases match the public nonce commitments
// without making the case contents part of the public artifact set.
func VerifySealedCases(publicRoot string, cases []Case) (Manifest, error) {
	manifest, err := VerifyArtifacts(publicRoot)
	if err != nil {
		return Manifest{}, err
	}
	if normalizedCommitmentScheme(manifest.CommitmentScheme) != NonceCommitmentScheme {
		return Manifest{}, fmt.Errorf("sealed case verification requires nonce commitments")
	}
	file, err := os.Open(filepath.Join(publicRoot, manifest.CommitmentsFile))
	if err != nil {
		return Manifest{}, fmt.Errorf("open benchmark commitments: %w", err)
	}
	defer file.Close()
	sealed := make(map[string]struct{}, manifest.SplitCounts["holdout"])
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var commitment CaseCommitment
		if err := json.Unmarshal(scanner.Bytes(), &commitment); err != nil {
			return Manifest{}, fmt.Errorf("decode benchmark commitment: %w", err)
		}
		if commitment.Split == "holdout" {
			sealed[commitment.CommitmentHash] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return Manifest{}, fmt.Errorf("scan benchmark commitments: %w", err)
	}
	if len(cases) != manifest.SplitCounts["holdout"] {
		return Manifest{}, fmt.Errorf("holdout case count %d does not match sealed count %d", len(cases), manifest.SplitCounts["holdout"])
	}
	seen := make(map[string]struct{}, len(cases))
	for index, evalCase := range cases {
		if evalCase.Split != "holdout" || evalCase.CommitmentNonce == "" {
			return Manifest{}, fmt.Errorf("private case %d is not a nonce-committed holdout case", index+1)
		}
		expected := commitmentHash(evalCase.CommitmentNonce, evalCase.CaseChecksum)
		if expected != evalCase.CommitmentHash {
			return Manifest{}, fmt.Errorf("private case %d commitment is invalid", index+1)
		}
		if _, exists := sealed[expected]; !exists {
			return Manifest{}, fmt.Errorf("private case %d is not in the public sealed commitments", index+1)
		}
		if _, duplicate := seen[expected]; duplicate {
			return Manifest{}, fmt.Errorf("duplicate private holdout commitment at case %d", index+1)
		}
		seen[expected] = struct{}{}
	}
	return manifest, nil
}

func verifyFullArtifacts(root string, manifest Manifest) (Manifest, error) {
	if !validLocalFilename(manifest.CasesFile) {
		return Manifest{}, fmt.Errorf("cases_file must be a local filename")
	}
	cases, err := readVerifiedCases(filepath.Join(root, manifest.CasesFile), "")
	if err != nil {
		return Manifest{}, err
	}

	rebuilt := buildManifest(Config{
		BenchmarkID:      manifest.BenchmarkID,
		BenchmarkVersion: manifest.BenchmarkVersion,
		SourceRunID:      manifest.SourceRunID,
		GeneratorVersion: manifest.GeneratorVersion,
		Seed:             manifest.Seed,
		DatasetVersionID: manifest.DatasetVersionID,
		CommitmentScheme: normalizedCommitmentScheme(manifest.CommitmentScheme),
	}, cases)
	if err := compareManifest(rebuilt, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func verifyPublicArtifacts(root string, manifest Manifest) (Manifest, error) {
	if manifest.ArtifactScope != publicArtifactScope {
		return Manifest{}, fmt.Errorf("unsupported public artifact scope %q", manifest.ArtifactScope)
	}
	if !validLocalFilename(manifest.DevelopmentFile) || !validLocalFilename(manifest.CommitmentsFile) || manifest.DevelopmentFile == manifest.CommitmentsFile {
		return Manifest{}, fmt.Errorf("development_file and commitments_file must be distinct local filenames")
	}

	development, err := readVerifiedCases(filepath.Join(root, manifest.DevelopmentFile), "development")
	if err != nil {
		return Manifest{}, err
	}
	scheme := normalizedCommitmentScheme(manifest.CommitmentScheme)
	developmentByCommitment := make(map[string]struct{}, len(development))
	for _, evalCase := range development {
		value := evalCase.CaseChecksum
		if scheme == NonceCommitmentScheme {
			if evalCase.CommitmentNonce == "" || evalCase.CommitmentHash == "" || evalCase.CommitmentHash != commitmentHash(evalCase.CommitmentNonce, evalCase.CaseChecksum) {
				return Manifest{}, fmt.Errorf("public development case has invalid nonce commitment")
			}
			value = evalCase.CommitmentHash
		}
		developmentByCommitment[value] = struct{}{}
	}

	file, err := os.Open(filepath.Join(root, manifest.CommitmentsFile))
	if err != nil {
		return Manifest{}, fmt.Errorf("open benchmark commitments: %w", err)
	}
	defer file.Close()

	commitments := make([]CaseCommitment, 0, manifest.CaseCount)
	seenCommitments := make(map[string]struct{}, manifest.CaseCount)
	matchedDevelopment := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		var commitment CaseCommitment
		if err := json.Unmarshal(scanner.Bytes(), &commitment); err != nil {
			return Manifest{}, fmt.Errorf("decode benchmark commitment line %d: %w", line, err)
		}
		if commitment.Ordinal != line {
			return Manifest{}, fmt.Errorf("commitment ordinal mismatch at line %d", line)
		}
		if commitment.Split != "development" && commitment.Split != "holdout" {
			return Manifest{}, fmt.Errorf("invalid commitment split at line %d", line)
		}
		value := commitment.CaseChecksum
		if scheme == NonceCommitmentScheme {
			value = commitment.CommitmentHash
			if commitment.CaseChecksum != "" {
				return Manifest{}, fmt.Errorf("nonce commitment line %d must not expose a case checksum", line)
			}
		} else if commitment.CommitmentHash != "" {
			return Manifest{}, fmt.Errorf("legacy commitment line %d must not contain commitment_hash", line)
		}
		if commitment.TaskType == "" || commitment.ReviewStatus == "" || !validSHA256(value) {
			return Manifest{}, fmt.Errorf("incomplete commitment at line %d", line)
		}
		if _, duplicate := seenCommitments[value]; duplicate {
			return Manifest{}, fmt.Errorf("duplicate commitment checksum at line %d", line)
		}
		_, isPublic := developmentByCommitment[value]
		if commitment.Split == "development" && !isPublic {
			return Manifest{}, fmt.Errorf("development commitment at line %d has no public case", line)
		}
		if commitment.Split == "holdout" && isPublic {
			return Manifest{}, fmt.Errorf("holdout commitment at line %d exposes a public case", line)
		}
		if isPublic {
			matchedDevelopment++
		}
		seenCommitments[value] = struct{}{}
		commitments = append(commitments, commitment)
	}
	if err := scanner.Err(); err != nil {
		return Manifest{}, fmt.Errorf("scan benchmark commitments: %w", err)
	}
	if len(commitments) != manifest.CaseCount {
		return Manifest{}, fmt.Errorf("commitment count %d does not match manifest case count %d", len(commitments), manifest.CaseCount)
	}
	if matchedDevelopment != len(development) || len(development) != manifest.SplitCounts["development"] {
		return Manifest{}, fmt.Errorf("public development cases do not match manifest commitments")
	}

	rebuilt := buildManifestFromCommitments(manifest, commitments)
	if err := compareManifest(rebuilt, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func readVerifiedCases(path string, requiredSplit string) ([]Case, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open benchmark cases: %w", err)
	}
	defer file.Close()

	cases := make([]Case, 0)
	checksums := make(map[string]struct{})
	queries := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		var evalCase Case
		if err := json.Unmarshal(scanner.Bytes(), &evalCase); err != nil {
			return nil, fmt.Errorf("decode benchmark case line %d: %w", line, err)
		}
		if requiredSplit != "" && evalCase.Split != requiredSplit {
			return nil, fmt.Errorf("benchmark case line %d has split %q, want %q", line, evalCase.Split, requiredSplit)
		}
		if actual := checksumCase(evalCase); actual != evalCase.CaseChecksum {
			return nil, fmt.Errorf("case checksum mismatch at line %d", line)
		}
		if evalCase.CommitmentNonce != "" || evalCase.CommitmentHash != "" {
			if evalCase.CommitmentNonce == "" || !validSHA256(evalCase.CommitmentHash) || evalCase.CommitmentHash != commitmentHash(evalCase.CommitmentNonce, evalCase.CaseChecksum) {
				return nil, fmt.Errorf("case commitment mismatch at line %d", line)
			}
		}
		if _, exists := checksums[evalCase.CaseChecksum]; exists {
			return nil, fmt.Errorf("duplicate case checksum at line %d", line)
		}
		if _, exists := queries[evalCase.Query]; exists {
			return nil, fmt.Errorf("duplicate benchmark query at line %d", line)
		}
		checksums[evalCase.CaseChecksum] = struct{}{}
		queries[evalCase.Query] = struct{}{}
		cases = append(cases, evalCase)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan benchmark cases: %w", err)
	}
	return cases, nil
}

func buildManifestFromCommitments(source Manifest, commitments []CaseCommitment) Manifest {
	splits := map[string]int{}
	tasks := map[string]int{}
	reviews := map[string]int{}
	hasher := sha256.New()
	scheme := normalizedCommitmentScheme(source.CommitmentScheme)
	writeManifestIdentity(hasher, Config{
		BenchmarkID:      source.BenchmarkID,
		BenchmarkVersion: source.BenchmarkVersion,
		SourceRunID:      source.SourceRunID,
		GeneratorVersion: source.GeneratorVersion,
		Seed:             source.Seed,
		DatasetVersionID: source.DatasetVersionID,
		CommitmentScheme: scheme,
	})
	for _, commitment := range commitments {
		splits[commitment.Split]++
		tasks[commitment.TaskType]++
		reviews[commitment.ReviewStatus]++
		value := commitment.CaseChecksum
		if scheme == NonceCommitmentScheme {
			value = commitment.CommitmentHash
		}
		_, _ = fmt.Fprintln(hasher, value)
	}
	return Manifest{
		BenchmarkID:      source.BenchmarkID,
		BenchmarkVersion: source.BenchmarkVersion,
		SourceRunID:      source.SourceRunID,
		GeneratorVersion: source.GeneratorVersion,
		Seed:             source.Seed,
		Status:           "frozen",
		CaseCount:        len(commitments),
		SplitCounts:      splits,
		TaskCounts:       tasks,
		ReviewCounts:     reviews,
		ManifestChecksum: hex.EncodeToString(hasher.Sum(nil)),
		DatasetVersionID: source.DatasetVersionID,
		CommitmentScheme: scheme,
		ApprovalStatus:   source.ApprovalStatus,
	}
}

func compareManifest(rebuilt Manifest, expected Manifest) error {
	if rebuilt.ManifestChecksum != expected.ManifestChecksum {
		return fmt.Errorf("manifest checksum mismatch: got %s, want %s", rebuilt.ManifestChecksum, expected.ManifestChecksum)
	}
	if rebuilt.CaseCount != expected.CaseCount ||
		!maps.Equal(rebuilt.SplitCounts, expected.SplitCounts) ||
		!maps.Equal(rebuilt.TaskCounts, expected.TaskCounts) ||
		!maps.Equal(rebuilt.ReviewCounts, expected.ReviewCounts) {
		return fmt.Errorf("manifest counts do not match benchmark artifacts")
	}
	return nil
}

func writeManifest(path string, manifest Manifest) error {
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode benchmark manifest: %w", err)
	}
	return writeAtomic(path, append(raw, '\n'))
}

func writeJSONLines[T any](path string, values []T) error {
	temporary := path + ".tmp"
	file, err := os.Create(temporary)
	if err != nil {
		return fmt.Errorf("create benchmark JSONL artifact: %w", err)
	}
	writer := bufio.NewWriter(file)
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			_ = file.Close()
			_ = os.Remove(temporary)
			return fmt.Errorf("encode benchmark JSONL value: %w", err)
		}
		if _, err := writer.Write(append(raw, '\n')); err != nil {
			_ = file.Close()
			_ = os.Remove(temporary)
			return fmt.Errorf("write benchmark JSONL value: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return fmt.Errorf("flush benchmark JSONL artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("close benchmark JSONL artifact: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish benchmark JSONL artifact: %w", err)
	}
	return nil
}

func validLocalFilename(value string) bool {
	return value != "" && filepath.Base(value) == value
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func normalizedCommitmentScheme(value string) string {
	if value == "" {
		return LegacyCommitmentScheme
	}
	return value
}

func writeAtomic(path string, contents []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0644); err != nil {
		return fmt.Errorf("write benchmark artifact: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish benchmark artifact: %w", err)
	}
	return nil
}
