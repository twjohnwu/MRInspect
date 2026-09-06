# Evaluation

## What eval measures

The review evaluation runs each fixture diff through each review mode
(single, multi, reflect) and records the raw review output and token
breakdown. It is qualitative: a human reads the output. There is no scoring,
no precision/recall, and no pass/fail judgement of review quality.
`ok`/`failed` in the report mean only that the mode-run completed or errored
— not that the review itself was good.

## Modes × fixtures

| fixture | single | multi | reflect |
|---|---|---|---|
| 01-echo-cut-earliest-marker.diff | ok | ok | ok |
| 02-logger-metrics-race.diff | ok | ok | ok |
| 03-lane-overlays-config.diff | ok | ok | ok |
| 04-lane-topk-default.diff | ok | ok | ok |

See `eval/REPORT.md` for the full raw output per fixture/mode, and
`eval/fixtures/README.md` for what each fixture contains.

## Offline retrieval check

`mrinspect eval -retrieval` replays each fixture through the production lane
query path against a local store and computes recall@k and MRR per
fixture/lane/set, with reranking off and on. Relevant sections per fixture
come from `eval/retrieval-golden.yaml`. The numbers are written to
`eval/RETRIEVAL.md`. The report lists numbers only — it does not summarise
or draw any conclusion about retrieval quality; readers draw their own.

## How to rerun

- `mrinspect eval` — runs the review evaluation over `-fixtures`
  (default `eval/fixtures`) and writes `-report` (default `eval/REPORT.md`).
- `mrinspect eval -retrieval [-store PATH] [-report PATH]` — runs the
  offline retrieval check; requires a store built from the current corpus,
  so run `mrinspect index` first.

In practice, the generation quota (free tier) fits roughly six mode-runs per
day, so fixtures get split across days: e.g. `-fixtures` pointed at a subset
with `-report` set to a separate path per run, then the resulting reports
merged by hand (see the `Merged from two runs:` line in `eval/REPORT.md`).
