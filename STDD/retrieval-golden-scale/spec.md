---
status: approved
approved_date: 2026-09-07
approved_fingerprint: 538519b9b0da80c8d0e728dfbcab0a92382c75aee34cc22111084d802fba7b39
design_ux_fingerprint: null
language: zh-TW
---

# retrieval-golden-scale — 檢索量測擴到可引用規模，hard negatives 可按類別讀

## 背景與目的

第二輪量測（retrieval-golden-discrimination，decisions_log #8）已有訊號並決定保留 rerank，但只有
8 個 query（4 個 review fixture × 2 lane），一列翻轉就改變 mean 0.125，對外引用站不住；干擾層只有一個
總數，看不出 rerank 在哪一類干擾上失敗。本 change 把 query 擴到 48 個、分佈在兩個示範系統，給每個
干擾目標一個人工類別，並在 header 記一行檢索耗時，讓報告可以被引用、被按類別解讀。**不改 retriever 行為**。

不做：跨 set stress slice、embedding cost、rerank 去留重判、生產 `lanes.yaml`／`resources.yaml`。

## Supersedes（明文取代前 spec 條文；前 spec 檔案不動）

| 舊條文 | 本 spec 取代為 |
|---|---|
| retrieval-quality-eval spec:137「fixtures 目錄固定 `eval/fixtures`」 | `eval -retrieval` 預設讀 `eval/retrieval-fixtures/<system>/`（REQ-01）；review eval 仍讀 `eval/fixtures` |
| retrieval-quality-eval spec:45-46「`fried-chicken` 不被任何 fixture 查詢，不動」 | `fried-chicken` 取得 10 個 retrieval fixture 與對應 corpus 段落（REQ-02） |

## Domain Language

| Term | Exact meaning | Do not use |
|---|---|---|
| retrieval fixture | `eval/retrieval-fixtures/<system>/NN-name.diff` 的純 diff，只供 `eval -retrieval`；`eval/fixtures/` 的檔案是 review fixture | eval fixture |
| fixture id | golden 與報告中識別 retrieval fixture 的字串 `<system>/<檔名>`；`evalrun.Fixture.Name` 仍是檔名，前綴由 harness 在建三元組時加上 | 檔名單獨、絕對路徑 |
| system | `projects/<system>/` 目錄名，同時是 retrieval fixture 子目錄名；字元限 `^[a-z0-9][a-z0-9-]*$` | service、repo |

## REQ-01 retrieval fixture 目錄、多 system 執行、預檢

- **目錄解析**：`retrievaleval.Options.FixturesDir` 為空時預設 `eval/retrieval-fixtures`。`cmd/mrinspect/main.go:68`
  的 `-fixtures` 旗標在 `-retrieval` 模式下只有使用者明確給值才傳入（以 `flags.Visit` 判斷，沿 `main.go:80-88`
  `-report` 旗標的既有做法）；判斷邏輯抽成 `ragcmd.EvalFixturesDir(retrieval, explicit bool, value string) string`
  供單元測試。review eval 預設不變。
- **子目錄即 system**：`Run` 以 `os.ReadDir` 列出 `FixturesDir` 直屬項目（排序）；名稱以 `.` 開頭的項目（如 `.DS_Store`）靜默略過；其餘每個項目須是**一般目錄**（`Lstat`
  非 symlink）且名稱符合 `^[a-z0-9][a-z0-9-]*$`，否則錯誤 `load retrieval fixtures: entry %q is not a plain
  directory with a valid system name`；`projects/<system>/` 須為目錄，否則 `load retrieval fixtures: system %q has
  no projects directory`；沒有任何合格子目錄 → `load retrieval fixtures: no system directories found`。錯誤訊息不含
  路徑（承 retrieval-quality-eval spec:167）。子目錄內以既有 `evalrun.LoadFixtures(dir)`（`evalrun.go:369`）載入。
