# MRInspect

AI-powered merge request code review for GitLab, powered by Claude, Gemini, or OpenAI.

MRInspect runs as a non-blocking CI/CD job. It reads your code diff, loads a team-specific review project, and posts a structured review comment directly to the MR — no human reviewer required for the first pass.

---

## How It Works

```
MR opened / updated
        │
        ▼
  GitLab pipeline
        │
   ┌────┴──────────────────────────────────────┐
   ▼                    ▼                       ▼
mrinspect (Go)    mrinspect (TypeScript)   superpowers layer
  │                    │                        │
  ├─ loads profile     ├─ loads profile         ├─ /code-review:code-review
  ├─ composes prompt   ├─ composes prompt       ├─ /security-review
  ├─ calls AI          ├─ calls AI              └─ /pr-review-toolkit:review-pr
  ├─ self-reflection   ├─ self-reflection            (5 parallel sub-agents)
  └─ posts 1 MR comment└─ posts 1 MR comment   posts up to 3 MR comments
```

**Layer 1a — mrinspect Go binary**
Resolves the service name to a system project (`projects/registry.yaml`), loads the matching YAML + Markdown review standards, and composes a context-aware prompt. Calls the configured AI provider (Gemini by default) with retry and exponential backoff. Optionally runs a self-reflection second pass where the AI validates its own review against the project standards. Posts one structured MR comment. Follows SOLID principles — all collaborators are wired via interfaces in `internal/interfaces/`; `cmd/mrinspect/main.go` is the composition root.

**Layer 1b — mrinspect TypeScript runner**
Same project loading, prompt composition, AI provider, and self-reflection logic as the Go binary — implemented in TypeScript following SOLID principles. Runs via `npx tsx review.ts` with no compile step required. Uses `node:22` in CI (no pre-built Docker image needed). Ideal when you prefer a no-build setup or want to extend the reviewer in TypeScript.

**Layer 2 — superpowers**
Installs Claude Code CLI and the superpowers plugin, then runs three skills in sequence:
- `/code-review:code-review` — logic bugs, conventions, findings table + verdict
- `/security-review` — OWASP Top 10, secrets exposure, dependency CVEs
- `/pr-review-toolkit:review-pr` — dispatches five parallel sub-agents (code reviewer, type design analyzer, silent failure hunter, comment analyzer, test coverage analyzer) and posts their aggregated findings

All layers are `allow_failure: true` — they are advisory and never block a merge.

Go review notes carry the stable `<!-- mrinspect:review -->` marker. On a rerun, mrinspect looks for a marked note whose author ID or username matches the current GitLab token user and updates that note instead of stacking another one; if it cannot identify a matching owned note, it posts a new note.

---

## Review Layers at a Glance

| Layer | Image | Posts | Requires |
|---|---|---|---|
| mrinspect (Go) | `mrinspect:latest` | 1 structured MR comment | `AI_PROVIDER_KEY`, `GITLAB_TOKEN` |
| mrinspect (TypeScript) | `node:22` | 1 structured MR comment | `AI_PROVIDER_KEY`, `GITLAB_TOKEN` |
| superpowers | `node:22` (Claude Code CLI) | Up to 3 MR comments | `ANTHROPIC_API_KEY`, `GITLAB_TOKEN` |

---

## AI Providers

Both runners support three AI backends. Select one via `AI_PROVIDER`:

| Provider | `AI_PROVIDER` value | Default model | Key variable |
|---|---|---|---|
| Google Gemini | `gemini` _(default)_ | `gemini-2.5-pro` | `AI_PROVIDER_KEY` |
| Anthropic Claude | `anthropic` | `claude-3-5-sonnet-20241022` | `AI_PROVIDER_KEY` |
| OpenAI | `openai` | `gpt-5` | `AI_PROVIDER_KEY` |

> **Note:** The superpowers layer always uses Anthropic (Claude Code CLI requirement). Set `ANTHROPIC_API_KEY` regardless of which provider the mrinspect runner uses.

---

## Installation & Build

### Prerequisites

- Go 1.23+ (for the Go binary)
- Node.js 22+ (for the TypeScript runner)
- Docker (for containerized Go deployments)
- Claude Code CLI — superpowers layer only: `npm install -g @anthropic-ai/claude-code`

### Go binary

```bash
git clone https://github.com/twjohnwu/MRInspect
cd mrinspect
make build        # compiles to ./bin/mrinspect
```

### Build and push the Docker image

```bash
make docker       # builds mrinspect:latest locally

# Tag and push to your registry:
docker build -t registry.example.com/mrinspect:latest .
docker push registry.example.com/mrinspect:latest
```

