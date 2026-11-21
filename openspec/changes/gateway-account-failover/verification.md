# 网关账号级容灾验收证据底表

**2026-11-21 工作树快照，最终验收待完成。** 本表按实施规划 §2.1 的 26 项（25 必做、1 原文可选）及 §4.1 的 8 个 blocker 编排。Team 任务号用于追溯命令、退出码和验收记录。文件存在性以采样时的共享工作树为准。采样 HEAD 为 `530c5e3a4f9c73ca2b0881191d0ef8e0d82c5484`，树上包含历史未提交改动和同期开发。本表引用各次运行时的结果，最终验收需在稳定工作树重新取得整套证据。 [实施规划](implementation.md#L191) · [风险清单](risks.md) · [发布手册](rollout.md)。任务号对应现有团队 `gw-failover-impl` 的任务板。

## 1. 26 项证据底表

列中的 A01–A20 对应 §2 的实际命令与退出码。定向测试结果仅覆盖各次命令选中的包及用例。

| ID | 当前状态 | 实际文件或核对证据 | 执行结果与缺口 |
| --- | --- | --- | --- |
| T0.1 | 配置定向验证 | [配置与默认值](../../../backend/internal/config/config.go)、[校验测试](../../../backend/internal/config/config_test.go)、[默认表一致性](../../../backend/internal/config/failover_status_classes_sync_test.go) | A01 原始整条命令普通/unit 均 1，config 包分别通过。A02 普通/unit 均 0。标量默认键由 Viper 注册，`status_classes` 由加载阶段补默认表，保留显式空数组。D1 的 `max_selection_retries=0` 已锁定，最终仍需核对全部配置消费点。 |
| T0.2 | 静态 DDL 与 sqlmock | [254 迁移](../../../backend/migrations/254_group_failover_policy.sql)、[三列读取仓储](../../../backend/internal/repository/group_failover_policy_repo.go)、[仓储测试](../../../backend/internal/repository/group_failover_policy_repo_test.go)、[早期静态测试](../../../backend/internal/migrations/group_failover_policy_migration_test.go)、[嵌入迁移测试](../../../backend/migrations/group_failover_policy_migration_test.go) | A01 仓储新增 sqlmock 用例通过，整条命令两档各 1。A03 两档 0。只增加 `groups` 的三列，可空、无默认且幂等。t1/t24 记录的 Ent 对照保留历史改动，最终按隔离快照复核。真实 PostgreSQL 的迁移和读取尚未执行。 |
| T0.3 | 解析与请求快照定向验证 | [策略解析](../../../backend/internal/service/failover_policy.go)、[三级覆盖测试](../../../backend/internal/service/failover_policy_test.go)、[请求快照](../../../backend/internal/service/failover_request_policy.go)、[快照测试](../../../backend/internal/service/failover_request_policy_test.go) | t1 结果报告三级合并与 JSONB 测试两档通过，A01 的包参数仅覆盖 config/repository。A03 两档 0 验证按授权分组冻结和取消处理。账号级读取、分组 Reader 生产注入、新旧键的现场优先级待 t28 确认。 |
| T0.4 | 分类器定向验证 | [纯函数分类器](../../../backend/internal/service/failover_classifier.go)、[F-01 表](../../../backend/internal/service/failover_classifier_test.go) | A04 两档 0，覆盖五类入口、判定优先级及取消/上游超时/瞬态三条不变量。新增 `policy_blocked` 分支位于绑定透传之后、同账号重试之前。最终复核生效策略是否确实传到生产调用。 |
| T1.1 | 四处状态判定接线 | [Gateway](../../../backend/internal/service/gateway_forward.go)、[Antigravity](../../../backend/internal/service/antigravity_gateway_retry.go)、[Gemini](../../../backend/internal/service/gemini_messages_compat_service.go)、[OpenAI](../../../backend/internal/service/openai_gateway_upstream_errors.go) | A05 两档 0。域外状态仍走旧 `>=500` 兜底，同账号重试函数仍沿用旧逻辑。动态策略和专用路径需结合后续接线核验。 |
| T1.2 | 影子等价定向验证 | [影子对照](../../../backend/internal/service/failover_legacy_equivalence_test.go) | A05 两档 0，逐值对照 2808 次，另有传输层和同号重试对照。早期 service 全包复验曾报 WS TTFT/Ollama CAS（§4），不能用影子对照替代最终两档全仓。 |
| T2.1 | Messages 主路径判据定向验证 | [写出状态](../../../backend/internal/service/upstream_write_state.go)、[状态测试](../../../backend/internal/service/upstream_write_state_test.go)、[上游流处理](../../../backend/internal/service/gateway_upstream_response.go)、[handler 判定](../../../backend/internal/handler/gateway_handler.go) | A06 两档 0。语义/心跳读同一请求状态，未分类字节保守计作语义，首写前尺寸哨兵 `-1` 归零。兼容及专用串流仍有既有 `Written/Size` 判据，逐入口复核边界。 |
| T2.2 | 纯决策、收集器与主路径接线已有证据 | [决策与副作用](../../../backend/internal/service/failover_decision.go)、[决策测试](../../../backend/internal/service/failover_decision_test.go)、[Forward](../../../backend/internal/service/gateway_forward.go)、[handler](../../../backend/internal/handler/gateway_handler.go)、[启动设置](../../../backend/internal/service/wire.go) | A06 两档 0。A14 记录 P5 的调用证据及整体两档 1。D2 在预算前引入门控，`side_effects_deferred` 默认关，总开关关闭时门控关闭。最终排查放弃换号、取消及同号多次失败时的副作用，补授权分组运行接线。 |
| T2.3 | 粘性判定修复经独立复验 | [粘性清除](../../../backend/internal/service/gateway_service.go)、[Messages/兼容入口](../../../backend/internal/handler/gateway_handler.go)、[Gemini 入口](../../../backend/internal/handler/gemini_v1beta_handler.go)、[粘性测试](../../../backend/internal/service/sticky_clearance_test.go) | A07、A08 各两档 0。t14 首次两档各 1，空范围误清除，t16 将清除限定于显式账号归因，unknown 保留。原断言双绿。冷却触发后的运行时粘性、显式缓存抑制及度量仍待跨层验证。 |
| T2.4 | 切换链状态定向验证 | [切换链](../../../backend/internal/handler/failover_loop.go)、[发送后回填](../../../backend/internal/handler/failover_budget.go)、[切换轨迹测试](../../../backend/internal/handler/failover_switch_hop_test.go) | A07、A08 各两档 0。7 例覆盖目标回填、同号、耗尽、利润否决、16 跳及快照。以当前 from/to/原因和发送记录为证。规划 T-19/T-22 的候选层级、负载/优先级证据和 `record_chain=false` 零额外分配尚未完整证明。 |
| T3.1 | 实际发送预算定向验证 | [预算实现](../../../backend/internal/handler/failover_budget.go)、[发送边界](../../../backend/internal/service/failover_attempt.go)、[时钟与发送测试](../../../backend/internal/handler/failover_budget_test.go)、[槽位测试](../../../backend/internal/handler/gateway_failover_slot_release_test.go) | A09 构建及双模式 exit 0，含取槽起钟、真实发送、父取消、首语义输出停表。t15 记录覆盖 Anthropic、Gemini、Antigravity 应用层的 13 处发送点。最终版本复跑并核对没有接入的专用入口、传输层隐式重试及端到端限时。 |
| T3.2 | 重试预算、排除集与退避定向验证 | [循环](../../../backend/internal/handler/failover_loop.go)、[无预算重试源](../../../backend/internal/handler/failover_loop_no_budget_source_test.go)、[jitter](../../../backend/internal/handler/failover_backoff_jitter_test.go)、[既有利润否决](../../../backend/internal/handler/failover_loop_profit_veto_test.go) | A09 两档 0。D1 保留 `0` 的旧 503 重选路径。最终锁定版复跑既有利润否决与同号语义，核对启用预算后的请求上界和全路径 jitter 范围。 |
| T3.3 | 历史定向通过，P5 合并态回归失败 | [预算终局入口](../../../backend/internal/handler/gateway_handler.go)、[原断言与 P5 用例](../../../backend/internal/handler/failover_exhausted_error_shape_test.go) | A09 两档 0。t21 找回原断言后，A14 两档 1。依据[修复方案](repair-plan.md)，t12 attempt 5 恢复原契约且通过 A20。原状态映射和 `X-Failover-Reason` 保留，预算正文隐藏 `attempts`，内部 outcome 保留次数。 |
| T4.1 | 仓储与半开原语经 Redis 模拟验证 | [服务接口](../../../backend/internal/service/failover_cooldown.go)、[Redis 仓储](../../../backend/internal/repository/failover_cooldown_cache.go)、[仓储测试](../../../backend/internal/repository/failover_cooldown_cache_test.go)、[半开测试](../../../backend/internal/repository/failover_cooldown_probe_cache_test.go) | A10、A12 各两档 0。`miniredis` 运行 Lua、TTL、租约、延长和分布式去抖。启动注入待 t28，真实 Redis 实例与跨进程负载未验证。 |
| T4.2 | 负载感知四层定向验证 | [调度](../../../backend/internal/service/gateway_scheduling.go)、[实例注入入口](../../../backend/internal/service/gateway_service.go)、[冷却调度测试](../../../backend/internal/service/gateway_scheduling_cooldown_test.go)、[隔离调度测试](../../../backend/internal/service/gateway_scheduling_isolation_test.go) | A11、A13 各两档 0。t11 起步只覆盖 Layer 2，t27 增加四层共享快照，普通 168 个顶层/290 含子用例，unit 337/619。`load_batch_enabled=false` 仍走旧路径。半开结果携带许可计划，生产消费待 t28。 |
| T4.3 | 状态机和存储通过定向，实际写入/恢复待接线 | [状态机](../../../backend/internal/service/failover_cooldown.go)、[状态测试](../../../backend/internal/service/account_isolation_state_machine_test.go)、[池模式测试](../../../backend/internal/service/pool_mode_isolation_test.go)、[半开原子完成](../../../backend/internal/repository/failover_cooldown_cache.go) | A12 两档 0。真实失败汇总后才决定同请求多账号同败时零新增冷却，半开租约需持槽并通过利润终检后取得。生产注入、失败汇总、代际完成、降级指标与 F-01/T-15/T-16 端到端待 t28。 |
| T4.4 | 只延长及 529 去抖定向验证 | [账号仓储](../../../backend/internal/repository/account_repo.go)、[临时封禁缓存](../../../backend/internal/repository/temp_unsched_cache.go)、[仓储测试](../../../backend/internal/repository/account_repo_temp_unschedulable_test.go)、[限流](../../../backend/internal/service/ratelimit_service.go)、[Antigravity](../../../backend/internal/service/antigravity_gateway_retry.go)、[529 并发测试](../../../backend/internal/service/ratelimit_concurrent_529_write_amplification_test.go) | A15 构建及两档 0。SQL 守卫用 sqlmock、快照用 spy，529 两实例 50 并发在 1 秒窗口内一次仓储写入由 `miniredis` 验证。保持首次 529 策略及生效原因。真实 PG 原子行为未验证。 |
| T4.5 | **原文可选，最终待裁决** | 本快照没有该三态开关的交付路径。相关现有机制为 [池模式测试](../../../backend/internal/service/pool_mode_isolation_test.go) 中的 `cooldown.pool_mode_enabled` 语义。 | 无本项实际执行命令。A12 的池模式覆盖属 T4.3。`observe/mark/none` 是否纳入以最终裁决为准，并记录默认关闭下的残留。 |
| T5.1 | 发送观测已落盘，合并态命令红 | [日志链](../../../backend/internal/handler/failover_loop.go)、[实际发送观察器](../../../backend/internal/service/failover_attempt.go)、[观测测试](../../../backend/internal/handler/failover_observability_test.go) | A14 构建 0、两档各 1。记录覆盖 Messages、Gemini 及有共享 hook 的 HTTP 兼容入口。WebSocket 与 transport 内部重试不算应用层发送。缺少 hook 的专用兼容路径仍待明确。 |
| T5.2 | 换号后恢复漏计已修复，两档定向通过 | [事件与执行标识](../../../backend/internal/service/ops_upstream_context.go)、[恢复遥测](../../../backend/internal/handler/ops_failover_recovered_telemetry.go)、[采集器](../../../backend/internal/service/ops_metrics_collector.go) | A19 两档 0。无旧错误事件的换号 2xx 生成独立遥测行；历史 `account_switch_count` 仍取既有三种错误事件。新告警按服务端执行 ID 去重，保持旧列表、provider 指标与空预聚合桶隔离。真实 PostgreSQL 待验证，A14 仍以原退出码保留。 |
| T5.3 | 恢复告警分子、监控排除与旧统计已定向复核 | [告警计算](../../../backend/internal/service/ops_alert_evaluator_service.go)、[告警参数测试](../../../backend/internal/handler/admin/ops_alerts_failover_test.go)、[SQL 过滤测试](../../../backend/internal/repository/ops_repo_failover_telemetry_test.go)、[去重测试](../../../backend/internal/service/ops_failover_recovered_metrics_test.go) | A19 两档 0，SQL 参数与去重证据为 sqlmock。活跃冷却/降级指标仍依赖 P4 装配；真实 PostgreSQL 聚合、分母一致性和最终锁定版尚待验收。 |
| T5.4 | 预算正文兼容修复已通过定向测试 | [Messages](../../../backend/internal/handler/gateway_handler.go)、[OpenAI](../../../backend/internal/handler/openai_gateway_handler.go)、[Chat Completions](../../../backend/internal/handler/gateway_handler_chat_completions.go)、[Responses](../../../backend/internal/handler/gateway_handler_responses.go)、[终局测试](../../../backend/internal/handler/failover_exhausted_error_shape_test.go)、[SSE 终止测试](../../../backend/internal/handler/gateway_stream_terminal_event_test.go) | A14 历史记录为两档 1，原预算断言四例失败。A20 两档 0，原四例保留，新增详情开启和已提交 SSE 的预算正文回归。B3 默认 `false`，各协议沿用已有 `Retry-After`，最终全仓待复跑。 |
| T6.1 | 账本实际发送接线已通过两档定向 | [255 迁移](../../../backend/migrations/255_upstream_cost_attempts.sql)、[嵌入迁移测试](../../../backend/migrations/upstream_cost_attempts_migration_test.go)、[写入接口](../../../backend/internal/service/upstream_cost_attempt.go)、[仓储](../../../backend/internal/repository/upstream_cost_attempt_repo.go)、[仓储测试](../../../backend/internal/repository/upstream_cost_attempt_repo_test.go)、[异步队列](../../../backend/internal/service/upstream_cost_attempt_recorder.go)、[队列测试](../../../backend/internal/service/upstream_cost_attempt_recorder_test.go) | A16、A17 各两档 0。t25 独立审查曾通过，后续并行改动使全局工作树 revision 失效，锁定版须重审。当前键为 `(execution_id UUID, attempt_index)`。t26 完成实际发送、启动与关闭接线；构建及两档定向均为 0。完整 Wire 生成仍因已有 OpsMgmtHandler provider 缺失而受阻，定向同步生成文件后构建通过。T-04 使用替身仓储已验证，真实 PostgreSQL 集成待执行。 |
| T6.2 | **扣费结平未实施，产品裁决待定** | 只读定位到 [指纹函数](../../../backend/internal/service/usage_billing.go)、[结算构造](../../../backend/internal/service/gateway_usage_billing.go)、[扣费仓储](../../../backend/internal/repository/usage_billing_repo.go)。本快照无本项交付改动。 | 无本项实际验证命令。t18 的已完成状态仅指边界审查。首体摘要冻结不足以处理账号、用量和金额差异。需决定旧指纹兼容和同次执行的 partial→final 补差、归档及投影，再验证 T-05/T-06。 |
| T6.3 | 纯幂等闸门定向通过，运行期未接入 | [闸门](../../../backend/internal/service/failover_idempotency.go)、[闸门测试](../../../backend/internal/service/failover_idempotency_test.go) | A04 中 t6 命令两档 0。默认关时零检测，打开后只能收窄既有允许换号决定。t29 待把闸门装入同号重试之后、跨账号副作用之前，保留已写语义后的禁止重放。 |
| T7.1 | D4 注释及手册已有，最终验收待完成 | [发布手册](rollout.md)、[配置中文注释](../../../backend/internal/config/config.go)、[实施规划](implementation.md) | A02 与 t7 静态文档检查已有记录。t17 是环境及基线只读审计。全量 build/vet、两档全仓、五文件强制回归同版、历史同树对照和最终文档回填仍待收口。手册已删除固定跳过建议；t33 在当前工作树全量摸底，不能代替最终锁定版验收。 |

## 2. 原始命令与结果索引

每组的「普通/unit」指同一命令分别按原报告执行无 tag 版和在 `test` 后加 `-tags=unit` 的版本。含 `-p 1` 的报告记法可写成 `test -p 1 -tags=unit`，两者参数等价。此处引用任务报告的实际结果。未给退出码的事项以「未运行」或「未定位原命令」标示。

| 索引 | 来源 | 命令或原报告记录 | 普通 / unit |
| --- | --- | --- | --- |
| A01 | t1 | `go -C backend test ./internal/config/... ./internal/repository/... -count=1` | 1 / 1。config 1.452s / 2.71s 通过，仓储三个 `TestPgDumper*` 缺 `sh` 失败。P0 另有 `go -C backend build ./...` 与 `go -C backend vet ./...` 均 0。 |
| A02 | t8、t10 | `go -C backend test ./internal/config/... -run 'Failover' -count=1` | 各自 0 / 0。t10 还记录 build、vet、`gofmt -l backend/internal/config` 各 0。 |
| A03 | t24 | `go -C backend test -p 1 ./internal/service ./migrations -run 'FailoverRequestPolicy\|GroupFailoverPolicyMigration' -count=1` | 0 / 0，静态 DDL 及请求缓存。 |
| A04 | t2、t6 | t2 `go -C backend test ./internal/service/... -run 'Failover\|Classifier' -count=1`。t6 `go -C backend test ./internal/service/... -run 'Idempotency' -count=1`。 | 各自 0 / 0。t6 普通0.510s/unit 2.741s，t2 与 t6 的 build/vet 也各 0。 |
| A05 | t3 | `go -C backend test ./internal/service/... -run 'Failover\|Classifier\|Equivalence' -count=1` | 0 / 0，5.555s / 5.582s。另 `go -C backend test ./internal/handler/... -run 'Failover' -count=1` 普通 0（18.533s）。 |
| A06 | t4 | `go -C backend test ./internal/service/... -run 'Failover\|WriteState\|Decision' -count=1` 和 `go -C backend test ./internal/handler/... -run 'Failover' -count=1` | 两条各 0 / 0。service 5.553s / 11.630s，handler 20.246s / 20.018s。build/vet 各 0。 |
| A07 | t5 | `go -C backend test ./internal/handler/... -run 'Failover\|SwitchHop\|Sticky' -count=1` | 0 / 0。同期普通 service 包通过，unit service 全包若干次 OOM，定向结果仅覆盖选中子集。 |
| A08 | t14 attempt 4、t16 | `go -C backend test -p 1 ./internal/handler ./internal/service -run 'Test.*(SwitchHop\|StickyClearance)' -count=1` | 两任务复跑各 0 / 0。t14 attempt 4 最终普通 handler 2.314s/service 1.986s，unit 2.266s/0.281s。首跑两档各 1，分别报 `tlsfingerprint` 导入文件缺失和 `strings` 未使用。t16 unit 首跑构建 1。 |
| A09 | t15 attempt 2 | `go -C backend test -p 1 ./internal/handler/... ./internal/service/... -run 'Failover\|Budget\|Jitter\|SelectionExhausted' -count=1` | 0 / 0。handler 36.559s / 37.144s，service 7.073s / 5.733s。`go -C backend build -p 1 ./...` 0。unit 首跑曾因共享构建缓存文件消失退出 1。 |
| A10 | t9 | `go -C backend test ./internal/repository/... ./internal/service/... -run 'Cooldown' -count=1` | 0 / 0，含 `miniredis`。 |
| A11 | t11 | `go -C backend test ./internal/service/... -run 'Schedule\|Cooldown\|Select' -count=1` | 0 / 0，1.328s / 9.343s。另 `go -C backend test ./internal/service/... -run 'Scheduler\|Selection\|LoadAware' -count=1` 普通 0。 |
| A12 | t19、t22 | t19 `go -C backend test -p 1 ./internal/service/... -run 'IsolationStateMachine\|PoolModeIsolation\|FailoverCooldown' -count=1`。t22 `go -C backend test -p 1 ./internal/repository ./internal/service -run 'FailoverCooldown\|IsolationStateMachine\|PoolModeIsolation' -count=1`。 | 各自 0 / 0，服务层及 Redis 模拟。 |
| A13 | t27 | `go -C backend test -p 1 ./internal/service/... -run 'Cooldown\|Isolation\|Scheduler\|LoadAware' -count=1` | 0 / 0，含待探测计划及候选层级。 |
| A14 | t12 attempt 4 | 档案命令 `go -C backend test -p 1 ./internal/handler/... ./internal/service/... -run 'Failover\|StreamTerminal\|ErrorFallback\|AlertEvaluator\|AccountSwitch' -count=1`。原报告确认实际正则额外包含 `StreamWrittenGuard\|PoolMode\|UsageIntegrity`，无跳过。 | **1 / 1**。`TestFailoverExhaustedBudgetErrorShape` 四子例均失败。普通 handler 29.198s，service 6.946s 与 admin 0.677s 通过。unit handler 29.162s，admin 1.124s 通过，service 另报 `selectionFailoverProbe` 未定义。`go -C backend build -p 1 ./...` 为 0。 |
| A15 | t13 attempt 3 | `go -C backend test -p 1 ./internal/repository/... ./internal/service/... -run 'TempUnschedulable\|529\|OverloadCooldown\|WriteAmplification' -count=1` | 0 / 0，SQL mock、快照 spy、Redis 模拟。`go -C backend build -p 1 ./...` 0。 |
| A16 | t20、t25 | `go -C backend test -p 1 ./migrations ./internal/repository -run 'UpstreamCostAttempt' -count=1` | 各自 0 / 0。t25 普通 migration 0.903s/repo 9.663s，unit 0.902s/0.230s。历次运行证据仅涵盖嵌入 DDL 文本、sqlmock 和代码检查。 |
| A17 | t23 | `go -C backend test -p 1 ./internal/service -run 'UpstreamCostAttemptRecorder' -count=1` | 0 / 0，队列与 writer stub，接线和真实库写入未覆盖。 |
| A18 | 独立复跑 t27 | `go -C backend test -p 1 ./internal/service -run '^TestSchedulerIsolation' -count=1 -v` | 0 / 0，普通 0.173s、unit 0.217s，各实际执行 5 个顶层用例。此复跑不覆盖 §4 的新增重选缺口。 |
| A19 | t31 | `go -C backend test -p 1 ./internal/handler ./internal/service ./internal/repository -run 'RecoveredFailoverTelemetry\|FailoverRecoveredMetrics\|FailoverTelemetry\|RecoveredTelemetryFiltersSkipMonitoring' -count=1` | 0 / 0。覆盖恢复遥测、执行 UUID 去重、旧 SQL 读取隔离和监控排除；SQL 为 sqlmock，不代替真实 PostgreSQL。 |
| A20 | t12 attempt 5 | `go -C backend test -p 1 ./internal/handler/... -run 'FailoverExhausted\|StreamTerminal\|ErrorFallback' -count=1` | 0 / 0。原预算四例保留。新增预算错误在详情开启和已提交 SSE 时仍隐藏 `attempts` 的回归。构建退出码 0。 |

## 3. 阻塞风险证据边界

| blocker | 已具备证据 | 闭合前缺口 |
| --- | --- | --- |
| R-01 | T-01 主路径共享 `UpstreamWriteState`，A06 双绿。 | 排队 ping 加首输出前断流的完整 handler→service 覆盖及兼容/专用入口逐路审计待锁定版复验。 |
| R-02 | T-02 语义字节和显式心跳区分，未知字节保守阻断，A06 双绿。 | 真实连续 SSE 的纯 ping、已提交状态与已写语义三个边界需在最终流路径复跑，确认语义输出后不会拼接另一账号内容。 |
| R-10 | A16/A17 覆盖账本仓储和队列；t26 已接入应用层发送并通过构建及普通/unit 定向测试。[Responses 回归](../../../backend/internal/handler/gateway_failover_attempt_audit_test.go#L174)使用替身账本与计费仓储，验证四次发送、四条成本记录和一次计费提交。 | 真实 PostgreSQL 集成尚未执行（t38 已编译）；最终锁定版仍需全仓复跑。 |
| R-11 | V1 的禁止已写语义后换号已有 T2.1 局部证据。t18 明确旧指纹包含多个计费维度。 | **T6.2 结平尚未实现**。需要旧业务键及历史指纹兼容、一次执行/同一账务事件权限、部分量补差与投影原子性、T-05/T-06 端到端。 |
| R-17 | A06 纯决策及收集器双绿，t12 提供主入口真实 Forward 注入测试。 | A14 整体非绿。最终复查所有启用门控路径的否决/取消不标记、副作用按请求去重，以及 P4 冷却与预算次序。 |
| R-24 | A09 在 t15 证明已覆盖发送点的总预算、真实计次、槽位、父取消和停表。 | A14 的预算终局体回归仍红。须验证最终公开口径和预算硬上限，并清点未进入共享钩子的发送路径。 |
| R-30 | T-15 状态机、Redis 模拟及 T3 SQL mock 有 A12/A15 证据。 | 真实失败按请求聚合后的写入、半开恢复、代际互斥、运行期冷却启用及真实 DB 行锁并发待验。保留 T3 首次既有语义是已知残留。 |
| R-31 | T-16 的池模式冷却与不写 DB 语义有 A12 证据。 | `cooldown` 默认关闭，因此默认仍保留旧池模式行为。待 t28 证实开启后真实跨请求隔离。原文可选自定义三态仍待裁决。 |

## 4. 最终验收前尚缺的证据

- **全量门禁。** 当前稳定版本的两档全仓均待执行。待共享编辑结束后独占串行，运行下列四条并分别保存原始退出码、测试名、日志和树指纹。五个强制回归文件需在相同版本复核，且 `openai_gateway_usage_integrity_test.go` 仅在 unit 模式进入。早期报告的 WS TTFT 抖动、Ollama CAS、CyberPolicy 快照及 OOM 均属历史记录。t3 的局部 `-overlay` 能辅助 Ollama 归因，完整同树证据仍缺。 [五文件要求](implementation.md#L1138)

  ```powershell
  go -C backend build -p 1 ./...
  go -C backend vet -p 1 ./...
  go -C backend test -p 1 ./... -count=1
  go -C backend test -p 1 -tags=unit ./... -count=1
  ```

- **Windows 与手册勘误。** t17 实测 `D:\software\Git\usr\bin\sh.exe` 的两个脚本退出值为 0/7。三个历史 `TestPgDumper*` 注入 `sh` 及 SQL mock，可以临时前置 Git 的 `usr\bin`，走全量原命令。手册 §1.1、§1.2 和 §10.3 均采用完整不跳过的口径。磁盘/内存不足时使用 D 盘 `GOTMPDIR`、`GOCACHE` 与串行 `-p 1`，保留所有原始失败，不从测试列表剔除用例。 [数据库测试](../../../backend/internal/repository/backup_pg_dumper_test.go#L47) · [手册命令](rollout.md#L34) · [手册清单](rollout.md#L418)
- **同树基线。** 设计期只复核 build/vet 各 0 与 handler Failover 18.610s 的脏树记录，未定位两档全仓原始结果。t17 未找到可验证的历史 HEAD 关联、完整 staged/unstaged diff、未跟踪内容及哈希或等价快照。状态计数 167/177 不能还原内容。留待隔离副本恢复同一历史树后比较，现有干净 HEAD 或局部 overlay 不能填补。 [规划基线](implementation.md#L16)
- **真实 PostgreSQL 与 Redis。** 254/255 的实际 DDL、仓储并发重复键、T3 截止与快照、P5 新旧指标兼容均待隔离验证。t25 未发现本机 Docker/PG 工具，也无数据库实跑。Redis 模拟覆盖 Lua、TTL 与并发去抖，实际部署环境验证待做。 [255 仓储测试](../../../backend/internal/repository/upstream_cost_attempt_repo_test.go)
- **冷却重选。** t28 增加同请求、同实际分组与模型的快照复用用例。多账号组首轮发现 A 待半开、B 已冷却时，A 拒绝探测后的重选继续排除 B。真正单账号组保留零额外 Redis I/O，handler 测试核对忙时唯一释放槽位。真实 Redis 验收见下节。
- **对外协议及计费。** t12 attempt 5 已修复 P3 预算正文公开 `attempts` 的冲突。B3 默认关闭，各协议沿用既有 `Retry-After` 行为。跨尝试聚合与 T6.2 结平仍待裁决。D3 的 255 成本账本已接真实发送入口，使用服务端 `execution_id` 和发送序号，关联及业务扣费各自保持原口径。 [发布口径](rollout.md#L132) · [结算规划](implementation.md#L954)
