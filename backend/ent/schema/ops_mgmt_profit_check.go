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

// OpsMgmtProfitCheck 利润检查历史（对齐 relaydeck ProfitCheck）。
//
// 表结构真源是迁移 253_ops_mgmt.sql；运行期读写走原生 SQL 仓储。
//
// 字段口径：
//   - sale_multiplier_micros / cost_multiplier_micros：倍率口径（1e6 = 1.0x），用于防抖判定；
//   - revenue_micros / cost_micros：金额口径，来自本系统 usage_logs 的窗口聚合（不改计费链路）；
//   - status：healthy / risk / unsupported / unknown（unsupported 与 unknown 都不动作）；
//   - cost_reliable + cost_reason：成本可信性（快照过期/缺失时不做停用决策）。
type OpsMgmtProfitCheck struct {
	ent.Schema
}

func (OpsMgmtProfitCheck) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "opsmgmt_profit_checks"}}
}

func (OpsMgmtProfitCheck) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("site_id"),
		field.Int64("remote_group_id"),
		field.String("group_name").MaxLen(200).Optional(),
		field.String("channel_ref").MaxLen(200).Optional().Comment("本地 Channel 引用（channel:<id>）"),
		field.Int64("sale_multiplier_micros").Default(0),
		field.Int64("cost_multiplier_micros").Default(0),
		field.Int64("revenue_micros").Default(0),
		field.Int64("cost_micros").Default(0),
		field.Int64("margin_basis_points").Default(0),
		field.String("status").MaxLen(16).NotEmpty(),
		field.String("cost_source").MaxLen(32).Optional(),
		field.Time("cost_observed_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Bool("cost_reliable").Default(false),
		field.String("cost_reason").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional(),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (OpsMgmtProfitCheck) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("site_id", "remote_group_id", "created_at"),
		index.Fields("channel_ref", "created_at"),
		index.Fields("created_at"),
	}
}
