-- 253_ops_mgmt.sql
-- 运营管理模块（design.md 第二部分 v3.0）。
--
-- 命名：仓库里 `ops_*` 前缀已被既有可观测性模块占用（ops_error_logs 表、/admin/ops/* 路由），
-- 本模块的**数据库对象**统一用 `opsmgmt_` 前缀（表名/列名），Go 文件与路由用 `ops_mgmt` /
-- `/admin/opsmgmt`，避免任何形式的覆盖或歧义。
--
-- 本迁移是运营管理相关表结构的真源，采用幂等 DDL（可重复执行）。
-- 与 design.md 的三处必要对齐：
--   1. 上游对接用「账号密码登录 + 普通用户接口」（/auth/login、/groups/available、
--      /groups/rates），因此站点表存 password_enc / access_token_enc / refresh_token_enc；
--      upstream_api_key_enc 是「该站点开出的普通 API Key」，用于扩池时作本地 Channel 凭据，
--      与管理员 Key 无关（design §2.2.2）。
--   2. 扩池 = 对接渠道：控制对象是本地 channels 行，因此 channels 上打
--      opsmgmt_site_id / opsmgmt_remote_group_id 标签（design §2.6）。
--   3. 利润数据源是本系统 usage_logs，不含任何上游报表缓存表（design §2.4.1）。

-- ---------------------------------------------------------------------------
-- 上游站点（账号密码登录，非管理员 Key）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS opsmgmt_upstream_sites (
    id                    BIGSERIAL PRIMARY KEY,
    name                  VARCHAR(100) NOT NULL,
    base_url              TEXT NOT NULL,
    auth_mode             VARCHAR(16) NOT NULL DEFAULT 'password',
    username              TEXT NOT NULL,
    password_enc          TEXT NOT NULL,
    access_token_enc      TEXT NULL,
    refresh_token_enc     TEXT NULL,
    token_expires_at      TIMESTAMPTZ NULL,
    upstream_api_key_enc  TEXT NULL,
    status                VARCHAR(16) NOT NULL DEFAULT 'active',
    last_synced_at        TIMESTAMPTZ NULL,
    last_error            TEXT NULL,
    sync_interval_minutes INT NOT NULL DEFAULT 30,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 站点名唯一：便于按名字查找与避免重复添加同一上游。
CREATE UNIQUE INDEX IF NOT EXISTS idx_opsmgmt_upstream_sites_name
    ON opsmgmt_upstream_sites (name);
CREATE INDEX IF NOT EXISTS idx_opsmgmt_upstream_sites_status
    ON opsmgmt_upstream_sites (status);

-- ---------------------------------------------------------------------------
-- 上游分组快照（来自 GET /groups/available + GET /groups/rates）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS opsmgmt_upstream_groups (
    id                      BIGSERIAL PRIMARY KEY,
    site_id                 BIGINT NOT NULL REFERENCES opsmgmt_upstream_sites(id) ON DELETE CASCADE,
    remote_group_id         BIGINT NOT NULL,
    name                    VARCHAR(200) NOT NULL,
    platform                VARCHAR(32) NULL,
    ratio                   NUMERIC(10,4) NOT NULL DEFAULT 1,
    rate_multiplier         NUMERIC(10,4) NOT NULL DEFAULT 1,
    peak_enabled            BOOLEAN NOT NULL DEFAULT FALSE,
    peak_start              VARCHAR(8) NULL,
    peak_end                VARCHAR(8) NULL,
    peak_multiplier         NUMERIC(10,4) NOT NULL DEFAULT 1,
    image_separate_rate     BOOLEAN NOT NULL DEFAULT FALSE,
    video_separate_rate     BOOLEAN NOT NULL DEFAULT FALSE,
    is_exclusive            BOOLEAN NOT NULL DEFAULT FALSE,
    subscription_type       VARCHAR(32) NULL,
    -- 分组级最低利润率（就近覆盖的第三层，design §2.5.1）
    min_margin_basis_points BIGINT NULL,
    synced_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_opsmgmt_upstream_groups_site_remote
    ON opsmgmt_upstream_groups (site_id, remote_group_id);
CREATE INDEX IF NOT EXISTS idx_opsmgmt_upstream_groups_site
    ON opsmgmt_upstream_groups (site_id);

-- ---------------------------------------------------------------------------
-- 上游账号快照（仅 api_key 鉴权模式的站点可拉到；password 模式为空）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS opsmgmt_upstream_accounts (
    id                BIGSERIAL PRIMARY KEY,
    site_id           BIGINT NOT NULL REFERENCES opsmgmt_upstream_sites(id) ON DELETE CASCADE,
    remote_account_id BIGINT NOT NULL,
    name              VARCHAR(200) NULL,
    platform          VARCHAR(32) NULL,
    type              VARCHAR(32) NULL,
    status            VARCHAR(16) NULL,
    schedulable       BOOLEAN NULL,
    rate_multiplier   NUMERIC(10,4) NULL,
    group_ids         BIGINT[] NULL,
    credentials       JSONB NULL,
    synced_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_opsmgmt_upstream_accounts_site_remote
    ON opsmgmt_upstream_accounts (site_id, remote_account_id);
CREATE INDEX IF NOT EXISTS idx_opsmgmt_upstream_accounts_site
    ON opsmgmt_upstream_accounts (site_id);

-- ---------------------------------------------------------------------------
-- 利润检查历史（对齐 relaydeck ProfitCheck 语义）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS opsmgmt_profit_checks (
    id                      BIGSERIAL PRIMARY KEY,
    site_id                 BIGINT NOT NULL,
    remote_group_id         BIGINT NOT NULL,
    group_name              VARCHAR(200) NULL,
    channel_ref             VARCHAR(200) NULL,
    -- 倍率口径：micros（1e6 = 1.0x），与 relaydeck 的整数比较一致。
    sale_multiplier_micros  BIGINT NOT NULL DEFAULT 0,
    cost_multiplier_micros  BIGINT NOT NULL DEFAULT 0,
    -- 金额口径：本系统 usage_logs 的窗口聚合（micros）。
    revenue_micros          BIGINT NOT NULL DEFAULT 0,
    cost_micros             BIGINT NOT NULL DEFAULT 0,
    margin_basis_points     BIGINT NOT NULL DEFAULT 0,
    status                  VARCHAR(16) NOT NULL,
    cost_source             VARCHAR(32) NULL,
    cost_observed_at        TIMESTAMPTZ NULL,
    cost_reliable           BOOLEAN NOT NULL DEFAULT FALSE,
    cost_reason             TEXT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_opsmgmt_profit_checks_group
    ON opsmgmt_profit_checks (site_id, remote_group_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_opsmgmt_profit_checks_channel
    ON opsmgmt_profit_checks (channel_ref, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_opsmgmt_profit_checks_created
    ON opsmgmt_profit_checks (created_at DESC);

-- ---------------------------------------------------------------------------
-- 守护锁（幂等：active 行天然唯一，防重复停用）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS opsmgmt_guard_locks (
    id           BIGSERIAL PRIMARY KEY,
    site_id      BIGINT NOT NULL,
    target_ref   VARCHAR(200) NOT NULL,
    lock_type    VARCHAR(32) NOT NULL,
    active       BOOLEAN NOT NULL DEFAULT TRUE,
    reason       TEXT NULL,
    metadata     JSONB NULL,
    activated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cleared_at   TIMESTAMPTZ NULL
);

-- 幂等的物理保障：同一 (site, target, lock_type) 同时只允许一条 active 锁。
-- 应用层用 INSERT ... ON CONFLICT DO NOTHING，因此重复 Activate 不会产生重复锁。
CREATE UNIQUE INDEX IF NOT EXISTS idx_opsmgmt_guard_locks_active
    ON opsmgmt_guard_locks (site_id, target_ref, lock_type) WHERE active;
CREATE INDEX IF NOT EXISTS idx_opsmgmt_guard_locks_site
    ON opsmgmt_guard_locks (site_id, active);

-- ---------------------------------------------------------------------------
-- 运营配置（全局 KV，value 为 jsonb）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS opsmgmt_settings (
    key        VARCHAR(64) PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- 本地扩展：分组级最低利润率 + Channel 的运营标签
-- ---------------------------------------------------------------------------
ALTER TABLE groups ADD COLUMN IF NOT EXISTS opsmgmt_min_margin_basis_points BIGINT NULL;
ALTER TABLE opsmgmt_upstream_groups ADD COLUMN IF NOT EXISTS min_margin_basis_points BIGINT NULL;

ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_site_id BIGINT NULL
    REFERENCES opsmgmt_upstream_sites(id) ON DELETE SET NULL;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_remote_group_id BIGINT NULL;
-- 历史兼容：部分版本的 design 用「远程账号」作为标签，保留列但不再写入。
ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_remote_account_id BIGINT NULL;
-- 扩池时用户填的上游 Key（加密）与其 SHA-256，后者参与去重与审计展示。
ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_upstream_key_enc TEXT NULL;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_upstream_key_hash CHAR(64) NULL;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_auto_disabled_at TIMESTAMPTZ NULL;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS opsmgmt_auto_disabled_reason VARCHAR(64) NULL;

CREATE INDEX IF NOT EXISTS idx_channels_opsmgmt_site
    ON channels (opsmgmt_site_id);
CREATE INDEX IF NOT EXISTS idx_channels_opsmgmt_auto_disabled
    ON channels (opsmgmt_auto_disabled_at) WHERE opsmgmt_auto_disabled_at IS NOT NULL;

-- 扩池去重：同一 (站点, 上游分组, 上游 Key) 只允许一条本地 Channel（design §2.6）。
CREATE UNIQUE INDEX IF NOT EXISTS idx_channels_opsmgmt_unique
    ON channels (opsmgmt_site_id, opsmgmt_remote_group_id, opsmgmt_upstream_key_hash)
    WHERE opsmgmt_site_id IS NOT NULL AND opsmgmt_upstream_key_hash IS NOT NULL;
