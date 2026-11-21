# 工单图片上传 + 管理员运营管理模块 · 全量验证报告

> 验证任务：t5（verifier3 · attempt `b9fe40e0-88e5-4451-98c5-2058c267f99c`）
> 验证日期：2026-11-16
> 代码版本：`git HEAD = 530c5e3a4`（工作区含本阶段未提交改动 164 项，属预期）
> 设计依据：`openspec/changes/tickets-attachments-and-ops/design.md`（v3.0，第四部分验收标准）

## 0. 结论

**通过。** 契约要求的 6 条命令全部 `exit 0`；design.md 第四部分 A1–A7、B1–B6、C1–C4、D1 共 19 条验收项全部有可追溯证据；未发现阻断级缺陷。

- 后端：`go build ./...`、`go vet ./...` 干净；`internal/service`、`internal/handler`、`internal/handler/admin`、`internal/migrations` 全量测试通过（含本阶段新增 73 个用例）。
- 前端：`typecheck`、`check:i18n`、`test:run`（395 files / 3246 tests）、`build` 全部通过。
- 存在 4 项低风险/信息级观察（§5）与 7 类未覆盖项（§6）；另有 1 条既有构建告警（§7）。
- 本次验证**未修改任何实现代码**，仅新增本报告，因此无「修复后复跑」记录（§8）。

---

## 1. 验证环境

| 项 | 值 |
|---|---|
| OS | Windows（PowerShell / pwsh） |
| Go | `go1.26.4 windows/amd64` |
| Node | `v22.22.2` |
| pnpm | `10.24.0`（`npx --yes pnpm@10.24.0`，符合项目锁定的 10.x 约定） |
| 工作目录 | `D:\WorkSpace\sub2api` |
| 数据库 | 未连接真实 PostgreSQL（迁移/仓储测试为文本契约或 fake，见 §6） |
| 部署 | 未部署、未重启任何服务（遵守任务约束） |

---

## 2. 契约命令与结果

| # | 命令 | Exit | 输出摘要 |
|---|---|---|---|
| 1 | `go -C backend build ./...` | 0 | 无输出（全部包编译通过） |
| 2 | `go -C backend test ./internal/service/... ./internal/handler/... ./internal/migrations/... -count=1` | 0 | `ok service 146.513s` / `ok service/openai_ws_v2 3.643s` / `ok handler 41.485s` / `ok handler/admin 0.787s` / `ok handler/dto 0.254s` / `ok handler/quotaview 0.235s` / `ok migrations 0.546s` |
| 3 | `npx --yes pnpm@10.24.0 -C frontend typecheck` | 0 | `vue-tsc --noEmit` 无错误 |
| 4 | `npx --yes pnpm@10.24.0 -C frontend check:i18n` | 0 | 1 file / **3 tests passed**（40.85s） |
| 5 | `npx --yes pnpm@10.24.0 -C frontend test:run` | 0 | **395 files / 3246 tests passed**（156.02s） |
| 6 | `npx --yes pnpm@10.24.0 -C frontend build` | 0 | `✓ built in 44.13s`（仅既有 chunk >500 kB 告警，见 §7） |

附加命令：

