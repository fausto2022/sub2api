# 工单图片上传 + 管理员运营管理模块 · 设计方案

> 版本：**v3.0** · 2026-11-15（根据用户三点澄清重写上游对接方式）
> 状态：待确认后实现
> 依据：
> - relaydeck 真实源码（`gh api repos/fausto2022/relaydeck`，Go，私有库）
> - **用户澄清（v3.0 关键修正）**：
>   1. 上游主站**不是**自己的 sub2api 部署，**只有账号密码，没有管理员 API Key**
>   2. 上游也要添加 Key，本系统就是**添加账号**，本质是**对接渠道**
>   3. 需要「保证收入」概念，且**每个分组可单独设置最低利润率**

## v3.0 相对 v2.0 的三处重大修正

| 项 | v2.0（错） | v3.0（对） |
|---|---|---|
| 上游鉴权 | 只用 `x-api-key` | **账号密码登录**：`POST /api/v1/auth/login` → `access_token` + `refresh_token`，自动续期（relaydeck 的 `Login` 就是这么做的）；**上游 Key 另存**用于取账号凭据 |
| 数据来源 | 全部读管理员接口 `/admin/groups`、`/admin/usage/group-stats` | 用**普通用户接口**：`GET /api/v1/groups/available`（全部分组）、`GET /api/v1/groups/rates`（倍率）。这类接口普通账号即可访问 |
| 「扩池」语义 | 从上游拉账号 | **本系统新增账号**：把上游拿到的 Key 作为本系统账号的凭据，即「对接渠道」 |
| 最低利润率 | 全局单一值 | **全局默认 + 分组级覆盖**（每个分组可单独设） |
| 保证收入 | 未实现 | **实现**：`TodayGuaranteedRevenue` + `GuaranteedRevenueRatioBP` |


---

# 第一部分：工单图片上传

## 1.1 约束

| 项 | 决策 | 理由 |
|---|---|---|
| 数量上限 | **3 张/条** | 按需求 |
| 单张大小 | **5 MB** | 高分屏 PNG 截图常见 1–3 MB |
| 单次请求总量 | **12 MB** | 3×5MB + 余量 |
| 格式白名单 | `PNG` / `JPEG` / `WebP` / `GIF`（仅首帧） | **不收 SVG**（可携带脚本，XSS 载体） |
| 校验 | **魔数嗅探优先**，`Content-Type` 仅参考 | 客户端 MIME 可伪造；仓库已有 `detectImageContentType()` |
| 尺寸上限 | 宽高 ≤ 8000 px | 用 `image.DecodeConfig` 只读头，防解压炸弹 |
| 用户日配额 | 50 张 | 防刷盘，超限 429 |

## 1.2 存储选型

**决策：默认本地磁盘 + 鉴权代理读取；可选 S3 兼容（复用仓库已有 S3 客户端）。**

| 方案 | 结论 | 理由 |
|---|---|---|
| 数据库 BYTEA 内联 | ❌ | 撑大 PG、备份膨胀、无法走 CDN |
| **本地磁盘 + 鉴权代理** | ✅ 默认 | 零依赖、部署即用；**不暴露静态目录**，走 `/tickets/attachments/:id` 鉴权读取 |
| S3 兼容对象存储 | ✅ 可选 | 多实例友好；复用现有 S3 客户端但**私有 ACL** |

**为什么不复用现有 `ImageStorage`**：它是给「异步图片任务结果」用的公开可读存储。工单附件是**私密内容**，必须鉴权，故新建 `TicketAttachmentStorage` 接口（`Put/Get/Delete`），S3 实现内部可复用客户端。

**存储路径**：`data/ticket-attachments/{yyyy}/{mm}/{uuid}.{ext}`，写入用「临时文件 + rename」保证原子性。

## 1.3 数据模型

```sql
CREATE TABLE ticket_attachments (
  id            bigserial PRIMARY KEY,
  ticket_id     bigint      NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  reply_id      bigint      NULL     REFERENCES ticket_replies(id) ON DELETE CASCADE,
  uploader_id   bigint      NOT NULL,
  storage_key   text        NOT NULL,   -- UUID，不含路径前缀
  storage_kind  varchar(16) NOT NULL,   -- local | s3
  original_name varchar(255) NOT NULL,
  content_type  varchar(64)  NOT NULL,
  byte_size     bigint       NOT NULL,
  width         int          NULL,
  height        int          NULL,
  sha256        char(64)     NOT NULL,  -- 去重 + 完整性
  created_at    timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX idx_ticket_attachments_ticket ON ticket_attachments(ticket_id);
CREATE INDEX idx_ticket_attachments_reply  ON ticket_attachments(reply_id);
CREATE INDEX idx_ticket_attachments_orphan ON ticket_attachments(created_at) WHERE reply_id IS NULL;
```

**与工单数据的关联**：
- `ticket_id` 必填 → 任何附件可追溯；工单硬删时级联清理
- `reply_id` 可空 → 支持「先上传后提交」两阶段；提交时回填
- `sha256` → 同用户重复上传同图复用记录，节省存储

## 1.4 两阶段上传流程

