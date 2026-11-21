# 任务拆解

负责人缩写：`ui-lead`（设计/令牌）、`be-ticket`（工单后端）、`fe-ticket`（工单前端）、`be-price`（价格/口径后端）、`fe-price`（价格前端）、`fe-home`（首页）、`verifier`（验证）、`captain`（集成）。

> **状态（2026-11-15，t20 集成收口）**：§0–§8 全部完成并勾选（§2.5 按 3 条路由、§1.2 迁移测试在 internal/migrations/）；§7.4（部署）未触发，保持未勾选。验证证据：`verification.md`（t17 全量 + t20 集成复核 + §12 t19 复验）。

## 0. 设计令牌（ui-lead，无依赖）

- [x] 0.1 运行 `ui-ux-pro-max` 的 `--design-system`（查询：`AI API gateway developer platform SaaS dashboard`，参数 `--density 7 --variance 5 --motion 4`），产出方向依据；读取其 `references/quick-reference.md` 中 color-dark-mode / color-accessible-pairs 要点
- [x] 0.2 编写 `design-system/sub2api/MASTER.md`：三方向对比、默认方向 B 的完整 light/dark 令牌（颜色/表面/边框/文字/渐变/阴影/圆角/间距/字阶）、对比度实测表（正文 ≥4.5:1、按钮 ≥4.5:1）、禁用项（外部字体 CDN、emoji 图标、裸 hex 组件）
- [x] 0.3 更新 `frontend/tailwind.config.js`：`primary` 换 B 色阶、`dark` 换 ink 色阶、`accent` 保持不变、`gradient-primary`/`mesh-gradient`/`glow`/`gradient-dark` 更新、保留全部 animation/keyframes 名称
- [x] 0.4 更新 `frontend/src/style.css`：body/surface/边框改用新令牌、`.btn-*`/`.input-*`/`.card*`/`.badge-*` 焦点环与配色对齐、`::selection`、滚动条、`tabular-nums` 工具类、`prefers-reduced-motion` 兜底
- [x] 0.5 产出 `docs/design/ui-palette-preview.html`：三方向色板 + 按钮/输入/表格/卡片/暗色对比样例，纯静态无外部依赖，可直接浏览器打开
- [x] 0.6 自检：`grep` 确认无 Tailwind 配置语法错误；`npx pnpm@10.24.0 build` 失败则只修复令牌层问题并记录

**写作用域**：`design-system/**`、`docs/design/**`、`frontend/tailwind.config.js`、`frontend/src/style.css`

## 1. 工单后端（be-ticket，无依赖）

- [x] 1.1 新增 `backend/ent/schema/ticket.go`、`ticket_reply.go`（表名 `tickets`/`ticket_replies`，字段与索引见 design.md §1.1，`ON DELETE CASCADE` 由迁移与外键保证），执行 `cd backend && go generate ./ent`
- [x] 1.2 新增迁移 `backend/migrations/251_tickets.sql`：建表 + 索引 + 外键（`users(id)`、`tickets(id)`），幂等（`IF NOT EXISTS`）；迁移测试落在 `backend/internal/migrations/tickets_migration_test.go`（`backend/migrations/` 下仅保留 SQL，见 deviations D1）
- [x] 1.3 `backend/internal/domain` 增加工单状态/优先级/分类/角色常量与合法值校验
- [x] 1.4 新增 `backend/internal/repository/ticket_repo.go`：ent 实现（创建工单+首条回复同一事务、按用户分页、按管理条件分页、详情、追加回复并更新冗余字段、改状态、统计计数）
- [x] 1.5 新增 `backend/internal/service/ticket_service.go`：业务规则（状态机、归属校验、长度校验、分页边界、回复上限、关闭语义），注入 `NotificationEmailService` 发信；新增 `ticket_service_test.go` 覆盖状态机与权限边界
- [x] 1.6 新增 `backend/internal/handler/ticket_handler.go`（用户）与 `backend/internal/handler/admin/ticket_handler.go`（管理员），DTO 放 `handler/dto/tickets.go`；错误码按 design.md
- [x] 1.7 通知邮件：`notification_email_service.go` 新增 `ticket.reply` 事件、zh/en 模板、变量校验；补 `notification_email_service_test.go` 用例
- [x] 1.8 路由：`backend/internal/server/routes/user.go` 注册 `/tickets`；`routes/admin.go` 注册 `/admin/tickets`；`handler/handler.go` 增加 handler 字段；`cmd/server/wire_gen.go` 如为手工聚合则同步（优先复用既有构造函数与依赖）
- [x] 1.9 handler 测试 `backend/internal/handler/ticket_handler_test.go`、`backend/internal/handler/admin/ticket_handler_test.go`：权限、404 隔离、状态非法、回复关闭工单、发信失败不影响落库
- [x] 1.10 本地验证：`go build ./...`、`go test ./internal/service/... -run Ticket -count=1`、`go test ./internal/handler/... -run Ticket -count=1`

