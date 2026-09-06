---
status: approved
approved_date: 2026-09-06
approved_fingerprint: 58641e3ceea4b85d76bc66cb0c2516363a4435289eb645267757b9334b1d8edc
design_ux_fingerprint: null
language: zh-TW
---

# spec — retrieval-golden-discrimination

## 背景

retrieval-quality-eval 的離線檢索量測（`mrinspect eval -retrieval`）首輪 8 列全為 1.00／1.00，rerank
關／開零差異（`docs/decisions_log.md` 第 6 條）。原因是 golden 段落以 diff 識別字撰寫，BM25 已把它們
排第 1；rerank 只重排 BM25 候選池，第 1 位沒有可動空間。

兩個結構事實決定本 change 的形狀（`internal/rag/sqlite/retriever.go:164-166,185-189,235-240`）：
1. OFF 臂取 BM25 前 TopK+1 再截成 k 筆；ON 臂取 BM25 前 4×TopK，以 embedding 餘弦重排後截成 k 筆。
   ON 的候選池嚴格包含 OFF 的，所以「ON 高於 OFF」可能只是池子變大，不一定是 embedding 有訊號。
2. 查詢是 `lane.Terms` 從 diff 抽出的最多 40 個識別字 token；BM25 走 FTS5 `unicode61`（不拆 camelCase、
   同時索引 heading）。任何以另一套切詞定義的「字面重疊」都會與實際排名漂移。

因此本 change：(a) 新增**改寫層**（語意相關、BM25 名次落在 OFF 截點之外但仍在 ON 候選池內）與
**干擾層**（共用識別字、主題無關、BM25 排進前 k）兩層 golden，層的歸屬以 BM25 實際名次定義；
(b) 報告加入第三臂 `shuffle`——同一 4×TopK 候選池的隨機重排——作為 ON 臂的控制組；
(c) 把 rerank 去留判準寫在本 spec，報告仍只印數字。

## 硬約束

承接 `STDD/retrieval-quality-eval/spec.md` 全部硬約束（corpus/golden 只擴 `margherita-pizza/` 與
`_shared/`、全虛構、報告只印數字與標頭、不含 key／絕對路徑／URL、不設分數門檻、不 fail CI、
harness 不呼叫 guard、失敗政策 OFF 降級整趟拒／ON 家族降級逐格）。本 change 的增量：
- 查詢向量不得為量測而改：仍是 `lane.Terms(evalrun.SynthesizeChanges(diff))`。
- `Score` 既有簽章、`Options` 既有欄位、報告標頭五欄位不變；新欄位只能新增。
- 本 change 的所有 Go 測試都是**資料有效性守門**（golden 結構、層歸屬名次、corpus 規則），
  不對任何 rerank 數字設門檻。
- `shuffle` 臂不呼叫 embedding；ON 臂每三元組仍恰一次 embedding 呼叫。

## REQ-01 golden 分層 schema 與載入驗證

每個 golden 條目（既有 `{fixture, lane, relevant}`）MUST 新增 `paraphrase` 與 `distractors` 兩個清單，
三者皆為 `{set, path, heading}` 目標：

```yaml
entries:
  - fixture: 01-echo-cut-earliest-marker.diff
    lane: spec-conformance
    relevant:      # 原層：既有條目不動
      - {set: margherita-pizza-docs, path: api-conventions.md, heading: "… > Earliest response marker"}
    paraphrase:    # 改寫層：語意相關；BM25 名次在 (k, 4k]
      - {set: margherita-pizza-docs, path: api-conventions.md, heading: "… > First sentinel wins"}
    distractors:   # 干擾層：共用識別字、主題無關；BM25 名次 ≤ k
      - {set: margherita-pizza-docs, path: api-conventions.md, heading: "… > Marker deprecation timeline"}
```

載入期驗證在既有規則（≤1 MiB、UTF-8、fixture×lane 覆蓋、重複 fixture+lane 拒收、store 存在性）之上
MUST 新增，錯誤訊息沿用 `load golden: ` 前綴：
(a) 對每個三元組 (fixture, lane, set)（由 `BuildPlan` 解析），三個清單各至少一個目標的 `set` 等於該
    三元組的 set——取代原本只對 `relevant` 的每 set 最低數量；
(b) 目標的 `set` 不屬於該 lane 解析到的 set → 拒收（消除死資料）；
(c) 同一清單內重複目標拒收；同一條目三清單兩兩不得有相同目標；
(d) `ValidateAgainstStore` 對三清單一併檢查存在性，缺漏列出規則沿用（≤50 筆＋`and N more`）。
golden 不新增 `k`、`tier`、名次或任何可由結構或 store 推得的欄位。

### S-01 三清單最低數量與 set 歸屬

