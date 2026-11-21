package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OpsMgmtGuardLock 守护锁（对齐 relaydeck GuardLock）。
//
// 表结构真源是迁移 253_ops_mgmt.sql；运行期读写走原生 SQL 仓储。
//
// 幂等语义由数据库保证：`(site_id, target_ref, lock_type) WHERE active` 的部分唯一索引，
// 配合 `INSERT ... ON CONFLICT DO NOTHING`，因此重复 Activate 不会产生重复锁；
// ClearGuardLock 把 active 置为 false 并写 cleared_at，可反复修复。
//
// 附注：同模块的 opsmgmt_settings 是纯 KV 配置表（key + jsonb value），只在原生 SQL 里读写，
// 因此不建 ent 实体。
type OpsMgmtGuardLock struct {
	ent.Schema
}

func (OpsMgmtGuardLock) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "opsmgmt_guard_locks"}}
}

func (OpsMgmtGuardLock) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("site_id"),
		field.String("target_ref").MaxLen(200).NotEmpty().Comment("本地 Channel 引用（channel:<id>）"),
		field.String("lock_type").MaxLen(32).NotEmpty().Comment("margin / manual / ..."),
		field.Bool("active").Default(true),
		field.String("reason").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional(),
		field.JSON("metadata", map[string]any{}).Optional().SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.Time("activated_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("cleared_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (OpsMgmtGuardLock) Indexes() []ent.Index {
	return []ent.Index{
		// 幂等：同一 (site, target, lock_type) 只允许一条 active 锁。
		index.Fields("site_id", "target_ref", "lock_type").Unique().
			Annotations(entsql.IndexWhere("active")),
		index.Fields("site_id", "active"),
	}
}
