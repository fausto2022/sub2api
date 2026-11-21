# 网关账号级容灾 · 发布与回滚手册（rollout.md）

> 适用版本：`openspec/changes/gateway-account-failover/`（方案 B / 8 阶段 26 任务）。
> 本文件是 T7.1 的**文档部分**，与 `implementation.md` §六（上线与回滚）一一对应并做了可执行化。
> 读者：值班 SRE / 发布执行人 / 排障人。所有命令在仓库根目录 `D:\WorkSpace\sub2api` 下执行。

---

## 0. 核心保障：为什么"回滚只改配置、不发版"成立

本方案的全部新能力**默认关闭或默认等价现状**（`implementation.md` §1.5 / §1.6）：

| 事实 | 含义 |
| --- | --- |
| 35 个新键的默认值 = 改造前现状 | 不配置 `gateway.failover_policy` 就与发布前逐位一致 |
| 只有 3 项默认行为变更：B1 / B2 / B3 | 且都给回退开关（`switchable.use_semantic_bytes`、`switch_order.clear_sticky_on_account_failure`、`terminal.mixed_status_503`） |
| B3 当前实现默认 **关闭** | 用户尚未裁决，代码按"零默认行为变更"取 `terminal.mixed_status_503=false`；开启是配置动作，不是发版 |
| 迁移只加可空列 / 新建独立表 | 旧代码不读新列，新表不写就对现网零影响 |

因此：**发布 B ≈ 发布 A**。任何阶段出问题，第一动作是**改配置 + 重载**，不是回滚二进制；只有当"改动无法用配置表达"时才走回滚提交（见 §5）。

---

## 1. 发布前基线（每次发布前都要重跑一次）

### 1.1 必须知道的三个基线事实

| # | 事实 | 对发布的影响 |
| --- | --- | --- |
| **B-1** | **历史环境失败**：三个 `TestPgDumper*` 用例在 Windows 上曾因 `sh` 不在 `PATH` 而失败。用例注入 `sh` 和 SQL mock，不要求本机安装真实 `pg_dump`。 | 按 §1.2 临时前置 Git for Windows 的 `usr\bin`，两档全仓命令原样执行，不剔除用例。仍失败时保留日志和退出码；真实 PostgreSQL 备份验证另需对应主版本的客户端。 |
| **B-2** | **测试分两档**：`internal/service` 956 个测试文件，**448 带 `//go:build unit`、508 不带**（规划期数字；当前实测 962 = 448 带 tag / 514 不带，因并行任务新增了测试文件） | **漏 tag 等于漏跑近半数 service 回归**，会给出"绿"的假象。每档都必须单独跑（§1.2 的 `C-T-CFG`…`C-T-TAG`） |
| **B-3** | **基线必须同树对比**：当前工作树含前两阶段未提交改动（规划期 167 项，当前实测 **177 项** `git status --porcelain`） | 任何"回归数"只在与**同一工作树**（含这些未提交改动）对比时才有意义；用干净 `HEAD` 做基线会得到错误结论 |

### 1.2 命令矩阵

```powershell
# Windows：先为当前 PowerShell 进程补齐 sh.exe。
if ($IsWindows) {
    $gitRoot = Split-Path (Split-Path (Get-Command git).Source)
    $gitUnixBin = Join-Path $gitRoot 'usr\bin'
    if (!(Test-Path (Join-Path $gitUnixBin 'sh.exe'))) { throw 'Git for Windows sh.exe not found; locate its usr\bin before running the suite' }
    $env:PATH = "$gitUnixBin;$env:PATH"
}

# 系统盘空间不足时，先设置容量充足的 GOTMPDIR/GOCACHE；只改当前进程。
# 共享 Windows 工作站使用串行 -p 1，各命令原始退出码与日志分别保存。
go -C backend build -p 1 ./...                             # C-BUILD
go -C backend vet -p 1 ./...                               # C-VET
go -C backend test -p 1 ./... -count=1                     # C-T-ALL
go -C backend test -p 1 -tags=unit ./... -count=1          # C-T-TAG

# —— 强制回归子集（implementation.md §5.1 的 5 个必过文件，任一红即不得合并）——
go -C backend test ./internal/handler/... -run 'Failover' -count=1        # 18~20s
go -C backend test -tags=unit ./internal/service/... -run 'Failover|Classif|UsageBilling' -count=1

# —— 格式门禁 ——
gofmt -l backend/internal/config backend/internal/repository backend/internal/service
#   期望：无输出。（若输出了与本次改动无关的旧文件，单独记录、不要在本阶段顺手改）
```

> **计数留痕**：P0/P1 是"行为等价"阶段，出口条件除"绿"之外，还要求**同一批命令的用例数不发生数量级变化**（`-v` 计数对比），防止测试被悄悄删掉。

---

## 2. 两套顺序必须分开：编码顺序 ≠ 发版顺序