**写作用域**：`backend/ent/schema/ticket*.go`、`backend/ent/**`（生成产物）、`backend/migrations/25*_tickets*`、`backend/internal/{domain,repository,service,handler}/ticket*`、`backend/internal/handler/dto/tickets.go`、`backend/internal/service/notification_email_service*.go`、`backend/internal/server/routes/{user,admin}.go`（仅新增工单注册行）、`backend/internal/handler/handler.go`（仅新增字段）

## 2. 工单前端（fe-ticket，依赖 1）

- [x] 2.1 `frontend/src/api/tickets.ts` + `api/admin/tickets.ts`（类型完整、无 any）
- [x] 2.2 组件 `frontend/src/components/tickets/`：`TicketStatusBadge.vue`、`TicketThread.vue`、`TicketComposer.vue`、`CreateTicketModal.vue`
- [x] 2.3 用户页 `views/user/TicketsView.vue`（`/tickets`，列表/筛选/新建/空态/分页）与 `views/user/TicketDetailView.vue`（`/tickets/:id`，对话流/回复/关闭）
- [x] 2.4 管理员页 `views/admin/TicketsView.vue`（`/admin/tickets`，筛选/搜索/表格/详情弹窗/回复含"邮件通知"勾选/状态与优先级编辑）
- [x] 2.5 路由 `router/index.ts` 新增 3 条路由（用户 `/tickets`、`/tickets/:id`；管理员 `/admin/tickets`，详情用弹窗；见 deviations D9）与 meta；`components/layout/AppSidebar.vue` 新增用户与管理员工单入口（管理端显示 open 数角标，可懒加载）
- [x] 2.6 i18n：`locales/zh|en/tickets.ts`、`locales/zh|en/admin/tickets.ts` 及 index 注册、`nav.tickets`/`nav.ticketManagement`、分享/日期格式
- [x] 2.7 测试：`views/user/__tests__/TicketsView.spec.ts`、`views/admin/__tests__/TicketsView.spec.ts`（mock api：列表渲染、新建校验、回复交互、状态筛选、邮件勾选透传）
- [x] 2.8 自检：`npx pnpm@10.24.0 typecheck && npx pnpm@10.24.0 check:i18n && npx pnpm@10.24.0 test:run`

**写作用域**：`frontend/src/views/{user,admin}/Ticket*`、`frontend/src/components/tickets/**`、`frontend/src/api/{tickets.ts,admin/tickets.ts}`、`frontend/src/router/index.ts`（仅新增路由）、`frontend/src/components/layout/AppSidebar.vue`（仅新增条目）、`frontend/src/i18n/**/tickets.ts`、`frontend/src/i18n/**/index.ts`

## 3. 模型价格与人民币口径后端（be-price，无依赖）

