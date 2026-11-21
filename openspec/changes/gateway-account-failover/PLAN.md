# 网关账号级容灾优化 · 可执行方案（汇总）

> 版本：v1.0 · 2026-11-17
> 状态：**设计完成，待评审拍板后进入实现**
> 子文档：`recon.md`（现状盘点 374 行）· `design.md`（架构设计 1410 行）· `risks.md`（风险审查 248 行）· `implementation.md`（实现规划 1413 行）

---

## 一、一句话结论

**账号级 failover 已经存在**（`FailoverState` + `maxAccountSwitches` 默认 10），本次要做的**不是从零建，而是补 4 个真缺口**：

| # | 缺口 | 严重度 | 证据 |
|---|---|---|---|
| 1 | **失败判定规则不可配置**——可换号状态码硬编码在 4 处 | 核心需求 | `gateway_forward.go:47-54`、`antigravity_gateway_retry.go:780-787`、`gemini_messages_compat_service.go:1706-1713`、`openai_gateway_upstream_errors.go:303` |
| 2 | **判定与副作用不同源**——service 层无条件标记，handler 放弃换号也撤不回 | blocker | `gateway_upstream_response.go:550-557`（R-17） |
| 3 | 失败账号**不写库、不清粘性** → 坏账号跨请求被反复命中 | high | `ratelimit_service.go:340-349` |
| 4 | **无请求级总预算** + 无切换率指标 | high | 无总 deadline；`account_switch_count` 口径错误（R-37） |

---

## 二、现状要点（避免重复建设）

### 已经有、不用做

- **切换循环**：`FailoverState` 覆盖 Anthropic / Gemini / Antigravity / chat / responses / v1beta / systemone
- **排除重选**：`SelectAccountWithLoadAwareness(..., excludedIDs)`
- **切换上限**：`maxAccountSwitches`（Anthropic 默认 10，**OpenAI 网关与 Grok 媒体兜底 3**）
- **400 可配**：`failover_on_400`（默认关闭）
- **冷却 extend-only 守卫**：`SetTempUnschedulable` 不会缩短既有冷却
- **扣费幂等**：`usage_billing_dedup (request_id, api_key_id, request_fingerprint)` 唯一
- **分组级账号池告警**：**6 个指标已存在**（`group_available_accounts` / `group_available_ratio` / `group_rate_limit_ratio` / `account_error_ratio` / `account_temp_unscheduled_count` / `overload_account_count`）——**今天就能配，不需要开发**

### 真实重试量级（比直觉大）

`maxAccountSwitches` 是**切换次数**，尝试账号数 = 值 + 1；再叠加「每账号原地重试」：

| 平台 | 最坏上游调用数 |
|---|---|
| Gemini | 3 切换 × 每账号最多 5 次 = **约 20 次** |
| Anthropic | 11 账号 × 每账号最多 4 次 = **约 44 次** |

这正是「需要请求级总预算」的直接理由。

---

## 三、推荐方案：B（冷却层）

### 为什么选 B —— 决定性理由

**B 的全部新能力默认关闭 → 发布 B 等于发布 A 的行为，回滚只需改配置，不需要构建/部署新代码。**

> ⚠️ 精度修正（由 t7 核实，2026-11-17）：本仓库**配置不热加载**（`WatchConfig`/`fsnotify`/`OnConfigChange` 全 backend 零命中），所以「改配置即回滚」的真实动作是**改配置 + 滚动重启**。仍然不需要重新构建镜像或发版，但值班必须知道含重启动作。重启前还须满足 `Validate` 组合，否则**拒绝启动**——尤其 `cooldown.fail_open` 必须为 `true`。详见 `rollout.md` §9。
>
> 另一处勘误：环境变量前缀**不是** `SUB2API_GATEWAY_FAILOVER_POLICY_*`（`config.go` 未调用 `viper.SetEnvPrefix`，全 backend 零命中），实际为 `GATEWAY_FAILOVER_POLICY_<路径>`。