- **fixture id**：三元組 `Triple.Fixture`（`plan.go:75`）與 `LoadGolden` 的 fixture 名清單（`run.go:46-50`）改用
  `<system>/<檔名>`；`LoadGolden` 仍只呼叫一次，涵蓋全部 system。
- **每 system 建計畫**：對每個 system 各呼叫 `BuildPlan(repoRoot, system, fixtures)`（`plan.go:24`）；lane 解析沿既有
  overlay 規則。`Options.System` 移除；`main.go:95` 的 `ragcmd.SystemDirectory(cfg)` 不再用於 `-retrieval` 分支
  （index 分支不變）。新鮮度檢查 `resources.Load(opts.RepoRoot, systems[0])`——loader 支援 per-system overlay
  （`internal/rag/resources/loader.go:44-50` 讀 `projects/<system>/resources.yaml`），今日 repo 無任何 overlay 檔，結果與 system 無關；此不變量由 S-03 守門。
- **預檢先於任何檢索**：全部 system 的 fixtures 載入、`BuildPlan`、`ValidateAgainstPlan`、store 新鮮度與
  `ValidateAgainstStore` 全部通過後，才開始第一個 `Retrieve`；任一失敗 → 零次檢索、零次 embedding、不寫報告。
- store 仍單一份、涵蓋全部 resource set；header 五欄不變。

### S-01 子目錄即 system；預檢失敗零檢索

- GIVEN 暫存 `retrieval-fixtures/` 含 `alpha/01-a.diff`、`beta/01-b.diff`、一個 symlink `gamma -> alpha`、一個檔案 `.DS_Store`（須被略過）、一個檔案
  `notes.txt`；`projects/alpha/` 存在、`projects/beta/` 不存在；embedder 為呼叫計數 fixture
- WHEN 呼叫 `Run`
- THEN 回傳錯誤含 `load retrieval fixtures:` 且含 `"beta" has no projects directory` 或 `"gamma" is not a plain directory`
  （兩者之一，依檢查順序）；embedder 呼叫次數 0；報告檔不存在。另一子案：目錄下無合格子目錄 → 錯誤含
  `no system directories found`；錯誤字串不含 `t.TempDir()` 路徑
- Test mapping: `internal/retrievaleval/run_test.go` `TestRun_LoadsFixturesPerSystemDir`
- Verification command: `go test ./internal/retrievaleval/ -run TestRun_LoadsFixturesPerSystemDir -count=1 -v`

### S-02 `-fixtures` 明確給值才覆寫

- GIVEN `ragcmd.EvalFixturesDir`
- WHEN 以 (retrieval=true, explicit=false, "eval/fixtures")、(true, true, "x")、(false, false, "eval/fixtures") 呼叫
- THEN 依序回 `eval/retrieval-fixtures`、`x`、`eval/fixtures`
- Test mapping: `internal/ragcmd/fixtures_test.go` `TestEvalFixturesDir`
- Verification command: `go test ./internal/ragcmd/ -run TestEvalFixturesDir -count=1 -v`

## REQ-02 資料規模：24 diff、48 query、兩系統、`_shared` 一併擴寫

- `eval/retrieval-fixtures/margherita-pizza/` 14 個 diff：既有 `eval/fixtures/` 4 個 diff 的**位元組相同複本**（review
  eval 保留原檔）＋10 個新 diff；`eval/retrieval-fixtures/fried-chicken/` 10 個新 diff。全部虛構，不含真實公司、
  主機名、URL、工作機或內部 repo 名。
- `eval/retrieval-golden.yaml` 每個 fixture id × 兩個 requiredLanes（`golden.go:22`）各一條目，共 48。`standards` lane
  在兩系統皆解析到 `shared-standards`（`projects/lanes.yaml`；overlay 只覆寫 `spec-conformance`），因此 24 個
  `standards` query 的三清單目標全在 `projects/_shared/`：`_shared` 須依 20 個新 diff 補相關段、改寫段、干擾段。
  `projects/fried-chicken/` 依 chicken 10 個 diff、`projects/margherita-pizza/` 依 pizza 10 個新 diff 各補三類。
