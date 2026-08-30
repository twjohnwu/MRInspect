# Integration

Four ways for another repository to start an MRInspect review: the published image, a mirrored GitLab `include`, a cross-repo pipeline trigger, or a GitHub Actions workflow.

[繁體中文版](../tw/integration.md)

## Option A — Published image (fastest)

Add this job to the target repository's `.gitlab-ci.yml`, and set `AI_PROVIDER_KEY` and `GITLAB_TOKEN` as CI/CD variables.

```yaml
ai-review:
  stage: test
  image:
    name: ghcr.io/twjohnwu/mrinspect:v0.1.0
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

## Option B — GitLab mirror and `include`

First import or mirror the GitHub repository into your own GitLab instance via **Project → New project → Import project → Repository by URL**. Then include the reusable template and extend the job:

```yaml
# your-repo/.gitlab-ci.yml

include:
  - project: 'your-group/mrinspect'   # your GitLab copy, not the GitHub repo
    ref: main
    file: 'templates/ai-review-template.yaml'

ai-review:
  extends: .mrinspect-full       # runs all layers in parallel
  variables:
    MRI_SERVICE_NAME: my-service
    MRI_SERVICE_TYPE: backend  # backend | frontend | ai | iac
```

Warning: `include: project:` cannot point at GitHub; it must reference a project on the same GitLab instance.

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

## Option C — GitLab pipeline trigger (cross-repo, works from any Git host)

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

## Option D — GitHub Actions trigger

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
