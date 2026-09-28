# Decisions Log

Design pivots and architectural decisions for mrinspect. Append a new numbered
entry whenever a non-trivial design choice is made, reversed, or revisited.

**Entry format**

```
## N. <short title>

**Date:** YYYY-MM-DD
**Status:** current | superseded by #M | reverted

**Initial idea**
What we first considered or shipped.

**Why it was wrong**
The constraint, incident, or insight that forced a change.

**Current approach**
What the code does now.

**Lesson**
The general principle worth remembering — the part that survives even if the
specific decision is later reversed.
```

**Rules**

- Keep entries narrative, not prescriptive — "rules" belong in `AGENTS.md`.
- One entry per decision, even small ones. Easier to link to later.
- Never edit a past entry's content; if a decision is reversed, add a new entry
  and mark the old one `superseded by #N`.
- Code is authoritative. If an entry contradicts current code, the entry is
  stale — supersede it rather than rewriting history.

---

<!-- Append entries below, starting from #1. -->

## 1. 跨系統資源選取：tag 廣播 → per-system lane overlay

1. **最初想法**：per-system 文件集（margherita-pizza-docs 等）都掛共用 `docs` tag，canonical `spec-conformance` lane 以 `tags: [docs]` 一網打盡。
2. **為什麼錯**：`Resolve` 依 tag 匹配時沒有系統範圍——審 A 系統的 MR 會檢索到 B 系統的 spec 並據以審查（出貨審查發現 F3，經獨立驗證確認）。
3. **現在做法**：撤掉 per-system set 的 `docs` tag；每個系統以 `projects/<system>/lanes.yaml` overlay 把 `spec-conformance` 釘到自己的 set。canonical 的 `tags: [docs]` 保留給未來真正跨系統共用的文件。
4. **學到什麼**：以 tag 做跨集合選取時，tag 的語意邊界必須和資料的隔離邊界一致；新增系統的正確擴充點是「加一個 overlay 檔」而非「往共用 tag 塞」。

## 2. 引用驗證：從「有比對就好」到「來源可及且出處限定」

1. **最初想法**：lane 回傳的 `citations[].sourceId` 與該次檢索到的 chunk ID 比對，match 就渲染為已驗證座標。
2. **為什麼錯**：兩個獨立漏洞——(a) chunk ID（sqlite rowid）從未出現在 prompt 裡，模型不可能誠實引用，但隨口捏一個數字反而可能 match 成「已驗證」；(b) 跨 lane 合併把 citations 串接且不留出處，A lane 可以捏 B lane 收到的 ID 騙過驗證。修復前的空 map 讓一切顯示 unverified（無害但無用）；接通 chunks 後這兩條路徑變成主動的假背書。
3. **現在做法**：檢索 chunk 注入 prompt 時帶 `[sourceId: … | source: …:line]` 表頭，契約明令只能引用表頭所示 id；合併時每筆 citation 記錄提供者 lane，渲染只對「該 lane 實際收到的 chunks」驗證。
4. **學到什麼**：「驗證」機制要成立，被驗證方必須先拿得到正確答案的素材，且驗證範圍必須等於資料的信任邊界——兩者缺一，驗證徽章比沒有徽章更危險。

## 3. 超大 diff：整趟拒絕 → 檔案級誠實縮減

1. **最初想法**：diff 超過 `MaxDiffSizeKB` 就整趟拒絕審查（validator 硬閘），要嘛全審、要嘛不審。
2. **為什麼錯**：實測事故資料（一次 126 檔／~1MB diff 的 review）顯示兩件事——超大輸入下模型會**省略**必要區段而非截斷（diff 佔 prompt >93% 時三次 attempt 同型失敗，降到 ~85% 即恢復）；而整趟拒絕讓大型重構 MR 完全得不到審查。截斷 hunk 也不可行：模型會對不存在的程式碼提 finding。
3. **現在做法**：`internal/diffbudget` 檔案級剔除——先剔不可人審檔（lockfile／snapshot／generated 等 pattern 清單，config 可覆寫），再按檔案大小由大到小整檔剔除到符合 model-aware 預算（`MRI_DIFF_PROMPT_SHARE` × 模型 prompt 預算）；剔除清單在 prompt 與貼出的 review footer 雙處揭露；`MaxDiffSizeKB` 降為縮減後仍放不下的最終 backstop。
4. **學到什麼**：退化門檻跟格式遵循有關、跟 context 上限無關——1M context 的模型一樣會在高佔比輸入下漏節，所以上限要按「佔 prompt 比例」縮放，不能釘固定 KB；「誠實的部分審查＋明示未審清單」勝過「全有或全無」。