| 命令 | Exit | 输出摘要 |
|---|---|---|
| `go -C backend vet ./...` | 0 | 无输出 |
| `go -C backend test ./internal/service -run '^(TestTicketAttachment.*)$' -count=1 -v` | 0 | **19 / 19 PASS** |
| `go -C backend test ./internal/handler -run '^(TestTicketAttachmentHandler.*)$' -count=1 -v` | 0 | **10 / 10 PASS** |
| `go -C backend test ./internal/service -run '^(TestOpsSync.*|TestOpsBuildProfitCheck.*|TestOpsEvaluate.*|TestOpsAutoRecovery.*|TestOpsGrayMode.*|TestOpsUpstream.*|TestOpsNormalizeBaseURL|TestOpsDashboard.*|TestOpsGuaranteedRevenue.*|TestOpsMarginBasisPoints.*|TestOpsRoundMoney.*|TestOpsExpand.*|TestOpsSanitizeChannelName|TestOpsTaskRegistry.*|TestOpsMarginPolicy.*|TestOpsEffectiveMinMargin.*|TestOpsProfitBasisPoints.*|TestOpsUpstreamAPIKeyHash.*)$' -count=1 -v` | 0 | **37 PASS**（其中 33 个为本阶段新增 ops-mgmt 用例，另 4 个为既有 `ops_upstream_*` 用例被正则命中） |
| `go -C backend test ./internal/handler/admin -run '^TestOpsMgmt.*$' -count=1 -v` | 0 | **7 / 7 PASS** |
| `go -C backend test ./internal/migrations -run '^(TestTicketAttachmentsMigration.*|TestOpsMgmtMigration.*)$' -count=1 -v` | 0 | **4 / 4 PASS** |

本阶段新增测试统计：**后端 73 例**（附件 service 19 + 附件 handler 10 + 附件迁移 1 + ops-mgmt service 33 + ops-mgmt handler 7 + ops-mgmt 迁移 3）；**前端 49 例**（工单附件 22 = Uploader 16 + Thread 6；opsmgmt 27 = KpiCard 4 + RateChangeCell 4 + source 9 + SourceBadge 4 + Dashboard 6）。

---

## 3. 验收矩阵（design.md 第四部分）

图例：**✅ 通过** / ⚠️ 通过但有附注 / ❌ 未通过。

### 3.1 工单图片上传（A1–A7）

| # | 验收项 | 结论 | 证据（测试函数 / 断言） |
|---|---|---|---|
| A1 | 4 张被拒 / 3 张通过 | ✅ | `TestTicketAttachmentBindingLimits`：4 张 → `ErrTicketAttachmentLimitExceeded`；3 张 → `bound` 长度 3 且 `ReplyID` 回填；`TestTicketAttachmentHandlerCreateTicketRejectsFourAttachments` 走 HTTP：4 张 → `400` + `TICKET_ATTACHMENT_LIMIT_EXCEEDED`。上限常量 `TicketAttachmentMaxPerMessage = 3`（service/ticket_attachment.go:31） |
| A2 | 5MB 通过 / 6MB 被拒 | ✅ | `TestTicketAttachmentHandlerUploadSizeBoundary`：恰好 5MB → `201` 且 `ByteSize == 5<<20`；6MB → `413` + `TICKET_ATTACHMENT_TOO_LARGE`。常量 `TicketAttachmentMaxBytes = 5<<20`、请求体上限 `12<<20` |
| A3 | 伪造扩展名（文本改名 .png）被拒 | ✅ | `TestTicketAttachmentHandlerUploadRejectsSVGAndFakeExtension`：`plain text pretending to be a png`（文件名 `evil.png`）→ `400` + `TICKET_ATTACHMENT_TYPE_NOT_ALLOWED`；`TestTicketAttachmentUploadIgnoresDeclaredContentType` 证明以魔数为准、忽略客户端声明；`TestTicketAttachmentSniffContentTypeWhitelist` 覆盖白名单嗅探 |
| A4 | SVG 被拒 | ✅ | 同 `TestTicketAttachmentHandlerUploadRejectsSVGAndFakeExtension`：带 `<script>` 的 SVG → `400` + `TICKET_ATTACHMENT_TYPE_NOT_ALLOWED`，且 `repo.count()==0`（未落库）；白名单常量只含 PNG/JPEG/WebP/GIF，无 `image/svg+xml` |
| A5 | 用户 A 读不到用户 B 附件（404） | ✅ | `TestTicketAttachmentReadPermissionMatrix`：用户 8 读用户 7 的未绑定/已绑定附件均 `ErrTicketAttachmentNotFound`（404 不暴露存在性）；管理员可读；`TestTicketAttachmentHandlerUploadAndReadPermission` 走 HTTP 断言 `404` + `TICKET_ATTACHMENT_NOT_FOUND`、`Cache-Control: private, max-age=3600`、`X-Content-Type-Options: nosniff` |
| A6 | 绑定他人附件被拒 | ✅ | `TestTicketAttachmentBindingLimits`：用户 8 `BindReply` 用户 7 的附件 → `ErrTicketAttachmentNotFound`；`ValidateBinding` 同样拒绝；`TestTicketAttachmentHandlerCreateTicketRejectsForeignAttachment` 走 HTTP 建单路径 |
| A7 | 孤儿附件 2h 后清理 | ⚠️ | `TestTicketAttachmentCleanupOrphans`：-3h 未绑定 → 记录+磁盘文件删除；-1h 未绑定 → 保留；已绑定 → 永不清；重复执行幂等（第二次 0）。ticker 间隔 `1h`（`TicketAttachmentCleanupInterval`）、TTL `2h`（`TicketAttachmentOrphanTTL`）；启动接线 `cmd/server/wire_gen.go:305 ticketAttachmentService.StartOrphanCleanupLoop(context.Background())` 已核实。附注：停止函数未纳入优雅关闭链（§5 V-03），且未在真实时钟/PG 下跑过（§6） |