- 層歸屬仍以 BM25 名次守門（改寫層 k<r≤4k、原層與干擾層 r≤k），單一暫存 store 對兩系統的三元組檢查。
- 五類干擾各 ≥6 個目標（全 golden 合計）。撰寫指引（非守門）：scope／version／neighbor 各約 30%、25%、20%，
  responsibility 與 lexical 補足。
- 全部三元組的 `K` 相同（目前 `projects/` 無任何 `topK:` 覆寫，皆為 `lane.DefaultLaneTopK` 8）。

### S-03 規模、分佈、複本、K 一致守門

- GIVEN 真 repo 的 `eval/retrieval-fixtures/`、`eval/fixtures/`、`eval/retrieval-golden.yaml`
- WHEN 測試載入並統計
- THEN `margherita-pizza` 14 個 fixture、`fried-chicken` 10 個；golden 條目 48；每個 fixture id 兩個 lane 都有條目；
  五類 category 各 ≥6 個干擾目標；`eval/fixtures/*.diff` 每檔在 `retrieval-fixtures/margherita-pizza/` 有同名且
  位元組相同的複本；全部三元組 `K` 相同；`projects/*/resources.yaml` 不存在（新鮮度不變量）。違反逐項列出
- Test mapping: `internal/retrievaleval/corpus_test.go` `TestCorpus_GoldenScale`
- Verification command: `go test ./internal/retrievaleval/ -run TestCorpus_GoldenScale -count=1 -v`

### S-04 兩系統名次守門

- GIVEN 真 corpus 建**一份**暫存 store（沿 `corpus_test.go:118` 做法）
- WHEN 對每個 system 子目錄的三元組以 OFF retriever `Query{TopK: 4k}` 取名次
- THEN 改寫層目標 k<r≤4k、原層與干擾層 r≤k；違反逐筆列出 `fixture id / lane / set/path#heading / 名次或「不在前 4k」`
- Test mapping: `internal/retrievaleval/corpus_test.go` `TestCorpus_TierRankBands`（改為迭代 system 子目錄）
- Verification command: `go test ./internal/retrievaleval/ -run TestCorpus_TierRankBands -count=1 -v`

## REQ-03 golden 干擾目標的 category

- `Target`（`golden.go:25-29`）**不變**——它是命中比對的 map key（`metrics.go:24,54,95`），加欄會讓所有比對落空。
  新型別 `Distractor{Target \`yaml:",inline"\`; Category string}`；`Entry.Distractors` 改為 `[]Distractor`；計分
  一律傳 `d.Target`。
- golden 解碼開 `yaml.Decoder.KnownFields(true)`：`relevant`／`paraphrase` 項目型別仍為 `Target`，出現 `category`
  鍵（含 `null`）即解碼失敗，錯誤含 `load golden:` 與 `category`。
- `distractors` 項目 `category` 必填，值須**完全等於**五個字串之一（不做大小寫或空白正規化）：
  `scope`／`version`／`responsibility`／`lexical`／`neighbor`；缺鍵、`null`、空字串、其他值 →
  `load golden: fixture %q lane %q distractor %s has invalid category %q`。
- `category` 不進 `targetKey`（`golden.go:247-249`）：同一段落不得以不同 category 重複出現。

### S-05 category 驗證

- GIVEN golden 四份：(a) 干擾目標缺 `category`；(b) `category: fuzzy`；(c) `category: Scope`；(d) `relevant` 目標帶 `category: scope`
- WHEN `LoadGolden`
- THEN 四份各回錯誤；(a)(b)(c) 含 `load golden:`、fixture id、lane、`invalid category`；(d) 含 `load golden:` 與 `category`
- Test mapping: `internal/retrievaleval/golden_test.go` `TestGolden_DistractorCategory`
- Verification command: `go test ./internal/retrievaleval/ -run TestGolden_DistractorCategory -count=1 -v`

## REQ-04 報告：system 欄、header 耗時行、兩張聚合表

