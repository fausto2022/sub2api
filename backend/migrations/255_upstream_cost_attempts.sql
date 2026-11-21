-- 网关账号级容灾 T6.1：逐次实际发送的成本审计。
-- execution_id 来自服务端。账号 ID 是历史快照，账号移除后仍保留。
CREATE TABLE IF NOT EXISTS upstream_cost_attempts (
    execution_id  UUID NOT NULL CHECK (execution_id <> '00000000-0000-0000-0000-000000000000'),
    attempt_index INTEGER NOT NULL CHECK (attempt_index > 0),
    account_id    BIGINT NOT NULL CHECK (account_id > 0),
    status        SMALLINT NULL CHECK (status BETWEEN 100 AND 999),
    elapsed_ms    BIGINT NOT NULL CHECK (elapsed_ms >= 0),
    billed_usage  JSONB NULL CHECK (jsonb_typeof(billed_usage) = 'object'),
    decision      VARCHAR(64) NOT NULL CHECK (btrim(decision) <> ''),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (execution_id, attempt_index)
);

COMMENT ON COLUMN upstream_cost_attempts.status IS '实际收到的 HTTP 状态。未收到响应时为 NULL';
COMMENT ON COLUMN upstream_cost_attempts.billed_usage IS '本次发送观察到的上游用量。未观测到用量时为 NULL';
COMMENT ON COLUMN upstream_cost_attempts.decision IS '本次发送完成后的处置结果';
