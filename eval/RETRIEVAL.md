built_at: 2026-09-05T09:38:49Z
resources_sha256: 61faf57a
embed_model: gemini-embedding-001
pool: off=TopK+1 on=4xTopK
generated_at: 2026-09-05T10:03:09Z

| fixture | lane | set | k | recall_off | recall_on | mrr_off | mrr_on |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| 01-echo-cut-earliest-marker.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 1.00 | 1.00 | 1.00 |
| 01-echo-cut-earliest-marker.diff | standards | shared-standards | 8 | 1.00 | 1.00 | 1.00 | 1.00 |
| 02-logger-metrics-race.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 1.00 | 1.00 | 1.00 |
| 02-logger-metrics-race.diff | standards | shared-standards | 8 | 1.00 | 1.00 | 1.00 | 1.00 |
| 03-lane-overlays-config.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 1.00 | 1.00 | 1.00 |
| 03-lane-overlays-config.diff | standards | shared-standards | 8 | 1.00 | 1.00 | 1.00 | 1.00 |
| 04-lane-topk-default.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 1.00 | 1.00 | 1.00 |
| 04-lane-topk-default.diff | standards | shared-standards | 8 | 1.00 | 1.00 | 1.00 | 1.00 |
| mean |  |  |  | 1.00 | 1.00 (n=8) | 1.00 | 1.00 (n=8) |
