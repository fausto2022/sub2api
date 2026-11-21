# 网关账号级容灾能力优化 · 架构设计

- 变更：`gateway-account-failover`
- 阶段：t2 架构设计（**只做设计，不改代码**）
- 输入依赖：`openspec/changes/gateway-account-failover/recon.md`（t1 只读盘点，374 行）
- 结论适用范围：本仓库当前工作树（sub2api fork）后端 Go 网关；不含前端、不含管理端 UI

---

## 0. 摘要与设计立场

### 0.1 一句话结论

**账号级 failover 已经存在，本设计不重建它。** 本次要补的是 4 个真实缺口：

| 缺口 | 本设计的补法 | 是否改变默认行为 |
| --- | --- | --- |
| **G1** 失败判定规则不可配置、4 处硬编码重复 | 引入 `FailoverPolicy` + `FailoverClassifier` 单一判定源，三级就近覆盖（账号 > 分组 > 全局） | 否（默认规则表 = 现有硬编码集合逐项等价） |
| **G2** 无请求级时间/成本预算 | `FailoverBudget`：整请求 deadline + 总尝试上限 + 切前剩余预算检查 | 否（默认关闭，见 §5 默认值表） |
| **G3** 失败账号跨请求不隔离 | T2 冷却窗口（Redis），**不改 T3 写库契约** | 否（`cooldown.enabled` 默认 `false`） |
| **G4** 幂等/重复上游调用无审计 | 切换链结构化审计 + 可选"非流式有副作用请求禁止换号" | 否（默认关闭） |

> **关键取舍**：方案 B（冷却层）的实现进主干，但**默认关闭**。因此**默认配置下方案 B 与方案 A 的行为完全一致**，回滚 = 改一个布尔配置，不需要回滚代码。这是本设计最重要的可运维性决定（见 §4.3）。

### 0.2 设计立场（三条硬约束）

1. **不重复实现已有机制**。`FailoverState` 状态机、排除集、同账号重试、粘性、槽位、调度快照、T3 写库全部保留，本设计只在其上**加判定源、加预算、加冷却、加观测**。
2. **默认零行为变更**。所有新增配置的默认值必须让系统行为与当前 `main` 逐位一致；唯一例外是"构造顺序"（分类函数替代硬编码 switch，返回值必须逐项相等，用参数化测试锁定）。
3. **失败只能"少打上游"，不能"多打上游"**。任何设计若可能增加单请求的上游调用次数，直接否决。

### 0.3 复用清单 vs 新建清单

| 能力 | 现状 | 本次处置 |
| --- | --- | --- |
| `FailoverState` 状态机（切换计数/排除集/同账号重试/503 退避/利润否决） | `failover_loop.go:124-319` | **复用**，仅扩展字段 |
| 选号（快照→过滤链→分层排序→LRU） | `gateway_scheduling.go:100`、`:652-694`、`:738-776` | **复用**，过滤链插入 1 个 T2 冷却判定 |
| 同账号重试（`pool_mode_retry_count` / `pool_mode_retry_status_codes`） | `failover_loop.go:58-122`、`account.go:1242-1360` | **复用**，不新增平行机制 |
| T3 持久标记（`temp_unschedulable_until` / `rate_limit_reset_at` / `model_rate_limits`） | `ratelimit_service.go:333-398`、`SetTempUnschedulable` | **复用所有权不变**，仅加"只延长不缩短"变体 |
| 粘性会话（生成/查询/清除） | `gateway_scheduling.go:131-141`、`gateway_service.go:560-571` | **复用**，新增失败时按 `Scope` 决定是否清除 |
| 告警框架（`metric_type` 规则 + 评估器） | `ops_alert_evaluator_service.go:447-615` | **复用**，只加 2 个 metric_type |
| 切换计数指标 | `ops_metrics_collector.go:568-587` | **复用**，加分组维度与失败类别 |
| 失败判定（4 处硬编码状态码集合） | `gateway_forward.go:47-54` 等 | **新建**统一分类器，硬编码点改为调用它 |
| 分级冷却（T2） | 无 | **新建**（默认关闭） |
| 请求级预算 | 无 | **新建**（默认关闭） |
| 切换链审计 | 无 | **新建**（默认开启，纯日志） |

---

## 一、设计目标与非目标

### 1.1 目标

| # | 目标 | 验收口径（可测） |
| --- | --- | --- |
| **O1** | 失败判定规则可配置，且**单一来源** | 4 处硬编码状态码集合收敛到 1 个函数；参数化单测逐项对齐旧行为；分组级覆盖可生效并可查 `source` |
| **O2** | 切换顺序**可解释**（不要求确定） | 每次请求产出完整 `switch_chain`（第 N 次切到谁、为什么、来自哪个调度层），可从日志还原 |
| **O3** | 重试上限**不再相乘** | 单请求上游调用次数上界可**先验计算**并断言：`≤ min(MaxSwitches+1, MaxTotalAttempts) + MaxSameAccountAttemptsTotal` |
| **O4** | 失败账号**跨请求**短期隔离 | 连续失败达阈值 → T2 冷却期内选号跳过；冷却到期半开探测，不惊群 |
| **O5** | 可切换点判据**语义化** | 判据从"字节数不等"改为"是否写出过语义字节"；keepalive/ping 不计入 |
| **O6** | 时间可控 | 三层超时叠加；预算耗尽**必须**返回错误而非继续切换 |
| **O7** | 全失败错误**可判读** | 聚合 attempts，`Retry-After` 有值，Anthropic 路径与 OpenAI 路径语义一致 |
| **O8** | 可观测 | 切换链日志 + 4 个指标 + 2 个新告警 metric_type |

### 1.2 非目标（明确不做，避免范围失控）

| # | 非目标 | 理由 |
| --- | --- | --- |
| N1 | 不统一 OpenAI 网关 / 生图 / AlphaSearch / Grok 媒体的 4 套内联 `switchCount` 实现 | 本次范围锁定 `FailoverState` 那套（Anthropic / Gemini / Antigravity / chat / responses / v1beta / SystemOne）。4 套内联实现见 §8-Q1，作为后续独立变更 |
| N2 | 不新增"账号权重"（weight）字段 | 现有 `EffectiveLoadFactor()`（`MaxConcurrency`）已提供权重效果；显式 weight 要改调度排序核心，收益低 |
| N3 | 不新增"地域/机房"字段，不做跨地域分层字段 | 用 `priority` 分段表达"优先同地域"（§3.1.4），零 schema 改动 |
| N4 | 不引入 `Idempotency-Key` 响应缓存/去重 | 网关路由无幂等中间件（`routes/gateway.go:180-189`），补响应重放会破坏流式且存储成本高；只做审计 + 可选禁换号 |
| N5 | 不做管理端 UI | 配置走 `config.yaml` + `groups` 列；UI 属 t4 之后 |
| N6 | 不改 `maxAccountSwitches` 的语义 | 改语义 = 改现网配置含义（默认 10），破坏面不可控。改为**新增** `max_total_upstream_attempts` 作为硬顶 |
| N7 | 不改池模式"不标记本地账号状态"的契约 | `ratelimit_service.go:340-349` 的契约保留；T2 是 failover 内部短期避让，不是账号状态（§3.4.4 论证） |

---

## 二、总体架构图

### 2.1 分层图

```
 Handler 层（9 个入口，共用 FailoverState 的 7 个）
   Messages / ChatCompletions / Responses / v1beta / SystemOne / Antigravity
        │
        │ ① fs := NewFailoverState(maxSwitches, hasBoundSession)   ← 扩展：注入 Policy + Budget
        │ ② for { 选号 → 抢槽 → Forward → 失败分类 → 决策 }
        ▼
 ┌─────────────────────────────────────────────────────────────────────┐
 │ L0  策略层  FailoverPolicy（service/failover_policy.go，新建）        │
 │     解析链：account.credentials > groups.* 列 > gateway.failover_policy│
 │     纯函数 + 每请求解析一次 + 请求级缓存；返回 (policy, source)        │
 └───────────────┬─────────────────────────────────────────────────────┘
                 │ policy
 ┌───────────────▼─────────────────────────────────────────────────────┐
 │ L1  分类层  FailoverClassifier（service/failover_classifier.go，新建）│
 │     Classify(statusCode, transportKind, body, headers, platform)     │
 │       → FailureClass + RetryAdvice{SameAccount, Cooldown, T3, Sticky}│
 │     替换 4 处硬编码：gateway_forward.go:47-54 /                       │
 │     antigravity_gateway_retry.go:780-787 /                           │
 │     gemini_messages_compat_service.go:1706-1713 /                    │
 │     openai_gateway_upstream_errors.go:303                            │
 └───────────────┬─────────────────────────────────────────────────────┘
                 │ class + advice
 ┌───────────────▼─────────────────────────────────────────────────────┐
 │ L2  状态层（三档，生命周期与一致性各不相同）                            │
 │  T1 请求内排除集   FailoverState.FailedAccountIDs（进程内存，已有）    │
 │  T2 冷却窗口       Redis  failover:cooldown:*（新建，默认关闭）        │
 │  T3 持久标记       DB     accounts.temp_unschedulable_until 等（已有） │
 └───────────────┬─────────────────────────────────────────────────────┘
                 │
 ┌───────────────▼─────────────────────────────────────────────────────┐
 │ L3  预算层  FailoverBudget（挂在 FailoverState 上，新建）             │
 │     Deadline(整请求) / MaxTotalAttempts / MaxSameAccountTotal        │
 │     / SemanticBytesWritten(可切换判据) / SwitchChain[]               │
 └───────────────┬─────────────────────────────────────────────────────┘
                 │
 ┌───────────────▼─────────────────────────────────────────────────────┐
 │ L4  观测层（已有框架，扩展）                                          │
 │     结构化日志 failover.attempt / failover.outcome                    │
 │     ops_error_logs.upstream_errors kind 扩展                          │
 │     指标 failover_switch_ratio / failover_exhausted_ratio            │
 │     终局错误 FailoverOutcomeError（聚合 + Retry-After）              │
 └─────────────────────────────────────────────────────────────────────┘
```

### 2.2 一次失败的处理流水线（替换 `failover_loop.go:193-267` 的内部逻辑，签名不变）

```
Forward 返回 UpstreamFailoverError
  │
  ├─(1) ctx.Err() != nil ──────────────────────────► FailoverCanceled（现有）
  ├─(2) Classify(err) → class, advice
  │       若 class == policy_blocked / binding_passthrough
  │         ────────────────────────────────────► FailoverExhausted（现有语义）
  ├─(3) 可切换判据：SemanticBytesWritten > 0 且 !SafeToFailoverAfterWrite
  │         ────────────────────────────────────► FailoverExhausted（写 SSE error 帧）
  ├─(4) 记账（顺序固定，见 §3.4.5）
  │       a. T1: FailedAccountIDs += accountID
  │       b. T2: 冷却写入（按 advice.Cooldown，仅 Scope==account 时）
  │       c. T3: TempUnscheduleRetryableError（现有，仅 advice.T3）
  │       d. 粘性：advice.Sticky == clear 且 Scope==account → 删绑定
  ├─(5) 同账号重试判定（现有 sameAccountRetryAllowed，但受 MaxSameAccountTotal 约束）
  │       许可 ──► sleep(retryDelay) → FailoverContinue（不消耗 SwitchCount）
  ├─(6) 预算判定：SwitchCount >= MaxSwitches 或 TotalAttempts >= MaxTotalAttempts
  │               或 now + MinSwitchCost >= Deadline
  │         ────────────────────────────────────► FailoverExhausted(budget)
  └─(7) SwitchCount++ → 记录 SwitchHop → FailoverContinue
```

### 2.3 分层职责边界（防止"调度器变成状态机"）

| 层 | 只拥有 | 绝不拥有 |
| --- | --- | --- |
| L0 策略 | 配置值 + 来源标签 | 任何运行时状态 |
| L1 分类 | 纯函数映射 | 任何 I/O |
| L2-T1 | 请求内排除集 | 跨请求可见性 |
| L2-T2 | 短期冷却事实（有 TTL） | 账号业务状态（`status` / 配额） |
| L2-T3 | 账号业务状态 | 请求内决策 |
| L3 预算 | 本次请求的计数与时钟 | 跨请求配额 |
| L4 观测 | 只读事实 | 决策 |

---

## 三、各维度设计

### 3.1 切换顺序

#### 3.1.1 现状

- **没有"下一个账号"**。每次换号都重跑完整选号：候选快照（Redis 桶 / DB fallback）→ 10 步过滤链（`gateway_scheduling.go:652-694`）→ 分层排序（最小优先级 `filterByMinPriority` → 可选最早重置 → 最低负载 → LRU）→ **同组多候选取随机**（`:1734`），路由路径再 `shuffleWithinSortGroups`（`:1763-1780`）。
- 失败账号靠**请求内排除集** `FailedAccountIDs` 规避（`failover_loop.go:128`、`:242`），不是游标。排除集是每请求私有的 `map[int64]struct{}`，随请求丢弃。
- **三种"回到同一账号"的通道**：S3 503 退避清空排除集（`failover_loop.go:310-315`）；S4 降级分组新建 `FailoverState`（`gateway_handler.go:666`）；跨请求（失败不写库 + 粘性不清）。
- **粘性冲突现状**：失败路径只 `ReleaseAccountSession`（`gateway_handler.go:1068-1070`），**不删粘性绑定**。`shouldClearStickySession`（`gateway_service.go:560-571`）存在但只在选号内部使用，失败后不调用。

#### 3.1.2 设计：保留"重算"，补齐"可解释"

**决策：不引入游标/轮转。** 理由：

1. 游标与并发/负载感知天然冲突——游标指向的账号可能已满载或已限流，必须回退重算，等于没游标。
2. 引入游标要新增请求内 + 跨请求状态，与"请求内无锁、跨请求不共享"的现有并发模型相悖。
3. 用户的真实诉求（recon §六-1）是"能解释为什么切到 B 而不是 C"，不是"必须按固定顺序"。**可解释性靠记录，不靠确定性。**

