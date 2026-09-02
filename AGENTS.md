# MRInspect — Claude Code Context

AI-powered GitLab MR review tool implemented as a Go binary that posts structured review comments to MRs. A separate superpowers layer runs Claude Code CLI skills on top.

---

## Repo Layout

```
mrinspect/
├── cmd/mrinspect/
│   └── main.go       # Go entry point — composition root, wires all deps
├── internal/
│   ├── interfaces/   # All Go interfaces (IGitLabClient, IDiffFetcher, IProjectLoader, IPromptComposer, IReviewValidator, IErrorHandler)
│   ├── reviewer/     # MRInspectReviewer — orchestrator, depends only on interfaces
│   ├── ai/           # Provider interface + Anthropic, Gemini, OpenAI implementations
│   ├── config/       # Config struct, env var loading
│   ├── diff/         # LocalDiffFetcher, APIDiffFetcher, FallbackDiffFetcher, ConvertChangesToDiff
│   ├── gitlab/       # GitLab HTTP client (implements IGitLabClient)
│   ├── lane/         # Ordered lane registry, composition, fan-out, merge, and rendering
│   │   └── hunk/     # Changed-line lookup for rendered findings
│   ├── project/      # YAML project loader (implements IProjectLoader)
│   ├── prompt/       # Prompt composer + legacy templates
│   ├── rag/          # Store source resolution and retrieval contracts
│   │   ├── chunk/    # Markdown/structured chunking and token estimation
│   │   ├── intake/   # Resource walking and denylist filtering
│   │   ├── resources/# Resource-set loading, selectors, and CI coverage checks
│   │   └── sqlite/   # SQLite indexing, storage, and retrieval backend
│   ├── ragcmd/       # `mrinspect index` parsing, checks, and exit-code policy
│   ├── ragwire/      # Production adapters from RAG stores to review lanes
│   ├── testfake/     # Shared Go fakes for reviewer and RAG tests
│   ├── validator/    # Input validation, env var access (implements IReviewValidator)
│   ├── errors/       # Error categorization, MR comment generation
│   └── logger/       # JSON logging + metrics collection
├── projects/         # Review projects
│   ├── registry.yaml # service-name → system-dir mapping
│   ├── resources.yaml# Named RAG resource sets
│   ├── lanes.yaml    # Canonical ordered review lanes
│   ├── _lanes/       # Static lane prompt preambles
│   ├── _shared/      # docs injected into every review prompt
│   └── <system>/     # system.yaml + .md docs + optional lanes.yaml overlay
├── templates/        # GitLab CI reusable template
├── Dockerfile        # Multi-stage Go build
└── Makefile          # Go build targets
```

---

## Build & Test Commands

```bash
make build              # → ./bin/mrinspect
make test               # go test ./...
make lint               # golangci-lint run
make docker             # builds the local Docker image
```

---

## Architecture (SOLID)

- **Interfaces** live in `internal/interfaces/` — `IGitLabClient`, `IDiffFetcher`, `IProjectLoader`, `IPromptComposer`, `IReviewValidator`, `IErrorHandler`
- **`MRInspectReviewer`** (`internal/reviewer/reviewer.go`) is the orchestrator — holds only interface fields, no concrete pointer types (except `*logger.Logger` for metrics lifecycle)
- **`cmd/mrinspect/main.go`** is the composition root — the only place that constructs concrete types and selects which `IDiffFetcher` to inject based on `CrossRepo.Enabled`
- **Diff fetching** is split into three focused types: `LocalDiffFetcher` (go-git), `APIDiffFetcher` (GitLab API), and `FallbackDiffFetcher` (tries local, falls back to API) — all implement `IDiffFetcher`
- **AI providers** are interchangeable via the existing `ai.Provider` interface; selected by `config.AIProvider`
- Concrete types (`*gitlab.Client`, `*validator.Validator`, etc.) satisfy their interfaces implicitly — no changes to those packages were needed

---

## CI Job Names

Reusable review jobs are defined in `templates/ai-review-template.yaml`; the repository pipeline also defines its RAG index job in `.gitlab-ci.yml`:

