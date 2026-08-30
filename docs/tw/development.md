# 開發

每個套件放在哪裡，以及哪些 Make 指令負責建置、測試與 lint。

[English](../us/development.md)

## Repository 結構

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
├── projects/             # Review projects
│   ├── registry.yaml     # Service-name → system-directory mapping
│   ├── resources.yaml    # Named RAG resource sets
│   ├── lanes.yaml        # Canonical ordered review lanes
│   ├── _lanes/           # Static lane prompt templates
│   ├── _shared/          # Shared review documents
│   └── <system>/         # System config, docs, and optional lane overlay
├── docs/                 # Design and architecture documentation
├── templates/            # GitLab CI reusable template
├── Dockerfile            # Multi-stage Go build
└── Makefile              # Go build targets
```

## Makefile 與指令

| 指令 | 說明 |
|---|---|
| `make build` | 編譯出 `./bin/mrinspect` |
| `make test` | 跑 Go 單元測試（`go test ./...`） |
| `make test-integration` | 跑整合測試（需要 `-tags integration`） |
| `make lint` | 執行 `golangci-lint` |
| `make lint-lane-ids` | 禁止 `internal/` 與 `cmd/` 底下非測試的 Go 檔以 lane-ID 字串字面值做分支 |
| `make docker` | 建置 Docker image `mrinspect:latest` |
| `make clean` | 移除 `./bin` 的建置產物 |
