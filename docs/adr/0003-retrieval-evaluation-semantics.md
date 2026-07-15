# ADR 0003: Retrieval Evaluation Semantics

- Status: Accepted
- Date: 2026-07-16

## Context

The previous benchmark combined distinct failure modes under “no answer” and exposed deterministic case checksums that could be reconstructed from public inputs. That risks optimistic metrics and accidental holdout tuning.

## Decision

1. `no_relevant_document` means no eligible source exists after project, dataset, visibility, and deletion filtering. It has no Gold Source.
2. `insufficient_evidence` means relevant sources exist, but they do not support the requested causal, universal, precise, or extrapolated claim. It has Gold Sources and expects abstention.
3. `authorization_boundary` is evaluated under two principals: the authorized principal must retrieve the expected project source, while the unauthorized principal must receive zero results and no source-derived hint.
4. OCR, cross-note, temporal, semantic, and typo tasks retain source-specific Gold Sources. Metrics are reported per task and split.
5. Development content may be used for implementation and tuning. Holdout content, nonces, and labels remain private and may only be evaluated by a versioned release job.
6. V4 commitments use a random per-case nonce. The public repository stores only commitment hashes for sealed cases.
7. No public quality claim is allowed until an independent reviewer adjudicates a stratified holdout sample and records agreement and corrections as a new benchmark version.

## Consequences

- Abstention and access-control failures can be diagnosed separately.
- Public artifacts prove holdout membership without revealing reconstructible case identities.
- Any holdout correction creates a new immutable benchmark; frozen or retired rows cannot be edited.