前 spec 的 Rejected「三張分層報告表」是同一批列拆三表；本 spec 主表仍單一，新增的是**聚合**表（列＝k 或
category，列鍵不重複）。前 spec 的 Rejected「多印 recall@3」以「MRR 全 1.00」為由；#8 已顯示 orig_mrr_on 0.94、
para_mrr_on 0.22，k=1／3 有資訊，故以聚合表重審。

- **主表**欄位（`report.go:68` 字面更新）：在既有 19 欄前加 `system`，共 20 欄：
  `| system | fixture | lane | set | k | orig_recall_off | … | distractors_on |`。`fixture` 欄印檔名（不含前綴）。
  `Row` 加 `System string`（`report.go:40-46`）。
- **header 加一行** `retrieve_ms: off_mean=<int> on_mean=<int> (n=<int>)`：OFF `Retrieve`（`run.go:155`）與 ON
  `Retrieve`（`run.go:171`）各以 `time.Now()`／`time.Since()` 包住；off_mean 對全部三元組、on_mean 對 ON 未降級的
  三元組取均值後 `math.Round` 成整數毫秒，`n` 為 on 的樣本數；pool 查詢與 shuffle 不計時。全部 ON 降級時印
  `on_mean=- (n=0)`。`Header` 加對應欄位，`validateHeader` 接受該行。單次取樣、OFF 先於 ON，是指示性數字。
- **`## Mean by k` 表**：列 `k=1`、`k=3`、`k=<TopK>`（TopK 印實際值，S-03 保證唯一）；欄
  `| k | orig_recall_off | orig_recall_shuf | orig_recall_on | para_recall_off | para_recall_shuf | para_recall_on |`，
  格式 `%.2f (n=…)`。k=1、k=3 以**同一份** off／on 命中切片與同一 shuffle 排列重算（`Score` 本就截 k，
  `metrics.go:20-22`；`forEachShuffle` 先整池重排再截 k，`metrics.go:99-117`，故為同排列前綴），不新增 `Retrieve`。
  `k=<TopK>` 列六格與主表 mean 列相等。recall 定義不變（多正解時 recall@1 上限 1/|relevant|）。
- **`## Distractors by category` 表**：五列固定順序；欄 `| category | n | distractors_off | distractors_shuf |
  distractors_on |`；`n` 為該類目標總數；三臂格為「該三元組中該類目標的**命中數**」對含該類目標的三元組取均值，
  `%.2f (n=…)`；n=0 的類別三臂格印 `- (n=0)`。
- 整數格渲染改 `math.Round`（`report.go:140-147` 目前截斷；現有整數格皆為整數值，無影響）。全文只印數字；
  `system`、`category` 格經 `escapeCell`。

### S-06 主表 system 欄、header 耗時行、降級列、措辭

- GIVEN 兩個 system 各一個 fixture（每 fixture 兩個 lane → 各兩個三元組，共四個），embedder 為 `embed.NewFixture` 設定第二次呼叫失敗（ON 只有第二個三元組降級
  `rerank degraded: embed-call-failed`）；測試 `resources.yaml` 的 set 名含 `|`
- WHEN `Run`
- THEN 主表恰一張、標頭字面如 REQ-04（20 欄）；每列 `system` 欄為子目錄名，`set` 欄 `|` 已轉義；降級列五個 `_on`
  格皆 `degraded: embed-call-failed`；header 含 `retrieve_ms: off_mean=` 與 `on_mean=` 且 `(n=3)`（四個三元組中一個降級）；全文無
  `better`／`worse`／`improve`／`good`／`bad`／`更好`／`更差`／`改善`；每張表各列 `|` 數相等
- Test mapping: `internal/retrievaleval/run_test.go` `TestRun_RendersSystemColumnAndRetrieveMs`
- Verification command: `go test ./internal/retrievaleval/ -run TestRun_RendersSystemColumnAndRetrieveMs -count=1 -v`

### S-07 Mean by k 不多呼叫檢索

