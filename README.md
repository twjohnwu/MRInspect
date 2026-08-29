# MRInspect

![CI](https://github.com/twjohnwu/MRInspect/actions/workflows/ci.yml/badge.svg)

[繁體中文版](README.tw.md)

AI-powered merge request code review for GitLab, powered by Claude, Gemini, or OpenAI. MRInspect runs as a non-blocking CI/CD job: it reads your code diff, loads a team-specific review project, and posts a structured review comment directly to the MR — no human reviewer required for the first pass.

## Quickstart

Build the Go binary:

```bash
git clone https://github.com/twjohnwu/MRInspect
cd mrinspect
make build        # compiles to ./bin/mrinspect
```

Wire it into another repo's pipeline by including the reusable template:

```yaml
include:
  - project: 'twjohnwu/MRInspect'
    ref: main
    file: 'templates/ai-review-template.yaml'
```

## Documentation

| Document | Answers |
|---|---|
| [Architecture](docs/us/architecture.md) | What runs on each review layer, and how a single-mode or multi-lane review flows from MR to posted comment |
| [Installation and build](docs/us/installation.md) | What to install, how to build the Go binary, the Docker image, and the TypeScript runner, and how to run a review locally |
| [Configuration](docs/us/configuration.md) | Which AI provider to select, which secrets to set, and what every environment variable does |
| [Integration](docs/us/integration.md) | How another repository triggers MRInspect from GitLab CI or GitHub Actions |
| [Project system](docs/us/project-system.md) | How a team defines its own review standards, resource sets, and lanes under `projects/` |
| [Development](docs/us/development.md) | Where each package lives, and which Make and npm commands build, test, and lint the code |
| [設計決策記錄](docs/decisions_log.md) | Why the design went the way it did (Traditional Chinese) |
