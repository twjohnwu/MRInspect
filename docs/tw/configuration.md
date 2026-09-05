# 設定

要選哪個 AI backend、要存哪些密鑰，以及 MRInspect 會讀取的每一個環境變數。

[English](../us/configuration.md)

## AI provider

MRInspect 支援三種 AI backend，用 `AI_PROVIDER` 選擇：

| Provider | `AI_PROVIDER` 值 | 預設模型 | 金鑰變數 |
|---|---|---|---|
| Google Gemini | `gemini` | `gemini-3.1-pro-preview` | `AI_PROVIDER_KEY` |
| Anthropic Claude | `anthropic` | `claude-sonnet-5` | `AI_PROVIDER_KEY` |
| OpenAI | `openai` _(預設)_ | `gpt-5.6` | `AI_PROVIDER_KEY` |

> **注意：** superpowers 層一律使用 Anthropic（Claude Code CLI 的要求）。不管 mrinspect runner 用哪個 provider，都要設定 `ANTHROPIC_API_KEY`。

## 必要密鑰

使用前先在你的 CI/CD 密鑰存放區設定這些：

| 變數 | 誰需要 | 說明 |
|---|---|---|
| `AI_PROVIDER_KEY` | mrinspect | 選定 AI provider 的 API 金鑰（Gemini / Anthropic / OpenAI） |
| `ANTHROPIC_API_KEY` | superpowers | Anthropic API 金鑰（Claude Code CLI 一律使用 Anthropic） |
| `GITLAB_TOKEN` | 所有層 | 具備 `api` 與 `write_repository` scope 的 GitLab token |

**GitLab：** `Settings → CI/CD → Variables` → 每一項都勾選 **Protected** 與 **Masked**。

**GitHub：** `Settings → Secrets and variables → Actions → New repository secret`。

## 完整變數對照

以下任何變數也可以透過工作目錄下的 `.env` 檔提供。`.env` 只會補上尚未設定的變數——process 環境變數一律優先。切勿把 `.env` 提交進版控。

<details>
<summary>mrinspect 變數</summary>