```
编码（按依赖）：P0 → P1 → P2(最高风险，先做) → P3 / P4 / P5 → P6 → P7
发版（按风险）：P0 → P1 → P5 → P3 → P4 → P6 → P2 → P7
                          └──── 观测与预算先上线 ────┘   └ 最后开判定门控 ┘
```

### 2.1 为什么 P5/P3/P4 必须先于 P2（**不能反过来**）

P2 引入的是**默认生效**的行为变更（B1 语义字节判据、B2 清粘性），它把"是否换号"的判定与副作用收敛到同一处。如果先上 P2：

1. **没有观测就无法判断它做对了没有**。P2 的验收指标是 `failover_suppressed{reason}` 能否解释每一次"该换没换"、`failover_attempt` 能否按 `request_id` 聚合出真实上游调用数。这些能力来自 P5。P5 不在线时，P2 的行为变更只能靠"用户投诉"发现。
2. **没有预算就无法阻止放大**。P2 让"keepalive 后仍可换号"，可恢复请求变多 = 上游调用数上升；P3 的 `budget.max_total_upstream_attempts` 是唯一能兜住"换号风暴"的闸门。先开 P2 再上 P3，等于把放大窗口留给现网。
3. **没有冷却就无法限制跨请求扩散**。P4 的 T2 冷却负责把"坏账号"从**跨请求**维度摘掉；P2 若先上，一次失败会在多个账号上重复发生而无跨请求记忆，故障面被放大而不是收敛。
4. **R-17 的前置条件**：P2 的 R-17 门控（`T2.2`）把"副作用"绑定到"决策"上。P4 的冷却写入依赖这个门控——否则 handler 事后放弃的换号也会被写成冷却（把 **DB 误标**升级为**跨请求误冷却**）。故 P4 的**正确性**依赖 P2 已合入，但 P4 的**发版**必须先于 P2 的**开启**：P4 以 `cooldown.enabled=false` 发布（代码在线、能力关闭），等 P2 开启并观察稳定后再打开冷却。

> 一句话：**P5 给眼睛，P3 给刹车，P4 给记忆；这三样必须在"踩油门"（P2 的 B1/B2）之前就位。**

### 2.2 各阶段"发版时是否改变现网行为"

| 阶段 | 发版即生效的行为 | 是否需灰度 |
| --- | --- | --- |
| P0 地基 | 无（无调用点） | 否 |
| P1 接线 | 无（分类器镜像旧硬编码） | 否（但需对比指标） |
| P5 观测 | 只读（日志/指标/终态字段）；B3 默认关 | 否 |
| P3 预算 | 无（`budget.*=0`） | 代码全量，**开启**按分组灰度 |
| P4 冷却 | 无（`cooldown.enabled=false`） | 代码全量，**开启**按分组灰度 |
| P6 成本账本 | 无（`cost_attempts.enabled=false`） | 代码全量，默认不写 |
| P2 判据/决策 | **有**（B1/B2 默认 true） | **是，强制两步灰度** |

---

## 3. 逐阶段发布手册（P0 → P7）

> 每阶段统一四段：**上线动作 / 灰度范围 / 观察（≥3 天） / 回滚**。
> 「观察期」默认 **3 个自然日**（P4 冷却建议 3–7 天）；判定用**同期对比**（本周同星期 vs 上周同星期），不看绝对值波动。

### 3.1 P0 地基（配置骨架 + 迁移 254 + 策略解析）

| 项 | 内容 |
| --- | --- |
| **上线动作** | ① 发布二进制（新配置结构体、迁移 254 随包嵌入）→ ② 启动时自动执行迁移 `254_group_failover_policy.sql`（`IF NOT EXISTS`，幂等）→ ③ 确认启动日志无 `validate config error` |
| **灰度** | 全量（无行为变更：无调用点，`groups` 新列无人读） |
| **观察 3 天** | `C-BUILD` / `C-VET` 绿；`SELECT column_name,data_type,is_nullable FROM information_schema.columns WHERE table_name='groups' AND column_name LIKE 'failover%'` 返回 3 行且 `is_nullable=YES`；无新增 error 日志；进程重启次数为 0 |
| **回滚** | **无需回滚**（旧代码不 select 新列）。如需彻底干净回退：`ALTER TABLE groups DROP COLUMN IF EXISTS failover_policy, DROP COLUMN IF EXISTS failover_max_account_switches, DROP COLUMN IF EXISTS failover_on_400;` + 回滚提交 |
| **配置键** | 无（全部默认值） |

**迁移 254 的约束（发布前必须逐条核对）**：

- 三列全 `NULL`、**无 DEFAULT、无 NOT NULL**——`DEFAULT false` 会把"未配置（回落全局）"静默变成"配置了 false"（`failover_on_400=false` 是有效语义）。
- **不动 ent**：`groups` 扩列走纯原生 SQL（先例 `opsmgmt_min_margin_basis_points`），不新增 `ent/schema` 字段、不重跑 codegen。因此 **ent 代码零改动**，回滚不涉及 ent 迁移。
- 迁移一旦应用不得修改（checksum 校验）；要改就新增 255/256。

