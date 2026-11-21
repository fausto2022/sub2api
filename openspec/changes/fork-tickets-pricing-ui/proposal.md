# 二次开发：工单系统、模型价格管理、人民币口径与 UI 重构

## Why

本仓库是 sub2api 的 fork，当前版本具备完整的 API 网关、计费、支付与运营能力，但缺少与站点运营直接相关的四类能力：

1. **用户支持闭环**：用户遇到额度、密钥、支付问题只能依赖站外渠道沟通，后台没有工单沉淀，也没有回复通知手段。
2. **内置模型价格不可管理**：内置价格来自 LiteLLM 目录（`backend/resources/model-pricing/model_prices_and_context_window.json`，运行时可由远程覆盖）与 `BillingService` 硬编码兜底表。管理员只能改渠道/分组级价格，看不到、也改不了"系统内置价"，出现上游调价或汇率口径变化时只能改代码重新发布。
3. **人民币口径缺口**：站点按 1 元人民币兑 1 美元充值，但内置价卡中由人民币官方价换算而来的条目（如 Kimi K2、豆包 embedding）仍按 `¥ ÷ 7.14` 记为美元，实际计费只收到人民币成本的约 1/7。需要可配置的"是否换算"开关，让内置人民币价格可以直接按 1:1 使用。
4. **视觉与首页陈旧**：全站配色沿用默认 teal/cyan，首页是通用营销模板，未体现"统一接入多模型"的产品定位。

## What Changes

- **新增工单系统**：用户提交/追加/关闭工单，管理员查看、回复、改状态与优先级；管理员回复可按开关触发邮件通知（新增通知邮件事件模板，中英文）。
- **新增管理端「模型价格」页**：展示系统内置模型价格（输入/输出/缓存读/缓存写，USD/百万 token），支持逐模型覆盖、恢复内置价、热生效；覆盖写入既有 `pricing.override_file` 机制，不新增数据库表。
- **新增人民币计费口径配置**：`convert`（默认，¥÷汇率转 USD）与 `direct`（¥1=$1，直接用人民币数字）两种模式 + 可配置汇率；按模型登记人民币原生价（CNY/百万 token），运行时换算为 USD 参与计费；配置与登记写入 `cny-overrides.json`，热重载。
- **全站配色重构**：以 ui-ux-pro-max skill 驱动，重设 primary / dark / 渐变 / 阴影 / 选中色等设计令牌；保持既有工具类名（`primary-*`、`dark-*`、`accent-*`）不变，避免大规模改组件。
- **首页重做**：公共首页重设计为产品落地页（导航、Hero + 代码示例、能力矩阵、模型矩阵、计费 CTA、页脚），保留既有"自定义首页内容 / 紧凑首页"两种覆盖模式与登录跳转行为。
- **验证基线**：后端 `go build ./...`、相关包 `go test`；前端 `pnpm typecheck`、`pnpm check:i18n`、`pnpm test:run`、`pnpm build`。全部功能提供可执行验收命令与测试用例。

## Capabilities

### New Capabilities

- `ticket-center`：工单数据模型、用户侧与管理侧 API、状态机、权限隔离、回复邮件通知、用户与管理员界面。
- `model-price-admin`：内置模型价格快照与分页检索、逐模型覆盖/恢复、覆盖文件原子写与热重载、管理端表格与编辑弹窗。
- `model-pricing-currency`：人民币原生价登记、`convert`/`direct` 口径切换、汇率配置、与内置价的优先级、可验证的计费差异。
- `ui-refresh`：设计令牌（配色/表面/渐变/阴影）与首页信息架构、组件与 i18n 落地。

### Modified Capabilities

- `billing`：内置定价解析链在 LiteLLM/兜底之后、USD 覆盖之前插入人民币口径层；DeepSeek 官方价强制覆盖需让位于管理员显式覆盖。
- `notification-email`：新增 `ticket.reply` 事件、中英文官方模板与变量校验。

## Impact

- **数据库**：新增 `tickets`、`ticket_replies` 两张表与迁移 `backend/migrations/251_tickets.sql`；ent schema 与生成代码。
- **后端**：`internal/domain`、`internal/service`、`internal/repository`、`internal/handler`（user/admin）、`internal/server/routes`、`internal/config`（人民币口径文件路径，可选）、`internal/service/notification_email_service.go`。
- **前端**：路由、侧边栏、i18n（zh/en）、用户工单页、管理员工单页、管理员模型价格页、首页、设计令牌；`tailwind.config.js`、`src/style.css`。
- **配置/部署**：新增可选配置 `pricing.cny_override_file`（默认与 `override_file` 同目录的 `cny-overrides.json`）；Docker/文件权限需允许后端写该目录。
- **不引入**：新的数据库表用于价格覆盖、外部字体 CDN、新的三方依赖。