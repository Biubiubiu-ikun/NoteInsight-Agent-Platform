# Retrieval Benchmark v5

This directory is the public scaffold for the independently reviewed Phase 7D benchmark. It intentionally contains no cases yet.

Benchmark v4 remains the reproducible pipeline baseline, but it is not sufficient for a production quality claim because all 240 cases are machine-validated and several task families contain equivalent near-duplicate evidence with a single Gold source.

Before adding `manifest.json`, the review process must satisfy [the independent review protocol](../../../docs/19_phase7d_benchmark_v5_review.md). Holdout content stays outside Git; only nonce commitments and aggregate review evidence may be published.

`review.schema.json` defines one adjudicated review record. It is a contract for the review tooling, not evidence that review has already happened.