### 3.2 P1 接线（4 处硬编码 → 分类器，行为等价）

| 项 | 内容 |
| --- | --- |
| **上线动作** | 发布二进制。分类器镜像旧状态码集合，判定结果应与改造前逐项相同 |
| **灰度** | 全量（等价发布），但**必须开指标对比窗口** |
| **观察 3 天** | ① `gateway.failover_switch_account` 计数与上周同期偏差 **< 5%**；② 上游错误码分布（`upstream_errors.kind`）不变；③ 扣费条数不变；④ `C-H`（handler Failover 回归）绿 |
| **回滚** | `gateway.failover_policy.enabled=false`（走旧硬编码路径，**不发版**）→ 重载进程配置。确认 `policy_source=legacy` 出现在日志 |
| **配置键** | `gateway.failover_policy.enabled`（`true`→`false`） |

### 3.3 P5 观测与终局（只读，**发版顺序第 3 位**）

| 项 | 内容 |
| --- | --- |
| **上线动作** | 发布二进制：`upstream_errors` 新增 `kind="failover_attempt"`（含 `attempt_seq`）、5 个指标 + 2 条告警规则、终态错误补齐 `attempts` / `exhausted_reason` |
| **灰度** | 全量（只读能力）。**B3 默认关闭**（`terminal.mixed_status_503=false`），客户感知零变化 |
| **观察 3 天** | ① `failover_attempt` 日志量 ≤ 旧 `gateway.failover_switch_account` warn 的 **1.2 倍**；② 7 个指标均可查（`failover_switch_total` / `failover_exhausted_total{reason}` / `failover_attempts_per_request` / `failover_duration_seconds` / `failover_cooldown_active_accounts` / `failover_cooldown_degraded_total` / `failover_decision_total{decision}`）；③ 2 条告警可被人工触发（`failover_exhausted_ratio` / `failover_switch_ratio`）；④ `account_switch_count` 旧指标仍可查（仅对照，不告警，R-37） |
| **回滚** | 只读能力可保留（无害）。若要回退 B3：`gateway.failover_policy.terminal.mixed_status_503=false`（当前已是默认 `false`，即"不开启"就是回滚） |
| **配置键** | `gateway.failover_policy.terminal.mixed_status_503`（B3；默认 `false`，**开启=改 true**）<br>`gateway.failover_policy.expose_failover_detail`（终态是否向客户端暴露 attempts，默认 `false`） |

> **B3 特别说明**：`terminal.mixed_status_503` 默认 `false`，沿用各端点原有末错映射。开启后才将混合的上游可用性错误映射为 503，不覆盖既有透传规则。`Retry-After` 只沿用各端点已有行为；跨尝试取最大还是最早 reset 尚未裁决，本轮不聚合，也不为原先未回写的端点增加回填。`attempts`、`exhausted_reason`、`last_status` 为安全标量；只有开启 `expose_failover_detail` 才下发脱敏失败类别，不下发账号 ID、输出正文或内部链路。

覆盖边界：观测接在应用层 HTTP 实际发送点，涵盖 Messages、Gemini 原生及已接入该发送层的 OpenAI 兼容路径；不把未发送的候选、WebSocket 重连或 transport 内部重试算作 HTTP 尝试。`gateway_forward_as_chat_completions.go`、`gateway_forward_as_responses.go`、`compat_stream` 与 `openai_*` keepalive 的其余 `Written()/Size()` 判定仍保留为 P3/P4 残留项；这些地方有各自的协议终止/心跳判据，本轮不替换。R-17 灰度单独用 `gateway.failover_policy.side_effects_deferred`（默认 `false`），总开关关闭时该门控也关闭。

`gateway.failover_metrics` 输出进程累计的实际切换与耗尽计数、决策计数和尝试/耗时直方图。两个告警通过 PostgreSQL 显式事件按 `request_id` 去重并过滤 `group_id`/平台，分母为同窗口成功与失败请求总数；历史 `account_switch_count` 的错误事件口径不变。冷却活跃/降级两个度量依赖 P4 的存储和启动接线，不能用容灾计数或旧错误计数替代。

### 3.4 P3 请求级预算（默认关，**发版顺序第 4 位**）

