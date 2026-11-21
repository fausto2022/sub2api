# 设计：工单 / 模型价格 / 人民币口径 / UI 重构

## 0. 协作方式

- 所有 agent 共享同一工作树，不建分支、不 rebase；按"写作用域"切分文件，避免同文件同段落冲突。
- 共享文件的改动集中在不同区块：`internal/handler/handler.go`（各自新增字段）、`internal/server/routes/admin.go`、`routes/user.go`（各自新增注册行）。
- 阶段顺序：**设计令牌 → 后端实现 → 前端实现 → 全量验证 → 集成**。前端实现依赖对应后端接口契约（本文件第 2、3 节即契约），后端未落地前前端可先按契约编码，集成阶段对齐。
- 每个实现任务必须自带测试；验证任务只做"跑命令 + 对照验收"，不改实现（除非是验证任务明确允许的最小修复）。

## 1. 工单系统（ticket-center）

### 1.1 数据模型

新增 ent schema `backend/ent/schema/ticket.go`、`backend/ent/schema/ticket_reply.go`，迁移 `backend/migrations/251_tickets.sql`。

`tickets`：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | bigserial PK | |
| user_id | bigint NOT NULL | 提交人；`ON DELETE CASCADE` 到 users |
| subject | varchar(200) NOT NULL | 标题，TrimSpace 后非空 |
| category | varchar(40) NOT NULL DEFAULT 'general' | general/billing/api/account/other |
| priority | varchar(16) NOT NULL DEFAULT 'normal' | low/normal/high/urgent |
| status | varchar(16) NOT NULL DEFAULT 'open' | open/pending/resolved/closed |
| last_reply_at | timestamptz NULL | 最后回复时间，用于列表排序 |
| last_reply_by | varchar(16) NULL | user/admin |
| reply_count | int NOT NULL DEFAULT 0 | 冗余计数，列表用 |
| created_at / updated_at | timestamptz NOT NULL | |

索引：`(user_id, created_at DESC)`、`(status, last_reply_at DESC)`、`(created_at DESC)`。

`ticket_replies`：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | bigserial PK | |
| ticket_id | bigint NOT NULL | `ON DELETE CASCADE` |
| author_id | bigint NOT NULL | 用户或管理员 id |
| author_role | varchar(16) NOT NULL | user/admin |
| content | text NOT NULL | 1..8000 字符（TrimSpace 后非空） |
| email_notified | boolean NOT NULL DEFAULT false | 管理员回复是否已发信 |
| created_at | timestamptz NOT NULL | |

索引：`(ticket_id, created_at ASC)`。

工单首条内容不单独建表：创建工单时同事务插入一条 `author_role=user` 的 reply，工单详情 = ticket + replies 全量（单工单回复上限 500 条，超限拒绝）。

### 1.2 状态机

```
用户创建            → open   (last_reply_by=user)
管理员回复          → pending (last_reply_by=admin, 发邮件可选)
用户追加回复        → open   (last_reply_by=user)
管理员显式改状态    → 任意合法值（resolved/closed 后用户回复自动回 open）
用户关闭            → closed（仅本人，open/pending/resolved 可关）
管理员关闭          → closed
```

合法状态值固定为 4 个；非法值返回 `400 INVALID_TICKET_STATUS`。已 `closed` 工单拒绝新增回复（`409 TICKET_CLOSED`），管理员可先改回 open。

### 1.3 权限与安全

- 用户接口只按 `user_id = 当前用户` 查询；他人工单统一返回 `404 TICKET_NOT_FOUND`（不暴露存在性）。
- 管理员接口走既有 `adminAuth` 中间件；敏感写操作复用现有审计中间件（无需额外代码）。
- 回复内容按纯文本渲染（`whitespace-pre-wrap`），**禁止 v-html**；后端对内容做 TrimSpace + 长度校验，邮件变量做 HTML 转义。
- 用户创建工单与追加回复挂既有 `panelRateLimiter`（防刷）；建议 20 次/10 分钟的账号级限制，若现有 limiter 不支持自定义额度，则至少挂上默认限流并在 handler 内做 1 分钟 5 次的轻量计数（内存 token bucket）。

### 1.4 API 契约

统一响应沿用现有 `response.Success/Error` 与分页结构（`items/total/page/page_size`）；以下只列业务字段。

**用户侧**（`/api/v1/tickets`，均需登录）

