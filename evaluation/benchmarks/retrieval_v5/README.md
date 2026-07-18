# Retrieval Benchmark v5

This directory is the public scaffold for the independently reviewed Phase 7D benchmark. It intentionally contains no public cases or human-review claims yet. The Git-ignored D-drive workspace now contains model-assisted drafts and blind assignments, but zero human submissions.

Benchmark v4 remains the reproducible pipeline baseline, but it is not sufficient for a production quality claim because all 240 cases are machine-validated and several task families contain equivalent near-duplicate evidence with a single Gold source.

Before adding `manifest.json`, the review process must satisfy [the independent review protocol](../../../docs/19_phase7d_benchmark_v5_review.md). Holdout content stays outside Git; only nonce commitments and aggregate review evidence may be published.

The executable workflow is implemented by `cmd/benchmarkreview` and `scripts/review_retrieval_benchmark.ps1`:

1. `init` creates the deterministic 288-slot private matrix.
2. `draft` reproducibly writes model-assisted, unapproved cases from frozen canonical payloads without reading system-under-test rankings or evaluation output.
3. `prepare` resolves every candidate from the frozen Evidence Store and emits separate blind assignments for two reviewers.
4. `serve` opens a loopback-only review UI with atomic progress persistence and immutable finalization.
5. `audit` validates independent submissions, computes overall and per-task agreement, and emits the adjudication queue.
6. `freeze` is rejected until all 288 cases have two reviews, independent third-party adjudication, valid final semantics, and passing agreement gates.

The schemas are contracts, not review evidence:

- `authoring.schema.json`: private authored case and candidate references.
- `assignment.schema.json`: blind assignment with canonical frozen evidence; it deliberately excludes author identity and expected answer.
- `submission.schema.json`: one independent reviewer submission.
- `adjudication.schema.json`: one third-party resolution.
- `review.schema.json`: combined private ledger record.

Only a successful freeze may add `manifest.json`, `development.jsonl`, `case_commitments.jsonl`, and aggregate `review_summary.json`. The full holdout, reviewer identity mapping, assignments, submissions, and ledger stay under the Git-ignored private workspace.