| 變數 | 預設值 | 說明 |
|---|---|---|
| `AI_PROVIDER` | `openai` | AI provider：`anthropic` \| `gemini` \| `openai` |
| `AI_PROVIDER_KEY` | _(必填)_ | 選定 provider 的 API 金鑰 |
| `GITLAB_TOKEN` | _(必填)_ | GitLab API token |
| `GITLAB_API_BASE` | `https://gitlab.com/api/v4` | GitLab API base URL（自架時要設） |
| `MRI_SERVICE_NAME` | `unknown` | 服務名稱——必須對應到 `projects/registry.yaml` 裡的某個 key |
| `MRI_SERVICE_TYPE` | `backend` | 服務類型：`backend` \| `frontend` \| `ai` \| `iac` |
| `MRI_REVIEW_MODE` | `single` | Go runner 的審查模式：`single` \| `multi`；`multi` 會跑設定好的 lane 平行展開 |
| `MRI_LANE_CONCURRENCY` | `4` | Go runner 的最大平行 lane 數；必須是正整數，否則會記錄一則警告並改用 `4` |
| `IS_SELF_REFLECTION` | `false` | 設為 `true` 會再跑一次 AI 驗證 |
| `PROJECTS_DIR` | `./projects` | projects 目錄的路徑 |
| `MRI_RAG_STORE` | _(未設定)_ | Go `path` 來源要用的明確 SQLite 路徑；把 `path` 放在來源鏈最前面即可讓它優先 |
| `MRI_RAG_SOURCE` | `package,artifact,baked` | Go runner 的 store 來源鏈，以逗號分隔並依序嘗試 |
| `MRI_RAG_PACKAGE_VERSION` | `latest` | GitLab generic-package 版本；設定明確版本可釘住 RAG store |
| `MRI_RAG_EMBEDDINGS` | `false` / _(未設定；關閉)_ | 啟用選用的語意重排；它在小型語料庫的價值尚未量測。啟用時，索引會把 resource-set 文字傳送到選定的外部 embeddings API，因此會產生 provider 用量成本與資料外送。API 呼叫前，索引會印出成本行 `embedding <chunk-count> chunks (~<request-count> requests)`。檢索時若 embeddings 無法使用或不相容，或 provider 發生錯誤，會降級回 BM25，並在 review scope/footer 顯示可見原因。 |
| `MRI_RAG_EMBED_KEY` | _(未設定)_ | 選定任一 embedding provider 時共用的唯一憑證；啟用 embeddings 時必填，且與 `AI_PROVIDER_KEY` 各自獨立。 |
| `MRI_EMBED_PROVIDER` | _(未設定)_ | Embedding provider：`openai` \| `gemini`。模型為固定常數：`text-embedding-3-small`（OpenAI，1536 維）與 `gemini-embedding-001`（Gemini，768 維）。 |
| `MRI_RAG_ON_NORMATIVE_EVICTION` | `warn` | full 模式的 normative 段落被裁掉時，Go runner 的處理方式：`warn` \| `fail` |
| `MRI_PROMPT_BUDGET_FACTOR` | `0.8` | 與選定模型的 prompt 上限相乘的正浮點數。預設值 0.8 也吸收了估算器實測約 11% 的系統性低估。 |
| `MRI_DIFF_PROMPT_SHARE` | `0.85` | 有效模型 prompt 預算中 diff 可佔用的比例；超額的 diff 會整檔剔除（先剔不可人審 pattern，再依大小由大到小剔除；hunk 永不截斷），剔除清單會同時揭露於 prompt 與 review footer。無效值會退回預設值。實測事故資料顯示 diff 佔 prompt 超過約 93% 時，模型會省略必要區段；降到約 85% 即恢復格式遵循——上限依模型預算比例縮放，而非固定 KB 值。 |
| `MRI_REVIEW_DUMP_ENABLED` | _(未設定)_ | 設為精確字串 `true` 才會在 review 驗證失敗時把 failure-only 的 prompt/response dump 寫入 CI job log。預設關閉：預設的失敗 log 只帶驗證錯誤原因、找到的標題清單、清洗前後長度，以及 prompt 與 response 的 sha256 前 12 碼——絕不含內容。僅在除錯時開啟，且僅限 diff 不含敏感值的 repo。 |
| `MRI_DAILY_TOKEN_BUDGET` | _(未設定；停用)_ | 離線 eval 的預算對照。以單次執行的 token 總量和每日預算比較，不保留跨執行狀態；usage 未知的呼叫會讓總量成為帶 `≥` 的下限，超過預算只記錄 Warn。建議值為每日 `2500000` tokens，推導自每日 20 次 review × 高情境 100k input + 8k output tokens ≈ 216 萬，再加上餘裕。定價研究取回日期為 2026-08-30；實測校準中，一輪 9 次 small-diff review 共使用 ≥79.5k tokens，每次約 6–10k，因此 250 萬是安全上限。 |
| `MRI_EVAL_ALLOW_CI` | _(未設定)_ | 設為精確字串 `true` 才允許在 CI 內執行 `mrinspect eval`，以防意外產生三倍模式花費。 |
| `ANTHROPIC_MODEL` | `claude-sonnet-5` | 覆寫 Anthropic 模型 |
| `GEMINI_MODEL` | `gemini-3.1-pro-preview` | 覆寫 Gemini 模型。既有帳號可覆寫回 `gemini-2.5-pro`；free-tier API 金鑰對 3.1 Pro 的配額為零，因此請改用 `gemini-2.5-flash`。 |
| `OPENAI_MODEL` | `gpt-5.6` | 覆寫 OpenAI 模型 |
| `ANTHROPIC_MAX_TOKENS` | `4000` | Anthropic 的最大輸出 token 數 |
| `GEMINI_MAX_TOKENS` | `8000` | Gemini 的最大輸出 token 數 |
| `OPENAI_MAX_TOKENS` | `4000` | OpenAI 的最大輸出 token 數 |
| `AI_PER_CALL_TIMEOUT_MS` | `120000` | 每次 AI provider 呼叫的逾時時間（毫秒）；逾時的單次嘗試會接續進入重試 |
| `MRI_AI_LOG_DIR` | _(未設定；停用)_ | 本機目錄，用來存放每次執行的 `ai-log-<timestamp>-<pid>.jsonl` transcript，包含每一次 AI provider 嘗試，也包含失敗與重試。範例：`.ai-log/`。檔案含有完整 prompt 與 response，請只保留在本機並視為敏感資料處理。 |
| `MRI_MODEL_LIMITS` | _(未設定)_ | Go runner：以逗號分隔的 `model:tokens` 項目，會疊在內建的 context-window 預設值之上；格式錯誤的項目（缺冒號、token 非正整數）會導致啟動失敗 |
| `API_RETRY_ATTEMPTS` | `3` | API 重試次數 |
| `API_RETRY_DELAY_MS` | `1000` | 初始重試延遲（毫秒） |
| `API_MAX_RETRY_DELAY_MS` | `10000` | 最大重試延遲（指數退避的上限） |
| `API_TIMEOUT_MS` | `30000` | HTTP 請求逾時（毫秒） |
| `MAX_FILES_CHANGED` | `50` | MR 更動的檔案數超過此值就略過審查 |
| `AI_RETRY_ATTEMPTS` | `3` | AI 輸出未通過驗證時的重試次數 |
| `LOG_LEVEL` | `info` | 記錄層級：`debug` \| `info` |
| `AI_REVIEW_METRICS_FILE` | `./mrinspect-metrics.json` | metrics JSON 的輸出路徑 |

