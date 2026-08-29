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