具体三件事：

**(a) 切换链记录（解决 O2，默认开启）**

在 `FailoverState` 上新增：

```go
type SwitchHop struct {
    Seq            int           // 第几次尝试（1 起）
    AccountID      int64
    SelectionLayer string        // "router_sticky" | "sticky" | "load_balance" | "wait_queue"
    Priority       int
    LoadRate       int
    StickyHit      bool
    FailureClass   string        // 本跳的失败类别（首跳为空）
    UpstreamStatus int
    UpstreamKind   string        // ops_error_logs 的 kind
    ElapsedMs      int64         // 本跳从选中到失败/成功的耗时
    Outcome        string        // "failover" | "success" | "exhausted"
}
SwitchChain []SwitchHop
```

写入点：选号成功后立即 Append（`Outcome` 留空）；`HandleFailoverError` / 成功分支回填。

对外产出：终局一条 `gateway.failover_outcome` 日志携带完整链（§3.8.1）。**这使"第 N 次切到谁"从"不可复现"变成"可回溯"**，且不改变选号行为。

**(b) 排除集语义收紧（解决活锁风险，对齐 G11）**

现状 `HandleSelectionExhausted` 的 503 分支会清空排除集（`failover_loop.go:310-315`），且该分支**不推进 `SwitchCount`**（注释 `:284-285` 只判断 `<= MaxSwitches`）。这是活锁的结构性来源，现有补丁只挡住了"利润否决全占"（`:290-296`）。

设计新增护栏：`FailoverState.SelectionRetryCount`，`HandleSelectionExhausted` 每次清空 +1，**同一请求内最多清空 1 次**（配置 `gateway.failover_policy.max_selection_retries`，默认 `1`，`0` = 沿用旧行为）。超过则 `FailoverExhausted`。理由：单账号分组的 503 场景确实需要一次"同账号再试"，但第二次清空等于"重跑一遍注定失败的循环"。

**(c) 粘性冲突处理（解决 G3 的一半）**

失败后按 `FailureClass` 决定是否清粘性：

| 情形 | 动作 | 理由 |
| --- | --- | --- |
| `Scope == account`（账号自身问题：401/403/429/5xx/网络） | **删除**该 `sessionKey` 的粘性绑定 | 否则下一请求仍命中同一坏账号（recon §2.4 结论） |
| `Scope == provider`（上游整体降载/容量） | **保留** | 换账号无意义，删绑定只会让所有请求丢失缓存亲和性，放大首 token 延迟 |
| `Scope == request`（请求本身的问题：400/404/422） | **保留** | 与账号无关 |
| `BoundUpstreamPassthrough`（绑定缓存账号的 429/5xx） | **保留** | 该账号持有显式缓存，换号会丢缓存，代价大于收益 |

复用现有 `shouldClearStickySession` 的调用形态，但**判定条件从"账号不可调度"扩展为"本次失败归因于账号"**。注意：删除绑定要与现有 `GatewayCache` 的 session 接口一致，不新增 Redis 数据结构。

#### 3.1.3 权重与负载

- **负载感知已存在**：`GetAccountsLoadBatch` + `filterByMinLoadRate`（`gateway_scheduling.go:716-736`、`:1626-1643`），并已剔除 `LoadRate >= 100`。
- **"权重"用 `MaxConcurrency` 表达**：`EffectiveLoadFactor()` 越大 → 负载率越低 → 越容易被选中，效果等价于加权。不新增字段。
- **边界**：负载率是**瞬时**指标，切换时可能选出"负载低但正在失败"的账号——这正是 T2 冷却层要补的洞（§3.4）。

#### 3.1.4 组内分层（如"优先同地域"）

**设计：用现有 `priority` 分段，不新增字段。**

`filterByMinPriority`（`gateway_scheduling.go:1606-1623`）是**硬分层**：只保留最小优先级的集合，高优先级账号在当前分层耗尽前不会被选。

推荐用法（写进运维文档，不改代码）：

| 场景 | 配置 |
| --- | --- |
| 同地域优先 | 同地域账号 `priority = 10`，异地 `priority = 20`；同地域全挂才会切到异地 |
| 主备 | 主池 `priority = 10`，备池 `priority = 50` |
| 容量分档 | 高配额账号 `priority = 10`，低配额 `priority = 30` |

注意一个**已知交互**：换号时排除集里已有同地域的全部账号，`filterByMinPriority` 会自然降级到异地层。行为正确，但**必须在 switch_chain 里记录 `Priority`**，否则运维看不出"为什么突然用了异地账号"——这就是 §3.1.2(a) 的 `SwitchHop.Priority` 字段。

#### 3.1.5 配置项

| 键 | 默认 | 说明 |
| --- | --- | --- |
| `gateway.failover_policy.switch_order.record_chain` | `true` | 是否产出 switch_chain（关闭可省日志量） |
| `gateway.failover_policy.switch_order.clear_sticky_on_account_failure` | `true` | 账号级失败是否清粘性 |
| `gateway.failover_policy.max_selection_retries` | `1` | 排除集清空次数上限；`0` = 旧行为 |

#### 3.1.6 边界

- **不保证**"不回到刚失败的账号"在跨请求维度成立：T2 关闭时，坏账号仍可能被下一次请求命中。T1 层面成立（排除集硬排除），唯一例外是 3.1.2(b) 收紧后的单次清空。
- **不处理**同账号重试的"重复命中"：同账号重试按设计不进排除集（`failover_loop_test.go:470` 已锁定），不改变。
- 粘性删除是**尽力而为**：Redis 失败时 fail-open 记 warn，不阻断请求。

---

### 3.2 重试上限

#### 3.2.1 现状（爆炸的根源）

```
实际上游调用次数 = Σ(每个账号的尝试次数)
                 ≈ (MaxSwitches + 1) × (1 + SameAccountRetryLimit)
```

| 路径 | 参数 | 最坏上游调用 |
| --- | --- | --- |
| Anthropic | `max_account_switches=10` → 11 账号；`pool_mode_retry_count=3` | **11 × 4 = 44** |
| Gemini | `max_account_switches_gemini=3` → 4 账号；服务内 `MaxSameAccountRetriesGemini=4` → 5 次 | **4 × 5 = 20** |
| OpenAI 网关 | 兜底 `maxAccountSwitches=3` → 4 账号（`openai_gateway_handler.go:368`） | 4 × 4 = 16 |

证据：`failover_loop.go:245-250`（语义=切换次数，尝试上限 +1）、`failover_loop.go:33-52`、`gemini_messages_compat_service.go:1678-1684`、`openai_gateway_handler.go:368`、`grok_media.go:220-222`。

**三个独立缺陷**：

1. **乘法叠加**：切换次数 × 同账号重试，两者无共同上限。
2. **语义误导**：`maxAccountSwitches=10` 被读成"10 个账号"，实际是 11 个账号 × 4 次。
3. **零值兜底不一致**：`GatewayHandler` 兜底 10（`gateway_handler.go:84-93`），`OpenAIGatewayHandler` 兜底 3，`grok_media` 兜底 3（recon §0 修正表第 3 条）。

#### 3.2.2 设计：三层上限取最小值

```
TotalAttempts（上游调用总次数）从 1 开始，每次真正打到上游才 +1
  ↑ 上界 = min( MaxSwitches + 1 , MaxTotalAttempts )      ← 账号维度
        + MaxSameAccountTotal                            ← 同账号重试维度（全局池，不是每账号 3 次）
```

关键变化：**同账号重试预算从"每账号各自 N 次"改为"整请求合计 N 次"**。这是消灭乘法的那一刀。

| 参数 | 语义 | 默认值 | 上线建议值 |
| --- | --- | --- | --- |
| `gateway.max_account_switches` | 切换次数（**语义不变**） | `10` | 保持 |
| `gateway.max_account_switches_gemini` | Gemini 切换次数 | `3` | 保持 |
| **`gateway.failover_policy.budget.max_total_upstream_attempts`** | 整请求上游调用总次数硬顶 | `0`（关闭 = 旧行为） | `6` |
| **`gateway.failover_policy.budget.max_same_account_attempts_total`** | 整请求同账号重试合计上限 | `0`（关闭 = 旧行为） | `4` |
| `gateway.failover_policy.budget.min_switch_cost` | 切前剩余预算下限 | `2s` | `2s` |

**为什么默认 0（关闭）**：默认 6 会让现网"11 账号"变"6 次上游"，是明确的行为变更，且方向上是**变少**（更保守）——仍然属于"改变现有行为"，必须在迁移文档里显式决策（§7.3），不能偷偷改默认值。

**为什么 `min_switch_cost=2s` 默认生效**：它只在"预算已开启"时才起作用（`Deadline` 为零时短路），因此不影响默认行为。语义：剩余预算不足 2s 时切换无意义（一次上游往返通常 &gt; 2s），直接返回错误比"切过去再超时"对用户更友好（更快拿到可重试的错误）。

#### 3.2.3 Gemini 单独上限的合理性评估

**结论：`max_account_switches_gemini=3` 合理，保留；但 `max_same_account_retries_gemini=4` 与本设计冲突，建议降为 2。**

- **保留切换 3 次**：Gemini 平台上游对同一 OAuth 身份有更严格的速率限制（配置注释原话「因 API 限制更严格」，`config.go:1194-1195`）。多切账号换来的成功率边际递减，且容易触发上游风控。3 次（4 账号）合理。
- **降服务内重试 4→2**：`MaxSameAccountRetriesGemini=4` 是**"跨账号 failover 不存在时代"的补偿机制**——当时同账号重试是唯一的恢复手段。现在外层已有 4 账号 × 同账号重试的组合能力，服务内再重试 5 次导致 `4×5=20`，其中大部分是"同一账号在 1 秒内被锤 5 次"，对 429/5xx 毫无帮助，纯放大上游压力。
- **兼容性**：`max_same_account_retries_gemini` 是**现有公开配置键**，改默认值是行为变更。因此设计为：**代码默认仍读 4，但 `budget` 层生效时统一受 `max_same_account_attempts_total` 约束**。运维把 `max_same_account_attempts_total=4` 打开后，Gemini 的最坏调用数自动从 20 降到 4+4=8，无需改 Gemini 专属配置。这是"用新层收敛旧参数"而非"改旧参数"，回滚成本最低。

#### 3.2.4 配置项

见 §3.2.2 表 + §5 完整清单。

#### 3.2.5 边界

- **不承诺**"至少尝试 N 个账号"：候选池可能只有 1 个可用账号，此时预算用不完。
- **`BoundUpstreamPassthrough` 不计入切换**：该路径原样回写上游错误（`gateway_service.go:708` 附近），不产生新上游调用，`TotalAttempts` 不 +1。
- **`RecordProfitVeto` 不消耗上游调用**（`failover_loop.go:161-172`，有独立上限 `maxProfitVetoAttempts=10`），但也**不能无限**：预算层把"选号次数"单独计数，`max_selection_attempts`（默认 `MaxSwitches+1+maxProfitVetoAttempts`，即不为利润否决额外加限，保持现状）。

---

### 3.3 可配置失败判定规则（核心缺口 G1）

#### 3.3.1 现状：4 处硬编码 + 1 个全局布尔

| # | 位置 | 集合 |
| --- | --- | --- |
| 1 | `gateway_forward.go:47-54` `shouldFailoverUpstreamError` | `{401,403,429,529} ∪ [500,∞)` |
| 2 | `antigravity_gateway_retry.go:780-787` | 同上 |
| 3 | `gemini_messages_compat_service.go:1706-1713`（+ 服务内重试集合 `:1686-1704`） | 同上 + `{429,500,502,503,504,529}` |
| 4 | `openai_gateway_upstream_errors.go:303`、`:395-412` | 自有分类（capacity shed / credential / 400） |

可配的只有：`gateway.failover_on_400`（默认 `false`，且只覆盖关键词白名单型 400：`anthropic-beta`、`beta feature`、`requires beta`、`thinking`、`thought_signature`、`signature`、`tool_use`、`tool_result`、`tools`，见 `gateway_upstream_response.go:212-237`）；账号凭据 `pool_mode*` / `custom_error_codes` / `temp_unschedulable_rules`。

**无分组级、无 DB 列、无迁移**（`backend/migrations` 无 failover 命中）。

#### 3.3.2 设计：规则模型（Rule Model）

**核心概念：`FailureClass`（失败类别）取代"状态码集合"。**

状态码只是分类的**输入之一**；同一状态码在不同上下文是不同类别（例：429 可能是"瞬时限流"也可能是"配额耗尽"，差别在于响应头有无 `reset`；503 可能是"账号容量"也可能是"上游整体降载"，差别在 `RequestScopedTransient`）。

```go
// service/failover_classifier.go（新建）
type FailureClass string

const (
    ClassAuthInvalid     FailureClass = "auth_invalid"      // 401 / 403（凭据失效）
    ClassRateLimited     FailureClass = "rate_limited"      // 429（瞬时，有 reset）
    ClassQuotaExhausted  FailureClass = "quota_exhausted"   // 429+reset / 529（窗口耗尽）
    ClassCapacityShed    FailureClass = "capacity_shed"     // 503（与账号无关的降载）
    ClassUpstream5xx     FailureClass = "upstream_5xx"      // 500/502/504 + [500,599]
    ClassUpstreamTimeout FailureClass = "upstream_timeout"  // 首字节超时 / 响应头超时
    ClassNetwork         FailureClass = "network"           // 拨号/TLS/连接重置/EOF
    ClassRequestInvalid  FailureClass = "request_invalid"   // 400/404/409/413/422（请求本身）
    ClassPolicyBlocked   FailureClass = "policy_blocked"    // 本地策略拒绝，禁止换号
    ClassBindingPassthru FailureClass = "binding_passthru"  // 绑定缓存账号，原样回写
)

// RetryAdvice 是该类别的"处置建议"，由策略层可配置覆盖
type RetryAdvice struct {
    Failover        bool          // 是否允许换号
    SameAccount     RetryPolicy   // inherit | off | n
    Cooldown        time.Duration // T2 冷却时长；0 = 不冷却
    CooldownSource  string        // "upstream_reset" | "fixed" | "none"
    TempUnschedule  bool          // 是否走 T3 写库
    ClearSticky     bool          // 是否清粘性
    Scope           GatewayFailureScope // account | provider | request
}
```

