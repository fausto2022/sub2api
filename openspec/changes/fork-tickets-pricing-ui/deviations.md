# 实现偏差登记（captain 维护）

本文件登记实现阶段相对 `design.md` 的有据偏离。**验证时这些项按"已接受偏差"判定，不作为缺陷**；若实现与偏差描述不符，仍需登记为发现项。

## D1 · 迁移测试位置（工单，t9）

- **设计**：`tasks.md` 1.2 要求迁移测试位于 `backend/migrations/`。
- **实际**：断言内容合并到 `backend/internal/migrations/tickets_migration_test.go`，删除 `backend/migrations/tickets_migration_test.go`。
- **原因**：任务 verify 路径写的是 `./internal/migrations/...`；原两份测试断言同一 SQL，重复。SQL 单一来源仍是 `backend/migrations/251_tickets.sql`，桥接包只做只读断言（`dbmigrations.FS.ReadFile`）。
- **结论**：接受。已验证 `go test ./internal/migrations/... ./migrations/...` 均通过。

## D2 · 两个 ent schema 同文件（工单，原 t2）

- **设计**：`design.md §1.1` 提到 `ticket.go`、`ticket_reply.go` 两个 schema 文件。
- **实际**：`Ticket` 与 `TicketReply` 两个 schema 定义同处 `backend/ent/schema/ticket.go`。
- **原因**：ent 按类型名生成，文件划分不影响产物；单文件便于同表族对照。
- **结论**：接受。生成物 `backend/ent/ticket*.go`、`ticketreply*.go` 均正常。

## D3 · 邮件收件人不校验 verified 字段（工单）

- **设计**：`design.md §1.5` 写"用户邮箱为空/未验证则跳过并返回 `email_sent=false`"。
- **实际**：仅在收件邮箱为空时跳过；用户模型无独立 `email_verified` 字段可判定。
- **结论**：接受（受既有数据模型约束）。测试用例：`TestTicketAdminReplySkipsEmailWhenRecipientUnavailable`。

## D4 · 回复上限错误码（工单）

- **设计**：`design.md §1.1` 写"单工单回复上限 500 条，超限拒绝"，未指定错误码。
- **实际**：单工单 500 条，超限返回 `400 TICKET_REPLY_LIMIT_REACHED`。
- **结论**：接受。测试：`TestTicketReplyLimitIsEnforced`。

## D5 · 无真实 Postgres / SMTP 集成测试

- **设计**：`design.md §5.2` 验收项以单元/契约测试为主。
- **实际**：仓库基线不含数据库集成测试环境（`go test ./internal/repository/...` 在无 DB 时本就 FAIL），本次未改变该基线；邮件发送通过 mock 的 `NotificationEmailService` 断言入参。
- **结论**：接受为环境限制，需在交付摘要的"已知限制"中说明。

## D6 · 设计令牌的对比度偏离（UI，t1）

详见 `design-system/sub2api/MASTER.md §7.2`，此处摘要：

| 项 | 设计值 | 实际值 | 原因 |
|---|---|---|---|
| `dark-400` | `#6E7C9A` | `#7A88A6` | 原值在 dark-800 卡面仅 4.05:1，不达 AA；该令牌是三级文字主力（47 处使用） |
| 焦点环 | `primary-500/60` | 不透明 `primary-500` / 暗色 `primary-400` | 半透明叠加实测仅 2.31:1，不达非文本 3:1 |
| 语义实心按钮 | `emerald/amber/red-500` + 白字 | `emerald-700` / `red-600` / `warning` 深字浅底 | 500 系白字对比度 2.15–3.76:1 |

**结论**：接受。验收时按 MASTER.md 的新值判定对比度。

## D7 · 新增页面混用 `gray-*` 中性色（V2，t17 发现）

- **设计**：`MASTER.md §8` 建议组件迁移速查使用 `accent-*` / `dark-*`。
- **实际**：工单页与模型价格页部分中性文字/边框仍用 `gray-*`（Tailwind 默认灰阶）。
- **原因**：`accent` 令牌在本仓库被当作 slate 灰阶使用，`gray-*` 与其色相接近；页面为新写页面，未做全量替换。
- **验证**：实测对比度达标（gray-500/白 4.83、gray-600/白 7.56、gray-300/dark-950 13.36）。
- **结论**：接受为风格不一致（非可访问性缺陷）。若后续统一灰阶，按 MASTER §8 速查批量替换。

## D8 · MASTER.md 对比度数值小幅漂移（V3，t17 发现）

- **设计**：`MASTER.md §5.1/§5.2` 记载的对比度为设计阶段近似值。
- **实际**：独立复算 18 组中 16 组完全一致，2 组有微小差异：`dark-800 vs dark-950` 1.35:1 → 实测 1.16:1；`accent-600 on white` 7.56:1 → 实测 7.58:1。
- **结论**：接受为文档精度问题，不影响任何 AA 判定（两组均非"需达标"的正文/控件配对）。

## D9 · 工单路由数量（O1，t17 发现）

- **设计**：`tasks.md §2.5` 写"新增 4 条路由（用户 2、管理员 2）"。
- **实际**：3 条（用户 `/tickets`、`/tickets/:id`；管理员 `/admin/tickets`）。管理端详情按 `design.md §1.6` 用弹窗承载，无独立 `/admin/tickets/:id` 页面。
- **结论**：接受，实现符合 design 原意；`tasks.md` 勾选时按 3 条记录。

---

> **验证判定（2026-11-15，t20 集成收口）**：D1–D9 全部按「已接受偏差」判定，未发现「实现与偏差描述不符」的项；证据见 `verification.md §5`（逐项核对表）、`§12`（V1 修复复验）、`§13`（集成收口复核）。`tasks.md` 已按 D1（迁移测试位置）与 D9（3 条工单路由）更新措辞与勾选。