| Job | What it runs |
|---|---|
| `.mrinspect-go-review` | Go binary (`ghcr.io/twjohnwu/mrinspect:v0.2.0` Docker image) |
| `.superpowers-review` | Claude Code CLI + superpowers plugin |
| `.mrinspect-full` | Both layers in parallel |
| `index` | Builds `rag-index/mrinspect-rag.sqlite` on schedules, pushes to `main` that change `_shared` or either sample-system resource directory, or manual runs; publishes it for 21 days |

Callers use `extends: .mrinspect-go-review` (or `.mrinspect-full` for both layers).

---

## Projects System

1. `projects/registry.yaml` maps service names → system directory names
2. `projects/<system>/system.yaml` describes the system (name, frameworks, service type overrides)
3. `projects/resources.yaml` declares ordered, named sets with tags and required `retrieval` or `full` modes; lanes resolve them by set name and/or tag
4. `projects/lanes.yaml` declares ordered lanes with `id`, `enabled`, `template`, `intent`, and resource selectors; empty selectors make a diff-only lane
5. `projects/_lanes/*.tmpl.md` are static lane prompt preambles
6. `projects/<system>/lanes.yaml` overlays canonical lanes by ID: replacements keep position and new IDs append
7. Multi mode names configuration degradations when lane files are missing, invalid, or have no enabled lane; lane prompt failures remain named lane failures rather than silently switching templates
8. Single-review project documents still come from `projects/<system>/*.md` and `projects/_shared/*.md`; multi-lane documents enter only through selected resource sets

The Go binary uses the `projects/` directory. In CI, projects are baked into the Docker image; local runs read `PROJECTS_DIR`, which defaults to `./projects`.

---

## Key Environment Variables

| Variable | Default | Notes |
|---|---|---|
| `AI_PROVIDER_KEY` | _(required)_ | API key for Gemini / Anthropic / OpenAI |
| `GITLAB_TOKEN` | _(required)_ | GitLab token with `api` + `write_repository` |
| `AI_PROVIDER` | `openai` | `gemini` \| `anthropic` \| `openai` |
| `MRI_SERVICE_NAME` | `unknown` | Must match a key in `projects/registry.yaml` |
| `MRI_SERVICE_TYPE` | `backend` | `backend` \| `frontend` \| `ai` \| `iac` |
| `MRI_REVIEW_MODE` | `single` | Go runner: `single` \| `multi`; `multi` runs configured lane fan-out |
| `MRI_LANE_CONCURRENCY` | `4` | Positive integer; invalid values are logged and defaulted to `4` |
| `IS_SELF_REFLECTION` | `false` | Set `true` for a second AI validation pass |
| `PROJECTS_DIR` | `./projects` | Override projects directory path |
| `MRI_RAG_STORE` | _(unset)_ | Explicit SQLite path for the Go `path` source; put `path` first in the source chain for precedence |
| `MRI_RAG_SOURCE` | `package,artifact,baked` | Comma-separated Go store source chain, tried in order |
| `MRI_RAG_PACKAGE_VERSION` | `latest` | GitLab generic-package version; set explicitly to pin the store |
| `MRI_RAG_ON_NORMATIVE_EVICTION` | `warn` | `warn` \| `fail` policy for evicted full-mode normative sections |
| `MRI_PROMPT_BUDGET_FACTOR` | `0.8` | Positive float multiplied by the selected model's prompt limit |
| `MRI_MODEL_LIMITS` | _(unset)_ | Go runner: `model:tokens,model:tokens` entries merged over the built-in defaults; malformed entries fail startup |
| `CI_PROJECT_ID` | _(auto by GitLab)_ | Project ID for local MR mode |
| `CI_MERGE_REQUEST_IID` | _(auto by GitLab)_ | MR IID for local MR mode |

Cross-repo trigger mode uses `MRI_PROJECT_ID`, `MRI_MR_IID`, `MRI_SOURCE_BRANCH`, `MRI_TARGET_BRANCH` instead.

---

## Test Patterns

- Table-driven tests mirror Go conventions
- Shared test doubles live in `internal/testfake/`
- Integration tests use the `integration` build tag and run via `make test-integration`
