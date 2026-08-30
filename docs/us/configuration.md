# Configuration

Which AI backend to select, which secrets to store, and every environment variable MRInspect reads.

[繁體中文版](../tw/configuration.md)

## AI providers

MRInspect supports three AI backends. Select one via `AI_PROVIDER`:

| Provider | `AI_PROVIDER` value | Default model | Key variable |
|---|---|---|---|
| Google Gemini | `gemini` | `gemini-2.5-pro` | `AI_PROVIDER_KEY` |
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
| `MRI_RAG_ON_NORMATIVE_EVICTION` | `warn` | Go runner policy when a full-mode normative section is evicted: `warn` \| `fail` |
| `MRI_PROMPT_BUDGET_FACTOR` | `0.8` | Positive float multiplied by the selected model's prompt limit. The 0.8 default also absorbs the estimator's measured ~11% systematic undercount. |
| `MRI_DIFF_PROMPT_SHARE` | `0.85` | Fraction of the effective model's prompt budget the diff may occupy; oversized diffs drop whole files (non-reviewable patterns first, then largest-first; hunks are never truncated) with drops disclosed in the prompt and the review footer. Invalid values fall back to the default. Measured incident data showed a model omitting required sections when the diff exceeded ~93% of the prompt; ~85% restored format compliance — the cap scales with the model's budget rather than a fixed KB value. |
| `MRI_REVIEW_DUMP_ENABLED` | _(unset)_ | Set to the exact string `true` to enable failure-only prompt/response dumps in the CI job log when review validation fails. Off by default: the default failure log carries only the validation error, the section titles found, pre/post-clean lengths, and 12-char sha256 prefixes of the prompt and response — never their content. Enable only for debugging, and only on repositories whose diffs contain no sensitive values. |
| `ANTHROPIC_MODEL` | `claude-sonnet-5` | Override the Anthropic model |
| `GEMINI_MODEL` | `gemini-2.5-pro` | Override the Gemini model |
| `OPENAI_MODEL` | `gpt-5.6` | Override the OpenAI model |
| `ANTHROPIC_MAX_TOKENS` | `4000` | Max output tokens for Anthropic |
| `GEMINI_MAX_TOKENS` | `8000` | Max output tokens for Gemini |
| `OPENAI_MAX_TOKENS` | `4000` | Max output tokens for OpenAI |
| `MRI_MODEL_LIMITS` | _(unset)_ | Go runner: comma-separated `model:tokens` entries merged over the built-in context-window defaults; a malformed entry (missing colon, non-positive/non-integer tokens) fails startup |
| `API_RETRY_ATTEMPTS` | `3` | Number of API retry attempts |
| `API_RETRY_DELAY_MS` | `1000` | Initial retry delay in milliseconds |
| `API_MAX_RETRY_DELAY_MS` | `10000` | Maximum retry delay (exponential backoff cap) |
| `API_TIMEOUT_MS` | `30000` | HTTP request timeout in milliseconds |
| `MAX_FILES_CHANGED` | `50` | Skip review if MR touches more than this many files |
| `AI_RETRY_ATTEMPTS` | `3` | Retry count if AI output fails validation |
| `LOG_LEVEL` | `info` | Log level: `debug` \| `info` |
| `AI_REVIEW_METRICS_FILE` | `./mrinspect-metrics.json` | Path for metrics JSON output |

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