分类函数签名（纯函数，无 I/O，可单测穷举）：

```go
func ClassifyUpstreamFailure(in ClassifyInput) (FailureClass, RetryAdvice)
// ClassifyInput{ StatusCode, TransportKind, ResponseBody, ResponseHeaders,
//                Platform, IsCredentialFailure, RequestScopedTransient,
//                BoundUpstreamPassthrough, Policy *FailoverPolicy }
```

**默认规则表（必须与现状逐项等价）**：

| class | 默认状态码 | Failover | 同账号重试 | T2 冷却 | T3 写库 | 清粘性 | Scope |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `auth_invalid` | 401, 403 | 是 | **inherit**（`pool_mode_retry_status_codes` 默认含 401/403） | 60s | 现有 OAuth 流程不变 | 是 | account |
| `rate_limited` | 429（有 reset） | 是 | inherit | 上游 reset（cap 24h），无则 60s | 现有 | 是 | account |
| `quota_exhausted` | 429（耗尽/7d 100%）、529 | 是 | **off**（重试无意义） | 上游 reset（cap 24h）或 1800s | 现有 `rate_limit_reset_at` | 是 | account |
| `capacity_shed` | 503 + `RequestScopedTransient=true` | 是 | 走现有 2s 退避重选 | **0（不冷却）** | **否**（`gateway_service.go:761-763`） | 是（保留语义） | provider |
| `upstream_5xx` | 500, 502, 504, [500,599] | 是 | inherit | 连续 3 次 → 60s | 现有（502 空响应） | 是 | account |
| `upstream_timeout` | 首字节/响应头超时 | 是 | inherit | 连续 3 次 → 60s | 现有 transport 路径 | 是 | account |
| `network` | 拨号/TLS/连接重置/EOF | 是 | inherit | 连续 3 次 → 60s | 现有 transport 路径 | 是 | account |
| `request_invalid` | 400（非白名单）/ 404 / 409 / 413 / 422 | **否** | off | 0 | 否 | 否 | request |
| `request_invalid`（400 白名单） | 400 + 关键词命中且 `failover_on_400=true` | **是** | off | 0 | 否 | 否 | request |
| `policy_blocked` | `BetaBlockedError` | 否 | off | 0 | 否 | 否 | request |
| `binding_passthru` | `BoundUpstreamPassthrough` | 否 | off | 0 | 否 | 否 | account |

**"inherit" 的含义（关键兼容性设计）**：`SameAccount = inherit` 表示**沿用账号凭据的 `pool_mode_retry_count` / `pool_mode_retry_status_codes` 与错误对象上的 `RetryableOnSameAccount` 标志**，行为与今天完全一致。策略层只在运维显式写 `off` 或具体数字时才改变现状。这避免了"新规则表把 `pool_mode` 的既有语义吃掉"。

#### 3.3.3 是否含网络层错误

**含。** 理由：现状已经包含（`gateway_upstream_transport_error.go:106`、`openai_upstream_transport_error.go:205` 会对传输错误做 `SetTempUnschedulable`，说明"网络层错误归属账号健康"是既有语义）。设计只是把它**显式建模**成 `network` / `upstream_timeout` 两个类别，并允许配置开关：

```yaml
gateway:
  failover_policy:
    network_errors:
      enabled: true            # 默认 true（= 现状）
      classify_timeout_as: upstream_timeout
      classify_conn_reset_as: network
```

**边界**：`ctx.Err() == context.DeadlineExceeded` 且是**客户端**发起的取消 → 必须归 `Canceled`，不归 `network`，**且不写 T2/T3**（`gateway_upstream_transport_error_test.go:203` 已锁定"上游 deadline 不标记"、`:182` 锁定"客户端取消不标记"）。这条测试是本次设计的**硬约束**，分类器必须复用同一判定顺序。

#### 3.3.4 是否含 400

**默认不含，与现状一致（`failover_on_400=false`）。** 但要拆成两个独立开关，因为现状把它们混在一起：

| 场景 | 现状行为 | 设计 |
| --- | --- | --- |
| 400 + 兼容性关键词白名单 | `failover_on_400=true` 时换号 | `request_invalid.compat_400.keywords`（默认 = 现有 9 个关键词） |
| 400 其他 | 不换号 | `request_invalid.failover: false`（默认） |
| 400 + `RetryableOnSameAccount`（Google 间歇性 400） | **同账号重试**（`gateway_service.go:766-767` 的 `tempUnscheduleGoogleConfigError` 路径） | `upstream_5xx` 之外单独保留：这类 400 归 `ClassUpstream5xx` + `SameAccount=inherit`，因为它是**上游抖动**不是请求错误 |

最后一行是设计中的**易错点**：Google 间歇性 400 在代码里是 `RetryableOnSameAccount=true` 的 400（`gateway_service.go:765-770`），归到 `request_invalid` 会导致"不再重试"，属于回归。分类器必须**先查 `RetryableOnSameAccount` 标志，再查状态码**。顺序：`BoundUpstreamPassthrough` → `RetryableOnSameAccount` → `RequestScopedTransient` → 状态码类 → 默认。

#### 3.3.5 配置键命名与三级覆盖

**命名规范**：沿用 `gateway.` 前缀；嵌套块用 `failover_policy`（先例：`gateway.scheduling` → `GatewaySchedulingConfig`，`config.go:1202-1203`）；**已有的 4 个扁平键保持原名不变**（`max_account_switches` / `max_account_switches_gemini` / `max_same_account_retries_gemini` / `failover_on_400`）。

**就近覆盖范式**：完全对齐 `resolveEffectiveMinMargin(upstream, local, global) (int64, string)`（`ops_mgmt_settings.go:297-306`）——返回 `(值, 来源)`，来源用于日志与排查（`"upstream_group"` / `"local_group"` / `"global"`）。

```go
// service/failover_policy.go（新建）
// resolveEffectiveFailoverPolicy 按「账号 > 分组 > 全局」取出生效策略。
func resolveEffectiveFailoverPolicy(
    account *Account,      // account.credentials["failover_policy"]（可空）
    group   *Group,        // group.FailoverPolicy（JSONB 列，可空）
    global  FailoverPolicy,// config
) (FailoverPolicy, string) // 返回 source: "account" | "group" | "global"
```

**合并语义：字段级 merge，不是对象级覆盖。** 分组只写 `cooldown.enabled=true`，其余字段回落全局。理由：与 `resolveEffectiveMinMargin` 的"单个标量就近覆盖"精神一致，但这里是结构体，必须定义清楚"未设置"的表示——用**指针字段**（`*bool` / `*int` / `*time.Duration`），`nil` = 未设置 = 回落。**禁止**用零值表示"未设置"，否则 `false` 与"未配置"无法区分（本项目 `groups.opsmgmt_min_margin_basis_points` 用 `BIGINT NULL` 正是这个道理）。

**为什么分组级用 `groups` 新列而不是 `opsmgmt_settings`**：`groups` 是租户/业务概念（谁用这个池），failover 策略是池的容灾属性，归 `groups` 语义正确；`opsmgmt_*` 是运营管理视角，且 `opsmgmt_settings` 是全局 key-value，没有分组维度。

#### 3.3.6 配置项（本维度）

| 键 | 默认 | 范围 | 分组覆盖 |
| --- | --- | --- | --- |
| `gateway.failover_policy.enabled` | `true` | bool | 是 |
| `gateway.failover_policy.status_classes` | 见 §3.3.2 表 | 数组（class + codes/range + advice） | 是（整体替换数组） |
| `gateway.failover_policy.network_errors.enabled` | `true` | bool | 是 |
| `gateway.failover_policy.failover_on_400` | `false` | bool | 是（`groups.failover_on_400`） |
| `gateway.failover_policy.compat_400_keywords` | 9 个关键词 | string[] | 是 |
| `gateway.failover_policy.same_account.default_mode` | `inherit` | `inherit`/`off` | 是 |

#### 3.3.7 边界

- **不做**"按错误体正则匹配决定换号"：现有 `temp_unschedulable_rules` 已有 `keywords`（`account.go:141-149`），再做一套会双源。设计明确：`temp_unschedulable_rules` 负责 **T3 写库**，`failover_policy` 负责 **T1/T2 与是否换号**，两者职责不重叠（对齐 §3.4.5 的记账顺序）。
- **不做** per-model 的 failover 规则：模型维度已有 `model_rate_limits` 与 `temp_unschedulable_rules.scope=model`，够用。
- 分类器必须是**纯函数**：所有输入从参数来，不读 Redis/DB/ctx。否则无法穷举单测。

---

### 3.4 账号状态标记与恢复

#### 3.4.1 现状

| 层 | 机制 | 证据 | 问题 |
| --- | --- | --- | --- |
| 请求内 | `FailedAccountIDs` | `failover_loop.go:128` | 请求结束即丢 |
| 中期 | **不存在** | — | **G3 的根因** |
| 长期 | `accounts.temp_unschedulable_until` / `rate_limit_reset_at` / `model_rate_limits` | `migrations/020_add_temp_unschedulable.sql:5`、`005_schema_parity.sql:15`、`ratelimit_service.go:2265-2287` | 池模式默认跳过（`ratelimit_service.go:340-349`）；写库无"只延长"保护（G10） |

失败写库的覆盖情况（recon §0-5）：只有 429（带 reset）/ OAuth 403 / 400 配置类 / 502 空响应等少数路径写库。

#### 3.4.2 设计：三级状态（T1/T2/T3）

| 层 | 存储 | 生命周期 | 谁能读 | 谁能写 | 语义 |
| --- | --- | --- | --- | --- | --- |
| **T1 请求内排除** | `FailoverState.FailedAccountIDs`（进程内存） | 请求结束 | 本请求的选号 | `HandleFailoverError` / `RecordProfitVeto` | "这个请求已经试过它" |
| **T2 冷却窗口** | Redis `failover:cooldown:*` | 5s–24h（按类别） | 所有实例的选号 | 所有实例的失败路径 | "它最近刚刚失败，暂时别选它" |
| **T3 持久标记** | DB `accounts.*` | 分钟–小时（按类别） | 所有实例 + 管理端 | `RateLimitService`（唯一） | "这个账号的调度状态变了" |

**T2 与 T3 的分工（必须讲清楚，否则会被质疑"重复"）**：

- T3 是**业务状态变更**：影响管理端展示、影响 `/ops` 可用性统计、需要人工确认、有审批语义。池模式明确不要（运营者要求"上游错误不改变本地账号状态"）。
- T2 是**failover 内部短期记忆**：只有选号器看，TTL 短、自动恢复、无人工介入、不进管理端。它解决的是"同一批并发请求同时命中一个刚坏的账号"这个**纯性能问题**。
- 结论：**T2 不违反池模式契约**。池模式说"不标记本地账号状态"，它标记的是 DB；T2 不写 DB。

#### 3.4.3 差异化策略（按失败类型）

| FailureClass | T1 | T2 冷却时长 | T3 写库 | 恢复条件 |
| --- | --- | --- | --- | --- |
| `auth_invalid`（401/403） | 是 | **60s**（短） | 现有 OAuth refresh / 401 流程不变 | 冷却到期 + 一次成功 |
| `rate_limited`（429 瞬时） | 是 | 上游 `Retry-After`/reset，兜底 **60s** | 现有（池模式跳过） | 上游 reset 时间 |
| `quota_exhausted`（429 耗尽 / 529） | 是 | 上游 reset，**cap 24h**，兜底 1800s | 现有 `rate_limit_reset_at` / `handle529` | 上游窗口重置 |
| `capacity_shed`（503 降载） | 是 | **0（不冷却）** | **否** | — |
| `upstream_5xx` | 是 | 连续 3 次 → **60s** | 现有（502 空响应） | 冷却到期 / 成功清计数 |
| `upstream_timeout` | 是 | 连续 3 次 → **60s** | 现有 transport 路径 | 同上 |
| `network` | 是 | 连续 3 次 → **60s** | 现有 transport 路径 | 同上 |
| `request_invalid` | 视 `failover_on_400` | **0（不冷却）** | 否 | — |
| `policy_blocked` / `binding_passthru` | 否 | 0 | 否 | — |

**为什么 5xx/网络类要"连续 3 次"而不是"一次就冷却"**：
- 单次 5xx 极常见（上游抖动），一次就冷却会把健康账号摘掉，降低池容量 → 与"最大化可用账号"目标冲突。
- 连续 3 次（窗口 120s）是"明显的账号级问题"的低误报阈值。3 是保守起点，可配 `cooldown.consecutive_failures_threshold`。
- 反例检查：若上游整体故障（所有账号都 5xx），会**同时**把全池冷却 → 池瞬间为空 → 全站 503。这是 T2 的**最大风险**，必须有防护：**冷却写入前检查"本次请求已尝试账号数"**，若 `len(FailedAccountIDs) >= 2`（已确认不止一个账号坏）则**不写 T2**——故障是平台级，冷却无意义。这条规则叫"多账号同败不冷却"，必须写进实现（记为 F-01）。

#### 3.4.4 自动恢复