```
① POST /api/v1/tickets/attachments  (multipart)
   校验 → 落盘 → 写 ticket_attachments(reply_id=NULL)
   → {id, url, width, height}

② POST /api/v1/tickets            body 带 attachment_ids:[...]
   POST /api/v1/tickets/:id/replies body 带 attachment_ids:[...]
   事务内：写 ticket/reply → 回填 reply_id
   校验 attachment.uploader_id == 当前用户（防绑定他人附件）
   数量 ≤ 3，否则 400

③ 后台任务每小时清理 reply_id IS NULL 且 created_at < now()-2h 的孤儿附件 + 磁盘文件
```

## 1.5 权限矩阵

| 请求者 | 读取条件 |
|---|---|
| 工单提交人 | `ticket.user_id == 自己` |
| 管理员 | 全部 |
| 其他人 | **404**（不暴露存在性） |

## 1.6 安全要点

| 威胁 | 防护 |
|---|---|
| 伪造扩展名 | 魔数嗅探为准，扩展名由嗅探结果反推 |
| SVG XSS | 白名单不含 SVG |
| 图片炸弹 | `DecodeConfig` 只读头，宽高 ≤ 8000 |
| 越权读取 | 权限矩阵 + 404 |
| 越权绑定 | 提交时校验 `uploader_id == 当前用户` 且未绑定 |
| 磁盘占满 | 日配额 50 + 单工单上限 15 + 孤儿清理 |
| 路径猜测 | key 用 UUIDv4，读取走 `attachment_id` |

