# Configuration

Which AI backend to select, which secrets to store, and every environment variable MRInspect reads.

[繁體中文版](../tw/configuration.md)

## AI providers

MRInspect supports three AI backends. Select one via `AI_PROVIDER`:

| Provider | `AI_PROVIDER` value | Default model | Key variable |
|---|---|---|---|
| Google Gemini | `gemini` | `gemini-3.1-pro-preview` | `AI_PROVIDER_KEY` |
| Anthropic Claude | `anthropic` | `claude-sonnet-5` | `AI_PROVIDER_KEY` |
| OpenAI | `openai` _(default)_ | `gpt-5.6` | `AI_PROVIDER_KEY` |

> **Note:** The superpowers layer always uses Anthropic (Claude Code CLI requirement). Set `ANTHROPIC_API_KEY` regardless of which provider the mrinspect runner uses.

## Required secrets

Set these in your CI/CD secrets store before use:

| Variable | Required by | Description |
|---|---|---|
| `AI_PROVIDER_KEY` | mrinspect | API key for the selected AI provider (Gemini / Anthropic / OpenAI) |
| `ANTHROPIC_API_KEY` | superpowers | Anthropic API key (Claude Code CLI always uses Anthropic) |
| `GITLAB_TOKEN` | all layers | GitLab token with `api` and `write_repository` scopes |

**GitLab:** `Settings → CI/CD → Variables` → mark each as **Protected** and **Masked**.

**GitHub:** `Settings → Secrets and variables → Actions → New repository secret`.

## Full variable reference

Any of the variables below can also be supplied via a `.env` file in the working directory. Values from `.env` only fill in variables that aren't already set — the process environment always wins. Never commit `.env`.

<details>
<summary>mrinspect variables</summary>

| Variable | Default | Description |
|---|---|---|
| `AI_PROVIDER` | `openai` | AI provider: `anthropic` \| `gemini` \| `openai` |
| `AI_PROVIDER_KEY` | _(required)_ | API key for the selected provider |
| `GITLAB_TOKEN` | _(required)_ | GitLab API token |
| `GITLAB_API_BASE` | `https://gitlab.com/api/v4` | GitLab API base URL (set for self-hosted) |
| `MRI_SERVICE_NAME` | `unknown` | Service name — must match a key in `projects/registry.yaml` |
| `MRI_SERVICE_TYPE` | `backend` | Service type: `backend` \| `frontend` \| `ai` \| `iac` |
| `MRI_REVIEW_MODE` | `single` | Go runner review mode: `single` \| `multi`; `multi` runs the configured lane fan-out |
| `MRI_LANE_CONCURRENCY` | `4` | Go runner maximum parallel lanes; must be a positive integer, otherwise a warning is logged and `4` is used |
| `IS_SELF_REFLECTION` | `false` | Set `true` to run a second AI validation pass |
| `PROJECTS_DIR` | `./projects` | Path to the projects directory |
| `MRI_RAG_STORE` | _(unset)_ | Explicit SQLite path for the Go `path` source; put `path` first in the source chain to give it precedence |
| `MRI_RAG_SOURCE` | `package,artifact,baked` | Go runner comma-separated store source chain, tried in order |
| `MRI_RAG_PACKAGE_VERSION` | `latest` | GitLab generic-package version; set an explicit version to pin the RAG store |
| `MRI_RAG_EMBEDDINGS` | `false` / _(unset; off)_ | Enables an optional semantic rerank whose value is unmeasured for small corpora. When enabled, resource-set text is sent to the selected external embeddings API during indexing, so provider usage costs and data egress apply. Before the API calls, indexing prints the cost line `embedding <chunk-count> chunks (~<request-count> requests)`. At retrieval time, unavailable or incompatible embeddings and provider failures degrade to BM25 with a visible reason in the review scope/footer. |
| `MRI_RAG_EMBED_KEY` | _(unset)_ | The single embedding credential for whichever embedding provider is selected; required when embeddings are enabled and independent from `AI_PROVIDER_KEY`. |
| `MRI_EMBED_PROVIDER` | _(unset)_ | Embedding provider: `openai` \| `gemini`. Models are fixed constants: `text-embedding-3-small` (OpenAI, 1536 dimensions) and `gemini-embedding-001` (Gemini, 768 dimensions). |
| `MRI_RAG_ON_NORMATIVE_EVICTION` | `warn` | Go runner policy when a full-mode normative section is evicted: `warn` \| `fail` |
| `MRI_PROMPT_BUDGET_FACTOR` | `0.8` | Positive float multiplied by the selected model's prompt limit. The 0.8 default also absorbs the estimator's measured ~11% systematic undercount. |
| `MRI_DIFF_PROMPT_SHARE` | `0.85` | Fraction of the effective model's prompt budget the diff may occupy; oversized diffs drop whole files (non-reviewable patterns first, then largest-first; hunks are never truncated) with drops disclosed in the prompt and the review footer. Invalid values fall back to the default. Measured incident data showed a model omitting required sections when the diff exceeded ~93% of the prompt; ~85% restored format compliance — the cap scales with the model's budget rather than a fixed KB value. |
| `MRI_REVIEW_DUMP_ENABLED` | _(unset)_ | Set to the exact string `true` to enable failure-only prompt/response dumps in the CI job log when review validation fails. Off by default: the default failure log carries only the validation error, the section titles found, pre/post-clean lengths, and 12-char sha256 prefixes of the prompt and response — never their content. Enable only for debugging, and only on repositories whose diffs contain no sensitive values. |
| `MRI_DAILY_TOKEN_BUDGET` | _(unset; disabled)_ | Offline eval budget comparison. The single-run token total is compared with this daily budget; no usage is retained across runs. Calls with unknown usage make the total a `≥` lower bound, and exceeding the budget logs a warning only. Recommended: `2500000` tokens/day, derived from 20 reviews/day × a high scenario of 100k input + 8k output tokens ≈ 2.16M plus headroom. Pricing research was retrieved 2026-08-30; measured calibration found one eval round of 9 small-diff reviews used ≥79.5k tokens, about 6–10k per review, so 2.5M is a safe upper bound. |
| `MRI_EVAL_ALLOW_CI` | _(unset)_ | Set to the exact string `true` to allow `mrinspect eval` inside CI; this guards against accidental triple-mode spend. |
| `ANTHROPIC_MODEL` | `claude-sonnet-5` | Override the Anthropic model |
| `GEMINI_MODEL` | `gemini-3.1-pro-preview` | Override the Gemini model. Existing accounts may override it back to `gemini-2.5-pro`; free-tier API keys have zero quota for 3.1 Pro, so use `gemini-2.5-flash` instead. |
| `OPENAI_MODEL` | `gpt-5.6` | Override the OpenAI model |
| `ANTHROPIC_MAX_TOKENS` | `4000` | Max output tokens for Anthropic |
| `GEMINI_MAX_TOKENS` | `8000` | Max output tokens for Gemini |
| `OPENAI_MAX_TOKENS` | `4000` | Max output tokens for OpenAI |
| `AI_PER_CALL_TIMEOUT_MS` | `120000` | Per-attempt timeout for each AI provider call in milliseconds; a timed-out attempt proceeds to retry |
| `MRI_AI_LOG_DIR` | _(unset; disabled)_ | Local directory for a per-run `ai-log-<timestamp>-<pid>.jsonl` transcript containing every AI provider attempt, including failures and retries. Example: `.ai-log/`. The files contain full prompts and responses, so keep them local and handle them as sensitive data. |
| `MRI_MODEL_LIMITS` | _(unset)_ | Go runner: comma-separated `model:tokens` entries merged over the built-in context-window defaults; a malformed entry (missing colon, non-positive/non-integer tokens) fails startup |
| `API_RETRY_ATTEMPTS` | `3` | Number of API retry attempts |
| `API_RETRY_DELAY_MS` | `1000` | Initial retry delay in milliseconds |
| `API_MAX_RETRY_DELAY_MS` | `10000` | Maximum retry delay (exponential backoff cap) |
| `API_TIMEOUT_MS` | `30000` | HTTP request timeout in milliseconds |
| `MAX_FILES_CHANGED` | `50` | Skip review if MR touches more than this many files |
| `AI_RETRY_ATTEMPTS` | `3` | Retry count if AI output fails validation |
| `LOG_LEVEL` | `info` | Log level: `debug` \| `info` |
| `AI_REVIEW_METRICS_FILE` | `./mrinspect-metrics.json` | Path for metrics JSON output |

