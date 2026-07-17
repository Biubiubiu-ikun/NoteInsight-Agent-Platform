# NoteInsight Recovery Runbook

## Targets

- Development/staging RPO: 24 hours with one verified PostgreSQL dump per day.
- Development/staging RTO: 2 hours, including restore, migration, reconcile, and smoke tests.
- Production target before launch: PostgreSQL PITR with RPO <= 15 minutes and RTO <= 60 minutes.

## Backup

Run `./scripts/backup_postgres.ps1`. Store the resulting custom-format dump outside the Docker volume and record its SHA-256 checksum.

At least monthly, restore the newest dump into an isolated Compose project and run:

1. `./scripts/migrate.ps1`
2. `go run ./cmd/reconcile --full`
3. `./scripts/smoke_phase2c_auth.ps1`
4. the data integrity checks in `docs/10_phase6_capacity_testing.md`

The 2026-07-15 drill restored `noteinsight_20260715_053939.dump` into the temporary
`creatorinsight_restore_verify` database. It recovered nine migrations, 5,459 notes,
101,024 comments, and 113,184 Evidence Sources. The temporary database was removed after validation.

The final post-acceptance archive is `noteinsight_20260715_065618.dump` (12,893,476 bytes,
SHA-256 `CE2E7486F9F736EFD27A8B26485D9006A9ADB0A8C8C136DE437B0089F659439C`).
Its archive directory was verified after migration 10 and contains 262 entries.

The Phase 7A-0 archive is `noteinsight_20260716_033326.dump` with SHA-256
`3424FEF52C080F432E6F230DB579EAE51379CC90C8F1C90CDCED89745F140E92`.
Its custom-format TOC parses successfully with 316 entries after migration 15. The
2026-07-15 isolated restore remains the latest full restore exercise.

The Phase 7A completion archive is `noteinsight_20260718_062530.dump` (97,180,232 bytes),
SHA-256 `45F2E285534DE9D3E94BBE1949D11318B8D40CB04DB44F608840A5F36C973CB0`.
Its custom-format TOC parses successfully with 434 entries after migration 16. It includes
the canonical Evidence Store; an isolated restore of this archive is still required by the
monthly restore schedule.

## Restore

Stop API and worker traffic before an in-place restore. Run `./scripts/restore_postgres.ps1 -BackupFile <dump> -ConfirmRestore`, then execute the four verification steps above.

## Derived systems

- Redis contains caches and rankings only. Flush or replace it, then run reconcile to rebuild derived data.
- NATS JetStream is durable transport, not the source of truth. PostgreSQL Outbox and behavior facts remain authoritative.
- Inspect DLQ with `go run ./cmd/dlqctl --limit 20`.
- Replay one inspected event with `go run ./cmd/dlqctl --event-id <id> --replay` after correcting the consumer failure.
- Never delete failed Outbox or DLQ records as part of routine retention.

## Evidence indexes

Evidence indexes must be reproducible from frozen dataset sources, immutable source/fact payloads, and the parser contract. After restore, run `scripts/evidence.ps1 -Operation reconcile`, audit the restored completed run, and perform a checksum-identical rebuild when the existing Evidence Store is not restored. A restore is incomplete until no ready document references a deleted source and citation byte slices verify.
