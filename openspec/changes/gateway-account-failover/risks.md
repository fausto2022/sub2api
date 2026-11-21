# 网关账号级 failover · 对抗性风险审查（risks）

- 变更：`gateway-account-failover`
- 阶段：t3 风险审查（只读代码，未修改任何实现）
- 输入：`openspec/changes/gateway-account-failover/recon.md`（t1）+ 当前工作树源码
- 立场：**找出它会怎么坏**，不是"它应该怎么做"。缓解建议只给约束与方向，不替代 t2/t4 的方案设计

---

## 一、审查范围与方法

### 1.1 审查对象

| 层 | 被审对象 | 审查重点 |
| --- | --- | --- |
| 现有实现 | `FailoverState` 状态机、4 套并行 failover 循环、写字节守卫、终局错误映射 | 现有行为在边界条件下是否已经危险（新设计会继承这些缺陷） |
| 拟议方向（来自 recon §四/§六） | 可配置失败判定规则、跨请求失败隔离、请求级预算、幂等/去重、告警维度 | 每个方向"照最直接的做法实现"会在哪里炸 |
| 交互面 | 计费幂等表、账号状态写库、调度快照、利润门（opsmgmt）、ops 指标/告警 | 改一处是否会在另一处产生不可见后果 |

### 1.2 方法

1. **逆向追踪"不可切换"判定点**：先枚举所有"不允许换号"的位置（写字节守卫、`NextAccountStop`、缓存绑定、客户端断开），再看每个判据的**事实依据是否唯一且一致**。
2. **触发链推演**：对每个风险域构造最小触发序列（并发数 × 状态码 × 时机），并标注该序列在产品里是否常见。
3. **证据纪律**：每条风险至少一个 `文件:行` 或测试名，行号取自本轮 `read`/`grep` 实际输出；无法静态确定的量化结论标 `[估算]`。
4. **交叉校验前序文档**：逐条复核 recon 的关键论断，**推翻 3 条、修正 1 条**（见 1.4）——被继承的错误结论比遗漏更危险。
5. **不做的事**：不执行 `go build/test`、不起服务、不连 DB/Redis。本阶段只做设计（任务契约），因此所有涉及运行时行为的结论都标注为静态推断。

### 1.3 严重度定义（本文档口径）

| 级别 | 含义 | 处置 |
| --- | --- | --- |
| **blocker** | 会让"设计目标"直接失效，或造成用户可见的数据/账务错误；**设计必须显式裁决**，否则不得进入实现 | 上线前必须闭合 |
| **high** | 会造成成本泄漏、可观测性缺失、状态机退化；可在实现期修复，但必须有测试钉死 | 上线前必须有对应测试 |
| **medium** | 影响排障效率、日志/存储成本、配置易用性 | 排期修复 |
| **low** | 边界很小或已有兜底 | 记录即可 |

### 1.4 对 recon 的复核结论（4 条）

| # | recon 原结论 | 本轮复核 | 影响 |
| --- | --- | --- | --- |
| 1 | §六 Q10：`PingInterval` 与 `writerSizeBeforeForward` 时序可能导致 `Writer.Size()` 误判，需 t3 实测 | **静态可判定为不成立**。ping 只在下游同一 goroutine 的 `select` 里写（`backend/internal/handler/gateway_helper.go:417-431`），ticker 由 `defer pingTicker.Stop()` 关闭（`:394-400`）；`writerSizeBeforeForward` 在**取槽位之后**采样（`gateway_handler.go:919`），因此不存在跨采样点的异步 ping 写者。真正的问题不是 ping，而是**总字节数无法区分"心跳字节"与"语义字节"**（见 R-02） | 该问题可关闭；替换为 R-02 |
| 2 | 缺口 G10：并发失败时较长的冷却窗口可能被 `SetTempUnschedulable(now+1m)` 覆盖为 1 分钟 | **不成立**。`SetTempUnschedulable` 的 SQL 带"只延长"守卫：`AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until < $1)`（`backend/internal/repository/account_repo.go:2524-2533`），更长窗口不会被缩短 | G10 降级；真实风险改为**写放大**（R-16）与**reason 不更新**（R-36） |
| 3 | 缺口 G4 表述为"无幂等去重" | **不准确**。扣费幂等**存在且很强**：`usage_billing_dedup` 以 `(request_id, api_key_id)` 唯一（`backend/migrations/071_add_usage_billing_dedup.sql:12-13`），请求 id 优先取客户端 id（`backend/internal/service/gateway_usage_billing.go:226-233`）。缺的是**上游调用幂等**（网关路由无 `Idempotency-Key`，`backend/internal/server/routes/gateway.go:180-189`）与**成本记账**（见 R-10/R-11） | 缓解方案完全不同：不要"再加一层去重"，要"分开扣费键与成本键" |
| 4 | 4 套并行 failover 实现需收敛 | 规模被低估：`grep '&UpstreamFailoverError{'` 在 `backend/internal/service` 命中 **89 处**（含约 20 处测试构造），非测试构造点约 **69 处** [估算]。真正的收敛成本在 **69 个"错误语义构造点"**（各自带 `Scope`/`Stage`/`NextAccountAction`/`SafeToFailoverAfterWrite`），不只是 4 个循环 | 直接决定 R-48 与取舍 V8 |

### 1.5 局限

- 未验证 DB/Redis 在并发下的实际行为（如 `scheduler_outbox` 写入速率、`usage_billing_dedup` 的锁竞争），相关结论标注为静态推断。
- 未跑 `-race`；并发相关结论基于单写者/`sync.Once`/SQL 原子性阅读。
- 行号基于当前工作树，若实现前代码变动需重新核对。

---

## 二、风险清单

> 证据列一律给"文件相对 `backend/internal/` 的路径:行"。`[估算]` 表示结论由常量/配置推算，未实测。

### 2.1 幂等与半截响应（R-01 ~ R-09）