| 项 | 内容 |
| --- | --- |
| **上线动作** | 发布二进制（预算层代码在线，`budget.*` 全 0 = 关闭）；随后按分组灰度**开启** |
| **灰度** | 先选 **1 个低流量分组**开启 3 天 → 扩到 10% 分组 → 全量。分组级覆盖方式见 §8 |
| **开值建议** | `budget.total_timeout=120s`、`budget.per_account_timeout=90s`、`budget.max_total_upstream_attempts=6`（VIP 分组 12）、`budget.max_same_account_attempts_total=4`；`budget.min_switch_cost` 保持默认 `2s`；`budget.stop_clock_on_stream` 保持默认 `true` |
| **观察 3 天** | ① `failover_exhausted_total{reason="budget"}` 占比 **< 0.5%**；② P99 延迟**不上升**；③ `failover_attempts_per_request` 分布**左移**（调用数被收敛）；④ 无新增 5xx；⑤ `failover_exhausted_total{reason="budget"}` > 1% 持续 10min → P2 告警 |
| **回滚** | 全部置 0（**不发版**）：`budget.total_timeout=0`、`budget.per_account_timeout=0`、`budget.max_total_upstream_attempts=0`、`budget.max_same_account_attempts_total=0` |
| **配置键** | 见上；分组级覆盖用 `groups.failover_policy` JSONB 的 `budget` 子对象 |

### 3.5 P4 T2 跨请求冷却（默认关，**发版顺序第 5 位**）

| 项 | 内容 |
| --- | --- |
| **上线动作** | 发布二进制；**先确认 Redis 可达**（`redis_timeout=500ms`）；再按分组开启 `cooldown.enabled=true` |
| **灰度** | 1 个低流量分组 3–7 天 → 10% → 全量。**`cooldown.fail_open` 必须保持 `true`**（设为 `false` 时进程会**拒绝启动**，这是 `Validate()` 的硬校验） |
| **开值建议** | `cooldown.enabled=true`、`consecutive_failures_threshold=3`、`streak_window=120s`、`default_ttl=60s`、`max_ttl=24h`、`pool_mode_enabled=true`、`probe_enabled=true`、`probe_max_attempts=3`、`redis_timeout=500ms`、`batch_read_max=200` |
| **观察 3–7 天** | ① `failover_cooldown_degraded_total` **必须 == 0**（> 0 即 Redis 故障/超时，等价于冷却失效，**硬性回滚触发条件**）；② `failover_cooldown_active_accounts` 在预期量级（不应长期等于分组账号总数）；③ 各分组 `group_available_ratio` 不下降；④ `failover_exhausted_ratio` 不上行（< 5%）；⑤ 首 token 延迟（缓存亲和性丢失）在可接受范围 |
| **回滚** | `cooldown.enabled=false`（**不发版**）；Redis 故障时自动 fail-open（不阻断请求），无需人工干预 |
| **配置键** | `gateway.failover_policy.cooldown.enabled`（`true`→`false`）；分组级 `groups.failover_policy.cooldown.enabled` |

### 3.6 P6 成本账本与幂等审计（默认不写，**发版顺序第 6 位**）

| 项 | 内容 |
| --- | --- |
| **上线动作** | ① 发布二进制 → ② 执行迁移 **255**（`upstream_cost_attempts`，**独立发布**）→ ③ 需要时按客户开通 `cost_attempts.enabled=true`、`idempotency.require_key_for_side_effecting=true` |
| **灰度** | 代码全量；**写入默认关闭**。账本按客户/分组开启；幂等禁令按客户开启（会显著降低可用性，只给"绝对不能重复执行"的客户） |
| **观察 3 天** | ① 表增长速率 ≈ 请求数 × 平均尝试数（与 `failover_attempts_per_request` 对齐）；② **扣费条数不变**（成本表不是账单表）；③ 唯一键冲突有计数且不报错；④ 开启幂等禁令的客户出现 `failover_suppressed{reason=side_effect_no_key}` 且可解释 |
| **回滚** | `cost_attempts.enabled=false`、`idempotency.require_key_for_side_effecting=false`、`idempotency.blocked_tools=[]`（均**不发版**）；成本表可保留（无害）或按 §6.2 `DROP` |
| **配置键** | `gateway.failover_policy.cost_attempts.enabled`（默认 `false`）<br>`gateway.failover_policy.idempotency.require_key_for_side_effecting`（默认 `false`）<br>`gateway.failover_policy.idempotency.blocked_tools`（默认 `[]`） |

### 3.7 P2 判据与副作用同源（**最高风险，发版顺序第 7 位，强制两步**）

