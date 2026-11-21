# 全量验证报告 · fork-tickets-pricing-ui

- **验证任务**：t17 全量验证（依赖 t9 / t10 / t11 / t12 / t13 / t16）；**t20 集成收口补记**（依赖 t19，见 §12–§14）
- **验证人**：verifier2
- **验证时间**：2026-11-15 11:00–11:14（t17）；2026-11-15 14:08–14:35（t20 补记，Asia/Shanghai）
- **代码基线**：`main` @ `530c5e3a4`（上游提交）＋未提交工作树（t17 时 36 modified + 53 untracked；t20 时 36 modified + 54 untracked，新增 `ticket_rate_limit.go`）
- **工具链**：`go1.26.4 windows/amd64`；`Node v22.22.2`；`npx pnpm@10.24.0`；`-race` 需 MinGW-w64 gcc（见 §12）
- **验证方式**：只读验证（跑命令、读代码、独立复算），**未修改任何功能实现**；t17 期间唯一写入是前端 `build` 输出到 gitignored 的 `backend/internal/web/dist/`；t20 期间额外执行了代码生成（`go generate ./ent`、`./cmd/server`）且生成物无 churn（见 §13）。

## 结论

> **通过（PASS，最终态）** — 6 条契约命令全部 `exit 0`；`design.md §5.2` 九项验收全部通过（其中 2 项受环境限制，以「契约/单测级 + 令牌级复算」佐证）；`deviations.md **D1–D9**` 九项与实现逐项一致，全部按已接受偏差判定。
>
> t17 登记的 3 项低危发现已闭环：**V1 已由 t19 修复**（用户侧工单写操作 60 秒 5 次限流 + 429，含 `-race` 复验），**V2/V3 已转登记为已接受偏差 D7/D8**（O1 → D9）。仅剩 **1 项基线既有失败**（`internal/repository`，Windows 缺 `sh`，与本次变更无关）。**可交付；按约定不部署、不重启服务。**

---

## 1. 命令与输出摘要

### 1.1 后端

| # | 命令 | 退出码 | 输出摘要 |
|---|---|---|---|
| B1 | `go -C backend build ./...` | **0** | 无输出（全部包编译通过） |
| B2 | `go -C backend vet ./...`（附加，非契约但 tasks.md 6.1 要求） | **0** | 无输出 |
| B3 | `go -C backend test ./internal/service/... ./internal/handler/... ./internal/server/... ./internal/migrations/... ./migrations/... -count=1` | **0** | 10 个包全 `ok`（见下表） |
| B4 | `go -C backend test ./internal/repository/... -count=1`（**基线对照，单列**） | **1** | `FAIL github.com/Wei-Shaw/sub2api/internal/repository 11.062s`，3 个 `backup_pg_dumper` 用例失败（Windows 缺 `sh`），与本次变更无关 → 见 §2.4 |
| B5 | `go -C backend test ./internal/service/... -run 'Ticket\|ModelPricing\|Currency\|CNY\|PricingOverride\|DeepSeek\|NotificationEmail' -count=1 -v`（design §5.1 定向） | **0** | **97 top-level PASS / 0 FAIL** |
| B6 | `go -C backend test ./internal/handler/... -run 'Ticket\|ModelPricing' -count=1 -v`（design §5.1 定向） | **0** | **16 top-level PASS / 0 FAIL** |

B3 逐包结果：

```
ok  github.com/Wei-Shaw/sub2api/internal/service            169.848s
ok  github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2 4.443s
ok  github.com/Wei-Shaw/sub2api/internal/handler            46.381s
ok  github.com/Wei-Shaw/sub2api/internal/handler/admin       1.360s
ok  github.com/Wei-Shaw/sub2api/internal/handler/dto         0.584s
ok  github.com/Wei-Shaw/sub2api/internal/handler/quotaview   0.660s
?   github.com/Wei-Shaw/sub2api/internal/server             [no test files]
ok  github.com/Wei-Shaw/sub2api/internal/server/middleware   0.796s
ok  github.com/Wei-Shaw/sub2api/internal/server/routes      13.350s
ok  github.com/Wei-Shaw/sub2api/internal/migrations          1.118s
ok  github.com/Wei-Shaw/sub2api/migrations                   1.081s
```

### 1.2 前端（必须 `pnpm@10.24.0`；本机 `pnpm 11` 与 lockfile overrides 不兼容）

| # | 命令 | 退出码 | 输出摘要 |
|---|---|---|---|
| F1 | `npx --yes pnpm@10.24.0 -C frontend typecheck`（`vue-tsc --noEmit`） | **0** | 无类型错误 |
| F2 | `npx --yes pnpm@10.24.0 -C frontend check:i18n`（`vitest run src/i18n/__tests__/localeKeyCompleteness.spec.ts`） | **0** | `Test Files 1 passed`，`Tests 3 passed` |
| F3 | `npx --yes pnpm@10.24.0 -C frontend test:run`（`vitest run`） | **0** | `Test Files 388 passed (388)`，`Tests 3171 passed (3171)`，174.25s |
| F4 | `npx --yes pnpm@10.24.0 -C frontend build`（`check:i18n && vue-tsc -b && vite build`） | **0** | `✓ built in 42.28s`（仅 chunk >500 kB 的常规提示，无错误） |

F3 中与本变更直接相关的新增 spec：

```
✓ src/views/__tests__/HomeView.spec.ts              (31 tests)
✓ src/views/__tests__/HomeView.compact.spec.ts      (13 tests)
✓ src/views/user/__tests__/TicketsView.spec.ts      (12 tests)
✓ src/views/admin/__tests__/TicketsView.spec.ts     ( 7 tests)
✓ src/views/admin/__tests__/ModelPricingView.spec.ts(11 tests)
✓ src/i18n/__tests__/localeKeyCompleteness.spec.ts  ( 3 tests)
```

> F4 的 build 脚本本身包含 `check:i18n` 与 `vue-tsc -b`，因此 build 通过同时再次证明类型与 i18n 完整。

---

## 2. 后端验证明细

### 2.1 重点特性测试覆盖（design §5.1 过滤词）

