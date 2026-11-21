# 网关账号级 failover 现状盘点（recon）

- 变更：`gateway-account-failover`
- 阶段：t1 需求梳理与现状盘点（只读代码，未修改任何实现）
- 方法：静态阅读 `backend/internal/{handler,service,config,repository,server}` 与迁移脚本，逐条核对 captain 提供的「已确认存在」清单，并回答 9 个指定问题
- 结论适用范围：本仓库当前工作树（sub2api fork），后端 Go 网关；不含前端

---

## 0. 摘要（TL;DR）

1. **账号级 failover 已经存在且是主路径默认行为**，不是"待建能力"。多账号分组内任一账号在特定上游错误下会自动换号重试，状态机为 `FailoverState`（`backend/internal/handler/failover_loop.go:124-153`），各入口复用同一状态机。
2. **换号不是"同组下一个"的稳定顺序**。每次重选都重新走一遍完整调度（候选快照 → 过滤 → 分层排序 → LRU + **组内随机打散**），失败账号靠**请求内排除集**`FailedAccountIDs` 规避，而不是游标/轮转。`backend/internal/service/gateway_scheduling.go:738-776`、`:1763-1780`。
3. **"可配置的失败判定规则"是最大缺口**。可配置的只有 1 个全局布尔（`gateway.failover_on_400`）与若干账号级凭据字段；可触发换号的状态码集合在 **4 处硬编码重复**，且**没有任何分组级 / DB 级 / 管理端配置**，迁移脚本中也不存在 failover 相关列（`grep failover backend/migrations` 无命中）。
4. **`maxAccountSwitches` 语义 = 切换次数上限**，实际最多尝试 `MaxSwitches+1` 个账号（`failover_loop.go:245-250`）。同一请求的实际上游调用数还会被「同账号重试」与「Gemini 服务内重试」放大（Gemini 最坏 4 账号 × 5 次 = 20 次上游请求）。
5. **失败账号大多不写库**：池模式默认跳过状态标记（`backend/internal/service/ratelimit_service.go:340-349`），API-key 账号未配置自定义错误码时只换号不标记；只有 429（带 reset）/OAuth 403/400 配置类/502 空响应等少数路径写 `accounts.temp_unschedulable_until`、`rate_limit_reset_at`、`model_rate_limits`。因此**坏账号在跨请求维度可能被反复命中**。
6. **幂等保护只有"写字节守卫"**：一旦向客户端写出任何字节即禁止换号（`backend/internal/handler/gateway_handler.go:1060-1064`）。网关路由**没有幂等中间件**，没有 `Idempotency-Key` 去重，换号必然产生真实重复上游调用（成本重复，本地计费不重复）。
7. **没有请求级 failover 时间预算**。Anthropic/Gemini 路径没有"首字节语义超时"（只有 OpenAI 有，默认还是关闭的）；最坏耗时由 `(MaxSwitches+1) × (单次转发 + 同账号重试 + 退避)` 叠加，可达数分钟。
8. **最终错误返回是"最后一个错误的映射"**，无聚合、Anthropic 路径不复制上游 `Retry-After`（只有 OpenAI 路径 `copyFailoverRetryAfter`）。

### 对 captain 初始「已确认存在」清单的三处修正

| 初始假设 | 核实结果 |
| --- | --- |
| `FailedRetryBackoffSeconds`（config.go:252）、`ErrorRetryDelaySeconds`（config.go:292）与 failover 相关 | **不相关**。前者属 `IdempotencyConfig`（管理端写接口幂等），后者属 `BatchImageConfig`（批量生图队列重试）。两者都不参与网关账号切换。 |
| `gateway_inflight_reservation.go` 参与账号选择/failover 并发控制 | **不相关**。该文件是**用户余额在途预留**（`InflightReservation`，按金额预留），与账号槽位并发、换号均无关。账号并发控制的真实入口是 `GatewayService.tryAcquireAccountSlot` → `ConcurrencyService.AcquireAccountSlot`（`gateway_scheduling.go:1193-1198`）。 |
| `maxAccountSwitches` 默认 10（`gateway_handler.go`） | 正确但**不完整**：`OpenAIGatewayHandler` 的兜底默认是 **3**（`openai_gateway_handler.go:368`），`grok_media.go:220-222` 也是 3。同一配置键在不同 handler 的零值兜底不一致。 |

---

## 一、请求流程时序

### 1.1 入口矩阵：谁有 failover、用哪套引擎

| 入口（路由） | 处理函数 | failover 引擎 | 证据 |
| --- | --- | --- | --- |
| `POST /v1/messages`（Anthropic 分组） | `GatewayHandler.Messages` | 内层 `for` + `FailoverState(h.maxAccountSwitches, hasBoundSession)` | `gateway_handler.go:665-1143` |
| `POST /v1/messages`（分组为 Gemini） | 同上，`platform == PlatformGemini` 分支 | `FailoverState(h.maxAccountSwitchesGemini, …)` | `gateway_handler.go:343-634` |
| `POST /v1/chat/completions` | `GatewayHandler.ChatCompletions` | 同一套 `FailoverState` | `gateway_handler_chat_completions.go:188-190`、`:370` |
| `POST /v1/responses`（Anthropic 后端） | `GatewayHandler.Responses` | 同一套 `FailoverState` | `gateway_handler_responses.go:188-194`、`:353` |
| `POST /v1beta/...`（Gemini 原生） | `GeminiV1BetaHandler` | `FailoverState(h.maxAccountSwitchesGemini, …)`，显式缓存绑定请求强制 `0` | `gemini_v1beta_handler.go:571-596`、`:783` |
| `POST /antigravity/v1/messages` | 同 `GatewayHandler.Messages`（`ForcePlatform`） | 同一套 | `server/routes/gateway.go:494-505` |
| `POST /v1/systemone/...` | `GatewayHandler.SystemOne` | 同一套 | `gateway_systemone.go:124-220` |
| OpenAI 网关：`/openai/v1/responses`、`/openai/v1/chat/completions`、`/openai/v1/messages`、WebSocket | `OpenAIGatewayHandler.*` | **另一套内联循环**（`switchCount` + `failedAccountIDs` + `oauth429FailoverState` + `passthroughFailoverState`），**不用** `FailoverState` | `openai_gateway_handler.go:657-665`、`:917-996`、`:1319-1545`、`:2767-2821` |
| 生图 / AlphaSearch / Grok 媒体 | 各自 handler | 又一套独立 `switchCount` 循环 | `openai_images.go:161-363`、`openai_alpha_search.go:131-263`、`grok_media.go:213-463` |

