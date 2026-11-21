package repository

import (
	"context"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/ticketattachment"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type ticketAttachmentRepository struct {
	client *dbent.Client
}

// NewTicketAttachmentRepository 构造工单附件仓储（ent 实现）。
func NewTicketAttachmentRepository(client *dbent.Client) service.TicketAttachmentRepository {
	return &ticketAttachmentRepository{client: client}
}

func (r *ticketAttachmentRepository) Insert(ctx context.Context, item *service.TicketAttachment) error {
	create := clientFromContext(ctx, r.client).TicketAttachment.Create().
		SetUploaderID(item.UploaderID).
		SetStorageKey(item.StorageKey).
		SetStorageKind(item.StorageKind).
		SetOriginalName(item.OriginalName).
		SetContentType(item.ContentType).
		SetByteSize(item.ByteSize).
		SetSha256(item.SHA256).
		SetCreatedAt(item.CreatedAt)
	if item.TicketID != nil {
		create = create.SetTicketID(*item.TicketID)
	}
	if item.ReplyID != nil {
		create = create.SetReplyID(*item.ReplyID)
	}
	if item.Width > 0 {
		create = create.SetWidth(item.Width)
	}
	if item.Height > 0 {
		create = create.SetHeight(item.Height)
	}
	entity, err := create.Save(ctx)
	if err != nil {
		return err
	}
	applyTicketAttachmentEntity(item, entity)
	return nil
}

func (r *ticketAttachmentRepository) GetByID(ctx context.Context, id int64) (*service.TicketAttachment, error) {
	entity, err := clientFromContext(ctx, r.client).TicketAttachment.Query().
		Where(ticketattachment.IDEQ(id)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrTicketAttachmentNotFound, nil)
	}
	return ticketAttachmentEntityToService(entity), nil
}

func (r *ticketAttachmentRepository) FindReusable(
	ctx context.Context,
	uploaderID int64,
	sha256Hex string,
	byteSize int64,
) (*service.TicketAttachment, error) {
	entity, err := clientFromContext(ctx, r.client).TicketAttachment.Query().
		Where(
			ticketattachment.UploaderIDEQ(uploaderID),
			ticketattachment.Sha256EQ(sha256Hex),
			ticketattachment.ByteSizeEQ(byteSize),
			ticketattachment.ReplyIDIsNil(),
		).
		Order(dbent.Desc(ticketattachment.FieldCreatedAt), dbent.Desc(ticketattachment.FieldID)).
		First(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return ticketAttachmentEntityToService(entity), nil
}

func (r *ticketAttachmentRepository) ListByTicket(ctx context.Context, ticketID int64) ([]service.TicketAttachment, error) {
	entities, err := clientFromContext(ctx, r.client).TicketAttachment.Query().
		Where(ticketattachment.TicketIDEQ(ticketID)).
		Order(dbent.Asc(ticketattachment.FieldCreatedAt), dbent.Asc(ticketattachment.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.TicketAttachment, 0, len(entities))
	for _, entity := range entities {
		out = append(out, *ticketAttachmentEntityToService(entity))
	}
	return out, nil
}

// BindReply 在单一事务内回填 (ticket_id, reply_id)：只要有一行不满足
// uploader_id 匹配或仍未绑定，受影响行数就会少于预期并整体回滚。
func (r *ticketAttachmentRepository) BindReply(
	ctx context.Context,
	ticketID, replyID, uploaderID int64,
	ids []int64,
) error {
	_, err := withTicketTx(ctx, r.client, func(txCtx context.Context, client *dbent.Client) (struct{}, error) {
		affected, err := client.TicketAttachment.Update().
			Where(
				ticketattachment.IDIn(ids...),
				ticketattachment.UploaderIDEQ(uploaderID),
				ticketattachment.ReplyIDIsNil(),
			).
			SetTicketID(ticketID).
			SetReplyID(replyID).
			Save(txCtx)
		if err != nil {
			return struct{}{}, err
		}
		if int(affected) != len(ids) {
			return struct{}{}, service.ErrTicketAttachmentAlreadyBound
		}
		return struct{}{}, nil
	})
	return err
}

func (r *ticketAttachmentRepository) DeleteUnbound(ctx context.Context, id int64) (bool, error) {
	affected, err := clientFromContext(ctx, r.client).TicketAttachment.Delete().
		Where(ticketattachment.IDEQ(id), ticketattachment.ReplyIDIsNil()).
		Exec(ctx)
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (r *ticketAttachmentRepository) CountByUploaderSince(
	ctx context.Context,
	uploaderID int64,
	since time.Time,
) (int64, error) {
	count, err := clientFromContext(ctx, r.client).TicketAttachment.Query().
		Where(
			ticketattachment.UploaderIDEQ(uploaderID),
			ticketattachment.CreatedAtGTE(since),
		).
		Count(ctx)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

func (r *ticketAttachmentRepository) CountBoundByTicket(ctx context.Context, ticketID int64) (int64, error) {
	count, err := clientFromContext(ctx, r.client).TicketAttachment.Query().
		Where(
			ticketattachment.TicketIDEQ(ticketID),
			ticketattachment.ReplyIDNotNil(),
		).
		Count(ctx)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

func (r *ticketAttachmentRepository) ListOrphansBefore(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) ([]service.TicketAttachment, error) {
	if limit <= 0 {
		limit = 500
	}
	entities, err := clientFromContext(ctx, r.client).TicketAttachment.Query().
		Where(
			ticketattachment.ReplyIDIsNil(),
			ticketattachment.CreatedAtLT(cutoff),
		).
		Order(dbent.Asc(ticketattachment.FieldCreatedAt), dbent.Asc(ticketattachment.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.TicketAttachment, 0, len(entities))
	for _, entity := range entities {
		out = append(out, *ticketAttachmentEntityToService(entity))
	}
	return out, nil
}

func (r *ticketAttachmentRepository) DeleteOrphanBefore(ctx context.Context, id int64, cutoff time.Time) (bool, error) {
	affected, err := clientFromContext(ctx, r.client).TicketAttachment.Delete().
		Where(
			ticketattachment.IDEQ(id),
			ticketattachment.ReplyIDIsNil(),
			ticketattachment.CreatedAtLT(cutoff),
		).
		Exec(ctx)
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func ticketAttachmentEntityToService(entity *dbent.TicketAttachment) *service.TicketAttachment {
	if entity == nil {
		return nil
	}
	item := &service.TicketAttachment{}
	applyTicketAttachmentEntity(item, entity)
	return item
}

func applyTicketAttachmentEntity(item *service.TicketAttachment, entity *dbent.TicketAttachment) {
	if item == nil || entity == nil {
		return
	}
	item.ID = entity.ID
	item.TicketID = entity.TicketID
	item.ReplyID = entity.ReplyID
	item.UploaderID = entity.UploaderID
	item.StorageKey = entity.StorageKey
	item.StorageKind = entity.StorageKind
	item.OriginalName = entity.OriginalName
	item.ContentType = entity.ContentType
	item.ByteSize = entity.ByteSize
	if entity.Width != nil {
		item.Width = *entity.Width
	}
	if entity.Height != nil {
		item.Height = *entity.Height
	}
	item.SHA256 = entity.Sha256
	item.CreatedAt = entity.CreatedAt
}

var _ service.TicketAttachmentRepository = (*ticketAttachmentRepository)(nil)
