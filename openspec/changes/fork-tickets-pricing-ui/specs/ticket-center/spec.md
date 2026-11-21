# ticket-center

## ADDED Requirements

### Requirement: 用户提交与管理工单
系统 SHALL 允许已登录用户创建工单（标题 1–200 字符、正文 1–8000 字符），并允许管理员按状态、优先级、关键字与用户筛选工单。创建工单 SHALL 与首条用户回复在同一事务内落库，并使工单状态为 `open`。

#### Scenario: 用户创建工单
- **WHEN** 用户提交 `{subject:"无法调用 Claude", content:"返回 401", priority:"high"}`
- **THEN** 返回工单与首条回复，`status=open`、`reply_count=1`、`last_reply_by=user`
- **AND** 该工单仅出现在该用户与管理员的列表中

#### Scenario: 参数非法
- **WHEN** 用户提交空白标题或超长正文
- **THEN** 返回 `400` 与 `INVALID_TICKET_INPUT`，不落库

### Requirement: 管理员工单回复与邮件通知
管理员回复工单 SHALL 更新工单为 `pending`、`last_reply_by=admin`，并在 `notify_email=true`（默认）且用户邮箱可用时调用通知邮件服务发送 `ticket.reply` 事件。发信失败 SHALL NOT 回滚回复，响应中返回 `email_sent=false` 与 `email_error`。

#### Scenario: 回复并成功发信
- **WHEN** 管理员回复并勾选邮件通知
- **THEN** 回复落库，`email_sent=true`，邮件变量包含工单标题、回复正文、工单链接

#### Scenario: 发信失败
- **WHEN** SMTP 不可用
- **THEN** 回复仍落库，`email_sent=false`，工单状态与回复内容不受影响

#### Scenario: 用户无邮箱
- **WHEN** 提交人未绑定可用邮箱
- **THEN** 跳过发信，`email_sent=false`，记录 warn 日志

### Requirement: 权限隔离与状态机
用户 SHALL 只能读取、回复、关闭自己的工单；越权访问 SHALL 返回 `404`。已关闭工单 SHALL 拒绝新回复（`409 TICKET_CLOSED`）。用户追加回复 SHALL 使工单回到 `open`。

#### Scenario: 访问他人工单
- **WHEN** 用户 A 请求用户 B 的 `/tickets/:id`
- **THEN** 返回 `404 TICKET_NOT_FOUND`

#### Scenario: 关闭后追加回复
- **WHEN** 用户对 `closed` 工单提交回复
- **THEN** 返回 `409 TICKET_CLOSED`，不落库

### Requirement: 工单界面
系统 SHALL 提供用户工单列表/详情页与管理员工单工作台，全部文案支持 zh/en，回复内容按纯文本渲染。

#### Scenario: 新建工单后跳转详情
- **WHEN** 用户在列表页成功新建工单
- **THEN** 跳转 `/tickets/:id` 并显示首条对话

#### Scenario: 管理员回复后状态更新
- **WHEN** 管理员在详情弹窗回复
- **THEN** 列表行状态更新为"待用户回复"，回复数 +1