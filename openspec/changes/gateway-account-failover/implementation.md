# 网关账号级 failover · 实现规划（implementation）

- 变更：`gateway-account-failover`
- 阶段：t4 实现规划（**只做规划，不改代码**）
- 输入依赖：
  - `openspec/changes/gateway-account-failover/recon.md`（t1 只读盘点）
  - `openspec/changes/gateway-account-failover/design.md`（t2 架构设计，方案 B）
  - `openspec/changes/gateway-account-failover/risks.md`（t3 风险审查，8 blocker / 25 high / 16 medium）
- 定位：把方案 B 拆成 **8 个可独立合并、独立灰度、独立回滚的阶段**、**26 个可直接派工的任务**，每个任务给文件级改动点、可测验收、本仓库可跑命令、复杂度与依赖。
- 本阶段不改任何代码；本文所有 `file:line` 均取自当前工作树，实施时以**符号名**定位、行号仅作参考。

---

## 0. 阅读约定与实测基线

### 0.1 实测基线（本轮真跑的，不是推断）

| 命令 | 结果 | 用途 |
| --- | --- | --- |
| `go -C backend build ./...` | exit 0 | 编译门禁基线 |
| `go -C backend vet ./...` | exit 0 | 静态门禁基线 |
| `go -C backend test ./internal/handler/... -run 'Failover' -count=1` | `ok internal/handler 18.610s`（exit 0） | 现有 failover 回归基线**绿** |

> 结论：本计划的所有验证命令以这三个为基线，后文新增命令都是它们的参数化变体，没有引入本仓库不存在的能力。
>
> **基线口径说明**：上述命令跑在当前工作树上，而该工作树**本身包含前两个阶段未提交的改动**（`git status` 有 57 个已修改文件 + 大量前序阶段新增文件，如 tickets/opsmgmt/model-pricing 相关）。因此"绿"的含义是"`HEAD` + 前序阶段工作区改动"下为绿，与 `HEAD` 单独的状态可能不同。派工时请以同一工作树为准，勿用干净 `HEAD` 对比回归数。

### 0.2 必须先知道的三个仓库事实（直接影响任务拆法）

| # | 事实 | 证据 | 对实施的影响 |
| --- | --- | --- | --- |
| A | **`service` 与 `handler` 被禁止 import `redis` 与 `repository`** | `backend/.golangci.yml` 的 `depguard` 规则 `service-no-repository` / `handler-no-repository`（`deny: redis/go-redis/v9`、`internal/repository`） | T2 冷却层的 Redis 实现**必须**落在 `backend/internal/repository/`；`service` 只定义接口（先例：`ConcurrencyCache` 接口 + `repository/concurrency_cache.go:357` 实现 + `service/wire.go:472` 注入） |
| B | **`groups` 表的既有扩列先例不经过 ent** | `groups.opsmgmt_min_margin_basis_points` 只在 `backend/internal/repository/ops_mgmt_repo.go:1150`、`:1182` 用原生 SQL 读写；`backend/internal/service/group.go:18` 的 `Group` 结构体**没有**该字段；`backend/ent/schema/group.go` 也没有 | 迁移 254 **不改 ent schema、不重跑 ent codegen**，用原生 SQL reader（T0.2）。否则会改写 `backend/ent/*.go` 30+ 生成文件，diff 噪声与冲突风险远大于收益 |
| C | **测试分两档：无 tag（默认跑）与 `//go:build unit`** | 实测比例：`internal/service` 448 个 unit / 508 个无 tag；`internal/handler` 43 unit / 101 无 tag；`internal/config` 4 unit / 4 无 tag。强制回归的 5 个文件里 4 个无 tag，`openai_gateway_usage_integrity_test.go` 带 unit tag。`Makefile:test-unit` = `go test -tags=unit ./...` | 验证命令**必须**区分：不带 `-tags=unit` 会漏跑约半数 service 侧回归。这是最容易写错的地方，本文每条命令都标注了是否带 tag |

### 0.3 编号约定

| 编号形如 | 含义 | 出处 |
| --- | --- | --- |
| `T0.1` … `T7.1` | **本计划的任务**（阶段.序号） | 本文 |
| `R-01` … `R-49` | 风险条目 | `risks.md` §二 |
| `T-01` … `T-23` | 必补测试条目 | `risks.md` §四（**与本文任务编号不同**，映射见 §4.5） |
| `F-01` / `C-01` … | 实施检查点 | `design.md` 附录 A（本文 §附录 B 追加 C-05…C-09） |

---

## 一、实施总览

### 1.1 阶段图

```
                   ┌──────────────────────────────────────────────┐
                   │ P0 地基（无调用点、零行为变更）               │
                   │ 配置/迁移/策略解析/分类器                      │
                   └───────────────┬──────────────────────────────┘
                                   │ 分类器就位（死代码）
                   ┌───────────────▼──────────────────────────────┐
                   │ P1 接线（行为等价）                          │
                   │ 4 处硬编码 → 分类器；旧实现仅存于测试          │
                   └───────────────┬──────────────────────────────┘
                                   │ 单一判定源
                   ┌───────────────▼──────────────────────────────┐
                   │ P2 判定与副作用同源 + 写状态统一  ★最高风险  │
                   │ B1 语义字节 / B2 清粘性 / R-17 门控 / 切换链  │
                   └───────┬───────────────────────┬──────────────┘
                           │                       │
        ┌──────────────────▼──────────┐   ┌────────▼──────────────────────┐
        │ P3 预算层（默认关闭）        │   │ P5 观测与终局（只读，低风险）  │
        │ 三层上限/活锁护栏/预算耗尽    │   │ 日志/指标/告警/终局聚合        │
        └──────────────────┬──────────┘   └────────┬──────────────────────┘
                           │                       │
        ┌──────────────────▼──────────┐            │
        │ P4 T2 冷却层（默认关闭）     │            │
        │ Redis 冷却/半开探测/T3 只延长│            │
        └──────────────────┬──────────┘            │
                           │                       │
        ┌──────────────────▼───────────────────────▼──────────────────────┐
        │ P6 成本与幂等审计（独立发布，默认不写成本表）                    │
        │ 成本尝试账本 / 计费指纹冻结 / 幂等禁令开关                        │
        └───────────────────────────────┬──────────────────────────────────┘
                                        │
        ┌───────────────────────────────▼──────────────────────────────────┐
        │ P7 文档与发布（手册 + 全量回归）                                  │
        └──────────────────────────────────────────────────────────────────┘
```

**风险递增顺序**：P0 < P1 < P5 < P3 < P4 < P6 < P2（P2 因含 B1/B2 两项真实行为变更故最高）。P2 之所以排在最前而非最后：**它是 P4 的前置条件**——R-17（判定与副作用解耦）不修，T2 冷却会把"handler 事后放弃的换号"也写成跨请求冷却，把 DB 误标升级为**跨请求误冷却**，影响面反而扩大。

### 1.2 阶段 → 分层 → 缺口对照

| 阶段 | 落地的设计层（design §2.1） | 解决的缺口 | 关闭的 blocker | 关联 high |
| --- | --- | --- | --- | --- |
| **P0** | L0 策略模型、L1 分类器（**未接线**） | — | — | R-34/R-48 的**前置**（输入字段就位） |
| **P1** | L1 分类器（**接线**） | G1（单一判定源） | — | R-34、R-48 |
| **P2** | L2-T1 + L3 判据 + L4 决策 | G4（审计）、G7 的一半 | **R-01、R-02、R-17** | R-09、R-18、R-39、R-42、R-44、R-45 |
| **P3** | L3 预算 | G2 | **R-24** | R-08、R-25、R-26、R-27、R-28、R-43 |
| **P4** | L2-T2（新建）+ L2-T3（只延长） | G3、G10（真实形态） | **R-30、R-31** | R-16、R-19、R-20、R-32、R-33、R-36 |
| **P5** | L4 观测 + 终局错误 | — | — | R-03、R-04、R-05、R-37、R-38、R-40 |
| **P6** | 计费/成本（design B5，未排期） | G4 的成本面 | **R-10、R-11** | R-12、R-13、R-14、R-15 |
| **P7** | 文档/手册 | — | — | R-49 |

### 1.3 与 design.md 的 4 处偏差（需 t2 追认，实施前确认）

| # | design 原文 | 本计划 | 理由 |
| --- | --- | --- | --- |
| **D1** | `max_selection_retries` 默认 `1`（design §3.1.5，且 §7.3 未把它列为行为变更） | **默认 `0`（= 现状无限清空），发布手册建议值 `1`** | design 的 §7.3 只承认 B1/B2/B3 三项默认变更，而 `1` 相对旧行为是第 4 项变更。硬约束"默认零行为变更"优先；防活锁能力用配置开启，灰度期观察后转正 |
| **D2** | 附录 A 阶段顺序：0 地基 → 1 接线 → **2 预算 → 3 可切换判据** → 4 冷却 | 把**可切换判据 + R-17 副作用门控**合并为 P2 并**前置**到预算（P3）之前 | R-17 是 blocker 且是 P4 的正确性前提（见 §1.1）。预算层不依赖判据层，但冷却层依赖 R-17 |
| **D3** | 未排期 | 新增 **P6（成本尝试账本 + 指纹冻结 + 幂等禁令）**，独立发布 | R-10/R-11 是 blocker；design 把它们记为"未来前置门禁"，但没有任务承接就无法验收。P6 默认不写、可独立关闭 |
| **D4** | 附录 A 阶段 6「更新 `config.yaml.example`」 | 仓库**不存在** `config.yaml.example`（全仓仅 `openspec/config.yaml` 是 openspec 工具配置）；改为 `config.go` 逐键中文注释 + 新增 `rollout.md` | 按不存在文件派工必然失败 |

**另 3 项实施口径补充**（不是偏差，是 design 未写清的落地细节）：

- **D5** 迁移 254 采用 opsmgmt 先例：原生 SQL，不动 ent（§0.2-B）。
- **D6** Redis 实现落 repository，service 只定义接口（§0.2-A）。
- **D7** T3 写库去抖（R-16）扩到 `handle529`/`SetOverloadUntil`，不止 design 写的 2 个调用点。

### 1.4 里程碑与出口条件

| 里程碑 | 含任务 | 出口条件（全部满足才算过） | 可独立发布？ |
| --- | --- | --- | --- |
| **M0 判定源就位** | T0.1–T0.4 | build/vet 0；分类器等价表 ≥60 行全绿；迁移测试绿；**无任何生产调用点** | ✅ 发布即"无行为变更包" |
| **M1 单一判定源生效** | T1.1–T1.2 | 4 处硬编码删除（grep 断言）；handler Failover 回归绿；service unit 回归绿 | ✅ 行为等价发布 |
| **M2 判据与决策统一** | T2.1–T2.4 | T-01/T-02/T-09/T-19 绿；`use_semantic_bytes=false` 回退路径有测试；B1/B2 有独立开关 | ⚠️ 含 B1/B2，需灰度 |
| **M3 时间可控** | T3.1–T3.3 | 预算耗尽语义正确（T-13 部分）；`total_timeout=0` 逐位等价（F-03）；T-11/T-14 绿 | ✅ 默认关闭 |
| **M4 跨请求隔离** | T4.1–T4.4 | T-08/T-15/T-16 绿；Redis 全挂 fail-open（C-02）；`cooldown.enabled=false` 零 RTT（F-02） | ✅ 默认关闭 |
| **M5 可观测与终局** | T5.1–T5.4 | T-03/T-13 绿；指标口径修正落 `account_switch_count` 注释（C-03）；2 个告警类型可触发 | ✅ 只读能力 |
| **M6 成本可见** | T6.1–T6.3 | T-04/T-05/T-06 绿；扣费条数不变 | ✅ 默认不写 |
| **M7 可交付** | T7.1 | 手册 + 全量回归 + 基线对比 | ✅ |

### 1.5 默认行为变更清单（**评审必须逐项确认**）

| # | 变更 | 关闭开关 | 默认 | 影响面 |
| --- | --- | --- | --- | --- |
| **B1** | 可切换判据：`Writer.Size()` → 语义字节 | `failover_policy.switchable.use_semantic_bytes=false` | `true` | keepalive 后现在**允许**换号 → 更多可恢复请求，也更多重复上游调用 |
| **B2** | 账号级失败后**清粘性绑定** | `failover_policy.switch_order.clear_sticky_on_account_failure=false` | `true` | 坏账号不再被同 session 反复命中；代价是切换后缓存亲和性丢失（首 token 变慢） |
| **B3** | 混合类别终局错误 → `503` + `Retry-After` | **本计划新增** `failover_policy.terminal.mixed_status_503`（design 写"无独立开关(可加)"，本计划补上） | `true` | 客户端退避策略变化（部分客户端对 502/503 处理不同）→ 见 §附录 D-Q2 |
| **BN-1** | 换号/同账号重试**退避加 ±20% jitter**（R-19） | 无开关（回滚=回滚提交） | 开启 | 只改变重试时刻分布，不改变重试次数与结果语义 |
| **BN-2** | 计费指纹**冻结为首次尝试的 body**（R-11） | 无开关（回滚=回滚提交） | 开启 | 只影响 `usage_billing_dedup.request_fingerprint` 的冲突检测，不影响"一请求一账单"唯一性 |
| **BN-3** | T3 写库**去抖 + 只延长**（R-16/R-20） | `failover_policy.t3.only_extend=false`（去抖部分无开关） | `true` | 落库次数下降、no-op 也刷快照 + 更新 reason；对外状态不变 |

> 除上表 6 项外，**本计划不含其他默认行为变更**。`cooldown.*`、`budget.*`、`idempotency.*`、`cost_attempts.*` 全部默认关闭/默认 0。

### 1.6 配置兼容基线表（新增 key 的默认值 = 现状）

> 判定标准：`默认值下，升级前后的可观测行为逐位一致`。带 ⚠️ 的是 §1.5 的显式变更项。

| key（`gateway.failover_policy.` 前缀） | 类型 | 默认 | 默认等价现状？ | 首启建议值 |
| --- | --- | --- | --- | --- |
| `enabled` | bool | `true` | ✅（等价于"分类器镜像旧硬编码"） | 保持 |
| `status_classes` | []Rule | 见 design §3.3.2 | ✅（F-01 锁定） | 保持 |
| `network_errors.enabled` | bool | `true` | ✅ | 保持 |
| `failover_on_400` | bool | `false` | ✅（旧键 `gateway.failover_on_400` 回落） | 保持 |
| `compat_400_keywords` | []string | 9 个 | ✅ | 保持 |
| `same_account.default_mode` | string | `inherit` | ✅ | 保持 |
| `budget.total_timeout` | duration | `0` | ✅ | `120s` |
| `budget.per_account_timeout` | duration | `0` | ✅ | `90s` |
| `budget.max_total_upstream_attempts` | int | `0` | ✅ | `6`（VIP 分组覆盖 `12`） |
| `budget.max_same_account_attempts_total` | int | `0` | ✅ | `4` |
| `budget.min_switch_cost` | duration | `2s` | ✅（Deadline 为零时短路） | 保持 |
| `budget.stop_clock_on_stream` | bool | `true` | ✅ | 保持 |
| `first_output_timeout_seconds` | int | `0` | ✅ | `90`（灰度观察后） |
| `cooldown.enabled` | bool | **`false`** | ✅ | 单分组开 |
| `cooldown.consecutive_failures_threshold` | int | `3` | ✅ | 保持 |
| `cooldown.streak_window` | duration | `120s` | ✅ | 保持 |
| `cooldown.default_ttl` | duration | `60s` | ✅ | 保持 |
| `cooldown.max_ttl` | duration | `24h` | ✅ | 保持 |
| `cooldown.pool_mode_enabled` | bool | `true` | ✅（`cooldown.enabled=false` 时不生效） | 保持 |
| `cooldown.probe_enabled` | bool | `true` | ✅ | 保持 |
| `cooldown.probe_max_attempts` | int | `3` | ✅ | 保持 |
| `cooldown.redis_timeout` | duration | `500ms` | ✅ | 保持 |
| `cooldown.fail_open` | bool | `true` | ✅ | **必须 true**（`false` 拒绝启动） |
| `cooldown.batch_read_max` | int | `200` | ✅ | 保持 |
| `switch_order.record_chain` | bool | `true` | ✅（纯日志） | 保持 |
| `switch_order.clear_sticky_on_account_failure` | bool | `true` | ⚠️ **B2** | 保持 |
| `max_selection_retries` | int | `0` | ✅（= 现状） | `1`（见 D1） |
| `switchable.use_semantic_bytes` | bool | `true` | ⚠️ **B1** | 首周可 `false` |
| `switchable.treat_keepalive_as_safe` | bool | `true` | ✅（B1 的子开关） | 保持 |
| `idempotency.require_key_for_side_effecting` | bool | `false` | ✅ | 按客户开 |
| `idempotency.blocked_tools` | []string | `[]` | ✅ | 按客户配 |
| `t3.only_extend` | bool | `true` | ✅（DB 已有 extend-only 守卫） | 保持 |
| `expose_failover_detail` | bool | `false` | ✅ | 保持 |
| `terminal.mixed_status_503` | bool | `true` | ⚠️ **B3**（本计划新增开关） | 保持 |
| `cost_attempts.enabled` | bool | `false` | ✅ | P6 单分组开 |