The Docker image is a multi-stage build: the Go binary is compiled in `golang:1.23-alpine` and copied into an `alpine:3.20` runtime image (~15 MB). The `projects/` directory is baked into the image at `/app/projects/`.

### TypeScript runner

```bash
cd mrinspect
npm install       # one-time dependency install (no build step)
npm test          # run Jest test suite
```

### Run locally

**Go binary:**

```bash
AI_PROVIDER_KEY=your-key \
GITLAB_TOKEN=your-token \
CI_PROJECT_ID=123 \
CI_MERGE_REQUEST_IID=45 \
  ./bin/mrinspect
```

**TypeScript runner (no Docker required):**

```bash
AI_PROVIDER_KEY=your-key \
GITLAB_TOKEN=your-token \
CI_PROJECT_ID=123 \
CI_MERGE_REQUEST_IID=45 \
  npx tsx review.ts
```

### `mrinspect index`

Build the SQLite RAG store with `./bin/mrinspect index --out .rag/mrinspect-rag.sqlite`. `--dry-run` reports resource and file statistics without writing a store, while `--check` validates an existing store at the `--out` path; `--check` and `--dry-run` cannot be combined. The default output is `.rag/mrinspect-rag.sqlite`.

| Exit code | Meaning |
|---|---|
| `0` | Index, dry run, or store check succeeded |
| `1` | Configuration, argument, resource-loading, or indexing failure |
| `2` | Usage conflict or no resource sets resolved |
| `3` | Index completed with one or more file failures |
| `4` | Existing-store check failed |
| `5` | Selected backend does not support indexing |

---

## Repository Structure

```
mrinspect/
├── cmd/mrinspect/
│   └── main.go           # Go binary entry point — composition root, wires all deps
├── internal/
│   ├── interfaces/       # All Go interfaces (IGitLabClient, IDiffFetcher, etc.)
│   ├── reviewer/         # MRInspectReviewer — orchestrator, depends only on interfaces
│   ├── ai/               # Provider interface + Anthropic, Gemini, OpenAI implementations
│   ├── config/           # Config struct, env var loading
│   ├── diff/             # LocalDiffFetcher, APIDiffFetcher, FallbackDiffFetcher
│   ├── gitlab/           # GitLab HTTP client (implements IGitLabClient)
│   ├── lane/             # Ordered lane loading, prompt composition, fan-out, merge, rendering
│   ├── project/          # YAML profile loader (implements IProjectLoader)
│   ├── prompt/           # Prompt composer + legacy templates
│   ├── rag/              # Resource loading, store sources, retrieval, chunking, SQLite backend
│   ├── ragcmd/           # `mrinspect index` command parsing and exit-code policy
│   ├── ragwire/          # Production adapters connecting RAG to review lanes
│   ├── testfake/         # Shared Go test doubles
│   ├── validator/        # Input validation, env var access (implements IReviewValidator)
│   ├── errors/           # Error categorization, MR comment generation
│   └── logger/           # JSON logging + metrics collection
├── review.ts             # TypeScript entry point
├── src/
│   ├── ai/               # AnthropicProvider, GeminiProvider, OpenAIProvider
│   ├── config/           # ReviewConfig (loadConfig)
│   ├── diff/             # GitDiffFetcher, ApiDiffFetcher
│   ├── gitlab/           # GitLabClient
│   ├── project/          # YamlProjectLoader
│   ├── prompt/           # PromptComposer
│   ├── review/           # MRReviewer (orchestrator)
│   ├── error/            # ErrorHandler
│   ├── interfaces/       # All interface types (DIP)
│   ├── factory.ts        # Composition root — wires all dependencies
│   └── types.ts          # Shared types
├── tests/                # Jest test suite
├── projects/             # Review projects (shared by both runners)
│   ├── registry.yaml     # Service-name → system-directory mapping
│   ├── resources.yaml    # Named RAG resource sets
│   ├── lanes.yaml        # Canonical ordered review lanes
│   ├── _lanes/           # Static lane prompt templates
│   ├── _shared/          # Shared review documents
│   └── <system>/         # System config, docs, and optional lane overlay
├── docs/                 # Design and architecture documentation
├── templates/            # GitLab CI reusable template
├── Dockerfile            # Multi-stage Go build
├── Makefile              # Go build targets
├── package.json          # TypeScript dependencies
└── tsconfig.json         # TypeScript compiler config
```

---

## CI/CD Variables

### Required secrets

Set these in your CI/CD secrets store before use:

