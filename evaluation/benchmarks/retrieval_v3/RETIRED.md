# Retired Benchmark

`retrieval_v3_20260715` was retired on 2026-07-16 before retrieval implementation began.

Its case content was generated separately from the corpus Gold Case function, but all 240 case checksums can still be reconstructed from deterministic public source inputs. It is retained only for historical audit and must not be used for retrieval quality claims or tuning.

The approved replacement is `evaluation/benchmarks/retrieval_v4`, which binds an immutable dataset snapshot and seals holdout identities with random nonce commitments.