## 4. Dump 預設：從「開」反轉為「關」

- **最初想法**：validation 失敗時預設把完整 prompt＋response dump 進 CI log，除錯零摩擦；敏感 repo 用 `MRI_REVIEW_DUMP_DISABLED=true` 自行關閉。
- **為什麼錯**：public 工具的預設值要按最不受信任的部署環境設計。diff 可能含未被偵測的 credential、customer data、internal URL，而 CI log 的可見範圍與保存期不在本工具控制內——「文件有警語」補救不了預設行為。外部安全審視意見同樣指出這個 default 站不住。
- **現在做法**：預設不 dump；`MRI_REVIEW_DUMP_ENABLED=true`（精確字串）才開。預設失敗路徑保留驗證錯誤原因、標題清單、清洗前後長度，另補 prompt/response 的 sha256 前 12 碼供對帳。舊變數直接移除、不留相容層（上線僅數日、無外部使用者）。
- **學到什麼**：可觀測性與資料外洩是同一個開關的兩面；除錯便利靠 opt-in，不靠預設。

## 5. 雙實作（Go＋TypeScript）→ Go 單一實作

- **最初想法**：Go binary 之外保留 TypeScript runner 作為「免編譯、node 直跑」的替代路徑，兩邊共用 projects/ 與 env 介面。
- **為什麼錯**：能力早已不對等——RAG、multi-lane、diff 縮減、佔比表都只在 Go 端，但文件把兩者並列，讀者會誤以為完整 parity；每個 Go 修復都要多做一次「要不要回移 TS」的判斷。官方 image 發佈後，「免編譯」的存在理由也消失了——拉 image 比 npm install 更快。
- **現在做法**：移除 review.ts、src/、TS 測試與 npm 工具鏈；CI 只剩 Go job；template 移除 Layer 1b。Go 為唯一實作。
- **學到什麼**：第二實作的維護稅是每次改動都付的隱形成本；當它的差異化理由被更好的分發方式取代，就該退場，而不是掛著誤導定位。

## 6. 檢索品質量測：從「拒絕 precision/recall」到離線 golden harness；首輪結果無訊號

1. **最初想法**：review-quality-eval 把 precision/recall 列為範圍外（dogfood 無答案卷），embedding-rerank 以「功能正確即可」驗收、明令不宣稱品質。corpus 只有 25 chunks，任何量測都無鑑別力，先不量。
2. **為什麼錯**：不是錯，是沒做完——rerank 上線後仍無法回答「有沒有用」。真正缺的不是分數，是**答案卷**與**夠大的干擾集**；而檢索量測不需要生成呼叫，不受 20 req/日的免費層限制，可以離線做。
3. **現在做法**：`mrinspect eval -retrieval`（`internal/retrievaleval`）重放每個 eval fixture 的生產 lane 查詢，對本機 store 算 recall@k／MRR，rerank 關／開兩欄，寫 `eval/RETRIEVAL.md`；答案卷是 `eval/retrieval-golden.yaml`（結構化 `{set,path,heading}`，每 fixture 兩 lane）；示範 corpus 擴到 211 段（pizza 5 新檔＋_shared 2 新檔，四主題各有相關段落，其餘干擾）。store 若非以現行 corpus 建置即拒跑；golden 有 lane 卻無三元組即報錯。報告只印數字，不寫結論（接手 embedding-rerank 的措辭紅線）。
4. **學到什麼**：首輪 8 列全 1.00／1.00、兩欄零差異——**量測本身沒有訊號**。原因是 golden 段落用 diff 的字彙撰寫，BM25 靠字面重疊就排第一，k=8 又寬，rerank 沒有可改善的空間。harness 各路徑（新鮮度、雙欄、降級、守門）都經測試，所以這是 corpus/golden 設計問題，不是工具問題。要有鑑別力，下一步是讓 golden 段落**語意相關但字面不重疊**（改寫用詞）並縮 k；在那之前，任何「rerank 有用／沒用」的結論都沒有依據。另兩個執行期教訓：量測工具的 system 解析必須與生產走同一條映射（registry.yaml → system 目錄），否則 overlay 不載入、lane 靘默消失；新鮮度指紋必須與 index 用同一份 set 清單，否則真 repo 一跑就誤判。