### 3.2 运营管理后端（B1–B6）

| # | 验收项 | 结论 | 证据（测试函数 / 断言） |
|---|---|---|---|
| B1 | 站点添加后能拉分组/账号/倍率 | ✅ | `TestOpsSyncSitePullsGroupsAndRates`（httptest mock）：账号密码登录 1 次、后续 `Bearer tok-1`，`group_count=2`，`rate_multiplier 0.95→1.20` 生成 `RateChanges[0].RateChanged=true` 且保留 `PreviousRateMultiplier`，峰时/图片/视频/独占字段完整落库，token 加密缓存 `enc:tok-1`；password 模式 `adminGroupsCalls==0`、`adminAccountsCalls==0`（不调用管理员接口）。辅证：`TestOpsSyncAPIKeyModePullsGroupsAndAccounts`、`TestOpsUpstreamLoginUsesPasswordEndpoint`、`TestOpsUpstreamListGroupsAndRates`、`TestOpsSyncFailureKeepsPreviousSnapshot`、`TestOpsSyncDueSitesRespectsInterval` |
| B2 | 倍率 0.95→1.20 触发 ProfitCheck status=risk | ✅ | `TestOpsBuildProfitCheckRateChangeTriggersRisk`：0.95 成本 / 1.0 售价 → `healthy`、`500 bps`、`CostSource=upstream_group_snapshot`；1.20 → `risk`、`-2000 bps`，`CostReason` 含 `does not exceed` |
| B3 | 连续 risk 达 risk_confirmations 才停用 | ✅ | `TestOpsEvaluateDebouncesRiskBeforeDisabling`（`risk_confirmations=2`）：第 1 次仅 `WouldDisable`、`ProtectionApplied` 为空、无锁、`AutoDisabledAt=nil`；第 2 次才 `ProtectionApplied` + 1 把 active 锁 + Channel `status=disabled` + reason `margin`；第 3 次幂等（仍 1 把锁、`disabledCalls=1`）；`ActivateGuardLock` 再调 `created=false`；留痕 3 条 ProfitCheck |
| B4 | 成本不可靠（过期）时不动作 | ✅ | `TestOpsEvaluateDoesNotActWhenCostUnreliable`：快照 2h 前同步（>60min 时效），连续 3 次评估均 `Risk=0`、`Unknown=1`、无 `WouldDisable`、0 锁、`AutoDisabledAt=nil`；`TestOpsBuildProfitCheckUnknownWhenCostUnreliable` 断言过期/缺失倍率 → `unknown` 而非 `risk` |
| B5 | AutoRecovery 生效后恢复账号 | ✅ | `TestOpsAutoRecoveryRestoresChannel`：连续 2 次 risk 停用 → 倍率回落 0.8 → 第 1 次 healthy 不解锁（防抖）→ 第 2 次 `Recovered=[channelRef(11)]`、`activeLockCount()==0`、`AutoDisabledAt=nil`、`status=active` |
| B6 | 灰度模式（auto_margin_protection=false）只记录不停用 | ✅ | `TestOpsGrayModeRecordsWithoutDisabling`：开关关闭时连续 3 次 `GrayMode=true`、`WouldDisable` 有值、`ProtectionApplied` 为空、0 锁、`AutoDisabledAt=nil`、告警含 `gray mode`；打开开关后再评估即真正停用（`ProtectionApplied` + 1 锁） |

