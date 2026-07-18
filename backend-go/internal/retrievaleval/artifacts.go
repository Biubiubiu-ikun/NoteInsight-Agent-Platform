package retrievaleval

import (
	"path/filepath"

	"creatorinsight/backend-go/internal/evalbench"
)

func LoadCases(config Config) (evalbench.Manifest, []evalbench.Case, error) {
	if config.Split == "development" {
		manifest, err := evalbench.VerifyArtifacts(config.BenchmarkDirectory)
		if err != nil {
			return evalbench.Manifest{}, nil, err
		}
		cases, err := evalbench.ReadVerifiedCases(filepath.Join(config.BenchmarkDirectory, manifest.DevelopmentFile), "development")
		return manifest, cases, err
	}
	cases, err := evalbench.ReadVerifiedCases(config.InputFile, "holdout")
	if err != nil {
		return evalbench.Manifest{}, nil, err
	}
	manifest, err := evalbench.VerifySealedCases(config.BenchmarkDirectory, cases)
	return manifest, cases, err
}
