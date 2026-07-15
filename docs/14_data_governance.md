# Data Governance Baseline

Updated: 2026-07-16

## Scope And Ownership

- Every note belongs to one `project`; project membership is the authorization boundary.
- Every project owns a default `community` dataset. `dataset_notes` is mutable current membership; `dataset_versions` and `dataset_version_sources` preserve immutable experiment snapshots.
- `evidence_sources` is the source registry for notes, media OCR/captions, and comments. It records project, dataset, visibility, content hash, immutable source version, index state, and deletion state.
- `evidence_source_payloads` preserves canonical text and the complete source payload for each version; frozen dataset references are rejected if any payload is unavailable.
- PostgreSQL rows and transactional Outbox events are facts. Redis, NATS, rankings, and future search indexes are rebuildable derivatives.

## Deletion Propagation

1. Note and comment API deletes are soft deletes with a deletion timestamp.
2. Database triggers immediately mark current and child Evidence Sources as `deleted`.
3. `note.deleted` and `comment.deleted` events invalidate cache/ranking state and will remove future lexical/vector index entries.
4. Previous Evidence Source versions remain as audit history but are never eligible for active current-dataset retrieval. Frozen experiments resolve their exact recorded source version.
5. A future hard-delete maintenance job must remove credentials, sessions, PII, source rows, index entries, and generated-report citations in one auditable workflow.

## Retention

| Data | Local policy | Production decision gate |
| --- | --- | --- |
| Sent Outbox events | maintenance candidate after 7 days | confirm audit requirement |
| Processed event IDs | maintenance candidate after 30 days | must exceed broker redelivery window |
| Expired/revoked sessions | maintenance candidate after 30 days | security review |
| NATS event stream | 7 days | align with replay RTO |
| NATS DLQ | 30 days | export incident evidence before expiry |
| Behavior events | retained as fact source | define legal/product window before launch |
| Daily facts | retained with `source_run_id` | rebuildable from retained behavior facts |
| Backups | manual local artifact today | managed encrypted schedule and lifecycle required |

`noteinsight-maintenance` is dry-run by default. Production deletion requires an approved run with `--apply`, captured logs, and post-run counts.

## PII And Secrets

- Passwords use bcrypt; refresh tokens are random and only their hash is stored.
- Browser refresh tokens use HttpOnly/SameSite cookies; access tokens remain in memory.
- Usernames, profile text, IP addresses, user agents, comments, and OCR may contain personal data. Do not place raw values in metrics or traces.
- Production secrets must come from a managed secret store. Local Compose credentials are development-only and must never be promoted.

## Retrieval Rules

- Every retrieval query must filter by `project_id`, dataset membership, source visibility, `index_status`, and `deleted_at` before scoring.
- Citations must resolve to an active source version and include source ID, version, selector/offset, content hash, and ingestion version.
- Banned-user content remains excluded from new writes; product/legal policy must decide whether existing content is hidden or retained.
- No-answer evaluation cases must not be satisfied from another project or deleted evidence.
- `no_relevant_document` and `insufficient_evidence` are separate outcomes; the latter may cite relevant sources while refusing an unsupported claim.
- Retrieval experiments and benchmark claims must name an immutable `dataset_version_id` and parser/tokenizer version.

## Audit Evidence

- Structured request logs carry request ID, trace ID, user ID, path, status, and latency.
- Profile, note, and comment mutations emit dedicated `audit mutation` records; admin actions include `elevated=true`.
- Migration checksums, corpus run IDs, fact materialization run IDs, source versions, and event IDs provide lineage across the data plane.