### 3.3 运营看板与前端视觉（C1–C4）

| # | 验收项 | 结论 | 证据 |
|---|---|---|---|
| C1 | 看板 ProfitSummary 数值正确 | ✅ | 后端 `TestOpsDashboardProfitSummary`（构造 usage totals 断言收入/成本/利润/利润率/保底收入）、`TestOpsDashboardGuaranteedRevenueUsesConfiguredRatio`（按 `guaranteed_revenue_ratio_bp` 换算）、`TestOpsDashboardUnavailableWhenUsageFails`（数据不可用 → `Available=false`，不伪造 0）、`TestOpsDashboardSingleDayWindowDoesNotFabricateSevenDay`、边界 `TestOpsGuaranteedRevenueEdgeCases` / `TestOpsMarginBasisPointsMoneyEdgeCases` / `TestOpsRoundMoneyEdgeCases`。口径核实：仓储 SQL `SUM(actual_cost)` 为收入、`SUM(total_cost)` 为成本（repository/ops_mgmt_repo.go:1217）。前端 `OpsMgmtDashboard.spec.ts` 6 例：KPI 数值/保底小字、双折线+利润面积、逐日缺失回退两点、亏损行 + badge、不可用横幅与 `—`、加载失败不崩 |
| C2 | 账号列表显示「运营管理」徽章 | ✅（组件/逻辑级） | `SourceBadge.spec.ts` 4 例（⬢ 内联 SVG + 站点名 + `title` 悬浮详情；手工创建灰字；停用原因 `margin`→「利润率不足」；未知原因回退原文）；`source.spec.ts` 中 `resolveAccountSource` 覆盖「分组被运营渠道覆盖 → opsmgmt」。视图接线已核实：`AccountsView.vue` 有 `cell-source` 列（:300）、`accounts-source-filter`（:19）与 `loadOpsSourceIndex()`（:2648）。附注：无 AccountsView 挂载级断言（§5 V-01） |
| C3 | 按来源筛选生效 | ✅（纯函数级） | `source.spec.ts` `filterBySource`：空筛选保持原顺序 `[11,1,2]`、`opsmgmt` → `[11]`、`manual` → `[1,2]`；`AccountsView.vue:1170` / `ChannelsView.vue` 均调用 `filterBySource`。附注：筛选为「当前页客户端过滤」（t4 已声明的口径偏差），且无视图挂载级断言（§5 V-01） |
| C4 | 倍率变动视觉（箭头+原值） | ✅ | `RateChangeCell.spec.ts` 4 例：上涨 `text-red-700` + ↑ + 文本 `admin.opsmgmt.rate.up` + 原值灰字；下降绿色 + ↓；不变隐藏；显式 `flag='unchanged'` 优先于数值差异（规避布尔 prop 缺省强转问题） |

### 3.4 全量门禁（D1）

| # | 验收项 | 结论 | 证据 |
|---|---|---|---|
| D1 | 全量 typecheck/i18n/test/build | ✅ | 见 §2 命令 3–6：`typecheck exit 0`、`check:i18n 3 tests`、`test:run 395 files/3246 tests`、`build 44.13s exit 0`；另附 i18n 使用键核对：本阶段模板中 237 个 `admin.opsmgmt.*` / `attachments.*` 键在 zh 语言包均有同名叶子键（0 缺失） |

