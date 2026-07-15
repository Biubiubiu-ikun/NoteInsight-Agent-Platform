# Quality And Security Gates

Updated: 2026-07-16

## Release Baseline

Commit `f0dee23` and annotated tag `v0.6.4` preserve the Phase 6C baseline. Retrieval benchmark, test-depth, contract, and supply-chain changes are kept in the following independent commit so Phase 7 does not grow the baseline into one unreviewable change.

## Retrieval Benchmark

`evaluation/benchmarks/retrieval_v4` is the only approved pre-retrieval benchmark. It is bound to immutable dataset version `2`, contains 80 public development cases and random-nonce SHA-256 commitments for all 240 cases across eight balanced task families. The 160 holdout questions, answers, sources and nonces remain in Git-ignored private artifacts. `evalfreeze -verify-only` validates either representation against manifest checksum `851a0ae94df77291d72904185754a2bea65893826fa942d52961472b65ab1b74`.

`retrieval_v3` is retired because its deterministic public inputs reproduce every case checksum. It remains historical audit evidence and is forbidden for tuning or quality claims.

```powershell
cd backend-go
go run ./cmd/evalfreeze -verify-only `
  -output-dir ../evaluation/benchmarks/retrieval_v4
```

## Test Layers

- Go unit/contract tests: `go test ./...`; statement coverage floor 25%, current 27.72%.
- Linux race: official `golang:1.25.12-bookworm` over the full repository mount.
- Integration tag: disposable PostgreSQL database plus live NATS for refresh replay, concurrent rotation, unique interactions, transaction rollback, Outbox lease recovery, evidence history, concurrent dataset freeze/reuse, frozen/retired rows, DLQ and replay.
- Frontend: Vitest/jsdom with coverage floors of 60/45/55/64 for statements/branches/functions/lines.
- Playwright: live API desktop and Pixel 7 flows; full authenticated publish/comment/interaction flow runs on desktop.
- Compose smoke: auth, ownership, banned users, project visibility, source propagation, idempotency and async convergence.

## Contract Gates

- `openapi-spec-validator` validates OpenAPI 3.1 and references.
- Go contract test compares every `/api/v1` Gin method/path with OpenAPI in both directions.
- `promtool check config` loads Prometheus config and all nine alert rules.
- Actionlint parses both GitHub Actions workflows.
- CodeQL analyzes Go and JavaScript/TypeScript and uploads findings to GitHub Code Scanning on the public remote. Private mirrors can set `CODEQL_UPLOAD=false` to retain SARIF artifacts and fail locally on findings.

## Supply Chain

- Gitleaks scans the working tree locally and full Git history in CI.
- Govulncheck is pinned at v1.1.4.
- Syft v1.20.0 emits an SPDX JSON SBOM as a CI artifact.
- Trivy v0.59.1 blocks fixable HIGH/CRITICAL runtime-image vulnerabilities.
- The scratch image runs as UID 65532. The verified local image contains Go 1.26.5, x/crypto 0.52.0, x/net 0.55.0 and quic-go 0.59.1; both vulnerability gates pass.

## External Gates

The sanitized public GitHub remote is established at `Biubiubiu-ikun/NoteInsight-Agent-Platform`, and Actions executes the Linux quality chain. CODEOWNERS, a PR template, `SECURITY.md`, protected `main`, required checks and review rules define the merge path. The pre-public repository remains a private archive so full holdout content is never reachable from public history. Environment approvals, registry signing and managed deployment secrets still require deployment infrastructure.