| 方法/路径 | 请求 | 响应 |
|---|---|---|
| `GET /tickets?page&page_size&status` | status 可选 | `{items:[TicketBrief],total,page,page_size}` |
| `POST /tickets` | `{subject,category?,priority?,content}` | `{ticket:TicketBrief, reply:TicketReply}` |
| `GET /tickets/:id` | — | `{ticket:TicketBrief, replies:[TicketReply]}` |
| `POST /tickets/:id/replies` | `{content}` | `{ticket, reply}` |
| `POST /tickets/:id/close` | — | `{ticket}` |

`TicketBrief`：`{id,subject,category,priority,status,reply_count,last_reply_at,last_reply_by,created_at,updated_at}`。
`TicketReply`：`{id,author_id,author_role,author_name,content,email_notified,created_at}`。`author_name` 对用户侧显示管理员为站点名（`site_name`）或 "客服"，对管理侧显示用户邮箱/用户名。

**管理侧**（`/api/v1/admin/tickets`）

| 方法/路径 | 请求 | 响应 |
|---|---|---|
| `GET /admin/tickets?page&page_size&status&priority&search&user_id` | search 匹配标题/内容/用户邮箱 | `{items:[AdminTicketBrief],total,...}` |
| `GET /admin/tickets/:id` | — | `{ticket, replies, user:{id,email,username}}` |
| `POST /admin/tickets/:id/replies` | `{content,notify_email?:bool,status?}` | `{ticket,reply,email_sent:bool,email_error?:string}` |
| `PUT /admin/tickets/:id` | `{status?,priority?}` | `{ticket}` |
| `GET /admin/tickets/stats` | — | `{open,pending,resolved,closed,total}` |

`AdminTicketBrief` = `TicketBrief` + `{user_id,user_email,user_name}`。

**管理员回复发信**：`notify_email` 默认 true；发信失败不回滚回复，响应 `email_sent=false + email_error`，并记录 warn 日志。用户邮箱为空/未验证则跳过并返回 `email_sent=false`。

### 1.5 邮件通知

在 `backend/internal/service/notification_email_service.go` 新增：

- 事件常量 `NotificationEmailEventTicketReply = "ticket.reply"`，加入 `notificationEmailEvents` 有序列表。
- `notificationEmailEventDefinitions`：`Label: "Ticket reply"`、`Category: "support"`、`Optional: false`（服务响应属事务性邮件，不受退订影响）、变量 `recipient_name, site_name, ticket_subject, ticket_id, reply_content, ticket_url`。
- `notificationEmailOfficialTemplates` 提供 zh/en 两套 `notificationEmailCard(...)` HTML；`reply_content` 作为纯文本变量注入（模板层已转义，多行用 `<br>` 需在 runtimeVariables 里转换并保证不二次转义；若模板引擎直接 HTML 转义，则保持换行折叠为空格亦可，需测试断言包含回复正文）。
- 发送入口：`NotificationEmailService.Send(ctx, NotificationEmailSendInput{Event: ticket.reply, UserID, RecipientEmail, Locale, Variables})`；`ticket_url = baseURL + "/tickets/" + id`。
- 测试：事件在 `ListEventInfos` 中出现、zh/en 模板可渲染、变量缺失返回模板错误、发送失败返回 `notificationEmailDeliveryError`。

### 1.6 前端

- 用户：`views/user/TicketsView.vue`（`/tickets`，列表 + 新建弹窗）与 `views/user/TicketDetailView.vue`（`/tickets/:id`，对话流 + 回复框 + 关闭）。
- 管理：`views/admin/TicketsView.vue`（`/admin/tickets`，筛选 + 表格 + 详情抽屉/弹窗）。
- 组件：`components/tickets/TicketStatusBadge.vue`、`TicketThread.vue`、`TicketComposer.vue`、`CreateTicketModal.vue`。
- API：`api/tickets.ts`、`api/admin/tickets.ts`（沿用 `apiClient`/`adminUIRequest` 模式）。
- 路由：用户路由 `requiresAuth`；管理员路由沿用 admin meta；侧边栏新增"工单"（用户）与"工单管理"（管理员，含 open 数角标）。
- i18n：`i18n/locales/zh/tickets.ts`、`en/tickets.ts`、`zh/admin/tickets.ts`、`en/admin/tickets.ts`，并在各 index 注册；`nav.tickets`/`nav.ticketManagement` 同步。
- 空状态、加载态、错误 toast、分页复用 `components/common`（EmptyState/Pagination/Skeleton/Toast）。

## 2. 内置模型价格管理（model-price-admin）

