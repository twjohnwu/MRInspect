# Eval fixtures

fixtures 均取自本 repo 已公開歷史；curation 準則見 `STDD/review-quality-eval/spec.md` REQ-02。

| 檔名 | 來源 commit | kind | 挑選理由 |
|---|---|---|---|
| `01-echo-cut-earliest-marker.diff` | `2aad056` | `logic` | 回應清洗的優先序 bug 修復、判斷力測試點在 marker 位置語意 |
| `02-logger-metrics-race.diff` | `8e87066` | `refactor` | 併發 metrics 的 mutex 補強＋Makefile 連動、跨檔一致性 |
| `03-lane-overlays-config.diff` | `7855389` | `config` | 純設定檔變更、lane overlay 語意 |
| `04-lane-topk-default.diff` | `d420a64` | `logic` | 零值 TopK 預設補丁、防靜默 no-op |