| 机制 | 触发 | 实现 |
| --- | --- | --- |
| TTL 自然过期 | 到时 | Redis `EX` |
| **成功即清除** | 该账号本次请求成功 | `DEL failover:streak:acct:{id}` + `DEL failover:cooldown:acct:{id}` |
| **半开探测**（防惊群） | 冷却到期后 | `SET failover:probe:acct:{id} 1 EX 10 NX` 成功者放行，其余视为仍冷却 |
| T3 到期 | DB 时间到 | 现有（`isAccountSchedulableForSelection`） |
| T3 人工清除 | 管理端 | 现有 |

**半开探测的必要性**：若不探测，冷却到期的瞬间所有并发请求同时放行 → 全打到刚恢复的账号 → 再次失败 → 再次冷却，形成**周期性雪崩**（thundering herd）。探测令牌把恢复期的流量限制为"每 10s 最多 1 个请求"，成功一次即清空冷却。

**边界**：探测失败不应延长冷却太久（否则坏账号永远不恢复），最多重试 3 次探测（`probe.max_attempts`，默认 3），之后彻底清空冷却让调度器自己判断。

#### 3.4.5 记账顺序（必须固定，否则并发/写库语义会乱）

```
a. T1（内存，先写：让同请求后续选号立刻跳过）
b. 粘性清理（Redis DEL，尽力而为）
c. T2（Redis，先检查"多账号同败不冷却"）
d. T3（DB，最后写：这是最慢且最需要一致性的）
```

理由：T1 最先是因为它零成本且能立刻止损；T3 最后是因为它最慢（DB 往返），不能让它拖慢"切到下一个账号"的关键路径。

#### 3.4.6 配置项

| 键 | 默认 | 说明 | 分组覆盖 |
| --- | --- | --- | --- |
| `gateway.failover_policy.cooldown.enabled` | **`false`** | T2 总开关。**默认关闭 = 零行为变更** | 是 |
| `gateway.failover_policy.cooldown.consecutive_failures_threshold` | `3` | 连续失败达此值才冷却（仅 5xx/网络类） | 是 |
| `gateway.failover_policy.cooldown.streak_window` | `120s` | 连续失败计数窗口 | 是 |
| `gateway.failover_policy.cooldown.default_ttl` | `60s` | 未指定类的兜底冷却时长 | 是 |
| `gateway.failover_policy.cooldown.max_ttl` | `24h` | 上游 reset 时间的上限截断 | 是 |
| `gateway.failover_policy.cooldown.pool_mode_enabled` | `true` | 池模式账号是否也受 T2 约束（T2 不改 T3 契约） | 是 |
| `gateway.failover_policy.cooldown.probe_enabled` | `true` | 半开探测 | 是 |
| `gateway.failover_policy.cooldown.probe_max_attempts` | `3` | 探测次数上限 | 是 |
| `gateway.failover_policy.t3.only_extend` | `true` | T3 写库"只延长不缩短"（修 G10） | 否（全局） |

#### 3.4.7 边界

- **T2 不改变 `IsSchedulable()` / 管理端展示**。运维在账号列表看到的仍是"正常"，只有日志/指标能看到冷却。若需要可见性，走 §3.8 的 `failover_cooldown_active_accounts` 指标，**不加管理端字段**。
- **T2 不覆盖"模型级"与"账号级"的混淆**：模型级冷却用 `failover:cooldown:acct:{id}:{modelHash}`，只在 `FailureClass ∈ {rate_limited, quota_exhausted}` 且上游明确指模型时写；其余写账号级。模型级 key 用哈希避免超长（现有 `compat_cc_` 前缀的哈希先例见 `openai_compat_prompt_cache_key.go:74`）。
- **T2 关闭时系统行为 = 今天**（逐位一致），这是本设计的回滚保证。

---

### 3.5 并发安全

#### 3.5.1 现状

- 请求内状态**全部私有、无锁**（`FailoverState` 每请求 new，`failover_loop.go:145-153`）。
- 跨请求共享只通过：Redis 槽位（`concurrency:account:*`，`concurrency_cache.go:28`）、Redis 负载批量缓存（`GetAccountsLoadBatch`，`:572-577` 短 TTL）、DB 账号状态。
- 快照可能过期，选号时二次校验（`gateway_scheduling.go:659-664` 注释）。
- **无跨请求熔断**；写库无"只延长"保护（G10，对比 OpenAI 的 `SetRateLimitedIfLater`：`openai_gateway_grok.go:1826`、`:1865-1867`）。

#### 3.5.2 设计：存储位置决策

| 选项 | 一致性 | 多实例正确性 | 成本 | 结论 |
| --- | --- | --- | --- | --- |
| 进程内（map + mutex） | 单实例内强 | **错**（实例 A 冷却，实例 B 照打） | 最低 | **否决**（多实例部署下完全无效） |
| DB（`accounts` 新列） | 强 | 对 | 每次失败一次 UPDATE；高频写放大 | **否决**（污染 T3 语义 + 写放大） |
| **Redis（TTL key）** | 最终一致（ms 级） | 对 | 1–2 次 RTT | **采用** |

**为什么不用 DB**：T2 是"短命、高频、可丢失"的状态。DB 写入会与 `SetTempUnschedulable` 竞争同一行，制造锁争用；且池模式契约明确"不写 DB"。

#### 3.5.3 Redis 原子操作

**问题 1：冷却写入必须"只延长不缩短"**

朴素 `SET key v EX ttl` 会用短 TTL 覆盖长 TTL（正是 G10 的 Redis 版本）。必须原子比较：

```lua
-- KEYS[1] = failover:cooldown:acct:{id}
-- ARGV[1] = 目标截止时间戳(ms)  ARGV[2] = class  ARGV[3] = 兜底 TTL(ms)
local cur = redis.call('PTTL', KEYS[1])
local curDeadline = 0
if cur > 0 then
  curDeadline = tonumber(redis.call('TIME')[1]) * 1000 + cur   -- 近似
end
if curDeadline >= tonumber(ARGV[1]) then
  return 0   -- 已有更长的冷却，保持不动
end
local ttl = math.max(tonumber(ARGV[1]) - tonumber(redis.call('TIME')[1]) * 1000, 1000)
redis.call('SET', KEYS[1], ARGV[2], 'PX', ttl)
return 1
```

> 实现注记：`TIME` 与 `PTTL` 的双重取时会有毫秒级抖动，等价实现可改用 `GET` 读 value 里存的 deadline（value = `class|deadlineMs`），只做一次 `TIME`。t3 实测时选定一种，不两种混用。

**问题 2：连续失败计数必须"计数 + 设 TTL"原子**

朴素 `INCR` 后 `EXPIRE` 在崩溃/超时下会留下**无 TTL 的永久 key**，导致账号永久被冷却（严重事故）。必须 Lua：

```lua
-- KEYS[1] = failover:streak:acct:{id}  ARGV[1] = windowMs
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return n
```

**问题 3：半开探测令牌**

`SET key 1 EX 10 NX` —— 单命令天然原子，无需 Lua。返回 `OK` 者为探测者。

**问题 4：冷却读取（选号热路径）**

选号是**每请求关键路径**，不能对每个候选做一次 `GET`（N 个候选 = N 次 RTT）。设计用**一次 MGET 批量读**：

```
MGET failover:cooldown:acct:{id1} ... {idN}
```

- 批量读在建候选集之后、过滤链之前（`gateway_scheduling.go:652` 前），只对 `len(candidates) > 1` 走；单账号分组跳过。
- RTT 预算：`500ms` 超时，超时则 **fail-open**（不排除任何账号，记 `failover_cooldown_degraded_total` 指标）。**绝不允许 Redis 故障导致全站 503**。
- 冷路径优化：`cooldown.enabled=false` 时该代码路径**完全不执行**（编译期可读的 if，无 RTT）。

#### 3.5.4 多实例一致性

| 场景 | 行为 |
| --- | --- |
| 实例 A 冷却，实例 B 选号 | B 的 MGET 能读到（同一 Redis） |
| Redis 主从切换丢数据 | 最多丢"最近几秒的冷却"，退化为现状，可接受 |
| Redis 完全不可用 | fail-open，记指标 + warn 日志，等价于 `cooldown.enabled=false` |
| 时钟漂移 | 冷却以 TTL 为准（Redis 服务端时钟），不用客户端绝对时间比较 |

**时钟漂移这条要特别说明**：不要在客户端算 `until - now()`，直接交给 Redis 的 `PX`。否则多实例时钟漂移 5s 就会让冷却提前/延后。

#### 3.5.5 避免惊群

三重防护：

1. **半开探测**（§3.4.4）：冷却到期只放 1 个。
2. **冷却写入去抖**：同一账号 1s 内多次失败只写一次（`SET ... NX` 用于"写入去抖标记"），避免 N 个并发请求同时发 Lua。
3. **"多账号同败不冷却"**（F-01，§3.4.3）：平台级故障不写冷却，避免全池同时进入冷却又同时到期。

#### 3.5.6 写库竞态（G10 修复）

新增 repository 方法（与 `SetRateLimitedIfLater` 同构）：

```go
// SetTempUnschedulableIfLater 仅当 until 晚于现有值时更新，避免并发失败覆盖更长冷却。
SetTempUnschedulableIfLater(ctx context.Context, id int64, until time.Time, reason string) error
```

实现：`UPDATE accounts SET temp_unschedulable_until = $2 WHERE id = $1 AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until < $2)`。

**改动范围**：只改 `tempUnscheduleGoogleConfigError` / `tempUnscheduleEmptyResponse`（`antigravity_gateway_retry.go:797-825`）两个调用点，其余 `SetTempUnschedulable` 调用点不动（它们语义是"设置状态"不是"延长冷却"）。**这缩小了 G10 的爆炸半径**。

#### 3.5.7 配置项

| 键 | 默认 | 说明 |
| --- | --- | --- |
| `gateway.failover_policy.cooldown.redis_timeout` | `500ms` | 冷却读取/写入超时 |
| `gateway.failover_policy.cooldown.fail_open` | `true` | Redis 失败时是否放行（必须 true） |
| `gateway.failover_policy.cooldown.batch_read_max` | `200` | MGET 批量上限（超出分片） |

#### 3.5.8 边界

- **不引入分布式锁**：T2 是"尽力而为的避让"，不需要互斥。加锁会把选号热路径变成锁竞争。
- **不保证**精确的"连续失败次数"：并发下 `INCR` 可能多计（同一失败被多个请求看到？不会——每个请求只对自己看到的失败计一次）。可能少计（Redis 写失败），可接受。
- **不做**跨账号的"相关性冷却"（如"A 账号挂了，同 IP 的 B 也可疑"）：无数据支撑，属过度设计。

---

### 3.6 幂等风险与可切换点

#### 3.6.1 现状

- 唯一保护：**写字节守卫** `c.Writer.Size() != writerSizeBeforeForward` → `handleFailoverExhausted(..., streamStarted=true)`（`gateway_handler.go:1060-1064`；Gemini 同构 `:523-526`）。
- OpenAI 有 `SafeToFailoverAfterWrite` + `openAIForwardMayFailover`（`openai_gateway_handler.go:3976-3984`），Anthropic/Gemini **没有等价机制**（G7）。
- 网关路由**无幂等中间件**（`server/routes/gateway.go:180-189`）；`IdempotencyConfig` 只服务管理端写接口（`config.go:243-260`、`handler/idempotency_helper.go:45`）。
- 换号必然产生**真实重复上游调用**：成本重复（每次真实计费），本地计费不重复（失败 `result=nil`，`gateway_handler.go:1104-1111`）。

#### 3.6.2 设计：判据从"字节数"升级为"语义字节"

**新判据（唯一真值）**：

```
canFailover = (semanticBytesWritten == 0) || (err.SafeToFailoverAfterWrite == true)
```

**为什么必须换掉 `Writer.Size()`**：

1. **ping/keepalive 污染**：排队等待期会写 ping（`concurrency.ping_interval=10s`，`config.go:2709`），写入位置在取槽阶段（`gateway_handler.go:791-798`），而 `writerSizeBeforeForward` 在其后采样（`:919`）。**静态看是安全的**，但 recon §六-10 已标为"未运行时验证，需 t3 实测"。若存在异步 ping goroutine 跨越采样点，`Size()` 会误判为非零 → 错误地禁止换号（把本可恢复的请求变成用户可见失败）。
2. **语义更清晰**：业务关心的不是"写了几个字节"，而是"用户是否已经看到内容"。SSE 注释帧（`: keepalive`）用户看不到，可以安全切换。
3. **平台一致性**：Anthropic 与 OpenAI 用同一判据，消除 G7 的"行为不一致"。

**实现**：在 SSE 转发层（`gateway_upstream_response.go` 的写入点）维护 `requestSemanticBytes` 计数器（`atomic.Int64`，挂 ctx 或 `FailoverState`）：

- 计入：任何 `data:` 行、任何非注释的 SSE 事件、任何非流式 JSON body 的非空写。
- 不计入：`:` 开头的注释帧、`event: ping` / `event: keepalive`、空行、`retry:` 行。

然后 `SendSemanticSSEFrame` 类的写入函数统一打点（若现有代码没有统一入口，**新增一个写入包装函数**并把现有写入点逐个改过去——这是本设计改动面最分散的一处，见 §附录A 阶段 3）。

#### 3.6.3 流式 vs 非流式

| 场景 | 可切换？ | 中间态 |
| --- | --- | --- |
| 非流式，header 未写出 | **是** | — |
| 非流式，header 已写出但 body 未写 | 否（现状 `Size()!=before` 会拦；实际 Gin 在 `WriteHeader` 后 `Size()` 仍可能是 0） | **需专项验证**：`c.Writer.Written()` 与 `Size()` 语义不同，设计统一用 `Written()` 判断"已提交" |
| 流式，仅 keepalive | **是**（设计新增能力） | semanticBytes=0，`SafeToFailoverAfterWrite` 视为 true |
| 流式，首个语义 chunk 已写 | **否**（默认） | 只能返回 SSE `error` 帧 |
| 流式，首个语义 chunk 已写 + `BoundUpstreamPassthrough` | 否 | 原样回写上游错误 |
| 流式，首个语义 chunk 已写 + OpenAI `SafeToFailoverAfterWrite` | 是（OpenAI 路径现状） | 用"流内重放"补齐（OpenAI 专有，本次不推广） |

