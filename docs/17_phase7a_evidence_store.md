# Phase 7A Evidence Store

Updated: 2026-07-18

## Scope

Phase 7A converts frozen source payloads and versioned daily facts into deterministic PostgreSQL evidence. It does not expose retrieval APIs, tune ranking, call an embedding model, use Qdrant, or run an Agent.

The authoritative corpus input remains frozen `dataset_version_id=2`:

- dataset: `2`, project: `1`;
- source references: `113,921`;
- dataset manifest: `b91df11ca9136e000c759fd2c6de5b448816bb57d903849c478f99db8533eab5`.

## Data Model

Migration `000016_phase7a_evidence_store.sql` adds:

| Table | Purpose |
| --- | --- |
| `daily_fact_payloads` | Immutable note/user daily-fact versions and payload hashes |
| `ingestion_runs` | Dataset, parser contract, input/output checksum, counts, failure and retry lineage |
| `ingestion_run_fact_inputs` | Fact payloads frozen when a run begins |
| `evidence_documents` | Canonical Unicode evidence scoped to project, dataset and dataset version |
| `evidence_document_sources` | One or more immutable source rows behind each document |
| `evidence_chunks` | UTF-8 byte/rune ranges, lexical tokens, generated `tsvector` and trigram fallback |
| `source_citations` | Document and source byte ranges plus hashes for exact provenance |
| `ingestion_run_documents` | Created/reused disposition for every run output |

Unique constraints make document, chunk, citation and run linkage writes idempotent. Completed runs, immutable payloads, chunks, citations and provenance mappings are protected by database triggers.

## Canonical Contract

- Parser: `evidence_parser_v1`.
- Chunker: `utf8_paragraph_1200_160_v1`, maximum 1,200 bytes with 160-byte overlap.
- Tokenizer: `zh_unigram_bigram_latin_v1`.
- Canonical source text changes only CRLF/CR line endings to `\n`; punctuation, Unicode and internal whitespace are preserved.
- Authoritative ranges are half-open UTF-8 byte offsets `[start_byte,end_byte)`; rune offsets are derived for UI use.
- Document hashes include the parser contract. Document/chunk/citation keys are deterministic SHA-256 manifests.
- Every chunk must have at least one citation. Citation verification slices canonical document bytes and canonical source bytes and checks the quote hash.

Notes and media become direct documents. Comments are deterministically ordered by semantic metadata and content richness, then grouped into 12-source note-level clusters; every included comment keeps its own exact source citation. All comments are retained, while higher-value comments appear earlier in each note's cluster sequence.

Daily facts receive database-controlled `content_version` values. A run snapshots the latest immutable fact payloads before parsing, so retrying the same `run_id` cannot observe a later fact update.

## Operations

```powershell
# New incremental run; a run id is generated when omitted
.\scripts\evidence.ps1 -Operation ingest -DatasetVersionId 2

# Resume a failed run by reusing its exact run id
.\scripts\evidence.ps1 -Operation ingest `
  -DatasetVersionId 2 -RunId phase7a_dv2_v1_20260718

# Deterministic rebuild and consistency audit
.\scripts\evidence.ps1 -Operation rebuild `
  -DatasetVersionId 2 -RunId my_rebuild
.\scripts\evidence.ps1 -Operation audit -RunId my_rebuild

# Propagate source/fact supersession and deletion into active document lifecycle
.\scripts\evidence.ps1 -Operation reconcile
```

The same command is available in the platform image as `/app/noteinsight-evidence`. Fresh and partial runs use eight bounded database writers. Ready documents are validated and linked in batches of 500, making rebuilds cheap without bypassing immutable constraints.

## Acceptance Snapshot

Run `phase7a_dv2_v1_20260718`:

- `113,921` frozen registry sources and `1,283` immutable daily-fact inputs;
- `25,448` documents: 5,509 notes, 6,789 media, 11,867 comment clusters, 805 note facts and 478 user facts;
- `56,349` chunks and `153,348` citations;
- input checksum `fd3d986eafdfa3bcb1689dfd8d84877dd6d7fc8161672d02ae9b2218a8958b88`;
- output checksum `3f372c59b8108bd95fb747e5d04aa73fe35ea6657f7219022ce047b07da3ee1a`;
- first serial baseline took about 25m56s and established the canonical rows.

Rebuild `phase7a_dv2_rebuild_v2_20260718` reused all `25,448` documents, created no duplicate document/chunk/citation, produced the identical output checksum, and completed its database run in about 46 seconds after batched-reuse optimization.

Both run audits report zero incomplete documents, chunk-count mismatches, invalid chunk slices, chunks without citations, invalid citation slices, scope mismatches, active deleted sources or output-checksum mismatches. A full SQL comparison over all registry-backed citations reports zero document/source byte-slice mismatches.

Real PostgreSQL integration tests additionally cover injected failure and same-run retry, full rebuild determinism, source version supersession, comment deletion propagation, fact version history, child immutability and citation round trips.

The Phase 7A completion backup is `noteinsight_20260718_062530.dump` (97,180,232 bytes), SHA-256 `45F2E285534DE9D3E94BBE1949D11318B8D40CB04DB44F608840A5F36C973CB0`. PostgreSQL `pg_restore --list` parses its 434-entry custom-format TOC.

## Phase 7B Boundary

Phase 7B will add authorization-filtered lexical query APIs, ranking and offline evaluation against `retrieval_v4`. The existing `search_vector` and trigram index are storage primitives only; no retrieval quality claim is made by Phase 7A.