### 離線檢索檢查

`mrinspect eval -retrieval [-store PATH] [-report PATH]` 會把 `eval/fixtures/` 的每個 fixture 走一次生產的 lane 查詢路徑，對本機 store 計算 rerank 關／開兩欄的 recall@k 與 MRR，寫到 `eval/RETRIEVAL.md`（預設）。各 fixture 的相關段落定義在 `eval/retrieval-golden.yaml`。整趟不呼叫生成 API；`MRI_RAG_EMBEDDINGS=true` 時每個 fixture × resource set 各一次 embedding 呼叫。store 若以舊 corpus 建置會被拒絕，請先重跑 `mrinspect index`。報告只列數字，不對檢索品質下結論。

</details>

<details>
<summary>執行模式變數</summary>

mrinspect 會依 `CI_PIPELINE_SOURCE` 自動判斷執行模式。

**本地 MR 模式** — 由 `CI_PIPELINE_SOURCE == "merge_request_event"` 觸發（GitLab 自動設定）：

| 變數 | 由誰設定 | 說明 |
|---|---|---|
| `CI_PROJECT_ID` | GitLab（自動） | 目前 repository 的 project ID |
| `CI_MERGE_REQUEST_IID` | GitLab（自動） | 目前 repository 的 MR IID |
| `CI_COMMIT_REF_NAME` | GitLab（自動） | 來源分支 |
| `CI_MERGE_REQUEST_TARGET_BRANCH_NAME` | GitLab（自動） | 目標分支 |

**跨 repo 觸發模式** — 由 `CI_PIPELINE_SOURCE == "trigger"` 觸發（由呼叫端 pipeline 傳入）：

| 變數 | 說明 |
|---|---|
| `MRI_PROJECT_ID` | 被審查 repository 的 project ID |
| `MRI_MR_IID` | 目標 repository 的 MR IID |
| `MRI_SOURCE_BRANCH` | 該 MR 的來源分支 |
| `MRI_TARGET_BRANCH` | 該 MR 的目標分支 |
| `MRI_SERVICE_NAME` | 用來查找 profile 的服務名稱 |
| `MRI_SERVICE_TYPE` | 服務類型覆寫值 |

</details>