**"header 已写出之后的策略"（任务明确要求）**：

- `Written()==true` 且 `semanticBytes==0`（只写了 header + keepalive）→ **可以切换**，但切换前**不能**再写任何字节（包括"重试中"提示），否则下游会把两次响应的 SSE 拼在一起。
- 切换后，新上游的响应直接续写同一客户端流。这是安全的，因为客户端只看到"一段连续的 SSE"，且之前只有注释帧。
- **风险**：若第一次尝试写了 `200 OK` header 且客户端已经按 200 处理，第二次尝试失败时无法改状态码 → 只能用 SSE `error` 帧。这正是现状行为，不改变。

#### 3.6.4 幂等：不做去重，做审计 + 可选禁令

**明确不做的**：`Idempotency-Key` 响应缓存（N4）。理由：流式响应无法重放；非流式重放需要存完整响应体（token 成本 × 存储成本）；且用户请求本身没有幂等语义保证。

**做的两件事**：

**(a) 切换链审计（默认开启）**：每次上游尝试写一条 `ops_error_logs.upstream_errors` 事件，`kind = "failover_attempt"`，携带 `request_id` / `attempt_seq` / `account_id` / `status` / `class`。这样"本次请求打了几次上游"可以按 `request_id` 聚合出来。现有 `queryAccountSwitchCount`（`ops_metrics_collector.go:568-587`）已经在读 `upstream_errors` 的 `kind`，只需把新 kind 加入 `IN` 列表。

> 注意：现有 `kind` 白名单是 `('failover','retry_exhausted_failover','failover_on_400')`，**新增 kind 必须同时改这里**，否则指标口径断裂（记为实施检查点 C-03）。

**(b) 可选禁令（默认关闭）**：

```yaml
gateway:
  failover_policy:
    idempotency:
      require_key_for_side_effecting: false   # 非流式 + 工具调用/写操作 → 必须有 Idempotency-Key 才能换号
      blocked_tools: ["bash", "str_replace_editor", "web_fetch"]  # 示例
```

开启后：检测到请求体含"有副作用的工具调用"且无 `Idempotency-Key` → `Class` 仍分类，但 `Failover=false`（只能同账号重试，不能跨账号）。这是给"绝对不能重复执行"的客户的逃生门，**默认关闭**因为会显著降低可用性。

#### 3.6.5 配置项

| 键 | 默认 | 说明 |
| --- | --- | --- |
| `gateway.failover_policy.switchable.use_semantic_bytes` | `true` | 用语义字节判据替代 `Writer.Size()` |
| `gateway.failover_policy.switchable.treat_keepalive_as_safe` | `true` | keepalive/ping 不计入语义字节 |
| `gateway.failover_policy.idempotency.require_key_for_side_effecting` | `false` | 见 3.6.4(b) |
| `gateway.failover_policy.idempotency.blocked_tools` | `[]` | 工具名列表 |

#### 3.6.6 边界

- **`use_semantic_bytes=true` 是行为变更**（会让原本"因 ping 被误拦"的请求变得可切换）。必须放在 §7 的变更清单里，且提供 `false` 回退到 `Writer.Size()` 语义。
- 语义字节计数器必须**每请求独立**，不能用全局 `atomic`（否则并发请求互相污染）。
- 流式切换后**不能**重发 `event: message_start`（客户端已收到第一次的）→ 这是 OpenAI 路径的既有约束，Anthropic 路径本次**不启用**"流内切换"，只允许"首 chunk 前切换"。范围明确。

---

### 3.7 超时处理

#### 3.7.1 现状

| 超时 | 默认 | 范围 | 证据 |
| --- | --- | --- | --- |
| `gateway.response_header_timeout`（Anthropic） | `600s`（代码兜底 300s） | 到响应头 | `config.go:1058-1060`、`:2523`；`repository/http_upstream.go:56-58` |
| `gateway.openai_response_header_timeout` | `0`（无） | OpenAI | `config.go:1061-1063` |
| `gateway.grok_response_header_timeout` | `120s` | Grok | `config.go:1065-1067` |
| `gateway.openai_first_output_timeout_seconds` | `0`（关闭；开启需 30–600） | OpenAI 首语义输出 | `config.go:1068-1069`、`:3499-3501` |
| `gateway.stream_data_interval_timeout` | `180s` | 流内间隔 | `config.go:1165-1166` |
| 同账号重试退避 | 500ms → cap 8s | — | `failover_loop.go:38-41` |
| 503 单账号退避 | 2s/轮 | — | `failover_loop.go:45` |
| 服务层原地重试 | 300ms→3s，总 10s | — | `gateway_forward.go:25-33` |

**缺陷**：没有"整请求预算"。最坏耗时 = `(MaxSwitches+1) × (转发 + 同账号重试 + 退避)`，可达分钟级（G2）。

#### 3.7.2 设计：三层超时叠加

