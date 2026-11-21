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

// OpsMgmtUpstreamSite 上游站点（运营管理，design.md 第二部分 v3.0）。
//
// 表结构真源是迁移 253_ops_mgmt.sql。运行期读写走原生 SQL 仓储
// （internal/repository/ops_mgmt_repo.go），与本仓库 channels / channel_groups 的做法一致：
// 运营管理的控制对象是 channels（非 ent 实体），为它单独引入一套 ent 实体只会产生无人使用的
// 生成代码。本 schema 的作用是把 DDL 以类型化方式固化在代码里（字段/索引/表名一致），
// 供评审比对与未来需要 ORM 时直接 `go generate ./ent` 落地。
//
// 敏感字段：password_enc / access_token_enc / refresh_token_enc / upstream_api_key_enc
// 均为密文（复用 security_secret 的 AES-256-GCM 机制），任何响应与日志都必须脱敏。
type OpsMgmtUpstreamSite struct {
	ent.Schema
}

func (OpsMgmtUpstreamSite) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "opsmgmt_upstream_sites"}}
}

func (OpsMgmtUpstreamSite) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(100).NotEmpty().Comment("站点名（唯一）"),
		field.String("base_url").SchemaType(map[string]string{dialect.Postgres: "text"}).NotEmpty(),
		field.String("auth_mode").MaxLen(16).Default("password").Comment("password（默认）/ api_key（可选）"),
		field.String("username").SchemaType(map[string]string{dialect.Postgres: "text"}).NotEmpty(),
		field.String("password_enc").SchemaType(map[string]string{dialect.Postgres: "text"}).NotEmpty().Sensitive(),
		field.String("access_token_enc").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional().Sensitive(),
		field.String("refresh_token_enc").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional().Sensitive(),
		field.Time("token_expires_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("upstream_api_key_enc").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional().Sensitive().
			Comment("该站点开出的普通 API Key（扩池时作 Channel 凭据，非管理员 Key）"),
		field.String("status").MaxLen(16).Default("active"),
		field.Time("last_synced_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("last_error").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional(),
		field.Int("sync_interval_minutes").Default(30),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (OpsMgmtUpstreamSite) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("name").Unique(),
		index.Fields("status"),
	}
}