| ID | 风险 | 触发条件 | 影响 | 证据 | 严重度 | 缓解 |
| --- | --- | --- | --- | --- | --- | --- |
| **R-01** | **"已写字节"存在两套互斥定义**：handler 用"相对本次 Forward 起点的字节增量"，service 用"进程内是否写过任何字节"。排队期 ping 一旦落盘，service 的 mid-stream 换号路径**永久失效**，而 handler 仍认为可切换 | 流式请求 + 等槽位超过 `concurrency.ping_interval`(10s) + 上游在首字节前断流（HTTP/2 GOAWAY、连接 reset） | 同一请求在两层得到相反结论：service 走 `sendErrorEvent` 硬失败，handler 的守卫却允许继续换号。**可恢复错误（换号即可成功）变成用户可见失败**，且行为随"是否排过队"漂移，无法复现 | 需求侧：`gateway_handler.go:919`（采样点）、`:1061`（`Size()` 增量判据）；服务侧：`service/gateway_upstream_response.go:1066`（`if !c.Writer.Written()`）、`:1081-1082`（否则 `sendErrorEvent`）；ping 写入：`handler/gateway_helper.go:426-431`、`handler/user_msg_queue_helper.go:117-122` | **blocker** | 统一为**一个**判据结构体（如 `UpstreamWriteState{SemanticBytes, HeartbeatBytes}`），由 Forward 入口注入、两层共读；禁止任何路径直接读 `c.Writer.Written()` 做换号决策 |
| **R-02** | **字节总量无法区分语义/心跳**：`writerSizeBeforeForward` 把 ping 计入基线，因此"已写 200 头 + ping"与"已写半个正文"在判据上等价 | 排队期 ping 后上游失败；或未来给 Anthropic 引入"首字节语义超时"/staging | 若按"增量>0 即禁止换号"，则 ping 也禁止（R-01 的另一半）；若按"增量>0 即可换号"，则**半截内容拼接**（前账号半段 + 后账号完整段）。二者只能选一个，现状是两层各选了一个 | `gateway_handler.go:919`、`:1061`；`gateway_helper.go:419-424`（ping 落地即提交 200）；`service/gateway_upstream_response.go:1102-1116`（Anthropic 逐事件直写，无 staging） | **blocker** | 引入"语义字节"计数（只在写 message_start/content_block_delta 等语义事件时累加），换号判据改为 `semanticBytes == 0`；ping/keepalive 不计入 |
| **R-03** | **流式中断后只补一条 error 帧，不补协议终止事件**：Anthropic 路径 flush 过语义内容后，失败只能以 `data:{"type":"error",...}` 结束 | 上游 200 后中断（长思考、网络抖动、上游限流） | Anthropic SDK 视 SSE error 为流中断，**客户端往往整条重试** → 真实重复上游调用（我方承担），并在客户端侧重复展示半截内容；且无 `message_stop`，部分 SDK 会报解析异常 | `gateway_handler.go:1060-1064` → `:1992-2021`（SSE error 帧，无 `message_stop`）；对照 OpenAI 侧有 `writeResponsesFailedSSE` 终止事件（`:2002-2006`） | high | 终局补协议合法终止事件（Anthropic 补 `message_stop` + `stop_reason`），或至少写入"已产生内容"标记供客户端决策；不得靠"客户端会自己重试" |
| **R-04** | **同一 SSE 流可能追加两个 error 帧** | service 已 `sendErrorEvent`（流内读错误），handler 又走 `ensureForwardErrorResponse` 兜底 | 客户端收到 2 个 error 帧；部分 SDK 在第一个帧后关闭连接，第二个帧丢失或引发二次解析错误；日志上表现为"同一请求两次上游失败" | `service/gateway_upstream_response.go:1081-1082`；`gateway_handler.go:1080-1084`、`:2033-2049`（`IsResponseCommitted` 未在流内错误路径设置）、`:2061-2074`（`gatewayForwardErrorAlreadyCommunicated` 对 `text/event-stream` 恒定返回 false） | high | 流内错误必须 `MarkResponseCommitted`（`service/ops_upstream_context.go:73`），或由 Forward 返回结构化"已写终止帧"标志 |
| **R-05** | **网关路由无调用级幂等**：换号 = 真实重复上游调用，且无法审计关联 | 任意换号 | 一次用户请求在上游产生 N 次真实计费调用；无法按 `request_id` 聚合"本次打了几次上游" | `server/routes/gateway.go:180-189`（中间件链：bodyLimit/clientRequestID/opsErrorLogger/endpointNorm/apiKeyAuth/groupModelAllowlist/compositeTarget/requireGroupAnthropic，**无幂等**）；`handler/idempotency_helper.go` 仅服务管理端 | high | 不为网关加 `Idempotency-Key` 拦截（见取舍 V6），而是把"每次尝试"显式记为审计事件（`attempt_index`），使重复调用可见、可计量 |
| **R-06** | **`SafeToFailoverAfterWrite` 的成立前提在 Anthropic 路径不存在**：该字段语义依赖"首输出前 staging 缓冲" | 把 OpenAI 的"写后可切换"能力复用到 Anthropic | 一旦复用，`SafeToFailoverAfterWrite=true` 会在**已 flush 语义内容**时放行换号 → 半截拼接（当前 Anthropic 无 staging，见 R-02） | 字段定义 `service/gateway_service.go:707`；staging 实现（仅 OpenAI）`service/openai_gateway_response_handling.go:255-285`、`:419-449`；Anthropic 无等价物（`service/gateway_upstream_response.go:1102-1116` 直写） | medium | 该字段只在"有 staging 且 `firstOutputProgressObserved == false`"时允许为 true；Anthropic 路径若要用，必须先实现 staging（见取舍 V1） |
| **R-07** | **WebSocket 换号恢复只覆盖"下一轮 payload"，不覆盖"上一账号已产出的字节"** | WS 会话在已收到部分事件后换号 | 客户端在同一 WS 会话里看到账号 A 的部分输出 + 账号 B 的完整输出；现有测试只断言 payload 克隆，不断言"无重复输出" | `handler/openai_gateway_ws_failover_resume_test.go:9-19`（只测 `openAIWSNextAttemptMessage` 的 payload 选择与克隆）；`:28-35`（首轮失败保留初始 payload） | medium | WS 换号仅允许在"本轮未产出任何语义事件"时进行；否则以 `response.failed` 结束本轮，由客户端发起新一轮 |
| **R-08** | **客户端断开后上游 detach 请求仍在计费**，而新设计若允许"断开后换号"会重复放大 | 客户端 499/取消 + 上游仍在跑 | 无人接收的响应持续消耗上游额度；换号再叠一层 → 成本非线性放大 | `handler/failover_loop.go:203-205`（ctx 取消即 `Canceled`）、`:327-347`（注释明确"上游 detach 的在途请求照常完成计费，但不再为无人接收的响应启动新的上游尝试"）；detach 调用点 `service/gateway_forward.go:587` | high | 保持"ctx 取消即终止换号"的硬约束；预算/重试逻辑必须在 `sleepWithContext` 类函数上响应取消（现状已满足，勿改坏） |
| **R-09** | **Gemini 显式缓存绑定禁止换号，但该"禁止"不可观测** | 显式缓存绑定请求 + 上游失败 | 排障时无法区分"换号失败"与"本来就不允许换号"；若新规则默认允许换号，会破坏 cache 绑定语义（换号后 cache 命中归零，成本上升且用户计费口径变化） | `handler/gemini_v1beta_handler.go:571-585`（`maxAccountSwitches=0`，无日志/指标）；recon §1.4 已记录 | medium | 换号被"策略性禁止"时必须落结构化事件（`failover_suppressed{reason="cache_bound"}`），并纳入 R-39 的切换链 |

### 2.2 重复计费与账单完整性（R-10 ~ R-15）