**含义**：设计任何"统一可配置失败判定规则"时，实际要收敛的是 **≥4 套并行实现**，不是一个函数。

### 1.2 Anthropic `Messages` 主时序（最完整路径）

| # | 阶段 | 代码 | 失败时行为 | 可切换? |
| --- | --- | --- | --- | --- |
| 1 | 鉴权 / 读 body / 解析 | `gateway_handler.go:125-170` | 400/401 直接返回 | 否 |
| 2 | 生成 `sessionHash` → `sessionKey`；查粘性绑定账号 | `:300-341` | 无绑定照常继续 | — |
| 3 | 外层循环（仅降级分组用）：**每次新建** `FailoverState` | `:665-666` | — | 重置预算 |
| 4 | 选号：`SelectAccountWithLoadAwareness(groupID, sessionKey, model, fs.FailedAccountIDs, metadataUserID, userID)` | `:683` | 见 4a/4b | — |
| 4a | 选号失败 + 排除集为空 → 区分「模型不存在(404)」与「无可用账号(503)」 | `:689-708`、`no_account_error.go:101-160` | 直接返回错误 | 否 |
| 4b | 选号失败 + 排除集非空 → `HandleSelectionExhausted` | `failover_loop.go:276-319` | 503 且 `SwitchCount<=Max`：清空排除集 + 2s 退避 + 置 `SingleAccountRetry` 后 `continue`（**重试同一批账号**）；否则 exhausted | **S3** |
| 5 | 拦截预热 / SUGGESTION（mock 响应） | `:741-754` | 直接 mock 返回 | 否 |
| 6 | 取账号并发槽位（可能等待，等待期写 ping） | `:756-807` | 无槽位无等待计划 → 503；队列满 → 429 | **否**（不换号） |
| 7 | 分组利润门终检 | `:809-826` | `RecordProfitVeto` → `continue` 重选（无上游请求） | **S1** |
| 8 | 会话槽注册 / 粘性绑定 / 用户消息队列 | `:830-891` | fail-open warn | — |
| 9 | 渠道映射 + Bedrock CC 兼容改写 body | `:893-906` | 400 直接返回 | 否 |
| 10 | 记录 `writerSizeBeforeForward`，注入 `SwitchCount` / `ForceCacheBilling` 上下文 | `:912-919` | — | **切换点边界** |
| 11 | `Forward`（按平台分流 Anthropic / Antigravity） | `:920-924` | 见 12 | — |
| 12a | `BetaBlockedError` | `:999-1005` | 400 直接返回 | 否 |
| 12b | `PromptTooLongError` | `:1007-1057` | 若配置了降级分组 → 切换分组重试（外层循环，**新 `FailoverState`**） | **S4** |
| 12c | `UpstreamFailoverError` 且 `c.Writer.Size() != writerSizeBeforeForward` | `:1060-1064` | `handleFailoverExhausted(..., streamStarted=true)` 终止（写 SSE error 帧） | **否** |
| 12d | `UpstreamFailoverError` → `HandleFailoverError` | `:1065-1078`、`failover_loop.go:193-267` | `Continue`（同账号重试 / 换号）｜`Exhausted`（返回错误）｜`Canceled`（客户端断开，499） | **S2** |
| 12e | 其他错误 | `:1080-1112` | `ensureForwardErrorResponse` → 502；`result!=nil` 时补记账 | 否 |
| 13 | 成功：RPM++ → 绑定/刷新粘性 → 提交 usage | `:1115-1138` | — | — |
| 14 | 终局错误映射 | `:1921-1985`、`:1987-2026` | 透传规则 → `mapUpstreamError` → JSON / SSE error 帧 | — |

### 1.3 可切换点汇总

| 编号 | 位置 | 触发条件 | 是否消耗 `SwitchCount` |
| --- | --- | --- | --- |
| **S1** | 利润门终检否决（`:809-826`） | 分组最低利润率不达标 | **否**（有独立上限 `maxProfitVetoAttempts=10`） |
| **S2** | Forward 返回 `UpstreamFailoverError` 且未写字节（`:1058-1079`） | 见 §1.5 状态码集合；（若 `RetryableOnSameAccount` 且额度未用完则先同账号重试，不消耗） | **是**（`failover_loop.go:250`） |
| **S3** | 选号返回"排除集已满"且上次错误是 503（`failover_loop.go:276-317`） | `LastFailoverErr.StatusCode==503 && SwitchCount<=MaxSwitches` | 否，但会**清空排除集** |
| **S4** | PromptTooLong → 降级分组（`:1014-1053`） | 分组配置了 `FallbackGroupIDOnInvalidRequest` 且满足平台/订阅约束 | 预算重置（新 fs） |
| **S5** | Gemini 显式缓存绑定 | `maxAccountSwitches=0` → **禁止**换号（`gemini_v1beta_handler.go:571-585`、`:592-602`） | 不适用 |
| **S6** | OpenAI 首字节超时 | `SafeToFailoverAfterWrite=true` 且 `firstOutputTimeoutSwitchCount<1`（`openai_gateway_handler.go:179`、`:3993-4001`） | 独立计数，最多 1 次 |