| 主题 | 测试文件 / 用例数 | 关键断言 |
|---|---|---|
| **Ticket** 服务 | `backend/internal/service/ticket_service_test.go`：15 个 top-level 用例 | 创建即 open + 首条回复同事务；越权 404；关闭后回复 409；回复上限 500；回复内容 TrimSpace/长度；状态/优先级/分类非法值 |
| **Ticket** 用户端 handler | `backend/internal/handler/ticket_handler_test.go`：7 用例 | 未认证 401；列表/创建/详情；**他人工单 detail/replies/close 均 404 `TICKET_NOT_FOUND`**；关闭工单回复 409；非法 status 400；非法 id |
| **Ticket** 管理端 handler | `backend/internal/handler/admin/ticket_handler_test.go`：6 用例 | `notify_email` 默认 true；发信失败不丢回复；状态/优先级校验；详情含 user；列表+stats |
| **Ticket** 迁移 | `backend/internal/migrations/tickets_migration_test.go`：1 用例 | 251 SQL 建表/字段/索引/`ON DELETE CASCADE`（`dbmigrations.FS.ReadFile` 只读断言） |
| **NotificationEmail** | `notification_email_service_test.go`：`ticket.reply` 相关 4 用例 | 事件在 `ListEventInfos` 出现且 `Optional=false`、zh/en 可预览、回复正文转义、`ticket_url` 生成、发送失败返回 `notificationEmailDeliveryError` |
| **ModelPricing** 管理服务 | `model_pricing_admin_service_test.go`：9 用例 | 排序 `overridden desc, model asc`；`page_size` 上限 100；搜索大小写不敏感/可搜供应商；覆盖原子写 + 热重载；`null` 字段回退人民币层；删除覆盖回目录价；非法值 400 `INVALID_MODEL_PRICE`；未配置 override_file 时 `editable=false` |
| **ModelPricing** 管理 handler | `model_pricing_handler_test.go`：3 用例 | 列表/覆盖/恢复流程；校验错误码；人民币口径读写 |
| **CNY / Currency** | `pricing_service_cny_test.go`：10 用例（9 个 `TestCNY*` + `TestPricingSnapshotDeepCopyAndManagedModels`） | convert=÷7.14、direct=1:1；优先级（USD override > CNY > 目录）；损坏 JSON 降级；目录外模型建条；热重载；文件路径三级推导；Snapshot 深拷贝；`0` 表示不登记 |
| **CNY** 计费一致性 | `billing_cny_mode_test.go`：3 用例 | `kimi-k2` 1M 输入 = `4/7.14`（≈**0.5602**）USD / direct = **4.0** USD，误差 `<1e-9`；`gpt-5.4` 不随口径变化；DeepSeek 官方价让位、峰谷倍率让位 |
| **DeepSeek / PricingOverride** | 含在上述 `billing_cny_mode_test.go` 与 `pricing_service_cny_test.go` 中 | 未覆盖时官方价 `$0.66/MTok` + 峰谷 ×2；被 override 后按覆盖价；被 CNY 登记后按登记价 |

### 2.2 design §5.1 定向命令结果

- service 定向 **97 PASS / 0 FAIL**，其中：`TestTicket*` 15、`TestModelPricing*` 9、`TestCNY*` 9、`TestBillingCNY*` 1、`TestBillingDeepSeek*` 2、`TestNotificationEmailTicketReply*` 4、其余匹配到的既有 NotificationEmail/DeepSeek 用例 57。
- handler 定向 **16 PASS / 0 FAIL**：`TestTicketHandler*` 7、`TestAdminTicketHandler*` 6、`TestModelPricingHandler*` 3。

### 2.3 路由注册核对

| 侧 | 位置 | 端点 |
|---|---|---|
| 用户 | `backend/internal/server/routes/user.go:166-173`（`authenticated` 组内，继承 JWT + BackendModeUserGuard + `panelRateLimiter.Global()` + 审计） | `GET/POST /tickets`、`GET /tickets/:id`、`POST /tickets/:id/replies`、`POST /tickets/:id/close` |
| 管理员 | `routes/admin.go:919-927`（`adminAuth` 组） | `GET /admin/tickets`、`GET /admin/tickets/stats`、`GET/POST/PUT /admin/tickets/:id[/replies]` |
| 管理员价格 | `routes/admin.go:930-940`（`adminAuth` 组） | `GET /admin/model-pricing`；`PUT`、`DELETE /admin/model-pricing/overrides/:model`；`GET`、`PUT /admin/model-pricing/currency`；`PUT`、`DELETE /admin/model-pricing/currency/models/:model` |

> `/stats` 先于 `/:id` 注册，避免被参数路由吞掉（`admin.go:917-927` 注释与顺序）。

### 2.4 基线既有失败（单列，非本次回归）

```
--- FAIL: TestPgDumperHoldsMigrationLockThroughReaderClose
    backup_pg_dumper_test.go:61: start pg_dump: exec: "sh": executable file not found in %PATH%
--- FAIL: TestPgDumperReleasesMigrationLockWhenProcessFails
--- FAIL: TestPgDumperReportsUnlockFailureAndDiscardsConnection
FAIL github.com/Wei-Shaw/sub2api/internal/repository 11.062s
```

- **性质**：仓库基线问题 —— 本机 Windows 环境无 `sh`，`pg_dump` 调用链无法启动；同时测试 mock 依赖真实 DB 行为。
- **与本次变更的关系**：`git status` 中 `internal/repository/` 仅新增未跟踪的 `ticket_repo.go` 与修改 `wire.go`；失败的 `backup_pg_dumper_test.go` 未被本次变更触碰，且 `ticket_repo.go` 无对应单测（仓储层靠 service fake 覆盖）。**本次未使该包从绿变红**。
- **处置**：按 tasks.md 与 deviations.md D5，该包在无 DB 环境下的 FAIL 属既有基线，不计入本次验收；合并后如需回归仓储层，应在带 Postgres + `sh` 的环境执行。

---

## 3. 前端验证明细

