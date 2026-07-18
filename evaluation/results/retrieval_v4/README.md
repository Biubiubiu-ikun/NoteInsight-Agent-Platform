# Retrieval v4 Development Results

All files in this directory contain public `development` cases only. Sealed holdout queries, answers, sources, nonces, and reports must not be committed here.

## Current Evidence

| Artifact | Purpose |
| --- | --- |
| `development_benchmark_audit_v1.json` | Deterministic Gold membership and distinguishability audit |
| `development_phase7b_lexical_v3.json` | Current formal dataset-version-2 lexical baseline using `retrieval_metrics_v2` |
| `development_phase7c_vector_v1.json` | Formal dataset-version-2 dense baseline; failed Recall/MRR gate |
| `development_phase7c_hybrid_v1.json` | Formal dataset-version-2 RRF hybrid baseline; failed Recall/MRR gate |
| `development_phase7c_vector_dv4_v1.json` | Dense-only diagnostic on the dataset-version-4 quality subset |
| `development_phase7c_hybrid_dv4_v1.json` | Pre-threshold hybrid diagnostic retained as before evidence |
| `development_phase7c_hybrid_dv4_v2.json` | Current hybrid diagnostic with the 0.60 acceptance threshold |

Files named `phase7b_v1/v2` and `phase7b_diagnostic_dv4_v1/v2/v3` are historical development iterations. They are retained to make query-plan, threshold, performance, and metric-semantic changes auditable; they are not the current quality claim.

Only reports with `dataset_contract_matched=true` are formal benchmark runs. Dataset-version-4 reports deliberately set the development override and cannot pass that contract check even when their quality metrics exceed the numeric gate.