### 1.4 "直接返回、不换号"的拒绝清单（现状）

| 类别 | 证据 |
| --- | --- |
| 状态码不在换号集合内（400 除 `failover_on_400` 命中项外、404、409、413、422…） | `gateway_forward.go:47-54`、`:752-800` |
| 账号自定义错误码未命中（`custom_error_codes_enabled`）→ 不重试、不标记 | `account.go:1216-1226`、`:1373-1400`；`gateway_forward.go:36-44` |
| 池模式下不标记账号状态（仅显式 `temp_unschedulable_rules` 例外） | `ratelimit_service.go:340-349` |
| `BetaBlockedError`（本地策略拒绝） | `gateway_handler.go:999-1005` |
| 并发槽位失败 / 等待队列满 | `gateway_handler.go:759-767`、`:773-780` |
| 客户端已断开（`context.Canceled` → 标 499） | `failover_loop.go:203-205`、`:332-347` |
| 已向客户端写出字节（流已开始） | `gateway_handler.go:1060-1064` |
| Gemini 显式缓存绑定请求 | `gemini_v1beta_handler.go:573-575` |
| OpenAI 三态 `NextAccountAction=NextAccountStop` | `gateway_service.go:682-690`、`:724-726`；`grok_credential_failure.go` 等构造点 |
| OpenAI 首次输出超时（每请求最多 1 次） | `openai_gateway_handler.go:3993-4001` |
| Grok OAuth 429 风暴预算（第一个 429 后只允许 1 次跟随账号） | `openai_account_runtime_block_fastpath.go:20-29`、`:599-627` |

### 1.5 触发换号的状态码集合（四份重复实现）

| 平台/路径 | 集合 | 证据 |
| --- | --- | --- |
| Anthropic（`GatewayService.Forward`） | `{401,403,429,529} ∪ [500,∞)` | `gateway_forward.go:47-54` |
| Antigravity | 同上 | `antigravity_gateway_retry.go:780-787` |
| Gemini Messages Compat | 同上（另有服务内重试集合 `{429,500,502,503,504,529}` + OAuth `code_assist` 的 403） | `gemini_messages_compat_service.go:1706-1713`、`:1686-1704` |
| OpenAI | 自有实现（含 capacity shed / credential / 400 分类） | `openai_gateway_upstream_errors.go:303`、`:395-412` |

`failover_on_400` 只覆盖"兼容性差异"型 400，判定为**消息关键词白名单**：`anthropic-beta`、`beta feature`、`requires beta`、`thinking`、`thought_signature`、`signature`、`tool_use`、`tool_result`、`tools`（`gateway_upstream_response.go:212-237`，调用点 `gateway_forward.go:754-790`）。默认 `false`（`config.go:2532`）。

### 1.6 Gemini 分支差异（`gateway_handler.go:343-634`）

- 状态机只创建一次（`:344`），循环体与 Anthropic 主路径同构，但选号不传 `metadataUserID` / `subUserID`（"Gemini 不使用会话限制"，`:351` 注释）。
- **没有**外层降级分组循环、**没有**用户消息队列、**没有**利润门终检（`:501-564` 直接 Forward）。
- 流式写字节守卫位置相同（`:523-526`）。
- 服务层自带同账号重试：`geminiMaxAttemptsPerAccount() = MaxSameAccountRetriesGemini + 1`（默认 5）（`gemini_messages_compat_service.go:1678-1684`、`:721`、`:1275`）。

### 1.7 OpenAI 网关差异（`openai_gateway_handler.go`）

- 不用 `FailoverState`：`switchCount` / `failedAccountIDs` / `sameAccountRetryCount` / `lastFailoverErr` 全部是函数局部变量（`:657-665`）。
- 同账号重试逻辑与 Anthropic 共用**同一批工具函数**：`effectiveSameAccountRetryLimit` / `sameAccountRetryAllowed` / `sameAccountRetryDelayFor`（`failover_loop.go:58-122`，调用点 `openai_gateway_handler.go:948-968`）。
- 有额外"流内可安全切换"能力：`openAIForwardMayFailover`（`:3976-3984`，依据 `SafeToFailoverAfterWrite`），Anthropic 路径**没有**等价机制。
- 终局错误会复制上游 `Retry-After`：`handleFailoverExhausted` → `copyFailoverRetryAfter`（`:3592-3597`、`:3735-3745`）；Anthropic 路径的 `handleFailoverExhausted`（`gateway_handler.go:1921-1961`）不会。

---

## 二、账号选择算法说明

### 2.1 候选集合来源

1. `ListSchedulableAccounts(ctx, groupID, platform, hasForcePlatform)`（`scheduler_snapshot_service.go:211-273`）：
   - 先读 Redis 调度快照桶（按 `(groupID, platform, mode)` 分桶）；
   - 未命中或读失败 → 受 `db_fallback_enabled` 保护的 DB 回源，并尝试回写快照；
   - `mode` 决定是否启用混合调度（`resolveMode`，`:1580`）：Anthropic/Gemini 分组可把 `mixed_scheduling` 的 Antigravity 账号并入候选（`gateway_scheduling.go:80-86`、`:212`）。
2. 快照可能**短暂过期**（bucket rebuild 被节流），因此选号时对每个候选再执行 `isAccountSchedulableForSelection`（`gateway_scheduling.go:659-664` 注释）。