| 项 | 内容 |
| --- | --- |
| **上线动作** | **第 ① 步**：以 `switchable.use_semantic_bytes=false` + `switch_order.clear_sticky_on_account_failure=false` 发布（等于"只上线 R-17 门控与切换链"，不改变换号判据）。<br>**第 ② 步**：观察 1 周稳定后，再开 `use_semantic_bytes=true`（B1）、`clear_sticky_on_account_failure=true`（B2） |
| **灰度** | ①步全量（等价 + 门控）；②步先 1 个分组 3 天 → 10% → 全量。B1/B2 是**两个独立开关**，建议按 B1 → B2 的顺序分别开，便于归因 |
| **观察 3 天（①步）** | 上游调用总数**不变**；`failover_suppressed{reason}` 出现且每条可解释（缓存绑定 / 已写字节 / `NextAccountStop`）；扣费条数不变；`failover_decision_total{decision=suppressed}` 与日志能对上 |
| **观察 3 天（②步）** | 切换成功率上升；上游调用数**小幅上升**（预期，因为 keepalive 后不再被误拦）；扣费条数**不变**；`failover_switch_ratio` 未越过 30% 告警线；TTFT（B2 清粘性导致缓存亲和性丢失）在可接受范围 |
| **回滚** | 精确到键（**全部不发版**）：<br>• B1：`gateway.failover_policy.switchable.use_semantic_bytes=false`<br>• B2：`gateway.failover_policy.switch_order.clear_sticky_on_account_failure=false`<br>• 整体回旧路径：`gateway.failover_policy.enabled=false`<br>• 排除集活锁护栏：`max_selection_retries=0`（默认） |
| **配置键** | `switchable.use_semantic_bytes` / `switchable.treat_keepalive_as_safe` / `switch_order.clear_sticky_on_account_failure` / `switch_order.record_chain` / `max_selection_retries` / `enabled` |

> **P2 唯一"不能靠配置回滚"的部分**：BN-1 的退避 jitter 与 BN-2 的计费指纹冻结（见 §5）。它们没有开关，回滚=回滚提交。

### 3.8 P7 文档收尾

| 项 | 内容 |
| --- | --- |
| **上线动作** | 发布 `rollout.md`（本文件）+ `implementation.md` 终稿；把 `deploy/config.example.yaml` 的注释补齐（**注意**：仓库不存在 `config.yaml.example`，见裁决 D4） |
| **灰度** | — |
| **观察** | 文档与代码一致性核对：配置键名逐个 grep 过 `config.go`；默认值表与 `implementation.md` §1.6 一致 |
| **回滚** | — |

---

## 4. 配置回滚总表（值班速查）

> 全部键的前缀是 `gateway.failover_policy.`。**改完需要重载进程配置**（本项目无热重载配置，见 §9 第 3 步）。
> 环境变量形式：`GATEWAY_FAILOVER_POLICY_<路径大写，点换下划线>`，例如 `GATEWAY_FAILOVER_POLICY_COOLDOWN_ENABLED=false`。
> 注意：`implementation.md` T0.1 里写的 `SUB2API_` 前缀**是文档笔误**——`config.go` 未调用 `viper.SetEnvPrefix`，实测环境变量无前缀。
> **`status_classes` 是结构化切片，只能走配置文件**（viper 环境变量通路无法表达 `[]struct`），不能用环境变量回滚。

| 想关掉的能力 | 配置键 | 关闭值 | 是否需发版 |
| --- | --- | --- | --- |
| 整个策略层（回旧硬编码） | `enabled` | `false` | 否 |
| B1 语义字节判据 | `switchable.use_semantic_bytes` | `false` | 否 |
| B1 子开关（keepalive 计入） | `switchable.treat_keepalive_as_safe` | `false` | 否 |
| B2 清粘性 | `switch_order.clear_sticky_on_account_failure` | `false` | 否 |
| B3 终局 503 | `terminal.mixed_status_503` | `false`（当前默认） | 否 |
| 预算层（全部） | `budget.total_timeout` / `budget.per_account_timeout` | `0` | 否 |
| 预算次数硬顶 | `budget.max_total_upstream_attempts` / `budget.max_same_account_attempts_total` | `0` | 否 |
| 切换前预算下限 | `budget.min_switch_cost` | `0s` | 否 |
| 首字节语义超时 | `first_output_timeout_seconds` | `0` | 否 |
| T2 冷却层 | `cooldown.enabled` | `false` | 否 |
| 冷却池模式 | `cooldown.pool_mode_enabled` | `false` | 否 |
| 半开探测 | `cooldown.probe_enabled` | `false` | 否 |
| 成本账本写入 | `cost_attempts.enabled` | `false` | 否 |
| 幂等禁令 | `idempotency.require_key_for_side_effecting` | `false` | 否 |
| 幂等封禁名单 | `idempotency.blocked_tools` | `[]` | 否 |
| 排除集活锁护栏 | `max_selection_retries` | `0` | 否 |
| 400 换号（新键） | `failover_on_400` | `false` | 否 |
| 400 换号（旧键，仍有效） | `gateway.failover_on_400` | `false` | 否 |
| 终态暴露 attempts | `expose_failover_detail` | `false` | 否 |
| 网络层不参与分类 | `network_errors.enabled` | `false` | 否 |
| T3 只延长 | `t3.only_extend` | `false` | 否 |
| 切换链日志 | `switch_order.record_chain` | `false` | 否 |
| 切换次数（既有键，语义不变） | `gateway.max_account_switches` | 维持 `10`（回滚不改它） | 否 |
| 分组级 400 覆盖 | `groups.failover_on_400` | `NULL` | 否（SQL） |
| 分组级切换次数覆盖 | `groups.failover_max_account_switches` | `NULL` | 否（SQL） |
| 分组级完整覆盖 | `groups.failover_policy` | `NULL` | 否（SQL） |

