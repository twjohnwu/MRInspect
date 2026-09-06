# Case study：demo 專案上的一次 multi-lane 審查

一個 diff、三條 lane、一則貼出去的審查，從頭走到尾。

[English](../us/case-study.md)

以下內容全部出自本 repo：fixture diff、`projects/` 底下的專案設定檔，以及 [`eval/REPORT.md`](../../eval/REPORT.md) 與 [`eval/RETRIEVAL.md`](../../eval/RETRIEVAL.md) 裡記錄下來的輸出。demo 專案是虛構的：margherita-pizza、fried-chicken、dough-service，以及審查讀到的那些 spec，都是為這個 repo 寫的，不屬於任何人。

## 這個改動做了什麼

fixture 是 [`eval/fixtures/03-lane-overlays-config.diff`](../../eval/fixtures/03-lane-overlays-config.diff)，`eval/fixtures/README.md` 把它標為 `config` 類，挑選理由是純設定檔變更、lane overlay 語意。這個 diff 把 `projects/resources.yaml` 裡 `margherita-pizza-docs` 與 `fried-chicken-docs` 兩個 set 的共用 `docs` tag 拿掉，讓它們只留下自己系統名稱的 tag。它新增兩個 overlay 檔 `projects/margherita-pizza/lanes.yaml` 與 `projects/fried-chicken/lanes.yaml`，各自把 `spec-conformance` lane 釘到自己系統的 set，tag 清單留空。剩下的 hunk 改的是兩份設計文件：`design-be.md` 補上一節記錄這次撤回與理由，`tasks.md` 的一行任務從 `[ ]` 翻成 `[x]`。沒有動到任何 Go 程式碼。

## Lane 怎麼解析出來的

`projects/lanes.yaml` 宣告的三條 lane 都是 enabled：`spec-conformance`、`standards`、`code-diff`。`projects/registry.yaml` 把 service 對應到系統（`dough-service` 與另外兩個對到 `margherita-pizza`，另外三個對到 `fried-chicken`），並把 `margherita-pizza` 指定為預設系統，這次跑的就是解析到這個系統。

三條 lane 各自拿到不同的 resource selector：

- `spec-conformance` 吃到 `projects/margherita-pizza/lanes.yaml` 這個 overlay，指名 set `margherita-pizza-docs`、不帶 tag，取代 canonical 版本的 `docs` tag selector。
- `standards` 維持 canonical selector，也就是 `standards` tag，對應到 `projects/resources.yaml` 裡的 `shared-standards` set。
- `code-diff` 既沒指定 set 也沒指定 tag，因此不檢索任何東西，只看得到 diff。

沒有任何 lane 設定檔指定 TopK，三條 lane 都退回 `DefaultLaneTopK = 8`（`internal/lane/registry.go:35`），檢索報告裡兩條會檢索的 lane 記錄的 k 也是 8。overlay、set、tag 怎麼解析，寫在 [Project 系統](project-system.md)；外圍的審查流程寫在[架構](architecture.md)。

## 檢索到了什麼

以下是 `eval/RETRIEVAL.md` 裡屬於這個 fixture 的兩列，由[設定](configuration.md#offline-retrieval-check)文件裡說明的離線檢索檢查產生：

| lane | set | k | recall_off | recall_on | mrr_off | mrr_on |
|---|---|---:|---:|---:|---:|---:|
| spec-conformance | margherita-pizza-docs | 8 | 1.00 | 1.00 | 1.00 | 1.00 |
| standards | shared-standards | 8 | 1.00 | 1.00 | 1.00 | 1.00 |

recall@k 是 `eval/retrieval-golden.yaml` 裡標為相關的 chunk，有多少比例出現在前 k 筆結果裡；MRR 則是第一個相關 chunk 的排名倒數，取查詢平均。`_off` 與 `_on` 這兩組是同一個查詢分別跑在關閉與開啟 embedding rerank 的 store 上（`internal/retrievaleval/run.go:130-179`）。`code-diff` lane 不檢索，所以沒有對應的列。

## 審查說了什麼

`eval/REPORT.md` 裡這個 fixture 的 `### multi` 區塊，就是原封不動的貼出內容。前十一行：

```markdown
## MRInspect Review

### Scope
- **spec-conformance** — Resource sets: margherita-pizza-docs (8 chunks retrieved)
- **standards** — Resource sets: shared-standards (8 chunks retrieved)
- **code-diff** — Resource sets: none

### Findings
| # | Severity | Category | Standard | Item | File:Line |
|---|----------|----------|----------|------|-----------|
| - | - | - | - | No findings reported | - |
```

剩下的部分很短。findings 表格後面接三個嚴重度分節 High、Medium、Low，每一節都只有一行 `- None.`；最後的 `### Verdict` 寫著 `Approved`。同一個 fixture 區塊裡也記錄了 `single` 與 `reflect` 兩種模式，它們從同一份 diff 產出另一種形狀的審查、不走 lane 檢索，內容比較長、還列出 positive observations，就放在這一段旁邊。

## 這一次花了多少 token

`eval/REPORT.md` 對每次模型呼叫都印出 prompt 組成明細，並在這個 fixture 結尾印出 `Token subtotal: 45351`，涵蓋它跑過的三種模式。multi-lane 模式佔其中三次呼叫，一條 lane 一次，估算的 prompt token 分別是 2,516、2,359、1,859。

`spec-conformance` 那 2,516 個 token 裡最大的幾項：

| Section | Tokens | % of total |
|---|---:|---:|
| diff | 1107 | 44.0% |
| margherita-pizza-docs | 641 | 25.5% |
| base prompt/metadata | 510 | 20.3% |

另外兩條 lane 帶的是同一份 1,107 token 的 diff 與同一份 510 token 的 base prompt；`standards` 多了 495 token 的 `shared-standards` 檢索內容，`code-diff` 一個都沒有。最大與最小 lane prompt 之間相差 657 token，多數來自這段檢索內容，剩下的 16 token 是 lane 模板前言的差距，`spec-conformance` 是 67、`code-diff` 是 51。

## 這份紀錄能說明什麼、不能說明什麼

這是一個 fixture、一個模型、一次執行：`gemini-3.6-flash`，記錄時間是 2026-09-05 與 2026-09-06（那次執行因為免費方案的每日額度而拆成兩天）。它檢索到的文件是為 demo 專案寫的虛構 spec，所以審查對這些文件下的判斷，說明不了這個工具在真實 codebase 上會怎麼跑。檢索數字量的是 golden 檔裡標為相關的 chunk 有沒有被拉回來，不是這則審查有沒有用；這裡沒有任何審查品質評分，MRInspect 也沒有外部採用者的結果可以拿來替代。

四個 fixture、三種模式的完整紀錄在 `eval/REPORT.md`，全部的檢索表在 `eval/RETRIEVAL.md`。
