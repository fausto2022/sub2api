-- 251_tickets.sql
-- 工单系统：工单主表 + 回复表。幂等建表/索引/外键（可重复执行）。
-- 首条提问不单独建表：创建工单时同事务插入一条 author_role=user 的回复。

CREATE TABLE IF NOT EXISTS tickets (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject VARCHAR(200) NOT NULL,
    category VARCHAR(40) NOT NULL DEFAULT 'general',
    priority VARCHAR(16) NOT NULL DEFAULT 'normal',
    status VARCHAR(16) NOT NULL DEFAULT 'open',
    last_reply_at TIMESTAMPTZ NULL,
    last_reply_by VARCHAR(16) NULL,
    reply_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tickets_user_created_at
    ON tickets (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_tickets_status_last_reply_at
    ON tickets (status, last_reply_at DESC);

CREATE INDEX IF NOT EXISTS idx_tickets_created_at
    ON tickets (created_at DESC);

CREATE TABLE IF NOT EXISTS ticket_replies (
    id BIGSERIAL PRIMARY KEY,
    ticket_id BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    author_id BIGINT NOT NULL,
    author_role VARCHAR(16) NOT NULL,
    content TEXT NOT NULL,
    email_notified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ticket_replies_ticket_created_at
    ON ticket_replies (ticket_id, created_at ASC);