| ID | 风险 | 触发条件 | 影响 | 证据 | 严重度 | 缓解 |
| --- | --- | --- | --- | --- | --- | --- |
| **R-10** | **扣费幂等键让"换号产生的额外上游成本"在账上不可见** | 任意换号（上游对失败尝试也计费，如 200 后中断、429 前已入队） | 一个客户端请求最多一条账单（`(request_id, api_key_id)` 唯一），重复上游消耗**既不计费也不留痕**。设计目标里的"成本预算"缺少数据源，且无法回答"换号是否省钱" | `backend/migrations/071_add_usage_billing_dedup.sql:12-13`（唯一索引）；键解析 `service/gateway_usage_billing.go:218-238`（客户端 id 优先）；写入 `repository/usage_billing_repo.go:69-90` | **blocker** | 成本记账必须用**另一张表/另一套键**（如 `(request_id, attempt_index)` 的 `upstream_cost_attempts`），绝不复用扣费幂等键（见取舍 V2） |
| **R-11** | **一旦允许"写字节后切换/重放"，第二条账单会以 `ErrUsageBillingRequestConflict` 直接失败** | 客户端带 `X-Request-Id` + 允许写后换号 + 两次尝试的 body 被改写（渠道映射/Bedrock CC 兼容/模型替换） | 指纹不同 → 冲突错误 → **计费失败（漏记）**，用户白拿、我方承担上游成本；同时产生 `gateway.record_usage_failed` 噪音，被误判为故障 | 冲突判定 `repository/usage_billing_repo.go:86-88`；指纹来源 `handler/gateway_handler.go:944`（`HashUsageRequestPayload(attemptParsedReq.Body.Bytes())`）；body 按尝试改写 `:894-906` | **blocker** | 换号/重放前必须冻结"计费指纹"（用首次尝试的原始 body），或让重放走独立计费键；两者择一并在设计中写死 |
| **R-12** | **失败尝试 `result=nil` 不入账，形成成本黑洞** | 上游已计量但本地无 usage（429 排队、首字节前断流、超时） | 上游成本 100% 由我方承担且**无任何记录**；没有"本次请求打了几次上游"的字段，无法验证容灾策略的经济性 | `handler/gateway_handler.go:1104-1111`（注释：failover 错误恒定 `result=nil`，不会重复计费）；`service/gateway_forward.go:682-717`（failover 分支直接 `return nil, &UpstreamFailoverError{}`） | high | 与 R-10 同一个修法：独立成本事件表 + 每尝试一条（含 status、耗时、是否进入计费） |
| **R-13** | **"部分流 usage 入账"与"后续重放"叠加时会静默吞掉完整账单** | 先入账一条 partial usage（#5148 路径），之后允许重放并再次入账 | 第二次写入命中 `ON CONFLICT DO NOTHING` 且指纹相同 → `return false, nil`（静默跳过，无日志） → 账单停留在较小的 partial 值，**漏记且无告警** | `handler/gateway_handler.go:937-939`、`:1107-1111`；partial usage 构造 `service/gateway_upstream_response.go:685`；静默跳过 `repository/usage_billing_repo.go:77-89` | high | 重放路径必须显式"结平/替换"前一条 billing（upsert 语义），并在跳过时打结构化 warn（附 `request_id`/`attempt_index`） |
| **R-14** | **usage 提交是有界 worker 池异步任务，换号放大提交次数即放大丢失面** | 进程退出、队列满、DB 抖动 | 账单丢失且无重试证据；换号把 1 次提交放大为 N 次 | `handler/gateway_handler.go:961-995`（`submitUsageRecordTask`）；失败仅 `Error("gateway.record_usage_failed")`（`:986-994`） | medium | 计费任务需要可观测的丢弃计数（prometheus counter），并在设计中明确"丢弃即漏记"的 SLO |
| **R-15** | **"计费完整性"问题被当成"账号故障"触发换号** | Grok 上游返回 200 但无 billable usage | 换号后同样拿不到 usage（问题不在账号），却真实多打一次上游；真实原因（上游协议变化）被掩盖 | `service/openai_gateway_usage_integrity.go:19-24`、`:46-82`（构造 `StatusCode=502` 的 `UpstreamFailoverError`）；`:12-13` 错误码 `grok_missing_usage` | medium | 该类错误应标记 `NextAccountAction=NextAccountStop` 或独立 `Stage=GatewayFailureStageProtocol`，且**不得**参与账号健康统计 |

### 2.3 并发安全（R-16 ~ R-23）

| ID | 风险 | 触发条件 | 影响 | 证据 | 严重度 | 缓解 |
| --- | --- | --- | --- | --- | --- | --- |
| **R-16** | **一次 529 引发 O(N) 次写库 + O(N) 次调度快照重建（写放大/惊群）** | 单账号并发 N 个请求同时收到 529 | `handle529` 每个请求都 `SetOverloaded` → 1 次 UPDATE + 1 条 `scheduler_outbox` + 1 次 `syncSchedulerAccountSnapshot`。N=100 时即 100 次写 + 100 条 outbox + 100 次快照同步，与"一次上游抖动"完全不成比例；outbox 反压还会触发全量重建 | `service/ratelimit_service.go:2018-2055`（`handle529`）；`repository/account_repo.go:2509-2522`（无幂等/无"只延长"守卫，直接 `SetOverloadUntil`） | high | 账号状态写库改为"Redis 先写 + 异步合并落库"或"带条件更新 + 单飞（singleflight）"；同一账号同一窗口内只允许一次落库 |
| **R-17** | **账号状态标记在 service 层，与"是否真的换号"解耦** | 任意 `>=500/401/403/429/529` 上游错误 | `Forward` 一返回就 `HandleUpstreamError`，即使 handler 随后因**已写字节**、`NextAccountStop`、客户端断开而**放弃换号**，标记照样落库。**这意味着"可配置的失败判定规则"在 handler 层实现时无法抑制副作用** —— 规则说不换号，账号已经被摘掉 | `service/gateway_upstream_response.go:550-557`（`handleFailoverSideEffects`）；调用点 `service/gateway_forward.go:729`、`:796`；handler 侧放弃换号 `handler/gateway_handler.go:1060-1064` | **blocker** | 判定与副作用必须同源：由**一个**判定器产出 `{switch?, mark?, cooldown?}` 三元决策，副作用只在该决策之后执行；禁止在 Forward 内无条件标记 |
| **R-18** | **同账号重试把标记/写库放大 `pool_mode_retry_count + 1` 倍** | 池模式账号 + 可重试状态码（默认 `[401,403,429]`） | 每次同账号重试都完整走一遍 `Forward` → 一次请求最多 4 次 `HandleUpstreamError`（默认 3 次重试）→ 4 次写库/outbox | `handler/failover_loop.go:220-234`（同账号重试 `continue`）；`service/account.go:1242-1265`（默认 3，上限 10）；`service/ratelimit_service.go:333-398` | high | 同一账号在同一请求内的重复标记必须幂等（进程内 + Redis 去重），或改为"重试耗尽后再标记一次" |
| **R-19** | **同账号重试与单账号退避无抖动 → 同步重试风暴** | 一次 429 让同组 N 个并发请求同时失败 | 所有请求在 +500ms 同一时刻重试同一账号；单账号 503 退避则全体 +2s 齐步重试。上游看到的是"失败的放大版"，容易把 429 升级成 5xx/封禁 | 固定延时：`handler/failover_loop.go:38`（`sameAccountRetryDelay=500ms`）、`:58-77`（无 jitter）、`:45`（`singleAccountBackoffDelay=2s`）、`:303-305`；对照有 jitter 的实现：`handler/gateway_helper.go:458-474`（±20%）、`service/antigravity_gateway_retry.go:829-848`（±20%） | high | 所有换号/重试延时加 jitter（建议 ±20%），并对同账号重试做"账号级令牌桶"限速 |
| **R-20** | **`TempUnschedulable` 的"只延长"守卫让标记变更静默失效** | 同账号已有更长冷却，再次标记 | UPDATE 的 `WHERE ... < $1` 使 `RowsAffected=0` → **提前 `return nil`**：不写 outbox、不同步快照、不更新 reason。调用方无法区分"标记成功"与"被守卫吞掉"，冷却原因长期停留在旧值 | `repository/account_repo.go:2524-2533`、`:2541-2543`；对照 `SetRateLimitedIfLater` 的做法（`:2343-2370`，`updated==0` 时会 `syncSchedulerAccountSnapshot`） | high | 返回值应区分 `extended / no-op / shortened-rejected`，并在 no-op 时仍刷新快照；reason 改为"最长窗口对应原因" |
| **R-21** | **`model_rate_limits` 单键原子但无合并** | 并发失败命中同一 model scope | `jsonb_set` 保证不丢更新，但每个请求各写一次 + 各发一条 outbox + 各同步一次快照 | `repository/account_repo.go:2478-2506` | medium | 同 R-16：合并写/延迟落库 |
| **R-22** | **换号路径的槽位释放没有任何测试** | 换号（`continue`）时槽位/等待计数释放 | `wrapReleaseOnDone` 用 `sync.Once`（幂等已保证），但现有唯一测试只覆盖"hydrate 失败释放槽位"2 例；**换号路径若回归，表现为账号并发槽位缓慢泄漏（请求量大的账号先被限流）**，极难定位 | `handler/gateway_helper.go:188-202`（`sync.Once`）；释放点 `handler/gateway_handler.go:933-935`；测试仅 `service/gateway_scheduling_slot_release_test.go:14-50`（2 例） | medium | 补"换号 N 次后槽位计数回到基线"的集成测试（见 T-10） |
| **R-23** | **换号路径上的会话释放使用无超时 `context.Background()`** | 每次换号 | 若 Redis/DB 抖动，`ReleaseAccountSession` 串行阻塞在换号热路径上，直接叠加到用户首字节延迟 | `handler/gateway_handler.go:1068-1070`、`:1049-1051`、`:822-824` | medium | 换号清理改为带超时的异步清理队列（失败可重试），不与换号主流程串行 |