- GIVEN S-06 的四個三元組、TopK=8，embedder 呼叫計數
- WHEN `Run`
- THEN 報告含 `## Mean by k` 表三列 `k=1`／`k=3`／`k=8`，`k=8` 列六格與主表 mean 列對應格相等；ON 未降級三元組各
  恰一次 embedding 呼叫（既有 `TestRun_EmbedsOncePerRerankedTriple` 仍綠）
- Test mapping: `internal/retrievaleval/run_test.go` `TestRun_RendersMeanByK`
- Verification command: `go test ./internal/retrievaleval/ -run 'TestRun_RendersMeanByK|TestRun_EmbedsOncePerRerankedTriple' -count=1 -v`

### S-08 Distractors by category

- GIVEN 單一 fixture（兩個三元組：`scope` 干擾目標宣告在 `spec-conformance` lane 的條目、`lexical` 在 `standards` lane 的條目），golden 干擾目標：`scope` 兩個（OFF 命中一個）、`lexical` 一個（OFF 命中），其餘類別零個
- WHEN `Run`
- THEN `## Distractors by category` 表五列固定順序；`scope` 列 `n`=2、`distractors_off` 為 `1.00 (n=1)`；`lexical`
  列 `n`=1、`distractors_off` 為 `1.00 (n=1)`；`version`／`responsibility`／`neighbor` 列三臂格皆 `- (n=0)`
- Test mapping: `internal/retrievaleval/run_test.go` `TestRun_RendersDistractorsByCategory`
- Verification command: `go test ./internal/retrievaleval/ -run TestRun_RendersDistractorsByCategory -count=1 -v`

## REQ-05 eval 端 embedding 429 重試

- harness 把 `Options.Embedder` 包進 `embed.WithRateLimitRetry` decorator（eval 與 index 兩個批次呼叫方共用）：`Embed` 遇 `embed.IsRateLimited` 錯誤最多重試 3 次，
  等待 20／40／60 秒，每次等待前經 `Options.Progress` 印一行；非 429 錯誤立即回傳。等待函式可注入（測試傳零等待）。
  生產查詢路徑（`retriever.go:210`）不變——重試屬批次呼叫方（decisions_log #7）。

### S-09 429 重試

- GIVEN embedder fixture 前兩次呼叫回 429 `StatusError`、第三次成功；等待函式為零等待並記錄呼叫
- WHEN `Run` 一個 fixture（兩個三元組）
- THEN 該列 ON 不降級；embedder 呼叫 4 次（第一個三元組 429、429、成功；第二個三元組一次成功）；等待記錄為 `[20s, 40s]`；Progress 收到 2 行含 `rate limited`
- Test mapping: `internal/retrievaleval/run_test.go` `TestRun_RetriesRateLimitedEmbedding`
- Verification command: `go test ./internal/retrievaleval/ -run TestRun_RetriesRateLimitedEmbedding -count=1 -v`

## REQ-06 可引用門檻（人讀規則，harness 不實作、報告不印）

報告同時滿足以下兩條，該份報告為 citable，README／docs／案例才可引用其數字；否則只記錄：

1. 全表無 `degraded` 格；
2. 報告對應的 commit 上 S-03、S-04 守門測試綠（規模、分佈、複本、每類 ≥6、名次帶皆由此涵蓋）。

結果（citable 與否、mean 列、Mean by k 三列、by-category 五列、retrieve_ms 行）與逐類觀察記入
`docs/decisions_log.md` 新條目。不含 rerank 去留判準（#8 已決）。

### S-10 [MANUAL] 實跑、套用門檻、記錄

- GIVEN 本機 `MRI_RAG_EMBEDDINGS=true`＋Gemini key；corpus、fixtures、golden 已擴；`./bin/mrinspect index` 後等 ≥60 秒
- WHEN `./bin/mrinspect eval -retrieval`
- THEN 報告無 `degraded`；使用者以 REQ-06 兩條判定 citable，結果與三張表記入 `docs/decisions_log.md`；報告 commit；
  citable 時方可更新 `eval/README.md`／案例中的引用數字
