# ADR 0001: Evidence Versioning And Dataset Snapshots

- Status: Accepted
- Date: 2026-07-16

## Context

Retrieval evaluation is not reproducible when mutable notes, OCR, or comments overwrite the source identity used by an experiment. A dataset membership table alone records intent, not the exact source versions and hashes observed by a run.

## Decision

1. Every meaningful note, media, or comment mutation increments a database-controlled content version.
2. Evidence Source rows are append-only by `(project_id, source_type, source_id, source_version)`. Superseded rows remain auditable but become ineligible for active retrieval.
3. `dataset_versions` represents a published point-in-time manifest. `dataset_version_sources` copies source identity, version, hash, project, visibility, and registry reference.
4. A frozen snapshot and its source rows are immutable. A frozen dataset also rejects membership changes.
5. Snapshot checksums use logical source identity and exclude the internal Evidence Source registry row ID.
6. Dataset freezes are serialized per dataset. The rare administrative freeze holds a short PostgreSQL `SHARE` lock over the registry while hashing and copying to prevent a mixed-time manifest.

## Consequences

- Retrieval and evaluation runs can cite one immutable dataset version.
- Updating source content creates a new dataset version without rewriting old experiments.
- Freeze time grows linearly with active source count and briefly delays Evidence Source writes; it is not an online request operation.
- Hard deletion policy must reconcile legal erasure with immutable experiment audit records before production launch.