### 2.4 超时叠加（R-24 ~ R-29）

| ID | 风险 | 触发条件 | 影响 | 证据 | 严重度 | 缓解 |
| --- | --- | --- | --- | --- | --- | --- |
| **R-24** | **没有请求级 failover 总预算**：最坏耗时 = 尝试次数 × 单次超时，理论可达 110 分钟 | 上游整体不可达（DNS 污染/路由异常/全池挂起） | 单次尝试受"建连 10s + TLS 10s + 等响应头 300–600s"约束，尝试上限 = `MaxSwitches+1` = 11 → **理论上限约 110 分钟** [估算]。客户端早已超时并重发 → 重复请求 + 重复上游成本；服务端 goroutine/连接被长期占用 | `handler/failover_loop.go:245-250`（尝试上限 = 值+1）；`config/config.go:2523`（`response_header_timeout=600`）、`:3488`（只校验非负）；兜底 300s `repository/http_upstream.go:56-58`；建连/TLS 超时 `:65`、`:70`；**代码自己承认该风险**：`repository/http_upstream.go:60-64` 注释"多账号故障转移是串行的，一次请求会阻塞数分钟且不写中间错误" | **blocker** | 必须引入 `failover_total_budget_ms`（建议默认 90–120s）与"单次尝试预算 = min(剩余预算, 单次超时)"；预算耗尽的终局错误必须是**明确的** 504/503 + `attempts` 汇总 |
| **R-25** | **尝试数被三层乘算，最坏 220 次上游调用** | OAuth 账号 403（service 层固定重试 5 次）× 同账号重试 × 账号切换 | 一次用户请求最多把 220 次上游调用打出去 [估算：11 账号 × (1+3 同账号重试) × 5 service 重试]，成本放大两个数量级；日志按同比例爆炸 | `service/gateway_forward.go:25-33`（`maxRetryAttempts=5`、`maxRetryElapsed=10s`）、`:619-660`（重试循环）；`:36-44`（OAuth 仅 403 重试）；`handler/failover_loop.go:220-234`；`service/account.go:1242-1265` | high | 三层预算必须在设计里合并成一个"总尝试数/总耗时"口径；`pool_mode_retry_count × maxRetryAttempts` 必须显式封顶 |
| **R-26** | **Gemini 乘算最坏 20 次上游调用** | Gemini 分组 + `max_account_switches_gemini=3` | 3 次切换 × 每账号服务内 5 次 = 20 次；叠加退避可达分钟级，且 Gemini 客户端（SDK）超时通常远小于此 | `config/config.go:2534`（默认 3）；`service/gemini_messages_compat_service.go:1678-1684`（每账号 `MaxSameAccountRetriesGemini+1`）；recon §1.6 | high | Gemini 路径必须单独纳入总预算，不能只依赖 `max_account_switches_gemini` |
| **R-27** | **"预算耗尽"没有语义**：到达上限即返回"最后一个错误"，不区分"真没账号"与"预算用完" | 混部池（部分账号 429、部分 5xx）+ 上限用尽 | 用户看到 `Upstream service temporarily unavailable`（`mapUpstreamError` 对 5xx 的统一文案），既不知道是"全部 429 请退避"还是"全部 5xx 请重试"，也没有 `Retry-After` | `handler/gateway_handler.go:245-247` → `:1921-1961`、`:1970-1985`（无 attempts、无聚合、无 `Retry-After`）；对照 OpenAI 侧 `copyFailoverRetryAfter` | high | 终局错误必须带 `attempts`、`last_status`、`exhausted_reason`（budget/accounts/stream_started/client_gone）与聚合后的 `Retry-After`（取所有尝试中最小 reset） |
| **R-28** | **"不推进预算"的重试源是活锁温床** | 503 退避分支清空排除集 + 任何新增的"不消耗 SwitchCount"的重试源 | 历史 bug（#4925）已证明：利润否决不推进预算 + 503 退避清空排除集 = 每 2s 空转。现状只给利润门加了独立上限 `maxProfitVetoAttempts=10`，**新规则若不自觉，会重现同类活锁** | `handler/failover_loop.go:276-319`（清空排除集 `:310-315`）、`:46-51`（利润门独立预算）、`:161-172`；已钉死回归：`handler/failover_loop_profit_veto_test.go:64-82`、`:84-100`、`:102-118` | high | 设计必须规定：**任何**重试源都要么推进统一预算，要么有独立上限，并在测试里对"不推进预算 + 清空排除集"的组合做穷举 |
| **R-29** | **Anthropic 路径没有首字节语义超时，长思考请求 600s 内不会换号** | 上游"接受连接但不产出"（半死状态） | 用户等待到 600s 才失败；期间无法切换到健康账号。OpenAI 侧有该能力但**默认关闭**且每请求只允许 1 次 | `config/config.go:2523`（600s）、`:2527`（`openai_first_output_timeout_seconds` 默认 0=关闭）、`:1069`；`handler/openai_gateway_handler.go:3993-4002`（每请求 1 次） | medium | 若引入，必须同时满足 R-02 的"语义字节"条件（否则会切出半截内容）；建议先在"未写任何语义字节"范围内启用 |

### 2.5 账号状态误标与恢复（R-30 ~ R-36）

