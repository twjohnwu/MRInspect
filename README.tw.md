# MRInspect

![CI](https://github.com/twjohnwu/MRInspect/actions/workflows/ci.yml/badge.svg)

[English](README.md)

給 GitLab 用的 AI merge request 程式碼審查工具，可搭配 Claude、Gemini 或 OpenAI。MRInspect 以不阻擋流程的 CI/CD job 執行：讀取你的程式碼 diff，載入該團隊專屬的 review project，然後把結構化的審查留言直接貼到 MR——第一輪審查不需要人類 reviewer。

## 快速開始

你可以選擇使用已發布的 image，或重用鏡像至你 GitLab 實例的 template。

### 路徑 A — 拉取已發布的 image（最快）

將此 job 加入目標 repository 的 `.gitlab-ci.yml`，並將 `AI_PROVIDER_KEY` 與 `GITLAB_TOKEN` 設為 CI/CD 變數。

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

### 路徑 B — 透過 GitLab mirror 重用 template

先透過 **Project → New project → Import project → Repository by URL**，將 GitHub repository 匯入或鏡像至你自己的 GitLab 實例，然後加入此設定：

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

警告：`include: project:` 不能指向 GitHub；它必須參照同一個 GitLab 實例上的 project。

### 本機建置

```bash
git clone https://github.com/twjohnwu/MRInspect
cd mrinspect
make build        # compiles to ./bin/mrinspect
```

## 文件

| 文件 | 回答什麼 |
|---|---|
| [架構](docs/tw/architecture.md) | 每個審查層各跑什麼，以及 single 或 multi-lane 審查如何從 MR 走到貼出留言 |
| [安裝與建置](docs/tw/installation.md) | 要裝什麼、如何建置 Go 執行檔與 Docker image，以及如何在本機跑一次審查 |
| [設定](docs/tw/configuration.md) | 要選哪個 AI provider、要設哪些密鑰，以及每個環境變數的作用 |
| [整合](docs/tw/integration.md) | 另一個 repository 如何從 GitLab CI 或 GitHub Actions 觸發 MRInspect |
| [Project 系統](docs/tw/project-system.md) | 團隊如何在 `projects/` 底下定義自己的審查標準、resource set 與 lane |
| [開發](docs/tw/development.md) | 每個套件放在哪裡，以及哪些 Make 指令負責建置、測試與 lint |
| [設計決策記錄](docs/decisions_log.md) | 設計為何走到今天這樣（本檔為繁體中文） |
