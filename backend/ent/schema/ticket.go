package schema

import (
	"time"

	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Ticket holds the schema definition for the Ticket entity.
//
// 工单主表。首条提问不单独建表：创建工单时同事务写入一条 author_role=user 的
// ticket_replies 记录（见迁移 251_tickets.sql）。
//
// 外键（user_id → users.id ON DELETE CASCADE）由数据库迁移保证，这里不声明 ent edge，
// 避免改动既有 User schema。
type Ticket struct {
	ent.Schema
}

func (Ticket) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "tickets"},
	}
}

func (Ticket) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (Ticket) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id").
			Comment("提交人用户 ID"),
		field.String("subject").
			MaxLen(200).
			NotEmpty().
			Comment("工单标题"),
		field.String("category").
			MaxLen(40).
			Default("general").
			Comment("分类: general, billing, api, account, other"),
		field.String("priority").
			MaxLen(16).
			Default("normal").
			Comment("优先级: low, normal, high, urgent"),
		field.String("status").
			MaxLen(16).
			Default("open").
			Comment("状态: open, pending, resolved, closed"),
		field.Time("last_reply_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).
			Comment("最后回复时间，用于列表排序"),
		field.String("last_reply_by").
			MaxLen(16).
			Optional().
			Nillable().
			Comment("最后回复方: user, admin"),
		field.Int("reply_count").
			Default(0).
			Comment("回复数冗余计数，列表用"),
	}
}

func (Ticket) Indexes() []ent.Index {
	return []ent.Index{
		// 迁移 251_tickets.sql 中带 DESC 排序，这里只声明列集合。
		index.Fields("user_id", "created_at"),
		index.Fields("status", "last_reply_at"),
		index.Fields("created_at"),
	}
}

// TicketReply holds the schema definition for the TicketReply entity.
//
// 外键（ticket_id → tickets.id ON DELETE CASCADE）由数据库迁移保证。
type TicketReply struct {
	ent.Schema
}

func (TicketReply) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "ticket_replies"},
	}
}

func (TicketReply) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("ticket_id").
			Comment("所属工单 ID"),
		field.Int64("author_id").
			Comment("作者 ID（用户或管理员）"),
		field.String("author_role").
			MaxLen(16).
			NotEmpty().
			Comment("作者角色: user, admin"),
		field.String("content").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			NotEmpty().
			Comment("回复内容（纯文本，1..8000 字符）"),
		field.Bool("email_notified").
			Default(false).
			Comment("管理员回复是否已发送邮件通知"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (TicketReply) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ticket_id", "created_at"),
	}
}