## 7. embedding 速率限制：client 不重試，index 批次層重試

1. **最初想法**：embedding client 不重試、錯誤只留狀態碼（embedding-rerank REQ-01），查詢時 429 直接降級 BM25——對查詢端正確，索引端沿用同一 client、同樣不重試。
2. **為什麼錯**：corpus 擴到 221 chunks 後，index 以 64 chunks 一批連發，第 2 批穩定撞 Gemini 免費層每分鐘 30k tokens 上限，整趟失敗；等一分鐘重跑也一樣，因為前兩批總是在同一分鐘內。索引是一次性批次作業，失敗代價是整趟重來，和查詢端「快速降級」的取捨完全不同。
3. **現在做法**：client 仍不重試，但回傳型別化 `embed.StatusError`（`IsRateLimited` 判 429）；indexer 對同一批次最多重試 3 次、等 20／40／60 秒，每次等待前在進度輸出印一行；非 429 錯誤照舊立即失敗。查詢端 rerank 路徑不變，429 仍降級。
4. **學到什麼**：重試策略屬於呼叫方的工作性質，不屬於 client——同一個 client 在「互動式查詢」與「批次索引」兩種呼叫方下需要相反的行為，所以把重試放在呼叫方那一層，client 只負責把狀態碼型別化讓呼叫方能判斷。

## 8. 檢索量測第二輪：三臂＋分層 golden 有訊號；判準結果＝保留 rerank

1. **最初想法**：#6 結論是量測無訊號，補救方向定為「改寫 golden 段落用詞＋縮 k」。explore 時原擬：正控制組做成 Go 測試（打亂 OFF 命中再計分）、干擾層用 precision@k、判準只比 ON／OFF 兩欄。
2. **為什麼錯**：三點都被 panel 推翻。(a) OFF 只回 k 筆，k 內打亂 recall 不變，控制組測試必紅；(b) ON 候選池 4×TopK 嚴格包含 OFF 池（TopK+1），ON 命中率高於 OFF 可能只是池變大，沒有控制臂無法歸因給 embedding；(c) precision@k 等於兩層 recall 的線性組合，對干擾段與無關段一視同仁，量不到干擾。縮 k 也無意義：MRR 已為 1.00，相關段落已在第 1 位。
3. **現在做法**：golden 每條目三清單 `relevant`／`paraphrase`／`distractors`，層歸屬以 BM25 名次守門（改寫層 k<r≤4k、干擾層與原層 r≤k；`TestCorpus_TierRankBands` 對真 corpus 建暫存 store 查名次）。報告三臂 off／shuffle／on：shuffle 是同一 4×TopK 候選池的 20 次固定 seed 隨機重排平均，充當「池變大」的控制臂；干擾層直接數 `distractors@k`。判準（spec REQ-04）只寫在 spec 與本檔，報告只印數字。corpus 擴到 239 段（新增 `kitchen-notices.md`、`archive-notices.md`），golden 8 條目各加 1 個改寫層、1 個干擾層目標。
4. **結果與判準**（2026-09-07 實跑，8 列皆無降級）mean 列：

   | 層 | off | shuf | on |
   |---|---:|---:|---:|
   | orig_recall | 1.00 | 0.27 | 1.00 |
   | orig_mrr | 0.81 | 0.16 | 0.94 |
   | para_recall | 0.00 | 0.21 | 0.88 |
   | para_mrr | 0.00 | 0.05 | 0.22 |
   | distractors | 1.00 | 0.21 | 1.00 |

   改寫層 `on`（0.88／0.22）同時高於 `shuf`（0.21／0.05）與 `off`（0.00／0.00）；原層 `on` recall 持平、MRR 0.94 ≥ 0.81；干擾層 `on` 1.00 ≤ 1.00。落在 REQ-04 第一列：**保留 rerank，預設是否開啟另議**。逐列異常照錄（判準只看 mean，不改結論）：03 standards 改寫層 `on` recall 0.00（該改寫段未進前 8）；04 standards 原層 `on` MRR 0.50 低於 `off` 1.00（相關段被擠到第 2 位）；干擾層 `on` 與 `off` 皆為 1，rerank 沒有把語意近鄰的干擾段擠出前 k。