| 项 | 结论 | 证据 |
|---|---|---|
| 类型安全 | ✅ | `vue-tsc --noEmit` exit 0；全部新增/修改的前端源文件（api 3、视图 4、组件 9、i18n 6，另含修改的 `HomeView.vue` 与 5 个 spec）中 `any` / `@ts-ignore` / `@ts-expect-error` **0 命中**（grep + `git diff` 新增行 1093 行扫描） |
| 新增文件 `v-html` | ✅ | 新增文件 0 命中；`HomeView.vue:12` 的 `v-html` 属既有 `hasHomeContent` 管理员自定义内容分支（design 允许） |
| i18n 完整性 | ✅ | `check:i18n` 3 passed；新增/修改的 zh/en 键行数一致（tickets 74/74、admin tickets 53/53、admin modelPricing 142/142、landing 212/212） |
| 工单前端 | ✅ | 用户 12 + 管理 7 = 19 用例：列表/空态/错误态/新建校验/详情回复/关闭/邮件勾选透传/状态筛选/保存状态 |
| 价格前端 | ✅ | 11 用例：列表渲染/分页搜索参数透传/编辑 payload/字段级 `null`/恢复内置/只读提示/口径切换/人民币表增删/种子导入 |
| 首页 | ✅ | 31 + 13 = 44 用例：三模式分支（自定义 HTML / iframe / 空白降级 / compact / 默认落地页）、CTA 目标随登录态与支付/订阅/余额开关、复制按钮 + aria-live、模型广场入口、页脚版本与联系信息、无伪造统计数字、reduced-motion、焦点环、暗色写 localStorage |

---

## 4. `design.md §5.2` 验收矩阵（逐条）

| # | 验收项 | 结论 | 证据 | 说明 |
|---|---|---|---|---|
| 1 | 用户可创建工单并在列表中看到；管理员列表可见并按状态筛选 | ✅ **通过** | `TestTicketCreateStartsOpenWithFirstReply`、`TestTicketHandlerCreateListAndGet`、`TestTicketAdminListValidatesFiltersAndClampsPagination`、`TestAdminTicketHandlerListAndStats`、用户/管理 `TicketsView.spec.ts` | 单测 + handler 契约级；未连真实 DB（见 §7） |
| 2 | 管理员回复后用户详情出现新回复；`notify_email=true` 时邮件事件被调用（断言 Send 入参），失败不影响回复落库 | ✅ **通过** | `TestTicketAuthorNamesUseSiteNameForAdminReplies`（用户侧 `GetUserTicket` 末条回复为管理员回复）、`TestTicketAdminReplySendsEmailAndDefaultsPending`（断言 Event/UserID/RecipientEmail/`ticket_subject`/`reply_content`/`ticket_id`，`notify_email=false` 不发信）、`TestTicketAdminReplyEmailFailureDoesNotRollbackReply`（`repo.replies` 仍为 2 条）、`TestAdminTicketHandlerReplyEmailFailureDoesNotDropReply` | 发信为 mock notifier，未接真实 SMTP（见 §8） |
| 3 | 用户不可读取/回复他人工单（404）；已关闭工单拒绝回复 | ✅ **通过** | `TestTicketOwnershipIsolationAndClosedDetail`、`TestTicketHandlerIsolatesOtherUsersTickets`（detail/replies/close 三端点均断言 404 `TICKET_NOT_FOUND`）、`TestTicketUserReplyReopensAndRejectsClosed`、`TestTicketHandlerClosedTicketReplyReturns409`（409 `TICKET_CLOSED`）、`TestAdminTicketHandlerReplyValidation` | — |
| 4 | 管理页展示内置价格且分页/搜索正确；修改后 `GET` 立即返回新价，计费单测按新价计算；恢复内置后回到目录价 | ✅ **通过** | `TestModelPricingAdminListSortingPaginationAndFilters`（6 条目录、排序、`page_size=500→100`、`Search:"KIMI"` 大小写不敏感、供应商筛选）、`TestModelPricingAdminUpdateOverrideAtomicWriteAndHotReload`、`TestModelPricingAdminDeleteOverrideRestoresBuiltin`、`TestModelPricingHandlerListAndOverrideFlow`、`ModelPricingView.spec.ts` 11 例 | 写盘为临时文件 + `os.Rename` 原子替换 + 回读 JSON 校验 + `ReloadCustomPricingLayers()` |
| 5 | `convert`/`direct` 切换对已登记模型产生 7.14 倍差异（kimi-k2 输入 4 → 0.5602 / 4.0 USD per MTok） | ✅ **通过** | `TestCNYPricingConvertAndDirectRates`（`4/7.14`、`16/7.14`、`1/7.14`，误差 `1e-9`）、`TestBillingCNYModeOneMillionInputCost`（`CalculateCost("kimi-k2", 1M input)` = `4.0/7.14` = **0.560224…**；direct = **4.0**）、`TestCNYPricingUnregisteredModelsUnchanged`（`gpt-5.4` 恒 2.5） | 数值独立复核：`4/7.14 = 0.5602240896358543`，四舍五入 0.5602 ✅ |
| 6 | DeepSeek 覆盖价生效且不再被官方价强制覆盖；未覆盖模型行为不变 | ✅ **通过** | `TestBillingDeepSeekOfficialPriceYieldsToManagedPricing`（未接管 $0.66/MTok + 峰谷 ×2 = 1.32；override 后 = 5.0；CNY 登记 7.14 后 = 1.0）、`TestBillingDeepSeekPeakMultiplierSkipsManagedPricing`、`TestCNYPricingUSDPriorityAndNullFallback` | `HasManagedPricing` 让位规则在 `billing_service.go:1467/1688` 落地 |
| 7 | 损坏 JSON / 未配置路径时管理页给出明确提示且系统其它定价不受影响 | ✅ **通过** | `TestCNYPricingCorruptFileDegrades`（损坏文件仅告警，其它模型价不变）、`TestModelPricingAdminOverrideFileNotConfigured`（`editable=false` / `cny_editable=false`）、`ModelPricingView.spec.ts` 断言 `installed.readonlyHint` 与 `currency.readonlyHint` | 前端只读态 + 提示条均有测试覆盖 |
| 8 | 亮/暗两套主题下首页、工单页、价格页对比度与可读性通过；`pnpm build` 成功 | ✅ **通过（令牌级复算 + 构建）** | ① `npx pnpm@10.24.0 -C frontend build` exit 0；② 按 MASTER.md 附录 A 公式独立复算 18 组关键配对：白字/`primary-600` 6.29、白字/`primary-700` 7.90、`accent-900`/canvas 16.68、`dark-100`/`dark-950` 15.86、`dark-300`/`dark-800` 6.92、`dark-400`/`dark-800` 4.77、`primary-400`/`dark-950` 6.60、白字/`emerald-700` 5.48、白字/`red-600` 4.83、`dark-900`/`amber-400` 11.28 —— **全部与文档一致且达 AA**；③ 新增页面/组件 `dark:` 变体覆盖：用户工单页 14（列表 7 + 详情 7）、工单组件 12、管理工单 24、价格页 27、价格组件 66；灰度类实测 `gray-500 on white 4.83:1`、`gray-500 on gray-50 4.63:1`、`gray-600 on white 7.56:1`、`gray-300 on dark-950 13.36:1` | **未做浏览器截图与像素级目视走查**（见 §8）；对比度以令牌复算 + 代码扫描判定 |
| 9 | 全部新增 UI 文案 zh/en 键齐全；无 `any`/`@ts-ignore` 新增 | ✅ **通过** | `check:i18n` 3 passed（zh/en 逐键对齐）；全部新增/修改前端源文件 grep + 1093 行 diff 新增行扫描：`any`/`@ts-ignore`/`@ts-expect-error`/`v-html` **0 新增** | — |

