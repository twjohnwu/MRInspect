# 安裝與建置

需要先裝什麼、如何建置 MRInspect，以及如何在自己的機器上跑一次審查。

[English](../us/installation.md)

## 前置需求

- Go 1.23+
- Docker（Go 容器化部署需要）
- Claude Code CLI — 只有 superpowers 層需要：`npm install -g @anthropic-ai/claude-code`

## Go 執行檔

```bash
git clone https://github.com/twjohnwu/MRInspect
cd mrinspect
make build        # compiles to ./bin/mrinspect
```

## 使用 Docker image

```bash
# Pull the official release image:
docker pull ghcr.io/twjohnwu/mrinspect:v0.1.0

# Or build a development image locally:
docker build -t mrinspect:dev .
```

官方 release image 會發布到 GHCR。這個 Docker image 採多階段建置：Go 執行檔在 `golang:1.23-alpine` 裡編譯，再複製進 `alpine:3.20` 的 runtime image（約 15 MB）。`projects/` 目錄會被烤進 image 的 `/app/projects/`。

## 在本機執行

```bash
AI_PROVIDER_KEY=your-key \
GITLAB_TOKEN=your-token \
CI_PROJECT_ID=123 \
CI_MERGE_REQUEST_IID=45 \
  ./bin/mrinspect
```

## `mrinspect index`

用 `./bin/mrinspect index --out .rag/mrinspect-rag.sqlite` 建立 SQLite RAG store。`--dry-run` 只回報資源與檔案統計，不會寫出 store；`--check` 則驗證 `--out` 路徑上既有的 store，`--check` 與 `--dry-run` 不能一起用。預設輸出位置是 `.rag/mrinspect-rag.sqlite`。

| 離開碼 | 意義 |
|---|---|
| `0` | 建索引、dry run 或 store 檢查成功 |
| `1` | 設定、參數、資源載入或建索引失敗 |
| `2` | 用法衝突，或沒有解析出任何 resource set |
| `3` | 索引完成，但有一個以上的檔案失敗 |
| `4` | 既有 store 檢查失敗 |
| `5` | 選定的 backend 不支援建索引 |