> ⚠️ **不要**为了"回滚"把 `cooldown.fail_open` 设为 `false`：`Validate()` 会拒绝启动（fail-closed 冷却被认为不安全）。回滚冷却用 `cooldown.enabled=false`。

---

## 5. 不能靠配置回滚的变更（必须回滚提交）

| 编号 | 变更 | 为什么配置救不了 | 回滚方式 |
| --- | --- | --- | --- |
| **BN-1** | 换号/同账号重试退避加 ±20% jitter（R-19） | 无开关，只改变重试时刻分布 | **回滚提交**（revert 该 commit）；行为影响温和，通常无需回滚 |
| **BN-2** | 计费指纹冻结为**首次尝试的原始 body**（R-11） | 无开关；影响 `usage_billing_dedup.request_fingerprint` 的冲突检测 | **回滚提交**；注意回滚后 `ErrUsageBillingRequestConflict` 的静默跳过会恢复，需同步恢复告警 |
| — | P0/P1 的接线本身（分类器替换 4 处硬编码） | 旧实现只在测试里留影子副本 | 优先用 `enabled=false` 切回旧路径（保留代码）；彻底移除才回滚提交 |
| — | 迁移 254（`groups` 三列） | 已应用的迁移不能改（checksum） | 不回滚（无害）；要清理只能新增迁移 `DROP COLUMN IF EXISTS` |
| — | 迁移 255（`upstream_cost_attempts`） | 同上 | 可 `DROP TABLE IF EXISTS upstream_cost_attempts;`（先确认无报表/面板依赖），见 §6.2 |
| — | Redis key 结构（`failover:cooldown:acct:*` 等） | 结构是代码常量 | 回滚提交；旧版本不读这些 key，残留 key 会按 TTL 自然过期（≤24h） |
| — | `status_classes` 的**默认表** | 默认表在 `load()` 里落地，不是 viper 默认值 | 改配置文件（不是代码）；确认改的是配置文件而非环境变量 |
| — | `gateway.failover_policy` 结构体/键名本身 | 键不存在时旧版会忽略 | 回滚二进制时才需要 |

---

## 6. 迁移上线顺序与回滚

### 6.1 顺序总表

| 顺序 | 迁移 | 随哪个阶段上线 | 幂等性 | 回滚 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 1 | **254_group_failover_policy.sql** | **P0** | `ADD COLUMN IF NOT EXISTS`，可重复执行 | **不回滚**（旧代码不 select 新列） | 三列全 `NULL`、无 `DEFAULT`、无 `NOT NULL`；三条 `COMMENT ON COLUMN` |
| 2 | **255_upstream_cost_attempts.sql** | **P6**（独立发布） | `CREATE TABLE IF NOT EXISTS` + `CREATE UNIQUE INDEX IF NOT EXISTS` | 可回滚：`DROP TABLE IF EXISTS upstream_cost_attempts;` | 唯一键 `(request_id, attempt_index)`；`cost_attempts.enabled=false` 时零写入、零表依赖 |

### 6.2 迁移 254 的发布要点

1. **顺序**：迁移随 P0 二进制一起发布（`backend/migrations/migrations.go` 用 `//go:embed *.sql` 打包，启动自动执行）。**先发布 P0 代码，再谈任何开关**——因为分组级读取（`repository/group_failover_policy_repo.go`）依赖这三列存在。
2. **幂等**：同一 SQL 连续应用两次不报错（测试 `TestGroupFailoverPolicyMigrationForbidsNonNullableOrDefaultedColumns` 断言 3 处 `ADD COLUMN IF NOT EXISTS`）。
3. **不阻塞旧代码**：三列可空且无人读 → 即使不回滚代码也无影响；反之若代码已回滚，列的残留数据被忽略。
4. **不动 ent（关键）**：`groups` 扩列不新增 `ent/schema` 字段、不重跑 `go generate ./ent`。因此：
   - ent 生成文件（`backend/ent/**`）在本方案中**零改动**；
   - 回滚不涉及任何 ent 迁移；
   - 代码审查时若看到 `backend/ent/**` 有 diff，说明有人误改了生成物，应拒绝合并。
5. **列语义**：`NULL` = 该层未覆盖（回落 `gateway.failover_policy.*`）；`failover_on_400=false` 是**有效值**（显式关闭），不能用 `DEFAULT false` 表达"未配置"。

### 6.3 迁移 255 的发布要点（P6）

