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

// OpsMgmtUpstreamGroup 上游分组快照（数据来自 GET /api/v1/groups/available +
// GET /api/v1/groups/rates，禁止使用 /admin/* 接口）。
//
// 表结构真源是迁移 253_ops_mgmt.sql；运行期读写走原生 SQL 仓储。
//
// 倍率语义：
//   - ratio 是上游分组标价倍率；
//   - rate_multiplier 是该登录账号的用户专属倍率（缺失时回落 ratio），利润评估的成本口径用它；
//   - peak_* 是峰时倍率（作用在成本侧），image/video_separate_rate 标记非线性定价
//     （这类分组不参与自动停用，避免用单一倍率算出错误利润率）。
type OpsMgmtUpstreamGroup struct {
	ent.Schema
}

func (OpsMgmtUpstreamGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "opsmgmt_upstream_groups"}}
}

func (OpsMgmtUpstreamGroup) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("site_id").Comment("所属站点"),
		field.Int64("remote_group_id").Comment("上游分组 ID"),
		field.String("name").MaxLen(200).NotEmpty(),
		field.String("platform").MaxLen(32).Optional(),
		field.Float("ratio").SchemaType(map[string]string{dialect.Postgres: "numeric(10,4)"}).Default(1),
		field.Float("rate_multiplier").SchemaType(map[string]string{dialect.Postgres: "numeric(10,4)"}).Default(1),
		field.Bool("peak_enabled").Default(false),
		field.String("peak_start").MaxLen(8).Optional(),
		field.String("peak_end").MaxLen(8).Optional(),
		field.Float("peak_multiplier").SchemaType(map[string]string{dialect.Postgres: "numeric(10,4)"}).Default(1),
		field.Bool("image_separate_rate").Default(false),
		field.Bool("video_separate_rate").Default(false),
		field.Bool("is_exclusive").Default(false),
		field.String("subscription_type").MaxLen(32).Optional(),
		// 分组级最低利润率（就近覆盖的第三层：上游分组 > 本地分组 > 全局默认）。
		field.Int64("min_margin_basis_points").Optional().Nillable(),
		field.Time("synced_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (OpsMgmtUpstreamGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("site_id", "remote_group_id").Unique(),
		index.Fields("site_id"),
	}
}