**合计：9/9 通过**（其中 #1/#2 为契约级、#8 为令牌级，均已在 §8「未覆盖项」标明边界）。

---

## 5. `deviations.md` D1–D9 判定

| 项 | 偏差描述 | 实现核对 | 判定 |
|---|---|---|---|
| **D1** | 迁移测试合并到 `internal/migrations/tickets_migration_test.go`，删除 `backend/migrations/` 下的重复测试 | 存在 `backend/internal/migrations/tickets_migration_test.go`（46 行），`backend/migrations/` 下仅有 `250_*.sql`、`251_tickets.sql` 无 `_test.go`；`go test ./internal/migrations/... ./migrations/...` 均 `ok` | ✅ 按已接受偏差 |
| **D2** | `Ticket` 与 `TicketReply` 两个 ent schema 同处 `backend/ent/schema/ticket.go` | 文件存在；生成物 `backend/ent/ticket*.go`、`ticketreply*.go` 齐备，`go build ./...` 通过 | ✅ 按已接受偏差 |
| **D3** | 邮件收件人仅判空、不校验 `email_verified`（模型无该字段） | `TestTicketAdminReplySkipsEmailWhenRecipientUnavailable` 存在且通过；`ticket_service.go` 发信前仅判邮箱为空 | ✅ 按已接受偏差 |
| **D4** | 单工单回复上限 500，超限 `400 TICKET_REPLY_LIMIT_REACHED` | `domain.TicketMaxRepliesPerTicket` 校验在 service 两处（:263/:374）；错误码 `TICKET_REPLY_LIMIT_REACHED`（:38）；`TestTicketReplyLimitIsEnforced` 通过 | ✅ 按已接受偏差 |
| **D5** | 无真实 Postgres / SMTP 集成测试 | 本次验证同样未接入真实 DB/SMTP：`internal/repository` 基线 FAIL（§13.4）、邮件发信为 mock notifier | ✅ 按已接受偏差（已知限制见 §14.7） |
| **D6** | 设计令牌 3 处对比度偏离（`dark-400` 提亮、焦点环不透明、语义实心按钮压深） | `tailwind.config.js:23` `dark-400=#7A88A6`；`MASTER.md §4.1` 焦点环不透明 `primary-500`/暗色 `primary-400`；按钮改 `emerald-700`/`red-600`/深字浅底；独立复算 `dark-400`/`dark-800` = **4.77**（≥4.5）、`primary-500`/白 = **4.47**（≥3）、白字/`emerald-700` = **5.48**、白字/`red-600` = **4.83** | ✅ 按已接受偏差（以 MASTER.md 新值判定） |
| **D7** | 新页面混用 `gray-*` 中性色（原 V2） | 工单页/价格页部分中性文字与边框仍用 Tailwind 默认灰阶；对比度实测 `gray-500`/白 4.83、`gray-600`/白 7.56、`gray-300`/`dark-950` 13.36，均达标 | ✅ 按已接受偏差（风格不一致，非可访问性缺陷） |
| **D8** | `MASTER.md` 对比度数值小幅漂移（原 V3） | 独立复算 18 组：16 组一致，2 组差异（`dark-800` vs `dark-950` 实测 1.16 vs 文档 1.35；`accent-600` on white 实测 7.58 vs 文档 7.56），均非需达标的正文/控件配对 | ✅ 按已接受偏差（文档精度，不影响 AA） |
| **D9** | 工单路由数量（原 O1） | `tasks.md §2.5` 写 4 条，实际 3 条（用户 `/tickets`、`/tickets/:id`；管理员 `/admin/tickets`），管理详情用弹窗承载，符合 `design.md §1.6` | ✅ 按已接受偏差（tasks.md 已按 3 条更正） |

> 未发现「实现与偏差描述不符」的情况；D1–D9 全部按已接受偏差判定，无新增登记项。

---

## 6. 发现项（Findings）

> t17 登记 3 项**低危**发现；**t20 时状态**：V1 已由 t19 修复并复验（见 §12），V2/V3 已转登记为已接受偏差 D7/D8（见 §5）。编号 `V*` 保留以追溯。

### V1 · 工单防刷未完全落实 design §1.3 的兜底要求 —— **已修复（t19）**

