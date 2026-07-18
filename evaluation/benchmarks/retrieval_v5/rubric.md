# Retrieval v5 Relevance Rubric

## Answerability

- `answerable`: the frozen evidence snapshot contains enough authorized evidence to answer the query.
- `insufficient_evidence`: related authorized evidence exists, but it cannot support the expected conclusion.
- `no_relevant_document`: no authorized evidence is relevant.
- `authorization_denied`: supporting evidence exists but is unavailable to the denied principal.

## Relevance Grades

| Grade | Meaning |
| ---: | --- |
| 0 | Irrelevant or misleading for the query |
| 1 | Topically related, but insufficient to support the answer |
| 2 | Sufficient supporting evidence when read on its own or as an explicit comparison input |
| 3 | Direct, canonical evidence for the expected answer |

Reviewers label sources against the frozen source payload, not against a retriever's rank or score. Equivalent evidence receives the same grade. Deleted, superseded, or unauthorized evidence is recorded for boundary testing but cannot be a permitted Gold for the evaluated principal.

## Disagreements

Reviewers work independently. The adjudicator records the selected label and a short rubric-based rationale. No label may be changed after model evaluation without creating a new benchmark version.