```
┌─────────────────────────────────────────────────────────────┐
│ L3 整请求预算 Deadline（新增，唯一权威时钟）                   │
│    gateway.failover_policy.budget.total_timeout              │
│    默认 0（关闭）= 现状；建议 120s（非流式）/ 流式仅覆盖首字节前 │
│                                                              │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ L2 单账号总超时 per_account_deadline（新增，默认 0）    │  │
│  │    覆盖该账号的所有同账号重试                           │  │
│  │                                                       │  │
│  │  ┌─────────────────────────────────────────────────┐  │  │
│  │  │ L1 首字节超时 first_output_timeout（已有 OpenAI）│  │  │
│  │  │    单次尝试级别，超时 → 可安全换号               │  │  │
│  │  └─────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

**叠加规则（必须实现为该顺序）**：

1. `Deadline = now + total_timeout`（若 `total_timeout == 0` 则零值，全链路短路）。
2. 每次尝试前：`remaining = Deadline - now`；若 `remaining <= 0` → **立即 `FailoverExhausted(budget)`**，不再切换。
3. 本次尝试的上游 ctx deadline = `min(Deadline, now + per_account_deadline, now + header_timeout)`（`per_account_deadline` 为 0 时忽略）。
4. 进入下一账号前：`remaining < min_switch_cost`（默认 2s）→ **不再切换**，直接返回错误。理由：一次上游往返 > 2s，切过去必然超时，不如让用户早 2s 拿到错误。
5. **流式阶段停表**：`semanticBytes > 0` 后 `Deadline` 不再参与（不能因为总预算把已经给用户输出的流掐断）。实现：`FailoverBudget.StopClock()` 在首个语义字节写入时调用。

**预算耗尽必须是错误，不是继续切换**（任务明确要求）：
- 返回 `FailoverOutcomeError{Reason: "budget_exhausted"}`，HTTP 状态用最后失败的映射，`Retry-After` 用剩余冷却估算。
- 打 `gateway.failover_budget_exhausted` 日志 + `failover_exhausted_total{reason="budget"}` 指标。
- **不得**静默复用最后一次错误当作"账号耗尽"（否则运维无法区分"没账号了"和"时间不够了"）。

#### 3.7.3 与 `context` 的关系（易错点）

**不要在 `c.Request.Context()` 上直接 `WithDeadline`**。原因：

- 上游请求需要自己的 ctx（现有 `http_upstream` 会派生）。
- 流式响应必须在客户端断开时继续完成计费（`failover_loop.go:327-347` 的 `failoverClientGone` 语义）。
- 若把整请求预算挂在 `c.Request.Context()` 上，预算到期会 cancel 客户端请求 → 已写出的流被切断 → 从"可恢复失败"变成"半截响应"。

**正确做法**：`FailoverBudget` 是**纯逻辑时钟**（记录 `Deadline time.Time`），由 handler 在每次尝试前**主动检查**，不派生 ctx 的 cancel。上游 ctx 的 deadline 由第 3 步单独设置。

#### 3.7.4 首字节语义超时推广到 Anthropic

**建议做，但不在本次默认开启。** 理由：

- 现状 Anthropic 只有 600s 的响应头超时 —— 意味着一个挂死的上游会让单个账号占用 10 分钟，乘以 11 个账号最坏 110 分钟。这是 G2 的极端形态。
- 但 Anthropic 的 "thinking" 模型首 token 可能很慢（长思考），阈值设错会误杀健康请求。
- 设计：`first_output_timeout` 统一语义（首个**语义** chunk，复用 §3.6.2 的计数器），默认 `0`（关闭）；运维按模型设置 `gateway.failover_policy.first_output_timeout_seconds`（建议 90s 起，对齐 OpenAI 的下限 30s）。
- **收益**：把"挂死账号"的占用从 600s 降到 90s，直接改善 P99。

#### 3.7.5 配置项

| 键 | 默认 | 范围 | 说明 |
| --- | --- | --- | --- |
| `gateway.failover_policy.budget.total_timeout` | `0`（关） | 0–1800s | L3 整请求预算 |
| `gateway.failover_policy.budget.per_account_timeout` | `0`（关） | 0–600s | L2 单账号总超时 |
| `gateway.failover_policy.budget.min_switch_cost` | `2s` | 0–30s | 切前剩余预算下限 |
| `gateway.failover_policy.first_output_timeout_seconds` | `0`（关） | 0 或 30–600 | L1 首语义字节超时（Anthropic/Gemini 推广） |
| `gateway.failover_policy.budget.stop_clock_on_stream` | `true` | bool | 首语义字节后停表 |

#### 3.7.6 边界

- **预算不覆盖"排队等槽"时间**：`tryAcquireAccountSlot` 的等待（`sticky_session_wait_timeout=120s` / `fallback_wait_timeout=30s`，`config.go:2661-2662`）发生在 `Forward` 之前。若把排队计入预算，会让"排队久但上游快"的请求被误杀。设计：**预算从首次 `Forward` 开始计时**，排队不计。这需要在 handler 里把 `StartClock()` 放在取槽之后。
- **不改变**现有 `response_header_timeout` 的语义，只在其外再套 L2/L3。
- `stream_data_interval_timeout`（180s）是**流内**超时，与 L1/L2/L3 正交，不叠加。

---

### 3.8 日志告警与最终错误

#### 3.8.1 结构化日志

**每次尝试（切换点）一条 `gateway.failover_attempt`**（Warn 级，替换现有 `gateway.failover_switch_account`）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `request_id` | string | 贯穿全请求 |
| `group_id` / `platform` / `model` | — | 定位 |
| `attempt_seq` | int | 第几次上游调用（含同账号重试） |
| `switch_seq` | int | 第几次换号（= SwitchCount） |
| `from_account_id` / `to_account_id` | int64 | 切换前后 |
| `selection_layer` | string | `sticky` / `load_balance` / `router_sticky` / `wait_queue` |
| `failure_class` | string | §3.3.2 的 class |
| `upstream_status` / `upstream_kind` | int/string | 原始状态码与 ops kind |
| `stage` / `scope` | string | `GatewayFailureStage` / `GatewayFailureScope`（已有枚举） |
| `same_account_retry` | bool | 本次是否为同账号重试 |
| `response_committed` / `semantic_bytes` | bool/int | 可切换判据的现场 |
| `cooldown_written` / `cooldown_ttl_ms` | bool/int | T2 是否生效 |
| `t3_written` | bool | T3 是否写库 |
| `sticky_cleared` | bool | 是否清粘性 |
| `budget_remaining_ms` | int64 | 剩余预算（0 = 未启用） |
| `elapsed_ms` | int64 | 本跳耗时 |
| `policy_source` | string | `account` / `group` / `global`（就近覆盖来源） |

**终局一条 `gateway.failover_outcome`**（Info 级，每次请求最多一条）：

| 字段 | 说明 |
| --- | --- |
| `outcome` | `success` / `exhausted` / `budget_exhausted` / `client_gone` / `no_candidate` / `policy_blocked` |
| `attempts` / `accounts_tried` / `switches` | 计数 |
| `chain` | `[]SwitchHop` 完整切换链（§3.1.2a） |
| `duration_ms` / `ttft_ms` | 总耗时 / 首语义字节耗时 |
| `final_status` / `final_class` | 终局映射 |
| `retry_after` | 回写给客户端的 `Retry-After` |

**为什么 outcome 用 Info 而不是 Debug**：它承载切换链，是排障的唯一依据；量级可控（每请求 1 条）。

#### 3.8.2 指标

| 指标 | 类型 | 标签 | 来源 |
| --- | --- | --- | --- |
| `failover_switch_total` | Counter | `class`, `platform` | 新增（替代从 `upstream_errors` 反推） |
| `failover_exhausted_total` | Counter | `reason`（`no_candidate`/`budget`/`max_switches`/`policy_blocked`） | 新增 |
| `failover_attempts_per_request` | Histogram | — | 新增（直方图桶：1,2,3,4,6,8,12,20,50） |
| `failover_duration_seconds` | Histogram | `outcome` | 新增（失败到成功/耗尽的时长） |
| `failover_cooldown_active_accounts` | Gauge | `group_id` | 新增（MGET 采样或 scard） |
| `failover_cooldown_degraded_total` | Counter | — | Redis 失败放行次数（**必须监控**，非零代表 T2 失效） |
| `account_switch_count`（已有） | 派生 | 全局 | `ops_metrics_collector.go:568-587`，**保留不动**，新增 kind 加入 `IN` 列表 |

**`account_switch_count` 与 `failover_switch_total` 双源问题**：保留旧的（不改 dashboard），新的作为补充。文档明确：**新告警只用新指标**，旧指标作为兼容视图。

#### 3.8.3 告警

**重要更正（对 recon G9）**：recon 称"无分组维度、仅 error_rate/upstream_error_rate"，**不准确**。`ops_alert_evaluator_service.go` 的 `evaluateRule` 第一处 switch（`:447-575`）**已经支持分组维度**：

| 已有 metric_type | 语义 | 行号 |
| --- | --- | --- |
| `group_available_accounts` | 分组可用账号数 | `:463` |
| `group_available_ratio` | 分组可用账号比例 % | `:478` |
| `group_rate_limit_ratio` | 分组限流账号比例 % | `:524` |
| `account_error_ratio` | 账号错误比例 % | `:539` |
| `account_temp_unscheduled_count` | 临时停调账号数 | `:512` |
| `overload_account_count` | 过载账号数 | `:555` |

**结论：分组级"账号池健康"告警今天就能配，不需要开发。** 只需要配置规则：

| 优先级 | 规则 | metric_type | 条件 |
| --- | --- | --- | --- |
| P0 | 分组账号池濒临耗尽 | `group_available_ratio` | `< 20` 持续 10min |
| P1 | 分组限流面过大 | `group_rate_limit_ratio` | `> 30` 持续 15min |
| P2 | 分组临时停调账号多 | `account_temp_unscheduled_count` | `>= 3` 持续 20min |

**需要新增的只有 2 个 metric_type**（failover 专项，`evaluateRule` 两处 switch 各加一个 case）：

| metric_type | 语义 | 阈值示例 |
| --- | --- | --- |
| `failover_exhausted_ratio` | 耗尽请求 / 总请求（%），5min 窗口，分组维度 | `>= 5` 持续 10min → **P0** |
| `failover_switch_ratio` | 发生切换的请求 / 总请求（%），5min 窗口 | `>= 30` 持续 15min → **P1**（切换率突增 = 账号池在退化） |

**实现依赖（未决）**：`failover_*_ratio` 需要按分组聚合。现有 `queryAccountSwitchCount` 的 SQL 不按分组过滤（`ops_metrics_collector.go:568-587`），需确认 `ops_error_logs` 是否有 `group_id` 列；若没有，需从 `usage_logs` 关联或新增列。**记为 §8-Q6。**

#### 3.8.4 最终错误返回策略

**现状**：终局 = 最后一个错误的映射（`gateway_handler.go:1921-1985`），无聚合；Anthropic 路径不复制 `Retry-After`（只有 OpenAI 的 `copyFailoverRetryAfter`，`openai_gateway_handler.go:3592-3597`、`:3735-3745`）。

**设计**：

```go
type FailoverOutcomeError struct {
    Reason        string           // no_candidate | max_switches | budget_exhausted | all_429 | ...
    Attempts      []AttemptFailure // {AccountID, Class, Status, RetryAfter}
    FinalStatus   int              // 对外 HTTP 状态
    RetryAfter    time.Duration    // 取 max(各次 cooldown)/min(reset)
    LastErr       error            // 保持现有 mapUpstreamError 兼容
}
```

**状态码选择（新规则，替换"最后错误映射"）**：

| 全部尝试的同质性 | 对外状态 | 理由 |
| --- | --- | --- |
| 全部 `rate_limited` / `quota_exhausted` | `429` + `Retry-After = min(各 reset)` | 客户端应退避，语义精确 |
| 全部 `upstream_5xx` / `network` / `timeout` | `502`（保持现状） | 网关错误，可重试 |
| 全部 `capacity_shed`（503） | `503` + `Retry-After` | 上游容量问题，让客户端稍后重试 |
| **混合类别**（既有 429 又有 5xx 等） | **`503`** + `Retry-After` | 现状是"取最后一个"→ 结果取决于哪个账号最后失败（不确定）。`503` 表达"暂时不可用"最准确 |
| 全部 `request_invalid` | 原样映射（400/404/422） | 请求问题，重试无效 |
| `budget_exhausted` | 最后失败的映射 + `Retry-After` | 时间不够，不是账号不够 |
| `policy_blocked` | `400` | 本地策略拒绝（现状） |

**`Retry-After` 取值规则**：

1. 优先用失败尝试中**最大的上游 `Retry-After`/reset 剩余**（乐观：最短等待）。
2. 若没有上游值，用 T2 冷却剩余（若 T2 关闭则为 0 → 不回写 header）。
3. 上限 `3600s`（避免回写一个巨大的值让客户端永不重试）。
4. Anthropic 路径**必须补上**（修 G8），复用 OpenAI 的 `copyFailoverRetryAfter` 逻辑但抽成公共函数。

**是否暴露上游细节**：

| 内容 | 默认 | 开关 |
| --- | --- | --- |
| 上游响应体 | **不暴露** | 现有 `mapUpstreamError` 已有透传规则（`error_passthrough_runtime.go`），不改 |
| 尝试次数 / 失败类别列表 | **不暴露** | `gateway.failover_policy.expose_failover_detail`（默认 `false`），开启后返回 `{"error":{"type":"...","failover":{"attempts":3,"classes":["rate_limited","upstream_5xx"]}}}` |
| 上游 `Retry-After` | 暴露（作为 header，不暴露 body） | 无开关（标准语义） |

**为什么默认不暴露 attempts**：暴露"3 个账号都挂了"等于向客户端泄露内部池规模，属于信息泄露。但对运维自己的监控客户端有用，故留开关。

#### 3.8.5 边界

- 终局错误映射**必须保持**现有 `mapUpstreamError` 的可观测契约：现有测试 `gateway_handler_error_fallback_test.go` 锁定了错误兜底，改状态码选择规则会破坏它 → 必须**新增**测试而不是改旧测试，并在 §7 列出受影响测试。
- `Retry-After` 只对 `429`/`503` 有意义；对 `502` 回写会让部分客户端过度退避，因此 `502` 场景不回写（与现状一致）。

---

## 四、方案对比与推荐

### 4.1 三个候选方案

| | **方案 A：最小改动** | **方案 B：冷却层（推荐）** | **方案 C：调度器内置** |
| --- | --- | --- | --- |
| 核心内容 | 只补配置项 + 统一规则表（L0+L1） | A **+** T2 冷却层 **+** 预算层 **+** 观测层 | 把失败评分下沉到 `scheduler_snapshot_service.go`，切换变成调度决策 |
| 改动文件 | `config.go`、新增 `failover_classifier.go`、4 处调用点、`groups` 迁移 | A + 新增 `failover_cooldown.go`、`failover_budget.go`、`gateway_scheduling.go` 插 1 个过滤器、`ops_*` 指标 | 改 `scheduler_snapshot_service.go`、`gateway_scheduling.go` 排序链、快照结构 |
| 解决的缺口 | G1、G8（部分） | G1、G2、G3、G4（审计）、G5、G10 | G1、G3、G6（顺序可确定性） |

### 4.2 逐维对比

| 维度 | 方案 A | 方案 B | 方案 C |
| --- | --- | --- | --- |
| **改动面** | 小：~8 文件，无并发改动 | **中：~14 文件**，新增 2 个 service 文件 + 1 migration | 大：调度器核心 + 快照结构 + 排序链，间接影响所有选号路径 |
| **并发一致性** | 无新增共享状态（最安全） | Redis 原子 Lua + fail-open；T2 可丢失但可降级 | 失败评分要进快照 → 快照需高频重建 → 与现有"bucket rebuild 节流"冲突（`scheduler_snapshot_service.go` 注释提及节流） |
| **多实例正确性** | N/A（无跨请求状态） | 正确（Redis 共享）；Redis 故障 fail-open = 退化为 A | 依赖快照同步，多实例下"各实例看到不同失败评分"必然出现，且无收敛保证 |
| **可观测性** | 中（有分类日志，无切换链/无冷却视图） | **高**（切换链 + 5 指标 + 冷却 gauge + 2 告警类型） | 中（评分在调度器内部，需额外暴露） |
| **回滚难度** | 低（配置） | **低**（`cooldown.enabled=false` + `budget.*=0` = 完全等价于 A） | **高**（改的是调度算法，回滚要回滚代码 + 快照格式） |
| **对现有测试影响** | 中（4 处状态码集合的行为要逐项对齐） | **中低**（`failover_loop_test.go` 等 13 个文件基本不动；新增测试为主） | **高**（`gateway_account_selection_test.go` 等选号测试全部受影响） |
| **解决 G2（预算）** | 否 | **是** | 否（预算天然是请求级概念，不属于调度器） |
| **解决 G3（跨请求隔离）** | 否 | **是** | 是（但方式更重） |
| **解决 G6（顺序可解释）** | 部分（分类可读但顺序仍随机） | **是**（switch_chain 记录） | 是（顺序确定，但用户未必需要确定性） |
| **实施周期（估）** | 3–4 天 | **7–9 天** | 12–15 天 |

### 4.3 推荐：方案 B

**三条硬理由**：

1. **A 不解决用户最痛的问题**。G3（坏账号跨请求反复命中）是"用户感觉容灾不好用"的直接原因——每次都要先失败一次才换号。只做 A 等于"把规则做成可配置的，但坏账号照样反复打"。
2. **C 修错了层**。三个结构性理由：
   - **C 无法解决请求级预算**（G2）。预算是"这次请求还能不能继续"的概念，调度器不知道 `Forward` 花了多久。
   - **C 与现有快照模型冲突**。"失败评分"是**秒级变化**的状态，而调度快照是**分钟级重建 + 节流**（`scheduler_snapshot_service.go:211-273` 的 bucket 机制）。把秒级状态塞进分钟级快照，必然要么频繁重建（打爆 Redis）要么评分陈旧（等于没有）。
   - **C 会破坏分层**。当前 `scheduler_snapshot_service` 只做"静态可调度性"，`gateway_scheduling` 做"选择策略"，handler 做"重试决策"。C 把三者揉在一起，后续任何一处改动都会波及全局。
3. **B 的可回滚性是决定性的**。B 的所有新能力都由**默认值为"关闭"**的配置控制：
   - `cooldown.enabled=false`（默认）
   - `budget.*=0`（默认）
   - `switch_order.record_chain=true`（默认开启，但只是日志）
   - `use_semantic_bytes=true`（默认开启，行为变更项，可关）

   因此：**发布 B = 发布 A 的行为**；开启能力 = 改配置；出问题 = 改配置回滚，**不需要重新发版**。这在生产网关上是决定性优势。

**推荐方案里的两个诚实取舍（必须在评审时明确）**：

- **`use_semantic_bytes=true` 是有意的默认行为变更**。它会改变"ping 已写时能否换号"的判定。若担心，发布时设为 `false`（= 现状），观察一周后再开。**记入 §7.3 变更清单第 1 条。**
- **T2 默认关闭意味着 G3 在默认配置下未修复**。这是刻意选择：G3 的修复需要运维理解"冷却"概念并接受"账号短暂不被调度"。强制默认开启会在首次上线时造成不可预期的池容量下降。**修复路径写进发布手册**（附录 A 阶段 4）。

### 4.4 被否决的第四方案（记录以免重复讨论）

**方案 D：把 `FailoverState` 提到 Redis，做"跨请求共享的请求状态机"** —— 否决。理由：请求状态天然是进程内的，放 Redis 会引入序列化成本、一致性问题和"请求崩溃后状态残留"问题；而 T2 冷却已经用最小代价覆盖了唯一的跨请求需求（"别选它"）。

---

## 五、配置项清单

### 5.1 全局配置（`config.yaml` / `gateway.failover_policy`）

> 所有键的 `mapstructure` 前缀为 `gateway.failover_policy.`（结构体：`GatewayFailoverPolicyConfig`，挂在 `GatewayConfig` 下，先例 `gateway.scheduling`）。

| 键 | 类型 | 默认 | 范围 | 分组覆盖 | 说明 |
| --- | --- | --- | --- | --- | --- |
| `enabled` | bool | `true` | — | 是 | 策略层总开关；`false` = 全部走旧硬编码 |
| `status_classes` | []Rule | 见 §3.3.2 | — | 是（整体替换） | 规则表 |
| `network_errors.enabled` | bool | `true` | — | 是 | 网络层是否参与分类 |
| `failover_on_400` | bool | `false` | — | 是 | 兼容旧键，行为不变 |
| `compat_400_keywords` | []string | 9 个（§3.3.1） | ≤64 项 | 是 | 400 白名单 |
| `same_account.default_mode` | string | `inherit` | `inherit`/`off` | 是 | 同账号重试默认策略 |
| `budget.total_timeout` | duration | `0`（关） | 0–1800s | 是 | L3 整请求预算 |
| `budget.per_account_timeout` | duration | `0`（关） | 0–600s | 是 | L2 单账号超时 |
| `budget.max_total_upstream_attempts` | int | `0`（关） | 0–50 | 是 | 上游调用总次数硬顶 |
| `budget.max_same_account_attempts_total` | int | `0`（关） | 0–50 | 是 | 同账号重试合计上限 |
| `budget.min_switch_cost` | duration | `2s` | 0–30s | 是 | 切前剩余预算下限 |
| `budget.stop_clock_on_stream` | bool | `true` | — | 是 | 首语义字节后停表 |
| `first_output_timeout_seconds` | int | `0`（关） | 0 或 30–600 | 是 | L1 首语义字节超时 |
| `cooldown.enabled` | bool | **`false`** | — | 是 | T2 总开关 |
| `cooldown.consecutive_failures_threshold` | int | `3` | 1–20 | 是 | 连续失败阈值 |
| `cooldown.streak_window` | duration | `120s` | 10s–1h | 是 | 连续失败计数窗口 |
| `cooldown.default_ttl` | duration | `60s` | 1s–1h | 是 | 默认冷却时长 |
| `cooldown.max_ttl` | duration | `24h` | 60s–24h | 是 | 上游 reset 截断上限 |
| `cooldown.pool_mode_enabled` | bool | `true` | — | 是 | 池模式账号是否受 T2 |
| `cooldown.probe_enabled` | bool | `true` | — | 是 | 半开探测 |
| `cooldown.probe_max_attempts` | int | `3` | 1–10 | 是 | 探测次数上限 |
| `cooldown.redis_timeout` | duration | `500ms` | 50ms–5s | 否 | Redis 读写超时 |
| `cooldown.fail_open` | bool | `true` | — | 否 | **必须 true** |
| `cooldown.batch_read_max` | int | `200` | 1–1000 | 否 | MGET 上限 |
| `switch_order.record_chain` | bool | `true` | — | 否 | 切换链日志 |
| `switch_order.clear_sticky_on_account_failure` | bool | `true` | — | 是 | 账号级失败清粘性 |
| `max_selection_retries` | int | `1` | 0–5 | 是 | 排除集清空次数上限 |
| `switchable.use_semantic_bytes` | bool | `true` | — | 否 | 语义字节判据（**行为变更项**） |
| `switchable.treat_keepalive_as_safe` | bool | `true` | — | 否 | keepalive 不计入 |
| `idempotency.require_key_for_side_effecting` | bool | `false` | — | 是 | 有副作用请求需幂等键 |
| `idempotency.blocked_tools` | []string | `[]` | — | 是 | 工具名列表 |
| `t3.only_extend` | bool | `true` | — | 否 | T3 只延长不缩短 |
| `expose_failover_detail` | bool | `false` | — | 是 | 终局是否暴露 attempts |

### 5.2 保留不变的现有键（不改名、不改默认、不改语义）

| 键 | 默认 | 说明 |
| --- | --- | --- |
| `gateway.max_account_switches` | `10` | **语义不变**（切换次数） |
| `gateway.max_account_switches_gemini` | `3` | 不变 |
| `gateway.max_same_account_retries_gemini` | `4` | 不变（由新预算层收敛，不改默认） |
| `gateway.failover_on_400` | `false` | 不变（`failover_policy.failover_on_400` 优先，两者都为 false 时行为 = 现状） |
| `gateway.response_header_timeout` | `600` | 不变 |
| `gateway.openai_first_output_timeout_seconds` | `0` | 不变 |
| `gateway.scheduling.*`（9 项） | 不变 | 不变 |

### 5.3 分组级列（新增，`groups` 表）

| 列 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `failover_on_400` | `BOOLEAN NULL` | `NULL`（回落全局） | 单开关，高频调整项 |
| `failover_max_account_switches` | `INT NULL` | `NULL` | 分组覆盖切换次数 |
| `failover_policy` | `JSONB NULL` | `NULL` | 完整策略覆盖（字段级 merge，`nil` = 回落） |

**为什么 `failover_on_400` 和 `max_account_switches` 单列而不只放 JSONB**：与 `groups.opsmgmt_min_margin_basis_points BIGINT NULL` 同构——高频、单标量、需要能被 SQL 直接查询/统计的项走独立列；低频组合项走 JSONB。这样运维能在 pgAdmin 里直接改，不需要构造 JSON。

### 5.4 账号级（`accounts.credentials`，已有机制）

| 键 | 现状 | 本次 |
| --- | --- | --- |
| `pool_mode` / `pool_mode_retry_count` / `pool_mode_retry_status_codes` | 已有 | **不变**，作为 `same_account=inherit` 的取值来源 |
| `custom_error_codes_enabled` / `custom_error_codes` | 已有 | **不变**（优先级仍最高，覆盖策略表） |
| `temp_unschedulable_rules` | 已有 | **不变**（负责 T3 粒度） |
| `failover_policy`（建议新增） | 无 | **新增可选**：账号级 JSON 覆盖（最高优先级）。建议先不实现，等分组覆盖跑稳再开 |

---

## 六、数据与状态模型

### 6.1 DB 变更（迁移 254）

```sql
-- 254_group_failover_policy.sql
ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_on_400 BOOLEAN NULL;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_max_account_switches INT NULL;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_policy JSONB NULL;

