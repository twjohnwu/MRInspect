# AGENTS.md — mrinspect

Project-level guidance for AI coding agents. Read this (and `CLAUDE.md`) before making changes.

## What this project is

AI-powered GitLab MR code reviewer. Two independent runners share the same `projects/` directory and each post one structured review comment to the MR:

- **Go binary** (`cmd/mrinspect/`) — built into a Docker image, runs in GitLab CI
- **TypeScript runner** (`review.ts` + `src/`) — runs via `npx tsx review.ts`, no compile step

A third layer (**superpowers**) installs Claude Code CLI and runs `/code-review:code-review`, `/security-review`, and `/pr-review-toolkit:review-pr` as separate CI jobs.

All three layers are `allow_failure: true` — advisory only, never block a merge.

## Layout

```
cmd/mrinspect/main.go     # Go composition root — only place that calls `new` for Go
internal/
  interfaces/             # All Go interfaces (IGitLabClient, IDiffFetcher, IProjectLoader, …)
  reviewer/               # MRInspectReviewer — orchestrator, depends only on interfaces
  ai/                     # Provider interface + Anthropic, Gemini, OpenAI impls
  config/                 # Config struct + env var loading
  diff/                   # LocalDiffFetcher, APIDiffFetcher, FallbackDiffFetcher
  gitlab/                 # GitLab HTTP client
  project/                # YAML project loader
  prompt/                 # Prompt composer + legacy templates
  validator/              # Input validation + env var access
  errors/                 # Error categorization + MR comment generation
  logger/                 # JSON logging + metrics collection
review.ts                 # TypeScript composition root — only place that calls `new` for TS
src/
  interfaces/             # All TS interfaces (IAIProvider, IGitLabClient, IDiffFetcher, …)
  review/MRReviewer.ts    # Orchestrator — no `new` inside
  factory.ts              # Wires all TS deps
  ai/                     # AnthropicProvider, GeminiProvider, OpenAIProvider
  config/ReviewConfig.ts  # loadConfig() — throws early on missing required vars
  diff/                   # ApiDiffFetcher, GitDiffFetcher
  gitlab/GitLabClient.ts
  project/YamlProjectLoader.ts
  prompt/MarkdownPromptComposer.ts
  error/ErrorHandler.ts
  types.ts                # Config, AIProviderName, shared types
tests/                    # Jest tests
projects/                 # Review projects (shared by both runners)
  registry.yaml           # service-name → system-directory mapping
  _shared/                # docs injected into every review prompt
  <system>/               # system.yaml + *.md docs per system
templates/
  ai-review-template.yaml # GitLab CI reusable job template
Dockerfile                # Multi-stage Go build
Makefile
package.json              # TypeScript deps (tsx, jest, ts-jest)
```

## Build & test commands

### Go

```bash
make build          # → ./bin/mrinspect
make test           # go test ./...
make docker         # docker build -t mrinspect:latest .
```

### TypeScript

```bash
npm install         # one-time setup
npm test            # Jest suite
npx tsc --noEmit    # type-check only
npx tsx review.ts   # run reviewer directly (requires env vars)
```

## Architecture invariants — do not break

**SOLID boundaries (Go)**

- `internal/interfaces/` is the only package that defines interfaces. New interfaces go there.
- `MRInspectReviewer` (`internal/reviewer/reviewer.go`) holds only interface fields. It must never import a concrete type from another `internal/` package.
- `cmd/mrinspect/main.go` is the only place that selects concrete implementations and wires them together. No `new` inside `reviewer.go`.
- All three `IDiffFetcher` impls (`LocalDiffFetcher`, `APIDiffFetcher`, `FallbackDiffFetcher`) must maintain the compile-time `var _ interfaces.IDiffFetcher = ...` guard in `internal/diff/fallback.go`.

**SOLID boundaries (TypeScript)**

- `src/interfaces/` defines all interfaces. No `new` calls inside `MRReviewer`.
- `src/factory.ts` is the only composition root — the sole file that calls `new` and wires deps.

**AI providers**

Both runners select providers by the `AI_PROVIDER` env var. To add a new provider: implement `ai.Provider` (Go) / `IAIProvider` (TypeScript), add a case in `main.go` and `factory.ts`. Do not touch `reviewer.go` or `MRReviewer.ts`.

**Projects system**

`projects/registry.yaml` maps service names → system directories. Both runners read the same files. In CI the Go binary bakes projects into the Docker image; the TypeScript runner reads from disk (`PROJECTS_DIR`, default `./projects`). Falls back to built-in legacy templates when no project matches.

## Environment variables

| Variable | Required | Default | Notes |
|---|---|---|---|
| `AI_PROVIDER_KEY` | ✓ | — | Key for Gemini / Anthropic / OpenAI |
| `ANTHROPIC_API_KEY` | superpowers only | — | Claude Code CLI layer |
| `GITLAB_TOKEN` | ✓ | — | `api` + `write_repository` scopes |
| `AI_PROVIDER` | | `gemini` | `gemini` \| `anthropic` \| `openai` |
| `TARGET_SERVICE_NAME` | | `unknown` | Must match a key in `registry.yaml` |
| `TARGET_SERVICE_TYPE` | | `backend` | `backend` \| `frontend` \| `ai` \| `iac` |
| `IS_SELF_REFLECTION` | | `false` | Second AI validation pass |
| `PROJECTS_DIR` | | `./projects` | Override projects directory |
| `CI_PROJECT_ID` | | _(GitLab auto)_ | Set manually for local runs |
| `CI_MERGE_REQUEST_IID` | | _(GitLab auto)_ | Set manually for local runs |

Cross-repo trigger mode uses `TARGET_PROJECT_ID`, `TARGET_MR_IID`, `SOURCE_BRANCH`, `TARGET_BRANCH` instead of the standard CI vars. See `.env.example` for the full list.

## CI job names (from `templates/ai-review-template.yaml`)

| Job | What it runs |
|---|---|
| `.mrinspect-go-review` | Go binary (`mrinspect:latest`) |
| `.mrinspect-ts-review` | TypeScript (`node:22`, `npx tsx review.ts`) |
| `.superpowers-review` | Claude Code CLI + superpowers plugin |
| `.mrinspect-full` | All three layers in parallel |

Callers use `extends: .mrinspect-go-review` (or `-ts-review`, `-full`).

## Test patterns

- **Go**: table-driven tests; run with `make test` or `go test ./...`
- **TypeScript**: `assertValidationCode(fn, 'CODE')` checks `(error as ValidationError).code` — do not use `toThrow('CODE')` since the code is not in the message string
- `tests/projectLoader.test.ts` uses the real `projects/` directory — no mocks
- `tests/config.test.ts` uses a `withEnv()` helper to isolate env var state per test

## Adding a new review project

1. Add an entry to `projects/registry.yaml`: `my-service: my-system`
2. Create `projects/my-system/system.yaml` (name, frameworks, defaultServiceType, serviceTypeOverrides)
3. Add `projects/my-system/*.md` files (architecture, review-focus, etc.)
4. Docs in `projects/_shared/` are injected automatically into every review

## Do not do this

- ❌ Add `new` calls inside `MRInspectReviewer` (Go) or `MRReviewer` (TypeScript) — they must depend only on interfaces
- ❌ Define interfaces outside `internal/interfaces/` (Go) or `src/interfaces/` (TypeScript)
- ❌ Read env vars directly in `reviewer.go` or `MRReviewer.ts` — use the injected validator/config
- ❌ Commit `node_modules/`, `dist-scripts/`, or `.env` files — all gitignored
- ❌ Skip hooks (`--no-verify`) or force-push
