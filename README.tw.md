# MRInspect

![CI](https://github.com/twjohnwu/MRInspect/actions/workflows/ci.yml/badge.svg)

[English](README.md)

給 GitLab 用的 AI merge request 程式碼審查工具，可搭配 Claude、Gemini 或 OpenAI。MRInspect 以不阻擋流程的 CI/CD job 執行：讀取你的程式碼 diff，載入該團隊專屬的 review project，然後把結構化的審查留言直接貼到 MR——第一輪審查不需要人類 reviewer。

## 快速開始

建置 Go 執行檔：

```bash
git clone https://github.com/twjohnwu/MRInspect
cd mrinspect
make build        # compiles to ./bin/mrinspect
```

引入可重用的 template，把它接進另一個 repo 的 pipeline：

```yaml
include:
  - project: 'twjohnwu/MRInspect'
    ref: main
    file: 'templates/ai-review-template.yaml'
```

## 文件

| 文件 | 回答什麼 |
|---|---|
| [架構](docs/tw/architecture.md) | 每個審查層各跑什麼，以及 single 或 multi-lane 審查如何從 MR 走到貼出留言 |
| [安裝與建置](docs/tw/installation.md) | 要裝什麼、如何建置 Go 執行檔、Docker image 與 TypeScript runner，以及如何在本機跑一次審查 |
| [設定](docs/tw/configuration.md) | 要選哪個 AI provider、要設哪些密鑰，以及每個環境變數的作用 |
| [整合](docs/tw/integration.md) | 另一個 repository 如何從 GitLab CI 或 GitHub Actions 觸發 MRInspect |
| [Project 系統](docs/tw/project-system.md) | 團隊如何在 `projects/` 底下定義自己的審查標準、resource set 與 lane |
| [開發](docs/tw/development.md) | 每個套件放在哪裡，以及哪些 Make 與 npm 指令負責建置、測試與 lint |
| [設計決策記錄](docs/decisions_log.md) | 設計為何走到今天這樣（本檔為繁體中文） |
