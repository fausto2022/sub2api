package repository

import (
	"context"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/ent/ticket"
	"github.com/Wei-Shaw/sub2api/ent/ticketreply"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type ticketRepository struct {
	client *dbent.Client
}

func NewTicketRepository(client *dbent.Client) service.TicketRepository {
	return &ticketRepository{client: client}
}

// CreateWithFirstReply 在同一事务内创建工单并写入首条 user 回复。
func (r *ticketRepository) CreateWithFirstReply(ctx context.Context, item *service.Ticket, reply *service.TicketReply) error {
	_, err := withTicketTx(ctx, r.client, func(txCtx context.Context, client *dbent.Client) (struct{}, error) {
		created, err := client.Ticket.Create().
			SetUserID(item.UserID).
			SetSubject(item.Subject).
			SetCategory(item.Category).
			SetPriority(item.Priority).
			SetStatus(item.Status).
			SetReplyCount(item.ReplyCount).
			SetNillableLastReplyAt(item.LastReplyAt).
			SetLastReplyBy(item.LastReplyBy).
			Save(txCtx)
		if err != nil {
			return struct{}{}, err
		}
		applyTicketEntity(item, created)

		reply.TicketID = created.ID
		replyEntity, err := client.TicketReply.Create().
			SetTicketID(reply.TicketID).
			SetAuthorID(reply.AuthorID).
			SetAuthorRole(reply.AuthorRole).
			SetContent(reply.Content).
			SetEmailNotified(reply.EmailNotified).
			SetCreatedAt(reply.CreatedAt).
			Save(txCtx)
		if err != nil {
			return struct{}{}, err
		}
		applyTicketReplyEntity(reply, replyEntity)
		return struct{}{}, nil
	})
	return err
}

func (r *ticketRepository) GetByID(ctx context.Context, id int64) (*service.Ticket, error) {
	entity, err := clientFromContext(ctx, r.client).Ticket.Query().
		Where(ticket.IDEQ(id)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return ticketEntityToService(entity), nil
}

func (r *ticketRepository) GetByUser(ctx context.Context, id, userID int64) (*service.Ticket, error) {
	entity, err := clientFromContext(ctx, r.client).Ticket.Query().
		Where(ticket.IDEQ(id), ticket.UserIDEQ(userID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return ticketEntityToService(entity), nil
}

func (r *ticketRepository) ListByUser(
	ctx context.Context,
	userID int64,
	params pagination.PaginationParams,
	status string,
) ([]service.Ticket, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	query := client.Ticket.Query().Where(ticket.UserIDEQ(userID))
	if status != "" {
		query = query.Where(ticket.StatusEQ(status))
	}
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	entities, err := query.
		Order(dbent.Desc(ticket.FieldCreatedAt), dbent.Desc(ticket.FieldID)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}
	out := make([]service.Ticket, 0, len(entities))
	for _, entity := range entities {
		out = append(out, *ticketEntityToService(entity))
	}
	return out, paginationResultFromTotal(int64(total), params), nil
}

func (r *ticketRepository) ListAdmin(
	ctx context.Context,
	params pagination.PaginationParams,
	filters service.AdminTicketFilters,
) ([]service.Ticket, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	query := client.Ticket.Query()

	predicates := make([]predicate.Ticket, 0, 3)
	if filters.Status != "" {
		predicates = append(predicates, ticket.StatusEQ(filters.Status))
	}
	if filters.Priority != "" {
		predicates = append(predicates, ticket.PriorityEQ(filters.Priority))
	}
	if filters.UserID > 0 {
		predicates = append(predicates, ticket.UserIDEQ(filters.UserID))
	}
	if filters.Search != "" {
		orPredicates := []predicate.Ticket{ticket.SubjectContainsFold(filters.Search)}
		userIDs, err := r.searchUserIDs(ctx, client, filters.Search)
		if err != nil {
			return nil, nil, err
		}
		if len(userIDs) > 0 {
			orPredicates = append(orPredicates, ticket.UserIDIn(userIDs...))
		}
		replyTicketIDs, err := r.searchReplyTicketIDs(ctx, client, filters.Search)
		if err != nil {
			return nil, nil, err
		}
		if len(replyTicketIDs) > 0 {
			orPredicates = append(orPredicates, ticket.IDIn(replyTicketIDs...))
		}
		predicates = append(predicates, ticket.Or(orPredicates...))
	}
	if len(predicates) > 0 {
		query = query.Where(predicates...)
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	entities, err := query.
		Order(dbent.Desc(ticket.FieldUpdatedAt), dbent.Desc(ticket.FieldID)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	out := make([]service.Ticket, 0, len(entities))
	userIDs := make([]int64, 0, len(entities))
	for _, entity := range entities {
		item := ticketEntityToService(entity)
		out = append(out, *item)
		userIDs = append(userIDs, item.UserID)
	}
	if err := r.fillUserSummaries(ctx, client, out, userIDs); err != nil {
		return nil, nil, err
	}
	return out, paginationResultFromTotal(int64(total), params), nil
}

func (r *ticketRepository) ListReplies(ctx context.Context, ticketID int64) ([]service.TicketReply, error) {
	entities, err := clientFromContext(ctx, r.client).TicketReply.Query().
		Where(ticketreply.TicketIDEQ(ticketID)).
		Order(dbent.Asc(ticketreply.FieldCreatedAt), dbent.Asc(ticketreply.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.TicketReply, 0, len(entities))
	for _, entity := range entities {
		out = append(out, *ticketReplyEntityToService(entity))
	}
	return out, nil
}

// AddReply 在同一事务内写入回复并更新工单的状态/最后回复/冗余计数；
// 已关闭工单（并发关闭）返回 ErrTicketClosed。
func (r *ticketRepository) AddReply(
	ctx context.Context,
	reply *service.TicketReply,
	status string,
) (*service.Ticket, error) {
	return withTicketTx(ctx, r.client, func(txCtx context.Context, client *dbent.Client) (*service.Ticket, error) {
		createdReply, err := client.TicketReply.Create().
			SetTicketID(reply.TicketID).
			SetAuthorID(reply.AuthorID).
			SetAuthorRole(reply.AuthorRole).
			SetContent(reply.Content).
			SetEmailNotified(reply.EmailNotified).
			SetCreatedAt(reply.CreatedAt).
			Save(txCtx)
		if err != nil {
			return nil, err
		}
		applyTicketReplyEntity(reply, createdReply)

		affected, err := client.Ticket.Update().
			Where(ticket.IDEQ(reply.TicketID), ticket.StatusNEQ(domain.TicketStatusClosed)).
			SetStatus(status).
			SetLastReplyAt(createdReply.CreatedAt).
			SetLastReplyBy(reply.AuthorRole).
			AddReplyCount(1).
			Save(txCtx)
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			return nil, service.ErrTicketClosed
		}

		entity, err := client.Ticket.Query().Where(ticket.IDEQ(reply.TicketID)).Only(txCtx)
		if err != nil {
			return nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
		}
		return ticketEntityToService(entity), nil
	})
}

func (r *ticketRepository) UpdateTicket(
	ctx context.Context,
	ticketID int64,
	status, priority *string,
) (*service.Ticket, error) {
	client := clientFromContext(ctx, r.client)
	update := client.Ticket.UpdateOneID(ticketID)
	if status != nil {
		update = update.SetStatus(*status)
	}
	if priority != nil {
		update = update.SetPriority(*priority)
	}
	if _, err := update.Save(ctx); err != nil {
		return nil, translatePersistenceError(err, service.ErrTicketNotFound, nil)
	}
	return r.GetByID(ctx, ticketID)
}

func (r *ticketRepository) MarkReplyEmailNotified(ctx context.Context, replyID int64) error {
	return clientFromContext(ctx, r.client).TicketReply.UpdateOneID(replyID).
		SetEmailNotified(true).
		Exec(ctx)
}

func (r *ticketRepository) Stats(ctx context.Context) (*service.TicketStats, error) {
	client := clientFromContext(ctx, r.client)
	stats := &service.TicketStats{}
	counts := []struct {
		status string
		target *int64
	}{
		{domain.TicketStatusOpen, &stats.Open},
		{domain.TicketStatusPending, &stats.Pending},
		{domain.TicketStatusResolved, &stats.Resolved},
		{domain.TicketStatusClosed, &stats.Closed},
	}
	for _, item := range counts {
		count, err := client.Ticket.Query().Where(ticket.StatusEQ(item.status)).Count(ctx)
		if err != nil {
			return nil, err
		}
		*item.target = int64(count)
	}
	total, err := client.Ticket.Query().Count(ctx)
	if err != nil {
		return nil, err
	}
	stats.Total = int64(total)
	return stats, nil
}

func (r *ticketRepository) searchUserIDs(ctx context.Context, client *dbent.Client, search string) ([]int64, error) {
	return client.User.Query().
		Where(user.Or(
			user.EmailContainsFold(search),
			user.UsernameContainsFold(search),
		)).
		IDs(ctx)
}

func (r *ticketRepository) searchReplyTicketIDs(ctx context.Context, client *dbent.Client, search string) ([]int64, error) {
	replies, err := client.TicketReply.Query().
		Where(ticketreply.ContentContainsFold(search)).
		Select(ticketreply.FieldTicketID).
		All(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]struct{}, len(replies))
	out := make([]int64, 0, len(replies))
	for _, reply := range replies {
		if _, ok := seen[reply.TicketID]; ok {
			continue
		}
		seen[reply.TicketID] = struct{}{}
		out = append(out, reply.TicketID)
	}
	return out, nil
}

func (r *ticketRepository) fillUserSummaries(
	ctx context.Context,
	client *dbent.Client,
	items []service.Ticket,
	userIDs []int64,
) error {
	if len(items) == 0 || len(userIDs) == 0 {
		return nil
	}
	unique := make([]int64, 0, len(userIDs))
	seen := make(map[int64]struct{}, len(userIDs))
	for _, id := range userIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	users, err := client.User.Query().Where(user.IDIn(unique...)).All(ctx)
	if err != nil {
		return err
	}
	summaries := make(map[int64][2]string, len(users))
	for _, u := range users {
		summaries[u.ID] = [2]string{u.Email, u.Username}
	}
	for i := range items {
		if summary, ok := summaries[items[i].UserID]; ok {
			items[i].UserEmail = summary[0]
			items[i].UserName = summary[1]
		}
	}
	return nil
}

func withTicketTx[T any](
	ctx context.Context,
	client *dbent.Client,
	fn func(context.Context, *dbent.Client) (T, error),
) (T, error) {
	var zero T
	if dbent.TxFromContext(ctx) != nil {
		return fn(ctx, clientFromContext(ctx, client))
	}
	tx, err := client.Tx(ctx)
	if errors.Is(err, dbent.ErrTxStarted) {
		return fn(ctx, client)
	}
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback() }()

	value, err := fn(dbent.NewTxContext(ctx, tx), tx.Client())
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return value, nil
}

func ticketEntityToService(entity *dbent.Ticket) *service.Ticket {
	if entity == nil {
		return nil
	}
	item := &service.Ticket{}
	applyTicketEntity(item, entity)
	return item
}

func applyTicketEntity(item *service.Ticket, entity *dbent.Ticket) {
	if item == nil || entity == nil {
		return
	}
	item.ID = entity.ID
	item.UserID = entity.UserID
	item.Subject = entity.Subject
	item.Category = entity.Category
	item.Priority = entity.Priority
	item.Status = entity.Status
	item.LastReplyAt = entity.LastReplyAt
	if entity.LastReplyBy != nil {
		item.LastReplyBy = *entity.LastReplyBy
	} else {
		item.LastReplyBy = ""
	}
	item.ReplyCount = entity.ReplyCount
	item.CreatedAt = entity.CreatedAt
	item.UpdatedAt = entity.UpdatedAt
}

func ticketReplyEntityToService(entity *dbent.TicketReply) *service.TicketReply {
	if entity == nil {
		return nil
	}
	item := &service.TicketReply{}
	applyTicketReplyEntity(item, entity)
	return item
}

func applyTicketReplyEntity(item *service.TicketReply, entity *dbent.TicketReply) {
	if item == nil || entity == nil {
		return
	}
	item.ID = entity.ID
	item.TicketID = entity.TicketID
	item.AuthorID = entity.AuthorID
	item.AuthorRole = entity.AuthorRole
	item.Content = entity.Content
	item.EmailNotified = entity.EmailNotified
	item.CreatedAt = entity.CreatedAt
}

var _ service.TicketRepository = (*ticketRepository)(nil)