- GIVEN 一份 golden，某 fixture×lane 條目的 `paraphrase` 為空；另一份 golden 某條目的 `distractors`
  目標 set 為 `fried-chicken-docs`（不屬該 lane）；另一份的 `relevant` 只含另一 set 的目標而該 lane
  解析到兩個 set
- WHEN `LoadGolden` 後以 `BuildPlan` 的三元組驗證
- THEN 三份各回錯，含 `load golden:`、fixture、lane、清單名與 set 名
- Test mapping: `internal/retrievaleval/golden_test.go` `TestGolden_RequiresEveryTierPerTriple`
- Verification command: `go test ./internal/retrievaleval/ -run TestGolden_RequiresEveryTierPerTriple -count=1 -v`

### S-02 清單內重複、跨清單重疊與存在性

- GIVEN 一份 golden 的 `distractors` 含兩個相同目標；另一份 `relevant` 與 `distractors` 含相同目標；
  另一份 `paraphrase` 目標在 store 中不存在
- WHEN `LoadGolden`／`ValidateAgainstStore`
- THEN 前兩份回錯含 `load golden:` 與該目標 `set/path#heading`；第三份回錯列出缺漏目標
- Test mapping: `internal/retrievaleval/golden_test.go` `TestGolden_RejectsDuplicatesOverlapAndMissingTargets`
- Verification command: `go test ./internal/retrievaleval/ -run TestGolden_RejectsDuplicatesOverlapAndMissingTargets -count=1 -v`

## REQ-02 分層 corpus 段落與名次守門

corpus MUST 為每個三元組 (fixture, lane, set) 新增至少一個改寫層段落與一個干擾層段落，並維持
retrieval-quality-eval REQ-01 的全部規則（總段落 ≥200、檔內 breadcrumb 唯一、heading 不含 ` > `、
全虛構；`fried-chicken/` 不動）。

層歸屬以 **BM25 實際名次**定義，不以任何切詞規則定義。守門測試在 `t.TempDir()` 以 `sqlite.Index`＋
`embed.NewFixture` 建置真 corpus 的暫存 store，對每個三元組以 OFF retriever、`Query.TopK = 4k`
取得 BM25 前 4k 筆（k 為該 lane 的 TopK）：
- 改寫層目標：名次 r 必滿足 `k < r ≤ 4k`（進得了 ON 候選池，但 OFF 截不到）；
- 干擾層目標：名次 r 必滿足 `r ≤ k`（BM25 已被騙進前 k）；
- 原層目標：名次 r 必滿足 `r ≤ k`（基線不變）。
違反者逐筆列出 `fixture / lane / set/path#heading / 實際名次或「不在前 4k」`，各自獨立失敗；不設比例。

### S-03 三層目標的 BM25 名次落點

- GIVEN 真 corpus 建入暫存 store、真 golden、真 fixtures
- WHEN 對每個三元組以 OFF retriever `TopK=4k` 取候選並查每個目標的名次
- THEN 每個改寫層目標 `k < r ≤ 4k`、每個干擾層與原層目標 `r ≤ k`；任一違反即列出並失敗
- Test mapping: `internal/retrievaleval/corpus_test.go` `TestCorpus_TierRankBands`
- Verification command: `go test ./internal/retrievaleval/ -run TestCorpus_TierRankBands -count=1 -v`

### S-04 corpus 既有規則仍成立

- GIVEN 擴寫後的 `projects/margherita-pizza/*.md` 與 `projects/_shared/*.md`
- WHEN 走 `resources.Load` 解析的 set 路徑切段
- THEN 總段落 ≥200、檔內 breadcrumb 唯一、heading 不含 ` > `；`projects/fried-chicken/` 與前一 commit 相同
- Test mapping: `internal/retrievaleval/corpus_test.go` `TestCorpus_MeetsSizeAndUniqueBreadcrumbs`（既有，須仍綠）＋`TestCorpus_GoldenCoversAllFixtures`（既有）
- Verification command: `go test ./internal/retrievaleval/ -run 'TestCorpus_MeetsSizeAndUniqueBreadcrumbs|TestCorpus_GoldenCoversAllFixtures' -count=1 -v && git diff --quiet HEAD~1 -- projects/fried-chicken/`

## REQ-03 三臂、分層指標與報告

每個三元組 MUST 產生三臂命中序列，皆截成 k 筆：
- `off`：既有 OFF retriever；
- `on`：既有 ON retriever（embedding rerank，4×TopK 候選池）；
- `shuffle`：以 OFF retriever `Query.TopK = 4k` 取同一 BM25 候選池，以固定 seed 1..20 各隨機重排一次、
  各截 k 筆計分後取 20 次平均。不呼叫 embedding；OFF 降級時與 `off` 同屬 store 級失敗、整趟拒。