COMMENT ON COLUMN groups.failover_on_400 IS '分组级 failover_on_400 覆盖；NULL 回落 gateway.failover_policy.failover_on_400';
COMMENT ON COLUMN groups.failover_policy IS '分组级 failover 策略覆盖（字段级 merge，null 字段回落全局）';
```

- **不建表**：无新实体。
- **不改 `accounts`**：T2 全在 Redis，T3 复用现有列。
- **迁移编号**：`253_ops_mgmt.sql` 是当前最大（已核实 `backend/migrations` 目录），故用 `254`。
- **可空 + 无默认值**：`NULL` 表示"未覆盖"，符合 §3.3.5 的指针语义。**禁止** `DEFAULT false`（会把"未配置"变成"配置了 false"）。

### 6.2 Redis key 结构

命名遵循现有扁平前缀风格（`concurrency:account:` / `session_limit:account:` / `window_cost:account:`，见 `repository/concurrency_cache.go:28`、`repository/session_limit_cache.go:26-30`）：

| Key | 类型 | Value | TTL | 说明 |
| --- | --- | --- | --- | --- |
| `failover:cooldown:acct:{accountID}` | string | `{class}\|{deadlineMs}` | 5s–24h（class 决定） | 账号级冷却 |
| `failover:cooldown:acct:{accountID}:{modelHash}` | string | 同上 | 同上 | 模型级冷却（仅 rate_limited/quota_exhausted 且上游明确指模型） |
| `failover:streak:acct:{accountID}` | string (counter) | 整数 | `streak_window`（默认 120s） | 连续失败计数 |
| `failover:probe:acct:{accountID}` | string | `1` | 10s | 半开探测令牌（SETNX） |
| `failover:cooldown_degrade:{instanceID}` | string | `1` | 60s | 本实例最近一次 fail-open 标记（仅用于指标去抖） |

**不设** `failover:budget:*`：预算是进程内 `FailoverState` 字段，不需要共享（理由见 §4.4 否决方案 D）。

**不设** "全局越线计数"：告警从 `ops_error_logs` 派生，避免双源。

**`modelHash`**：`sha256(model)[:16]`，与现有 `hashSensitiveValueForLog` 风格一致（`openai_compat_prompt_cache_key.go:74`），避免超长 key。

### 6.3 进程内状态（`FailoverState` 扩展）

```go
type FailoverState struct {
    // ——— 现有字段，全部保留 ———
    SwitchCount           int
    MaxSwitches           int
    FailedAccountIDs      map[int64]struct{}
    SameAccountRetryCount map[int64]int
    LastFailoverErr       *service.UpstreamFailoverError
    ForceCacheBilling     bool
    hasBoundSession       bool
    profitVetoedAccountIDs map[int64]struct{}
    profitVetoCount        int

    // ——— 新增 ———
    Budget        *FailoverBudget  // 预算与时钟
    Policy        *FailoverPolicy  // 生效策略（含 source 标签）
    SwitchChain   []SwitchHop      // 切换链
    SelectionRetryCount int        // 排除集清空次数（活锁护栏）
    SemanticBytes atomic.Int64     // 已写出的语义字节（可切换判据）
}

type FailoverBudget struct {
    Deadline              time.Time     // 零值 = 未启用
    MaxTotalAttempts      int           // 0 = 未启用
    MaxSameAccountTotal   int           // 0 = 未启用
    TotalAttempts         int
    SameAccountAttempts   int
    Started               bool          // 首次 Forward 后置 true
    ClockStopped          bool          // 首个语义字节后置 true
}
```

**并发性**：`FailoverState` 每请求一个实例，被单个 goroutine 顺序访问（现有假设，`failover_loop.go` 无锁）。`SemanticBytes` 用 `atomic.Int64` 是因为 SSE 写入可能发生在 HTTP 库的 goroutine（如 chunked writer 的回调）——这是唯一需要原子的字段。

### 6.4 状态迁移图（单个账号的生命周期）

```
      正常
        │ 失败（class 需要冷却）
        ▼
   ┌─────────┐  连续未达阈值（5xx/网络类）   ┌──────────┐
   │ 计数中  │ ─────────────────────────────►│ 仍正常   │
   │(streak) │      streak 窗口过期          └──────────┘
   └────┬────┘
        │ streak >= threshold 且 !多账号同败
        ▼
   ┌─────────┐   半开探测成功 / 成功即清   ┌──────────┐
   │ T2 冷却 │ ───────────────────────────►│ 正常     │
   └────┬────┘                             └──────────┘
        │ TTL 到期
        ▼
   ┌──────────────┐  探测成功              ┌──────────┐
   │ 半开（探测） │ ──────────────────────►│ 正常     │
   └──────┬───────┘                        └──────────┘
          │ 探测失败（最多 3 次）
          ▼
   清空冷却（交给调度器判断）→ 正常