### 2.2 过滤链（Layer 2，逐条命中即跳过）

`gateway_scheduling.go:652-694` 顺序：

1. 请求内排除集 `excludedIDs`（= `fs.FailedAccountIDs`）→ `:656-658`
2. 账号级可调度性（状态/配额/过载/限流/temp-unschedulable）→ `:662-664`
3. 分组利润门资格 → `:665-667`
4. 平台允许（含混合调度规则）→ `:668-670`
5. 模型支持（model_mapping / 白名单）→ `:671-673`
6. 渠道级上游计费限制 → `:674-677`
7. 模型级限流（`model_rate_limits`）→ `:678-680`
8. 配额（配额快照）→ `:681-684`
9. 窗口费用（5h/7d window cost）→ `:685-688`
10. RPM 软限 → `:689-692`

另有 **Layer 1：模型路由优先**（`:276-521`）：命中 `group.model_routing` 时只在路由账号集合内选，且路由账号优先于粘性会话。

### 2.3 排序与随机（"下一个账号"到底是什么）

Layer 2 分层过滤（`gateway_scheduling.go:738-776`，每轮移除已尝试账号后重跑）：

1. 优先级最小集合 `filterByMinPriority`（`:1606-1623`）
2. 可选项：会话窗口最早重置集合 `filterBySoonestReset`（受 `scheduling.prefer_soonest_reset` 控制，默认 `false`，`config.go:2665`）
3. 负载率最低集合 `filterByMinLoadRate`（`:1626-1643`），`LoadRate >= 100` 的账号在更早一步已被剔除（`:730-736`）
4. `selectByLRU`：`LastUsedAt` 最早者；**同组多个候选取随机**（`:1734`），`preferOAuth`（Gemini 平台为 true，`:218`）时先在 OAuth 内选

路由路径额外调用 `shuffleWithinSortGroups`（`:460-479`、`:1763-1780`）。

**结论**：不存在"同组下一个账号"的稳定定义。同一失败序列在不同请求、甚至同一请求的不同轮次都可能选出不同账号；无法从日志推断"第 N 次切换到谁"，除非记录每次选号结果（现在只有 `sticky.account_selected` 日志，`:731-738`）。

### 2.4 粘性会话参与方式

- 生成：`GenerateSessionHash`（header 优先，回落 `prompt_cache_key`）；Gemini 分组加前缀 `gemini:`（`gateway_handler.go:315-318`）。
- 查询：先看 prefetched（`WithPrefetchedStickySession`，`:329-336`），否则 Redis `GetSessionAccountID`（`gateway_scheduling.go:131-141`）。
- 命中优先级：**路由内粘性（Layer 1.5）> 普通粘性（Layer 1.5）> 负载均衡（Layer 2）> 排队（Layer 3）**（`:344-434`、`:524-643`、`:779-793`）。
- 粘性账号仍要通过全部 gate，且 `shouldClearStickySession`（`gateway_service.go:560-571`）在账号不可调度或模型级限流时**删除绑定**。
- 成功后才刷新绑定（`gateway_handler.go:1129-1133`）；选号内部也会在命中时绑定（`gateway_scheduling.go:491`、`:761`、`:808`）。
- **失败不解除粘性**：换号失败路径只 `ReleaseAccountSession`（`:1068-1070`），没有删除 sticky 绑定。若失败未写库（池模式 / API-key 5xx），下一次同 session 请求仍会命中同一账号。

### 2.5 排除集与"会不会回到同一账号"

- `FailedAccountIDs` 是**每请求私有**的 `map[int64]struct{}`（`failover_loop.go:128`、`:145-153`），随请求结束丢弃。
- 单请求内正常路径不会回到同一账号（`:656-658` 排除）。
- 会"回到"的三种情况：
  1. **S3 清空排除集**：503 退避分支把 `FailedAccountIDs` 重置（`failover_loop.go:310-315`），只保留利润门否决的账号；
  2. **S4 降级分组**：新 `FailoverState`，排除集为空（`gateway_handler.go:666`）；
  3. **跨请求**：失败未写库 + 粘性未清 → 下次请求命中同一账号（§2.4）。
- 同账号重试**不进**排除集（`failover_loop_test.go:470`「同账号重试不应加入失败列表」）。

### 2.6 槽位与负载

- 槽位获取：`tryAcquireAccountSlot` → `ConcurrencyService.AcquireAccountSlot`（`gateway_scheduling.go:1193-1198`）。
- 批量负载：`GetAccountsLoadBatch`（`:708-716`，短 TTL 缓存，`concurrency_service.go:572-577`）。
- 等待计划：`AccountWaitPlan{MaxConcurrency, Timeout, MaxWaiting}`（`gateway_service.go:573-578`），粘性用 `scheduling.sticky_session_*`，兜底用 `fallback_*`（`gateway_scheduling.go:194-210`、`:786-791`）。
- **槽位获取失败不触发换号**（`gateway_handler.go:759-767`、`:773-780`）——这是与"账号失败"分离的另一类失败，设计时需明确边界。

---

## 三、需求映射表

需求拆解自用户原文："以分组为单位管理账号池 … 当前账号请求失败时，按可配置的失败判定规则自动切换到同组下一个可用账号继续请求，而不是立即向用户返回错误"。