指標（命中比對沿用 `ResourceSet/Source/Heading` 三欄）：原層 recall@k、MRR 對 `relevant`；改寫層
recall@k、MRR 對 `paraphrase`（皆用既有 `Score`）；干擾層 `distractors@k = |前 k 命中 ∩ distractors|`
（整數）。不新增 precision（它是兩層 recall 的線性函數，且對干擾段與無關段不分）。

報告 `eval/RETRIEVAL.md` MUST 保留標頭五欄位（`pool:` 行改為 `pool: off=TopK+1 on=4xTopK shuffle=4xTopK×20`），
主體為**一張表**、一列 mean（排除 degraded 格，附 `(n=N)`）：

```
| fixture | lane | set | k | orig_recall off/shuf/on | orig_mrr off/shuf/on | para_recall off/shuf/on | para_mrr off/shuf/on | distractors off/shuf/on |
```

每個「off/shuf/on」為三欄；浮點兩位小數，`distractors` 整數，`shuffle` 的 distractors 印兩位小數平均。
ON 臂 rerank 家族降級時該列所有 `on` 格為 `degraded: <code>`，`off`／`shuf` 格照印；轉義 `|` 與換行；
temp＋rename；全文不得含結論性詞。

### S-05 shuffle 臂與 distractors@k 計算

- GIVEN k=4，候選池 16 筆固定內容，其中 relevant 在第 1 位、paraphrase 在第 6 位、distractor 在第 3 位；
  另一例命中為空
- WHEN 以 seed 1..20 重排並計分
- THEN `off` 臂：orig_recall 1.00、para_recall 0.00、distractors 1；`shuffle` 臂：para_recall 平均落在
  (0, 1) 開區間且 20 次結果對同 seed 可重現；空命中全部為 0
- Test mapping: `internal/retrievaleval/metrics_test.go` `TestMetrics_ShuffleArmAndDistractorCount`
- Verification command: `go test ./internal/retrievaleval/ -run TestMetrics_ShuffleArmAndDistractorCount -count=1 -v`

### S-06 單表三臂渲染、降級格與轉義

- GIVEN 兩個三元組，其中一個 ON 為 `rerank degraded: embed-call-failed (…)`；測試用 `resources.yaml` 的
  一個 set 名含 `|`
- WHEN `Run` 以 `embed.Fixture` 執行
- THEN 報告恰一張表、一列 `| mean |` 含 `(n=1)`；降級三元組的五個 `on` 格皆為 `degraded: embed-call-failed`
  而 `off`／`shuf` 格為數字；set 名的 `|` 已轉義；標頭五欄位存在且 `pool:` 行含 `shuffle=`；全文不含
  結論性詞（better／worse／improve／good／bad 及中文對應）
- Test mapping: `internal/retrievaleval/run_test.go` `TestRun_RendersThreeArmTable`
- Verification command: `go test ./internal/retrievaleval/ -run TestRun_RendersThreeArmTable -count=1 -v`

### S-07 ON 臂嵌入次數不變

- GIVEN 兩個三元組、`embed.Fixture` 計數
- WHEN `Run`
- THEN embedding 呼叫次數 = 三元組數（shuffle 臂零次）
- Test mapping: `internal/retrievaleval/run_test.go` `TestRun_EmbedsOncePerRerankedTriple`（既有，加 shuffle 後須仍綠）
- Verification command: `go test ./internal/retrievaleval/ -run TestRun_EmbedsOncePerRerankedTriple -count=1 -v`

## REQ-04 rerank 去留判準（人讀規則）

以報告 mean 列**印出的兩位小數**比較，harness 不實作、報告不印。「不劣於」對 recall／MRR 為 ≥，
對 distractors 為 ≤：

| 條件 | 結果 |
|---|---|
| 改寫層 `on` 的 recall 或 MRR 同時 > `shuf` 且 > `off`，且原層／干擾層每個 `on` 皆不劣於 `off` | 保留 rerank；預設是否開啟另議 |
| 原層或干擾層任一 `on` 劣於 `off` | 建議移除 rerank |
| 其餘（改寫層 `on` 未同時勝過 `shuf` 與 `off`，且無劣化） | 無歸因證據：記錄，維持預設關，是否移除另議 |

三列互斥且覆蓋所有結果。判準結果與 mean 列一併記入 `docs/decisions_log.md` 新條目。

### S-08 [MANUAL] 實跑、套用判準、記錄

- GIVEN 本機 `MRI_RAG_EMBEDDINGS=true`＋Gemini key；corpus 與 golden 已擴寫；`./bin/mrinspect index`
  完成後等待 ≥60 秒讓 embedding 每分鐘額度窗口重置