### Offline retrieval check

`mrinspect eval -retrieval [-store PATH] [-report PATH]` replays each fixture in `eval/fixtures/` through the production lane query path against the local store and writes recall@k / MRR with reranking off and on to `eval/RETRIEVAL.md` (default). Relevant sections per fixture come from `eval/retrieval-golden.yaml`. The run makes no generation calls; with `MRI_RAG_EMBEDDINGS=true` it makes one embedding call per fixture and resource set. A store built from an older corpus is refused; rerun `mrinspect index` first. The report lists numbers only and draws no conclusion about retrieval quality.

</details>

<details>
<summary>Execution mode variables</summary>

mrinspect auto-detects its execution mode from `CI_PIPELINE_SOURCE`.

**Local MR mode** — triggered by `CI_PIPELINE_SOURCE == "merge_request_event"` (auto-set by GitLab):

| Variable | Set by | Description |
|---|---|---|
| `CI_PROJECT_ID` | GitLab (auto) | Project ID of the current repository |
| `CI_MERGE_REQUEST_IID` | GitLab (auto) | MR IID in the current repository |
| `CI_COMMIT_REF_NAME` | GitLab (auto) | Source branch |
| `CI_MERGE_REQUEST_TARGET_BRANCH_NAME` | GitLab (auto) | Target branch |

**Cross-repo trigger mode** — triggered by `CI_PIPELINE_SOURCE == "trigger"` (passed by the calling pipeline):

| Variable | Description |
|---|---|
| `MRI_PROJECT_ID` | Project ID of the repository being reviewed |
| `MRI_MR_IID` | MR IID in the target repository |
| `MRI_SOURCE_BRANCH` | Source branch of the MR |
| `MRI_TARGET_BRANCH` | Target branch of the MR |
| `MRI_SERVICE_NAME` | Service name for profile lookup |
| `MRI_SERVICE_TYPE` | Service type override |

</details>