- 与 P6 二进制**同一次发布**执行；先建表、再按客户开 `cost_attempts.enabled=true`。
- 回滚顺序：先 `cost_attempts.enabled=false`（停止写入）→ 观察是否仍有写入（应为 0）→ 再 `DROP TABLE`。
- 删表前确认：无 Grafana/报表/对账任务依赖 `upstream_cost_attempts`。
- 该表**不是账单表**：扣费条数由 `usage_billing_dedup` 保证不变（一请求一账单）。

---

## 7. 观察指标看板与回滚阈值（发布期常开）

| 指标 | 含义 | 告警阈值 | 回滚触发（建议） |
| --- | --- | --- | --- |
| `failover_cooldown_degraded_total` | T2 失效（Redis 故障/timeout），等价于冷却关闭 | **> 0 即 P1** | **> 0 且持续 5min → 立即 `cooldown.enabled=false`** |
| `failover_exhausted_total{reason="budget"}` | 预算耗尽占比，预算过紧 | 占比 > 1% 持续 10min → P2 | 占比 > 3% 持续 30min → 预算回滚为 0 |
| `failover_exhausted_ratio` | 分组容灾退化 | ≥ 5% 持续 10min → **P0** | ≥ 10% 持续 30min → 回滚最近开启的开关（P2 → P4 → P3 逆序） |
| `failover_switch_ratio` | 账号池在退化 | ≥ 30% 持续 15min → P1 | ≥ 30% 且伴随扣费异常 → `enabled=false` |
| `failover_decision_total{decision="suppressed"}` | 被抑制的换号（缓存绑定/已写字节/幂等门控） | 无（只做归因） | 若 `reason=side_effect_no_key` 占比突增且客户投诉 → 关幂等禁令 |
| `failover_attempts_per_request` | 每请求真实上游调用数 | 分布右移 → 关注 | 中位数翻倍 → 先看 P2 的 B1，再决定是否 `use_semantic_bytes=false` |
| `account_switch_count`（旧指标） | 语义=可切换**错误事件数**（R-37，不是切换次数） | 仅对照，不告警 | — |
| `group_available_ratio`（既有） | 分组账号池 | < 20 持续 10min → P0 | 结合冷却一并检查（`cooldown.max_ttl` 是否过长） |

**回滚判定的统一口径**：任一告警**连续 3 个采样周期**（默认 10min/周期）越线即执行回滚；单点尖峰只记录不动作。回滚顺序永远是**逆着开启顺序**：先关最后开的开关。

---

## 8. 分组级灰度怎么做（P3/P4/P6 的分组开启）

分组级覆盖是**字段级 merge**（不是对象级替换）：只写要改的字段，其余回落全局。

```sql
-- 例：给分组 12 单独打开冷却，只覆盖 cooldown.enabled
UPDATE groups
   SET failover_policy = COALESCE(failover_policy, '{}'::jsonb)
                          || '{"cooldown": {"enabled": true}}'::jsonb,
       updated_at = NOW()
 WHERE id = 12;

-- 例：单列覆盖（高频标量，pgAdmin 直接改）
UPDATE groups SET failover_on_400 = true, updated_at = NOW() WHERE id = 12;
UPDATE groups SET failover_max_account_switches = 6, updated_at = NOW() WHERE id = 12;

-- 回滚一个分组（恢复"回落全局"）
UPDATE groups
   SET failover_policy = NULL, failover_on_400 = NULL,
       failover_max_account_switches = NULL, updated_at = NOW()
 WHERE id = 12;
```

**JSONB 键名与全局配置一致**（少一层前缀），时长写 `"60s"` / `"500ms"` 字符串；**未知键会直接报错**（`ParseGroupFailoverPolicyJSON` 用 `DisallowUnknownFields`），这是故意的——避免"配了不生效"的静默漂移。

> 灰度选组标准：近 7 天 `failover_exhausted_ratio` 最低、QPS 占全站 < 5%、有明确负责人。**不要**拿 VIP/大客户分组做首批灰度。

---

## 9. 事故快速止血（3 步）

1. **先关判定门控，别关观测**：`gateway.failover_policy.enabled=false`（回旧硬编码路径）。观测能力（`failover_attempt` 日志、指标）保留，便于定位。
2. **若问题与冷却相关**：`cooldown.enabled=false`；Redis 抖动会自动 fail-open，但 `failover_cooldown_degraded_total>0` 本身就是回滚信号。
3. **重载配置**：本项目**不热加载配置文件**——改完需重启进程（或用你们的配置下发通道重新下发并滚动重启）。重启前先确认 `Validate()` 会通过的组合：
   - `cooldown.fail_open` 必须为 `true`（否则**拒绝启动**）；
   - `budget.total_timeout >= budget.per_account_timeout`（两者都非 0 时）；
   - `cooldown.default_ttl <= cooldown.max_ttl`；
   - `same_account.default_mode ∈ {inherit, off}`；
   - `first_output_timeout_seconds ∈ {0} ∪ [30,600]`。