---

## 4. 集成接线核对（附加，非验收项）

| 接线点 | 结论 | 证据 |
|---|---|---|
| 工单附件路由 | ✅ | `routes/user.go:171-174`：`POST /tickets/attachments`、`GET|DELETE /tickets/attachments/:id`（声明在 `/:id` 之前，避免 `attachments` 被当作工单 ID） |
| 附件存储 | ✅ | `wire_gen.go:301 service.ProvideTicketAttachmentStorage(configConfig)` → 默认 `{pricing.data_dir}/ticket-attachments`，可用 `TICKET_ATTACHMENT_STORAGE_DIR` 覆盖 |
| 孤儿清理回路 | ✅ | `wire_gen.go:305 StartOrphanCleanupLoop(context.Background())`；`TicketAttachmentCleanupInterval=1h`、TTL=2h |
| 运营管理路由 | ✅ | `routes/admin.go:209-246` 注册 24 条（23 条 `/admin/opsmgmt/*` + `PUT /admin/groups/:id/opsmgmt-margin` 别名）；与既有 `/admin/ops/*` 前缀隔离由 `TestOpsMgmtRoutePrefixIsIsolated` 断言 |
| 定时同步回路 | ✅ | `service/wire.go:1152-1159 ProvideOpsManagementService` 构造后调用 `svc.StartSyncScheduler(context.Background())`；`SyncDueSites` 按站点 `sync_interval_minutes` 调度，`SyncSite` 成功后调用 `EvaluateSite(ctx, site.ID, "sync")`（sync_service.go:131） |
| 看板数据源 | ✅ | `NewOpsManagementService` 对 repo 做 `OpsUsageStatsRepository` 类型断言（`SumUsageTotals`/`SumUsageByGroup`/`CountUsageDays` 均由 `opsMgmtRepository` 实现），dashboard 不会退化为 nil |
| 前端路由/侧栏 | ✅ | `router/index.ts:480-528` 4 页（dashboard/sites/expand/profit）；`AppSidebar.vue:818-828` 运营管理组；构建产物含 `SitesView/ExpandView/ProfitView/DashboardView` chunk |
| 前端 API 契约 | ✅ | `api/admin/opsmgmt.ts` 的 23 条路径与后端路由逐条对齐；扩池同时支持单条（201）与 `{items}` 批量（202 task），与 handler 的 `OpsExpandRequest` 兼容 |

---

## 5. 缺陷 / 风险登记

未发现阻断级（blocker/high）缺陷。以下为本次验证发现的可改进项，均**未修改实现**（遵守任务约束）。

| ID | Severity | Problem | RequiredFix |
|---|---|---|---|
| V-01 | low | C2/C3 的「账号列表徽章 / 按来源筛选」只有组件级（`SourceBadge.spec.ts`）与纯函数级（`source.spec.ts`）证据，缺 `AccountsView.vue` / `ChannelsView.vue` 的挂载级断言；两处视图接线靠人工核对（列定义、filter 调用、页面加载时 `loadOpsSourceIndex`）。 | 补两个视图级 spec：mock `@/api/admin/opsmgmt`，断言 `account-source-<id>` / `channels-source-filter` 渲染与筛选后的行集合。 |
| V-02 | low | 迁移 252/253 仅有「SQL 文本契约 + embed 存在性」断言（`internal/migrations/*`），未在真实 PostgreSQL 执行；`ops_mgmt_repo.go` 的原生 SQL（部分唯一索引、UPSERT 冲突路径、`bigint[]` 列、聚合 SQL）无集成测试。 | 在带 PG 的 CI job 中执行迁移 + 仓储冒烟（建站点→同步快照→GuardLock 幂等→usage 聚合），或引入 testcontainers。 |
| V-03 | low | 附件清理 goroutine 以 `context.Background()` 启动，`StopOrphanCleanup()` 未纳入 `provideCleanup` 关闭链；进程退出时随进程终止，无泄漏，但无法优雅停止。 | 在 `wire_gen.go` 的 `provideCleanup` 增加 `TicketAttachmentService` 停止步骤，或传入可取消 context。 |
| V-04 | info | design.md §2.4.2 保底收入公式文字自相矛盾：正文写「毛收入 × 保底比例」，伪代码写 `TodayRevenue / GuaranteedRateRatio`。实现取除法（`opsGuaranteedRevenue`），语义为「按实际折扣反推毛收入」。 | 澄清文档口径（建议保留「÷ 比例」并修正文字），避免后续按「×」回改导致回归。 |