### 方案对比

| | A 最小改动 | **B 冷却层（推荐）** | C 调度器内置 |
|---|---|---|---|
| 改动面 | 小 | 中 | 大 |
| 可配置失败判定 | ✅ | ✅ | ✅ |
| 跨请求冷却 | ❌ | ✅ Redis | ✅ |
| 多实例正确性 | — | ✅ | ✅ |
| 请求级预算 | ❌ | ✅ | ❌ 不属于调度器 |
| 回滚成本 | 低 | **改配置** | 需发版 |
| 主要问题 | 修不了缺口 3/4 | — | 秒级评分 vs 分钟级快照**结构性冲突**；破坏三层分层 |

**C 被否的三条硬理由**：请求级预算不属于调度器职责；秒级失败评分与分钟级快照节流结构性冲突；破坏 handler / 调度 / 快照分层。

### 架构分层

```
L0 策略层   分组级 failover_policy（JSONB）+ 全局默认
L1 分类层   错误分类器：Canceled → BoundUpstreamPassthrough
                        → RetryableOnSameAccount → RequestScopedTransient → 状态码
L2 状态层   三级：请求内排除 / Redis 冷却窗口 / DB 停用
L3 预算层   请求级总 deadline + 最大上游尝试数
L4 观测层   SwitchHop[] 切换链 + 新指标 + 告警
```

**分类器判定顺序受既有测试硬约束**：`gateway_upstream_transport_error_test.go` 锁定「客户端取消不标记 / 上游 deadline 仍 failover / 瞬态错误不驱逐」三条规则，设计必须遵守。

---

## 四、我（captain）对 4 处偏差的裁决

| 编号 | 偏差内容 | 裁决 | 理由 |
|---|---|---|---|
| **D1** | `max_selection_retries` 默认 0（design 写 1） | ✅ **采纳 0** | 保持「零默认行为变更」硬约束。改成 1 会变成第 4 项默认变更，破坏「发布 B = 发布 A」这个回滚前提。发布手册建议运维按需设 1 |
| **D2** | R-17 门控并入 P2 且前置到预算之前 | ✅ **采纳** | 否则 T2 冷却会把「handler 事后放弃的换号」写成跨请求冷却，把 **DB 误标升级为误冷却**，危害放大 |
| **D3** | 新增 P6 成本账本（design 未排期） | ✅ **采纳** | R-10/R-11 是 blocker，没有任务就无法验收。blocker 必须有对应任务 |
| **D4** | `config.yaml.example` 不存在，改 config.go 注释 + 新增 rollout.md | ✅ **采纳** | 仓库现状如此；文档落在 `rollout.md` 更合理 |

**两条勘误已核实成立**：
- E1：`gateway_usage_integrity_test.go` 不存在，实际是 `openai_gateway_usage_integrity_test.go`
- E2：`service` 目录 **956 个测试文件，448 带 `-tags=unit`、508 不带** —— 跑回归不加 tag 会漏掉近半数并给出「绿」的假象

**两条仓库约束已核实成立**：
- `backend/.golangci.yml` 有 `service-no-repository` depguard 规则 → T2 冷却的 Redis 实现必须放 `backend/internal/repository/`，service 只定义接口
- 迁移 254 **不要动 ent**：`groups` 扩列先例（`opsmgmt_min_margin_basis_points`）是纯原生 SQL，`service.Group` 与 `ent/schema/group.go` 均无该字段

---

## 五、实施计划（8 阶段 / 26 任务 / 47 人日）

### 关键：两套顺序要分开

- **编码顺序按依赖**：P0 → P1 → **P2（最高风险，先做）** → P3/P4/P5 → P6 → P7
- **发版顺序按风险**：**P5/P3/P4 先于 P2**（观测与预算先上线，再开判定门控）

### 阶段一览