| # | 需求项 | 现状 | 证据（文件:行） | 结论 |
| --- | --- | --- | --- | --- |
| R1 | 以**分组**为单位管理账号池 | `groups` + `account_groups`；调度快照按 `(groupID, platform, mode)` 分桶；选号入参 `groupID *int64` | `scheduler_snapshot_service.go:211-273`；`gateway_scheduling.go:100` | **已实现** |
| R2 | 当前账号失败 → 自动切到同组下一个可用账号 | 有；排除集 + 重选。但"下一个"是重算的负载/优先级/LRU 结果（含随机），非固定顺序；候选仍限于原分组 | `gateway_handler.go:683`、`:1058-1079`；`gateway_scheduling.go:738-776` | **部分实现** |
| R3 | 失败判定规则**可配置** | 可配置项仅：全局 `gateway.failover_on_400`（默认 false）；账号凭据 `pool_mode` / `pool_mode_retry_count` / `pool_mode_retry_status_codes` / `custom_error_codes` / `temp_unschedulable_rules`。可换号状态码集合在 4 处硬编码；无分组级/DB/UI 配置 | `config.go:1189-1190`、`:2532`；`gateway_forward.go:47-54`；`antigravity_gateway_retry.go:780-787`；`gemini_messages_compat_service.go:1706-1713`；`account.go:1228-1400` | **部分实现（核心缺口）** |
| R4 | 失败时**不要立即**返回错误（先换号） | 默认即换号；但存在 §1.4 的拒绝清单（含"未写字节守卫"以外的合理项与此处不适用的项） | `failover_loop.go:245-266` | **已实现** |
| R5 | 切换顺序可解释/可观测 | 无切换链记录（仅记"当前账号 + switch_count"）；无"本次候选顺序"日志；排序含随机打散 | `failover_loop.go:251-256`；`gateway_scheduling.go:1763-1780` | **部分实现** |
| R6 | 重试上限可配置 | `gateway.max_account_switches`(10)、`gateway.max_account_switches_gemini`(3)；语义=切换次数（尝试上限 +1）；与同账号重试、Gemini 服务内重试相乘；OpenAI handler 兜底默认 3 与 GatewayHandler 的 10 不一致 | `config.go:1192-1195`、`:2533-2534`；`gateway_handler.go:84-93`；`openai_gateway_handler.go:368-373`；`failover_loop.go:245-250` | **部分实现** |
| R7 | 失败账号**状态标记与恢复** | 写到 `accounts.temp_unschedulable_until` / `rate_limit_reset_at` / `model_rate_limits`；到期自动恢复 + 管理端手动清除。但换号本身**不保证**写库（池模式/API-key 未配置错误码）；无"连续失败 N 次自动冷却" | `migrations/020_add_temp_unschedulable.sql:5`；`migrations/005_schema_parity.sql:15`；`ratelimit_service.go:333-349`、`:2265-2287`；`antigravity_gateway_retry.go:797-825` | **部分实现** |
| R8 | 并发安全 | 请求内状态私有、无共享锁；账号并发由 Redis 槽位 + 批量负载控制；快照可能过期但在选号时二次校验。**跨请求无"失败熔断"**；写库无"只延长不缩短"保护 | `failover_loop.go:124-153`；`gateway_scheduling.go:1193-1198`、`:652-694`；`gateway_service.go:753-771`（对比 OpenAI 的"只延长"接口 `SetRateLimitedIfLater`：`openai_gateway_grok.go:1826`） | **部分实现** |
| R9 | 幂等/半截响应保护 | 有"写字节即禁止切换"守卫（含 SSE 不拼接）；无 `Idempotency-Key` 去重；换号会造成真实重复上游请求；失败尝试不产生本地 usage（`result=nil`） | `gateway_handler.go:1060-1064`、`:1104-1111`；`gateway_handler_stream_failover_test.go:26-140`；`server/routes/gateway.go:180-189`；`handler/idempotency_helper.go:45`（仅管理端） | **部分实现** |
| R10 | 超时处理与总预算 | 有上游响应头超时（默认 600s）、流数据间隔（180s）、keepalive（10s）、同账号重试退避（上限 8s）、503 单账号退避（2s/轮）、Antigravity 线性延时。**无请求级 failover 预算**；Anthropic/Gemini 无首字节语义超时 | `config.go:2523`、`:2651-2653`；`failover_loop.go:33-52`、`:258-264`、`:298-305`；`repository/http_upstream.go:56-58` | **部分实现（预算缺失）** |
| R11 | 日志与告警 | 有结构化 warn 日志 + `ops_error_logs.upstream_errors(kind)` 事件 + `account_switch_count` 指标；告警规则支持 `error_rate`/`upstream_error_rate` + 邮件通道。**无切换率/耗尽率专项告警，无分组维度** | `failover_loop.go:223-229`、`:251-256`；`gateway_forward.go:701`、`:737`、`:782`；`ops_metrics_collector.go:568-587`；`ops_repo_metrics.go:46`、`:113`；`ops_alert_evaluator_service.go:606-615`、`:678` | **部分实现** |
| R12 | 最终错误返回策略 | 透传规则 → `mapUpstreamError`；401/403→502、429→429、529→503、5xx→502；无聚合错误、Anthropic 路径无 `Retry-After` | `gateway_handler.go:1921-1985`；`openai_gateway_handler.go:3592-3597`、`:3735` | **部分实现** |

---

## 四、缺口清单（按严重度）

### P0（阻塞设计目标）

**G1. 失败判定规则不可配置、分散且重复**
- 证据：`gateway_forward.go:47-54`、`antigravity_gateway_retry.go:780-787`、`gemini_messages_compat_service.go:1706-1713`、`openai_gateway_upstream_errors.go:303`；`config.go:1189-1190` 只有 `failover_on_400`；`grep failover backend/migrations` 无命中；`group.go` 无 failover 字段。
- 影响：用户要求"按可配置的失败判定规则"，现状只能改代码或按账号逐个配凭据；无法按分组差异化（重要客户组 vs 试用组）。