- **id**：V1　**severity**：low（已关闭）
- **problem**：`design.md §1.3` 要求用户创建工单/追加回复挂 `panelRateLimiter`；若现有 limiter 不支持自定义额度，则「至少挂上默认限流**并**在 handler 内做 1 分钟 5 次的轻量计数（内存 token bucket）」。t17 实测：`routes/user.go:166-173` 的 `tickets` 组位于 `authenticated` 组内，确实继承了 `panelRateLimiter.Global()`（默认 `UserRPM=240`），但**无工单专用 limiter**，`handler/ticket_handler.go`、`service/ticket_service.go` 内对 `tokenBucket|rateLimit|limiter` grep **0 命中**。
- **影响（修复前）**：单账号默认配置下每分钟最多 240 次建单/回复，刷量风险高于设计要求。
- **修复**：t19 新增 `backend/internal/handler/ticket_rate_limit.go`（进程内滑动窗口，`limit=5` / `window=60s`，仅记录放行时间戳、拒绝不顺延窗口、惰性清理、时钟可注入），`TicketHandler.Create` 与 `AddReply` 在鉴权后、绑定前调用 `allowTicketWrite`；超限返回 `HTTP 429` + `RATE_LIMITED` + `Retry-After`。管理端与用户读接口不受影响。
- **复验**：见 §12（4 个用例含 429/窗口滑动/用户隔离/并发，`-race` 亦通过）。**状态：已关闭。**

### V2 · 新页面灰度类名未按 MASTER.md §8 迁移指引改用令牌 —— **已转登记 D7（已接受偏差）**

- **id**：V2　**severity**：low
- **problem**：新增工单页/价格页及组件仍使用默认 `gray-*` 调色板（用户工单 9 处、管理工单 23 处、价格页 14 处、price 组件若干），暗色侧配合 `dark:*`。`MASTER.md §8 迁移指引` 要求 `text-gray-* → text-accent-*`、`dark:text-gray-* → dark:text-dark-*`。
- **影响**：对比度实测均达标（`gray-500`/白 4.83:1、`gray-500`/`gray-50` 4.63:1、`gray-600`/白 7.56:1、`gray-300`/`dark-950` 13.36:1），**不影响可读性与验收**；但令牌语义不一致，未来切换方向 A/C 时这些页面不会跟随令牌替换。
- **requiredFix**（可选清理）：将 `gray-*` 批量替换为 `accent-*`（暗色已是 `dark-*`）；或明确接受并在 `deviations.md` 登记。
- **建议归属**：fe-ticket / fe-price。**状态：已转 D7 接受，不再要求修复。**

### V3 · `MASTER.md` 两处对比度数值与独立复算不符（文档） —— **已转登记 D8（已接受偏差）**

- **id**：V3　**severity**：low
- **problem**：按附录 A 公式复算 18 组关键配对，16 组完全一致、2 组有偏差：
  1. `§5.2` 第 20 行「卡片 `dark-800` vs canvas `dark-950`（表面差）**1.35:1**」→ 实测 **1.16:1**（`dark-700` vs `dark-950` 才是 1.33:1，`§6 E2` 引用正确）。
  2. `§5.1` 第 3 行「`accent-600 #475569` on `#FFFFFF` **7.56:1**」→ 实测 **7.58:1**（四舍五入差异）。
- **影响**：仅文档数值；两项均为表面差/装饰性说明，不改变任何 AA 判定（其余 16 组复算全部一致，含 D6 涉及的 4 组）。
- **requiredFix**：更正 `design-system/sub2api/MASTER.md` 两处数值（或标注计算口径）。
- **建议归属**：ui-lead / captain 文档维护。**状态：已转 D8 接受，不再要求修复。**

---

## 7. 观察项（非缺陷，供集成阶段参考）

| # | 观察 | 判定 |
|---|---|---|
| O1 | `tasks.md §2.5` 写「4 条路由（用户 2、管理员 2）」，实际注册 3 条（用户 2 + 管理员 1）：管理详情按 `design.md §1.6`「详情抽屉/弹窗」实现 | 与 design 一致，**已转登记 D9**；tasks.md 已按 3 条更正 |
| O2 | compact 首页分支（`HomeView.vue:19-96`）保留旧 `gray-*`/`white` 调色板，未随本次令牌刷新 | 该分支为未改动的既有紧凑页；对比度达标；`#8` 验收项只点名「首页/工单页/价格页」默认落地页路径，**不阻断** |
| O3 | `backend/go.sum` 含 captain 补入的 `github.com/google/subcommands v1.2.0` 两行（wire CLI 传递依赖） | 生成链路需要，**非本次验证产生的改动**；已有 t16 记录 |
| O4 | 前端 `build` 落盘 `backend/internal/web/dist/`（`.gitignore:103` 忽略），本次验证重跑了 build | 文件为 gitignored 构建产物，**不进入提交**；如需交付嵌入版二进制，t18 应确认 dist 为最新 |
| O5 | `wire_gen.go` 由 wire 重生成（t16），价格 provider 以构造参数注入 | `go build`/`go vet` 通过；`make generate` 依赖 go.sum 两行（O3） |

---

## 8. 未覆盖项（明确边界）

1. **无浏览器截图 / 像素级视觉走查**：未启动前后端联调环境，UI 结论来自单测断言 + 令牌对比度独立复算，未验证真实渲染（字体回退、1px 边框、渐变色偏）。
2. **无真实 SMTP**：`ticket.reply` 发信通过 mock notifier 断言入参与失败分支，未验证真实邮件服务送达/退信/限速。
3. **无真实 Postgres 集成**：迁移仅做 SQL 文件字符串断言（D1/D5），未在真实库执行 `251_tickets.sql`；`internal/repository` 基线 FAIL（Windows 缺 `sh`）。
4. **无真实 Redis**：限流与邮件队列相关用例在内存/降级路径下通过，未做多实例 Redis 行为验证。
5. **未验证多实例文件同步**：价格 override/CNY 文件依赖共享卷（design §6 已列为部署约束），本次未在双实例下验证。
6. **性能压测未覆盖**：工单限流的并发安全已用 `-race` 覆盖（见 §12）；`ReloadCustomPricingLayers` 的「并发读不加锁阻塞」仍仅由单测语义保证，无基准测试/吞吐测试。
7. **未做 E2E（Playwright/Cypress）**：前后端契约靠各自单测 + 类型/DTO 对齐，未跑浏览器端到端。
8. **前端 build 产物体积**：存在 `>500 kB` chunk 提示（既有基线，非本次引入），未做代码分割优化。

---

## 9. 最小阻断修复记录

