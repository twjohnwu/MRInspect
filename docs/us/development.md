# Development

Where the code lives and which build, test, and lint commands each runner provides.

[繁體中文版](../tw/development.md)

## Repository structure

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

## Makefile and scripts

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
