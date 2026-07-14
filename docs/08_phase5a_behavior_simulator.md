# Phase 5A Behavior Simulator

Completed: 2026-07-14.

## Scope

Phase 5A adds a deterministic, note-domain behavior simulator. It produces realistic
user sessions for retrieval, ranking, analytics, and later Agent evaluation without
changing the public API or the Phase 4 event pipeline.

This phase does not call an LLM for bulk generation. Structured behavior at million-row
scale is generated locally from seeded probability distributions. LLM calls are better
reserved for a smaller, reviewed corpus of natural-language note and comment content.

## Structure

```text
backend-go/
  cmd/simulator/main.go
  internal/simulator/
    engine.go
    models.go
    repository.go
    sinks.go
    file_sink.go
    engine_test.go
    file_sink_test.go
  migrations/000006_phase5a_behavior_simulator.sql
scripts/build_backend_linux.ps1
```

The engine is independent of PostgreSQL. A `Sink` receives profiles and events as they
are generated, so scale runs do not retain every event in memory. File and database
sinks can run together.

## Model

Personas:

```text
lurker, commenter, collector, fan, critic, knowledge_user, creator, spammer
```

Session states:

```text
feed_impression -> note_viewed -> media_viewed/comments_viewed
                -> note_liked/note_collected/note_shared
                -> comment_created/comment_liked -> session_exited
```

- Markov transition weights vary by persona and profile probabilities.
- Zipf selection concentrates activity on popular notes.
- Pareto-like activity weights create light and heavy users.
- Poisson/exponential timing spreads sessions over the configured interval.
- `viral`, `controversy`, and `mixed` scenarios add reproducible burst windows.
- Comment events include synthetic sentiment, intent, and length preference metadata.

## Migration

`simulation_runs` records immutable run configuration and the final distribution
report. `user_behavior_profiles` stores the generated persona parameters. Existing
`behavior_events` gain nullable `simulation_run_id`, `session_id`, and `sequence_no`
columns, preserving compatibility with real API events.

Database output is transactional: run metadata, profile upserts, batched events, and
the completed report commit together. Reusing a run ID requires `--replace`; the
foreign key cascade removes that run's prior simulated events.

## Profiles

| Profile | Users | Notes | Sessions | Simulated time |
| --- | ---: | ---: | ---: | ---: |
| `smoke` | 50 | 100 | 500 | 24 hours |
| `dev` | 1,000 | 5,000 | 20,000 | 7 days |
| `scale` | 100,000 | 10,000 | 250,000 | 30 days |

Synthetic datasets can run offline. `--dataset=database` loads active users, published
notes, and existing comment IDs. `--write-db` selects that dataset automatically and
persists the run.

## Commands

Preview the resolved workload:

```powershell
cd backend-go
go run ./cmd/simulator --profile=scale --dry-run
```

Generate NDJSON and a JSON report:

```powershell
go run ./cmd/simulator --profile=smoke --scenario=mixed --replace
```

Generate from the current database and persist atomically:

```powershell
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
go run ./cmd/simulator --profile=smoke --dataset=database --write-db --replace
```

Run the full scale model without writing 1.3 million NDJSON lines:

```powershell
go run ./cmd/simulator --profile=scale --no-event-files --strict=true
```

Output is written beneath `backend-go/tmp/simulator/<run_id>/` as
`profiles.ndjson`, `events.ndjson`, and `report.json`. Temporary files are renamed only
after a successful run.

## Validation Evidence

The database smoke run generated 500 sessions and 2,645 events. PostgreSQL reported
2,645 stored events, 50 profiles, and zero sessions with a missing, duplicate, or
non-contiguous sequence number.

The local scale run generated 250,000 sessions and 1,322,565 events in approximately
1 minute 50 seconds on an Intel i5-10300H workstation:

```text
average events/session: 5.2903
p50 / p95 events/session: 5 / 11
burst event ratio: 0.2512
top 1% note event share: 0.7323
top 10% user event share: 0.3374
personas represented: 8
```

Strict checks enforce multi-step sessions, non-pathological note and user
concentration, persona and event diversity, and scenario-appropriate burst behavior.

## Tests

```powershell
cd backend-go
go test ./internal/simulator -count=1
go test ./...
go vet ./...
```

Tests cover fixed-seed determinism, valid session chains, event identity uniqueness,
comment references, distribution quality, organic traffic, unsafe run IDs, atomic file
finalization, and replacement behavior. A 10,000-session benchmark completes in about
0.85 seconds on the same workstation.

## Deferred

- materializing simulated actions into note like/comment/collect fact tables
- LLM-generated note and comment text corpora
- multi-week lifecycle evolution for users and notes
- distribution calibration against production telemetry
- RAG, Qdrant, Agent runtime, and evaluation datasets

Phase 5B should add lifecycle evolution and optional fact materialization, then make
the simulator a reusable fixture for Phase 6 steady, spike, soak, and outage testing.
