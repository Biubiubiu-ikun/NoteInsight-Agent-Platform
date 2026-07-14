# Phase 5B Quality Text Corpus

Completed: 2026-07-14.

## Planning Adaptation

`最新项目规划.md` remains authoritative for the Xiaohongshu-style note and auth
domain. This phase adapts the useful data-quality requirements from the first historical
plan: separate load-test volume from Agent-quality data, generate a hidden scenario
before visible content, and produce ground truth that can later be bound to evidence IDs.

The historical video/subtitle/danmu vocabulary is not restored. It is mapped to the
current domain as follows:

```text
video description/subtitle -> note body + media caption/OCR
comment                    -> note comment
hidden video scenario      -> hidden note scenario
subtitle evidence          -> note_body/media_ocr evidence
comment cluster            -> note comment topic cluster
```

## Why This Phase Was Needed

The previous seed generator created realistic row counts but its text was placeholder
content. That was sufficient for API and database load tests, but unsuitable for
embedding, retrieval, clustering, citation, or Agent evaluation.

Phase 5B now keeps two data layers:

1. `seedgen` creates high-volume development/load-test data with substantive Chinese
   note bodies, OCR, and note-linked comments.
2. `corpusgen` creates a smaller, denser Agent-quality corpus with hidden scenarios,
   multiple OCR pages, 200 comments per note, and ground-truth evaluation cases.

Neither layer requires an LLM API. Fixed-seed structured templates make the current
dataset cheap, reproducible, inspectable, and suitable for automated assertions.

## Generation Flow

```text
category theme
  -> hidden scenario
     - subject, audience, context, goal
     - steps, positive feedback, concerns
     - key metric, conclusion, unsuitable audience
  -> unique title and 650+ character note body
  -> media pages
     - summary caption/OCR
     - procedure caption/OCR
     - measurement caption/OCR
     - caveat caption/OCR
  -> semantically linked comments
     - intent, sentiment, topic_id
  -> evaluation cases
     - question, expected_answer, gold_sources
  -> quality report and PostgreSQL transaction
```

The generated media rows intentionally use `url = NULL`. Images are optional at this
stage, while `caption`, `ocr_text`, and metadata such as `content_role` remain complete.
Real uploads or generated images can be attached later without changing the text and
evidence model.

## Corpus Topics

The catalog contains two explainable themes for each current note category:

```text
beauty, fashion, food, travel, home,
fitness, career, digital, study, local_life
```

Examples include sensitive-skin sunscreen testing, capsule wardrobes, weekly meal
planning, slow travel, rental lighting, beginner strength training, interview review,
headphone testing, study plans, and local rental/remote-work observations.

Comments cover these intent classes:

```text
positive_feedback, ask_detail, experience_share, supplement,
ask_suitable, disagreement, risk_warning, request_followup
```

## Database Migration

`000007_phase5b_quality_corpus.sql` adds:

- `content_corpus_runs`: fixed-seed configuration, counts, report, and run status.
- `content_scenarios`: one hidden scenario for every quality note.
- `content_eval_cases`: questions, expected answers, and pre-index gold source selectors.

`content_scenarios.note_id` and `content_eval_cases.note_id` reference real `notes` rows.
Replacing a run deletes only notes owned by that run, and normal note cascades remove
its media, comments, interactions, scenarios, and eval cases in one transaction.

Gold sources currently identify semantic source locations, for example:

```json
[
  {"source_type": "note_body", "topic": "具体过程"},
  {"source_type": "media_ocr", "position": 2}
]
```

The later ingestion phase will resolve these selectors to concrete `evidence_id`
values after Evidence Store rows are built.

## Profiles

| Profile | Notes | Media/note | Comments/note | Eval cases/note |
| --- | ---: | ---: | ---: | ---: |
| `smoke` | 20 | 3 | 30 | 5 |
| `quality` | 200 | 4 | 200 | 5 |

The five task types are `summary`, `procedure`, `controversy`, `audience`, and
`ocr_detail`.

## Commands

Generate the Agent-quality corpus:

```powershell
.\scripts\generate_quality_corpus.ps1 `
  -Profile quality `
  -RunId phase5b_quality_20260714 `
  -Replace
```

Preview without PostgreSQL writes:

```powershell
cd backend-go
go run ./cmd/corpusgen --profile=quality --dry-run
```

Inside the scratch service image, add `--no-report-file`; the complete report remains
stored in `content_corpus_runs.report` and is also printed to stdout.

Rebuild all reproducible development data with meaningful text:

```powershell
cd backend-go
$env:POSTGRES_DSN = "postgres://creatorinsight:creatorinsight@localhost:15432/creatorinsight?sslmode=disable"
$env:REDIS_ADDR = "localhost:6379"
go run ./cmd/seedgen --profile=dev --seed=20260706 --truncate --with-tokens
```

## Quality Gates

Strict mode rejects a corpus unless it satisfies:

- all expected categories are represented;
- at least 98% of titles are unique;
- every note body contains at least 300 characters;
- every OCR page contains at least 30 characters;
- every comment contains at least 35 characters;
- duplicate comment ratio is at most 1%;
- at least 98% of comments contain note-scenario semantics;
- all configured evaluation task types are represented.

Unit tests additionally verify fixed-seed deep equality, note/scenario linkage,
caption/OCR completeness, comment labels, and non-empty ground truth.

## Local Validation

The final local database contains:

```text
notes: 5,220
note_media: 5,860
note_comments: 60,600
hidden quality scenarios: 220
ground-truth eval cases: 1,100
placeholder note bodies: 0
placeholder comments: 0
```

For the 200-note `quality` run:

```text
media rows: 800
comments: 40,000
eval cases: 1,000
title unique ratio: 1.0
duplicate comment ratio: 0.0
semantic alignment ratio: 1.0
minimum body length: 651 characters
minimum OCR length: 42 characters
minimum comment length: 95 characters
```

All ten categories contain 20 quality notes, and each of the five evaluation task types
contains 200 cases.

## LLM Boundary

An LLM is not used for bulk generation now. A future optional enrichment job may use
the Model Gateway to rewrite a reviewed subset for greater linguistic diversity. That
job must preserve the hidden scenario, validate factual consistency, record model and
prompt versions, cap cost, and never overwrite the deterministic baseline.

## Next Step

Phase 6 should collect reproducible steady, spike, soak, and outage capacity evidence.
After that, the first RAG-facing implementation should add the note-domain Evidence
Store and deterministic ingestion for `note_body`, `media_ocr`, representative comments,
comment clusters, and behavior summaries before introducing Qdrant or an Agent runtime.