| ID | 风险 | 触发条件 | 影响 | 证据 | 严重度 | 缓解 |
| --- | --- | --- | --- | --- | --- | --- |
| **R-30** | **单请求即可把账号标记为不可用**（账号级、无观测期、无梯度） | 一个请求命中 429/529/401 等 | 一次抖动 → 账号立即掉出池（429 写 `rate_limit_reset_at`、529 写 `overload_until` 默认 10 分钟；401/403 走 `HandleUpstreamError` 之后的账号异常分支）。**没有"连续失败 N 次才降级"**，也没有"半开探测" | `service/ratelimit_service.go:333-398`（`HandleUpstreamError` 主流程）、`:358-365`（529）、`:375-390`（Anthropic 429 窗口）、`:2018-2055`（529 冷却）；标记入口 `service/gateway_upstream_response.go:550-557` | **blocker** | 引入失败计数/滑动窗口（Redis），阈值内只"降权"不改状态；状态升级需 N 次或跨请求多次证据 |
| **R-31** | **池模式默认不标记，与"坏账号跨请求隔离"直接矛盾** | 池模式账号（`pool_mode=true`）任意失败 | 坏账号**永远不摘**，只能靠"请求内排除集"躲一次；并发请求各自踩同一个坑。用户感知为"这个分组时好时坏" | `service/ratelimit_service.go:342-349`（`pool_mode_error_skipped` 后 `return false`）、`:311-319`（`CheckErrorPolicy` 同语义）；契约来源 `service/account.go:1228-1240` 注释 | **blocker** | 必须区分两种"池模式"语义：**"不写账号状态"**（上游是共享池，账号无独立健康含义）vs **"不隔离"**（当前实现）。设计要求显式配置：`isolation_mode: mark \| observe \| none`，默认 `observe`（计数不摘除） |
| **R-32** | **恢复路径不对称，且缺少"被标记后一定恢复"的保证** | 各种标记组合 | 写库的标记有 TTL 与手动清除（`ClearTempUnschedulable`）；**不写库的"排除"没有恢复问题（也永远不隔离）**；并且没有"连续失败升级"与"成功降级"的闭环 → 要么全放要么全禁，中间态不可配置 | `service/ratelimit_service.go:2258-2273`（手动清除会顺带清 `model_rate_limits`）；`repository/account_repo.go:2596-2607`（无条件清空）；标记路径见 R-30 | high | 设计必须给出"标记 → 冷却 → 半开探测 → 恢复/再升级"的状态机（含最大冷却上限），并明确手动清除是唯一逃生口 |
| **R-33** | **临时封禁只覆盖两种错误，且冷却固定 1 分钟** | `RetryableOnSameAccount` 的 400（google config）/502（empty response） | 1 分钟对"账号真坏"太短（回池后再次失败，形成抖动循环），对"单次抖动"过长（误伤）。且只有这两类错误有"同账号重试后封禁"路径，其它错误完全无隔离 | `service/gateway_service.go:753-771`（`TempUnscheduleRetryableError` 仅 400/502 分支）；`service/antigravity_gateway_retry.go:798`、`:813`（固定 1 分钟） | high | 冷却时长必须可配置且支持指数退避（1m → 5m → 15m，封顶），并在设计里定义"升级触发条件" |
| **R-34** | **`NextAccountStop` / 凭据级失败与新规则的优先级未定义** | 凭据失效（`Stage=GatewayFailureStageAccountAuth`）、provider 级故障（`Scope=GatewayFailureScopeProvider`） | 当前语义精细（`ShouldReportAccountScheduleFailure` 决定是否算账号健康问题）。**若"可配置规则"按状态码一刀切，会把 provider 级/请求级故障误算到账号头上** | `service/gateway_service.go:724-740`（`ShouldRetryNextAccount`、`ShouldReportAccountScheduleFailure`）；构造点 `service/openai_gateway_upstream_errors.go:391`、`service/grok_credential_failure.go` | high | 规则引擎的输入不能只有 `status_code`，必须包含 `Stage/Scope/RequestScopedTransient`；否则判定精度会低于现状 |
| **R-35** | **标记是账号级，一个模型的上游故障会摘掉整账号** | 账号下某模型专属错误（配额/权限/模型下线） | 同账号其它模型一并不可用，可用容量骤降；`HandleUpstreamModelNotFound` 之外没有统一"账号×模型"标记策略 | `service/ratelimit_service.go:367-369`（模型不存在单独处理）、`:2275-2287`（`model_rate_limits` 存在但非默认路径）；`repository/account_repo.go:2478-2506` | medium | 明确"哪些失败必须落到 `model_rate_limits` 而不是 `temp_unschedulable_until`"，并把该映射写进配置项清单 |
| **R-36** | **`temp_unschedulable_reason` 可能长期停留在旧原因** | 见 R-20（守卫吞掉更新） | 管理端/告警显示"400 invalid project resource name"，实际原因是后面的 429；排障被误导 | `repository/account_repo.go:2526-2533`（reason 与 until 同一 UPDATE）、`:2541-2543`（`affected<=0` 早退） | medium | reason 改为"'当前生效窗口'的原因"语义，并在 no-op 时单独更新原因（或改为事件表） |

### 2.6 可观测性缺口（R-37 ~ R-42）

| ID | 风险 | 触发条件 | 影响 | 证据 | 严重度 | 缓解 |
| --- | --- | --- | --- | --- | --- | --- |
| **R-37** | **`account_switch_count` 名不符实：统计的是"判定为可切换的错误事件数"，不是"实际切换次数"** | 任意 failover 错误 | 该事件在 service 层"决定要换号"时就写入，与"最终是否换号"无关（已写字节中止、`NextAccountStop`、客户端断开都会计一次）。用它算"切换成功率/耗尽率"会**系统性偏高**；用它做容量告警会误报 | 指标口径 `service/ops_metrics_collector.go:568-587`（`COUNT(*)` where `kind IN ('failover','retry_exhausted_failover','failover_on_400')`）；写入点 `service/gateway_forward.go:693-709`、`:730-745`、`:774-785`；事件来源 `service/ops_upstream_context.go`（JSONB 数组） | high | 新增独立计数器：`failover_decision_total{decision=switch\|stop\|suppressed}`、`failover_switch_total{from,to,group}`、`failover_exhausted_total{reason}`；旧指标重命名为 `failover_eligible_errors_total` |
| **R-38** | **无分组/账号维度，无法回答"这个分组还剩几个可用账号"** | 分组级容灾退化（10 账号剩 1） | 无告警；`ops_repo_metrics` 全局快照只取 `platform IS NULL AND group_id IS NULL` | 指标查询条件只有时间窗 `service/ops_metrics_collector.go:579`；全局快照 `repository/ops_repo_metrics.go:186-188`；告警规则仅 `error_rate`/`upstream_error_rate`（recon §R11，`service/ops_alert_evaluator_service.go:606-615`） | high | 增加分组维度快照（可用账号数、切换率、耗尽率），并把"可用账号数 / 上期"纳入 P0 告警 |
| **R-39** | **没有"切换链"，且选号含随机 → 事后无法复现** | 任意多次换号 | 每条切换只有 1 条 warn（account_id/status/switch_count/max_switches），**没有候选数、排除集大小、被选中账号、耗时**；重选还含随机打散，"为什么切到 B 而不是 C"永远答不出 | `handler/failover_loop.go:251-256`；随机 `service/gateway_scheduling.go:1734`、`:1763-1780`；recon §2.3 | high | 每请求落一条 `failover_trace`（attempt[]: account_id, status, elapsed_ms, candidates_before, excluded_size, decision）；若要"顺序可解释"，选号必须改为确定性游标（见取舍 V4） |
| **R-40** | **终局错误无"耗尽原因"维度** | 全部账号失败 | 三种终止（503 退避耗尽 / 利润否决耗尽 / 直接耗尽）只在 warn 文案上有差别；用户侧统一 `Upstream service temporarily unavailable` | `handler/gateway_handler.go:1921-1961`；三种分支 `handler/failover_loop.go:245-247`、`:290-296`、`:318` | high | 终局错误体加 `exhausted_reason` 与 `attempts`；ops 事件同步该字段 |
| **R-41** | **日志/存储按"尝试数 × 并发"放大** | 429/5xx 风暴 | 每个失败账号 1 条 handler warn + 1 条 service `[Forward] Upstream error (failover)`（含最多 1000 字符 body）；`LogUpstreamErrorBody` 默认 true → 默认存 body 片段。N 并发 × 11 账号 = 11N 条 warn + 11N 段 body | `service/gateway_forward.go:726-727`（body 截断 1000）；`config/config.go:2529-2530`（默认 true，2048 字节）；`handler/failover_loop.go:223-229`、`:251-256` | medium | 换号过程日志降到 debug（保留 1 条汇总 warn/请求）；`upstream_errors` 事件按 `request_id` 聚合上限（如 20 条） |
| **R-42** | **"被抑制的切换"完全不可见** | 缓存绑定禁止换号、已写字节禁止换号、`NextAccountStop` | 排障时无法区分"能力不行"与"策略禁止"，会把设计约束误判成 bug | 见 R-09、R-06、R-34；`handler/gateway_handler.go:1060-1064` 只发终局错误不记"抑制原因" | medium | 见 R-37 的 `failover_decision_total{decision=suppressed, reason}` |