### 2.1 数据来源与写回

- 读取：`PricingService` 内存快照（LiteLLM 目录 + fallback 文件 + override 文件 + 人民币层，见第 3 节）。新增只读方法 `Snapshot()`，返回深拷贝 `map[string]LiteLLMModelPricing` 与元数据（模型数、更新时间、来源文件路径、汇率模式）。
- 写回：既有 `pricing.override_file`（LiteLLM 同格式、字段级浅合并、`null` 删除字段）。新增管理服务负责：读取现有 JSON → 修改单个模型 → 临时文件 + `os.Rename` 原子替换 → 校验 JSON → 调 `PricingService` 公开热重载（新增导出 `ReloadCustomPricingLayers() error`，内部复用 `reloadCustomPricingLayers`）→ 返回最新有效价。
- 覆盖只写**显式提交的字段**；空值/未提交字段保持目录价；"恢复内置"= 删除该模型在 override 文件中的条目（若删除后文件为空则写 `{}`）。
- 未配置 `pricing.override_file` 时，管理页禁用编辑并提示需要在配置中设置路径（只读展示仍可用）。

### 2.2 价格单位约定

- API 一律使用 **USD / 1M tokens**（前端直接展示与输入），后端换算为 per-token（÷1e6）。
- 支持字段：`input`、`output`、`cache_read`、`cache_write`（= cache_creation）、`image_input`、`priority_input`、`priority_output`。首版 UI 至少覆盖前 4 项，其余作为"高级字段"折叠。
- 展示同时给出人民币等值（× `cny_per_usd`）便于运营核对，只读。

### 2.3 API 契约

`GET /api/v1/admin/model-pricing?page&page_size&search&provider&overridden`

```json
{
  "items": [{
    "model": "kimi-k2",
    "provider": "moonshot",
    "mode": "chat",
    "input_usd_per_mtok": 0.56,
    "output_usd_per_mtok": 2.24,
    "cache_read_usd_per_mtok": 0.14,
    "cache_write_usd_per_mtok": 0,
    "source": "fallback|litellm",
    "overridden": false,
    "cny": {"input": 4, "output": 16, "cache_read": 1},
    "effective_updated_at": "2026-11-14T12:00:00Z"
  }],
  "total": 218, "page": 1, "page_size": 20,
  "meta": {
    "cny_mode": "convert|direct",
    "cny_per_usd": 7.14,
    "override_file": "/data/pricing-overrides.json",
    "cny_file": "/data/cny-overrides.json",
    "catalog_updated_at": "...",
    "model_count": 218
  }
}
```

- 搜索按模型名/供应商模糊匹配（大小写不敏感），排序：`overridden desc, model asc`。
- 分页服务端完成；`page_size` 上限 100。

`PUT /api/v1/admin/model-pricing/overrides/:model`

```json
{"input_usd_per_mtok": 1.2, "output_usd_per_mtok": null, "cache_read_usd_per_mtok": 0.2,
 "cache_write_usd_per_mtok": 0, "image_input_usd_per_mtok": null,
 "priority_input_usd_per_mtok": null, "priority_output_usd_per_mtok": null}
```

`null` = 删除该字段覆盖（回退目录值）；数值必须 ≥0 且 < 1e4，非法返回 400 `INVALID_MODEL_PRICE`。响应返回该模型最新有效价与 `overridden` 状态。

`DELETE /api/v1/admin/model-pricing/overrides/:model` → 恢复内置。

### 2.4 强制官方价的让位规则

`BillingService` 对 DeepSeek 有"强制官方价"策略（`getModelPricingAt` 与 `calculateTokenCost` 的 `applyModelSpecificPricingPolicyEx`）。管理员显式覆盖或人民币登记必须优先：

- 新增 `PricingService.HasManagedPricing(model) bool`（override 文件或人民币层命中）。
- `getModelPricingAt` / `calculateTokenCost` 在调用官方价策略时传入 `applyOfficial = (source==LiteLLM) && !HasManagedPricing(model)`。
- 新增测试：`deepseek-v4-pro` 被 override 后 `CalculateCost` 按覆盖价；未覆盖时仍按官方价并叠加峰谷倍率。

## 3. 人民币计费口径（model-pricing-currency）

### 3.1 语义

- 人民币原生价：`CNY / 1M tokens`，按模型登记输入/输出/缓存读/缓存写/图片输入。
- 口径：
  - `convert`（默认）：`USD = CNY / cny_per_usd`，`cny_per_usd` 默认 `7.14`（与现有内置表注释一致），可配 6 位小数。
  - `direct`：`USD = CNY`（即 ¥1 = $1，对应"充值为 1 元人民币兑 1 美元"）。