## 1.7 API

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/v1/tickets/attachments` | 上传单文件 |
| `GET` | `/api/v1/tickets/attachments/:id` | 鉴权代理读取（`Cache-Control: private, max-age=3600`） |
| `DELETE` | `/api/v1/tickets/attachments/:id` | 删除未绑定附件 |
| — | `GET /tickets/:id` 与 `GET /admin/tickets/:id` | 响应 `replies[].attachments[]` |

## 1.8 前端

`TicketAttachmentUploader.vue`：
- 拖拽 / 点击 / **粘贴（Ctrl+V 截图）**
- 客户端预校验（大小/类型）快速反馈，服务端二次校验
- 缩略图网格 + 删除 + 上传进度
- 对话流中渲染缩略图，点击看原图（因不收 SVG，可安全 `<img>`）

---

# 第二部分：运营管理模块（严格对齐 relaydeck）

## 2.1 relaydeck 真实架构（已读源码）

```
relaydeck
├── connector/sub2api/        ← 对接上游 Sub2API 管理端 API（x-api-key 鉴权）
│   ├── admin.go              ← AdminGroup / AdminChannel / AdminAccount / AdminProxy
│   └── admin_test.go
├── mainstation/
│   ├── profit.go             ← 利润评估：marginPolicy + resolvedCost + buildProfitCheck
│   ├── profit_summary.go     ← 今日/7日 收入·成本·利润（来自上游 ListGroupUsageStats）
│   ├── auto_expand.go        ← 扩池
│   ├── channel_deactivation.go ← 自动停用
│   ├── pools.go / bindings.go ← 池与绑定关系
│   ├── guard locks           ← ActivateGuardLock/ClearGuardLock（幂等锁）
│   ├── protection.go / schedule.go / sync.go
│   └── notifyProfitTransition ← 状态迁移通知
├── notify/                   ← 钉钉/飞书/企微/Telegram/邮件/Webhook/ServerChan
└── storage/                  ← SQLite/MySQL（GORM）
```

**关键设计（值得移植）**：

1. **`x-api-key` 而非账号密码**：`AdminClient` 用 `AdminTarget{BaseURL, APIKey}` 调用上游 `/api/v1/admin/*`。比登录拿 JWT 简单可靠。→ **修正 v1.0 的假设**。
2. **`AdminGroup` 含完整定价字段**：`Ratio`、`RateMultiplier`、`PeakEnabled/PeakStart/PeakEnd/PeakMultiplier`、`ImageSeparateRate`、`VideoSeparateRate`、`PricingMetadata`。倍率不只是一个数字。
3. **`AdminAccount` 含 `Credentials map[string]any`**：扩池时可直接取上游账号凭据。
4. **利润 = 收入 − 成本**，其中：
   - 收入 = 上游 `ListGroupUsageStats` 的 `ActualCost`
   - 成本 = 上游的 `AccountCost`
   - 即 **relaydeck 自己不参与计费**，完全以上游报表为准。→ **这消除了 v1.0 中「改计费链路」的高风险项**。
5. **`marginPolicy` 精细**：
   ```go
   type marginPolicy struct {
       Mode                     string // 模式
       MinimumMarginBasisPoints int64  // 最低利润率（基点，0–9900）
       RiskConfirmations        int    // 连续 N 次确认才动作（防抖动）
       CostMaxAgeMinutes        int    // 成本数据最大时效
   }
   ```
6. **`resolvedCost` 带信任模型**：`Micros/Source/Observed/ExpiresAt/Reliable/Reason` —— 成本数据过期或不可靠时**不做决策**，避免误停用。
7. **`GuardLock` 机制**：对上游账号加 `margin` 类型锁，幂等（已存在不重复加），自动恢复时 `ClearGuardLock`。
8. **`AutoRecovery`**：利润率恢复后自动解锁，形成闭环。
9. **`ProfitCheck` 历史表**：每次评估留痕（`ListProfitChecksSince`），用于 `RiskConfirmations` 判定与审计。
10. **`notifyProfitTransition`**：状态健康↔风险的迁移才通知（避免每次评估都刷屏）。

## 2.2 上游对接方式（**v3.0 按澄清重写**）

### 2.2.1 核心结论：用「账号密码登录 + 普通用户接口」，不用管理员 Key

**用户澄清**：上游主站不是自己部署的 sub2api，没有管理员 API Key，**只有账号密码**。

因此上游对接分两步：

```
① 账号密码登录（拿普通用户身份）
   POST {base_url}/api/v1/auth/login
   body: {email, password}
   → {access_token, refresh_token, expires_in}
   → 后续请求带 Authorization: Bearer {access_token}
   → access_token 过期用 refresh_token 续期（relaydeck 的 Login/Refresh 就是这么做的）

② 用普通用户接口拉数据（无需管理员权限）
   GET /api/v1/groups/available   → 该账号可见的全部分组（含 rate_multiplier）
   GET /api/v1/groups/rates       → 用户专属分组倍率
   GET /api/v1/profile            → 当前账号余额/信息
```

**已在 sub2api 源码中确认这两个接口存在**：
- `backend/internal/server/routes/user.go`：`groups.GET("/available", h.APIKey.GetAvailableGroups)`、`groups.GET("/rates", h.APIKey.GetUserGroupRates)`
- 二者都只依赖 `middleware.GetAuthSubjectFromContext`（普通用户身份），**不需要管理员权限**

### 2.2.2 上游 Key 的作用：提供账号凭据

**用户澄清**：上游也要添加 Key，本系统就是添加账号，本质是**对接渠道**。

```
上游站点配置中额外存一个「上游 API Key」（不是管理员 Key，而是该站点开出来的普通 API Key）
用途：作为本系统 Channel 的上游凭据（channel.api_key）

扩池流程：
  选上游站点 → 登录拿分组列表
  → 选上游分组 + 填该分组对应的上游 API Key
  → 本系统创建 Channel（api_key = 上游 Key，base_url = 上游站点地址，group_ids = 本地目标分组）
  → 打 ops_site_id / ops_remote_group_id 标签
```

**这就是「对接渠道」**：本系统的 Channel 指向上游站点，用上游开出的 Key 转发请求。

### 2.2.3 relaydeck 与我们的差异（明确）

| 维度 | relaydeck | 本方案 |
|---|---|---|
| 上游身份 | 管理员 API Key（`x-api-key`） | **账号密码登录（普通用户）** |
| 能看到的范围 | 全部账号、全部用量报表 | **仅该账号可见的分组与自身倍率** |
| 「扩池」 | 改上游账号状态 | **本系统新增 Channel（对接渠道）** |
| 利润控制对象 | 上游账号 | **本系统 Channel** |
| 盈亏数据来源 | 上游 `group-stats` 报表 | **本系统自带 `usage_logs`（已有 input_cost/output_cost 字段）+ 上游分组倍率推算** |

**本系统 `usage_logs` 已含成本字段**（源码确认）：`input_cost` / `output_cost` / `cache_creation_cost` / `cache_read_cost` 等，**无需改计费链路**。

## 2.3 移植方案：从 relaydeck 搬什么（逻辑层，不搬数据层）

| relaydeck 模块 | 移植方式 |
|---|---|
| `connector/sub2api` 的 Login/Refresh | **移植**为 `ops_upstream_auth.go`（账号密码登录 + token 续期） |
| `mainstation/profit.go` 的 marginPolicy/resolvedCost/buildProfitCheck | **移植**为 `ops_profit.go`（纯逻辑，可单测） |
| `mainstation/profit_summary.go` 的 ProfitSummary 结构（含 GuaranteedRevenue） | **移植结构**，数据源改为本系统 `usage_logs` |
| `auto_expand.go` 的批量创建流程与进度 | **移植**为 `ops_expand.go`（改为创建本地 Channel） |
| `channel_deactivation.go` 的停用逻辑 | **移植**为 `ops_deactivation.go` |
| GuardLock（幂等锁） | **移植**为 `ops_guard_locks` 表 + service |
| `notify/` 多渠道 | **不移植**，复用本系统 `NotificationEmailService` |

**关键架构判断**：
> 本系统既是「售卖方」也是「运营方」——利润 = 本系统计费的收入(`actual_cost`) − 按上游倍率推算的成本。因此**不需要上游报表**，用自己的 `usage_logs` 即可，且更实时。

## 2.3 数据模型（对齐 relaydeck 语义）

```sql
-- 上游站点（账号密码登录，非管理员 Key）
CREATE TABLE ops_upstream_sites (
  id                bigserial PRIMARY KEY,
  name              varchar(100) NOT NULL,
  base_url          text NOT NULL,
  auth_mode         varchar(16) NOT NULL DEFAULT 'password',  -- password（当前唯一）
  username          text NOT NULL,                            -- 登录邮箱/用户名
  password_enc      text NOT NULL,                            -- 加密，复用 security_secret
  -- 登录态缓存（relaydeck 同款：access + refresh）
  access_token_enc  text NULL,
  refresh_token_enc text NULL,
  token_expires_at  timestamptz NULL,
  -- 该站点的上游 API Key（用于本系统 Channel 凭据，非管理员 Key）
  upstream_api_key_enc text NULL,
  status            varchar(16) NOT NULL DEFAULT 'active',
  last_synced_at    timestamptz NULL,
  last_error        text NULL,
  sync_interval_minutes int NOT NULL DEFAULT 30,
  created_at / updated_at
);

-- 上游分组快照（对齐 AdminGroup）
CREATE TABLE ops_upstream_groups (
  id              bigserial PRIMARY KEY,
  site_id         bigint NOT NULL REFERENCES ops_upstream_sites(id) ON DELETE CASCADE,
  remote_group_id bigint NOT NULL,
  name            varchar(200) NOT NULL,
  platform        varchar(32) NULL,
  ratio           numeric(10,4) NOT NULL DEFAULT 1,
  rate_multiplier numeric(10,4) NOT NULL DEFAULT 1,
  peak_enabled    boolean NOT NULL DEFAULT false,
  peak_start      varchar(8), peak_end varchar(8),
  peak_multiplier numeric(10,4) NOT NULL DEFAULT 1,
  image_separate_rate boolean NOT NULL DEFAULT false,
  video_separate_rate boolean NOT NULL DEFAULT false,
  is_exclusive    boolean NOT NULL DEFAULT false,
  synced_at       timestamptz NOT NULL,
  UNIQUE (site_id, remote_group_id)
);

-- 上游账号快照（对齐 AdminAccount）
CREATE TABLE ops_upstream_accounts (
  id                bigserial PRIMARY KEY,
  site_id           bigint NOT NULL,
  remote_account_id bigint NOT NULL,
  name              varchar(200),
  platform          varchar(32),
  type              varchar(32),
  status            varchar(16),
  schedulable       boolean,
  rate_multiplier   numeric(10,4),
  group_ids         bigint[],
  synced_at         timestamptz NOT NULL,
  UNIQUE (site_id, remote_account_id)
);

-- 利润检查历史（对齐 ProfitCheck）
CREATE TABLE ops_profit_checks (
  id                   bigserial PRIMARY KEY,
  site_id              bigint NOT NULL,
  remote_group_id      bigint NOT NULL,
  group_name           varchar(200),
  member_ref           varchar(200),        -- 本地账号引用
  revenue_micros       bigint NOT NULL DEFAULT 0,
  cost_micros          bigint NOT NULL DEFAULT 0,
  margin_basis_points  bigint NOT NULL DEFAULT 0,
  status               varchar(16) NOT NULL,   -- healthy | risk | unsupported | unknown
  cost_source          varchar(32),
  cost_observed_at     timestamptz,
  cost_reliable        boolean NOT NULL DEFAULT false,
  cost_reason          text,
  created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON ops_profit_checks(site_id, remote_group_id, created_at DESC);

-- 守护锁（对齐 GuardLock，防重复停用）
CREATE TABLE ops_guard_locks (
  id                bigserial PRIMARY KEY,
  site_id           bigint NOT NULL,
  target_ref        varchar(200) NOT NULL,   -- 本地账号/分组引用
  lock_type         varchar(32) NOT NULL,    -- margin | manual | ...
  active            boolean NOT NULL DEFAULT true,
  reason            text,
  metadata          jsonb,
  activated_at      timestamptz NOT NULL DEFAULT now(),
  cleared_at        timestamptz NULL,
  UNIQUE (site_id, target_ref, lock_type, active) -- 简化：active 时唯一由应用层保证
);

-- 运营配置（全局，对齐 relaydeck 的 config）
CREATE TABLE ops_settings (
  key   varchar(64) PRIMARY KEY,
  value jsonb NOT NULL
);
-- 关键键：auto_margin_protection(bool) / auto_recovery(bool) /
--         minimum_margin_basis_points(int) / risk_confirmations(int) /
--         cost_max_age_minutes(int) / auto_margin_protection_enabled
```

**本地账号扩展**（在现有 `channels` 或 `accounts` 表标注来源）：

```sql
ALTER TABLE channels ADD COLUMN ops_site_id bigint NULL REFERENCES ops_upstream_sites(id);
ALTER TABLE channels ADD COLUMN ops_remote_account_id bigint NULL;
ALTER TABLE channels ADD COLUMN ops_auto_disabled_at timestamptz NULL;
ALTER TABLE channels ADD COLUMN ops_auto_disabled_reason varchar(64) NULL;
CREATE INDEX ON channels(ops_site_id);
```

## 2.4 今日盈利统计口径（**v3.0 改为用本系统 usage_logs**）

### 2.4.1 数据来源与公式

**不再依赖上游报表**（因为没有管理员权限），改用本系统已有的 `usage_logs`：

```go
type ProfitSummary struct {
    Available bool

    // 今日
    TodayRevenue           float64  // Σ usage_logs.actual_cost（用户实付）
    TodayGuaranteedRevenue float64  // 保底收入（见 §2.4.2）
    TodayCost              float64  // Σ (input_cost+output_cost+cache_*_cost)（按上游倍率算的成本）
    TodayProfit            float64  // Revenue - Cost
    TodayMarginBasisPoints int64    // 利润率（基点）

    // 近 7 日
    SevenDayRevenue / SevenDayGuaranteedRevenue / SevenDayCost / SevenDayProfit
    SampledDays      int
    Complete         bool
    GuaranteedRevenueRatioBP int64  // 保底收入占比（基点）
    LastSampledAt    *time.Time
}
```

**关键**：`usage_logs` 已含成本字段（源码确认：`input_cost`/`output_cost`/`cache_creation_cost`/`cache_read_cost`），**无需改计费链路**。

**成本口径**：`usage_logs` 里的 cost 字段本身就是「按上游价计算的成本」——这正是 sub2api 计费链路产出的。因此：

```
今日收入 = Σ actual_cost          （实际向用户收取，含分组倍率折扣）
今日成本 = Σ total_cost            （按上游价计算的成本）
今日利润 = 收入 − 成本
```

### 2.4.2 「保证收入」概念（用户要求实现）

**定义**：保底收入 = 即使分组倍率打折，也按约定比例计入的兜底收入。用于评估「折扣策略是否侵蚀利润」。

```go
// 实现取「除法」：从实际收到的收入反推「若不打折应得的毛收入」。
// 例：保底比例 90%（9000 bp），实际收到 100 → 反推毛收入 = 100 / 0.90 ≈ 111.11。
// 默认 10000 bp（不打折）时，保底收入恒等于实际收入。
// 注意：不是「×保底比例」，那样 100 会变成 90，语义相反。
TodayGuaranteedRevenue = TodayRevenue / GuaranteedRateRatio

// 保底收入占比（基点）= 保底毛收入 ÷ 实际收入 × 10000。
// 不打折时 = 10000；打折时 > 10000（上例 111.11/100 ≈ 11111），用于量化折扣幅度。
GuaranteedRevenueRatioBP = (GuaranteedRevenue / Revenue) × 10000
```

**配置项**（`ops_settings`）：
| 键 | 默认 | 说明 |
|---|---|---|
| `guaranteed_revenue_ratio_bp` | 10000（100%） | 保底收入比例；10000=不打折，9000=按90%保底 |

**展示**：看板「今日收入」卡片下方显示小字「保底 $xxx（90%）」，让运营者一眼看出折扣影响。

## 2.5 利润管理：分组级最低利润率 + 倍率变动自动停用

### 2.5.1 分组级最低利润率（**v3.0 新增**）

**用户明确要求：「可以给每个分组设置最低利润」**。

配置层级（就近覆盖）：

```
全局默认 ops_settings.minimum_margin_basis_points（默认 1000 = 10%）
   ↓ 被覆盖
分组级 groups.ops_min_margin_basis_points（nullable，NULL 表示继承全局）
   ↓ 被覆盖
上游分组级 ops_upstream_groups.min_margin_basis_points（可空，用于「这个上游分组我要求 15%」）
```

**取用优先级**：上游分组配置 > 本地分组配置 > 全局默认。

**迁移**：
```sql
ALTER TABLE groups ADD COLUMN ops_min_margin_basis_points bigint NULL;
ALTER TABLE ops_upstream_groups ADD COLUMN min_margin_basis_points bigint NULL;
```

**UI**：分组编辑对话框新增「运营管理」区块：
```
┌ 运营管理 · 利润保护
│ 最低利润率：[10]%  ☐ 继承全局设置
│ 提示：低于此利润率时，该分组的账号将在连续 N 次评估后被自动停用
└
```

### 2.5.2 自动停用判定（移植 relaydeck 的防抖与可靠性模型）

**对齐 relaydeck `EvaluatePool` 的核心循环**：

```
每次同步上游分组后，对每个「本地账号 → 上游分组」映射：

1. resolveMemberCost(member, policy, now)
   → 成本 = 上游分组倍率 × 用量；带 Source/Observed/ExpiresAt/Reliable
   → 若成本不可靠（过期/缺失）→ status = unsupported/unknown，不动作

2. buildProfitCheck(...)
   → margin_basis_points = (revenue - cost) / revenue × 10000
   → status: healthy（margin ≥ 阈值）/ risk（margin < 阈值）

3. wouldDisable 判定：连续 risk 达 risk_confirmations 次才真停用（防抖动）

4. 若 AutoMarginProtection && 确认风险:
     ActivateGuardLock(account, "margin", reason)  ← 幂等
     本地账号 status = disabled, ops_auto_disabled_reason = "margin"

5. 若 AutoRecovery && 全部 healthy && 已有锁:
     marginRecoveryConfirmed(...) → ClearGuardLock → 恢复账号
```

**与 relaydeck 的差异**：
- relaydeck 改**上游**账号状态（`ActivateGuardLock(remoteAccountID)`）
- 本方案改**本地**账号状态 + 记录 `ops_auto_disabled_*`
- 保留 `GuardLock` 语义做幂等与审计

**配置项**（`ops_settings`）：
| 键 | 默认 | 说明 |
|---|---|---|
| `auto_margin_protection` | false | 关闭为「仅告警」灰度模式 |
| `auto_recovery` | true | 利润率恢复后自动启用 |
| `minimum_margin_basis_points` | 1000（10%） | 最低利润率 |
| `risk_confirmations` | 2 | 连续 N 次风险才停用 |
| `cost_max_age_minutes` | 60 | 成本数据时效 |

## 2.6 扩池 = 对接渠道（**v3.0 按澄清重写**）

**用户澄清**：上游也要添加 Key，本系统就是添加账号，本质是**对接渠道**。

因此「扩池」不是从上游搬账号，而是**在本系统创建一个指向该上游的转发渠道**：

```
① 选择上游站点（已用账号密码登录过，能看到其分组列表与倍率）
② 选择上游分组（如 claude-pro，倍率 0.85x）
③ 填该分组对应的上游 API Key（上游开出的普通 Key）
④ 选择本地目标分组 + 倍率
⑤ 本系统创建 Channel：
     name          = "ups-{site}-{group}"
     base_url      = 上游站点地址
     api_key       = 上游 Key（加密）
     platform      = 上游分组 platform
     model_pricing = 从上游分组倍率推导（或手填模型清单）
     group_ids     = [本地目标分组]
     ops_site_id   = 上游站点 ID   ← 标签
     ops_remote_group_id = 上游分组 ID  ← 标签
⑥ 进度反馈 → 结果：成功 / 跳过（已存在）/ 失败（附原因）
```

**批量扩池**：可一次选多个上游分组，批量创建多个 Channel。

**去重**：`(ops_site_id, ops_remote_group_id, api_key_hash)` 唯一；同一上游分组重复导入则跳过。

**与 relaydeck 的差异**：relaydeck 是「把上游账号导进自己的池并改上游状态」；本方案是「本系统新增一个转发渠道指向上游」。**控制对象是本地 Channel**。

## 2.7 UI 设计

遵循 `design-system/sub2api/MASTER.md` 方向 B（Indigo & Cyan）。

### 信息架构

```
运营管理（侧边栏一级菜单）
├─ 运营看板   /admin/ops/dashboard
├─ 上游站点   /admin/ops/sites
├─ 扩池任务   /admin/ops/expand
└─ 利润管理   /admin/ops/profit
```

### 运营看板

```
┌ 运营看板                                     [今日 ▾] [刷新]
│ ┌──────────┬──────────┬──────────┬──────────┐
│ │ 今日收入  │ 今日成本  │ 今日利润  │ 利润率    │
│ │ $1,284.50│ $892.10  │ $392.40  │ 30.55%   │
│ │ ↑12.3%   │ ↑8.1%    │ ↑22.6%   │ ↑4.2pp   │
│ └──────────┴──────────┴──────────┴──────────┘
│
│ ┌ 近 7 日收入 vs 成本 ────────────────────┐
│ │  ╱╲    ╱╲                              │
│ │ ╱  ╲__╱  ╲___   收入（indigo）          │
│ │ ────────────   成本（灰）               │
│ │ ▒▒▒▒▒▒▒▒▒▒▒    利润（青，面积）          │
│ └─────────────────────────────────────────┘
│
│ ┌ 分组盈利榜 ─────────────────────────────┐
│ │ claude-pro  ████████████░ $820  margin 32%│
│ │ gpt-plus    ████████░░░░░ $464  margin 18%│
│ │ gemini-max  ██░░░░░░░░░░░ $120  margin -4%│ ← 亏损行淡红底
│ └─────────────────────────────────────────┘
└
```

**设计要点**：
- KPI 卡用 `tabular-nums` + 环比箭头
- **颜色语义严格区分**：收入/利润↑=绿，成本↑=红；但必须**同时加文字标签**（避免仅靠颜色传达信息，符合无障碍要求）
- 亏损分组：`bg-danger/10` 淡红底 + 「亏损」徽章
- 数据不可用时显示「成本数据不可用（上游未提供）」而非 0，避免误导

### 上游站点详情

```
┌ 上游站点：api.example.com              [同步] [编辑] [禁用]
│ 状态：正常 · 上次同步 3 分钟前 · 下次 27 分钟后
├ 分组列表
│ ┌────────────┬────────┬────────────────┬────────┐
│ │ 分组名      │ 平台   │ 倍率            │ 本地映射│
│ │ claude-pro │ claude │ 0.85x          │ 已映射  │
│ │ gpt-plus   │ openai │ 1.20x ↑ (0.95) │ 未映射  │ ← 变动高亮
│ │ gemini-max │ gemini │ 1.45x ↑ (1.10) │ 已映射⚠ │ ← 触发停用
│ └────────────┴────────┴────────────────┴────────┘
│
├ 账号列表（本系统为该站点创建的账号）
│ ┌──────────────┬────────┬────────┬──────────┐
│ │ 账号          │ 平台   │ 状态    │ 来源     │
│ │ ups-001      │ claude │ 正常    │ ⬢ 运营管理│
│ │ ups-002      │ claude │ 自动停用 │ ⬢ 运营管理│
│ └──────────────┴────────┴────────┴──────────┘
└
```

**倍率变动视觉**：`1.20x ↑ (0.95)` —— 箭头 + 原值灰字；上涨用 `text-danger`，下降用 `text-success`。

### 账号列表「运营管理」标识（重点需求）

在现有 `ChannelsView` / `AccountsView` 增加 **`SourceBadge`** 列：

```
┌────────────────────────────────────────────────────────────┐
│ 账号          │ 平台   │ 分组 │ 来源                │ 状态     │
├────────────────────────────────────────────────────────────┤
│ local-001     │ claude │ pro  │ 手工创建             │ 正常     │
│ ups-001       │ claude │ pro  │ ⬢ 运营管理           │ 正常     │
│               │        │      │   api.example.com    │          │
│ ups-002       │ claude │ pro  │ ⬢ 运营管理           │ 自动停用 │
│               │        │      │   api.example.com    │ ⚠ 利润不足│
└────────────────────────────────────────────────────────────┘
筛选：来源 ▾ [全部 / 手工创建 / 运营管理]
```

- `SourceBadge` 组件：`⬢ 运营管理` + 悬浮显示站点名/上游分组/远程账号 ID
- 自动停用：`StatusBadge` 悬浮显示「上游倍率 0.95x → 1.20x，利润率 −4% 低于 10%」
- 支持按来源筛选与批量恢复

### 利润管理页

```
┌ 利润管理
│ 全局开关：[☑ 自动利润保护] [☑ 自动恢复]  最低利润率 [10]%  确认次数 [2]
├ 待处理（需要人工确认）
│ ┌────────────┬──────────┬────────┬────────┬────────┐
│ │ 分组        │ 倍率变化  │ 利润率  │ 影响账号│ 操作   │
│ │ gemini-max │ 1.10→1.45│ -4.2%  │ 3 个   │[停用][忽略]│
│ └────────────┴──────────┴────────┴────────┴────────┘
├ 已自动停用（可一键恢复）
│ ┌────────────┬──────────┬──────────┬────────┐
│ │ 账号        │ 停用原因  │ 停用时间  │ 操作   │
│ │ ups-002    │ 利润率不足│ 12:31    │ [恢复] │
│ └────────────┴──────────┴──────────┴────────┘
└ 变更历史（ProfitCheck 时间线）
```

**灰度设计**：`auto_margin_protection=false` 时只记录 `wouldDisable` 并告警，不真停用。上线建议先跑 1–2 天观察。

## 2.8 API 契约（**v3.0 按账号密码登录重写**）

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/admin/ops/dashboard?range=today\|7d` | ProfitSummary（含保底收入）+ 分组盈利榜 |
| `GET/POST` | `/admin/ops/sites` | 上游站点列表 / 新增（name + base_url + username + password + 可选 upstream_api_key） |
| `GET/PUT/DELETE` | `/admin/ops/sites/:id` | 详情 / 编辑 / 删除 |
| `POST` | `/admin/ops/sites/:id/test` | **登录连通性测试**（POST /api/v1/auth/login） |
| `POST` | `/admin/ops/sites/:id/sync` | 手动同步（异步任务） |
| `GET` | `/admin/ops/sites/:id/groups` | 上游分组列表（来自 `GET /groups/available` + `/groups/rates`，含倍率与变动标记） |
| `POST` | `/admin/ops/expand` | **对接渠道**：`{site_id, remote_group_id, upstream_api_key, target_group_id, rate_multiplier?}` → 创建本地 Channel |
| `GET` | `/admin/ops/tasks` | 任务列表与进度 |
| `GET` | `/admin/ops/profit/checks` | 利润检查历史 |
| `POST` | `/admin/ops/profit/evaluate` | 手动触发评估（灰度观察用） |
| `POST` | `/admin/ops/channels/:id/restore` | 恢复被自动停用的本地 Channel |
| `GET/PUT` | `/admin/ops/settings` | 全局配置（含 guaranteed_revenue_ratio_bp / minimum_margin_basis_points / risk_confirmations / auto_margin_protection / auto_recovery） |
| `PUT` | `/admin/groups/:id/ops-margin` | **分组级最低利润率**（`groups.ops_min_margin_basis_points`，null=继承全局） |


---

# 第三部分：实施计划（多 Agent）

## 3.1 任务分解

| # | 任务 | 负责 | 依赖 | 交付 |
|---|---|---|---|---|
| **T1** | 工单附件存储抽象 + local 实现 + 迁移 252 + 上传/读/删 API + 权限 | be-attach | — | `ticket_attachment*.go`、`252_*.sql` |
| **T2** | 工单创建/回复支持 `attachment_ids` 绑定 + 孤儿清理任务 | be-attach | T1 | service 改动 + 测试 |
| **T3** | 工单附件前端组件 + 对话流渲染 | fe-attach | T1 | 组件 + spec |
| **T4** | 上游站点/分组/账号/利润检查/守护锁 模型 + 迁移 253 + 加密存储 | be-ops | — | ent schema + 迁移 |
| **T5** | 上游客户端（移植 relaydeck connector/sub2api 为只读）+ 同步服务 | be-ops | T4 | `upstream_client.go` + 测试（mock） |
| **T6** | 利润评估引擎（marginPolicy/resolvedCost/buildProfitCheck/GuardLock） | be-ops | T4,T5 | `ops_profit.go` + 单测 |
| **T7** | 运营看板聚合（ProfitSummary，读上游报表） | be-ops | T5 | `ops_dashboard.go` + 测试 |
| **T8** | 扩池服务 + 异步任务 | be-ops | T5 | `ops_expand.go` + 测试 |
| **T9** | 运营管理 handler + 路由 + DTO | be-ops | T4-T8 | handler + 契约测试 |
| **T10** | 运营组件（KpiCard/TrendChart/SourceBadge/RateChangeCell） | fe-ops | — | 组件 + spec |
| **T11** | 运营看板页 + 上游站点页 | fe-ops | T9,T10 | 视图 + spec |
| **T12** | 扩池向导 + 利润管理页 | fe-ops | T9,T10 | 视图 + spec |
| **T13** | 账号/渠道列表「运营管理」标识 + 来源筛选 | fe-ops | T4 | 列表改动 + spec |
| **T14** | 全量验证 | verifier | 全部 | 报告 |
| **T15** | 部署上线 | captain | T14 | 部署记录 |

## 3.2 并行波次

```
Wave 1（并行）：T1 工单附件后端 · T4 ops 数据模型 · T10 运营组件
Wave 2（并行）：T2 工单绑定 · T3 工单附件前端 · T5 上游客户端 · T13 来源标识
Wave 3（并行）：T6 利润引擎 · T7 看板聚合 · T8 扩池 · T11 看板页
Wave 4（并行）：T9 路由组装 · T12 扩池/利润页
Wave 5：T14 全量验证 → T15 部署
```

## 3.3 写作用域隔离

| Agent | 独占路径 |
|---|---|
| be-attach | `backend/internal/service/ticket_attachment*`、`backend/internal/handler/ticket_attachment*`、`backend/migrations/252_*` |
| be-ops | `backend/internal/service/ops_*`、`backend/internal/service/upstream_*`、`backend/internal/handler/admin/ops_*`、`backend/ent/schema/ops_*`、`backend/migrations/253_*` |
| fe-attach | `frontend/src/components/tickets/TicketAttachmentUploader.vue`、`TicketThread.vue` |
| fe-ops | `frontend/src/views/admin/ops/**`、`frontend/src/components/ops/**` |

**共享文件只追加**：`routes/*.go`、`handler.go`、`router/index.ts`、`AppSidebar.vue`、`i18n/*/index.ts`。

---

# 第四部分：验收标准

| # | 验收项 | 方式 |
|---|---|---|
| A1 | 4 张被拒 / 3 张通过 | 单测 |
| A2 | 5MB 通过 / 6MB 被拒 | 单测 |
| A3 | 伪造扩展名（文本改名 .png）被拒 | 魔数测试 |
| A4 | SVG 被拒 | 测试 |
| A5 | 用户 A 读不到用户 B 附件（404） | 越权测试 |
| A6 | 绑定他人附件被拒 | 越权测试 |
| A7 | 孤儿附件 2h 后清理 | 定时任务测试 |
| B1 | 站点添加后能拉分组/账号/倍率 | httptest mock |
| B2 | 倍率 0.95→1.20 触发 ProfitCheck status=risk | 单测 |
| B3 | 连续 risk 达 risk_confirmations 才停用 | 单测（防抖） |
| B4 | 成本不可靠（过期）时不动作 | 单测 |
| B5 | AutoRecovery 生效后恢复账号 | 单测 |
| B6 | 灰度模式（auto_margin_protection=false）只记录不停用 | 单测 |
| C1 | 看板 ProfitSummary 数值正确 | 构造 mock 报表 |
| C2 | 账号列表显示「运营管理」徽章 | 前端测试 |
| C3 | 按来源筛选生效 | 前端测试 |
| C4 | 倍率变动视觉（箭头+原值） | 前端测试 |
| D1 | 全量 typecheck/i18n/test/build | CI 命令 |

---

# 第五部分：与 relaydeck 的差异（v3.0 已按用户澄清定稿）

| # | 差异点 | relaydeck | **本方案（v3.0）** | 状态 |
|---|---|---|---|---|
| 1 | 上游鉴权 | 管理员 `x-api-key` | **账号密码登录**（`POST /auth/login` → access/refresh token） | ✅ 已按澄清 |
| 2 | 数据可见范围 | 全站管理员视角 | **仅该账号可见的分组**（`/groups/available` + `/groups/rates`） | ✅ 已按澄清 |
| 3 | 利润数据来源 | 上游 `group-stats` 报表 | **本系统 `usage_logs`**（已含 cost 字段，无需改计费链路） | ✅ 已按澄清 |
| 4 | 扩池语义 | 把上游账号导入池 | **本系统新增 Channel = 对接渠道**（api_key 来自上游） | ✅ 已按澄清 |
| 5 | 控制对象 | 上游账号状态 | **本地 Channel 状态** | ✅ |
| 6 | 最低利润率 | 全局 + 池级 | **全局 + 本地分组级 + 上游分组级**（就近覆盖） | ✅ 已加分组级 |
| 7 | 保证收入 | 有（`GuaranteedRevenue`） | **有**（`guaranteed_revenue_ratio_bp`） | ✅ 已按澄清 |
| 8 | 通知渠道 | 钉钉/飞书/企微/Telegram/邮件/Webhook | 复用本系统 `NotificationEmailService` + 可选 webhook | 按需 |
| 9 | 存储 | SQLite/MySQL（GORM） | 本系统 PostgreSQL（ent） | ✅ |
| 10 | 防抖与可靠性 | `risk_confirmations` + `resolvedCost.Reliable` + GuardLock | **全部移植** | ✅ |

**已无待确认项**（原第五部分的 7 个问题已由用户三点澄清全部解决）。


---

# 第六部分：风险

| 风险 | 等级 | 缓解 |
|---|---|---|
| 误停用影响业务 | **高** | `risk_confirmations` 防抖 + 灰度开关 + 一键恢复 + 审计历史 |
| 上游报表不可用导致看板空白 | 中 | 显示「数据不可用」而非 0；保留上次快照 |
| 上游 API Key 泄露 | **高** | 加密存储（复用 `security_secret`）+ 日志脱敏 + 仅管理员可见 |
| 扩池产生大量账号压力上游 | 中 | 数量上限 + 速率限制 + 任务可中止 |
| 利润数据与实际不符 | 中 | 以上游为准；提供对账视图（本地 vs 上游） |
| 附件磁盘占满 | 中 | 日配额 + 孤儿清理 + 单工单上限 |