| Variable | Required by | Description |
|---|---|---|
| `AI_PROVIDER_KEY` | mrinspect (Go + TypeScript) | API key for the selected AI provider (Gemini / Anthropic / OpenAI) |
| `ANTHROPIC_API_KEY` | superpowers | Anthropic API key (Claude Code CLI always uses Anthropic) |
| `GITLAB_TOKEN` | all layers | GitLab token with `api` and `write_repository` scopes |

**GitLab:** `Settings → CI/CD → Variables` → mark each as **Protected** and **Masked**.

**GitHub:** `Settings → Secrets and variables → Actions → New repository secret`.

---

### Full variable reference

<details>
<summary>mrinspect runner variables (Go + TypeScript; Go-only variables noted)</summary>

| Variable | Default | Description |
|---|---|---|
| `AI_PROVIDER` | `gemini` | AI provider: `anthropic` \| `gemini` \| `openai` |
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
| `MRI_PROMPT_BUDGET_FACTOR` | `0.8` | Positive float multiplied by the selected model's prompt limit |
| `ANTHROPIC_MODEL` | `claude-3-5-sonnet-20241022` | Override the Anthropic model |
| `GEMINI_MODEL` | `gemini-2.5-pro` | Override the Gemini model |
| `OPENAI_MODEL` | `gpt-5` | Override the OpenAI model |
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

---

## How Other Repos Trigger MRInspect

### Option A — GitLab `include` (same GitLab instance, recommended)

Include the reusable template and extend the job:

```yaml
# your-repo/.gitlab-ci.yml

include:
  - project: 'twjohnwu/MRInspect'
    ref: main
    file: 'templates/ai-review-template.yaml'

ai-review:
  extends: .mrinspect-full       # runs all layers in parallel
  variables:
    MRI_SERVICE_NAME: my-service
    MRI_SERVICE_TYPE: backend  # backend | frontend | ai | iac
```

To run only one layer:

```yaml
ai-review-go:
  extends: .mrinspect-go-review  # Go binary only
  variables:
    MRI_SERVICE_NAME: my-service

ai-review-ts:
  extends: .mrinspect-ts-review  # TypeScript runner only
  variables:
    MRI_SERVICE_NAME: my-service

ai-review-superpowers:
  extends: .superpowers-review   # Claude Code skills only
  variables:
    MRI_SERVICE_NAME: my-service
```

### Option B — GitLab pipeline trigger (cross-repo, works from any Git host)

1. In the mrinspect project: `Settings → CI/CD → Pipeline triggers` → create a trigger token.
2. Store the token as `MRINSPECT_TRIGGER_TOKEN` in your calling repo's CI/CD variables.
3. Add a trigger job to your repo:

```yaml
# your-repo/.gitlab-ci.yml

trigger-ai-review:
  stage: review
  script:
    - |
      curl --silent --fail --request POST \
        --form "token=$MRINSPECT_TRIGGER_TOKEN" \
        --form "ref=main" \
        --form "variables[MRI_PROJECT_ID]=$CI_PROJECT_ID" \
        --form "variables[MRI_MR_IID]=$CI_MERGE_REQUEST_IID" \
        --form "variables[MRI_SOURCE_BRANCH]=$CI_COMMIT_REF_NAME" \
        --form "variables[MRI_TARGET_BRANCH]=$CI_MERGE_REQUEST_TARGET_BRANCH_NAME" \
        --form "variables[MRI_SERVICE_NAME]=my-service" \
        --form "variables[MRI_SERVICE_TYPE]=backend" \
        "https://gitlab.com/api/v4/projects/<MRINSPECT_PROJECT_ID>/trigger/pipeline"
  allow_failure: true
  rules:
    - if: '$CI_PIPELINE_SOURCE == "merge_request_event"'
```

Replace `<MRINSPECT_PROJECT_ID>` with the numeric project ID of your mrinspect deployment.

### Option C — GitHub Actions trigger

Store `MRINSPECT_TRIGGER_TOKEN` and `GITLAB_PROJECT_ID` as GitHub repository secrets, then add a workflow step:

