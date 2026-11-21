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

// OpsMgmtUpstreamAccount 上游账号快照。
//
// 表结构真源是迁移 253_ops_mgmt.sql；运行期读写走原生 SQL 仓储。
//
// v3.0 语义提醒：扩池**不再**从上游搬账号（扩池 = 在本系统创建 Channel 对接上游），
// 因此 password 模式下这张表为空；只有站点显式配了可选的管理员 Key（auth_mode=api_key）
// 时才会通过 GET /admin/accounts 拉到账号快照（含 credentials / group_ids /
// rate_multiplier / schedulable），用于运维对账与灰度观察。
type OpsMgmtUpstreamAccount struct {
	ent.Schema
}

func (OpsMgmtUpstreamAccount) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "opsmgmt_upstream_accounts"}}
}

func (OpsMgmtUpstreamAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("site_id"),
		field.Int64("remote_account_id"),
		field.String("name").MaxLen(200).Optional(),
		field.String("platform").MaxLen(32).Optional(),
		field.String("type").MaxLen(32).Optional(),
		field.String("status").MaxLen(16).Optional(),
		field.Bool("schedulable").Optional().Nillable(),
		field.Float("rate_multiplier").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "numeric(10,4)"}),
		field.JSON("group_ids", []int64{}).Optional().SchemaType(map[string]string{dialect.Postgres: "bigint[]"}),
		// 上游账号凭据原文（JSON），仅 api_key 模式可得；不对外输出。
		field.JSON("credentials", map[string]any{}).Optional().Sensitive().
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.Time("synced_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (OpsMgmtUpstreamAccount) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("site_id", "remote_account_id").Unique(),
		index.Fields("site_id"),
	}
}