**分组级三列**（迁移 254）：`failover_on_400 BOOLEAN NULL` / `failover_max_account_switches INT NULL` / `failover_policy JSONB NULL` —— 全 `NULL` 即"回落全局"，对旧代码完全无影响（旧代码不 select 这三列）。

---

## 二、任务总表

### 2.1 汇总表

> 角色：`BE-CFG` 配置/迁移/策略；`BE-GW-A` 判据与决策（网关循环）；`BE-GW-B` 预算与终局；`BE-INF` Redis/仓库/冷却；`BE-OBS` 观测/计费；`QA-VER` 回归与验收。
> 复杂度：S ≤ 1d，M 1–3d，L > 3d（按"实现 + 测试"合计）。

| 阶段 | ID | 标题 | 角色 | 依赖 | 复杂度 | 改动文件（主） |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | T0.1 | 配置结构体 + 默认值 + Validate | BE-CFG | — | M | `backend/internal/config/config.go`（改） |
| P0 | T0.2 | 迁移 254 + groups 读取（不动 ent） | BE-CFG | T0.1 | S | `backend/migrations/254_group_failover_policy.sql`（增）、`backend/internal/repository/group_failover_policy_repo.go`（增） |
| P0 | T0.3 | 策略解析（三级覆盖 + source） | BE-CFG | T0.1, T0.2 | M | `backend/internal/service/failover_policy.go`（增） |
| P0 | T0.4 | 分类器纯函数 + 等价表（F-01） | BE-GW-A | T0.1, T0.3 | L | `backend/internal/service/failover_classifier.go`（增） |
| P1 | T1.1 | 4 处硬编码 → 分类器 | BE-GW-A | T0.4 | M | `gateway_forward.go`、`antigravity_gateway_retry.go`、`gemini_messages_compat_service.go`、`openai_gateway_upstream_errors.go`（全改） |
| P1 | T1.2 | 旧实现影子副本 + 双跑对比 | BE-GW-A | T1.1 | S | `backend/internal/service/failover_legacy_equivalence_test.go`（增） |
| P2 | T2.1 | `UpstreamWriteState` 语义字节判据（B1） | BE-GW-A | T1.1 | L | `backend/internal/service/upstream_write_state.go`（增）、`gateway_upstream_response.go`、`backend/internal/handler/gateway_handler.go`（改） |
| P2 | T2.2 | 副作用受决策门控（R-17） | BE-GW-A | T2.1 | L | `backend/internal/service/failover_decision.go`（增）、`gateway_forward.go`、`gateway_upstream_response.go`（改） |
| P2 | T2.3 | 粘性分档清除 + 抑制可见（B2） | BE-GW-A | T2.2 | M | `backend/internal/handler/gateway_handler.go`、`backend/internal/service/gateway_service.go`、`backend/internal/handler/gemini_v1beta_handler.go`（改） |
| P2 | T2.4 | `SwitchHop` 切换链 | BE-GW-A | T2.2 | M | `backend/internal/handler/failover_loop.go`、`gateway_handler.go`（改） |
| P3 | T3.1 | 预算时钟（Start/Stop/三层叠加） | BE-GW-B | T2.2 | L | `backend/internal/handler/failover_budget.go`（增）、`failover_loop.go`、`gateway_handler.go`（改） |
| P3 | T3.2 | 同账号总预算 + hard/soft 排除集 + 活锁护栏 + jitter | BE-GW-B | T3.1 | L | `backend/internal/handler/failover_loop.go`（改） |
| P3 | T3.3 | 预算耗尽语义（最小版） | BE-GW-B | T3.1 | M | `backend/internal/handler/gateway_handler.go`（改） |
| P4 | T4.1 | Redis 冷却存储（3 段 Lua + MGET + fail-open） | BE-INF | T2.2 | L | `backend/internal/service/failover_cooldown.go`（增）、`backend/internal/repository/failover_cooldown_cache.go`（增）、两处 `wire.go`（改） |
| P4 | T4.2 | 选号过滤链接入 | BE-INF | T4.1 | M | `backend/internal/service/gateway_scheduling.go`（改） |
| P4 | T4.3 | 记账顺序 + 多账号同败不冷却 + 半开探测 | BE-INF | T4.2 | L | `backend/internal/handler/failover_loop.go`、`backend/internal/service/failover_cooldown.go`（改） |
| P4 | T4.4 | T3 只延长 + 写放大收敛（含 529） | BE-INF | T4.1 | M | `backend/internal/repository/account_repo.go`、`backend/internal/service/ratelimit_service.go`、`backend/internal/service/antigravity_gateway_retry.go`（改） |
| P4 | T4.5 | `[可选]` isolation_mode observe/mark/none | BE-INF | T4.4 | M | `backend/internal/service/ratelimit_service.go`（改） |
| P5 | T5.1 | 结构化日志 `failover.attempt`/`failover.outcome` | BE-OBS | T2.4 | M | `backend/internal/handler/failover_loop.go`、`gateway_handler.go`（改） |
| P5 | T5.2 | `upstream_errors` 新 kind + 指标口径修正 | BE-OBS | T5.1 | M | `backend/internal/service/gateway_forward.go`、`backend/internal/service/ops_metrics_collector.go`（改） |
| P5 | T5.3 | 5 指标 + 2 告警 metric_type | BE-OBS | T5.2 | M | `backend/internal/service/ops_metrics_collector.go`、`ops_alert_evaluator_service.go`（改） |
| P5 | T5.4 | 终局聚合 + `Retry-After` + 协议终止事件（B3） | BE-OBS | T5.2, T3.3 | L | `backend/internal/handler/gateway_handler.go`、`openai_gateway_handler.go`、`gateway_handler_chat_completions.go`、`gateway_handler_responses.go`（改） |
| P6 | T6.1 | 成本尝试账本（迁移 255 + 写入） | BE-OBS | T5.2 | L | `backend/migrations/255_upstream_cost_attempts.sql`（增）、`backend/internal/repository/upstream_cost_attempt_repo.go`（增）、`gateway_handler.go`（改） |
| P6 | T6.2 | 计费指纹冻结 + 静默跳过改结构化（BN-2） | BE-OBS | T6.1 | M | `backend/internal/handler/gateway_handler.go`、`backend/internal/repository/usage_billing_repo.go`（改） |
| P6 | T6.3 | 幂等禁令开关 + 侧效工具识别 | BE-GW-B | T2.2 | M | `backend/internal/service/failover_idempotency.go`（增） |
| P7 | T7.1 | 文档 + 发布/回滚手册 + 全量回归 | QA-VER | 全部 | S | `openspec/changes/gateway-account-failover/rollout.md`（增）、`config.go`（注释） |

统计：**26 个任务**（25 必做 + 1 可选），P0 4 个 / P1 2 个 / P2 4 个 / P3 3 个 / P4 5 个 / P5 4 个 / P6 3 个 / P7 1 个。

> 本表为总览，为控制表格宽度，同一目录下的后续文件省略了 `backend/internal/` 前缀；**完整精确路径以 §三 各任务的「改动文件」表为准**（已核验：§三 全部为仓库相对完整路径）。

### 2.2 依赖 DAG（无环证明）

```
T0.1 → T0.2 → T0.3 → T0.4 → T1.1 → T1.2
                    T0.4 → (T1.1)
T1.1 → T2.1 → T2.2 → T2.3
                T2.2 → T2.4
                T2.2 → T3.1 → T3.2
                            T3.1 → T3.3
                T2.2 → T4.1 → T4.2 → T4.3
                            T4.1 → T4.4 → T4.5(可选)
T2.4 → T5.1 → T5.2 → T5.3
              T5.2 → T5.4（另需 T3.3）
              T5.2 → T6.1 → T6.2
T2.2 → T6.3
{T0.1..T6.3} → T7.1
```

**无环性**：所有边都满足"阶段号小的指向阶段号大的，或同阶段内序号小的指向序号大的"，唯一例外是 `T3.3 → T5.4`（跨阶段但单向）。因此拓扑序存在，无环。

**唯一的跨阶段反向前置**：`T4.x` 依赖 `T2.2`（而非同阶段），这正是 §1.1 的顺序理由——不能为"阶段整齐"把冷却排到 R-17 之前。

---

## 三、逐任务详情

> 通用约定：
> - `C-BUILD` = `go -C backend build ./...`；`C-VET` = `go -C backend vet ./...`；`C-H` = `go -C backend test ./internal/handler/... -run 'Failover' -count=1`；`C-RACE` 见 §附录 C。
> - "改动文件"里标 **增** = 新建文件，**改** = 修改既有文件。路径均相对 `D:\WorkSpace\sub2api`。
> - 每个任务的验收都写成"可失败、可单独失败"的断言；不允许"看日志正常"这类不可测标准。

### 阶段 P0：地基（无调用点、零行为变更）

#### T0.1 配置结构体 + 默认值 + Validate

- **目标**：把 design §5.1 的 34 个键落成 Go 结构体与默认值，且默认值下行为与现状逐位一致。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/config/config.go` | 改 | 新增 `GatewayFailoverPolicyConfig` + 子结构（`Budget` / `Cooldown` / `SwitchOrder` / `Switchable` / `Idempotency` / `Terminal` / `T3` / `NetworkErrors` / `SameAccount` / `CostAttempts`）；挂到 `GatewayConfig.FailoverPolicy`（`mapstructure:"failover_policy"`，先例 `GatewayConfig.Scheduling` 在 `config.go:1203`）；在默认值区（`config.go:2532` 一带）逐键 `viper.SetDefault`；在 `Validate()`（`config.go:3488` 一带）加校验 |
| `backend/internal/config/config_failover_policy_test.go` | 增 | `//go:build unit`；默认值表驱动 34 断言 + 非法值 6 例 |

- **改动点**
  1. 所有可被分组覆盖的字段用**指针类型**（`*bool`/`*int`/`*time.Duration`），`nil` = "未设置"（design §3.3.5）。全局配置对象里的字段用值类型（全局永远有值）。
  2. `Validate()` 新增：`total_timeout >= per_account_timeout`（两者都非 0 时）、`consecutive_failures_threshold >= 1`、`default_ttl <= max_ttl`、`max_total_upstream_attempts == 0 || >= 1`、`cooldown.fail_open == false` → **返回错误拒绝启动**（危险配置）、`first_output_timeout_seconds == 0 || (30..600)`。
  3. `terminal.mixed_status_503` 与 `cost_attempts.enabled` 是本计划**新增**键（§1.3-D3 / §1.5-B3），要在同一次提交里落默认值。
