# MRInspect — Claude Code Context

AI-powered GitLab MR review tool. Two independent runners (Go binary + TypeScript) share the same projects and post structured review comments to MRs. A separate superpowers layer runs Claude Code CLI skills on top.

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
│   ├── project/      # YAML project loader (implements IProjectLoader)
│   ├── prompt/       # Prompt composer + legacy templates
│   ├── validator/    # Input validation, env var access (implements IReviewValidator)
│   ├── errors/       # Error categorization, MR comment generation
│   └── logger/       # JSON logging + metrics collection
├── review.ts         # TypeScript entry point (npx tsx review.ts)
├── src/              # TypeScript source
│   ├── ai/           # AnthropicProvider, GeminiProvider, OpenAIProvider
│   ├── config/       # loadConfig() — reads env vars, returns Config
│   ├── diff/         # GitDiffFetcher (local git), ApiDiffFetcher (cross-repo)
│   ├── gitlab/       # GitLabClient — MR details, diff, post note
│   ├── project/      # YamlProjectLoader — registry.yaml + system dirs
│   ├── prompt/       # PromptComposer — assembles AI prompt from project docs
│   ├── review/       # MRReviewer — SOLID orchestrator (no new inside)
│   ├── error/        # ErrorHandler — posts errors back to MR
│   ├── interfaces/   # All interfaces (IAIProvider, IGitLabClient, etc.)
│   ├── factory.ts    # Composition root — wires all deps, returns MRReviewer
│   └── types.ts      # Config, AIProviderName, shared types
├── tests/            # Jest tests
├── projects/         # Review projects (shared by both runners)
│   ├── registry.yaml # service-name → system-dir mapping
│   ├── _shared/      # docs injected into every review prompt
│   └── <system>/     # system.yaml + .md docs per system
├── templates/        # GitLab CI reusable template
├── Dockerfile        # Multi-stage Go build
├── Makefile          # Go build targets
├── package.json      # TypeScript deps (tsx, jest, ts-jest, etc.)
└── tsconfig.json     # outDir: ./dist-scripts, rootDir: .
```

---

## Build & Test Commands

### Go

```bash
make build              # → ./bin/mrinspect
make test               # go test ./...
make docker             # builds mrinspect:latest
```

### TypeScript

```bash
npm install             # one-time (node_modules is gitignored)
npm test                # Jest suite
npx tsc --noEmit        # type-check
npx tsx review.ts       # run reviewer directly
```

---

## Go Architecture (SOLID)

- **Interfaces** live in `internal/interfaces/` — `IGitLabClient`, `IDiffFetcher`, `IProjectLoader`, `IPromptComposer`, `IReviewValidator`, `IErrorHandler`
- **`MRInspectReviewer`** (`internal/reviewer/reviewer.go`) is the orchestrator — holds only interface fields, no concrete pointer types (except `*logger.Logger` for metrics lifecycle)
- **`cmd/mrinspect/main.go`** is the composition root — the only place that constructs concrete types and selects which `IDiffFetcher` to inject based on `CrossRepo.Enabled`
- **Diff fetching** is split into three focused types: `LocalDiffFetcher` (go-git), `APIDiffFetcher` (GitLab API), and `FallbackDiffFetcher` (tries local, falls back to API) — all implement `IDiffFetcher`
- **AI providers** are interchangeable via the existing `ai.Provider` interface; selected by `config.AIProvider`
- Concrete types (`*gitlab.Client`, `*validator.Validator`, etc.) satisfy their interfaces implicitly — no changes to those packages were needed

---

## TypeScript Architecture (SOLID)

- **Interfaces** live in `src/interfaces/` — `IAIProvider`, `IGitLabClient`, `IDiffFetcher`, `IProjectLoader`, `IPromptComposer`, `IReviewValidator`
- **`MRReviewer`** (`src/review/MRReviewer.ts`) is the orchestrator — receives all deps via constructor, contains no `new` calls
- **`factory.ts`** is the composition root — the only place that calls `new` and wires deps together
- **AI providers** are interchangeable via `IAIProvider`; selected by `config.aiProvider`
- **`loadConfig()`** reads all env vars and throws early with clear messages if required vars are missing

---

## CI Job Names

Defined in `templates/ai-review-template.yaml`:

| Job | What it runs |
|---|---|
| `.mrinspect-go-review` | Go binary (`mrinspect:latest` Docker image) |
| `.mrinspect-ts-review` | TypeScript runner (`node:22`, `npx tsx review.ts`) |
| `.superpowers-review` | Claude Code CLI + superpowers plugin |
| `.mrinspect-full` | All three layers in parallel |

Callers use `extends: .mrinspect-go-review` (or `-ts-review`, `-full`, etc.).

---

## Projects System

1. `projects/registry.yaml` maps service names → system directory names
2. `projects/<system>/system.yaml` describes the system (name, frameworks, service type overrides)
3. `projects/<system>/*.md` are review standards injected into the AI prompt
4. `projects/_shared/*.md` are injected into every review regardless of system
5. Falls back to built-in legacy templates if no project matches

Both runners use the same `projects/` directory. In CI the Go binary bakes projects into the Docker image; the TypeScript runner reads them from the working directory (`PROJECTS_DIR` defaults to `./projects`).

---

## Key Environment Variables

| Variable | Default | Notes |
|---|---|---|
| `AI_PROVIDER_KEY` | _(required)_ | API key for Gemini / Anthropic / OpenAI |
| `GITLAB_TOKEN` | _(required)_ | GitLab token with `api` + `write_repository` |
| `AI_PROVIDER` | `gemini` | `gemini` \| `anthropic` \| `openai` |
| `TARGET_SERVICE_NAME` | `unknown` | Must match a key in `projects/registry.yaml` |
| `TARGET_SERVICE_TYPE` | `backend` | `backend` \| `frontend` \| `ai` \| `iac` |
| `IS_SELF_REFLECTION` | `false` | Set `true` for a second AI validation pass |
| `PROJECTS_DIR` | `./projects` | Override projects directory path |
| `CI_PROJECT_ID` | _(auto by GitLab)_ | Project ID for local MR mode |
| `CI_MERGE_REQUEST_IID` | _(auto by GitLab)_ | MR IID for local MR mode |

Cross-repo trigger mode uses `TARGET_PROJECT_ID`, `TARGET_MR_IID`, `SOURCE_BRANCH`, `TARGET_BRANCH` instead.

---

## Test Patterns

- Table-driven tests mirror Go conventions
- `assertValidationCode(fn, 'CODE')` helper checks `(error as ValidationError).code` — do not use `toThrow('CODE')` since the code is not in the message string
- Project tests (`tests/projectLoader.test.ts`) use the real `projects/` directory — no mocks
- Config tests (`tests/config.test.ts`) use a `withEnv()` helper to isolate env var state per test

---

## Gitignored

```
node_modules/
dist-scripts/
```
