# Project 系統

團隊如何在 `projects/` 底下定義自己的審查標準，以及 registry、resource set 與 lane 之間怎麼搭起來。

[English](../us/project-system.md)

Project 讓每個團隊定義自己的審查標準。mrinspect 會載入某個服務對應的 project，並把那些文件當成情境注入 AI prompt。

```
projects/
├── registry.yaml               # service-name → system-name mapping
├── resources.yaml              # named resource sets selected by name or tag
├── lanes.yaml                  # canonical ordered lane declarations
├── _lanes/                     # static lane prompt preambles
│   ├── code-diff.tmpl.md
│   ├── spec-conformance.tmpl.md
│   └── standards.tmpl.md
├── _shared/
│   └── coding-standards.md     # standards applied to every system
├── margherita-pizza/           # sample system (Go + PostgreSQL + gRPC)
│   ├── system.yaml
│   ├── lanes.yaml              # per-system lane overlay
│   ├── architecture.md
│   └── review-focus.md
└── fried-chicken/              # sample system (Go + Kafka + MongoDB)
    ├── system.yaml
    ├── lanes.yaml              # per-system lane overlay
    ├── architecture.md
    └── review-focus.md
```

**`projects/registry.yaml`** — 把服務名稱對應到系統目錄：

```yaml
defaultSystem: my-system

services:
  payments-api: my-system
  auth-service: my-system
  dashboard: another-system
```

**`projects/my-system/system.yaml`** — 描述這個系統：

```yaml
name: My System
description: >
  Brief description of what this system does and its architecture.
defaultServiceType: backend
frameworks:
  - Go
  - PostgreSQL
serviceTypeOverrides:
  my-dashboard: frontend   # override per-service if needed
```

**`projects/my-system/review-focus.md`** — 引導 AI reviewer：

```markdown
# My System Review Focus

## Critical Checks
- All database writes must use transactions
- gRPC calls must propagate the incoming context

## Common Bugs
- Missing `defer tx.Rollback()` after `db.Begin()`
```

**`projects/resources.yaml`** — 宣告有順序、有名稱的 resource set。Lane 透過 `name` 或 `tags`（或兩者）挑選 set；每個 set 都必須在 `mode: retrieval`（取用已建索引的 chunk）與 `mode: full`（整份文件注入）之間擇一，並宣告來源 `paths` 以及可選的 include/exclude 樣式。

**`projects/lanes.yaml`** — 宣告有順序的 multi-review lane。每個 lane 都必須有 `id`、`enabled`、`template`、`intent` 與 `resources`；selector 可以包含明確的 `sets` 與 `tags`。兩個 selector 清單都留空的 lane，只會拿到 diff，不會附帶外部資源文件。

**`projects/_lanes/*.tmpl.md`** — 接在各自 lane prompt 前面的靜態前言。系統可以自行加上 `projects/<system>/lanes.yaml`；overlay 項目依 lane ID 合併，被取代者保留原本的位置，新的 ID 則依 overlay 的宣告順序附加在後面。

在 multi 模式下，lane 設定缺漏或無效、以及沒有任何啟用中 lane 的設定，都會退回 single 審查路徑，並在審查中附上具名的理由。lane 的 prompt 組裝失敗會被回報為具名的 lane 失敗，而不是默默換成舊版 template。