5. **學到什麼**：(a) 比較候選池大小不同的兩個系統，必須有一個在較大池上的無資訊基線（shuffle），否則差異可能全來自池大小；(b) 答案卷要含「進得了候選池但字面不重疊」的目標，量測才有訊號——shuffle 0.21 對 off 0.00 證明改寫段在池內、只是 BM25 排不上；(c) 衍生指標若是既有欄的線性函數就不要印（precision@k）；(d) 每分鐘額度型限流下，批次索引重試耗盡額度後緊接查詢會降級，批次與查詢之間要留窗口。

## 9. 檢索量測第三輪：golden 擴到 24 diff／48 query 兩系統；報告加聚合表；結果 citable

1. **最初想法**：外部 reviewer（Sol v3）建議 golden 擴到 30–50 query 並加 hard negatives，理由是「golden 標題與 query 過近」。
2. **為什麼那個論點不完全對**：query 不是從標題寫的，是 diff 詞彙經 `lane.Terms` 產生；#6 無訊號的原因是 corpus 段落用 diff 字彙寫成，#8 已修。但 n=8 的統計力太弱、干擾層沒有分類，這兩點成立。
3. **現在做法**：`eval/retrieval-fixtures/<system>/` 子目錄即 system（margherita-pizza 14＝`eval/fixtures/` 4 個位元組相同複本＋10 新；fried-chicken 10）；golden 48 條目，干擾目標加 `category`（scope／version／responsibility／lexical／neighbor 各 ≥6，實際 12／10／9／8／9）；報告加 `system` 欄、`retrieve_ms` header 行、`## Mean by k`（只列 recall 六格）、`## Distractors by category`；eval 端 embedding 429 改走 `embed.WithRateLimitRetry`，與 index 共用同一 decorator。可引用門檻（spec REQ-06）：全表無 degraded 格，且報告對應 commit 的 S-03／S-04 守門綠。
4. **結果**（2026-09-28，commit `926f2e4`，48 列皆無降級 → **citable**）：

   | 層 | off | shuf | on |
   |---|---:|---:|---:|
   | orig_recall | 1.00 | 0.31 | 1.00 |
   | orig_mrr | 0.89 | 0.14 | 0.95 |
   | para_recall | 0.00 | 0.23 | 0.50 |
   | para_mrr | 0.00 | 0.07 | 0.12 |
   | distractors | 1.00 | 0.18 | 0.96 |

   Mean by k（orig_recall off／shuf／on；para_recall off／shuf／on）：k=1 0.76／0.08／0.87；0.00／0.02／0.00。k=3 0.95／0.16／0.99；0.00／0.07／0.15。k=8 同 mean 列。
   Distractors by category（n；off／shuf／on）：scope 12；1.00／0.16／1.00。version 10；1.00／0.21／1.00。responsibility 9；1.00／0.20／1.00。lexical 8；1.00／0.12／1.00。neighbor 9；1.00／0.22／0.78。
   `retrieve_ms: off_mean=4 on_mean=331 (n=48)`（單次取樣，指示性）。
5. **逐類觀察**（不做 rerank 去留判準，#8 已決）：
   - 改寫層：on 0.50 對 shuffle 0.23，rerank 有訊號，但遠低於 #8 的 0.88——n 從 8 到 48 後，#8 是高估。k=1 的 para_recall_on 為 0.00：改寫段從未被排到第 1 位。
   - 干擾層：rerank 只在 neighbor 類把干擾段擠出前 k（0.78）；scope／version／responsibility／lexical 四類 on 皆 1.00，rerank 對這四類干擾無效。
   - 原層 k=1：on 0.87 對 off 0.76，rerank 讓正解更常排第 1；k=3 起兩者接近飽和。
6. **學到什麼**：(a) `requiredLanes` 讓每個 fixture 至少兩個三元組，spec 寫「一個三元組」的 scenario 在 execute 時全部要改——寫 scenario 前先對 harness 不變量；(b) BM25 的 IDF 是全表共享，corpus 一長既有目標的名次就漂，名次帶只能靠探針測試迭代，每批新內容都要全量重跑守門；(c) 小樣本的 rerank 效果會高估：這輪把改寫層 recall 從 0.88 修正到 0.50，引用數字要看 n。