### 2.7 与 opsmgmt 利润保护的交互（R-43 ~ R-46）

> 任务要求澄清"利润否决与失败切换是否冲突、是否可能互相抵消导致'永不停用'或'过度停用'"。结论：**二者不在同一预算维度，但共享同一个排除集、且清空策略不同**，因此同时存在"复活"与"双杀"两条边。

| ID | 风险 | 触发条件 | 影响 | 证据 | 严重度 | 缓解 |
| --- | --- | --- | --- | --- | --- | --- |
| **R-43** | **两套"否决"语义混用同一排除集**：利润否决不推进 `SwitchCount`（只增 `profitVetoCount`，上限 10），网络失败推进 `SwitchCount` | 利润门 + 网络失败同时发生 | 一个请求内两种否决互相影响：失败切换把账号加入排除集后，**503 退避会把它清掉**（只保留利润否决集）→ 刚被 503 摘掉的账号 2s 后回池；反之利润否决的账号被"永久"留在排除集 | `handler/failover_loop.go:161-172`（`RecordProfitVeto` 只增独立计数）、`:242`（失败加入同一 map）、`:310-315`（清空只保留利润集）、`:276-319`（退避分支） | high | 排除集拆成两个：`hardExcluded`（利润否决，请求内不复活）/ `softExcluded`（失败，可被退避复活）；日志分别计数 |
| **R-44** | **"过度停用"边**：一次请求即可同时触发"写库标记"与"利润排除" | 同请求内先失败后重选到更高价账号 | 账号被双杀：既被标记（可能 10 分钟 529 冷却）又在本请求内被利润门排除。外部表现"池子突然空了"，但账号状态在面板上看是"健康/冷却中"，**运维无从判断是价格还是故障** | `service/gateway_upstream_response.go:550-557`（标记）+ `handler/gateway_handler.go:809-826`（利润终检否决）；利润门基于请求开始冻结的倍率（`handler/failover_loop.go:134-139` 注释） | high | 面板与告警必须区分 `scheduling_block_reason ∈ {profit_veto, upstream_error, rate_limit}`；利润否决**不得**写账号状态（现状已如此，需在测试中钉死） |
| **R-45** | **"永不停用"边**：池模式不写库 + 503 退避复活 + 粘性不清 → 坏账号可被无限反复命中 | 池模式账号持续 5xx/429（非 401/403/429 的"可重试集合"内）+ 有粘性绑定 | 每次请求都命中同一坏账号 → 失败 → 换号 → 下次请求仍命中（粘性未清、状态未写），用户侧表现为"这个 session 一直很慢/偶发失败" | `service/ratelimit_service.go:342-349`（不写库）；`handler/gateway_handler.go:1068-1070`（只 `ReleaseAccountSession`，**不删粘性绑定**）；`service/gateway_scheduling.go:131-141`（下次仍读同一绑定）；recon §2.4 | high | 失败必须**分档**处理粘性：确定性失败（401/403/404/5xx 连续）删除绑定；瞬时失败保留绑定但记失败计数；计数超阈值再删 |
| **R-46** | **换号本身改变计费口径（`ForceCacheBilling`），与利润判定时点不一致** | 有粘性绑定 + 实际换号 | 换号后 `input_tokens` 被强制记为 `cache_read`（`needForceCacheBilling`）→ 单价变化。而利润门在"请求开始时冻结的下游倍率"上判定，二者时点/口径不同 → 可能出现"利润门放行了，但换号后实际毛利更差"（或反之） | `handler/failover_loop.go:214-216`、`:321-325`；冻结倍率 `:134-139` 注释；handler 注入 `handler/gateway_handler.go:915-917` | medium | 设计里必须写清：利润判定口径是否包含"换号导致的 cache 计费倾斜"；建议先冻结"最坏情况单价"再判，避免换号后越线 |

### 2.8 收敛与配置面（R-47 ~ R-49）

| ID | 风险 | 触发条件 | 影响 | 证据 | 严重度 | 缓解 |
| --- | --- | --- | --- | --- | --- | --- |
| **R-47** | **"同组下一个可用账号"在实现里没有稳定定义** | 任意多次换号 | 选号 = 重跑完整调度（优先级 → 可选窗口 → 负载 → LRU → **组内随机**）。若设计/文档/工单承诺"顺序"，将与实现不符；用户与运维无法自证"为什么是它" | `service/gateway_scheduling.go:1734`（同组随机）、`:1763-1780`（shuffle）；recon §2.3 | medium | 设计必须显式选择：**确定性游标**（可解释、可回归）或**重算**（现行为）。二者不能既承诺顺序又保留随机（见取舍 V4） |
| **R-48** | **69 处错误构造点使"统一判定规则"极易出现行为分叉** | 只改 1~4 处状态码集合 | 同一状态码在不同入口得到不同结论（Gemini 换、生图不换）；且 `Scope/Stage/NextAccountAction/RequestScopedTransient` 等语义会被"按状态码"的新规则**覆盖丢失**，精度反而下降 | `grep '&UpstreamFailoverError{'` 在 `service/` 命中 89 处（含约 20 处测试）；语义字段定义 `service/gateway_service.go:700-715`、`:724-740` | high | 判定器只能作为**叠加过滤器**（`decision = baseSemantics AND configRule`），不允许替代语义字段；收敛顺序按入口分批（先 Anthropic+Gemini，再看 OpenAI） |
| **R-49** | **配置面扩张后的"配置漂移"无校验** | 新增分组级 failover 配置 | 现有配置项散在 `config.yaml` + 账号 `credentials`（`pool_mode*` / `custom_error_codes` / `temp_unschedulable_rules`），无 DB/迁移（`grep failover backend/migrations` 无命中）。若再引入分组级，**三处配置源优先级未定义**，且没有"配置生效预览" | `config/config.go:1189-1195`、`:2523-2534`；账号级 `service/account.go:1216-1265`、`:1301-1371`；recon §五 | medium | 定义唯一解析顺序（账号级 > 分组级 > 全局 > 代码默认）并要求管理端展示"实际生效值"；新增配置必须有 `Validate()` 与默认值测试 |

### 2.9 汇总

| 严重度 | 数量 | 编号 |
| --- | --- | --- |
| **blocker** | 8 | R-01, R-02, R-10, R-11, R-17, R-24, R-30, R-31 |
| high | 25 | R-03, R-04, R-05, R-08, R-12, R-13, R-16, R-18, R-19, R-20, R-25, R-26, R-27, R-28, R-32, R-33, R-34, R-37, R-38, R-39, R-40, R-43, R-44, R-45, R-48 |
| medium | 16 | R-06, R-07, R-09, R-14, R-15, R-21, R-22, R-23, R-29, R-35, R-36, R-41, R-42, R-46, R-47, R-49 |
| 合计 | 49 | R-01 ~ R-49 |

