# 整合

其他 repository 要啟動 MRInspect 審查有三種做法：GitLab `include`、跨 repo 的 pipeline trigger，或 GitHub Actions workflow。

[English](../us/integration.md)

## 做法 A — GitLab `include`（同一個 GitLab 實例，建議做法）

引入可重用的 template 並延伸該 job：

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

只跑其中一層：

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

## 做法 B — GitLab pipeline trigger（跨 repo，任何 Git host 都能用）

1. 在 mrinspect 專案裡：`Settings → CI/CD → Pipeline triggers` → 建立一個 trigger token。
2. 把該 token 以 `MRINSPECT_TRIGGER_TOKEN` 存進呼叫端 repo 的 CI/CD 變數。
3. 在你的 repo 加一個 trigger job：

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

把 `<MRINSPECT_PROJECT_ID>` 換成你部署的 mrinspect 的數字 project ID。

## 做法 C — GitHub Actions trigger

把 `MRINSPECT_TRIGGER_TOKEN` 與 `GITLAB_PROJECT_ID` 存成 GitHub repository secret，然後加上一個 workflow step：

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

**要建立的 GitHub secret：**

| Secret | 值 |
|---|---|
| `MRINSPECT_TRIGGER_TOKEN` | 來自 mrinspect GitLab 專案的 pipeline trigger token |
| `GITLAB_PROJECT_ID` | 你部署的 mrinspect 的數字 project ID |

> 審查留言會出現在 GitLab MR（不是 GitHub PR），因為 mrinspect 是透過 GitLab API 貼出的。
