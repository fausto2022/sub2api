-- 254_group_failover_policy.sql
-- 网关账号级容灾：分组级 failover 策略覆盖（design.md §6.1，implementation.md T0.2）。
--
-- 只给 groups 表加 3 个**可空且无默认值**的列，用来表达「分组级覆盖」：
--   NULL      = 该层未配置，回落 gateway.failover_policy.* / gateway.max_account_switches（全局层）
--   非 NULL   = 该层显式覆盖（字段级 merge，不是对象级替换）
--
-- 三条硬约束（改动本文件前先读）：
--   1. **禁止** DEFAULT / NOT NULL。`failover_on_400` 的 false 本身是有效语义
--      （"分组显式关闭 400 换号"），必须与"未配置（回落全局）"可区分，否则
--      "未配置"会被静默变成"配置了 false"（design §3.3.5 的指针语义）。
--   2. **不动 ent**：沿用 groups.opsmgmt_min_margin_basis_points 的先例（只在
--      repository/ops_mgmt_repo.go 里用原生 SQL 读写），不新增 ent/schema 字段、
--      不重跑 ent codegen。读取实现见
--      backend/internal/repository/group_failover_policy_repo.go。
--   3. 幂等 DDL（IF NOT EXISTS），可重复执行；已应用的迁移不得修改（checksum 校验）。
--
-- 为什么 failover_on_400 与 failover_max_account_switches 单列而不只放 JSONB：
-- 与 groups.opsmgmt_min_margin_basis_points 同构——高频、单标量、需要能被 SQL 直接
-- 查询/统计的项走独立列，低频组合项走 JSONB，便于运维在 pgAdmin 里直接改。

ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_on_400 BOOLEAN NULL;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_max_account_switches INT NULL;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS failover_policy JSONB NULL;

COMMENT ON COLUMN groups.failover_on_400 IS '分组级 failover_on_400 覆盖；NULL 回落 gateway.failover_policy.failover_on_400';
COMMENT ON COLUMN groups.failover_max_account_switches IS '分组级切换次数覆盖；NULL 回落 gateway.max_account_switches（语义不变：切换次数，非账号数）';
COMMENT ON COLUMN groups.failover_policy IS '分组级 failover 策略覆盖（字段级 merge，null 字段回落全局）';