**止血后的复盘输入**：`failover_attempt`（按 `request_id` 聚合出真实上游调用数）、`failover_decision_total{decision,reason}`、`failover_suppressed{reason}`、扣费条数（必须仍是一请求一账单）。

---

## 10. 附录

### 10.1 全默认配置（= 发布前后的现状行为，可直接贴 `config.yaml`）

```yaml
gateway:
  failover_policy:
    enabled: true                       # 策略层总开关；false = 回旧硬编码
    # status_classes 省略 = 使用内置默认表（= 改造前 4 处硬编码状态码集合）
    network_errors:
      enabled: true
    failover_on_400: false              # 旧键 gateway.failover_on_400 仍有效，新键优先
    compat_400_keywords:
      ["anthropic-beta", "beta feature", "requires beta",
       "thinking", "thought_signature", "signature",
       "tool_use", "tool_result", "tools"]
    same_account:
      default_mode: inherit             # inherit | off
    budget:
      total_timeout: 0s                 # 建议开：120s
      per_account_timeout: 0s           # 建议开：90s
      max_total_upstream_attempts: 0    # 建议开：6（VIP 12）
      max_same_account_attempts_total: 0 # 建议开：4
      min_switch_cost: 2s
      stop_clock_on_stream: true
    first_output_timeout_seconds: 0     # 建议开：90（灰度后）
    cooldown:
      enabled: false                    # 建议先在 1 个低流量分组开
      consecutive_failures_threshold: 3
      streak_window: 120s
      default_ttl: 60s
      max_ttl: 24h
      pool_mode_enabled: true
      probe_enabled: true
      probe_max_attempts: 3
      redis_timeout: 500ms
      fail_open: true                   # 【必须 true，false 会拒绝启动】
      batch_read_max: 200
    switch_order:
      record_chain: true
      clear_sticky_on_account_failure: true   # B2（行为变更项）
    max_selection_retries: 0            # D1；发布手册建议值 1
    switchable:
      use_semantic_bytes: true          # B1（行为变更项）
      treat_keepalive_as_safe: true
    idempotency:
      require_key_for_side_effecting: false
      blocked_tools: []
    t3:
      only_extend: true
    expose_failover_detail: false
    terminal:
      mixed_status_503: false           # B3；用户未裁决 → 默认关
    cost_attempts:
      enabled: false
```

### 10.2 三个默认行为变更（发布评审必须逐项确认）

| 编号 | 变更 | 开关 | 代码默认 | 影响 |
| --- | --- | --- | --- | --- |
| B1 | 可切换判据 `Writer.Size()` → 语义字节 | `switchable.use_semantic_bytes` | `true` | keepalive 后现在**允许**换号 → 更多可恢复请求，也更多重复上游调用 |
| B2 | 账号级失败后**清粘性绑定** | `switch_order.clear_sticky_on_account_failure` | `true` | 坏账号不再被同 session 反复命中；代价是缓存亲和性丢失（首 token 变慢） |
| B3 | 混合类别终局错误 → 503 + `Retry-After` | `terminal.mixed_status_503` | **`false`**（design 写 `true`；用户未裁决，按"零默认行为变更"取关） | 开启后客户端退避策略变化 |

> BN-1（jitter）、BN-2（指纹冻结）、BN-3（T3 只延长）见 §5 与 §1.5：BN-1/BN-2 无开关（回滚=回滚提交），BN-3 的开关是 `t3.only_extend`（默认 `true`）。

### 10.3 发布检查清单（每个阶段复制一份）

- [ ] `C-BUILD` / `C-VET` exit 0
- [ ] `C-T-ALL` 与 `C-T-TAG` 都跑过（**不许只跑一档**，见 B-2）
- [ ] Windows 已按 §1.2 临时前置 `sh.exe`；两档完整执行并保留退出码与原始失败，不跳过任何 `TestPgDumper*`
- [ ] `gofmt -l` 无本次改动引入的新文件
- [ ] 基线数与当前数**同树对比**（B-3），记录 diff 文件数与测试计数
- [ ] 本阶段的配置键全部核对过 `config.go`（键名/默认值/校验）
- [ ] 已在隔离环境演练回滚开关（改配置 → 滚动重启 → 观察 → 改回并滚动重启）
- [ ] 观察看板已开，告警阈值已按 §7 配好
- [ ] 值班人知道"本次变更里哪些**不能**靠配置回滚"（§5）

### 10.4 术语

| 术语 | 含义 |
| --- | --- |
| B1 / B2 / B3 | 三项默认行为变更（见 §10.2） |
| BN-1 / BN-2 / BN-3 | jitter / 计费指纹冻结 / T3 只延长（首个无开关） |
| T1 / T2 / T3 | 请求内排除 / 跨请求 Redis 冷却 / DB 临时停用 |
| `policy_source` | 生效策略来源：`account` / `group` / `global`（`legacy` 表示走旧硬编码） |
| `used_default` | 该字段由全局默认值填充，未被上层覆盖 |