**G2. 没有请求级 failover 时间/成本预算**
- 证据：`failover_loop.go:33-52`（同账号 3 次、退避上限 8s、503 退避 2s）、`:258-264`（Antigravity 每轮 +1s）、`gemini_messages_compat_service.go:1678-1684`（每账号 5 次）。
- 最坏估算：Anthropic 11 账号 × 4 次同账号尝试 = 44 次上游调用；Gemini 4 账号 × 5 次 = 20 次；叠加退避可达分钟级，且客户端无任何进度反馈。
- 影响：首字节延迟与上游成本不可控；对长尾请求可能触发客户端超时，把"可恢复"变成"用户可见失败"。

**G3. 失败账号跨请求不隔离**
- 证据：`ratelimit_service.go:340-349`（池模式默认跳过标记）；`gateway_upstream_response.go:530-548`（API-key 未配置错误码不标记）；`gateway_handler.go:1068-1070`（失败只释放会话槽，不清粘性）。
- 影响：并发请求与后续请求反复命中同一坏账号，每次都要走完"失败 → 换号"链路，放大延迟与上游成本；与用户期望"自动跳过坏账号"存在语义差。

**G4. 幂等/重复上游调用**
- 证据：网关路由无幂等中间件（`server/routes/gateway.go:180-189`）；`IdempotencyConfig` 仅服务管理端写接口（`config.go:243-260`、`handler/idempotency_helper.go:45`）。
- 影响：一次用户请求可能对上游产生多次计费调用；无审计关联（无法按 `request_id` 聚合"本次请求打了几次上游"）。

### P1（影响可运维性与体验）

**G5. `maxAccountSwitches` 语义与默认值不一致**
- 证据：`failover_loop.go:245-250`（语义=切换次数，尝试上限 +1）；`gateway_handler.go:84`（兜底 10）vs `openai_gateway_handler.go:368`（兜底 3）vs `grok_media.go:220-222`（3）。
- 影响：同一配置在零值场景下行为不同；"10 次切换"容易被误读为"10 个账号"。

**G6. 切换顺序不可预测、不可复现**
- 证据：`gateway_scheduling.go:746-749` + `:1734`（随机）+ `:1763-1780`（组内 shuffle）。
- 影响：用户/运维无法回答"为什么切到 B 而不是 C"；难以做确定性回归测试；`failover_loop_profit_veto_test.go` 已用模拟循环规避这一点（说明测试层已感知该问题）。

**G7. 流式可切换点过严且平台不一致**
- 证据：Anthropic/Gemini 用 `c.Writer.Size()!=before`（`gateway_handler.go:1060-1064`、`:523-526`）全量否决；OpenAI 有 `SafeToFailoverAfterWrite`（`gateway_service.go:707`、`openai_gateway_handler.go:3976-3984`）。
- 影响：Anthropic 路径一旦上游 200 后中断（常见于长思考/网络抖动），无法换号，只能给用户一个 SSE error 帧；Uni 平台行为差异让"可切换性"无法统一描述。

**G8. 终局错误信息贫乏**
- 证据：`gateway_handler.go:1921-1961` 只用 `LastFailoverErr`；无 `Retry-After` 复制；无 attempts 汇总。
- 影响：用户看到"Upstream service temporarily unavailable"无法判断是"全部账号 429"还是"全部 5xx"；客户端无退避提示。

**G9. 无切换专项告警与分组维度**
- 证据：`ops_metrics_collector.go:568-587`（全局 `account_switch_count`）、`ops_alert_evaluator_service.go:606-615`（仅 error_rate/upstream_error_rate）、`ops_repo_metrics.go:186-188`（`platform IS NULL AND group_id IS NULL` 取全局快照）。
- 影响：分组级容灾能力退化（例如 A 组 10 个账号只剩 1 个可用）不会触发任何告警。

### P2（设计需覆盖，但非阻塞）

**G10. 写库竞态：临时封禁可能缩短既有冷却**
- 证据：`gateway_service.go:753-771` → `tempUnscheduleGoogleConfigError` / `tempUnscheduleEmptyResponse`（`antigravity_gateway_retry.go:797-825`）直接 `SetTempUnschedulable(now+1m)`；对比 OpenAI 的"只延长"接口 `SetRateLimitedIfLater`（`openai_gateway_grok.go:1826`、`:1865-1867`）。
- 影响：并发失败时较长的冷却窗口可能被覆盖为 1 分钟，坏账号提前回到池中。

**G11. 503 退避 + 排除集清空的状态机复杂**
- 证据：`failover_loop.go:276-317`；利润否决活锁修补 `:290-296`、`:310-315`（`maxProfitVetoAttempts=10`）。
- 影响：新增"可配置失败规则"时会引入新的"不推进 `SwitchCount` 的重试来源"，可能重现活锁；需要统一"预算推进"口径。

**G12. 特殊路径的不可切换性不可见**
- 证据：`gemini_v1beta_handler.go:571-585`（缓存绑定 `maxAccountSwitches=0`，无日志/指标）；`gateway_service.go:708`（`BoundUpstreamPassthrough` 原样回写）。
- 影响：排障时无法区分"换号失败"与"本来就不允许换号"。

---

## 五、现有可配置项清单

### 5.1 全局配置（`config.yaml` / 环境变量）

