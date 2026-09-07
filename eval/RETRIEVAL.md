built_at: 2026-09-07T11:44:05Z
resources_sha256: c1fdae0c
embed_model: gemini-embedding-001
pool: off=TopK+1 on=4xTopK shuffle=4xTopK×20
generated_at: 2026-09-07T11:48:13Z

| fixture | lane | set | k | orig_recall_off | orig_recall_shuf | orig_recall_on | orig_mrr_off | orig_mrr_shuf | orig_mrr_on | para_recall_off | para_recall_shuf | para_recall_on | para_mrr_off | para_mrr_shuf | para_mrr_on | distractors_off | distractors_shuf | distractors_on |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 01-echo-cut-earliest-marker.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.33 | 1.00 | 1.00 | 0.27 | 1.00 | 0.00 | 0.30 | 1.00 | 0.00 | 0.08 | 0.25 | 1 | 0.10 | 1 |
| 01-echo-cut-earliest-marker.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 1.00 | 0.00 | 0.04 | 0.25 | 1 | 0.10 | 1 |
| 02-logger-metrics-race.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.25 | 1.00 | 0.50 | 0.15 | 1.00 | 0.00 | 0.20 | 1.00 | 0.00 | 0.04 | 0.20 | 1 | 0.35 | 1 |
| 02-logger-metrics-race.diff | standards | shared-standards | 8 | 1.00 | 0.10 | 1.00 | 0.50 | 0.02 | 1.00 | 0.00 | 0.10 | 1.00 | 0.00 | 0.04 | 0.33 | 1 | 0.35 | 1 |
| 03-lane-overlays-config.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.24 | 1.00 | 0.00 | 0.30 | 1.00 | 0.00 | 0.07 | 0.14 | 1 | 0.20 | 1 |
| 03-lane-overlays-config.diff | standards | shared-standards | 8 | 1.00 | 0.10 | 1.00 | 0.50 | 0.02 | 1.00 | 0.00 | 0.10 | 0.00 | 0.00 | 0.06 | 0.00 | 1 | 0.35 | 1 |
| 04-lane-topk-default.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.33 | 1.00 | 1.00 | 0.27 | 1.00 | 0.00 | 0.15 | 1.00 | 0.00 | 0.03 | 0.25 | 1 | 0.10 | 1 |
| 04-lane-topk-default.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 0.50 | 0.00 | 0.25 | 1.00 | 0.00 | 0.08 | 0.33 | 1 | 0.10 | 1 |
| mean |  |  |  | 1.00 (n=8) | 0.27 (n=8) | 1.00 (n=8) | 0.81 (n=8) | 0.16 (n=8) | 0.94 (n=8) | 0.00 (n=8) | 0.21 (n=8) | 0.88 (n=8) | 0.00 (n=8) | 0.05 (n=8) | 0.22 (n=8) | 1.00 (n=8) | 0.21 (n=8) | 1.00 (n=8) |