```

---

## 七、兼容性与迁移

### 7.1 完全不变的清单（逐项承诺）

| # | 行为 | 证据/保障 |
| --- | --- | --- |
| C1 | `maxAccountSwitches` 语义 = 切换次数，尝试上限 = 值+1 | `failover_loop.go:245-250` 不改 |
| C2 | 默认可换号状态码集合 `{401,403,429,529} ∪ [500,∞)` | 分类器默认规则表逐项等价 + 参数化测试锁定 |
| C3 | `failover_on_400=false` 时 400 不换号 | 默认值不变 |
| C4 | 池模式不写 DB 账号状态 | `cooldown.enabled=false` 默认，T2 不写 DB |
| C5 | `custom_error_codes` 优先级最高 | 分类器入口先检查（现有 `CheckErrorPolicy` 顺序不变） |
| C6 | `temp_unschedulable_rules` 语义 | 不改 |
| C7 | 同账号重试行为（`pool_mode_retry_count` 默认 3） | `same_account=inherit` |
| C8 | 粘性会话在选号内的全部行为 | 不改选号逻辑，只在失败路径加"清绑定"（默认开启，见 §7.3 变更 2） |
| C9 | 503 单账号退避 + 排除集清空 | 保留，仅加"最多 1 次"护栏（默认 1 = 现状的典型路径） |
| C10 | 利润门否决与 `maxProfitVetoAttempts=10` | 不改 |
| C11 | 写字节守卫的**阻止**语义 | 语义字节判据只会**放宽**（allow more switching），不会**收紧**——除了一个例外：见 §7.3 变更 1 |
| C12 | 现有 13 个测试文件全部通过 | §附录C |
| C13 | 现有 23 项全局配置 + 6 项账号级配置的默认值与语义 | 5.2 / 5.4 |

### 7.2 迁移步骤（零停机）

1. **迁移 254**（三列全 `NULL`）——对现有代码完全无影响（旧代码不读这些列）。
2. **发布新版本**，所有新能力默认关闭/等价，行为与旧版一致。用 §7.3 的 3 个行为变更项逐项确认。
3. **配置分组覆盖**（如需）——只对指定分组生效。
4. **逐项开启能力**（灰度）：`record_chain`（默认已开）→ `cooldown.enabled`（先 1 个分组）→ `budget.*`。
5. **回滚**：任何一步只需改配置（无 schema 依赖）。迁移 254 不可回滚但**无害**（三列 `NULL` 被旧代码忽略）。

### 7.3 有意的默认行为变更（**必须评审确认，共 3 项**）

| # | 变更 | 影响 | 关闭方法 | 建议 |
| --- | --- | --- | --- | --- |
| **B1** | 可切换判据从 `Writer.Size()` 改为"语义字节" | keepalive 后现在**允许**换号（原本被拦）→ 更多请求可恢复；同时也意味着更多上游重复调用 | `switchable.use_semantic_bytes=false` | **建议开启**（修复 G7 的真实收益大于成本）；若不放心，首发设 `false` 观察 1 周 |
| **B2** | 账号级失败后清粘性（`clear_sticky_on_account_failure`） | 坏账号不再被后续请求反复命中（修 G3 的一半）；代价：切换后缓存亲和性丢失，首 token 变慢 | `switch_order.clear_sticky_on_account_failure=false` | **建议开启**——这是 G3 最便宜的修法 |
| **B3** | 混合类别终局错误从"最后错误映射"改为 `503` + `Retry-After` | 客户端行为变化（部分客户端对 502 与 503 退避策略不同） | 无独立开关（可加）| 建议开启，但需 **t3 兼容性审查**确认无客户端依赖 502 |

> 除 B1/B2/B3 外，**本设计不含其他默认行为变更**。G2/G3 的冷却与预算、G4 的幂等禁令全部默认关闭。

### 7.4 配置迁移注意

- `gateway.failover_on_400` 与 `gateway.failover_policy.failover_on_400` 同时存在：**新键优先**，旧键作为回落（避免现网 `config.yaml` 失效）。两者都为 `false` → 现状。
- 环境变量命名：`viper` 的 `mapstructure` 自动映射为 `SUB2API_GATEWAY_FAILOVER_POLICY_COOLDOWN_ENABLED` 之类。**需在 `config.go` 的 `Validate()` 中加入 `:3496-3504` 同风格的范围校验**（否则非法值会静默生效）。
- 校验规则（必须）：
  - `total_timeout >= per_account_timeout`（若两者都非 0）
  - `consecutive_failures_threshold >= 1`
  - `default_ttl <= max_ttl`
  - `max_total_upstream_attempts == 0 或 >= 1`
  - `cooldown.fail_open == false` 时**拒绝启动**或强制 warn（fail-open=false 意味着 Redis 故障会导致全站不可用，是危险配置）

---

## 八、未决问题

| # | 问题 | 需要谁决策 | 影响的章节 | 默认处置（若无答复） |
| --- | --- | --- | --- | --- |
| **Q1** | 是否统一 OpenAI 网关 / 生图 / AlphaSearch / Grok 媒体的 4 套内联 `switchCount`？ | 产品 + captain | §1.2 N1、附录A 阶段 | **不统一**（本次只做 `FailoverState` 那套）；4 套实现后续独立变更 |
| **Q2** | T2 冷却对池模式账号是否生效？池模式的"不标记状态"契约是否覆盖"短期避让"？ | 产品 / 运营 | §3.4.2、§6.1 | `cooldown.pool_mode_enabled=true`（论证见 §3.4.2：T2 不写 DB，不违约） |
| **Q3** | `max_total_upstream_attempts` 的推荐值（6 还是 8）？现网 11 账号的策略是否要保留给 VIP 分组？ | 运营 | §3.2.2 | 代码默认 `0`（关）；发布手册写"建议 6，VIP 分组用分组覆盖设 12" |
| **Q4** | 首批开启 T2 的分组是哪个？观察期多长？ | 运营 | 附录A 阶段 4 | 选 1 个低流量分组，观察 3 天 |
| **Q5** | `SemanticBytesWritten` 的实现位置：改造 SSE 写入函数（改动面大）还是用 `Writer.Size()` 减去 keepalive 累计字节（改动面小但脆弱）？ | t3 实现评审 | §3.6.2 | 优先"改造 SSE 写入函数"；若现有写入点过于分散（&gt; 15 处），退化到"减法"方案并在文档标注技术债 |
| **Q6** | `failover_exhausted_ratio` / `failover_switch_ratio` 按分组聚合所需的 `group_id` 来源：`ops_error_logs` 是否已有该列？ | t3 核实 | §3.8.3 | 若无列，则先只做全局维度，分组维度推迟 |
| **Q7** | 终局错误改为 `503`（B3）是否会影响现有客户端（有客户把 502 当"网关错误"重试、503 当"服务不可用"放弃）？ | 产品 + 客户成功 | §3.8.4、§7.3 | 保留 502（不改），仅补 `Retry-After` |
| **Q8** | 是否暴露 `failover.attempts` 给客户端（`expose_failover_detail`）？ | 产品 | §3.8.4 | 默认 `false` |
| **Q9** | `PingInterval` 与 `writerSizeBeforeForward` 的真实时序（recon §六-10 静态推断） | t3 运行时验证 | §3.6.2 | 若验证发现 ping 会跨越采样点 → **B1 的收益比预期更大**（现状是在误拦） |
| **Q10** | 是否需要 `accounts` 级 failover 策略覆盖（§5.4 最后一行）？ | 产品 | §5.4 | 先不做，分组覆盖跑稳后再评估 |
| **Q11** | Gemini 的 `max_same_account_retries_gemini=4` 是否要在文档里建议改 2？改默认值 vs 用预算层收敛？ | 运营 | §3.2.3 | 用预算层收敛（不改默认），文档给出建议值 |

---

## 附录 A：实施步骤（供后续任务拆解）

> 本阶段只做设计；以下步骤是给 t3/t4 的实施蓝图。每步都标注**可独立回滚**。

### 阶段 0：无行为变更的基础设施（1–2 天）

| 步 | 内容 | 产出 | 验证 |
| --- | --- | --- | --- |
| 0.1 | 新增 `GatewayFailoverPolicyConfig` 结构体 + `Validate()` + 全部默认值 | `config.go` | `go build`；默认值单测（30+ 断言） |
| 0.2 | 迁移 254（三列 `NULL`）+ `Group` 结构体字段 + repo 读写 | `migrations/254_*.sql`、`group.go`、`repo` | 迁移测试（仿 `ops_mgmt_migration_test.go:112`） |
| 0.3 | `resolveEffectiveFailoverPolicy` 纯函数 + `(value, source)` + 字段级 merge | `service/failover_policy.go` | 单测：account/group/global 三级 × nil/非 nil |
| 0.4 | `ClassifyUpstreamFailure` 纯函数 + 默认规则表 | `service/failover_classifier.go` | **参数化单测逐项对齐 4 处旧硬编码**（这是本阶段最重要的测试） |

**退出条件**：全部代码未被调用（无调用点），行为 100% 不变。

### 阶段 1：接线分类器（1–2 天，行为等价）

| 步 | 内容 | 验证 |
| --- | --- | --- |
| 1.1 | `gateway_forward.go:47-54` 改为调用分类器 | 现有 `gateway_pool_mode_retry_test.go` 必须过 |
| 1.2 | `antigravity_gateway_retry.go:780-787` 同上 | 现有 Antigravity 测试 |
| 1.3 | `gemini_messages_compat_service.go:1706-1713` 同上 | 现有 `gemini_error_policy_*_test.go` |
| 1.4 | `openai_gateway_upstream_errors.go:303` 同上（**只读不写**，OpenAI 行为不变） | 现有 OpenAI 测试 |
| 1.5 | 4 处硬编码删除，grep 断言无残留 | `rg "401, 403, 429, 529" backend/` 应只剩分类器 |

**退出条件**：13 个现有测试文件全绿；行为等价性由 F-01 参数化测试保障。

### 阶段 2：预算层（1–2 天，默认关闭）

| 步 | 内容 | 验证 |
| --- | --- | --- |
| 2.1 | `FailoverBudget` 结构 + `StartClock()` 接到首次 `Forward` 前 | 单测：未调用 StartClock 时 Deadline 不生效 |
| 2.2 | `HandleFailoverError` 加预算检查（步骤 6） | `failover_loop_test.go` 新增预算用例 |
| 2.3 | `StopClock()` 接到首个语义字节 | 流式测试 |
| 2.4 | `FailoverOutcomeError{Reason:"budget_exhausted"}` | 集成测试 |

### 阶段 3：可切换判据（2–3 天，**行为变更 B1**）

| 步 | 内容 | 风险 |
| --- | --- | --- |
| 3.0 | **t3 先做运行时验证**：`PingInterval` 是否跨越 `writerSizeBeforeForward`（Q9） | 决定 B1 的收益口径 |
| 3.1 | 找出全部 SSE 写入点（`rg "Flush\(\)" backend/internal/service backend/internal/handler`） | 若 &gt; 15 处则走 Q5 的减法方案 |
| 3.2 | 抽出 `writeSemanticSSE(...)` 包装 + 计数器 | 逐点迁移，每点单独提交 |
| 3.3 | `gateway_handler.go:1060-1064` / `:523-526` 改用计数器 | `gateway_handler_stream_failover_test.go` 必须过（**这是硬约束测试**） |
| 3.4 | 保留 `use_semantic_bytes=false` 回退路径 | 两条路径都要有测试 |

### 阶段 4：T2 冷却层（2–3 天，默认关闭）

| 步 | 内容 | 验证 |
| --- | --- | --- |
| 4.1 | Redis 三个 Lua 脚本（只延长 / 计数+TTL / 探测）+ Go 封装 | 用真实 Redis 的集成测试（仿 `billing_inflight_reservation_test.go`） |
| 4.2 | 选号插入 MGET 批量读（`gateway_scheduling.go:652` 前）+ fail-open | 单测：Redis 挂掉时返回全量候选 |
| 4.3 | 失败路径写入（记账顺序 a→b→c→d） | `failover_loop_test.go` 新增 |
| 4.4 | "多账号同败不冷却"（F-01）+ 半开探测 | 专项单测 |
| 4.5 | `SetTempUnschedulableIfLater` + 两个调用点改造（G10） | `antigravity_gateway_retry` 测试 |

### 阶段 5：观测与终局（1–2 天）

| 步 | 内容 |
| --- | --- |
| 5.1 | `failover.attempt` / `failover.outcome` 日志（替换 `gateway.failover_switch_account`） |
| 5.2 | `upstream_errors` 新增 `kind="failover_attempt"`；**同步改 `ops_metrics_collector.go:572` 的 `IN` 列表（检查点 C-03）** |
| 5.3 | 5 个新指标 |
| 5.4 | `FailoverOutcomeError` + `Retry-After` 公共函数（Anthropic 路径补齐） |
| 5.5 | 2 个新 alert `metric_type`（两处 switch 各加 case） |

### 阶段 6：文档与发布

- 更新 `config.yaml.example`（全部新键注释）
- 发布手册：能力开启顺序（`record_chain` → `clear_sticky` → `cooldown`（1 分组 3 天）→ `budget`）
- 回滚手册：三个开关

**实施检查点（必须逐项确认，否则视为未完成）**

| ID | 检查点 |
| --- | --- |
| F-01 | 分类器默认规则表与 4 处旧硬编码**逐项等价**（参数化测试） |
| F-02 | `cooldown.enabled=false` 时，代码路径**不执行任何 Redis 调用**（无 RTT） |
| F-03 | `budget.total_timeout=0` 时，行为与旧版逐位一致 |
| C-01 | `SemanticBytes` 每请求独立（并发测试） |
| C-02 | Redis 全挂时系统退化为现状（不返回 503） |
| C-03 | `upstream_errors` 新增 kind 后，`account_switch_count` 口径不断裂 |
| C-04 | 迁移 254 在旧版本代码上可安全存在（三列 NULL 被忽略） |

---

## 附录 B：对 recon 的补充与修正（本设计核实）

| # | recon 原文 | 本设计核实结果 |
| --- | --- | --- |
| B-1 | G9「无切换专项告警与分组维度……告警规则仅 error_rate/upstream_error_rate（`ops_alert_evaluator_service.go:606-615`）」 | **不准确**。`evaluateRule` 第一处 switch（`:447-575`）已支持 `group_available_accounts`(`:463`)、`group_available_ratio`(`:478`)、`group_rate_limit_ratio`(`:524`)、`account_error_ratio`(`:539`)、`account_temp_unscheduled_count`(`:512`)、`overload_account_count`(`:555`)。**分组级账号池健康告警今天就能配，不需要开发**。真正缺的只有 failover 专项切换率/耗尽率 |
| B-2 | §2.3「不存在'同组下一个账号'的稳定定义」 | **确认**。补一条设计含义：因此本设计**不引入游标**，改用 switch_chain 记录换取可解释性（§3.1.2） |
| B-3 | §六-3「是否需要对所有账号类型统一写库」 | 本设计**绕开**这个决策：T2 统一生效且不写库，T3 保持现状（池模式不写）。因此不需要产品先拍"统一写库"（§3.4.2） |
| B-4 | §五-5.2 `temp_unschedulable_rules` 的 `Scope` 取值 | 补全：`account` / `model`（`account.go:136-139`），空值时按"模型已知且非 401 走模型级"推断（`account.go:146-148`） |
| B-5 | 附录 B 常量表 | 补充：`maxRetryAttempts=5`、`retryBaseDelay=300ms`、`retryMaxDelay=3s`、`maxRetryElapsed=10s` 属**服务层原地重试**（`gateway_forward.go:25-33`），与 handler 层 `FailoverState` 是两套，本次不改 |
| B-6 | §1.2 步骤 10「记录 `writerSizeBeforeForward`」 | 补：`shouldFailoverUpstreamError` 与 `shouldRetryUpstreamError` 是**两个不同函数、不同语义**（`gateway_forward.go:36-54`）：前者决定"是否换号"，后者决定"是否在同账号重试"（OAuth 仅 403 重试；API-key 未配置错误码才重试）。分类器必须**分别建模**，不能合并 |
| B-7 | 迁移编号 | `backend/migrations` 当前最大为 `253_ops_mgmt.sql`（已核实），本次用 **254** |

---

## 附录 C：与现有测试的对齐（13 个文件 → 本设计的约束）

| 测试文件 | 本设计必须保持的约束 | 需要新增的用例 |
| --- | --- | --- |
| `handler/failover_loop_test.go` | 同账号重试不进排除集（`:470`）；切换计数语义；503 退避；取消 | 预算耗尽、`max_same_account_attempts_total` |
| `handler/failover_loop_profit_veto_test.go` | 利润否决留在排除集；`maxProfitVetoAttempts` | `max_selection_retries` 护栏 |
| `handler/gateway_handler_stream_failover_test.go` | **写字节后必须放弃换号**（`:26-140`） | 语义字节判据（keepalive 可切 / 首 chunk 不可切）**——这是 B1 的验收测试** |
| `handler/gateway_handler_error_fallback_test.go` | 错误兜底与终局响应 | `FailoverOutcomeError` 聚合；Q7 决定后调整 |
| `service/gateway_pool_mode_retry_test.go` | 池模式同账号重试与状态码集合 | `same_account=inherit` 等价性 |
| `handler/openai_gateway_credential_failover_test.go` | 凭据失败换号 + `Retry-After` 复制 | 抽公共函数后仍过 |
| `service/gateway_account_selection_test.go` 等 | 优先级/LRU/粘性/排除集/平台过滤/混合调度 | 冷却过滤器加入后的选号（Redis mock） |
| `service/gateway_service_selection_failure_stats_test.go` | 选号失败原因统计 | 冷却导致的 `excluded` 计数归入新原因 |
| `service/gateway_channel_restriction_fallback_test.go` | 渠道限制降级 | 不变 |
| `handler/openai_gateway_ws_failover_resume_test.go` | WS 换号恢复 | 不变（N1 范围外） |
| `service/scheduler_snapshot_hydration_test.go` | 快照 hydrate | 不变（不动快照） |
| `service/gemini_error_policy_*_test.go` | Gemini 跳过写库 / 同账号重试次数 | 分类器等价性 |
| `service/openai_first_output_timeout_test.go` | 首字节超时换号 / 每请求 1 次上限 | L1 推广到 Anthropic 时复用 `SemanticBytes` |
| `service/gateway_upstream_transport_error_test.go`（recon 未列，但关键） | **客户端取消不标记（`:182`）、上游 deadline 不标记（`:203`）、瞬态错误不标记（`:68`）** | 分类器的 `Canceled` 优先级 |

> 最后一行是 recon 附录 A 遗漏的关键测试：它锁定了"网络层错误不能一概归因于账号"的三条硬规则。分类器的判定**顺序**（`Canceled` → `BoundUpstreamPassthrough` → `RetryableOnSameAccount` → `RequestScopedTransient` → 状态码）正是为了不破坏它。