| 配置键 | 默认值 | 作用 | 证据 |
| --- | --- | --- | --- |
| `gateway.max_account_switches` | `10` | 单请求换号次数上限（尝试账号数上限 = 值 + 1） | `config.go:1193`、`:2533`；`gateway_handler.go:88-90` |
| `gateway.max_account_switches_gemini` | `3` | Gemini 平台换号次数上限（注释："因 API 限制更严格"） | `config.go:1194-1195`、`:2534` |
| `gateway.max_same_account_retries_gemini` | `4`（校验范围 0–10） | Gemini **服务层**同账号重试次数（每账号最多 值+1 次请求） | `config.go:1196-1197`、`:2535`、`:3496-3497`；`gemini_messages_compat_service.go:1678-1684` |
| `gateway.failover_on_400` | `false` | 是否对"兼容性差异"型 400 换号（关键词白名单见 §1.5） | `config.go:1189-1190`、`:2532`；`gateway_upstream_response.go:212-237` |
| `gateway.response_header_timeout` | `600`（秒；代码兜底 300） | 等待上游响应头超时（Anthropic 主路径） | `config.go:1058-1060`、`:2523`；`repository/http_upstream.go:56-58` |
| `gateway.openai_response_header_timeout` | `0`（无超时） | OpenAI/Codex 上游响应头超时 | `config.go:1061-1063`、`:2524` |
| `gateway.grok_response_header_timeout` | `120` | xAI/Grok 首字节前等待上限 | `config.go:1065-1067`、`:2526` |
| `gateway.openai_first_output_timeout_seconds` | `0`（关闭；开启需 30–600） | OpenAI 首个**语义输出**超时，触发后可安全换号 | `config.go:1068-1069`、`:2527`、`:3499-3501` |
| `gateway.openai_high_effort_first_output_timeout_seconds` | `0`（回落上项） | high/xhigh/max 推理的独立首字节超时 | `config.go:1070-1072`、`:2528` |
| `gateway.stream_data_interval_timeout` | `180`（秒） | 流内数据间隔超时（流已开始后） | `config.go:1165-1166`、`:2651` |
| `gateway.stream_keepalive_interval` | `10`（秒） | 下游 SSE keepalive 间隔 | `config.go:2652`；`gateway_upstream_response.go:802-816` |
| `gateway.log_upstream_error_body` | `true` | 是否记录上游错误体（决定 ops 事件 `Detail` 是否有内容） | `config.go:1181-1184`、`:2529-2530` |
| `concurrency.ping_interval` | `10`（秒） | 排队等待期下发的 ping 间隔（在 `writerSizeBeforeForward` 之前写入） | `config.go:2709` |
| `gateway.scheduling.sticky_session_max_waiting` | `3` | 粘性账号满载时允许排队数 | `config.go:2660` |
| `gateway.scheduling.sticky_session_wait_timeout` | `120s` | 粘性排队超时 | `config.go:2661` |
| `gateway.scheduling.fallback_wait_timeout` | `30s` | 兜底排队超时 | `config.go:2662` |
| `gateway.scheduling.fallback_max_waiting` | `100` | 兜底排队上限 | `config.go:2663` |
| `gateway.scheduling.fallback_selection_mode` | `last_used` | 兜底排序模式 | `config.go:2664`；`gateway_scheduling.go:780` |
| `gateway.scheduling.prefer_soonest_reset` | `false` | 是否优先选"会话窗口最早重置"的账号 | `config.go:2665`；`gateway_scheduling.go:743-745` |
| `gateway.scheduling.load_batch_enabled` | `true` | 是否启用批量负载感知调度（关闭则走 legacy 顺序） | `config.go:2666`；`gateway_scheduling.go:164-212` |
| `gateway.scheduling.slot_cleanup_interval` | `30s` | 槽位清理周期 | `config.go:2670` |
| `gateway.scheduling.db_fallback_enabled` | `true` | 快照缺失时是否回源 DB | `config.go:2671` |

### 5.2 账号级配置（`accounts.credentials` / `extra`）

| 字段 | 默认 | 作用 | 证据 |
| --- | --- | --- | --- |
| `pool_mode` | `false` | 池模式：上游错误**不标记本地账号状态**，优先同账号重试 | `account.go:1228-1240`；`ratelimit_service.go:340-349` |
| `pool_mode_retry_count` | `3`（0–10 截断） | 同账号重试次数（`HandleFailoverError` 的 `retryLimit`） | `account.go:1242-1265`；`failover_loop.go:113-122` |
| `pool_mode_retry_status_codes` | `[401,403,429]` | 池模式下触发同账号重试的状态码（`[]`=关闭） | `account.go:1287-1360` |
| `custom_error_codes_enabled` / `custom_error_codes` | 未启用 | 仅对白名单状态码做"重试 + 标记"；未命中则完全不处理（仍可换号） | `account.go:1216-1226`、`:1373-1400`；`gateway_forward.go:36-44` |
| `temp_unschedulable_rules` | 空 | 按错误码/关键词临时停调（支持 `account` / `model` 粒度、时长） | `account.go:135-144`；`ratelimit_service.go:333-398` |
| `model_rate_limits` / `antigravity_quota_scopes` | 空 | 模型级限流 / Antigravity 配额维度 | `ratelimit_service.go:2275-2287` |

### 5.3 与 failover **无关**但易被误认为相关的配置

| 配置键 | 真实用途 | 证据 |
| --- | --- | --- |
| `idempotency.failed_retry_backoff_seconds` | 管理端写接口幂等记录失败退避 | `config.go:243-260`、`:3448` |
| `batch_image.error_retry_delay_seconds` | 批量生图队列任务失败重排延时 | `config.go:292`；`batch_image_worker.go:72` |

---

## 六、不确定 / 需进一步确认的问题

