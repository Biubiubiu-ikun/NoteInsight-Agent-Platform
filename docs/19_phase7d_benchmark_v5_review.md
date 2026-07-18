# Phase 7D Benchmark v5 Independent Review Protocol

Updated: 2026-07-19

## Purpose

Benchmark v5 is the quality contract for retrieval tuning after the v4 baselines failed their Recall/MRR gates. It must measure retrieval quality independently of the corpus generator and must not turn the sealed holdout into tuning data.

The generator may assist with drafts, but it cannot approve cases, assign final relevance, or adjudicate disagreements. Human review evidence is required before the benchmark status can become `frozen`.

## Target Matrix

The target is 288 cases: 32 cases in each of nine task strata, split evenly between public development and sealed holdout.

| Task stratum | Development | Holdout | Primary risk |
| --- | ---: | ---: | --- |
| semantic paraphrase | 16 | 16 | vocabulary and phrasing shift |
| typo robustness | 16 | 16 | misspellings and input noise |
| temporal conflict | 16 | 16 | stale evidence outranking current evidence |
| cross-note comparison | 16 | 16 | evidence aggregation across notes |
| no relevant document | 16 | 16 | false-positive answer generation |
| insufficient evidence | 16 | 16 | evidence exists but cannot support the answer |
| OCR detail | 16 | 16 | media-caption and OCR source grounding |
| authorization boundary | 16 | 16 | private evidence leakage across principals |
| out-of-domain noise | 16 | 16 | gibberish, random strings, and domain shift |

Each answerable case must have a relevance pool that includes every known equivalent source, not an arbitrary single Gold. Relevance uses four grades: `0` irrelevant, `1` topically related but insufficient, `2` sufficient supporting evidence, and `3` direct or canonical evidence.

## Independence and Roles

1. An author drafts the query, answerability label, expected answer, and candidate pool without seeing retrieval output from the system under test.
2. Reviewer A and Reviewer B independently label answerability and relevance using the frozen rubric and source snapshot.
3. An adjudicator resolves every disagreement. The adjudicator must not be the case author.
4. Reviewer identities may be stable pseudonyms, but the mapping and conflict-of-interest record must be retained privately.
5. Development cases may be disclosed only after adjudication. Holdout query text, answers, nonces, and qrels remain private; the repository contains commitments only.

Model assistance must be recorded in `draft_assistance`. A model-assisted draft is not human review.

## Acceptance Gates

- 100% of cases have two independent reviews and adjudication status `resolved`.
- Cohen's kappa for binary answerability is at least `0.80` before adjudication.
- Quadratic weighted kappa for relevance grades is at least `0.70` before adjudication.
- Every answerable case has at least one grade-2-or-3 source.
- `no_relevant_document` and `out_of_domain_noise` have no grade-2-or-3 source.
- Authorization cases define both an allowed and denied principal; denied retrieval must expose zero result, citation, collection, count, or timing-derived index identity.
- Every source belongs to the frozen dataset and ingestion snapshot.
- No duplicate normalized query appears across development and holdout.
- Manifest, development file, commitment file, review ledger, and rubric each have a SHA-256 checksum.

Failure of an agreement gate sends the affected stratum back to rubric clarification and a fresh independent review. It must not be fixed by silently changing labels after inspecting model results.

## Artifacts

The private authoring workspace contains:

```text
retrieval_v5_private/
  review_plan.json
  authoring_matrix.jsonl
  authored_cases.template.jsonl
  authored_cases.jsonl
  resolved_sources.jsonl
  reviewer_a/assignments.jsonl
  reviewer_a/submissions.jsonl
  reviewer_b/assignments.jsonl
  reviewer_b/submissions.jsonl
  adjudications.jsonl
  adjudication_queue.jsonl
  review_ledger.jsonl
  review_summary.private.json
  approved_cases.jsonl
  reviewer_identity_map.enc
```

The public repository contains only:

```text
evaluation/benchmarks/retrieval_v5/
  README.md
  review.schema.json
  rubric.md
  manifest.json                 # added only after freeze
  development.jsonl            # added only after freeze
  case_commitments.jsonl        # added only after freeze
  review_summary.json           # aggregate agreement, no holdout labels
```

The JSON Schema in this directory validates the per-case review ledger. The freeze command must reject `machine_validated` cases and any case without two distinct reviewers and a resolved adjudication.

## Executable Workflow

The engineering workflow is complete; this does not mean the human review is complete.

```powershell
# One-time deterministic 288-slot matrix initialization.
.\scripts\review_retrieval_benchmark.ps1 -Operation init

# After authors fill authored_cases.jsonl, resolve frozen evidence and create blind assignments.
.\scripts\review_retrieval_benchmark.ps1 -Operation prepare `
  -ReviewerA reviewer-a-pseudonym `
  -ReviewerB reviewer-b-pseudonym

# After each reviewer independently creates submissions.jsonl.
.\scripts\review_retrieval_benchmark.ps1 -Operation audit

# Only after an independent adjudicator completes adjudications.jsonl.
.\scripts\review_retrieval_benchmark.ps1 -Operation freeze
```

`prepare` queries dataset version `2` and ingestion run `phase7a_dv2_rebuild_v2_20260718`. Every candidate must be present in both the immutable dataset membership and the completed ingestion citation graph. Assignments contain canonical source text and content hash, but omit the author's identity and expected answer. They never contain retrieval rank or model score.

`audit` enforces exactly one submission per case from each of two stable, distinct reviewer pseudonyms; neither reviewer may be the author. Every candidate must receive one grade from each reviewer. It reports exact agreement, binary answerability Cohen's kappa, and quadratic weighted relevance kappa overall and by task. Every case then requires a resolved adjudication from a third identity that is neither author nor reviewer.

`freeze` fails closed unless all matrix, independence, adjudication, source-membership, semantic, overall agreement, and per-task agreement gates pass. It then writes `human_approved` cases for the existing `evalfreeze` pipeline and an aggregate public summary. It does not publish holdout text, answer, source labels, nonce, assignments, submissions, or reviewer identities.

The private workspace is Git-ignored and physically stored inside the D-drive project directory. Back it up as a private encrypted artifact before human work begins; do not move it into the public repository.

## Work Estimate and Order

At 1.5 to 3 minutes per case per reviewer, 288 cases require about 14.4 to 28.8 reviewer-hours before adjudication. The deterministic matrix and review tooling are ready, but authorship, two human reviews, and adjudication have not started. This is an external evidence task and should start in parallel with load/security hardening. Ranker thresholds and a cross-encoder must wait until the development split is frozen; the v4 holdout remains sealed throughout.

## Promotion Rule

A retrieval implementation is promotable only when it beats the strongest frozen baseline on v5 overall and does not regress authorization non-leakage, citation integrity, no-answer rejection, or any safety-critical task stratum. Report per-task deltas, p50/p95/p99 latency, embedding/reranker calls, hardware, model revision, and cost. A quality claim without the review summary and checksums is invalid.
