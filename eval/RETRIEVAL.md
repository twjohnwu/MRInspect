built_at: 2026-09-28T09:37:46Z
resources_sha256: 57d63a0a
embed_model: gemini-embedding-001
pool: off=TopK+1 on=4xTopK shuffle=4xTopK×20
generated_at: 2026-09-28T09:43:17Z
retrieve_ms: off_mean=4 on_mean=331 (n=48)

| system | fixture | lane | set | k | orig_recall_off | orig_recall_shuf | orig_recall_on | orig_mrr_off | orig_mrr_shuf | orig_mrr_on | para_recall_off | para_recall_shuf | para_recall_on | para_mrr_off | para_mrr_shuf | para_mrr_on | distractors_off | distractors_shuf | distractors_on |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| fried-chicken | 01-fryer-temperature-schedule.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.20 | 0.00 | 0.00 | 0.04 | 0.00 | 1 | 0.10 | 1 |
| fried-chicken | 01-fryer-temperature-schedule.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.30 | 0.00 | 0.00 | 0.15 | 0.00 | 1 | 0.10 | 1 |
| fried-chicken | 02-batch-queue-backpressure.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.15 | 0.00 | 0.00 | 0.03 | 0.00 | 1 | 0.30 | 0 |
| fried-chicken | 02-batch-queue-backpressure.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.15 | 0.00 | 0.00 | 0.02 | 0.00 | 1 | 0.10 | 1 |
| fried-chicken | 03-marinade-config-validation.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.30 | 1.00 | 0.00 | 0.10 | 0.20 | 1 | 0.10 | 1 |
| fried-chicken | 03-marinade-config-validation.diff | standards | shared-standards | 8 | 1.00 | 0.10 | 1.00 | 0.50 | 0.02 | 1.00 | 0.00 | 0.05 | 1.00 | 0.00 | 0.01 | 0.33 | 1 | 0.35 | 1 |
| fried-chicken | 04-order-pickup-pagination.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.30 | 1.00 | 1.00 | 0.07 | 1.00 | 0.00 | 0.20 | 1.00 | 0.00 | 0.11 | 0.33 | 1 | 0.10 | 1 |
| fried-chicken | 04-order-pickup-pagination.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.10 | 1.00 | 0.00 | 0.06 | 0.12 | 1 | 0.30 | 1 |
| fried-chicken | 05-oil-change-alerting.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.30 | 1.00 | 0.25 | 0.09 | 1.00 | 0.00 | 0.20 | 0.00 | 0.00 | 0.08 | 0.00 | 1 | 0.20 | 1 |
| fried-chicken | 05-oil-change-alerting.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 0.00 | 0.00 | 0.09 | 0.00 | 1 | 0.10 | 1 |
| fried-chicken | 06-coating-double-dip-thickness-guard.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 1.00 | 0.00 | 0.09 | 0.25 | 1 | 0.10 | 1 |
| fried-chicken | 06-coating-double-dip-thickness-guard.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 1.00 | 0.00 | 0.06 | 0.25 | 1 | 0.10 | 1 |
| fried-chicken | 07-dlq-retry-exhaustion-routing.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.10 | 0.00 | 0.00 | 0.05 | 0.00 | 1 | 0.30 | 1 |
| fried-chicken | 07-dlq-retry-exhaustion-routing.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.30 | 0.00 | 0.00 | 0.14 | 0.00 | 1 | 0.10 | 1 |
| fried-chicken | 08-consumer-shutdown-context-cancel.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.17 | 1.00 | 0.00 | 0.30 | 0.00 | 0.00 | 0.06 | 0.00 | 1 | 0.20 | 1 |
| fried-chicken | 08-consumer-shutdown-context-cancel.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 0.00 | 0.00 | 0.06 | 0.00 | 1 | 0.10 | 1 |
| fried-chicken | 09-pipeline-batch-upsert-timestamp.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 1.00 | 0.00 | 0.09 | 0.17 | 1 | 0.10 | 1 |
| fried-chicken | 09-pipeline-batch-upsert-timestamp.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.35 | 1.00 | 0.00 | 0.12 | 0.25 | 1 | 0.10 | 1 |
| fried-chicken | 10-write-concern-majority-enforcement.diff | spec-conformance | fried-chicken-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.20 | 0.00 | 0.00 | 0.04 | 0.00 | 1 | 0.30 | 1 |
| fried-chicken | 10-write-concern-majority-enforcement.diff | standards | shared-standards | 8 | 1.00 | 0.10 | 1.00 | 0.50 | 0.02 | 0.50 | 0.00 | 0.20 | 0.00 | 0.00 | 0.04 | 0.00 | 1 | 0.35 | 1 |
| margherita-pizza | 01-echo-cut-earliest-marker.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.33 | 1.00 | 1.00 | 0.27 | 1.00 | 0.00 | 0.25 | 1.00 | 0.00 | 0.04 | 0.25 | 1 | 0.10 | 1 |
| margherita-pizza | 01-echo-cut-earliest-marker.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.30 | 1.00 | 0.00 | 0.14 | 0.33 | 1 | 0.10 | 1 |
| margherita-pizza | 02-logger-metrics-race.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.25 | 1.00 | 0.50 | 0.15 | 1.00 | 0.00 | 0.15 | 1.00 | 0.00 | 0.03 | 0.20 | 1 | 0.35 | 1 |
| margherita-pizza | 02-logger-metrics-race.diff | standards | shared-standards | 8 | 1.00 | 0.10 | 1.00 | 0.50 | 0.02 | 1.00 | 0.00 | 0.30 | 1.00 | 0.00 | 0.10 | 0.33 | 1 | 0.35 | 1 |
| margherita-pizza | 03-lane-overlays-config.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.33 | 1.00 | 1.00 | 0.27 | 1.00 | 0.00 | 0.10 | 1.00 | 0.00 | 0.05 | 0.14 | 1 | 0.10 | 1 |
| margherita-pizza | 03-lane-overlays-config.diff | standards | shared-standards | 8 | 1.00 | 0.10 | 1.00 | 0.50 | 0.02 | 1.00 | 0.00 | 0.30 | 0.00 | 0.00 | 0.08 | 0.00 | 1 | 0.35 | 1 |
| margherita-pizza | 04-lane-topk-default.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.33 | 1.00 | 1.00 | 0.27 | 1.00 | 0.00 | 0.10 | 1.00 | 0.00 | 0.05 | 0.25 | 1 | 0.10 | 1 |
| margherita-pizza | 04-lane-topk-default.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 0.50 | 0.00 | 0.05 | 1.00 | 0.00 | 0.01 | 0.33 | 1 | 0.10 | 1 |
| margherita-pizza | 05-oven-scheduling-window.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.10 | 1.00 | 0.00 | 0.06 | 0.20 | 1 | 0.10 | 1 |
| margherita-pizza | 05-oven-scheduling-window.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 1.00 | 0.00 | 0.06 | 0.25 | 1 | 0.10 | 1 |
| margherita-pizza | 06-basket-currency-guard.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 1.00 | 0.00 | 0.06 | 0.17 | 1 | 0.35 | 1 |
| margherita-pizza | 06-basket-currency-guard.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.55 | 1.00 | 0.00 | 0.19 | 0.25 | 1 | 0.30 | 1 |
| margherita-pizza | 07-kitchen-notice-board-ttl.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.15 | 1.00 | 0.00 | 0.03 | 0.33 | 1 | 0.10 | 1 |
| margherita-pizza | 07-kitchen-notice-board-ttl.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.30 | 1.00 | 0.00 | 0.05 | 0.50 | 1 | 0.30 | 1 |
| margherita-pizza | 08-oven-metric-name-stability.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.15 | 0.00 | 0.00 | 0.02 | 0.00 | 1 | 0.10 | 1 |
| margherita-pizza | 08-oven-metric-name-stability.diff | standards | shared-standards | 8 | 1.00 | 0.10 | 1.00 | 0.50 | 0.02 | 1.00 | 0.00 | 0.35 | 0.00 | 0.00 | 0.12 | 0.00 | 1 | 0.35 | 1 |
| margherita-pizza | 09-downstream-timeout-budget-split.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.15 | 0.00 | 0.00 | 0.03 | 0.00 | 1 | 0.10 | 1 |
| margherita-pizza | 09-downstream-timeout-budget-split.diff | standards | shared-standards | 8 | 1.00 | 0.10 | 1.00 | 0.50 | 0.02 | 1.00 | 0.00 | 0.10 | 1.00 | 0.00 | 0.06 | 0.20 | 1 | 0.30 | 1 |
| margherita-pizza | 10-fixture-builder-contradictory-options.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.55 | 0.00 | 0.00 | 0.19 | 0.00 | 1 | 0.10 | 1 |
| margherita-pizza | 10-fixture-builder-contradictory-options.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 1.00 | 0.00 | 0.09 | 0.20 | 1 | 0.10 | 1 |
| margherita-pizza | 11-dough-service-direct-db-boundary.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.15 | 0.00 | 0.00 | 0.03 | 0.00 | 1 | 0.35 | 0 |
| margherita-pizza | 11-dough-service-direct-db-boundary.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 0.25 | 0.07 | 0.33 | 0.00 | 0.30 | 0.00 | 0.00 | 0.05 | 0.00 | 1 | 0.10 | 1 |
| margherita-pizza | 12-kitchen-region-declaration-guard.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.30 | 0.00 | 0.00 | 0.08 | 0.00 | 1 | 0.10 | 1 |
| margherita-pizza | 12-kitchen-region-declaration-guard.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.55 | 0.00 | 0.00 | 0.19 | 0.00 | 1 | 0.10 | 1 |
| margherita-pizza | 13-pagination-cursor-tiebreak.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 0.00 | 0.00 | 0.04 | 0.00 | 1 | 0.10 | 1 |
| margherita-pizza | 13-pagination-cursor-tiebreak.diff | standards | shared-standards | 8 | 1.00 | 0.10 | 1.00 | 0.50 | 0.02 | 0.50 | 0.00 | 0.30 | 0.00 | 0.00 | 0.15 | 0.00 | 1 | 0.30 | 1 |
| margherita-pizza | 14-retry-trace-root-linking.diff | spec-conformance | margherita-pizza-docs | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.05 | 0.00 | 0.00 | 0.01 | 0.00 | 1 | 0.30 | 1 |
| margherita-pizza | 14-retry-trace-root-linking.diff | standards | shared-standards | 8 | 1.00 | 0.35 | 1.00 | 1.00 | 0.16 | 1.00 | 0.00 | 0.25 | 1.00 | 0.00 | 0.06 | 0.12 | 1 | 0.10 | 1 |
|  | mean |  |  |  | 1.00 (n=48) | 0.31 (n=48) | 1.00 (n=48) | 0.89 (n=48) | 0.14 (n=48) | 0.95 (n=48) | 0.00 (n=48) | 0.23 (n=48) | 0.50 (n=48) | 0.00 (n=48) | 0.07 (n=48) | 0.12 (n=48) | 1.00 (n=48) | 0.18 (n=48) | 0.96 (n=48) |