- **t17（验证轮）**：无。未发现编译 / 类型 / i18n 键缺失类阻断，未修改任何实现或测试文件；三条前端命令与三条后端命令在**同一份工作树**上一次性通过。
- **t19（V1 修复，非本报告作者）**：新增 `backend/internal/handler/ticket_rate_limit.go` 并在 `ticket_handler.go` 接入；属功能修复而非"最小阻断"，已按 t20 要求复验（§12）。
- **t20（集成收口）**：**未修改功能代码**。仅执行代码生成（生成物 SHA 与生成前一致）与一次 `backend/go.sum` 生成副产物清理：`go generate ./ent` 会临时写入 2 行 `github.com/inconshreveable/mousetrap v1.1.0`（ent CLI 工具链依赖），已在验证后移除，使 `go.sum` 最终只含预期的 `google/subcommands` 两行（详见 §13.2）。

---

## 10. 复现命令（同一工作树，Windows PowerShell）

```powershell
# 后端（工作目录 backend）
go -C backend build ./...
go -C backend vet ./...
go -C backend test ./internal/service/... ./internal/handler/... ./internal/server/... ./internal/migrations/... ./migrations/... -count=1
# 基线对照（无 DB/无 sh 环境下预期 FAIL，非本次回归）
go -C backend test ./internal/repository/... -count=1
# design §5.1 定向过滤
go -C backend test ./internal/service/... -run 'Ticket|ModelPricing|Currency|CNY|PricingOverride|DeepSeek|NotificationEmail' -count=1 -v
go -C backend test ./internal/handler/... -run 'Ticket|ModelPricing' -count=1 -v

# 前端（必须 pnpm 10.24.0；本机 pnpm 11 会因 lockfile overrides 不兼容失败）
npx --yes pnpm@10.24.0 -C frontend typecheck
npx --yes pnpm@10.24.0 -C frontend check:i18n
npx --yes pnpm@10.24.0 -C frontend test:run
npx --yes pnpm@10.24.0 -C frontend build

# 代码生成可重复性（t20）
go -C backend generate ./ent                     # 两次输出 SHA256 一致（346 个 .go）
go -C backend generate ./cmd/server              # 两次输出 SHA256 一致（wire_gen.go）
go -C backend build ./...

# 工单限流复验（t20；-race 需临时前置 CGO 工具链）
go -C backend test ./internal/handler/ -run 'TicketRate' -count=1 -v
$env:PATH="C:\Users\ThinkPad\AppData\Local\mingw64\bin;$env:PATH"; $env:CGO_ENABLED="1"
go -C backend test -race ./internal/handler/ -run 'TicketRate' -count=1 -v

# 计费数值抽查（t20）
go -C backend test ./internal/service/... -run 'TestBillingCNYModeOneMillionInputCost|TestCNYPricingConvertAndDirectRates|TestBillingDeepSeekOfficialPriceYieldsToManagedPricing|TestBillingDeepSeekPeakMultiplierSkipsManagedPricing' -count=1 -v
```

---

## 11. 结论与建议（t20 最终态）

- **验收结论**：`design.md §5.2` **9/9 通过**；`deviations.md **D1–D9**` **9/9 与实现一致**（全部按已接受偏差判定）；t17 的 6 条契约命令全部 `exit 0`，t20 的复核命令（ent/wire 各连跑两次、build、限流 4 例 + `-race` 4 例、计费 4 个用例、`internal/repository` 基线复跑）全部符合预期。
- **发现项闭合**：V1 已由 t19 修复并复验（§12）；V2/V3 已转 D7/D8 接受；O1 已转 D9。**无未决阻断项。**
- **基线说明**：`internal/repository` 的 FAIL 属既有环境基线（Windows 缺 `sh` + 无 Postgres），t20 复跑结果与 t17 相同（3 个 `backup_pg_dumper` 用例，`exec: "sh"`），t19/t20 未触碰失败用例。
- **交付状态**：**可交付**。按约定**不部署、不重启服务**；提交前注意排除 `.agent-teams/`（团队运行态、未被 gitignore）并确认 `backend/internal/web/dist` 不入库。

---

## 12. t19 修复后复验（V1 关闭）

### 12.1 实现核对

| 项 | 结论 | 证据 |
|---|---|---|
| 限流额度 | 5 次 / 60 秒 | `backend/internal/handler/ticket_rate_limit.go:11-12`：`ticketWriteRateLimit = 5`、`ticketWriteRateWindow = time.Minute` |
| 作用范围 | 仅用户侧「创建工单 + 追加回复」 | `ticket_handler.go:22/28`（handler 持 limiter）、`:97`（Create）、`:152`（AddReply）；管理端 handler 未改，读接口未改 |
| 429 响应 | 统一错误信封 + `Retry-After` | `ticket_handler.go:38-48`：`infraerrors.TooManyRequests("RATE_LIMITED", "Too many ticket submissions, please slow down and try again later")`；`Retry-After` 按最早记录向上取整秒 |
| 窗口滑动语义 | 拒绝不写时间戳、过期惰性清理 | `Allow` 只在放行时 append；`pruneLocked` 遍历清理过期时间戳与空键；`RetryAfter` 只读过滤不压缩底层数组 |
| 用户隔离 | `map[int64][]time.Time` 按 user_id 独立计数 | `ticket_rate_limit.go:24-30`；用例 `TestTicketRateLimitIsolatesUsersAndPrunesExpired` |
| 并发安全 | `sync.Mutex` 保护读改写 | 用例 `TestTicketRateLimitConcurrentAccessIsSafe`（64 goroutine → 每用户恰好 5 次）+ `-race` |
| nil 安全 | nil receiver 直接放行 | `Allow`/`RetryAfter` 首行判空（`:44`、`:63`） |

### 12.2 复验命令与结果

| # | 命令 | 结果 |
|---|---|---|
| 1 | `go -C backend test ./internal/handler/ -run 'TicketRate' -count=1 -v` | **exit 0**，4 PASS / 0 FAIL（`ok internal/handler 0.258s`） |
| 2 | `$env:PATH="C:\Users\ThinkPad\AppData\Local\mingw64\bin;$env:PATH"; $env:CGO_ENABLED="1"; go -C backend test -race ./internal/handler/ -run 'TicketRate' -count=1 -v` | **exit 0**，4 PASS / 0 FAIL（`ok internal/handler 1.292s`），**无 `WARNING: DATA RACE`** |

