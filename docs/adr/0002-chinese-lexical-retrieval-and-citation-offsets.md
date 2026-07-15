# ADR 0002: Chinese Lexical Retrieval And Citation Offsets

- Status: Accepted for Phase 7A/7B implementation
- Date: 2026-07-16

## Context

PostgreSQL does not provide useful Chinese word segmentation through its default language configurations. Citation offsets also become ambiguous when code points, UTF-8 bytes, UTF-16 units, line endings, or normalized text are mixed.

## Decision

1. Canonical evidence preserves source Unicode text, normalizing only line endings to `\n`. It does not apply compatibility normalization, trim internal whitespace, or silently rewrite punctuation.
2. Content hashes are computed from canonical UTF-8 bytes plus the versioned parser contract.
3. Chunk ranges are half-open `[start_byte, end_byte)` UTF-8 byte offsets into canonical evidence. Boundaries must decode as valid UTF-8. Derived rune offsets may be stored for UI display; UTF-16 offsets are never authoritative.
4. Citations carry evidence document/chunk identity, project, dataset version, source type/id/version, content hash, parser version, and byte range. Verification slices the canonical bytes and rechecks the content hash.
5. The lexical baseline uses a versioned application tokenizer for Chinese and Latin/number tokens, stores pre-tokenized lexemes in a PostgreSQL `tsvector` using the `simple` configuration, and uses `pg_trgm` only as a typo/substring fallback.
6. Metadata and authorization filters execute before scoring. `ts_rank_cd` is called a PostgreSQL FTS lexical baseline, not BM25. The project will claim BM25 only if an implementation with BM25 semantics is installed and measured.

## Consequences

- Citations remain reproducible across Go, PostgreSQL, and browser clients.
- Tokenizer upgrades require a new parser/tokenizer version and deterministic re-ingestion.
- Development starts with explainable PostgreSQL retrieval before adding embeddings or a vector database.