- Test mapping: manual
- Verification command: `./bin/mrinspect eval -retrieval && ! grep -q 'degraded' eval/RETRIEVAL.md`

## 硬約束

- 凍結介面零變更：`rag.Retriever`、`Query`、`lanes.yaml`／`resources.yaml` 語意、store schema、`embed.Embedder`。
- 所有新測試為資料有效性守門或隔離單元測試（暫存 store、`embed.NewFixture`、無網路、零真實等待）。
- 報告只印數字；門檻只在 spec 與 decisions_log；錯誤訊息不含路徑。
- 內容全虛構；不含工作機或內部 repo 名；heading 不含 ` > `；檔內 breadcrumb 唯一；`eval/fixtures/` 原檔不動。

## Rejected options

- embedding cost 欄：query ≤40 token 為常數；provider 解碼不含 usage（`internal/rag/embed/remote.go:100-105,175-179`），改了也只有 OpenAI 可能有值。
- 共用 `eval/fixtures/`：review eval 的 mode-run 數會跟著長，免費層額度撐不住，REPORT 要重做。
- 跨 set stress slice：生產不跨系統搜尋；需改 lanes 解析語意或 harness 造假路徑；Sol 自己也說此時不進 headline。
- 改 demo `lanes.yaml` 讓 spec-conformance 解析兩 set：改變 demo 生產語意，review 結果連帶變。
- 主表加欄到 31 欄／每三元組出三列：無法橫向閱讀／144 列淹沒 mean。
- 帶去留判準重判：#8 已決；本輪目的是可引用規模與類別洞見。
- 兩次執行按 system 分檔：引用要看兩份；且現有「同時執行互蓋報告」advisory 會被踩到。
- 五類比例 40/25/20/15 當守門：撰寫指引即可，比例信心 Sol 自評中等。
- Sol 的 label-leakage 前提：query 由 `lane.Terms` 從 diff 取 40 個 token，不讀標題（`internal/lane/terms.go:22-45`）；第一輪無訊號真因已在 #6，第二輪 #8 已有訊號。前提不成立，不採納。
- 16 diff 全 pizza／16 diff 兩系統／chicken 只做 spec-conformance lane：使用者選 24 且維持 requiredLanes，`_shared` 一併擴寫。
- golden 條目加 `system` 鍵：子目錄路徑已可推得，違反「不加可推得欄位」。
- 以 symlink 讓 retrieval fixtures 引用 `eval/fixtures/` 原檔：跨平台與 git 行為不一致；接受 4 個複本並以 S-03 位元組相同守門。
- fixture header 加 `system=` 鍵：header 目前只檢前綴、無解析器；子目錄更簡單。
- `Target` 加 `category` 欄：`Target` 是命中比對 map key，加欄使所有 distractors 指標歸零（panel elf）。
- latency 逐列兩欄：無任何判準讀它、結構成本最高、單次取樣受預熱污染（panel hobbit／orc）；改 header 一行均值。
- Mean by k 完整 15 格：MRR 與 distractors 在主表 mean 已有，重印無資訊；只列 recall 六格（panel hobbit）。
- 可引用門檻含「每 system ≥20 列」「每類 n≥6」：與 S-03 守門重複，守門綠即必然成立（panel hobbit）。
- 每 system 各建暫存 store：loader 無 per-system overlay、BM25 以 set 過濄，兩份 store 位元組相同（panel elf）。
- `-fixtures` 只在 `Run` 內給預設：`main.go:103` 仍無條件傳 `eval/fixtures`，真 CLI 讀錯目錄（panel orc）；改 `flags.Visit`＋可測 helper。
- 報告寫入加鎖：既有 advisory，本 change 不處理。

## Adjudications

