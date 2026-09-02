# MRInspect

![CI](https://github.com/twjohnwu/MRInspect/actions/workflows/ci.yml/badge.svg)
![Version](https://img.shields.io/github/v/tag/twjohnwu/MRInspect?label=version)

[繁體中文版](README.tw.md)

AI-powered merge request code review for GitLab, powered by Claude, Gemini, or OpenAI. MRInspect runs as a non-blocking CI/CD job: it reads your code diff, loads a team-specific review project, and posts a structured review comment directly to the MR — no human reviewer required for the first pass.

## Quickstart

Choose either the published image or a reusable template mirrored to your GitLab instance.

### Path A — pull the published image (fastest)

Add this job to the target repository's `.gitlab-ci.yml`, and set `AI_PROVIDER_KEY` and `GITLAB_TOKEN` as CI/CD variables.

```yaml
ai-review:
  stage: test
  image:
    name: ghcr.io/twjohnwu/mrinspect:v0.2.0
    entrypoint: [""]
  script:
    - mrinspect
  variables:
    AI_PROVIDER: openai
    AI_PROVIDER_KEY: $AI_PROVIDER_KEY   # GitLab CI/CD variable
    GITLAB_TOKEN: $GITLAB_TOKEN         # GitLab CI/CD variable (api scope)
    MRI_SERVICE_NAME: my-service
    MRI_SERVICE_TYPE: backend
    PROJECTS_DIR: /app/projects
  rules:
    - if: '$CI_PIPELINE_SOURCE == "merge_request_event"'
  allow_failure: true
```

### Path B — reuse the template via a GitLab mirror

First import or mirror the GitHub repository into your own GitLab instance via **Project → New project → Import project → Repository by URL**, then add this configuration:

```yaml
include:
  - project: 'your-group/mrinspect'   # your GitLab copy, not the GitHub repo
    ref: main
    file: 'templates/ai-review-template.yaml'

ai-review:
  extends: .mrinspect-go-review
  variables:
    MRI_SERVICE_NAME: my-service
    MRI_SERVICE_TYPE: backend
```

Warning: `include: project:` cannot point at GitHub; it must reference a project on the same GitLab instance.

### Build locally

```bash
git clone https://github.com/twjohnwu/MRInspect
cd mrinspect
make build        # compiles to ./bin/mrinspect
```

## Documentation

| Document | Answers |
|---|---|
| [Architecture](docs/us/architecture.md) | What runs on each review layer, and how a single-mode or multi-lane review flows from MR to posted comment |
| [Installation and build](docs/us/installation.md) | What to install, how to build the Go binary and Docker image, and how to run a review locally |
| [Configuration](docs/us/configuration.md) | Which AI provider to select, which secrets to set, and what every environment variable does |
| [Integration](docs/us/integration.md) | How another repository triggers MRInspect from GitLab CI or GitHub Actions |
| [Project system](docs/us/project-system.md) | How a team defines its own review standards, resource sets, and lanes under `projects/` |
| [Development](docs/us/development.md) | Where each package lives, and which Make commands build, test, and lint the code |
| [設計決策記錄](docs/decisions_log.md) | Why the design went the way it did (Traditional Chinese) |