- [x] 3.1 `pricing_service.go`：新增 `Snapshot()`（深拷贝目录+覆盖+人民币层，含元数据）、导出 `ReloadCustomPricingLayers()`、`HasManagedPricing(model)`、人民币文件路径推导与加载/校验
- [x] 3.2 人民币层：`applyPricingOverrides` 前叠加 `cny-overrides.json` 生成的原始字段（`input_cost_per_token` 等 = CNY/1e6/rate），支持目录外模型；USD override 最后叠加；`mode=direct` 时 rate=1
- [x] 3.3 新增 `backend/internal/service/model_pricing_admin_service.go`：列表/搜索/分页、逐模型 override 增删（原子写 + 校验 + 热重载）、人民币配置读写、种子导入数据、文件路径与元信息
- [x] 3.4 `billing_service.go`：`applyModelSpecificPricingPolicyEx` 的调用点按 `HasManagedPricing` 让位（DeepSeek 官方价不覆盖管理员配置），保持未覆盖模型行为不变
- [x] 3.5 `config.Config.Pricing` 新增 `CNYOverrideFile`（mapstructure `cny_override_file`），默认推导；`deploy/config.example.yaml` 与 `deploy/.env.example` 补注释示例
- [x] 3.6 管理端 handler `handler/admin/model_pricing_handler.go` + DTO + 路由 `routes/admin.go`（`/admin/model-pricing` 系列 6 个端点）；输入校验与错误码见 design.md §2.3/§3.3
- [x] 3.7 测试：
  - `pricing_service_cny_test.go`：convert/direct 换算、优先级、损坏文件降级、目录外模型、热重载
  - `model_pricing_admin_service_test.go`：override 增删/原子写/恢复/非法值
  - `billing_cny_mode_test.go`：1M token 成本断言（0.5602 / 4.0）、DeepSeek 让位、非登记模型不受影响
  - handler 测试：分页/搜索/校验错误码
- [x] 3.8 本地验证：`go build ./...`、`go test ./internal/service/... -run 'ModelPricing|Currency|CNY|PricingOverride|DeepSeek' -count=1`、`go test ./internal/handler/admin/... -run ModelPricing -count=1`

**写作用域**：`backend/internal/service/{pricing_service*,model_pricing_admin_service*,billing_service*}.go`、`backend/internal/config/config.go`（仅新增字段）、`backend/internal/handler/admin/model_pricing*`、`backend/internal/handler/dto/model_pricing*`、`backend/internal/server/routes/admin.go`（仅新增价格注册行）、`backend/deploy*/config.example.yaml`、`deploy/.env.example`

## 4. 模型价格前端（fe-price，依赖 3）

- [x] 4.1 `frontend/src/api/admin/modelPricing.ts`（类型与端点对齐 design.md §2.3/§3.3）
- [x] 4.2 `views/admin/ModelPricingView.vue`：Tab1 内置价格表格（搜索/来源筛选/分页/覆盖标记）、Tab2 人民币口径（模式、汇率、模型表、导入种子、对比预览）
- [x] 4.3 组件 `components/admin/pricing/`：`ModelPriceTable.vue`、`ModelPriceEditDialog.vue`（USD 输入 + 人民币等值实时换算 + 字段级"恢复内置"）、`CurrencyModeCard.vue`、`CnyPriceTable.vue`
- [x] 4.4 路由与侧边栏"模型价格"入口；`override_file` 未配置时只读提示
- [x] 4.5 i18n zh/en + index 注册；数字格式（USD 4 位小数、CNY 2 位小数、`tabular-nums`）
- [x] 4.6 测试：`views/admin/__tests__/ModelPricingView.spec.ts`（列表渲染、编辑提交 payload、null 字段、口径切换、无 override 文件只读）
- [x] 4.7 自检：typecheck + check:i18n + test:run

**写作用域**：`frontend/src/views/admin/ModelPricingView.vue`、`frontend/src/components/admin/pricing/**`、`frontend/src/api/admin/modelPricing.ts`、`frontend/src/router/index.ts`（仅新增路由）、`frontend/src/components/layout/AppSidebar.vue`（仅新增条目）、`frontend/src/i18n/**/admin/modelPricing.ts` 与 index

## 5. 首页重做（fe-home，依赖 0）

- [x] 5.1 `views/HomeView.vue` 默认分支重写：导航 / Hero + 代码示例（含复制）/ 能力矩阵 6 卡 / 模型矩阵 / 计费 CTA / 页脚；保留 `hasHomeContent`、`compactHomeEnabled` 分支逻辑与测试约定
- [x] 5.2 复用公开设置：`siteName/siteLogo/siteSubtitle/docUrl/apiBaseUrl/contactInfo/showModelPlazaEntry/支付与订阅开关`，未登录/已登录 CTA 行为与现状一致
- [x] 5.3 i18n 新增 `home.*` 键（zh/en），删除未使用键需同步移除避免 check:i18n 失败
- [x] 5.4 响应式与可达性：375/768/1024/1440；键盘焦点；`aria-label`；`prefers-reduced-motion`；对比度符合 0.2 的令牌
- [x] 5.5 测试：`views/__tests__/HomeView.spec.ts` 覆盖三模式分支、CTA 目标、模型墙渲染、无后端新增依赖
- [x] 5.6 自检：typecheck + check:i18n + test:run + build