---

## 6. 未覆盖项（本报告不声称已验证）

1. **真实上游站点**：B1 全部基于 `httptest` mock（`opsUpstreamMock`），未验证真实上游的登录/刷新协议差异、限频、错误体、分页与字段缺失场景；无管理员 Key 的真实站点也未联调。
2. **真实 PostgreSQL**：迁移仅在编译期 embed + 文本断言；表结构、级联外键、部分唯一索引、数组列、UPSERT 行为未在真实 PG 上执行（见 V-02）。`internal/repository` 未纳入本次契约命令（既有约定需 DB），故不声称其全绿。
3. **SMTP / 通知渠道**：design 中通知为「按需」，本阶段未实现/未验证邮件或 webhook 通知；`NotificationEmailService` 仅在代码层复用，无联调。
4. **浏览器 E2E / 像素走查**：无 Playwright/Cypress；前端仅 jsdom 单测 + 生产构建，未做真实浏览器点击、拖拽、粘贴截图、灯箱与响应式走查。
5. **前端页面级覆盖**：`SitesView.vue`、`ExpandView.vue`、`ProfitView.vue` 无挂载级测试；`AccountsView`/`ChannelsView` 的来源列/筛选无挂载级测试（V-01）。
6. **S3 附件存储**：design 列为可选，当前只落 local；`TicketAttachmentStorageKindS3` 仅预留常量，无实现与测试。
7. **安全与并发压测**：越权/伪造/尺寸/配额均有单测，但未做渗透测试、并发上传/并发绑定压测、真实图片解压炸弹样本；未跑 `-race`（本机需 MinGW/CGO，且非契约要求）。

---

## 7. 基线既有失败

| 项 | 结果 |
|---|---|
| 后端契约包 | **无失败**（`service` / `handler` / `handler/admin` / `migrations` 全绿；无 `FAIL`、无因缺 DB 的 `SKIP` 影响） |
| 前端全量套件 | **无失败**（395 files / 3246 tests 全 PASS，无 skipped 阻断） |
| 既有构建告警 | `frontend build` 报若干 chunk >500 kB（如 `AccountsView` 861 kB、`SettingsView` 452 kB）——历史遗留的体积告警，非本阶段引入，不影响构建成功 |
| 全仓 `go test ./...` | 未执行（超出契约范围；`internal/repository` 等包需 DB）——不代表这些包存在失败 |

---

## 8. 变更与复跑

本次验证**未修改任何实现代码或测试**，仅新增本文件 `verification.md`。因此：

- 不存在「最小阻断修复 → 复跑」记录；
- §2 的全部命令均为在当前工作区（含本阶段全部实现）上的一次性通过结果；
- 若后续按 §5 修复 V-01/V-02/V-03，需要重跑 §2 全部 6 条命令并更新本报告。

---

## 9. 附：本阶段新增用例 PASS 清单（摘要）

**工单附件（service，19）**：`TestTicketAttachmentObjectKeyUsesYearMonthUUIDLayout`、`...ObjectIDIsUUIDv4`、`...LocalStoragePutGetDelete`、`...SniffContentTypeWhitelist`、`...UploadPNGStoresMetadata`、`...UploadIgnoresDeclaredContentType`、`...UploadRejectsInvalidPayloads`、`...UploadRejectsOversizedDimensions`、`...UploadDedupesSameImage`、`...UploadDailyQuota`、`...ReadPermissionMatrix`、`...DeleteUnboundOnly`、`...BindingLimits`、`...BindEnforcesTicketCap`、`...CleanupOrphans`、`...ListReplyAttachmentsGroupsByReply`、`...SanitizeOriginalName`、`...AdminReplyBindsAttachments`、`...AdminReplyRejectsInvalidAttachments`。