- 换算在**写入内存前的原始 JSON 层**完成：`applyPricingOverrides` 中先叠加人民币层（生成 `input_cost_per_token = cny/1e6/rate`），再叠加 USD override 文件（显式 USD 覆盖优先级最高）。人民币层对目录中不存在的模型也要可建条目（复用 `mergeOverrideOnlyModels` 思路）。
- 优先级（高→低）：分组定价 > 渠道定价 > USD 显式覆盖 > 人民币登记（换算后）> LiteLLM 目录 > Go 兜底表。
- 生效方式：写文件 + 热重载，无需重启。

### 3.2 存储

- 文件：`pricing.cny_override_file`（新增可选配置项）。为空时按下述规则推导：
  - 有 `pricing.override_file` → 同目录 `cny-overrides.json`；
  - 否则 → `pricing.data_dir` 下 `cny-overrides.json`。
- 格式：

```json
{
  "version": 1,
  "mode": "direct",
  "cny_per_usd": 7.14,
  "models": {
    "kimi-k2": {"input_cny_per_mtok": 4, "output_cny_per_mtok": 16, "cache_read_cny_per_mtok": 1},
    "kimi-k2-thinking": {"input_cny_per_mtok": 4, "output_cny_per_mtok": 16, "cache_read_cny_per_mtok": 1},
    "doubao-embedding-vision": {"input_cny_per_mtok": 0.7, "image_input_cny_per_mtok": 1.8}
  }
}
```

- 原子写（临时文件 + rename），写入前后校验 JSON 合法；损坏文件只告警不阻断启动（与现有 override 行为一致）。
- 初始化种子：首次打开配置页时若文件不存在，不自动创建；UI 提供"导入已知人民币口径模型"按钮，预填上述 3 个模型（值来自 `billing_service.go` 注释中的官方人民币价），由管理员确认后保存。

### 3.3 API 契约

`GET /api/v1/admin/model-pricing/currency`

```json
{"mode":"convert","cny_per_usd":7.14,"file":"...","models":[
  {"model":"kimi-k2","input_cny_per_mtok":4,"output_cny_per_mtok":16,
   "cache_read_cny_per_mtok":1,"cache_write_cny_per_mtok":0,"image_input_cny_per_mtok":0,
   "effective_input_usd_per_mtok":0.56,"overridden":false}]}
```

`PUT /api/v1/admin/model-pricing/currency`：`{mode:"direct|convert", cny_per_usd?:number}`（`convert` 时 `cny_per_usd` 必须 >0，否则 400 `INVALID_CNY_RATE`）。
`PUT /api/v1/admin/model-pricing/currency/models/:model`：人民币字段（≥0、<1e6），`0` 表示该字段不登记。
`DELETE /api/v1/admin/model-pricing/currency/models/:model`。

写操作后统一热重载并返回 `{mode,cny_per_usd,models}`。

### 3.4 前端

- 管理页 `views/admin/ModelPricingView.vue` 两个 Tab：
  1. **内置价格**：搜索/来源筛选、分页表格（模型、供应商、输入/输出/缓存读/缓存写 USD、来源、是否覆盖）、行内"编辑/恢复"、编辑弹窗（USD/百万 tokens，带人民币等值实时预览）。
  2. **人民币口径**：模式开关（直接使用 1:1 / 按汇率折算）、汇率输入（仅 convert 可用）、人民币价格表（可增删模型）、"导入已知模型"按钮、口径切换后的示例对比（如 `kimi-k2` 输入价在两种模式下的 USD 值）。
- 顶部提示条：若 `override_file` 为空则禁用编辑；若某模型存在分组/渠道定价，提示"分组/渠道显式定价优先于此处设置"（静态提示即可）。
- i18n：`zh/en/admin/modelPricing.ts` 注册；侧边栏"模型价格"入口放在"渠道管理"附近。

### 3.5 计费一致性测试（必须）

1. `convert` 模式：`kimi-k2` 输入 1M tokens 成本 = `4/7.14` USD，误差 < 1e-9。
2. `direct` 模式：同模型 = `4` USD，误差 < 1e-9。
3. 切换模式只影响人民币登记模型；`gpt-5.4` 价格不变。
4. USD override 优先于人民币登记；`null` 删除字段后回退人民币层/目录。
5. DeepSeek 强制官方价不覆盖管理员配置（见 2.4）。
6. 损坏的 `cny-overrides.json` 不影响启动与其它模型定价。
7. 热重载：写文件后 5 秒内（或显式 reload 后）新价格生效，并发读不加锁阻塞。