```yaml
# .github/workflows/ai-review.yml

name: AI Code Review
on:
  pull_request:
    types: [opened, synchronize, reopened]

jobs:
  trigger-mrinspect:
    runs-on: ubuntu-latest
    steps:
      - name: Trigger MRInspect
        run: |
          curl --silent --fail --request POST \
            --form "token=${{ secrets.MRINSPECT_TRIGGER_TOKEN }}" \
            --form "ref=main" \
            --form "variables[MRI_PROJECT_ID]=${{ secrets.GITLAB_PROJECT_ID }}" \
            --form "variables[MRI_MR_IID]=${{ github.event.pull_request.number }}" \
            --form "variables[MRI_SOURCE_BRANCH]=${{ github.head_ref }}" \
            --form "variables[MRI_TARGET_BRANCH]=${{ github.base_ref }}" \
            --form "variables[MRI_SERVICE_NAME]=my-service" \
            --form "variables[MRI_SERVICE_TYPE]=backend" \
            "https://gitlab.com/api/v4/projects/${{ secrets.GITLAB_PROJECT_ID }}/trigger/pipeline"
```

**GitHub secrets to create:**

| Secret | Value |
|---|---|
| `MRINSPECT_TRIGGER_TOKEN` | Pipeline trigger token from the mrinspect GitLab project |
| `GITLAB_PROJECT_ID` | Numeric project ID of your mrinspect deployment |

> The review comment will appear in the GitLab MR (not the GitHub PR), since mrinspect posts via the GitLab API.

---

## Project System

Projects let each team define their own review standards. mrinspect loads the matching project for a service and injects the documentation into the AI prompt as context.

```
projects/
├── registry.yaml               # service-name → system-name mapping
├── resources.yaml              # named resource sets selected by name or tag
├── lanes.yaml                  # canonical ordered lane declarations
├── _lanes/                     # static lane prompt preambles
│   ├── code-diff.tmpl.md
│   ├── spec-conformance.tmpl.md
│   └── standards.tmpl.md
├── _shared/
│   └── coding-standards.md     # standards applied to every system
├── margherita-pizza/           # sample system (Go + PostgreSQL + gRPC)
│   ├── system.yaml
│   ├── lanes.yaml              # per-system lane overlay
│   ├── architecture.md
│   └── review-focus.md
└── fried-chicken/              # sample system (Go + Kafka + MongoDB)
    ├── system.yaml
    ├── lanes.yaml              # per-system lane overlay
    ├── architecture.md
    └── review-focus.md
```

**`projects/registry.yaml`** — maps service names to system directories:

```yaml
defaultSystem: my-system

services:
  payments-api: my-system
  auth-service: my-system
  dashboard: another-system
```

**`projects/my-system/system.yaml`** — describes the system:

```yaml
name: My System
description: >
  Brief description of what this system does and its architecture.
defaultServiceType: backend
frameworks:
  - Go
  - PostgreSQL
serviceTypeOverrides:
  my-dashboard: frontend   # override per-service if needed
```

**`projects/my-system/review-focus.md`** — guides the AI reviewer:

```markdown
# My System Review Focus

## Critical Checks
- All database writes must use transactions
- gRPC calls must propagate the incoming context

## Common Bugs
- Missing `defer tx.Rollback()` after `db.Begin()`
```

**`projects/resources.yaml`** — declares ordered, named resource sets. Lanes select sets by `name` and/or `tags`; every set must choose `mode: retrieval` for indexed chunk retrieval or `mode: full` for whole-document injection, and declares the source `paths` plus optional include/exclude patterns.

**`projects/lanes.yaml`** — declares the ordered multi-review lanes. Each lane requires `id`, `enabled`, `template`, `intent`, and `resources`; selectors may contain explicit `sets` and `tags`. A lane with both selector lists empty receives the diff without external resource documents.

**`projects/_lanes/*.tmpl.md`** — static preambles prepended to their lane prompts. A system can add `projects/<system>/lanes.yaml`; overlay entries merge by lane ID, replacements retain the canonical position, and new IDs append in overlay declaration order.

In multi mode, missing or invalid lane configuration and configurations with no enabled lanes degrade to the single-review path with a named reason in the review. A lane prompt-composition failure is reported as a named lane failure rather than silently replaced by a legacy template.

---

## Makefile & Scripts

### Go (Makefile)

| Command | Description |
|---|---|
| `make build` | Compile binary to `./bin/mrinspect` |
| `make test` | Run Go unit tests (`go test ./...`) |
| `make test-integration` | Run integration tests (requires `-tags integration`) |
| `make lint` | Run `golangci-lint` |
| `make lint-lane-ids` | Forbid lane-ID string-literal branching in non-test Go files under `internal/` and `cmd/` |
| `make docker` | Build Docker image `mrinspect:latest` |
| `make clean` | Remove `./bin` build artifacts |

### TypeScript (npm)

| Command | Description |
|---|---|
| `npm install` | Install dependencies |
| `npm test` | Run Jest test suite |
| `npx tsc --noEmit` | Type-check without emitting files |
| `npx tsx review.ts` | Run TypeScript reviewer directly |