> 计数口径：上表按**条目**计（8+25+16=49），便于逐条验收；§3.1 按**独立裁决点**合并为 6 个（R-01+R-02 → B1 写状态判据；R-30+R-31 → B4 标记语义）。

---

## 三、按严重度排序的行动建议

### 3.1 blocker：设计必须显式裁决，否则不得进入实现

| 序 | 裁决点 | 必须产出的设计结论 | 关联风险 |
| --- | --- | --- | --- |
| **B1** | **"能否切换"的唯一判据** | 定义 `UpstreamWriteState{SemanticBytes, HeartbeatBytes, StatusCommitted}`，由 Forward 入口注入、两层共读；**删除**任何直接读 `c.Writer.Written()` / `Writer.Size()` 做换号决策的分支；明确"ping 不算写" | R-01, R-02, R-06, R-09 |
| **B2** | **判定与副作用同源** | 只有一个判定器产出 `{switch, mark, cooldown, sticky}` 四元决策；`handleFailoverSideEffects` 不得在 `Forward` 内无条件调用；`NextAccountStop`/凭据级/请求级语义**必须**作为判定输入 | R-17, R-34, R-48 |
| **B3** | **请求级总预算** | 新增 `failover_total_budget_ms`（建议默认 90–120s）与"单尝试预算 = min(剩余, 单次超时)"；预算耗尽的终局错误必须带 `attempts` + `exhausted_reason=budget`；三层重试（service 5 次 / 同账号 N 次 / 账号 m 次）必须收敛为一个总口径 | R-24, R-25, R-26, R-27 |
| **B4** | **账号状态标记语义** | 三档隔离模式（`mark` / `observe` / `none`）+ 失败计数阈值 + 冷却指数退避 + 半开探测；明确池模式的默认档位（建议 `observe`）；明确"标记粒度"（账号 vs 账号×模型） | R-16, R-30, R-31, R-32, R-33, R-35 |
| **B5** | **成本记账与扣费分离** | 扣费继续用 `(request_id, api_key_id)`；**成本**用 `(request_id, attempt_index)` 的独立表；明确"写字节后重放"的账单结平语义（upsert 而非 DO NOTHING） | R-10, R-11, R-12, R-13 |
| **B6** | **半截响应策略** | 明确：Anthropic 路径**不允许**在已 flush 语义字节后换号；终局必须补协议合法终止事件；WS 仅允许"本轮无语义输出"时换号 | R-03, R-04, R-07 |

### 3.2 high：上线前必须有测试钉死（可与实现同批）

1. **可观测性先行**（R-37/R-38/R-39/R-40/R-42）：新增 `failover_decision_total` / `failover_switch_total` / `failover_exhausted_total{reason}` / `failover_trace`，同时把旧 `account_switch_count` 重命名为"可切换错误事件数"。**指标口径错误会让后续所有"容灾效果"论证失效**，优先级高于功能。
2. **抖动与写放大**（R-16/R-18/R-19/R-21）：所有退避加 jitter；账号状态写库合并/单飞。
3. **复活与双杀**（R-43/R-44/R-45）：排除集拆 hard/soft；粘性解绑分档；利润否决禁止写账号状态（用测试钉死）。
4. **粘性与隔离的闭环**（R-45）：失败 → 计数 → 解绑 → 冷却 → 半开 → 恢复，跑通一条完整状态机。
5. **清理路径**（R-23/R-22）：会话释放异步化 + 槽位释放回归测试。
6. **收敛策略**（R-48/R-49）：判定器作为叠加过滤器；配置解析顺序唯一 + 生效值可见。

### 3.3 medium：排期修复

R-06、R-07、R-09、R-14、R-15、R-21、R-29、R-35、R-36、R-41、R-42、R-46、R-47、R-49
（R-22、R-23 已并入 §3.2 第 5 条，不重复列出）。

其中 **R-47 与"顺序可解释"目标直接冲突**，必须在 t2 的方案对比中定调（确定性游标 vs 重算），否则文档与实现必然不一致；**R-42（被抑制的切换不可见）是排障第一入口**，建议与 §3.2 第 1 条的可观测性改造同批落地。

---

## 四、必须补的测试清单

> 约定：新增测试文件放在各自包内、沿用 `//go:build unit` 或集成 tag；每条测试**必须能单独失败**（不依赖时序巧合）。

