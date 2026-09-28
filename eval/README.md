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
query path against a local store. Fixtures live at
`eval/retrieval-fixtures/<system>/` — the subdirectory name is the system;
`-fixtures` only overrides this when passed explicitly (the review eval above
still defaults to `eval/fixtures`). For every fixture/lane/set it reports three
arms — `off` (BM25 top-k), `shuffle` (the same 4×k BM25 candidate pool
reordered with fixed seeds, averaged over 20 runs; a control arm with no
embedding calls) and `on` (embedding rerank) — over three golden tiers from
`eval/retrieval-golden.yaml`: `relevant` and `paraphrase` sections score
recall@k and MRR, `distractors` sections are counted when they appear in the
top k. The numbers are written to `eval/RETRIEVAL.md`: a header line
`retrieve_ms: off_mean=<int> on_mean=<int> (n=<int>)`, a main table with a
leading `system` column, and two aggregate tables — `## Mean by k` (k=1, 3,
TopK; recall only) and `## Distractors by category` (scope/version/
responsibility/lexical/neighbor). The report lists numbers only — it does not
summarise or draw any conclusion about retrieval quality; readers draw their
own.

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