- **验收标准**
  - [ ] 34 个键的默认值断言全绿（表驱动，一项一行）。
  - [ ] 6 条非法值各返回含键名的 error（`strings.Contains(err.Error(), "gateway.failover_policy.")`）。
  - [ ] `cooldown.fail_open=false` 时 `Validate()` 报错。
  - [ ] 不新增/不修改任何现有键的默认值（用 `git diff` 人工确认，`max_account_switches` 等 4 个扁平键的 `SetDefault` 行不变）。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test -tags=unit ./internal/config/... -run 'FailoverPolicy' -count=1
  go -C backend vet ./...
  ```
- **注意事项**
  - 环境变量命名由 viper 自动映射（`SUB2API_GATEWAY_FAILOVER_POLICY_COOLDOWN_ENABLED`），不需要手写 `BindEnv`。
  - **不要**在这步接任何调用点（否则"零行为变更"不成立）。
- **复杂度**：M（1–2d）｜**依赖**：无

#### T0.2 迁移 254 + groups 读取（不动 ent）

- **目标**：`groups` 表加 3 个可空列，并提供"只读这三个列"的仓库方法；旧代码完全不受影响。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/migrations/254_group_failover_policy.sql` | 增 | 三列 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` + `COMMENT ON COLUMN` |
| `backend/migrations/group_failover_policy_migration_test.go` | 增 | 仿 `backend/migrations/group_force_openai_fast_migration_test.go`：从 embed FS 读文件、断言 SQL 子串 |
| `backend/internal/service/failover_policy_reader.go` | 增 | 定义 `GroupFailoverPolicyReader` 接口（service 侧，供 T0.3 依赖） |
| `backend/internal/repository/group_failover_policy_repo.go` | 增 | 原生 SQL 实现（先例 `backend/internal/repository/ops_mgmt_repo.go:1150`） |
| `backend/internal/repository/wire.go` | 改 | `ProviderSet` 加 provider |

- **改动点**
  1. SQL 内容（与 design §6.1 一致，附加 `IF NOT EXISTS` 幂等）：
     ```sql
     ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_on_400 BOOLEAN NULL;
     ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_max_account_switches INT NULL;
     ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_policy JSONB NULL;
     ```
  2. **禁止** `DEFAULT false`/`NOT NULL`（会把"未配置"变成"配置了 false"）。
  3. 读取方法签名：`LoadGroupFailoverPolicy(ctx, groupID int64) (*GroupFailoverPolicyRow, error)`，`sql.ErrNoRows` → 返回 `(nil, nil)`。
  4. **不改** `backend/ent/schema/group.go`、**不重跑** ent codegen、**不改** `backend/internal/service/group.go` 的 `Group` 结构体（§0.2-B）。
- **验收标准**
  - [ ] 迁移测试断言三列名 + `BOOLEAN NULL` / `INT NULL` / `JSONB NULL` + 两条 `COMMENT ON COLUMN`。
  - [ ] 同一 SQL 连续应用两次不报错（`IF NOT EXISTS` 生效，测试里断言语句含该子串）。
  - [ ] `git status` 中 `backend/ent/**` **零改动**（人工 + 脚本核对）。
  - [ ] 旧路径不受影响：`go build ./...` 通过，且 `backend/internal/repository/group_repo.go` 未被修改。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test ./migrations/... -run 'GroupFailoverPolicy' -count=1
  ```
- **注意事项**
  - 迁移文件由 `backend/migrations/migrations.go` 的 `//go:embed *.sql` 自动打包，无需注册；但**文件名前缀必须唯一且递增**（当前最大 `253_ops_mgmt.sql`）。
  - 迁移一旦应用不得修改（checksum 校验），因此 SQL 定稿后再提交。
- **复杂度**：S（0.5–1d）｜**依赖**：T0.1

#### T0.3 策略解析（三级覆盖 + source）

- **目标**：`account > group > global` 字段级 merge，返回 `(policy, source)`；这是 design O1 的"可配置"落地件。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/failover_policy.go` | 增 | `FailoverPolicy`（全指针字段）、`Resolve(account, group, global) (FailoverPolicy, string)`、`Legacy()` |
| `backend/internal/service/failover_policy_resolve_test.go` | 增 | `//go:build unit`，表驱动 ≥18 例 |

- **改动点**
  1. `Resolve` 是**纯函数**（无 I/O）：group 行由调用方（T2.2 的 handler 入口）先经 `GroupFailoverPolicyReader` 取好再传入；`account` 取 `credentials["failover_policy"]`（T6 之前先留空实现返回 nil）。
  2. `source` 取值 `"account" | "group" | "global"`，用于日志（design §3.8.1 `policy_source` 字段）。
  3. **旧键回落**：`gateway.failover_on_400`（旧）与新 `failover_policy.failover_on_400`（新）——新键优先；两者都 `false` → 现状（design §7.4）。
  4. `enabled=false` → 返回 `Legacy=true` 的标记，调用方据此走旧硬编码路径（为 P1 的回退路径留门）。
- **验收标准**
  - [ ] 18 例：`{account nil/有值} × {group nil/部分字段/全字段} × {global 默认/改值}`，断言生效值与 `source`。
  - [ ] "group 只写 `cooldown.enabled=true`、其余字段回落 global"用例通过（字段级 merge，不是对象级覆盖）。
  - [ ] `nil` 与"显式 false"可区分：group 写 `failover_on_400=false`（指针非 nil）时必须**覆盖** global 的 `true`。
  - [ ] `enabled=false` 时 `Legacy=true`。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test -tags=unit ./internal/service/... -run 'FailoverPolicyResolve' -count=1
  ```
- **注意事项**
  - `service` 包不能 import `repository`（§0.2-A），因此接口定义在 service、实现注入；`service/wire.go` 是 depguard 豁免文件，可在此组装。
  - 解析结果必须**每请求解析一次并缓存**（design §2.1 L0），不要在每次选号时重算。
- **复杂度**：M（1–2d）｜**依赖**：T0.1、T0.2

#### T0.4 分类器纯函数 + 等价表（F-01）

- **目标**：用 11 个 `FailureClass` 取代 4 处状态码集合，**先写成死代码并用参数化测试锁定与旧实现逐项等价**。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/failover_classifier.go` | 增 | `FailureClass` 11 类、`RetryAdvice`、`ClassifyInput`、`ClassifyUpstreamFailure` |
| `backend/internal/service/failover_classifier_equivalence_test.go` | 增 | `//go:build unit`，F-01 主表 ≥60 行 |
| `backend/internal/service/failover_classifier_ordering_test.go` | 增 | `//go:build unit`，判定顺序 6 例 |

- **改动点**
  1. 判定顺序**写死**（顺序本身是契约）：`ctx canceled` → `BoundUpstreamPassthrough` → `RetryableOnSameAccount` → `RequestScopedTransient` → 状态码类 → 默认 `request_invalid`。顺序反了就会造成三类回归：Google 间歇性 400 不再重试、provider 级 503 被算成账号问题、客户端取消被写成冷却。
  2. `ClassifyInput` 必须携带 `Stage` / `Scope` / `NextAccountAction` / `RequestScopedTransient` / `BoundUpstreamPassthrough`（R-34：规则引擎输入不能只有 status_code）。
  3. 400 拆三类：兼容关键词白名单（9 个）、其它 400、Google 间歇性 400（`RetryableOnSameAccount=true` → 归 `upstream_5xx`，design §3.3.4 的易错点）。
  4. 纯函数：**不读** Redis/DB/ctx；`Body`/`Headers` 只做只读解析。
- **验收标准**
  - [ ] **F-01**：≥60 行参数表覆盖 `{401,403,429,429+reset,529,500,502,503+transient,503,504,400,400+关键词,400+Retryable,404,409,413,422,transport-deadline,client-cancel,conn-reset}` × 4 处旧实现的 `{Failover, SameAccount}`，逐行相等。
  - [ ] 6 条顺序用例：`client_cancel` 不被分类为 `network`（对齐 `backend/internal/service/gateway_upstream_transport_error_test.go` 的既有约束）；`bound_passthru` 不判换号；`RetryableOnSameAccount` 的 400 不落 `request_invalid` 且允许同账号重试。
  - [ ] 400 三分类：白名单 400 + `failover_on_400=true` → 换号；白名单 400 + `false` → 不换号；其它 400 → 不换号。
  - [ ] 关键约束：`RequestScopedTransient=503` → `capacity_shed`、`TempUnschedule=false`、`Cooldown=0`。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test -tags=unit ./internal/service/... -run 'Classif' -count=1
  go -C backend vet ./...
  ```
- **注意事项**
  - 这一步**只加文件不接调用**；接线在 T1.1。若先接线再补测试，等价性永远说不清。
  - `shouldFailoverUpstreamError` 与 `shouldRetryUpstreamError` 是**两个不同函数、不同语义**（`gateway_forward.go:36-54`），分类器要分别建模，不能合并（design 附录 B-6）。
- **复杂度**：L（2–3d）｜**依赖**：T0.1、T0.3

---

### 阶段 P1：接线（行为等价）

#### T1.1 4 处硬编码 → 分类器

- **目标**：4 处状态码集合改为调用分类器，返回值逐项不变。
- **改动文件**

| 文件 | 性质 | 位置（当前树） | 说明 |
| --- | --- | --- | --- |
| `backend/internal/service/gateway_forward.go` | 改 | `shouldFailoverUpstreamError`（`:47-54`）、`shouldRetryUpstreamError`（`:36-44`） | 内部改调分类器；签名不变 |
| `backend/internal/service/antigravity_gateway_retry.go` | 改 | `shouldFailoverUpstreamError`（`:780` 起）、服务内重试集合（`:721`） | 同上 |
| `backend/internal/service/gemini_messages_compat_service.go` | 改 | `shouldFailoverGeminiUpstreamError`（`:1706` 起）、服务内重试集合（`:1688`） | 同上 |
| `backend/internal/service/openai_gateway_upstream_errors.go` | 改 | `shouldFailoverUpstreamError`（`:303`）、`shouldFailoverOpenAIUpstreamResponse`（`:312`） | **只读接入**：capacity shed / credential 语义作为输入传入，OpenAI 行为不变 |

- **改动点**
  1. 每个函数体改成 `class, advice := service.ClassifyUpstreamFailure(in); return advice.Failover`（或对应字段），**不保留旧 switch**。
  2. 分类器在这里是**叠加过滤器**：`decision = 既有语义 AND configRule`（R-48）。OpenAI 的 `IsCredentialFailure` / `RequestScopedTransient` 必须原样传入，不允许被状态码覆盖。
  3. `gateway.failover_policy.enabled=false` 时短路返回旧集合（回退路径，供灰度期一键退回）。
- **验收标准**
  - [ ] 4 处旧集合删除后，`rg "case 401, 403, 429, 529" backend/internal/service` **只剩分类器相关 1 处**（可写成脚本断言）。
  - [ ] 既有测试全绿（见 §五）。
  - [ ] `enabled=false` 与 `enabled=true` 在同一批既有测试下结果一致（新增 1 个对照测试跑两遍同一用例集）。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test ./internal/handler/... -run 'Failover' -count=1
  go -C backend test -tags=unit ./internal/service/... -run 'PoolModeRetry|GeminiErrorPolicy|UpstreamTransportError|OpenAIUpstream' -count=1
  go -C backend vet ./...
  ```
- **注意事项**
  - 4 个文件里 `antigravity_gateway_retry.go` 与 `gemini_messages_compat_service.go` 各有两套集合（"是否换号" vs "同账号是否重试"），**两套都要接**，否则会出现"换号对了、重试不对"的半接状态。
  - 不要顺手改 `handleRetryExhaustedError`（`:562`）的语义。
- **复杂度**：M（2d）｜**依赖**：T0.4

#### T1.2 旧实现影子副本 + 双跑对比

- **目标**：把旧判定逻辑以 **test-only** 副本保留下来做双跑，防止 T1.1 在边角状态码上漂移。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/failover_legacy_equivalence_test.go` | 增 | `//go:build unit`；`legacyShouldFailover(status int) bool` 等副本 + ≥80 行双跑表 |

- **验收标准**
  - [ ] 表 ≥80 行，覆盖 4 个入口 × 全部状态码 + 4 个语义开关组合。
  - [ ] 任一行不一致即 `t.Fatalf`，输出两侧值。
  - [ ] 副本**不进生产代码**（断言方式：`rg "func legacyShouldFailover" backend/internal --glob '!*_test.go'` 无命中）。
- **验证命令**
  ```
  go -C backend test -tags=unit ./internal/service/... -run 'LegacyEquivalence' -count=1
  ```
- **复杂度**：S（0.5d）｜**依赖**：T1.1

---

### 阶段 P2：判定与副作用同源 + 写状态统一（★最高风险，含 B1/B2）

#### T2.1 `UpstreamWriteState` 语义字节判据（B1）

- **目标**：删除"两套互斥的已写字节定义"（R-01/R-02），全链只用一个判据。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/upstream_write_state.go` | 增 | `UpstreamWriteState{SemanticBytes atomic.Int64, HeartbeatBytes atomic.Int64, StatusCommitted atomic.Bool}`；`WriteSemantic`/`WriteHeartbeat`/`CanFailover()` |
| `backend/internal/service/gateway_upstream_response.go` | 改 | 所有 SSE 写入点改走统一包装（`:855`、`:1112`、`:1167` 的 `flusher.Flush()` 附近）；`:1066` 的 `if !c.Writer.Written()` 改读状态对象 |
| `backend/internal/handler/gateway_handler.go` | 改 | `:500`/`:523`（Gemini）、`:919`/`:1061`（Messages）改读状态对象；`:2061` `gatewayForwardErrorAlreadyCommunicated` 同步 |
| `backend/internal/handler/gateway_stream_write_state_test.go` | 增 | R-01（T-01） |
| `backend/internal/handler/gateway_stream_semantic_bytes_test.go` | 增 | R-02（T-02） |
| `backend/internal/service/upstream_write_state_test.go` | 增 | `//go:build unit`，状态机单测 + C-01 并发独立 |

- **改动点**
  1. 计入语义字节：任何 SSE `data:` 行、任何非注释事件、非流式 JSON body 的非空写。不计入：`:` 注释帧、`event: ping`/`keepalive`、空行、`retry:` 行（design §3.6.2）。
  2. 判据唯一入口：`canFailover = semanticBytes == 0 || err.SafeToFailoverAfterWrite`。**禁止**任何路径直接读 `c.Writer.Written()` / `c.Writer.Size()` 做换号决策（用 grep 断言）。
  3. `use_semantic_bytes=false` → 回退旧 `Writer.Size()` 语义（两条路径都要有测试，design §3.6.6）。
  4. 状态对象**每请求一个实例**，随 request context 或 Forward 参数传递；不得用全局 `atomic`（C-01）。
  5. `treat_keepalive_as_safe=true` 时 keepalive 只进 `HeartbeatBytes`。
- **验收标准**
  - [ ] **T-01**：排队期写 ping 后，上游首字节前断流 → 断言**发生换号**，且客户端最终收到**一个**完整响应（当前会硬失败，实施时该测试应"先红后绿"）。
  - [ ] **T-02**：已写 `message_start` → 禁换号；仅写 ping → 允许换号；两例在同一判据函数下同时成立。
  - [ ] **C-01**：两个并发请求各自计数互不污染（单测 100 次并发写断言）。
  - [ ] `rg "Writer\(\)\.Written\(\)|Writer\.Size\(\)" backend/internal` 命中的每一处都有注释说明"非换号决策"（清单化的人工断言）。
  - [ ] `use_semantic_bytes=false` 时 T-01 断言反转为"禁止换号"，T-02 行为与旧版一致。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test ./internal/handler/... -run 'StreamFailover|WriteState|Semantic' -count=1
  go -C backend test -tags=unit ./internal/service/... -run 'UpstreamWriteState|Semantic' -count=1
  ```
- **注意事项**
  - 这是 **改动面最分散**的一处（design §3.6.2 自述）：先 grep 出全部写入点（`rg "Flush\(\)" backend/internal/service backend/internal/handler`），若 > 15 处，退化为 design Q5 的"减法方案"（`Size()` 减去 keepalive 累计）并在文档标注技术债。
  - `service` 不能 import `handler`：状态对象放 `service`，由 handler 创建并传入。
- **复杂度**：L（3d）｜**依赖**：T1.1

#### T2.2 副作用受决策门控（R-17，★blocker）

- **目标**：把"是否换号"与"是否标记账号"合并为**一个决策**，副作用只在该决策之后执行。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/failover_decision.go` | 增 | `FailoverDecision{Switch, MarkAccount, Cooldown, ClearSticky, Scope, Class}` + `Decide(in) FailoverDecision` |
| `backend/internal/service/gateway_forward.go` | 改 | `:729`、`:796`、`:1036` 三处 `handleFailoverSideEffects` 调用改为"只算 decision、不写副作用"；`UpstreamFailoverError` 增 `Decision` 字段 |
| `backend/internal/service/gateway_upstream_response.go` | 改 | `handleFailoverSideEffects`（`:550-557`）拆为 `buildFailoverSideEffects`（纯计算）+ `ApplyFailoverSideEffects`（执行，由 handler 调） |
| `backend/internal/service/gateway_service.go` | 改 | 暴露 `ApplyFailoverSideEffects(ctx, account, decision)`（handler 可调的服务方法） |
| `backend/internal/handler/gateway_handler.go` | 改 | 在确认"要换号"的分支调 `ApplyFailoverSideEffects`；放弃换号的分支记 `failover_suppressed{reason}` |
| `backend/internal/handler/failover_side_effect_after_write_test.go` | 增 | R-17（T-09） |
| `backend/internal/service/failover_rule_semantics_test.go` | 增 | `//go:build unit`，R-34（T-17） |

- **改动点**
  1. `Decide` 的输入 = 分类器 `RetryAdvice` + 预算状态 + `UpstreamWriteState` + `NextAccountStop`/`BoundUpstreamPassthrough`/`IsCredentialFailure`；输出 `{Switch, MarkAccount, Cooldown, ClearSticky}` 四元。
  2. 同请求同账号**只执行一次**副作用（R-18）：在 `FailoverState` 上记 `sideEffectApplied map[int64]struct{}`。
  3. `ApplyFailoverSideEffects` 只做 design §3.4.5 的记账顺序（a T1 → b 粘性 → c T2 → d T3）中的 b/c/d；T1 仍由 `HandleFailoverError` 直接写内存。
  4. **保留回退路径**：`failover_policy.enabled=false` → 走旧"Forward 内无条件标记"，一个版本后删除（§附录 D-Q4）。
- **验收标准**
  - [ ] **T-09**：已写语义字节 → handler 放弃换号 → 断言 `accounts` 行**未被修改**（当前会修改，测试应先红）。
  - [ ] **T-17**：provider 级 5xx / request-scoped 529 / 凭据 401 在同一规则下分别判为"不算账号问题 / 不算 / 算"，`MarkAccount` 三态正确。
  - [ ] 同请求同账号 4 次失败（3 次同账号重试 + 1 次换号）→ `ApplyFailoverSideEffects` 只执行 1 次（R-18）。
  - [ ] 放弃换号路径必产出一条 `failover_suppressed` 结构化日志（含 `reason ∈ {semantic_bytes, next_account_stop, bound_passthru, client_gone}`）。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test ./internal/handler/... -run 'Failover|SideEffect' -count=1
  go -C backend test -tags=unit ./internal/service/... -run 'RuleSemantics|FailoverDecision' -count=1
  go -C backend vet ./...
  ```
- **注意事项**
  - **这是 P2 的最大返工点**：`handleFailoverSideEffects` 现在在 `Forward` 内无条件调用，拆成"纯判定 + 可延后执行"会牵动 3 个调用点与 1 个函数体。若 1 天内拆不净，**降级方案**：保留 Forward 内标记但加 `Decision.MarkAccount` 门（分类器写、handler 只读），并在 handler 放弃换号时记 `failover_suppressed`（**不撤销**已写标记）→ 残留风险写进 §4.4。
  - 不要动 `ShouldReportAccountScheduleFailure`（`gateway_service.go:735`）的既有语义，它决定"是否算账号健康问题"。
- **复杂度**：L（3d）｜**依赖**：T2.1

#### T2.3 粘性分档清除 + 抑制可见（B2）

- **目标**：`Scope==account` 的失败删粘性绑定（修 G3 的一半），其余保留；被抑制的切换全部可见。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/handler/gateway_handler.go` | 改 | `:1049-1051`、`:1068-1070`（只 `ReleaseAccountSession`、不删绑定）→ 按 `Decision.ClearSticky` 删绑定 |
| `backend/internal/service/gateway_service.go` | 改 | 复用 `shouldClearStickySession`（`:560-571`），判定条件从"账号不可调度"扩展为"本次失败归因于账号" |
| `backend/internal/handler/gemini_v1beta_handler.go` | 改 | `:571-585` 的 `maxAccountSwitches=0`（缓存绑定禁换号）补 `failover_suppressed{reason=cache_bound}` |
| `backend/internal/handler/failover_exclusion_sticky_test.go` | 增 | R-45/R-44（T-20） |
| `backend/internal/handler/gemini_cache_bound_suppressed_test.go` | 增 | R-09（T-23） |

- **改动点**
  1. 四档判定（design §3.1.2c）：`account` → 删；`provider` → 保留；`request` → 保留；`bound_passthru` → 保留。
  2. 删除动作**尽力而为**：Redis 失败 fail-open 记 warn，不阻断请求。
  3. `clear_sticky_on_account_failure=false` → 完全回到现状（只 `ReleaseAccountSession`）。
- **验收标准**
  - [ ] **T-20(a)** 503 退避后利润否决账号仍在排除集、失败账号被复活（含两者混合用例）。
  - [ ] **T-20(b)** 利润否决**不写**账号状态（断言 DB 未变）。
  - [ ] **T-20(c)** 确定性失败（`Scope==account`）后粘性绑定被删除；`provider` 级失败后**仍存在**。
  - [ ] **T-23** 缓存绑定请求失败产出 `failover_decision_total{decision=suppressed, reason=cache_bound}`。
  - [ ] 选号侧零回归（粘性层级行为不变，`gateway_account_selection_test.go` 绿）。
- **验证命令**
  ```
  go -C backend test ./internal/handler/... -run 'Failover|Sticky' -count=1
  go -C backend test -tags=unit ./internal/service/... -run 'AccountSelection' -count=1
  ```
- **注意事项**
  - "删绑定"必须走 `GatewayCache` 既有 session 接口，**不新增 Redis 数据结构**（design §3.1.2c）。
  - `ReleaseAccountSession` 用的是无超时 `context.Background()`（R-23），本任务**不改**它的超时语义（属 R-23 medium，见 §4.4 不解决清单），只在其后按需删绑定。
- **复杂度**：M（1.5d）｜**依赖**：T2.2

#### T2.4 `SwitchHop` 切换链

- **目标**：让"第 N 次切到谁、为什么"可回溯（O2 / R-39），不改变选号行为。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/handler/failover_loop.go` | 改 | `FailoverState` 增 `SwitchChain []SwitchHop`、`Policy`、`SemanticBytes`、`SelectionRetryCount`；`NewFailoverState` 保持**原签名**，新增链式 `WithPolicy/WithBudget`（避免改 9 个入口） |
| `backend/internal/handler/gateway_handler.go` | 改 | 选号成功后 `AppendHop`；`HandleFailoverError` 返回后回填 `Outcome` |
| `backend/internal/handler/failover_trace_test.go` | 增 | R-39/R-42（T-19）、R-47（T-22） |

- **改动点**
  1. `SwitchHop` 字段按 design §3.1.2(a)（含 `SelectionLayer`、`Priority`、`LoadRate`、`StickyHit`、`ElapsedMs`、`Outcome`）。
  2. **不做确定性游标**（design §3.1.2 已论证）：保留重算 + 记录。若产品坚持游标，属另一变更（V4 二选一）。
  3. `record_chain=false` → 不 Append，零开销。
  4. P2 阶段先用现有 warn 日志输出链（P5 升级为结构化 attempt/outcome）。
- **验收标准**
  - [ ] **T-19**：每次尝试一条 trace，含 `candidates_before` / `excluded_size` / `decision`；被抑制的切换有 `decision=suppressed, reason`。
  - [ ] **T-22**：不做游标时，trace 必须记录候选顺序证据（"为什么是它"可解释）；断言同一失败序列两次执行的 trace 都包含候选列表长度与分层信息。
  - [ ] `record_chain=false` 时 `SwitchChain` 长度恒为 0，且热路径无额外分配（benchmark 断言 `allocs/op` 不增）。
  - [ ] `NewFailoverState` 签名未变（`rg "NewFailoverState\(" ` 的 9 个调用点零改动）。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test ./internal/handler/... -run 'Failover|Trace' -count=1
  ```
- **复杂度**：M（1d）｜**依赖**：T2.2

---

### 阶段 P3：预算层（默认关闭）

#### T3.1 预算时钟（Start/Stop/三层叠加）

- **目标**：给每个请求一个**唯一权威时钟**，彻底关掉"110 分钟"（R-24）。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/handler/failover_budget.go` | 增 | `FailoverBudget`（`Deadline`/`MaxTotalAttempts`/`MaxSameAccountTotal`/`Started`/`ClockStopped`）+ `StartClock`/`StopClock`/`Remaining`/`CanSwitch` |
| `backend/internal/handler/failover_loop.go` | 改 | `HandleFailoverError` 插入步骤 (6)：预算判定 |
| `backend/internal/handler/gateway_handler.go` | 改 | 取槽成功后 `StartClock()`（`:926-935` 一带，即 `writerSizeBeforeForward` 采样点前后）；首个语义字节 `StopClock()` |
| `backend/internal/handler/failover_budget_test.go` | 增 | R-24/R-25/R-26（T-12） |

- **改动点**
  1. `Deadline = now + total_timeout`，`total_timeout==0` → 零值 → 全链路短路（F-03）。
  2. 每次尝试前：`remaining <= 0` → `FailoverExhausted(budget)`；`remaining < min_switch_cost` → 不再切换（design §3.7.2 步骤 4）。
  3. 尝试 ctx deadline = `min(Deadline, now+per_account_timeout, now+header_timeout)`（`per_account_timeout=0` 时忽略）。
  4. **禁止**在 `c.Request.Context()` 上 `WithDeadline`（design §3.7.3：预算到期会切断已写流 → 半截响应）。
  5. **排队时间不计入**：`StartClock` 必须在取槽之后（`sticky_session_wait_timeout=120s` / `fallback_wait_timeout=30s` 不占预算）。
  6. `stop_clock_on_stream=true`：首语义字节后 `Deadline` 不再参与（流式不能因总预算被掐断）。
- **验收标准**
  - [ ] **F-03**：`total_timeout=0` 时 `failover_loop_test.go` 全绿且行为逐位一致（同一批用例在开/关预算下结果相同）。
  - [ ] **T-12**：上游全部 hang → 总耗时 ≤ 预算 + 单次尝试余量；终局 `exhausted_reason=budget`、`attempts` 正确；Gemini 路径同样受限（`max_account_switches_gemini=3` + 预算）。
  - [ ] 排队场景：模拟 60s 等槽 + 上游 1s → 预算 30s 时**不得**因排队被判超时。
  - [ ] 流式：首语义字节写入后预算到期 → 流不被切断（断言完整写完）。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test ./internal/handler/... -run 'Failover|Budget' -count=1
  ```
- **注意事项**
  - 预算耗尽必须返回**独立**错误（不是复用最后一个账号错误），否则运维无法区分"没账号"与"时间不够"（design §3.7.2）。
  - `sleepWithContext` 已响应取消（`failover_loop.go:350-359`），不要改坏（R-08）。
- **复杂度**：L（2.5d）｜**依赖**：T2.2

#### T3.2 同账号总预算 + hard/soft 排除集 + 活锁护栏 + jitter

- **目标**：消灭乘法（`(MaxSwitches+1)×(1+Retry)`）、消灭活锁、消灭同步重试风暴。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/handler/failover_loop.go` | 改 | `sameAccountRetryAllowed`（`:79-103`）加总预算；`HandleFailoverError`（`:193-267`）计数；`HandleSelectionExhausted`（`:276-319`）加 `SelectionRetryCount`；`RecordProfitVeto`（`:161-172`）拆 hard/soft；退避常量（`:38`、`:45`）加 jitter |
| `backend/internal/handler/failover_loop_no_budget_source_test.go` | 增 | R-28（T-14） |
| `backend/internal/handler/failover_backoff_jitter_test.go` | 增 | R-19（T-11） |
| `backend/internal/handler/failover_loop_test.go` | 改（追加用例） | 预算 + 排除集 |

- **改动点**
  1. `TotalAttempts` 只在**真正打到上游**时 +1（`BoundUpstreamPassthrough` 不计，design §3.2.5）。
  2. `MaxSameAccountAttemptsTotal` 是**整请求合计**（不是每账号 N 次）——这是消灭乘法的那一刀。
  3. 排除集拆两个 map：`hardExcluded`（利润否决，请求内不复活）/ `softExcluded`（失败，可被 503 退避复活）。**这是 R-43 的正解**，同时让 503 分支的清空范围可控。
  4. `max_selection_retries`（默认 `0`=现状，建议发布值 `1`）：`HandleSelectionExhausted` 每次清空 +1，超限 → `FailoverExhausted`。
  5. jitter：所有重试/换号延时 ±20%（与 `backend/internal/handler/gateway_helper.go:458-474`、`backend/internal/service/antigravity_gateway_retry.go:829-848` 的既有做法一致），**不新增配置键**（BN-1）。
- **验收标准**
  - [ ] **T-11**：同批失败的重试时刻分布极差 > 0 且不超过 ±30% 上限。
  - [ ] **T-14**：对"不推进预算的重试源 × 清空排除集"组合做穷举，断言**有限步终止**（扩展 `failover_loop_profit_veto_test.go:26-62` 的模拟循环）。
  - [ ] 上界断言：`Anthropic` 配置下 `TotalAttempts <= min(MaxSwitches+1, MaxTotalAttempts) + MaxSameAccountAttemptsTotal`（表驱动，含 4 组配置）。
  - [ ] `failover_loop_test.go:470`（同账号重试不进排除集）**仍绿**。
  - [ ] 利润否决三例（`failover_loop_profit_veto_test.go:64-82`、`:84-100`、`:102-118`）**仍绿**。
  - [ ] `max_selection_retries=0` 时 503 分支行为与现状逐位一致。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test ./internal/handler/... -run 'Failover|Jitter|SelectionExhausted' -count=1
  go -C backend vet ./...
  ```
- **注意事项**
  - jitter 会让"重试时刻"类既有测试变脆：检查是否有测试断言精确 `500ms`/`2s`；若有，改为区间断言（**允许改测试，不允许改常量语义**）。
  - `maxProfitVetoAttempts=10` 不动（C10）。
- **复杂度**：L（2.5d）｜**依赖**：T3.1

#### T3.3 预算耗尽语义（最小版）

- **目标**：让 `budget_exhausted` 成为**可判读**的独立终局（P5 再做全量聚合，这里只保证 P3 可验收）。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/handler/gateway_handler.go` | 改 | `handleFailoverExhausted`（`:1921`）识别 `reason=budget`，输出独立文案 + `x-failover-reason` 头（内部排查用，默认不外泄 body） |
| `backend/internal/handler/failover_exhausted_error_shape_test.go` | 增（先建骨架，P5 补全） | T-13 的 budget/attempts 两列 |

- **验收标准**
  - [ ] 预算耗尽 → HTTP 状态按最后失败映射、`attempts` 正确、`reason=budget` 可区分。
  - [ ] 与"账号耗尽"文案**不同**（断言响应体/头不含 `no available accounts`）。
  - [ ] `total_timeout=0` 时该分支永不进入。
- **验证命令**
  ```
  go -C backend test ./internal/handler/... -run 'FailoverExhausted|Budget' -count=1
  ```
- **复杂度**：M（1d）｜**依赖**：T3.1

---

### 阶段 P4：T2 冷却层（默认关闭）

#### T4.1 Redis 冷却存储（3 段 Lua + MGET + fail-open）

- **目标**：提供跨请求的短期避让存储；Redis 挂掉时**退化为现状**，永不导致全站 503。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/failover_cooldown.go` | 增 | `FailoverCooldownStore` 接口 + `FailoverCooldownService`（纯逻辑：TTL 计算、连续失败阈值、多账号同败判定）+ 内存实现（测试用） |
| `backend/internal/repository/failover_cooldown_cache.go` | 增 | Redis 实现：3 段 Lua（只延长不缩短 / `INCR`+`PEXPIRE` 原子 / `SETNX` 探测）+ `MGET` 批量读；先例 `backend/internal/repository/concurrency_cache.go:357`、`:949` |
| `backend/internal/repository/failover_cooldown_cache_test.go` | 增 | 用 `miniredis`（已在 go.mod）或集成 tag |
| `backend/internal/service/failover_cooldown_test.go` | 增 | `//go:build unit`，TTL/阈值/同败判定 |
| `backend/internal/repository/wire.go`、`backend/internal/service/wire.go` | 改 | provider 注册（`service/wire.go` 是 depguard 豁免文件） |

- **改动点**
  1. key 结构（design §6.2）：`failover:cooldown:acct:{id}`、`:cooldown:acct:{id}:{modelHash}`、`failover:streak:acct:{id}`、`failover:probe:acct:{id}`、`failover:cooldown_degrade:{instanceID}`；`modelHash = sha256(model)[:16]`。
  2. Lua 语义：
     - 只延长不缩短（**禁止**朴素 `SET ... EX`，那是 G10 的 Redis 版）；
     - `INCR` + 首个计数 `PEXPIRE` 必须同一脚本（否则崩溃会留下**永久 key** → 账号永久冷却，严重事故）；
     - 探测 `SET key 1 EX 10 NX` 单命令，无需 Lua。
  3. 读：`MGET` 一次批量（`batch_read_max=200` 分片），超时 `500ms` → **fail-open**（不排除任何账号）+ `failover_cooldown_degraded_total++`。
  4. 时钟以 Redis TTL 为准，**不在客户端算 `until-now`**（design §3.5.4）。
  5. `cooldown.enabled=false` → 全部方法短路，**零 Redis 调用**（F-02）。
- **验收标准**
  - [ ] Lua 三例：短 TTL 不覆盖长 TTL；`INCR` 后必有 TTL（30 次并发后 `PTTL > 0`）；探测只放行 1 个。
  - [ ] **C-02**：Redis 全挂 → 选号返回全量候选，`degraded_total` 递增，不返回 503。
  - [ ] **F-02**：`cooldown.enabled=false` 时用 mock 断言 Redis 调用次数 = **0**。
  - [ ] `fail_open=false` 时启动被 `Validate()` 拒绝（T0.1 已覆盖，这里加跨包断言）。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test -tags=unit ./internal/service/... -run 'FailoverCooldown' -count=1
  go -C backend test ./internal/repository/... -run 'FailoverCooldownCache' -count=1
  ```
- **注意事项**
  - **depguard 硬约束**（§0.2-A）：`redis` 只能出现在 `internal/repository`；`service` 侧只允许接口。违反会在 `golangci-lint run` 报 `service must not import redis`。
  - 选号热路径**不能**每个候选一次 `GET`（N 候选 = N RTT）。
- **复杂度**：L（2.5d）｜**依赖**：T2.2

#### T4.2 选号过滤链接入

- **目标**：把冷却判定插进候选过滤链，且**单账号分组零开销**。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/gateway_scheduling.go` | 改 | 在候选过滤循环（`:652-694`）**之前**对 `accounts` 做一次 `MGET`；循环内查本地 map |
| `backend/internal/service/gateway_service_selection_failure_stats_test.go` | 改 | "被冷却排除"归入新原因计数 |
| `backend/internal/service/gateway_scheduling_cooldown_test.go` | 增 | `//go:build unit` |

- **改动点**
  1. 批量读位置：`accounts` 快照就绪后、过滤链之前；`len(accounts) <= 1` 直接跳过。
  2. 排除原因参与统计：`excluded_reason=cooldown`（区分于 `rate_limit` / `profit_veto`）。
  3. 与"二次可调度性校验"（`:659-664`）并存：冷却判定在它之前（便宜先跑）。
- **验收标准**
  - [ ] 单账号分组：Redis 调用 0 次（mock 断言）。
  - [ ] `cooldown.enabled=false`：Redis 调用 0 次（F-02 的第二道断言）。
  - [ ] 3 个候选其中 1 个冷却 → 该账号不出现在候选中；全冷却 → 返回 `ErrNoAvailableAccounts`（**不**降级执行）。
  - [ ] 现有选号测试全绿（优先级/LRU/粘性/排除集/平台过滤/混合调度）。
- **验证命令**
  ```
  go -C backend test -tags=unit ./internal/service/... -run 'AccountSelection|Cooldown' -count=1
  go -C backend test -tags=unit ./internal/service/... -run 'SelectionFailureStats' -count=1
  ```
- **注意事项**
  - "全冷却 → 无可用账号"是 T2 的**最大风险面**：必须与 F-01（多账号同败不冷却）配合，否则平台级故障会瞬间清空池（design §3.4.3）。
- **复杂度**：M（1.5d）｜**依赖**：T4.1

#### T4.3 记账顺序 + 多账号同败不冷却 + 半开探测

- **目标**：跑通"失败 → 计数 → 冷却 → 半开 → 恢复"的完整状态机，且平台级故障不误冷却全池。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/handler/failover_loop.go` | 改 | 记账顺序 a(T1) → b(粘性) → c(T2) → d(T3) 固化；`ApplyFailoverSideEffects` 调用 |
| `backend/internal/service/failover_cooldown.go` | 改 | `NoteFailure`（阈值/窗口/同败判定）、`NoteSuccess`（清 streak+cooldown）、`Probe` |
| `backend/internal/service/account_isolation_state_machine_test.go` | 增 | R-30/R-32/R-33（T-15） |
| `backend/internal/service/pool_mode_isolation_test.go` | 增 | R-31（T-16） |

- **改动点**
  1. **F-01 多账号同败不冷却**：写入冷却前检查 `len(FailedAccountIDs) >= 2` → 不写（故障是平台级）。
  2. 差异化策略按 design §3.4.3 表（`auth_invalid` 60s / `quota_exhausted` reset cap 24h / `capacity_shed` 0 / 5xx 连续 3 次 / `request_invalid` 0）。
  3. 写入去抖：同账号 1s 内多次失败只写一次（`SET ... NX` 去抖标记）。
  4. 半开探测：`SET ... EX 10 NX`；探测失败最多 3 次（`probe_max_attempts`）→ 彻底清空冷却交回调度器（否则坏账号永不恢复）。
  5. `cooldown.pool_mode_enabled` 控制池模式账号是否受 T2 约束（默认 `true`；论证见 design §3.4.2：T2 不写 DB，不违约池模式契约）。
- **验收标准**
  - [ ] **T-15**：单次失败**不**摘；连续 N 次摘；冷却指数退避；半开成功恢复；半开失败再升级；手动清除可逃生。
  - [ ] **T-16**：`cooldown.pool_mode_enabled=true` → 池模式账号被冷却但 **DB 未变**；`=false` → 完全跳过（等价现状）。
  - [ ] **F-01**：同一请求内 2 个不同账号失败（平台级）→ Redis 中**无**冷却 key。
  - [ ] 记账顺序：单测注入 spy，断言 T2 写入发生在粘性删除之后、T3 之前。
- **验证命令**
  ```
  go -C backend test -tags=unit ./internal/service/... -run 'IsolationStateMachine|PoolModeIsolation|FailoverCooldown' -count=1
  go -C backend test ./internal/handler/... -run 'Failover' -count=1
  ```
- **注意事项**
  - T2 **不得**改变 `IsSchedulable()` / 管理端展示（design §3.4.7）：运维在账号列表看到的仍是"正常"。
  - 冷却 TTL 与 `streak` 窗口是两套时间，不要混用。
- **复杂度**：L（3d）｜**依赖**：T4.2

#### T4.4 T3 只延长 + 写放大收敛（含 529）

- **目标**：修 G10 的真实形态（recon 的"1 分钟覆盖长窗口"已被推翻，真问题是写放大与 reason 不更新）。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/repository/account_repo.go` | 改 | 新增 `SetTempUnschedulableIfLater`（仿 `SetRateLimitedIfLater` `:2343-2370`，含 `updated==0` 时仍 `syncSchedulerAccountSnapshot`）；`SetOverloadUntil` 加去抖 |
| `backend/internal/service/antigravity_gateway_retry.go` | 改 | `tempUnscheduleGoogleConfigError` / `tempUnscheduleEmptyResponse`（`:797-825`）改用 IfLater 变体 |
| `backend/internal/service/ratelimit_service.go` | 改 | `handle529`（`:2018-2055`）加单飞去抖（Redis `SETNX`，1 窗口 1 次） |
| `backend/internal/service/ratelimit_concurrent_529_write_amplification_test.go` | 增 | R-16（T-08） |

- **改动点**
  1. `SetTempUnschedulableIfLater` 返回值区分 `extended / no_op / (unsupported)`；`no_op` 时**仍刷新快照 + 更新 reason**（修 R-36：reason 长期停留旧值）。
  2. 529 写放大：同一账号同一窗口只落库一次（Redis 去抖，失败则退回现状）。
  3. reason 语义改为"当前生效窗口对应的原因"。
- **验收标准**
  - [ ] **T-08**：同账号 50 并发 529 → 落库 ≤1 次/窗口（或 outbox 行数有界），而非 50 次。
  - [ ] `no_op` 路径仍触发一次快照同步（spy 断言），且 reason 被更新。
  - [ ] 只延长语义：先写 10 分钟、并发再写 1 分钟 → 最终仍是 10 分钟（DB 断言）。
  - [ ] `handle529` 的最终状态与现状一致（只有写入次数变化）。
- **验证命令**
  ```
  go -C backend build ./...
  go -C backend test -tags=unit ./internal/service/... -run 'RateLimit|529|WriteAmplification' -count=1
  go -C backend test ./internal/repository/... -run 'TempUnschedulable' -count=1
  ```
- **注意事项**
  - design §3.5.6 只列 2 个调用点，本任务扩到 529（§1.3-D7）；其余 `SetTempUnschedulable` 调用点**不动**（语义是"设置状态"不是"延长冷却"）。
  - 去抖是"减少写"不是"改变状态"，属 BN-3，不改对外可见性。
- **复杂度**：M（2d）｜**依赖**：T4.1

#### T4.5 `[可选]` isolation_mode observe/mark/none

- **目标**：给 R-31 提供 design 之外的选项（`observe` = 计数不摘除）。
- **说明**：design 用 `cooldown.pool_mode_enabled` 满足 R-31 的"跨请求隔离"；risks §3.1-B4 建议更细的 `isolation_mode` 三档。**本任务默认不做**，仅当产品裁决需要"池模式仍写计数但永不摘账号"时实施。
- **改动文件**：`backend/internal/service/ratelimit_service.go`（池模式分支 `:342-349`）+ `backend/internal/config/config.go` + `pool_mode_isolation_test.go` 追加。
- **验收标准**（若实施）
  - [ ] `observe`：不写状态但 T2 streak 递增；`mark`：写状态；`none`：完全跳过（= 现状）。
  - [ ] 默认值 = `none`（零行为变更）。
- **验证命令**：`go -C backend test -tags=unit ./internal/service/... -run 'PoolModeIsolation' -count=1`
- **复杂度**：M（1d）｜**依赖**：T4.4

---

### 阶段 P5：观测与终局（只读，低风险）

#### T5.1 结构化日志 `failover.attempt` / `failover.outcome`

- **目标**：把 `:251` 的单行 warn 升级为 21 字段结构化事件 + 每请求一条终局（design §3.8.1）。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/handler/failover_loop.go` | 改 | `gateway.failover_switch_account`（`:251`）→ `gateway.failover_attempt`（21 字段） |
| `backend/internal/handler/gateway_handler.go` | 改 | 终局 `gateway.failover_outcome`（每请求最多 1 条，Info 级） |

- **验收标准**
  - [ ] 字段齐全：`request_id`/`group_id`/`platform`/`model`/`attempt_seq`/`switch_seq`/`from_account_id`/`to_account_id`/`selection_layer`/`failure_class`/`upstream_status`/`upstream_kind`/`stage`/`scope`/`same_account_retry`/`response_committed`/`semantic_bytes`/`cooldown_written`/`cooldown_ttl_ms`/`t3_written`/`sticky_cleared`/`budget_remaining_ms`/`elapsed_ms`/`policy_source`（≥21，测试用 zap observer 断言字段名集合）。
  - [ ] `outcome` 取值 ∈ `{success, exhausted, budget_exhausted, client_gone, no_candidate, policy_blocked}`。
  - [ ] 每请求 outcome 条数 ≤ 1。
  - [ ] `chain` 完整（含 `SwitchHop` 全字段）。
- **验证命令**：`go -C backend test ./internal/handler/... -run 'Failover|Outcome' -count=1`
- **复杂度**：M（1.5d）｜**依赖**：T2.4

#### T5.2 `upstream_errors` 新 kind + 指标口径修正（C-03）

- **目标**：让"打了几次上游"可聚合，并阻止 `account_switch_count` 继续被当成"切换次数"（R-37）。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/gateway_forward.go` | 改 | 写 `upstream_errors` 事件处（`:693-709`、`:730-745`、`:774-785`）新增 `kind="failover_attempt"`（带 `attempt_seq`） |
| `backend/internal/service/ops_metrics_collector.go` | 改 | `queryAccountSwitchCount`（`:568-587`）的 `IN` 列表加入新 kind（C-03），并把指标语义注释改为"可切换错误事件数" |

- **验收标准**
  - [ ] **C-03**：新 kind 加入 `IN` 列表后，`account_switch_count` 口径**不断裂**（同一 fixture 下新旧值一致，因为新 kind 不是"可切换错误"）。
  - [ ] 一次请求 3 次失败 2 次成功切换 → `upstream_errors` 新增 4 条 `failover_attempt`，`decision_total{switch}=2`，旧指标（可切换错误数）= 3（R-37 的正解，见 T5.3）。
  - [ ] 按 `request_id` 聚合能还原尝试次数。
- **验证命令**
  ```
  go -C backend test -tags=unit ./internal/service/... -run 'FailoverMetrics|AccountSwitch' -count=1
  go -C backend test ./internal/handler/... -run 'Failover' -count=1
  ```
- **注意事项**
  - 新 kind 与旧 3 个 kind **并存**，dashboard 不改（design §3.8.2：旧指标作兼容视图）；不允许"重命名旧指标"（会打断历史看板）。
- **复杂度**：M（1.5d）｜**依赖**：T5.1

#### T5.3 5 指标 + 2 告警 metric_type

- **目标**：补齐 failover 专项指标与告警类型；分组级"账号池健康"告警**无需开发**（design 附录 B-1 已核实现有 6 个 metric_type 支持分组）。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/ops_metrics_collector.go` | 改 | `failover_switch_total` / `failover_exhausted_total{reason}` / `failover_attempts_per_request` / `failover_duration_seconds` / `failover_cooldown_active_accounts` / `failover_cooldown_degraded_total` / `failover_decision_total{decision}` |
| `backend/internal/service/ops_alert_evaluator_service.go` | 改 | `evaluateRule` 两处 switch（`:447`、`:600`）各加 `failover_exhausted_ratio` / `failover_switch_ratio` |
| `backend/internal/service/ops_failover_metrics_test.go` | 增 | R-37/R-38（T-18） |

- **验收标准**
  - [ ] **T-18**：`decision_total{switch}=2` 与旧指标 3 同时成立；分组维度快照存在（`group_id` 标签）。
  - [ ] 2 个新 metric_type 可被规则引用并触发（单测构造阈值越线）。
  - [ ] 前置核实并记录：`ops_error_logs` 是否有 `group_id` 列；无则**先只做全局维度**并在文档标注（design Q6 / §附录 D-Q3）。
- **验证命令**：`go -C backend test -tags=unit ./internal/service/... -run 'FailoverMetrics|AlertEvaluator' -count=1`
- **复杂度**：M（2d）｜**依赖**：T5.2

#### T5.4 终局聚合 + `Retry-After` + 协议终止事件（B3）

- **目标**：终局错误"可判读"（R-27/R-40），流内失败不再半截（R-03/R-04），Anthropic 路径补齐 `Retry-After`。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/handler/gateway_handler.go` | 改 | `handleFailoverExhausted`（`:1921-1985`）改为 `FailoverOutcomeError` 聚合；SSE error 帧路径（`:1992-2021`）补 `message_stop` + `stop_reason`；`IsResponseCommitted` 设置（R-04） |
| `backend/internal/handler/openai_gateway_handler.go` | 改 | `copyFailoverRetryAfter`（`:3735-3745`）抽成公共函数（handler 包内） |
| `backend/internal/handler/gateway_handler_chat_completions.go`、`gateway_handler_responses.go` | 改 | `:421`、`:401` 改用公共函数 |
| `backend/internal/handler/gateway_stream_terminal_event_test.go` | 增 | R-03/R-04（T-03） |
| `backend/internal/handler/failover_exhausted_error_shape_test.go` | 改（补全） | R-27/R-40（T-13） |

- **改动点**
  1. `FailoverOutcomeError{Reason, Attempts[], FinalStatus, RetryAfter, LastErr}`；`LastErr` 保留以兼容现有 `mapUpstreamError`。
  2. 状态码选择规则（design §3.8.4 表）：全 429→429、全 5xx/network→502、全 503→503、**混合→503 + Retry-After（B3，开关 `terminal.mixed_status_503`）**、全 request_invalid→原样、budget→最后失败映射。
  3. `Retry-After` 取值：优先最大上游 reset 剩余（乐观取最短等待）→ 无则 T2 剩余 → 上限 `3600s`；`502` **不回写**（与现状一致）。
  4. 终止事件：Anthropic 补 `message_stop`；保证 `message_start` 恰好 1 次、error 帧恰好 1 个（R-04 的 `MarkResponseCommitted`）。
- **验收标准**
  - [ ] **T-03**：流内失败后 (a) 只有 1 个 error 帧；(b) 存在协议终止事件；(c) `message_start` 恰好 1 次。
  - [ ] **T-13**：全 429 / 全 5xx / 混部三种终局的状态码、`Retry-After`、`attempts`、`exhausted_reason` 各自正确（当前全 5xx 无 `Retry-After`）。
  - [ ] B3 开关关 → 回到"最后错误映射"（旧测试 `gateway_handler_error_fallback_test.go` 全绿）。
  - [ ] Anthropic 路径终局带 `Retry-After`（当前无）。
- **验证命令**
  ```
  go -C backend test ./internal/handler/... -run 'Failover|StreamTerminal|ErrorFallback' -count=1
  go -C backend vet ./...
  ```
- **注意事项**
  - **不得改** `gateway_handler_error_fallback_test.go` 的既有断言（design §3.8.5）；B3 必须新增用例，旧用例在开关关闭时保持绿。
  - `expose_failover_detail=false`（默认）不得在 body 暴露 attempts（信息泄露）。
- **复杂度**：L（2.5d）｜**依赖**：T5.2、T3.3

---

### 阶段 P6：成本与幂等审计（独立发布，默认不写）

#### T6.1 成本尝试账本（迁移 255 + 写入）

- **目标**：让"换号产生的额外上游成本"可见（R-10/R-12），**且绝不复用扣费幂等键**（V2）。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/migrations/255_upstream_cost_attempts.sql` | 增 | 新表 `upstream_cost_attempts(request_id, attempt_index, account_id, status, elapsed_ms, billed_usage, created_at)`，唯一键 `(request_id, attempt_index)` |
| `backend/migrations/upstream_cost_attempts_migration_test.go` | 增 | 断言表结构 + 唯一键 |
| `backend/internal/repository/upstream_cost_attempt_repo.go` | 增 | 批量插入（有界异步） |
| `backend/internal/handler/gateway_handler.go` | 改 | 每次尝试结束异步写一条（复用 `submitUsageRecordTask` 的有界 worker 模式 `:2566`） |
| `backend/internal/handler/gateway_failover_attempt_audit_test.go` | 增 | R-05/R-12（T-04） |

- **验收标准**
  - [ ] **T-04**：一次请求换号 3 次 → 4 条 attempt 审计记录（含 status/elapsed/decision），**扣费仍只有 1 条**。
  - [ ] `cost_attempts.enabled=false`（默认）→ 零写入、零表依赖。
  - [ ] 写入失败不影响请求（有界队列丢弃计数 + warn）。
  - [ ] 唯一键冲突不报错、不重放（静默跳过但有计数）。
- **验证命令**
  ```
  go -C backend test ./migrations/... -run 'UpstreamCostAttempts' -count=1
  go -C backend test ./internal/handler/... -run 'FailoverAttemptAudit' -count=1
  ```
- **注意事项**
  - **V2 禁令**：不得用 `(request_id, api_key_id)` 统计成本（会被扣费幂等静默吞掉）。
- **复杂度**：L（2.5d）｜**依赖**：T5.2

#### T6.2 计费指纹冻结 + 静默跳过改结构化（BN-2）

- **目标**：消除 `ErrUsageBillingRequestConflict` 造成的"计费失败（漏记）"（R-11/R-13）。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/handler/gateway_handler.go` | 改 | 指纹来源（`:944` 取 `attemptParsedReq.Body`）→ **首次尝试的原始 body**（请求进入时拍快照，`channelMapping`/`ApplyBedrockCCCompat` 改写前） |
| `backend/internal/repository/usage_billing_repo.go` | 改 | `ON CONFLICT DO NOTHING`（`:74`）静默跳过（`:77-89`）→ 结构化 warn（含 `request_id`/`attempt_index`）；partial 场景改 upsert 结平 |
| `backend/internal/service/gateway_usage_billing_conflict_test.go` | 增 | R-10/R-11（T-05） |
| `backend/internal/service/gateway_usage_billing_partial_replay_test.go` | 增 | R-13（T-06） |

- **验收标准**
  - [ ] **T-05**：(a) 同 `request_id`、body 被渠道映射改写后二次入账**不返回** `ErrUsageBillingRequestConflict`；(b) 成本表 2 条；(c) 扣费表 1 条。
  - [ ] **T-06**：先 partial usage 入账再完整重放 → 账单**结平为完整值**，跳过路径有结构化 warn。
  - [ ] 单次请求（无换号）的指纹值与改造前**一致**（回归断言，避免全量请求指纹变化）。
- **验证命令**
  ```
  go -C backend test -tags=unit ./internal/service/... -run 'UsageBilling' -count=1
  go -C backend test ./internal/handler/... -run 'Usage' -count=1
  ```
- **注意事项**
  - 指纹只参与**冲突检测**，不参与唯一性（唯一性是 `(request_id, api_key_id)`），因此改指纹源不影响"一请求一账单"。
- **复杂度**：M（2d）｜**依赖**：T6.1

#### T6.3 幂等禁令开关 + 侧效工具识别

- **目标**：给"绝对不能重复执行"的客户一个逃生门（design §3.6.4b），默认关闭。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `backend/internal/service/failover_idempotency.go` | 增 | `HasSideEffectingTool(body, blockedTools) bool` + `HasIdempotencyKey(headers) bool` |
| `backend/internal/handler/gateway_handler.go` | 改 | 决策入口：命中且无 key → `Decision.Switch=false`（仅同账号重试） |
| `backend/internal/service/failover_idempotency_test.go` | 增 | `//go:build unit` |

- **验收标准**
  - [ ] 默认 `require_key_for_side_effecting=false` → 零行为变更（开关关闭时该模块不被调用，mock 断言 0 次）。
  - [ ] 开启 + 命中 `blocked_tools` + 无 `Idempotency-Key` → `Switch=false`、仍允许同账号重试、产出 `failover_suppressed{reason=side_effect_no_key}`。
  - [ ] 请求体解析失败 → 保守判为"有副作用"（fail-closed）并记 warn。
- **验证命令**：`go -C backend test -tags=unit ./internal/service/... -run 'FailoverIdempotency' -count=1`
- **复杂度**：M（1.5d）｜**依赖**：T2.2

---

### 阶段 P7：文档与发布

#### T7.1 文档 + 发布/回滚手册 + 全量回归

- **目标**：把"怎么开、怎么观察、怎么回滚"写成人可执行的步骤，并跑一次全量回归。
- **改动文件**

| 文件 | 性质 | 说明 |
| --- | --- | --- |
| `openspec/changes/gateway-account-failover/rollout.md` | 增 | 能力开启顺序、每阶段开关、观察指标、回滚动作、迁移回滚说明 |
| `backend/internal/config/config.go` | 改 | 每个新键的中文注释（含默认值与"关闭=现状"说明） |
| `openspec/changes/gateway-account-failover/implementation.md` | 改 | 顶部状态改为"已实施"，补实际偏差 |

- **验收标准**
  - [ ] 手册含 §六 的全部内容且每阶段有"回滚一条命令"。
  - [ ] `config.go` 中每个 `failover_policy` 键都有注释（脚本断言：`rg "failover_policy\." backend/internal/config/config.go` 命中数与注释数一致）。
  - [ ] 全量回归通过（§五命令矩阵）。
- **验证命令**：见 §五命令矩阵。
- **复杂度**：S（1d）｜**依赖**：全部

---

## 四、风险对策映射

> 口径：`risks.md` 全部 49 条（8 blocker + 25 high + 16 medium）。**blocker 与 high 逐条给任务**；medium 给任务或理由；**决定不解决的，写明残留风险**。

### 4.1 blocker（8 条，全部必须闭合）

| 风险 | 任务 | 落地方式 | 该任务内的验证 |
| --- | --- | --- | --- |
| **R-01** 两套"已写字节"判据互斥 | **T2.1** | 统一 `UpstreamWriteState`，删除 `c.Writer.Written()` 做换号决策的分支 | T-01 |
| **R-02** 字节总量无法区分语义/心跳 | **T2.1** | 语义字节计数（keepalive 不计） | T-02 |
| **R-10** 换号成本在账上不可见 | **T6.1** | 新表 `upstream_cost_attempts`，键 `(request_id, attempt_index)` | T-04 |
| **R-11** 写后重放导致第二条账单冲突失败 | **T6.2** + **裁决 V1** | ① 本发布**明确不允许**"已写语义字节后换号/重放"（design §3.6.6 + V1），从根本上不触发；② 指纹冻结为首次 body 作为第二道保险 | T-05 |
| **R-17** 标记与判定解耦 | **T2.2** | 单一 `Decide` 产四元决策，副作用只在其后执行 | T-09 |
| **R-24** 无请求级预算（理论 110 分钟） | **T3.1** | `FailoverBudget` 三层叠加 + 预算耗尽即报错 | T-12 |
| **R-30** 单请求即可摘账号 | **T4.3**（T2 阈值 + 半开）+ **T4.4**（reason/只延长） | T2 提供分级隔离；**T3 保持现状**（见 §4.4 残留） | T-15 |
| **R-31** 池模式永不摘账号 | **T4.3**（`cooldown.pool_mode_enabled`）+ **T4.5**（可选 `isolation_mode`） | T2 不写 DB 即不违约池模式契约；默认 T2 关闭故默认未修复（见 §4.4） | T-16 |

### 4.2 high（25 条）

| 风险 | 任务 | 说明 |
| --- | --- | --- |
| R-03 流式只补 error 帧、无终止事件 | **T5.4** | Anthropic 补 `message_stop` + `stop_reason` |
| R-04 同一 SSE 流两个 error 帧 | **T5.4** | `MarkResponseCommitted` + 单一终止帧 |
| R-05 网关无调用级幂等 | **T6.1** + 裁决 V6 | 只做 attempt 审计（不做 Idempotency-Key 拦截）；见 §4.4 |
| R-08 客户端断开后仍在换号 | **T3.1**（预算响应取消）+ 既有约束 | 保持 `ctx` 取消即终止；新增 T-12 中的取消用例 |
| R-12 失败尝试 `result=nil` 成本黑洞 | **T6.1** | 每尝试一条成本记录（含 status/耗时） |
| R-13 partial 入账 + 重放静默吞账单 | **T6.2** | 结平 upsert + 结构化 warn |
| R-16 529 写放大 / outbox 惊群 | **T4.4** | 529 单飞去抖 + 只延长 + no-op 仍刷快照 |
| R-18 同账号重试放大标记 N 倍 | **T2.2**（请求内去重）+ **T4.4**（写库去抖） | 同请求同账号只执行一次副作用 |
| R-19 无 jitter 的同步重试风暴 | **T3.2** | 全部退避 ±20% |
| R-20 extend-only 守卫吞掉更新 | **T4.4** | 返回值三态 + no-op 仍刷快照/reason |
| R-25 三层乘算最坏 220 次 | **T3.1** + **T3.2** | `TotalAttempts` 统一口径（含 service 层 5 次重试的显式封顶说明） |
| R-26 Gemini 最坏 20 次 | **T3.1** + **T3.2** | 预算层收敛，不改 `max_same_account_retries_gemini` 默认值 |
| R-27 预算耗尽无语义 | **T3.3** + **T5.4** | 独立 `reason=budget` + 聚合 attempts + `Retry-After` |
| R-28 不推进预算的重试源 = 活锁 | **T3.2** | hard/soft 排除集 + `max_selection_retries` + T-14 穷举 |
| R-32 恢复路径不对称 | **T4.3** | 状态机：计数→冷却→半开→恢复/再升级 |
| R-33 冷却固定 1 分钟且只覆盖 2 类错误 | **T4.3** + **T4.4** | 按类 TTL + 阈值 + 半开 |
| R-34 `NextAccountStop`/凭据级优先级未定义 | **T0.4**（输入含 `Stage/Scope`）+ **T2.2**（决策同源） | 规则是叠加过滤器，不替代语义字段 |
| R-37 `account_switch_count` 名不符实 | **T5.2** + **T5.3** | 新 `decision_total`；旧指标保留但语义注释修正 |
| R-38 无分组维度 | **T5.3** | 2 个新 metric_type + 分组标签；受 Q6 前置影响（见 §4.4） |
| R-39 无切换链、选号含随机 | **T2.4** | `SwitchHop[]` 记录候选/排除/层/优先级 |
| R-40 终局无耗尽原因 | **T5.4** | `exhausted_reason` + `attempts` |
| R-43 两套否决混用同一排除集 | **T3.2** | `hardExcluded` / `softExcluded` 拆分 |
| R-44 过度停用（标记 + 利润排除双杀） | **T2.2**（利润否决不写状态）+ **T2.4**（记录 reason） | 用测试钉死"利润否决不写账号状态" |
| R-45 永不停用（池模式 + 503 复活 + 粘性不清） | **T2.3**（粘性分档）+ **T4.3**（冷却闭环） | 三处同时修才闭合 |
| R-48 69 处构造点导致行为分叉 | **T0.4** + **T1.1** + 裁决 V8 | 分批收敛：本发布只覆盖 `FailoverState` 系；OpenAI/生图/AlphaSearch/Grok 媒体不在范围（§4.4） |

### 4.3 medium（16 条）

| 风险 | 任务 / 理由 |
| --- | --- |
| R-06 `SafeToFailoverAfterWrite` 前提不存在于 Anthropic | **T2.1** 判据中显式：Anthropic 无 staging → 该字段在 Anthropic 路径恒视为 `false`（V1） |
| R-07 WS 换号覆盖上一账号字节 | **不解决**（本发布不含 WS 路径改造，N1 范围外）；残留：WS 会话仍可能拼接输出，交接给后续变更 |
| R-09 缓存绑定禁换号不可观测 | **T2.3**（`failover_suppressed{reason=cache_bound}`） |
| R-14 usage 提交有界队列丢单 | **T6.1** 顺带加丢弃计数（`cost_attempts` 与 usage worker 同模式）；不承诺零丢单 |
| R-15 计费完整性被当账号故障 | **T0.4**：`grok_missing_usage` 归 `policy_blocked`/`request_invalid`，不参与账号健康（T-07 断言） |
| R-21 `model_rate_limits` 无合并写 | **T4.4** 的写放大收敛顺带覆盖（同账号同模型窗内去抖） |
| R-22 换号槽位释放零测试 | **T3.1** 的测试批次中新增 `gateway_failover_slot_release_test.go`（T-10） |
| R-23 会话释放无超时 | **不解决**（`context.Background()` 改异步清理属另一变更）；残留：Redis 抖动仍可能阻塞换号热路径 |
| R-29 Anthropic 无首字节语义超时 | **T3.1** 提供 `first_output_timeout_seconds`（默认 0=关），由运维按模型开 |
| R-35 模型级故障摘整账号 | **T4.1/T4.3**：`modelHash` 级冷却 key 已备，仅 `rate_limited`/`quota_exhausted` 且上游明确指模型时使用 |
| R-36 reason 停留旧值 | **T4.4** |
| R-41 日志/存储按尝试数放大 | **T5.1**（每请求 1 条 outcome）+ **T5.2**（事件按 request 聚合上限）；换号过程降 debug 见 §附录 D-Q5 |
| R-42 被抑制的切换不可见 | **T2.2** + **T2.3**（`failover_suppressed` + `decision=suppressed`） |
| R-46 换号改变计费口径 vs 利润判定时点 | **不解决**（属利润门口径变更）；记录为已知差异，`failover_force_cache_billing_profit_test.go`（T-21）先钉现状 |
| R-47 无"同组下一个"稳定定义 | **裁决 V4**：不引入游标，用 trace 解释（T2.4/T-22） |
| R-49 配置漂移无校验 | **T0.1**（`Validate()`）+ **T0.3**（唯一解析顺序 + `source`）+ **T5.1**（`policy_source` 落日志） |

### 4.4 明确"不解决"或"条件解决"清单（含残留风险）

| # | 风险 / 目标 | 决定 | 理由 | 残留风险（必须写进发布说明） |
| --- | --- | --- | --- | --- |
| N-1 | **R-30 的 T3 部分**（单次 429/529 仍立即写 DB） | 不解决 | T3 所有权不变（design §3.4.2 N7）；改 T3 阈值会改变管理端可见状态与 ops 统计口径，属产品决策 | 单次抖动仍可能把账号摘出池；缓解只有 T2（默认关闭） |
| N-2 | **R-31 在默认配置下未修复** | 条件解决 | `cooldown.enabled=false` 是"零行为变更"的前提（design §4.3 诚实取舍 2） | 池模式坏账号在默认配置下仍会被反复命中；修复路径=开启 T2（手册阶段 4） |
| N-3 | **R-05 上游调用幂等** | 不解决（只做审计） | V6：网关加 `Idempotency-Key` 会改变历史客户端语义，且骗不了上游（调用已发生） | 换号仍产生真实重复上游调用；只能被 T6.1 计量、不能消除 |
| N-4 | **R-07 WS 换号** / **R-48 的 4 套内联实现** / **N1** | 不解决（范围外） | V8 分批收敛：本发布只做 `FailoverState` 系（Anthropic/Gemini/Antigravity/chat/responses/v1beta/SystemOne） | OpenAI 网关/生图/AlphaSearch/Grok 媒体仍是旧行为；跨入口判定仍可能分叉 |
| N-5 | **R-38 的分组维度指标** | 条件解决 | 依赖 Q6：`ops_error_logs` 是否有 `group_id`；无列则先只做全局 | 分组级"耗尽率"告警可能缺席一个版本 |
| N-6 | **R-23 会话释放超时** | 不解决 | 改异步清理队列会动换号主流程时序，风险大于收益 | Redis 抖动时换号热路径可能被串行阻塞（毫秒~秒级） |
| N-7 | **R-46 换号带来的计费口径差** | 不解决 | 属利润门口径变更，需产品裁决 | 可能出现"利润门放行但实际毛利更差" |
| N-8 | **R-16 的"Redis 先写 + 异步合并落库"完整方案** | 部分解决 | 本计划只做去抖 + 只延长（改动小、可回滚）；完整合并写需要改 outbox 契约 | 高并发抖动下写库次数仍可能 >1/窗口 |
| N-9 | **B3 默认开启** | 待裁决 | 需确认无客户端依赖 502（§附录 D-Q2） | 若裁决为"保留 502"，则 `terminal.mixed_status_503=false` |
| N-10 | **`use_semantic_bytes` 默认开启（B1）** | 待裁决 | design Q9 的运行时前提已由 risks 静态判定（ping 同 goroutine），但线上行为差异需灰度验证 | 首周可能"更多请求可恢复"→ 上游调用数上升，成本需盯 |

### 4.5 risks 必补测试 → 任务归属

| 测试 | 风险 | 归属任务 | 测试文件 |
| --- | --- | --- | --- |
| T-01 | R-01 | T2.1 | `backend/internal/handler/gateway_stream_write_state_test.go` |
| T-02 | R-02 | T2.1 | `backend/internal/handler/gateway_stream_semantic_bytes_test.go` |
| T-03 | R-03/R-04 | T5.4 | `backend/internal/handler/gateway_stream_terminal_event_test.go` |
| T-04 | R-05/R-12 | T6.1 | `backend/internal/handler/gateway_failover_attempt_audit_test.go` |
| T-05 | R-10/R-11 | T6.2 | `backend/internal/service/gateway_usage_billing_conflict_test.go` |
| T-06 | R-13 | T6.2 | `backend/internal/service/gateway_usage_billing_partial_replay_test.go` |
| T-07 | R-15 | T0.4 | `backend/internal/service/openai_usage_integrity_no_failover_test.go` |
| T-08 | R-16 | T4.4 | `backend/internal/service/ratelimit_concurrent_529_write_amplification_test.go` |
| T-09 | R-17 | T2.2 | `backend/internal/handler/failover_side_effect_after_write_test.go` |
| T-10 | R-22 | T3.1 | `backend/internal/handler/gateway_failover_slot_release_test.go` |
| T-11 | R-19 | T3.2 | `backend/internal/handler/failover_backoff_jitter_test.go` |
| T-12 | R-24/R-25/R-26 | T3.1 | `backend/internal/handler/failover_budget_test.go` |
| T-13 | R-27/R-40 | T3.3（骨架）+ T5.4（补全） | `backend/internal/handler/failover_exhausted_error_shape_test.go` |
| T-14 | R-28 | T3.2 | `backend/internal/handler/failover_loop_no_budget_source_test.go` |
| T-15 | R-30/R-32/R-33 | T4.3 | `backend/internal/service/account_isolation_state_machine_test.go` |
| T-16 | R-31 | T4.3 | `backend/internal/service/pool_mode_isolation_test.go` |
| T-17 | R-34 | T2.2 | `backend/internal/service/failover_rule_semantics_test.go` |
| T-18 | R-37/R-38 | T5.3 | `backend/internal/service/ops_failover_metrics_test.go` |
| T-19 | R-39/R-42 | T2.4 | `backend/internal/handler/failover_trace_test.go` |
| T-20 | R-43/R-44/R-45 | T2.3 | `backend/internal/handler/failover_exclusion_sticky_test.go` |
| T-21 | R-46 | T7.1（钉现状） | `backend/internal/service/failover_force_cache_billing_profit_test.go` |
| T-22 | R-47 | T2.4 | `backend/internal/service/account_selection_order_determinism_test.go` |
| T-23 | R-09 | T2.3 | `backend/internal/handler/gemini_cache_bound_suppressed_test.go` |

**覆盖率**：23/23 有归属；其中 T-21（R-46，medium）只"钉现状"，不修。

---

## 五、回归保护清单

### 5.1 强制回归（**5 个必过文件**，任一红即不得合并）

| 文件 | 锁定的约束 | 会被哪些任务影响 |
| --- | --- | --- |
| `backend/internal/handler/failover_loop_test.go` | 同账号重试不进排除集（`:470`）、切换计数语义、503 退避、取消 | T2.2、T3.1、T3.2、T4.3 |
| `backend/internal/service/gateway_pool_mode_retry_test.go` | 池模式同账号重试与状态码集合 | T1.1、T0.4 |
| `backend/internal/handler/gateway_handler_stream_failover_test.go` | **写字节后必须放弃换号**（`:26-140`）——B1 的硬约束 | T2.1、T2.2 |
| `backend/internal/handler/gateway_usage_integrity_test.go` → 实际文件 **`backend/internal/service/openai_gateway_usage_integrity_test.go`** | 计费完整性不触发换号 | T0.4、T6.1 |
| `backend/internal/handler/failover_loop_profit_veto_test.go` | 利润否决留排除集、`maxProfitVetoAttempts`；`:64-82`/`:84-100`/`:102-118` 活锁回归 | T3.2、T2.3 |

> ⚠️ **路径勘误**：任务书写的 `gateway_usage_integrity_test.go` 在仓库中不存在；实际文件名是 `backend/internal/service/openai_gateway_usage_integrity_test.go`（带 `//go:build unit`）。派工时以此路径为准。

### 5.2 全量既有测试清单（design 附录 C 的 13 个文件 + 追加）

| # | 文件 | 标签 | 受哪个任务影响 |
| --- | --- | --- | --- |
| 1 | `backend/internal/handler/failover_loop_test.go` | 无 | T2.2/T3.1/T3.2 |
| 2 | `backend/internal/handler/failover_loop_profit_veto_test.go` | 无 | T3.2 |
| 3 | `backend/internal/handler/gateway_handler_stream_failover_test.go` | 无 | T2.1 |
| 4 | `backend/internal/handler/gateway_handler_error_fallback_test.go` | 无 | T5.4 |
| 5 | `backend/internal/service/gateway_pool_mode_retry_test.go` | 无 | T1.1 |
| 6 | `backend/internal/handler/openai_gateway_credential_failover_test.go` | **unit** | T5.4 |
| 7 | `backend/internal/service/gateway_account_selection_test.go` | **unit** | T2.3/T4.2 |
| 8 | `backend/internal/service/gateway_service_selection_failure_stats_test.go` | 无 | T4.2 |
| 9 | `backend/internal/service/gateway_channel_restriction_fallback_test.go` | 无 | T4.2 |
| 10 | `backend/internal/handler/openai_gateway_ws_failover_resume_test.go` | 无 | 不变（范围外） |
| 11 | `backend/internal/service/scheduler_snapshot_hydration_test.go` | 无 | 不变（不动快照） |
| 12 | `backend/internal/service/gemini_error_policy_test.go` + `_single_check` + `_skipped_write` | **unit** | T1.1 |
| 13 | `backend/internal/service/openai_first_output_timeout_test.go` | 无 | T3.1 |
| 14 | `backend/internal/service/gateway_upstream_transport_error_test.go` | **unit** | T0.4/T1.1（**关键**：客户端取消不标记 `:182`、上游 deadline 不标记 `:203`、瞬态不标记 `:68`） |
| 15 | `backend/internal/service/gateway_scheduling_slot_release_test.go` | 无 | T3.1 |
| 16 | `backend/internal/migrations/ops_mgmt_migration_test.go` | 无 | T0.2（跨包只读断言的范式） |
| 17 | `backend/migrations/*_migration_test.go`（23 个） | 无 | T0.2/T6.1（新增迁移不能破坏既有 SQL 断言） |

### 5.3 命令矩阵（本仓库真跑，按需组合）

| 目的 | 命令（可直接复制，无转义） |
| --- | --- |
| 编译 | `go -C backend build ./...` |
| 静态检查 | `go -C backend vet ./...` |
| **默认 tag** 的 handler 回归 | `go -C backend test ./internal/handler/... -run 'Failover' -count=1` |
| **unit tag** 的 service 回归 | 见 §附录 C 第 3 条（正则含 `|`，放在代码块里才能原样复制） |
| 配置默认值 | `go -C backend test -tags=unit ./internal/config/... -run 'FailoverPolicy' -count=1` |
| 迁移 | 见 §附录 C 第 5 条（`GroupFailoverPolicy` 与 `UpstreamCostAttempts` 分两次跑） |
| 仓库层 | 见 §附录 C 第 6 条 |
| 全量单测（提交前） | `go -C backend test -tags=unit ./...`（= `Makefile:test-unit`） |
| 数据竞争（可选，需 CGO） | 见 §附录 C 第 8 条 |
| 前端（**本变更无前端改动**，合并前可选确权） | `npx --yes pnpm@10.24.0 -C frontend typecheck` |

> 为什么不用一条超长正则：Markdown 表格里的 `|` 必须转义成 `\|`，**复制出来是错的**（Go 的 `-run` 是正则，`\|` 表示字面管道符而非"或"）。因此含 `|` 的命令一律放在 §附录 C 的代码块里，可原样复制。

### 5.4 基线（本轮实测，作为"不许变红"的对照）

| 命令 | 基线 |
| --- | --- |
| `go -C backend build ./...` | exit 0 |
| `go -C backend vet ./...` | exit 0 |
| `go -C backend test ./internal/handler/... -run 'Failover' -count=1` | `ok internal/handler 18.610s` |

> 说明：P0/P1 是"行为等价"阶段，其出口条件应包含**同一批命令的耗时与用例数不发生数量级变化**（`-v` 计数对比），以防"测试被悄悄删掉"。

---

## 六、上线与回滚

### 6.1 总体灰度顺序（每阶段独立发版）

```
发 P0（无调用点）→ 发 P1（等价）→ 发 P5（只读观测）→ 发 P3（预算，默认关）
   → 发 P4（冷却，默认关）→ 发 P6（成本表，默认不写）→ 发 P2（判据+决策，B1/B2 灰度）
   → 发 P7（文档收尾）
```

> 与 §1.1 的编码顺序不同：**编码**按依赖（P2 先于 P3/P4），**发版**按风险（P5/P3/P4 先于 P2）。P2 的代码可在 P3/P4 之后再上线，因为它只改判据与决策归属，默认回退路径完整。

### 6.2 每阶段：上线动作 / 观察指标 / 回滚

| 阶段 | 上线动作 | 灰度 | 观察指标（≥3 天） | 回滚动作 |
| --- | --- | --- | --- | --- |
| **P0** | 发布 + 迁移 254（三列 NULL） | 全量（无行为） | `build/vet` 绿；`groups` 表结构正确；无新日志 | 无需回滚（旧代码忽略新列）；如需干净回退可 revert 提交 |
| **P1** | 发布（分类器接线） | 全量（等价） | `gateway.failover_switch_account` 计数与上周同期偏差 < 5%；上游错误码分布不变 | `gateway.failover_policy.enabled=false`（走旧硬编码，无需发版） |
| **P5** | 发布（日志/指标/终局） | 全量（只读） | `failover_attempt` 日志量 ≤ 旧 warn 的 1.2 倍；新指标可查；2 个告警可触发 | 关 `terminal.mixed_status_503`（仅 B3）；其余只读能力可留 |
| **P3** | 发布（预算） | 先 1 个低流量分组 3 天 | `failover_exhausted_total{reason=budget}` 占比 < 0.5%；P99 不升；`attempts_per_request` 分布左移 | `budget.total_timeout=0`、`max_total_upstream_attempts=0`（无需发版） |
| **P4** | 发布（T2 冷却） | 先 1 个低流量分组 3–7 天 | `failover_cooldown_degraded_total == 0`（**硬性**）；分组可用账号数不降；`failover_exhausted_ratio` 不上行 | `cooldown.enabled=false`（无需发版）；Redis 故障自动 fail-open |
| **P6** | 发布 + 迁移 255 | 全量（默认不写） | 表增长速率符合"请求数 × 尝试数"预期；扣费条数不变 | `cost_attempts.enabled=false`；表可保留（无害） |
| **P2** | 发布（判据/决策） | **分两步**：① `use_semantic_bytes=false` 上线，只启用 R-17 门控与切换链；② 观察 1 周后开 `use_semantic_bytes=true` | ① 上游调用总数不变、`failover_suppressed` 出现且可解释；② 切换成功率上升、上游调用数变化（预期小幅上升）、扣费条数不变 | `switchable.use_semantic_bytes=false`、`clear_sticky_on_account_failure=false`；`enabled=false` 回旧路径 |
| **P7** | 文档 | — | — | — |

### 6.3 迁移回滚

| 迁移 | 可回滚？ | 说明 |
| --- | --- | --- |
| 254（`groups` 三列） | **不回滚**（无害） | 三列全 `NULL`，旧代码不 select 它们（`§0.2-B` 的原生 SQL reader 仅新代码使用） |
| 255（`upstream_cost_attempts`） | 可回滚 | `DROP TABLE IF EXISTS`；但**先确认无面板/报表依赖** |

### 6.4 观察指标看板（发布期常开）

| 指标 | 阈值/告警 | 含义 |
| --- | --- | --- |
| `failover_cooldown_degraded_total` | **> 0 即 P1 告警** | T2 失效（Redis 故障），等价于 `cooldown.enabled=false` |
| `failover_exhausted_total{reason}` | `budget` 占比 > 1% → P2 告警 | 预算过紧 |
| `failover_exhausted_ratio` | `>= 5%` 持续 10min → **P0** | 分组容灾退化（design §3.8.3） |
| `failover_switch_ratio` | `>= 30%` 持续 15min → P1 | 账号池在退化 |
| `account_switch_count`（旧） | 仅作对照，不告警 | 语义=可切换错误事件数（R-37） |
| `group_available_ratio`（已有） | `< 20` 持续 10min → P0 | 分组账号池濒临耗尽（**无需开发**，design 附录 B-1） |

---

## 七、工作量估算与并行建议

### 7.1 工作量（人日，"实现 + 测试"合计）

| 阶段 | 任务 | 人日 |
| --- | --- | --- |
| P0 | T0.1(1.5) + T0.2(1) + T0.3(1.5) + T0.4(2.5) | **6.5** |
| P1 | T1.1(2) + T1.2(0.5) | **2.5** |
| P2 | T2.1(3) + T2.2(3) + T2.3(1.5) + T2.4(1) | **8.5** |
| P3 | T3.1(2.5) + T3.2(2.5) + T3.3(1) | **6** |
| P4 | T4.1(2.5) + T4.2(1.5) + T4.3(3) + T4.4(2) [+T4.5(1) 可选] | **9（+1）** |
| P5 | T5.1(1.5) + T5.2(1.5) + T5.3(2) + T5.4(2.5) | **7.5** |
| P6 | T6.1(2.5) + T6.2(2) + T6.3(1.5) | **6** |
| P7 | T7.1(1) | **1** |
| **合计** | 25 必做 + 1 可选 | **47 人日（+1）** |

### 7.2 关键路径（不可压缩）

```
T0.1(1.5) → T0.3(1.5) → T0.4(2.5) → T1.1(2) → T2.1(3) → T2.2(3)
  → T4.1(2.5) → T4.2(1.5) → T4.3(3) → T5.4(2.5) → T7.1(1)
= 23.5 人日
```

其余任务（T2.3、T2.4、T3.x、T5.1–T5.3、T6.x、T4.4）都有关键路径之外的并行余量。

### 7.3 并行与角色负载

| 角色 | 任务 | 人日 | 建议 |
| --- | --- | --- | --- |
| **BE-CFG** | T0.1、T0.2、T0.3 | 4 | 1 人，P0 前 3 天完成即释放 |
| **BE-GW-A** | T0.4、T1.1、T1.2、T2.1、T2.2、T2.3、T2.4 | 13.5 | **必须 2 人**（T2.1/T2.2 是最重的两块，各 3d） |
| **BE-GW-B** | T3.1、T3.2、T3.3、T6.3 | 7.5 | 1 人，可在 T2.2 后启动（T3.x 只依赖 `Decision` 接口，可先对接口编程） |
| **BE-INF** | T4.1、T4.2、T4.3、T4.4、T4.5 | 10 | 1 人，最早在 T2.2 交付 `Decision` 接口后启动 |
| **BE-OBS** | T5.1–T5.4、T6.1、T6.2 | 12 | 1–2 人；T5.1 只依赖 T2.4，可与 P4 并行 |
| **QA-VER** | T7.1 + 各阶段回归门 | 3 | 与开发并行，每阶段出口跑 §五 矩阵 |

**推荐编排（3 人并行，日历约 4 周）**

```
第 1 周：BE-CFG(T0.1,T0.2,T0.3) ‖ BE-GW-A(T0.4 先写表) ‖ QA(基线固化)
第 2 周：BE-GW-A(T1.1,T1.2,T2.1) ‖ BE-OBS(T5.1 骨架) ‖ QA(P0/P1 回归)
第 3 周：BE-GW-A(T2.2) → BE-GW-B(T3.1,T3.2,T3.3) ‖ BE-INF(T4.1 起步) ‖ BE-OBS(T5.2,T5.3)
第 4 周：BE-INF(T4.2,T4.3,T4.4) ‖ BE-GW-A(T2.3,T2.4) ‖ BE-OBS(T5.4,T6.x) ‖ QA(T7.1)
```

### 7.4 与 design 估算的差异（诚实说明）

| 项 | design §4.2 | 本计划 | 差异原因 |
| --- | --- | --- | --- |
| 实施周期 | 7–9 天 | **47 人日**（≈ 单人 9–10 周；3 人并行约 4 周） | design 的估算只覆盖"实现"，未计：① 23 条必补测试（risks §四）；② 迁移 + 迁移测试；③ P6 成本账本（design 未排期）；④ 文档/手册。按本项目既有惯例（实现:测试 ≈ 1:1）核算 |

> 若把 P6 与 T4.5 移出本变更，规模降到 **40 人日**；若同时把 P5 的告警维度（T5.3）延后，降到 **38 人日**。

---

## 附录 A：新增文件清单（按阶段，便于派工与 code review 分片）

| 阶段 | 新增文件 | 类型 |
| --- | --- | --- |
| P0 | `backend/internal/config/config_failover_policy_test.go` | 测试 |
| P0 | `backend/migrations/254_group_failover_policy.sql`、`backend/migrations/group_failover_policy_migration_test.go` | 迁移 + 测试 |
| P0 | `backend/internal/service/failover_policy_reader.go`、`backend/internal/repository/group_failover_policy_repo.go` | 实现 |
| P0 | `backend/internal/service/failover_policy.go`、`failover_policy_resolve_test.go` | 实现 + 测试 |
| P0 | `backend/internal/service/failover_classifier.go`、`failover_classifier_equivalence_test.go`、`failover_classifier_ordering_test.go` | 实现 + 测试 |
| P1 | `backend/internal/service/failover_legacy_equivalence_test.go` | 测试 |
| P2 | `backend/internal/service/upstream_write_state.go`、`upstream_write_state_test.go` | 实现 + 测试 |
| P2 | `backend/internal/service/failover_decision.go` | 实现 |
| P2 | `backend/internal/handler/gateway_stream_write_state_test.go`、`gateway_stream_semantic_bytes_test.go`、`failover_side_effect_after_write_test.go`、`failover_exclusion_sticky_test.go`、`gemini_cache_bound_suppressed_test.go`、`failover_trace_test.go` | 测试 |
| P2 | `backend/internal/service/failover_rule_semantics_test.go` | 测试 |
| P3 | `backend/internal/handler/failover_budget.go`、`failover_budget_test.go`、`failover_backoff_jitter_test.go`、`failover_loop_no_budget_source_test.go`、`gateway_failover_slot_release_test.go`、`failover_exhausted_error_shape_test.go` | 实现 + 测试 |
| P4 | `backend/internal/service/failover_cooldown.go`、`failover_cooldown_test.go` | 实现 + 测试 |
| P4 | `backend/internal/repository/failover_cooldown_cache.go`、`failover_cooldown_cache_test.go` | 实现 + 测试 |
| P4 | `backend/internal/service/account_isolation_state_machine_test.go`、`pool_mode_isolation_test.go`、`ratelimit_concurrent_529_write_amplification_test.go`、`gateway_scheduling_cooldown_test.go` | 测试 |
| P5 | `backend/internal/service/ops_failover_metrics_test.go`、`openai_usage_integrity_no_failover_test.go` | 测试 |
| P5 | `backend/internal/handler/gateway_stream_terminal_event_test.go` | 测试 |
| P6 | `backend/migrations/255_upstream_cost_attempts.sql`、`backend/migrations/upstream_cost_attempts_migration_test.go` | 迁移 + 测试 |
| P6 | `backend/internal/repository/upstream_cost_attempt_repo.go` | 实现 |
| P6 | `backend/internal/service/failover_idempotency.go`、`failover_idempotency_test.go` | 实现 + 测试 |
| P6 | `backend/internal/handler/gateway_failover_attempt_audit_test.go`、`backend/internal/service/gateway_usage_billing_conflict_test.go`、`gateway_usage_billing_partial_replay_test.go`、`failover_force_cache_billing_profit_test.go` | 测试 |
| P7 | `openspec/changes/gateway-account-failover/rollout.md` | 文档 |

**改动（非新增）文件合计**：`config.go`、`gateway_forward.go`、`antigravity_gateway_retry.go`、`gemini_messages_compat_service.go`、`openai_gateway_upstream_errors.go`、`gateway_upstream_response.go`、`gateway_service.go`、`gateway_scheduling.go`、`ratelimit_service.go`、`account_repo.go`、`usage_billing_repo.go`、`ops_metrics_collector.go`、`ops_alert_evaluator_service.go`、`gateway_handler.go`、`failover_loop.go`、`openai_gateway_handler.go`、`gemini_v1beta_handler.go`、`gateway_handler_chat_completions.go`、`gateway_handler_responses.go`、`repository/wire.go`、`service/wire.go` = **21 个**。

> **不改**：`backend/ent/**`（全部生成代码）、`backend/internal/service/group.go`（`Group` 结构体）、`backend/internal/repository/group_repo.go`、`backend/internal/service/scheduler_snapshot_service.go`（快照模型不动，V3/方案 C 已否决）。

---

## 附录 B：实施检查点（design F/C 项 + 本计划追加）

| ID | 检查点 | 归属任务 | 不满足的后果 |
| --- | --- | --- | --- |
| **F-01** | 分类器默认规则表与 4 处旧硬编码**逐项等价**（参数化测试） | T0.4 / T1.2 | 接线即行为变更 |
| **F-02** | `cooldown.enabled=false` 时**零 Redis 调用**（无 RTT） | T4.1 / T4.2 | 默认配置引入额外延迟 |
| **F-03** | `budget.total_timeout=0` 时行为与旧版**逐位一致** | T3.1 | 默认配置引入超时 |
| **C-01** | `SemanticBytes` **每请求独立**（并发测试） | T2.1 | 并发请求互相污染 → 误判可切换 |
| **C-02** | Redis 全挂时退化为现状（**不返回 503**） | T4.1 | Redis 故障 = 全站不可用 |
| **C-03** | `upstream_errors` 新增 kind 后 `account_switch_count` **口径不断裂** | T5.2 | 历史看板失真 |
| **C-04** | 迁移 254 在旧版本代码上**可安全存在**（三列 NULL 被忽略） | T0.2 | 滚动发布期间旧实例报错 |
| **C-05**（新增） | **`NewFailoverState` 签名不变**（9 个入口零改动） | T2.4 | 9 个 handler 同时改，冲突面爆炸 |
| **C-06**（新增） | **`Decision` 四元决策是唯一副作用入口**（grep 断言 `HandleUpstreamError` 在 `Forward` 内无直接调用点） | T2.2 | R-17 复发 |
| **C-07**（新增） | **无新增 `Writer.Size()` / `Writer.Written()` 换号判据**（grep 白名单） | T2.1 | R-01 复发 |
| **C-08**（新增） | **jitter 不改变重试次数**（统计断言：次数分布不变，仅时刻变化） | T3.2 | 变相增加上游调用 |
| **C-09**（新增） | **迁移编号唯一且递增**（`254`、`255` 不与既有冲突） | T0.2 / T6.1 | 启动时迁移失败 |

---

## 附录 C：命令速查（含 PowerShell 语法与 `-race` 前置）

```powershell
# 1) 基础门禁（每个任务提交前）
go -C backend build ./...
go -C backend vet ./...

# 2) handler 侧回归（无 tag，默认跑）
go -C backend test ./internal/handler/... -run 'Failover' -count=1

# 3) service 侧回归（必须 -tags=unit，否则带 unit tag 的文件被跳过）
go -C backend test -tags=unit ./internal/service/... -run 'Failover|Classif|Cooldown|PoolModeRetry' -count=1

# 4) 配置默认值
go -C backend test -tags=unit ./internal/config/... -run 'FailoverPolicy' -count=1

# 5) 迁移（新增 SQL 的结构断言，不连 DB）
go -C backend test ./migrations/... -run 'GroupFailoverPolicy|UpstreamCostAttempts' -count=1

# 6) 仓库层（Redis 相关，miniredis 或集成 tag）
go -C backend test ./internal/repository/... -run 'FailoverCooldown|TempUnschedulable' -count=1

# 7) 全量单测（阶段出口 / 合并前）
go -C backend test -tags=unit ./...

# 8) 数据竞争（可选；本机默认 CGO 关闭，需临时前置 MinGW）
$env:PATH = "C:\Users\ThinkPad\AppData\Local\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
go -C backend test -race ./internal/handler/... -run 'Failover' -count=1
# 验证后可恢复：$env:CGO_ENABLED = "0"（不要改持久 PATH/GOENV）

# 9) 前端（本变更无前端改动；仅合并前确权）
npx --yes pnpm@10.24.0 -C frontend typecheck
```

> `-tags=unit` 的必要性：`internal/service` 与 `internal/config` 中大量测试文件带 `//go:build unit`（如 `gateway_account_selection_test.go`、`gemini_error_policy_test.go`、`gateway_upstream_transport_error_test.go`）。**不带 tag 会静默跳过**，给出"绿"的假象。
> `Makefile` 的 `test-unit`（`go test -tags=unit ./...`）与 `test`（`go test ./...` + `golangci-lint run`）都要在阶段出口各跑一次。

---

## 附录 D：需 t2 / 产品在实施前裁决的 6 个问题

| # | 问题 | 影响任务 | 无答复时的默认处置 |
| --- | --- | --- | --- |
| **Q1** | `max_selection_retries` 默认 `0`（本计划，零变更）还是 `1`（design §3.1.5，防活锁）？ | T0.1、T3.2 | 按本计划默认 `0`，发布手册建议 `1` |
| **Q2** | 终局"混合类别 → 503"（B3）是否会破坏依赖 502 的客户端？ | T5.4 | 保留 `terminal.mixed_status_503=true` 但灰度时先观察 1 周；有投诉即关 |
| **Q3** | `ops_error_logs` 是否有 `group_id` 列？没有的话分组维度指标是否延后？ | T5.3 | 无列 → 先只做全局维度（R-38 转"条件解决"） |
| **Q4** | P2 是否允许"Forward 内保留标记 + 只加决策门"的降级实现（不撤销已写标记）？ | T2.2 | 允许降级并要求 `failover_suppressed` 可见；残留风险入 §4.4 |
| **Q5** | 换号过程日志是否降到 debug（只留 1 条汇总）？ | T5.1 | 降到 debug + 保留 1 条 outcome（R-41） |
| **Q6** | P6（成本账本）是否随本变更发布，还是拆成独立变更？ | T6.1–T6.3 | 随本变更发版但 `cost_attempts.enabled=false`（不写表） |

---

## 附录 E：与 t3（risks.md）的 4 条复核结论对齐

| t3 结论 | 本计划的落地 |
| --- | --- |
| 两套"已写字节"判据互斥（R-01，blocker） | T2.1 统一为 `UpstreamWriteState`，C-07 用 grep 守住 |
| 标记与判定解耦（R-17，blocker）→ 在 handler 层做可配置规则**无法抑制副作用** | T2.2 把判定下沉为唯一 `Decide`，副作用紧随其后；**这是 P2 必须早于 P4 的根本原因** |
| `account_switch_count` 名不符实（R-37，high） | T5.2 修正注释 + 新 kind 并入 `IN`；T5.3 新 `decision_total` 作为告警唯一来源 |
| B1 若允许"已写语义字节后重放"会撞 `ErrUsageBillingRequestConflict` | 本计划**明确规定不允许**（design §3.6.6 + V1），并在 T6.2 冻结指纹作为第二道保险 |