| 测试 ID | 覆盖风险 | 建议文件 | 断言要点 |
| --- | --- | --- | --- |
| **T-01** | R-01 | `backend/internal/handler/gateway_stream_write_state_test.go` | 排队期写 ping 后，上游在首字节前断流：断言**发生换号**（而不是 `stream_read_error` 硬失败）；再断言换号成功后客户端收到**一个**完整响应 |
| **T-02** | R-02 | `backend/internal/handler/gateway_stream_semantic_bytes_test.go` | 已写 `message_start`（语义）后失败：断言**禁止换号**；仅写 ping 后失败：断言**允许换号**；两例必须在同一判据函数下同时成立 |
| **T-03** | R-03, R-04 | `backend/internal/handler/gateway_stream_terminal_event_test.go` | 流内失败后响应体：(a) 只有 1 个 error 帧；(b) 存在协议终止事件；(c) `message_start` 恰好 1 次。现有 `gateway_handler_stream_failover_test.go` 只调 `handleFailoverExhausted`（`test:52`），**必须补端到端循环级**测试 |
| **T-04** | R-05, R-12 | `backend/internal/handler/gateway_failover_attempt_audit_test.go` | 一次请求换号 3 次后：断言产生 4 条 attempt 审计记录（含 status/elapsed/decision），且扣费仍只有 1 条 |
| **T-05** | R-10, R-11 | `backend/internal/service/gateway_usage_billing_conflict_test.go` | (a) 同 `request_id`、body 被渠道映射改写后二次入账：断言**不返回** `ErrUsageBillingRequestConflict`（设计需先冻结指纹）；(b) 成本表按 `attempt_index` 落 2 条；(c) 扣费表仍 1 条 |
| **T-06** | R-13 | `backend/internal/service/gateway_usage_billing_partial_replay_test.go` | 先 partial usage 入账，再完整重放：断言账单被**结平为完整值**（而非静默停留在 partial），且跳过路径有结构化 warn |
| **T-07** | R-15 | `backend/internal/service/openai_usage_integrity_no_failover_test.go` | `grok_missing_usage` 不得触发换号，且不计入账号健康统计 |
| **T-08** | R-16 | `backend/internal/service/ratelimit_concurrent_529_write_amplification_test.go` | 同一账号 50 个并发 529：断言落库**≤1 次/窗口**（或断言 outbox 行数有界），而非 50 次 |
| **T-09** | R-17 | `backend/internal/handler/failover_side_effect_after_write_test.go` | 已写字节 → 放弃换号：断言**账号状态未被标记**（当前会标记，测试应先红） |
| **T-10** | R-22 | `backend/internal/handler/gateway_failover_slot_release_test.go` | 换号 N 次（含失败/取消/正常三条路径）后，账号槽位与等待计数回到基线；重复 release 不产生负计数 |
| **T-11** | R-19 | `backend/internal/handler/failover_backoff_jitter_test.go` | 同一批失败的重试触发时刻分布满足 jitter 约束（极差 > 0，且不超过 ±30% 上限） |
| **T-12** | R-24, R-25, R-26 | `backend/internal/handler/failover_budget_test.go` | 上游全部 hang：断言总耗时 ≤ 预算 + 单次尝试余量；断言终局错误 `exhausted_reason=budget`、`attempts` 正确；Gemini 路径同样受限 |
| **T-13** | R-27, R-40 | `backend/internal/handler/failover_exhausted_error_shape_test.go` | 全 429 / 全 5xx / 混部三种终局：断言状态码、`Retry-After`、`attempts`、`exhausted_reason` 各自正确（当前全 5xx 无 `Retry-After`） |
| **T-14** | R-28 | `backend/internal/handler/failover_loop_no_budget_source_test.go` | 对"不推进预算的重试源 × 清空排除集"组合做穷举，断言有限步终止（扩展 `failover_loop_profit_veto_test.go:26-62` 的模拟循环，覆盖新规则） |
| **T-15** | R-30, R-32, R-33 | `backend/internal/service/account_isolation_state_machine_test.go` | 单次失败**不**摘账号；连续 N 次摘除；冷却指数退避；半开成功恢复；半开失败再升级；手动清除可逃生 |
| **T-16** | R-31 | `backend/internal/service/pool_mode_isolation_test.go` | `isolation_mode=observe`：不写状态但计数递增；`mark`：写状态；`none`：完全跳过（现状行为） |
| **T-17** | R-34 | `backend/internal/service/failover_rule_semantics_test.go` | provider 级 5xx / request-scoped 529 / 凭据 401 在同一规则下分别判定为"不算账号问题 / 不算 / 算"，断言 `Scope/Stage` 参与决策 |
| **T-18** | R-37, R-38 | `backend/internal/service/ops_failover_metrics_test.go` | 一次请求 3 次失败 2 次成功切换：断言 `decision_total{switch}=2`，旧指标（可切换错误数）为 3；断言分组维度快照存在 |
| **T-19** | R-39, R-42 | `backend/internal/handler/failover_trace_test.go` | 每次尝试产出 trace 条目（含 `candidates_before/excluded_size/decision`）；被抑制的切换有 `decision=suppressed, reason` |
| **T-20** | R-43, R-44, R-45 | `backend/internal/handler/failover_exclusion_sticky_test.go` | (a) 503 退避后利润否决账号仍在排除集、失败账号被复活（现状已有 1 例，需补"两者混合"）；(b) 利润否决**不写**账号状态；(c) 确定性失败后粘性绑定被删除 |
| **T-21** | R-46 | `backend/internal/service/failover_force_cache_billing_profit_test.go` | 换号导致 `ForceCacheBilling` 时，利润判定口径与实际计费一致（或显式记录"口径差"） |
| **T-22** | R-47 | `backend/internal/service/account_selection_order_determinism_test.go` | 若选择"确定性游标"：同一失败序列在并发/重复执行下选出**同一序列**；若保留随机：断言 trace 记录候选顺序（禁止两者都不做） |
| **T-23** | R-09 | `backend/internal/handler/gemini_cache_bound_suppressed_test.go` | 缓存绑定请求失败时产出 `failover_decision_total{decision=suppressed, reason=cache_bound}` |

---

## 五、不可接受的设计取舍

> 以下方案因为风险必须**否决**；若产品坚持，需由 captain 在产品层面显式接受风险并写入设计文档。

| # | 被否决的取舍 | 否决理由（引用风险） | 可接受的替代 |
| --- | --- | --- | --- |
| **V1** | **在 Anthropic 路径复用 `SafeToFailoverAfterWrite` / 引入"写后切换"或"写后续跑"** | 该字段的前提是"首输出前 staging"（`service/openai_gateway_response_handling.go:255-285`），Anthropic 逐事件直写（`service/gateway_upstream_response.go:1102-1116`），没有 staging → 必然出现半截内容拼接（R-02、R-06）。且一旦重放，账单会因指纹冲突失败（R-11） | 先在 Anthropic 实现首输出 staging（缓冲到首个语义事件再 flush），再做写后切换；否则只允许"未写语义字节"时换号 |
| **V2** | **用 `(request_id, api_key_id)` 记账/统计换号成本** | 该键的唯一索引保证"一请求一账单"（`migrations/071:12-13`），换号的额外上游成本会被**静默吞掉**（R-10、R-13），并让"成本预算"永远拿不到分母 | 新增 `upstream_cost_attempts(request_id, attempt_index, account_id, status, elapsed_ms, billed_usage)` |
| **V3** | **把"利润否决"的预算口径（不推进 `SwitchCount`）套用到网络失败** | #4925 已证明"不推进预算 + 清空排除集"= 每 2s 空转（`handler/failover_loop_profit_veto_test.go:64-82`）；网络失败是**真实上游调用**，不推进预算会让"重试上限"彻底失效（R-28、R-43） | 网络失败一律推进统一预算；只有"零上游代价"的否决（利润门）才允许独立预算，且必须有硬上限 |
| **V4** | **既承诺"同组下一个账号"的顺序可解释，又保留选号随机** | 随机打散在 `service/gateway_scheduling.go:1734`、`:1763-1780`；同时承诺顺序会让工单/审计结论不可自证（R-39、R-47） | 二选一：①确定性游标（按分组维护轮转位置）；②明确"重算最低负载/LRU"，只用 trace 解释"当时为何选它"。**不得混用** |
| **V5** | **在 handler 层新增可配置判定规则，但保留 service 层无条件副作用** | 会造成"规则说不换号，账号已被摘掉"（R-17）；也会让规则精度低于现状（丢失 `Scope/Stage`，R-34、R-48） | 判定器下沉到唯一入口，副作用紧随决策；规则作为**叠加过滤器** |
| **V6** | **给网关加"客户端 `Idempotency-Key` 去重"来防重复上游调用** | 骗不了上游：上游调用已经发生，去重只会让**第二次真实消耗不被记录**（叠加 R-10）；且网关路由一旦加中间件，历史客户端的请求语义会变（`routes/gateway.go:180-189` 当前无幂等） | 只做"审计 + 计量"（attempt 记录），不做"重放拦截" |
| **V7** | **单次失败即永久/长期停用账号（无阈值、无半开）** | 一次抖动摘掉一个可用账号，多云/多账号池的容灾能力反而下降；且恢复只能靠人工（R-30、R-32） | 计数阈值 + 指数退避冷却 + 半开探测；必须有最大冷却上限 |
| **V8** | **一次性统一 4 套实现 / 69 个构造点** | 范围过大，任何一处漏改都会造成入口间行为分叉（R-48），且无法用一组测试覆盖 | 分批：先 Anthropic+Gemini（`FailoverState` 系）→ 再 OpenAI → 最后生图/AlphaSearch/Grok 媒体；每批带独立回归基线 |

---

## 附：与后续任务的接口

| 下游 | 需要本文件提供 | 对应章节 |
| --- | --- | --- |
| t2（方案对比/设计） | 必须裁决的 6 个 blocker 裁决点、8 个被否决取舍 | §三 3.1、§五 |
| t4（实施步骤） | 分批收敛顺序（V8）、测试清单（T-01~T-23）作为每步的验收门 | §四、§五 V8 |
| t5/验证 | 指标口径变更（R-37）会改变历史看板含义 → 需要发布说明 | §3.2 第 1 条 |