| 阶段 | 内容 | 默认状态 | 回滚方式 |
|---|---|---|---|
| **P0** | 地基（配置骨架、类型定义，**无调用点**） | — | 回滚提交 |
| **P1** | 分类器接线（行为等价，不改判定） | — | 回滚提交 |
| **P2** | 判据与副作用同源（R-01/R-17/B1/B2）**最高风险** | `enabled=false` | `enabled=false` |
| **P3** | 请求级预算 | `budget.*=0`（关） | `budget.*=0` |
| **P4** | T2 跨请求冷却 | `cooldown.enabled=false` | `cooldown.enabled=false` |
| **P5** | 观测与终局策略 | 指标常开；503 关 | `terminal.mixed_status_503` |
| **P6** | 成本账本与上游幂等 | `cost_attempts.enabled=false` | 同左 |
| **P7** | 文档与 rollout 手册 | — | — |

### 风险覆盖

- **49/49 条风险全部有任务**（脚本核对无缺失）
- **8 个 blocker 全部有任务**：R-01/R-02→T2.1、R-17→T2.2、R-24→T3.1、R-30/R-31→T4.3、R-10→T6.1、R-11→T6.2
- **10 条明确「不解决/条件解决」+ 残留风险**已逐条说明
- risks 的 T-01~T-23 必补测试全部落到具体任务与测试文件

### 工作量

- **47 人日**（25 必做 + 1 可选），3 人并行约 **4 周**
- 关键路径 **23.5 人日**
- 裁剪路径（去掉 P6 + T4.5）→ **40 人日**

---

## 六、需要你拍板的 2 件事

### 决策 1：范围 —— 全做还是裁剪？

| 选项 | 人日 | 说明 |
|---|---|---|
| **A. 全做（推荐）** | 47 | 含 P6 成本账本，覆盖全部 8 个 blocker |
| B. 裁剪 | 40 | 去掉 P6（成本账本）与 T4.5，**R-10/R-11 两个 blocker 变成已知残留风险**——换号时可能漏记或重复记上游成本 |

**我的建议：全做。** 成本记账错误会直接影响营收核对，属于「省 7 人日换长期账目风险」，不划算。

### 决策 2：B3 终局 503 是否本次开启？

**现状**：混合失败时返回「最后一个错误映射」，Anthropic 路径**没有 `Retry-After`**。
**t2 建议**：改为统一 503 + Retry-After。

**我的建议：代码做，但默认关闭**（`terminal.mixed_status_503=false`）。理由：
- 改状态码会改变客户端重试行为，需先确认你的调用方（客户端/CLI/第三方）能正确处理 503
- 默认关闭意味着发布零风险，等观测到真实混合失败场景后再评估开启
- 无论开关如何，**先补 `attempts` 与 `exhausted_reason` 字段**（纯增量，无兼容风险）

---

## 七、三个必须注意的执行约束

1. **`-tags=unit` 是隐形坑**：`service` 目录 956 个测试文件中 448 带 tag、508 不带。跑回归漏 tag 会**漏掉近半数并显示全绿**。所有回归命令必须显式带 `-tags=unit`（相关包）。
2. **工作树有未提交改动**：`git status` 显示 **167 项变更（57 个已修改文件）**，来自前两阶段。t4 的基线是在这个工作树上跑的，**落地回归必须用同一工作树对比**，否则基线不可比。
3. **迁移编号 254**：当前最大是 `253_ops_mgmt.sql`（305 个 SQL）。迁移只加 `groups` 三列（`failover_on_400` / `failover_max_account_switches` / `failover_policy JSONB`），**不动 ent**。

---

## 八、当前状态

- ✅ 设计阶段完成：4 份文档共 3435 行
- ⏸ **未改任何代码**（本阶段严格只做设计）
- ⏸ 团队已收尾，等待你的范围与 503 决策后进入实现

决策后我将组建实现团队，按 P0→P1→P2→… 依赖顺序推进，并遵循「发版顺序 P5/P3/P4 先于 P2」的风险控制。