1. **用户心智中的"下一个账号"**：是希望**确定顺序**（例如按优先级列表 + 轮转，可解释），还是接受"负载最低/LRU 的重新计算"？这决定 G6 是"改进"还是"保持"。
2. **"失败判定规则"的配置粒度**：分组级（`groups` 表新列）还是全局 + 分组覆盖？是否需要管理端 UI？本仓库尚无 failover 相关迁移（`backend/migrations` 无命中），需与 t2/t4 对齐是否新增迁移编号。
3. **是否需要"失败即写库"**：G3 的修复方向（写 `temp_unschedulable_until` 冷却 N 秒）会改变现有"池模式不标记账号"的契约（`ratelimit_service.go:340-349` 注释明确该契约），需产品确认是否对所有账号类型统一。
4. **Anthropic 路径是否需要"首字节语义超时"**：现只有 `response_header_timeout`(600s) + 流间隔(180s)。若要引入（对齐 OpenAI 的 `first_output_timeout`），需确认阈值与"是否允许在已发 ping 后切换"。
5. **重复上游调用的成本口径**：换号导致的上游重复计费是否需要在 ops 里单独记账/展示？当前 failover 错误路径 `result=nil` 不入账（`gateway_handler.go:1104-1111`），无法统计"因换号产生的额外上游成本"。
6. **是否统一 4 套 failover 实现**：本次设计是只收敛 Anthropic/Gemini/Antigravity（`FailoverState`），还是连 OpenAI/生图/AlphaSearch/Grok 媒体一起纳管？范围直接决定实施步骤规模。
7. **`Retry-After` 语义**：Anthropic 路径终局是否应回写上游 `Retry-After`（OpenAI 路径已有 `copyFailoverRetryAfter`）？还是统一改为"按剩余冷却时间估算"？
8. **告警阈值口径**：分组可用账号数 / 切换成功率 / 耗尽率，哪一个作为 P0 告警指标？现有告警规则只有 `error_rate` / `upstream_error_rate` 等（`ops_alert_evaluator_service.go:606-615`）。
9. **`maxAccountSwitches` 的兼容性**：是否允许把语义改成"尝试次数"（会改变现有配置的实际含义），还是保留"切换次数"并新增 `max_account_attempts`？需评估现网配置（默认 10）的影响面。
10. **`PingInterval` 与 `writerSizeBeforeForward` 的严格时序**：本次只静态确认 ping 在取槽位阶段写入、`writerSizeBeforeForward` 在其后采样（`gateway_handler.go:791-798` → `:919`），未做运行时验证；若存在异步 ping goroutine 跨越采样点，可能导致 `Writer.Size()` 误判（t3 风险审查需实测）。

---

## 附录 A：现有测试索引（可直接复用为回归基线）

| 测试文件 | 覆盖点 |
| --- | --- |
| `handler/failover_loop_test.go` | `FailoverState` 全状态机（同账号重试、切换计数、耗尽、取消、排除集去重、503 退避清空） |
| `handler/failover_loop_profit_veto_test.go` | 利润否决参与换号、退避清空后利润账号必须留在排除集、`maxProfitVetoAttempts` |
| `handler/gateway_handler_stream_failover_test.go` | **写字节守卫**：Messages / Gemini 路径写 SSE 后必须放弃换号；未写字节时不触发 |
| `handler/gateway_handler_error_fallback_test.go` | 转发失败后的错误兜底与终局响应 |
| `service/gateway_pool_mode_retry_test.go` | 池模式同账号重试与状态码集合 |
| `handler/openai_gateway_credential_failover_test.go` | 凭据类失败换号 + `Retry-After` 复制 |
| `service/gateway_account_selection_test.go`（选号系列另散落在 `gateway_multiplatform_test.go`） | 选号：优先级、LRU、粘性、排除集、平台过滤、混合调度 |
| `service/gateway_service_selection_failure_stats_test.go` | 选号失败原因统计（excluded / unschedulable / model_rate_limited / profit…） |
| `service/gateway_channel_restriction_fallback_test.go` | 渠道限制下的降级分组选择 |
| `handler/openai_gateway_ws_failover_resume_test.go` | WebSocket 换号与恢复 |
| `service/scheduler_snapshot_hydration_test.go` | 快照账号 hydrate（凭证补全）与限流跳过 |
| `service/gemini_error_policy_*_test.go` | Gemini 错误策略 / 跳过写库 / 同账号重试次数 |
| `service/openai_first_output_timeout_test.go` | 首字节语义超时触发换号、`SafeToFailoverAfterWrite`、每请求 1 次上限 |

## 附录 B：关键常量速查

| 常量 | 值 | 位置 |
| --- | --- | --- |
| `maxSameAccountRetries` | 3 | `failover_loop.go:36` |
| `sameAccountRetryDelay` | 500ms | `failover_loop.go:38` |
| `maxRequestScopedRetryDelay` | 8s | `failover_loop.go:41` |
| `singleAccountBackoffDelay` | 2s | `failover_loop.go:45` |
| `maxProfitVetoAttempts` | 10 | `failover_loop.go:51` |
| `maxRetryAttempts`（服务层原地重试） | 5 | `gateway_forward.go:25` |
| `retryBaseDelay` / `retryMaxDelay` / `maxRetryElapsed` | 300ms / 3s / 10s | `gateway_forward.go:28-33` |
| `defaultPoolModeRetryCount` / `maxPoolModeRetryCount` | 3 / 10 | `account.go:1243-1244` |
| 默认池模式可重试状态码 | `[401,403,429]` | `account.go:1289` |
| `openAIOAuth429StormMaxAccountSwitches` | 1 | `openai_account_runtime_block_fastpath.go:21` |
| `maxOpenAIFirstOutputTimeoutSwitches` | 1 | `openai_gateway_handler.go:179` |
| `googleConfigErrorCooldown` / `emptyResponseCooldown` | 1m / 1m | `antigravity_gateway_retry.go:798`、`:813` |
| `defaultResponseHeaderTimeout`（代码兜底） | 300s | `repository/http_upstream.go:58` |