> `-race` 前置说明：本机默认 `CGO_ENABLED=0`，直接 `go test -race` 会报 `-race requires cgo`；需临时前置 MinGW-w64 gcc（`C:\Users\ThinkPad\AppData\Local\mingw64\bin`）并把 `CGO_ENABLED=1`，**不改持久 PATH/GOENV**。该前置已在用户级记忆中记录。

### 12.3 用例断言清单

- `TestTicketRateLimitAllowsFiveThenBlocksSixth`：前 5 次放行、第 6 次拒绝且 `RetryAfter > 0`；30s 后仍拒绝（**拒绝不顺延窗口**）；再过 31s 恢复，并在新窗口第 6 次再次拒绝。
- `TestTicketRateLimitIsolatesUsersAndPrunesExpired`：用户 7 被限流不影响用户 8；每用户一条计数；窗口滑过后过期键被**惰性清理**（`hits` 2 → 1）。
- `TestTicketRateLimitHandlerReturns429`：4 次创建 + 1 次追加 = 5 次放行（第 5 次追加 200）；第 6 次创建 **429 + `RATE_LIMITED` + `Retry-After`**；追加回复同样 429（共用额度）；`GET /tickets` 与 `GET /tickets/:id` 仍 200（读接口不受影响）；窗口滑过后 `POST` 恢复 201。
- `TestTicketRateLimitConcurrentAccessIsSafe`：用户 7/8 各 32 并发写，严格各放行 5 次；`-race` 下无数据竞争。

**结论：V1 已关闭。** 限流为进程内单实例计数（多副本各自计数），与 panel 全局限流语义一致，属已知部署约束（不新增偏差）。

---

## 13. 集成收口复核（t20）

### 13.1 最终 diff 复核

| 检查项 | 结论 | 证据 |
|---|---|---|
| 变更规模 | 36 modified（+4119 / −871）+ 54 untracked 条目（展开 75 文件） | `git diff --shortstat`、`git status --porcelain` |
| 变更构成 | modified：backend/internal 11、ent 生成物 9、backend 其他 2（`go.sum`、`wire_gen.go`）、deploy 2、frontend i18n 6、frontend 其他 6；untracked：ent 15、backend/internal 18、migrations 1、frontend 26、design-system 1、openspec 10、团队运行态 4 | 按路径分组 |
| 调试代码 | **0 命中** | diff 新增 4119 行 + 60 个新源码文件（`.go/.ts/.vue/.sql`）扫描 `fmt.Print` / `console.log` / `debugger` / `TODO` / `FIXME` |
| 密钥/凭据 | **0 命中** | 同上扫描 `PRIVATE KEY` / `secret=` / `password=` / `api_key=`；`deploy/.env.example:500` 为 `PRICING_CNY_OVERRIDE_FILE=`（空示例），`config.example.yaml:1168` 为注释示例 |
| 锁文件 | 仅 `backend/go.sum` +2 行 | `google/subcommands v1.2.0`（h1 + /go.mod）；`pnpm-lock.yaml` / `package-lock.json` / `go.mod` 均未修改 |
| 前端产物 / 依赖 | 未入库 | `node_modules`、`backend/internal/web/dist`（`.gitignore:103`）、`.vite` 均未被跟踪 |
| 非交付项 | `.agent-teams/`（4 文件，`team.json` 204KB）未被 gitignore | **提交前须排除**（`git add -A` 会误收团队运行态） |

### 13.2 代码生成可重复性

| 步骤 | 命令 | 结果 |
|---|---|---|
| ent 生成 #1 | `go -C backend generate ./ent` | exit 0 |
| ent 生成 #2 | 同上 | exit 0；346 个 `.go` 文件 **SHA256 与第 1 次完全一致**（首轮与再次生成均无 churn） |
| wire 生成 #1 | `go -C backend generate ./cmd/server` | exit 0；`wire_gen.go` SHA256 = `FE560ED9…21C25DFE` |
| wire 生成 #2 | 同上 | exit 0；SHA256 相同，且**与生成前一致**（幂等） |
| 构建 | `go -C backend build ./...` | **exit 0**（生成后仍通过） |

> **go.sum 与 mousetrap 说明**：`go generate ./ent` 会临时追加 2 行 `github.com/inconshreveable/mousetrap v1.1.0`（ent CLI 工具链间接依赖）。本次在验证后已移除，使 `go.sum` 最终只保留预期的 `google/subcommands` 两行；若要求 `go generate ./ent` 完全不改 `go.sum`，需把这两行一并提交或固定工具依赖。

### 13.3 计费数值抽查（可复现）

`go -C backend test ./internal/service/... -run 'TestBillingCNYModeOneMillionInputCost|TestCNYPricingConvertAndDirectRates|TestBillingDeepSeekOfficialPriceYieldsToManagedPricing|TestBillingDeepSeekPeakMultiplierSkipsManagedPricing' -count=1 -v` → **exit 0，4/4 PASS**（含子用例）。

| 口径/场景 | 断言 | 复算值 |
|---|---|---|
| convert · kimi-k2 输入 1M | `4.0/7.14`（误差 `1e-9`） | **0.5602240896358543** ≈ 0.5602 USD/MTok |
| convert · 输出 / 缓存读 | `16.0/7.14` / `1.0/7.14` | 2.2408963585434 / 0.1400560224090 |
| direct · 输入 / 输出 / 缓存读 | `4` / `16` / `1` | 4.0 / 16.0 / 1.0 USD/MTok |
| 未登记模型 `gpt-5.4` | 两种口径均为 2.5 | 2.5（不随口径变化） |
| DeepSeek 未接管 | 官方价 0.66/MTok，峰谷 ×2 | 1.32 |
| DeepSeek + USD override | 覆盖价 `5e-06/token` | 5.0 |
| DeepSeek + CNY 登记（7.14） | `7.14 CNY ÷ 7.14` | 1.0 |
| 峰谷倍率让位 | 管理员接管（override/CNY）后不再叠加 | 1.32 → 5.0 / 1.0 无 ×2 |