## 4. UI 配色与首页（ui-refresh）

### 4.1 设计流程（skill 驱动）

1. 运行 `ui-ux-pro-max` 的 `--design-system` 获取产品向建议（已完成一次：API Developer Portal / 深色开发者控制台 / Minimalism & Swiss / 等宽字体 + 绿色端点色；查询串 `AI API gateway developer platform SaaS dashboard`）。
2. 产出一页三方向对比（`docs/design/ui-palette-preview.html`，可本地打开），三方向均给出 light/dark 令牌与组件样例；记录对比度数据。
3. 默认实现 **方向 B「Indigo & Cyan」**；A/C 保留为令牌替换即可切换（只改 `tailwind.config.js` 与 CSS 变量）。

### 4.2 三个方向

| 令牌 | A 玉青 Jade Circuit（延续品牌） | **B 靛青 Indigo & Cyan（默认）** | C 石墨青柠 Graphite & Lime |
|---|---|---|---|
| primary-500 | `#17A88A` | `#6366F1` | `#334155` |
| primary-600 | `#0C8A73` | `#4F46E5` | `#1E293B` |
| primary-700 | `#0C6E5D` | `#4338CA` | `#0F172A` |
| 强调/数据色 | `#0284C7` 天蓝 | `#06B6D4` 青 | `#84CC16` 青柠 |
| 成功 | `#059669` | `#10B981` | `#22C55E` |
| 警告/危险 | `#D97706` / `#DC2626` | `#F59E0B` / `#EF4444` | `#EAB308` / `#EF4444` |
| 亮色背景/卡片 | `#F7FAF9` / `#FFFFFF` | `#F6F7FB` / `#FFFFFF` | `#FAFAF9` / `#FFFFFF` |
| 暗色背景/卡片 | `#06110F` / `#0C1A17` | `#070B14` / `#0E1424` | `#0B0C0B` / `#141615` |
| 暗色边框 | `#1B2E29` | `#1E2740` | `#262B26` |
| 气质 | 稳健、延续现有品牌 | 现代开发者 SaaS、对比强、蓝色信任感 | 终端/极客、差异化强 |

B 完整色阶（写入 `tailwind.config.js` 的 `primary`）：

```
50 #EEF2FF  100 #E0E7FF  200 #C7D2FE  300 #A5B4FC  400 #818CF8  500 #6366F1
600 #4F46E5  700 #4338CA  800 #3730A3  900 #312E81  950 #1E1B4B
```

`dark`（暗色表面，写作 `dark-950..50`，语义从深到浅）：

```
950 #070B14  900 #0B1120  800 #131C31  700 #1E2740  600 #2C3852
500 #475573  400 #6E7C9A  300 #9AA6BE  200 #C3CCDC  100 #E2E7F0  50 #F5F7FB
```

其他令牌变更：
- `accent` **保持现有 slate 色阶不变**（大量组件把它当灰阶用，改语义会破坏视觉）。
- `backgroundImage.gradient-primary` → `linear-gradient(135deg,#6366F1 0%,#06B6D4 100%)`；`mesh-gradient` 改为 indigo/cyan 低透明度光斑。
- `boxShadow.glow` → `rgba(99,102,241,0.28)`；`card-hover` 保持中性。
- 新增 `colors.ink` 语义别名（可选）与 `fontFamily.sans` 保留系统栈（**不引入外部字体 CDN**，中文保持 PingFang/微软雅黑）。
- 表格/价格/统计数字启用 `font-variant-numeric: tabular-nums`（在 `style.css` 的 `.tabular-nums` 组件类或全局 `table`）。
- 焦点环统一 `focus-visible:ring-2 ring-primary-500/60`；`::selection` 用 primary；滚动条用 `dark-600`。

对比度硬性要求：亮色下 primary-600 上的白字 ≥4.5:1、正文灰 ≥4.5:1；暗色下 primary-400 文本对 `#070B14` ≥4.5:1。设计任务必须在 MASTER.md 记录实测比值。

### 4.3 首页信息架构

保留三种模式优先级：`hasHomeContent`（自定义 HTML/iframe）> `compactHomeEnabled`（紧凑页）> **默认落地页（本次重做）**。

默认落地页区块：