**工单附件（handler，10）**：`TestTicketAttachmentHandlerUploadAndReadPermission`、`...UploadRejectsSVGAndFakeExtension`、`...UploadSizeBoundary`、`...UploadDailyQuota`、`...DeleteUnbound`、`...CreateTicketBindsAttachments`、`...ReplyBindsAttachments`、`...CreateTicketRejectsForeignAttachment`、`...CreateTicketRejectsFourAttachments`、`...AdminCanUpload`。

**运营管理（handler/admin，7）**：`TestOpsMgmtHandlerSiteCRUDMasksSecrets`、`...SettingsContract`、`...DashboardContract`、`...GroupMarginContract`、`...ExpandAndRestoreContract`、`...TasksAndProfitContract`、`TestOpsMgmtRoutePrefixIsIsolated`。

**迁移（4）**：`TestTicketAttachmentsMigrationCreatesTableColumnsIndexesAndCascades`、`TestOpsMgmtMigrationCreatesSixTables`、`...UniqueConstraintsAndIndexes`、`...LocalExtensions`。

**运营管理（service，33）**：`TestOpsUpstreamLoginUsesPasswordEndpoint`、`...RefreshRotatesToken`、`...ListGroupsAndRates`、`...RejectsNonZeroBizCode`、`TestOpsNormalizeBaseURL`、`TestOpsSyncSitePullsGroupsAndRates`、`...FailureKeepsPreviousSnapshot`、`...APIKeyModePullsGroupsAndAccounts`、`...DueSitesRespectsInterval`、`...TreatsUpstreamErrorEnvelopeAsFailure`、`TestOpsBuildProfitCheckRateChangeTriggersRisk`、`...UnknownWhenCostUnreliable`、`...UnsupportedPricing`、`TestOpsMarginPolicyDefaultsAndParsing`、`TestOpsEffectiveMinMarginPriority`、`TestOpsEvaluateDebouncesRiskBeforeDisabling`、`...DoesNotActWhenCostUnreliable`、`TestOpsAutoRecoveryRestoresChannel`、`TestOpsGrayModeRecordsWithoutDisabling`、`TestOpsDashboardProfitSummary`、`...GuaranteedRevenueUsesConfiguredRatio`、`...UnavailableWhenUsageFails`、`...SingleDayWindowDoesNotFabricateSevenDay`、`TestOpsGuaranteedRevenueEdgeCases`、`TestOpsMarginBasisPointsMoneyEdgeCases`、`TestOpsRoundMoneyEdgeCases`、`TestOpsProfitBasisPointsAndPeakWindow`、`TestOpsExpandSkipsAlreadyConnected`、`TestOpsExpandValidatesInput`、`TestOpsExpandBatchAsyncTracksProgress`、`TestOpsSanitizeChannelName`、`TestOpsTaskRegistryIsBoundedAndPrunesExpired`、`TestOpsUpstreamAPIKeyHashIsStableAndOpaque`。

**前端（工单附件，22）**：`TicketAttachmentUploader.spec.ts` 16 例（三入口/输入/拖拽/粘贴/3 张上限/5MB 边界/格式/SVG/进度/失败重试/删除/已绑定/重置）+ `TicketThread.spec.ts` 6 例（缩略图/无附件/灯箱/Esc/纯文本无 v-html/空态）。

**前端（opsmgmt，27）**：`KpiCard.spec.ts` 4 + `RateChangeCell.spec.ts` 4 + `source.spec.ts` 9 + `SourceBadge.spec.ts` 4 + `OpsMgmtDashboard.spec.ts` 6。
