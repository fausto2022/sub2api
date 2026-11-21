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

// TicketAttachment holds the schema definition for the TicketAttachment entity.
//
// 工单附件（图片）。表结构真源是迁移 252_ticket_attachments.sql：
//   - ticket_id 可空：上传发生在工单创建之前（两阶段上传），提交工单/回复时回填；
//   - reply_id 可空：提交时回填，孤儿清理按 reply_id IS NULL 判定；
//   - 外键（ticket_id → tickets.id、reply_id → ticket_replies.id，均 ON DELETE CASCADE）
//     由数据库迁移保证，这里不声明 ent edge，避免改动既有 Ticket/TicketReply schema。
//
// 附件内容不入库（byte_size 之外不存二进制），文件由 TicketAttachmentStorage 承载；
// storage_key 是存储根目录下的相对对象键（{yyyy}/{mm}/{uuid}.{ext}）。
type TicketAttachment struct {
	ent.Schema
}

func (TicketAttachment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "ticket_attachments"},
	}
}

func (TicketAttachment) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("ticket_id").
			Optional().
			Nillable().
			Comment("所属工单 ID（上传阶段为空，提交时回填）"),
		field.Int64("reply_id").
			Optional().
			Nillable().
			Comment("所属回复 ID（提交时回填；为空即为待绑定附件）"),
		field.Int64("uploader_id").
			Comment("上传者用户 ID"),
		field.String("storage_key").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			NotEmpty().
			Comment("存储对象键：{yyyy}/{mm}/{uuid}.{ext}"),
		field.String("storage_kind").
			MaxLen(16).
			Default("local").
			Comment("存储后端: local, s3"),
		field.String("original_name").
			MaxLen(255).
			NotEmpty().
			Comment("原始文件名（仅展示用）"),
		field.String("content_type").
			MaxLen(64).
			NotEmpty().
			Comment("魔数嗅探得到的 MIME 类型"),
		field.Int64("byte_size").
			Comment("字节数"),
		field.Int("width").
			Optional().
			Nillable().
			Comment("像素宽（只读图片头得到）"),
		field.Int("height").
			Optional().
			Nillable().
			Comment("像素高（只读图片头得到）"),
		field.String("sha256").
			MaxLen(64).
			NotEmpty().
			Comment("SHA-256 十六进制，去重与完整性校验"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (TicketAttachment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ticket_id"),
		index.Fields("reply_id"),
		// 孤儿清理扫描：仅覆盖 reply_id IS NULL 的行（与 252 迁移的部分索引一致）。
		index.Fields("created_at").Annotations(entsql.IndexWhere("reply_id IS NULL")),
	}
}