## Mean by k

| k | orig_recall_off | orig_recall_shuf | orig_recall_on | para_recall_off | para_recall_shuf | para_recall_on |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| k=1 | 0.76 (n=48) | 0.08 (n=48) | 0.87 (n=48) | 0.00 (n=48) | 0.02 (n=48) | 0.00 (n=48) |
| k=3 | 0.95 (n=48) | 0.16 (n=48) | 0.99 (n=48) | 0.00 (n=48) | 0.07 (n=48) | 0.15 (n=48) |
| k=8 | 1.00 (n=48) | 0.31 (n=48) | 1.00 (n=48) | 0.00 (n=48) | 0.23 (n=48) | 0.50 (n=48) |

## Distractors by category

| category | n | distractors_off | distractors_shuf | distractors_on |
| --- | ---: | ---: | ---: | ---: |
| scope | 12 | 1.00 (n=12) | 0.16 (n=12) | 1.00 (n=12) |
| version | 10 | 1.00 (n=10) | 0.21 (n=10) | 1.00 (n=10) |
| responsibility | 9 | 1.00 (n=9) | 0.20 (n=9) | 1.00 (n=9) |
| lexical | 8 | 1.00 (n=8) | 0.12 (n=8) | 1.00 (n=8) |
| neighbor | 9 | 1.00 (n=9) | 0.22 (n=9) | 0.78 (n=9) |