- REQ-01（v1）：REFUTED（elf：fixture id 前綴無落點、`Options.System` 移除後新鮮度與錯誤文字懸空、錯誤訊息漏絕對路徑；orc：system 目錄無字元規則／symlink／IsDir 檢查、無預檢致付費呼叫後才失敗、`-fixtures` 明確值偵測不可測）→ v2：前綴由 harness 在三元組與名單加上、`resources.Load` 用 `systems[0]`、訊息去路徑、目錄 grammar＋Lstat、預檢先於任何檢索、`ragcmd.EvalFixturesDir` helper。
- REQ-02（v1）：REFUTED（elf：chicken 的 `standards` lane 解析到 `_shared`，20 個目標無 corpus 計畫、24 個 standards query 同池競爭）→ v2 明列 `_shared` 依 20 個新 diff 擴寫；使用者裁定維持 24 diff。hobbit 建議的 4 複本改指雙目錄：不採（第二根目錄沒有 system 名）；改加位元組相同守門（orc）。
- REQ-03（v1）：REFUTED（elf：`Target` 加欄使 map key 比對落空、distractors 全歸零；orc：YAML `null` 繞過「不得帶 category」、大小寫未定）→ v2 `Distractor{Target inline; Category}`、`KnownFields(true)`、精確字串比對。
- REQ-04（v1）：REFUTED（hobbit：Mean by k 45 格重複、latency 兩欄無人讀且結構成本最高；orc：latency 截斷非四捨五入、單次取樣預熱；elf：S-06 的 off 欄 `(n=1)` 錯、S-07 GIVEN 不可實現、S-08 期望值 0.50 與「命中數均值」定義矛盾且逃生句使其不可否證、`k=TopK` 依賴 TopK 唯一）→ v2 Mean by k 只列 recall 六格、latency 改 header 一行、`math.Round`、S-06 用 `embed.NewFixture` 指定第幾次失敗、S-08 期望 `1.00 (n=1)`、S-03 守 K 唯一。
- REQ-05（v1 可引用門檻四條）：REFUTED（hobbit：條件 2、3 與 S-03 重複；elf：條件 4 非報告性質、chicken 20 列剛好壓線）→ v2 REQ-06 兩條（無降級＋守門綠於該 commit）。
- 新增 REQ-05 429 重試：來自 orc（查詢端 429 立即降級，48 個 ON 查詢無重試契約）；依 decisions_log #7 放在 eval 呼叫方，生產路徑不變。
- Supersedes 第 3、4 列：REFUTED（hobbit：Rejected option 非規範條文不可被 supersede）→ 移入 REQ-04 前言。
- Domain Language 六列：REFUTED（hobbit：四列防不存在的碰撞、兩列重複規範）→ 縮為三列。
- S-09 措辭情境：REFUTED（hobbit：與前 spec S-06 重複）→ 併入 S-06；S-09 改為 429 重試。
- orc advisory（未納入，記錄）：報告寫入 last-writer-wins；escapeCell 不處理反斜線（system grammar 已排除）。

## Requirements Checklist

- [ ] retrieval fixture 子目錄即 system（grammar、Lstat、`projects/` 存在）；預檢先於檢索；fixture id 前綴；`-fixtures` 明確值偵測可測；review eval 不受影響（REQ-01）
- [ ] 24 diff、48 條目、14/10 分佈、五類各 ≥6、4 複本位元組相同、K 唯一、`_shared` 擴寫、兩系統名次守門（REQ-02）
- [ ] `Distractor` 型別、`KnownFields`、精確 enum、錯誤前綴（REQ-03）
- [ ] 主表 system 欄、header `retrieve_ms` 行、Mean by k 六格、Distractors by category、零額外 Retrieve、`math.Round`、轉義、措辭（REQ-04）
- [ ] `embed.WithRateLimitRetry` 3 次、可注入等待、index 共用、生產查詢路徑不變（REQ-05）
- [ ] 可引用門檻兩條只在 spec 與 decisions_log；報告只印數字（REQ-06）
- [ ] Supersedes 兩條明文；凍結介面零變更；`eval/fixtures/` 原檔不動；錯誤訊息不含路徑