- WHEN 執行 `./bin/mrinspect eval -retrieval`
- THEN 報告無 `degraded` 格；使用者以 REQ-04 表套用判準，把結果與 mean 列記入 `docs/decisions_log.md`；
  報告 commit
- Test mapping: manual
- Verification command: `./bin/mrinspect eval -retrieval && ! grep -q 'degraded' eval/RETRIEVAL.md`

## Rejected options

- 縮 k（改生產 TopK 或多印 recall@3）：MRR 全 1.00 表示相關段落已在第 1 位，縮 k 不改變結果。
- 正控制組做成 Go 測試（打亂 OFF 命中再計分）：OFF 只回 k 筆，k 內打亂 recall 不變、斷言必紅；且只驗 `Score` 排序敏感度，不驗檢索。
- 只改寫既有 golden 段落而非新增層：破壞原層基線，兩輪數字不可比。
- precision@k：等於 (orig_recall·|relevant| + para_recall·|paraphrase|)/k，是既有兩欄的線性函數；對干擾段與無關段一視同仁，量不到干擾。
- nDCG@k：多一個讀者要學的公式；distractors@k 直接數干擾段即可。
- 以 lane 切詞算段落與查詢的 token 交集當層歸屬守門：lane 切詞與 FTS5 `unicode61` 不同源（camelCase、heading 索引、40 token 上限），與實際名次漂移；改用 BM25 名次。
- 改寫層要求零字面重疊：進不了 BM25 候選池，兩臂必為 0。
- 干擾比例門檻：前一 change 已裁定算術必然；改為每目標各自可失敗的名次規則。
- 三張分層報告表：列鍵重複三次、判準要跨表對照；改為單表。
- 「不 fail CI」硬約束例外：正控制組撤除後不再需要。
- 用 mrinspect 自己的文件當 corpus：前一 change 已拒。
- 把去留判準寫進 RETRIEVAL.md：違反「只印數字」紅線。

## Adjudications

- REQ-01（v1 正控制組 Go 測試）：REFUTED（elf／orc／hobbit 一致）→ 撤除；控制組改為報告的 `shuffle` 臂（v2 REQ-03）。
- REQ-02（v1 golden 三清單）：REFUTED（elf：最低數量應對三元組而非條目，否則雙 set lane 靜默 0；orc：同清單重複、空 `relevant`、非該 lane set 的死資料未擋）→ v2 REQ-01 (a)–(d)。
- REQ-03（v1 token 交集守門）：REFUTED（elf：[1,2] 帶內 63% 段落進不了候選池、部分排在前 8；elf／orc：lane 切詞≠FTS5、heading 被索引、Q 為 40 個泛用字）→ v2 REQ-02 以 BM25 名次定義層歸屬。
- REQ-04（v1 precision@k＋三表）：REFUTED（hobbit／elf：precision 是兩層 recall 的線性函數且不分干擾段；hobbit：三表列鍵重複；orc：轉義情境放在無此欄的 heading、整數格式與渲染器衝突）→ v2 REQ-03 單表、刪 precision、`distractors@k` 整數格式明文、轉義情境改用 set 名。
- REQ-05（v1 判準）：REFUTED（elf：ON 池 4×TopK 嚴格包含 OFF 池，無控制臂無法歸因 embedding；判準表漏「原層進步、改寫層持平」一列；Decision tables 節與 REQ-05 矛盾）→ v2 REQ-04 以 `shuf` 為控制臂、三列互斥覆蓋、刪 Decision tables 節、比較以印出兩位小數為準。
- 硬約束例外：REFUTED（hobbit：僅為 v1 REQ-01 存在）→ 刪除。
- orc advisory（未納入，記錄）：報告標籤欄無路徑／URL 拒收（承前一 change，來源為 repo 內檔名／id）；兩個 eval 同時跑會互蓋報告（無鎖）。

## Requirements Checklist

- [ ] golden 三清單、每三元組最低數量、set 歸屬、清單內重複、跨清單重疊、存在性、錯誤前綴（REQ-01）
- [ ] 每三元組各 ≥1 改寫層／干擾層段落；層歸屬以 BM25 名次守門、各目標獨立失敗（REQ-02）
- [ ] 既有 corpus 規則仍綠；fried-chicken 不動（REQ-02）
- [ ] 三臂（off／shuffle／on）、shuffle 不呼叫 embedding、seed 固定、單表、mean、降級格、轉義、標頭（REQ-03）
- [ ] 去留判準只在 spec 與 decisions_log；三列互斥覆蓋；報告只印數字（REQ-04）
- [ ] 全部測試為資料有效性守門、隔離（暫存 store、embed.Fixture、無網路）；凍結介面零變更
