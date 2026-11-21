-- 252_ticket_attachments.sql
-- 工单图片附件。两阶段上传：先上传（reply_id/ticket_id 为空）→ 提交工单或回复时回填。
-- 幂等建表/索引/外键（可重复执行）。
--
-- 与 design.md §1.3 的差异（必要修正）：design 里 ticket_id 为 NOT NULL，但 §1.4 的
-- 两阶段流程要求「先上传后建工单」，上传时工单尚不存在，故 ticket_id 必须可空；
-- 追溯能力由 uploader_id + storage_key 保证，孤儿清理按 reply_id IS NULL 判定。

CREATE TABLE IF NOT EXISTS ticket_attachments (
    id BIGSERIAL PRIMARY KEY,
    ticket_id BIGINT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    reply_id BIGINT NULL REFERENCES ticket_replies(id) ON DELETE CASCADE,
    uploader_id BIGINT NOT NULL,
    storage_key TEXT NOT NULL,
    storage_kind VARCHAR(16) NOT NULL DEFAULT 'local',
    original_name VARCHAR(255) NOT NULL,
    content_type VARCHAR(64) NOT NULL,
    byte_size BIGINT NOT NULL,
    width INT NULL,
    height INT NULL,
    sha256 CHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ticket_attachments_ticket
    ON ticket_attachments (ticket_id);

CREATE INDEX IF NOT EXISTS idx_ticket_attachments_reply
    ON ticket_attachments (reply_id);

-- 孤儿清理扫描路径：仅覆盖未绑定（reply_id IS NULL）的行。
CREATE INDEX IF NOT EXISTS idx_ticket_attachments_orphan
    ON ticket_attachments (created_at) WHERE reply_id IS NULL;
