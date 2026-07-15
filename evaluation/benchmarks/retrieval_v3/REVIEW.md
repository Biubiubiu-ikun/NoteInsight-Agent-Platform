# Retrieval V3 Review Record

Review date: 2026-07-15

## Approved Baseline

- Benchmark: `retrieval_v3_20260715`
- Version: `retrieval_v3`
- Cases: 240 unique queries and 240 unique case checksums
- Split: 80 development, 160 holdout
- Manifest SHA-256: `cb1494b76b38a23e0e20190614c104e1e7e22baa35bbb771cc340236335a3d35`
- Provenance: model-assisted independent question design over frozen source documents
- Review state: machine validated with model-assisted task-level sample review

`retrieval_v1` and `retrieval_v2` remain database audit history only. They are not approved for retrieval quality claims because earlier templates allowed repeated queries.

## Validation

The verifier rejects a changed case checksum, duplicate query, duplicate checksum, malformed JSONL, unsafe cases filename, non-frozen status, count drift, split/task/review drift, or manifest checksum drift.

All six task families contain 40 cases: semantic paraphrase, typo robustness, temporal conflict, cross-note comparison, no answer, and authorization boundary. A sample from every family was reviewed for query/answer/source alignment after v3 generation; no blocking issue remained.

## Usage Rules

- Retrieval development may read only the `development` split.
- The `holdout` split must not be used for prompt, parser, ranking, or threshold tuning.
- `development.jsonl` is the only public file containing questions, answers, and gold sources.
- `case_commitments.jsonl` preserves ordinal, split, task, review status, and SHA-256 for all 240 cases without exposing holdout content.
- The full `cases.jsonl` exists only under the Git-ignored `evaluation/private/retrieval_v3` path and in the private pre-public history bundle.
- `evalfreeze -verify-only` validates either the private full artifact or the public development-plus-commitment artifact and must reproduce the same manifest checksum.
- Report per-task and per-split metrics. Never publish only one aggregate score.
- A no-answer case has no gold source and must not be forced into a citation.
- Authorization cases must apply project/dataset/visibility filters before scoring.
- Any future correction creates a new immutable benchmark version; v3 files are never edited in place.

## Known Limits

The questions are independent from the original pipeline evaluation cases, but the source documents are still synthetic quality-corpus records. There is not yet an independent human annotation agreement score. Before public retrieval-quality claims, review a stratified holdout sample and record disagreements without tuning on the corrected holdout.