**写作用域**：`frontend/src/views/HomeView.vue`、`frontend/src/views/__tests__/Home*`、`frontend/src/i18n/**/home.ts`

## 6. 全量验证（verifier，依赖 1–5）

- [x] 6.1 后端：`go build ./...`、`go vet ./...`、`go test ./internal/service/... ./internal/handler/... ./internal/server/... ./internal/migrations/... -count=1`，记录失败项与归属
- [x] 6.2 前端：`npx pnpm@10.24.0 typecheck`、`check:i18n`、`test:run`、`build`（pnpm 必须 10.24.0）
- [x] 6.3 逐条对照 design.md §5.2 验收清单，产出 `openspec/changes/fork-tickets-pricing-ui/verification.md`（命令、输出摘要、结论、未覆盖项）
- [x] 6.4 仅修复"验证发现的最小阻断"（编译/类型/i18n 键缺失），不扩大改动；功能缺陷登记为发现项并指派回原负责人
- [x] 6.5 结论写入验证报告，失败项必须带可复现命令

**写作用域**：`openspec/changes/fork-tickets-pricing-ui/verification.md`、验证报告；修复仅限编译/类型/测试夹具层

## 7. 集成（captain，依赖 6）

- [x] 7.1 复核最终 `git diff`：无调试代码、无密钥、无锁文件改动、无生成物异常；命名与注释一致
- [x] 7.2 抽查计费数值：convert/direct 两种模式的实际成本断言；确认无隐性放大（倍率、峰谷、优先级层叠加）
- [x] 7.3 更新 `openspec/changes/fork-tickets-pricing-ui/tasks.md` 勾选状态与 `verification.md` 结论；输出交付摘要
- [ ] 7.4 若用户要求部署，另行确认（默认不部署、不重启用户环境）｜**未触发**：用户未要求部署，本次交付不部署

## 8. V1 修复：工单用户写操作限流（t19，t17 发现项）

- [x] 8.1 新增 `backend/internal/handler/ticket_rate_limit.go`：按 user_id 的 60 秒 5 次滑动窗口（mutex、惰性清理、拒绝不写时间戳、时钟可注入、nil 安全）
- [x] 8.2 `ticket_handler.go` 的 `Create` / `AddReply` 接入 `allowTicketWrite`，超限 `429 RATE_LIMITED` + `Retry-After`；用户读接口与管理端不受影响
- [x] 8.3 `ticket_handler_test.go` 新增 4 例：5 次放行/第 6 次拒绝、窗口滑动、用户隔离与惰性清理、HTTP 429（含读接口不受影响与窗口恢复）、并发安全
- [x] 8.4 验证：`go -C backend build ./...`、`go -C backend test ./internal/handler/ -run Ticket -count=1`、`go -C backend test -race ./internal/handler/ -run TicketRate -count=1` 全部通过

**写作用域**：`backend/internal/handler/ticket_handler.go`、`backend/internal/handler/ticket_handler_test.go`、`backend/internal/handler/ticket_rate_limit.go`

**验证证据**：`verification.md §12`（4 例 PASS + `-race` PASS）；V1 已关闭。

## 执行波次

```
Wave 1（并行）：0 设计令牌 · 1 工单后端 · 3 价格后端
Wave 2（并行）：2 工单前端（←1） · 4 价格前端（←3） · 5 首页（←0）
Wave 3：6 全量验证（←1,2,3,4,5）
Wave 4：7 集成（←6）
```

## 共享文件冲突约定

- `handler/handler.go`、`routes/admin.go`、`router/index.ts`、`AppSidebar.vue`、`i18n/*/index.ts`：只允许**追加**自己的区块，不改他人行；改完立即 `typecheck`/`go build` 保证不破坏他人。
- 若同一文件出现无法避免的交叉修改，由 captain 在集成阶段统一收口。