> **无隐性倍率叠加**：`pricingManagedByAdmin` 在 `billing_service.go:1467/1688` 两处调用点让位，峰谷倍率仅对未接管模型生效；测试以 `require.InDelta(…, 1e-9)` 精确断言，排除了倍率/优先级层叠加放大。

### 13.4 基线复跑

`go -C backend test ./internal/repository/... -count=1` → **exit 1**（13.276s），仍为 `TestPgDumperHoldsMigrationLockThroughReaderClose` / `TestPgDumperReleasesMigrationLockWhenProcessFails` / `TestPgDumperReportsUnlockFailureAndDiscardsConnection` 三例，原因 `exec: "sh": executable file not found in %PATH%` —— **既有环境基线，非本次回归**。

---

## 14. 交付摘要

### 14.1 工单系统（ticket-center）

一句话：用户可创建/查看/回复/关闭工单，管理员可筛选、回复（默认邮件通知）、改状态与优先级；越权统一 404、关闭后回复 409、非法值 400，用户写操作 60 秒 5 次限流。

- **验证方式**：service 15 用例（状态机/归属/长度/上限/发信）+ 用户 handler 7 + 管理 handler 6 + 迁移 1；t19 限流 4 用例（含 `-race`）；前端用户 12 + 管理 7 vitest。
- **已知限制**：无真实 SMTP（mock notifier 断言入参与失败分支，D5）；无真实 Postgres（迁移仅 SQL 断言，D1/D5）；管理端详情为弹窗（D9）；限流为单实例进程内计数。

### 14.2 内置模型价格管理（model-price-admin）

一句话：管理员按模型分页/搜索内置价，可字段级覆盖 USD/1M 或一键恢复内置，写盘原子替换 + 回读校验 + 热重载，7 个管理端点。

- **验证方式**：管理 service 9 用例（排序/分页上限 100/搜索/原子写/热重载/`null` 回退/非法值 400/未配置只读）+ handler 3；前端 11 vitest；`go build` + `pnpm build` 通过。
- **已知限制**：价格来源为 LiteLLM 目录 + 文件快照，未接真实 DB；`override_file` 需共享卷（多实例部署约束）；无审计日志逐条核对（复用既有中间件）。

### 14.3 人民币计费口径（model-pricing-currency）

一句话：`direct`（¥1=$1）与 `convert`（÷`cny_per_usd`，默认 7.14）双口径，人民币登记优先于目录、USD 覆盖优先于人民币，DeepSeek 官方价让位，写文件即热重载。

- **验证方式**：CNY 服务 10 用例（换算/优先级/损坏降级/目录外模型/热重载/路径推导/深拷贝）+ 计费 3 用例；§13.3 抽查复现 **convert 0.560224 / direct 4.0** 与 DeepSeek 三态。
- **已知限制**：优先级表之外的分组/渠道定价仍优先（设计如此）；无真实账单端到端核对；损坏文件降级仅告警（按设计）。

### 14.4 全站 UI 配色重构（ui-refresh）

一句话：方向 B「Indigo & Cyan」令牌落地到 `tailwind.config.js` + `style.css`，焦点环不透明、语义按钮压深至 AA、暗色使用独立 ink 色阶。

- **验证方式**：MASTER.md 对比度表 18 组独立复算（16 组一致、2 组 D8 偏差接受）；前端 `typecheck`/`build` 通过；新增页面 `dark:` 变体覆盖（工单 14+12、管理工单 24、价格页 27、价格组件 66）。
- **已知限制**：**无浏览器像素级走查/截图**；compact 首页分支保留旧调色板（O2）；新页面 `gray-*` 混用（D7）；对比度按令牌配对复算，未逐页实测。

### 14.5 首页重做（ui-refresh §4.3）

一句话：默认落地页 7 区块（导航 / Hero / 代码示例 / 能力矩阵 6 卡 / 模型墙 / 计费 CTA / 页脚），保留 `hasHomeContent` > `compactHomeEnabled` > 默认页三模式优先级。

- **验证方式**：`HomeView.spec.ts` 31 + `HomeView.compact.spec.ts` 13 用例（三模式分支、CTA 目标随登录态与支付/订阅/余额开关、复制 + aria-live、页脚版本/联系信息、无伪造统计数字、reduced-motion、焦点环、暗色持久化）；`check:i18n` 3 passed。
- **已知限制**：无浏览器截图与 E2E；模型墙为静态清单；`prefers-reduced-motion` 仅断言类/样式存在，未做真实动画录制。

### 14.6 交付状态与后续建议

- **交付物**：代码（36 modified + 54 untracked，未提交）＋ 文档（`design.md` / `proposal.md` / `specs/` / `tasks.md` / `deviations.md` / 本报告）＋ `design-system/sub2api/MASTER.md` 与 `docs/design/ui-palette-preview.html`。
- **质量门**：后端 `build`/`vet`/相关包测试全绿（`internal/repository` 基线 FAIL 除外）；前端 `typecheck`/`check:i18n`/`test:run`（388 文件 3171 tests）/`build` 全绿；生成链路幂等。
- **部署**：**未部署、未重启服务**（按约定；如用户要求部署需另行确认）。
- **后续建议**：① 在带 Postgres + `sh` 的环境跑一次 `internal/repository` 回归（D5）；② 接入真实 SMTP 做一次送达验证；③ 多实例共享卷验证价格文件热重载；④ 补浏览器视觉走查与 E2E；⑤ 提交前排除 `.agent-teams/`。

### 14.7 已知限制汇总

1. 无浏览器截图/像素级视觉走查（UI 结论来自单测 + 令牌复算）。
2. 无真实 SMTP（邮件用 mock 断言）。
3. 无真实 Postgres / Redis 集成（迁移为 SQL 断言；限流为进程内计数）。
4. 无 E2E（Playwright/Cypress）。
5. 无性能/吞吐基准；多实例文件同步与价格热重载并发未实测。
6. `-race` 需本地 CGO 前置（MinGW-w64 gcc），CI 环境需具备 gcc 才能复跑。
