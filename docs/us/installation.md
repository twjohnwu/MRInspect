# Installation and build

What you need installed, how to build each runner, and how to run a review from your own machine.

[繁體中文版](../tw/installation.md)

## Prerequisites

- Go 1.23+ (for the Go binary)
- Node.js 22+ (for the TypeScript runner)
- Docker (for containerized Go deployments)
- Claude Code CLI — superpowers layer only: `npm install -g @anthropic-ai/claude-code`

## Go binary

```bash
git clone https://github.com/twjohnwu/MRInspect
cd mrinspect
make build        # compiles to ./bin/mrinspect
```

## Use the Docker image

```bash
# Pull the official release image:
docker pull ghcr.io/twjohnwu/mrinspect:v0.1.0

# Or build a development image locally:
docker build -t mrinspect:dev .
```

Official release images are published to GHCR. The Docker image is a multi-stage build: the Go binary is compiled in `golang:1.23-alpine` and copied into an `alpine:3.20` runtime image (~15 MB). The `projects/` directory is baked into the image at `/app/projects/`.

## TypeScript runner

```bash
cd mrinspect
npm install       # one-time dependency install (no build step)
npm test          # run Jest test suite
```

## Run locally

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

## `mrinspect index`

Build the SQLite RAG store with `./bin/mrinspect index --out .rag/mrinspect-rag.sqlite`. `--dry-run` reports resource and file statistics without writing a store, while `--check` validates an existing store at the `--out` path; `--check` and `--dry-run` cannot be combined. The default output is `.rag/mrinspect-rag.sqlite`.

| Exit code | Meaning |
|---|---|
| `0` | Index, dry run, or store check succeeded |
| `1` | Configuration, argument, resource-loading, or indexing failure |
| `2` | Usage conflict or no resource sets resolved |
| `3` | Index completed with one or more file failures |
| `4` | Existing-store check failed |
| `5` | Selected backend does not support indexing |