1. **导航**：Logo + 站点名、文档、模型广场（`showModelPlazaEntry` 时）、语言切换、主题切换、登录/控制台 CTA。
2. **Hero**：一句话价值主张（"一个 API，接入全系前沿模型"）、副标题（统一 OpenAI/Claude/Gemini 兼容协议、按量计费、密钥与配额管理）、双 CTA（免费注册 / 查看模型）、右侧或下方代码示例卡（`curl` 示例使用公开设置中的 API base，一键复制）。
3. **能力矩阵**：6 张卡片（多模型统一接入、协议兼容、按量计费与订阅、密钥与配额、渠道监控、用量与账单）。
4. **模型矩阵**：按供应商分组的模型名/图标墙（复用 `ModelIcon` 与 `@lobehub/icons`，静态清单，不新增后端依赖）。
5. **计费 CTA**：按 `publicSettings` 的余额/订阅开关展示对应入口（充值 or 订阅），未登录跳登录。
6. **页脚**：站点名、文档、联系信息（`contactInfo`）、版本号（`VersionBadge`）、备案/版权行。

约束：全部文案走 i18n（zh/en 键齐全，跑 `pnpm check:i18n`）；响应式 375/768/1024/1440；键盘可达；`prefers-reduced-motion` 下关闭装饰动画；不伪造"99.99% 可用性"等不可验证数据。

## 5. 验证策略

### 5.1 命令

后端（工作目录 `backend`）：

```
go build ./...
go test ./internal/service/... -run 'Ticket|ModelPricing|Currency|PricingOverride|DeepSeek|NotificationEmail' -count=1
go test ./internal/handler/... -run 'Ticket|ModelPricing' -count=1
go test ./internal/server/... -count=1
go test ./internal/migrations/... -count=1
```

前端（工作目录 `frontend`，`pnpm` 需 10.x：本机 `pnpm 11` 会因 lockfile overrides 不匹配失败，使用 `npx pnpm@10.24.0`）：

```
npx pnpm@10.24.0 typecheck
npx pnpm@10.24.0 check:i18n
npx pnpm@10.24.0 test:run
npx pnpm@10.24.0 build
```

### 5.2 验收清单（逐项可勾选）

- [ ] 用户可创建工单并在列表中看到；管理员列表可见并按状态筛选。
- [ ] 管理员回复后用户详情出现新回复，`notify_email=true` 时邮件事件被调用（单测断言 Send 入参），失败不影响回复落库。
- [ ] 用户不可读取/回复他人工单（404）；已关闭工单拒绝回复。
- [ ] 管理页展示内置价格且分页/搜索正确；修改一个模型后 `GET` 立即返回新价，计费单测按新价计算；恢复内置后回到目录价。
- [ ] `convert`/`direct` 切换对已登记模型产生 7.14 倍差异（kimi-k2 输入 4 → 0.5602 / 4.0 USD per MTok）。
- [ ] DeepSeek 覆盖价生效且不再被官方价强制覆盖；未覆盖模型行为不变。
- [ ] 损坏 JSON / 未配置路径时管理页给出明确提示且系统其它定价不受影响。
- [ ] 亮/暗两套主题下首页、工单页、价格页对比度与可读性通过；`pnpm build` 成功。
- [ ] 全部新增 UI 文案 zh/en 键齐全；无 `any`/`@ts-ignore` 新增。

## 6. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 价格写错导致资损 | 覆盖写入前二次校验（数值范围/JSON 合法）+ 返回生效值；"恢复内置"一键回退；审计中间件记录操作人。 |
| DeepSeek 强制价绕过管理员配置 | 2.4 的 `HasManagedPricing` 让位规则 + 专项测试。 |
| 人民币层与目录/兜底优先级混乱 | 固定优先级表（3.1）并在单测中逐层断言。 |
| 多实例部署文件不同步 | 与现有 override 文件同一约束；文档注明共享卷要求，管理页显示文件路径。 |
| 工单刷量 / XSS | 限流 + 纯文本渲染 + 长度校验 + 邮件变量转义。 |
| 全站改色导致对比度回退 | 只改令牌值、不改类名；设计任务输出对比度表；验证任务抽查关键页面。 |
| 前端依赖锁版本 | 统一使用 `npx pnpm@10.24.0`，禁止 `--no-frozen-lockfile` 改锁文件。 |
| 并行改同一文件冲突 | 写作用域表 + 共享文件只做"追加"式改动；集成阶段由 captain 复核 diff。 |