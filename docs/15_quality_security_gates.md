# Quality And Security Gates

Updated: 2026-07-15

## Release Baseline

Commit `f0dee23` and annotated tag `v0.6.4` preserve the Phase 6C baseline. Retrieval benchmark, test-depth, contract, and supply-chain changes are kept in the following independent commit so Phase 7 does not grow the baseline into one unreviewable change.

## Retrieval Benchmark

`evaluation/benchmarks/retrieval_v3` is the only approved pre-retrieval benchmark. It contains 240 unique cases with an 80/160 development/holdout split and six balanced adversarial task families. The database and files are immutable; `evalfreeze -verify-only` recalculates every checksum and manifest statistic.

```powershell
cd backend-go
go run ./cmd/evalfreeze -verify-only `
  -output-dir ../evaluation/benchmarks/retrieval_v3
```

## Test Layers

- Go unit/contract tests: `go test ./...`; statement coverage floor 25%, current 26.13%.
- Linux race: official `golang:1.25.12-bookworm` over the full repository mount.
- Integration tag: disposable PostgreSQL database plus live NATS for refresh replay, concurrent rotation, unique interactions, transaction rollback, Outbox lease recovery, frozen rows, DLQ and replay.
- Frontend: Vitest/jsdom with coverage floors of 60/45/55/64 for statements/branches/functions/lines.
- Playwright: live API desktop and Pixel 7 flows; full authenticated publish/comment/interaction flow runs on desktop.
- Compose smoke: auth, ownership, banned users, project visibility, source propagation, idempotency and async convergence.

## Contract Gates

- `openapi-spec-validator` validates OpenAPI 3.1 and references.
- Go contract test compares every `/api/v1` Gin method/path with OpenAPI in both directions.
- `promtool check config` loads Prometheus config and all nine alert rules.
- Actionlint parses both GitHub Actions workflows.

## Supply Chain

- Gitleaks scans the working tree locally and full Git history in CI.
- Govulncheck is pinned at v1.1.4.
- Syft v1.20.0 emits an SPDX JSON SBOM as a CI artifact.
- Trivy v0.59.1 blocks fixable HIGH/CRITICAL runtime-image vulnerabilities.
- The scratch image runs as UID 65532. The verified local image contains Go 1.26.5, x/crypto 0.52.0, x/net 0.55.0 and quic-go 0.59.1; both vulnerability gates pass.

## External Gates

Local workflow syntax and every command are verified, but a Git host is still required for a real green Actions run, protected branch, required reviews, environment approvals and registry signing. These controls must not be claimed complete until their remote evidence exists.
