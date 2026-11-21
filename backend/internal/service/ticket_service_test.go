package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func TestTicketCreateValidatesInput(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	svc := newFakeTicketService(repo)

	cases := []struct {
		name  string
		input CreateTicketInput
		want  error
	}{
		{name: "subject required", input: CreateTicketInput{Subject: "   ", Content: "hi"}, want: ErrTicketSubjectRequired},
		{name: "subject too long", input: CreateTicketInput{Subject: strings.Repeat("标", domain.TicketSubjectMaxLength+1), Content: "hi"}, want: ErrTicketSubjectTooLong},
		{name: "content required", input: CreateTicketInput{Subject: "subject", Content: "\n\t "}, want: ErrTicketContentRequired},
		{name: "content too long", input: CreateTicketInput{Subject: "subject", Content: strings.Repeat("x", domain.TicketContentMaxLength+1)}, want: ErrTicketContentTooLong},
		{name: "invalid category", input: CreateTicketInput{Subject: "subject", Content: "body", Category: "nope"}, want: ErrTicketInvalidCategory},
		{name: "invalid priority", input: CreateTicketInput{Subject: "subject", Content: "body", Priority: "critical"}, want: ErrTicketInvalidPriority},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := svc.CreateTicket(ctx, 1, tc.input)
			require.ErrorIs(t, err, tc.want)
		})
	}
	require.Zero(t, len(repo.tickets), "invalid input must not persist tickets")
}

func TestTicketReplyContentValidation(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	svc := newFakeTicketService(repo)
	ticket := repo.seedTicket(1, domain.TicketStatusOpen, 1)
	tooLong := strings.Repeat("x", domain.TicketContentMaxLength+1)

	_, _, err := svc.AddUserReply(ctx, 1, ticket.ID, "   ")
	require.ErrorIs(t, err, ErrTicketContentRequired)
	_, _, err = svc.AddUserReply(ctx, 1, ticket.ID, tooLong)
	require.ErrorIs(t, err, ErrTicketContentTooLong)

	_, err = svc.AdminReply(ctx, 99, ticket.ID, "", false, nil)
	require.ErrorIs(t, err, ErrTicketContentRequired)
	_, err = svc.AdminReply(ctx, 99, ticket.ID, tooLong, false, nil)
	require.ErrorIs(t, err, ErrTicketContentTooLong)

	require.Equal(t, 1, len(repo.replies[ticket.ID]), "invalid replies must not persist")
}

func TestTicketCreateStartsOpenWithFirstReply(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	notifier := &fakeTicketNotifier{}
	svc := newFakeTicketServiceWith(repo, notifier)

	ticket, reply, err := svc.CreateTicket(ctx, 42, CreateTicketInput{
		Subject: "  无法调用 API  ",
		Content: "  请求返回 500  ",
	})
	require.NoError(t, err)
	require.Equal(t, "无法调用 API", ticket.Subject)
	require.Equal(t, domain.TicketStatusOpen, ticket.Status)
	require.Equal(t, domain.TicketCategoryGeneral, ticket.Category)
	require.Equal(t, domain.TicketPriorityNormal, ticket.Priority)
	require.Equal(t, domain.TicketAuthorRoleUser, ticket.LastReplyBy)
	require.Equal(t, 1, ticket.ReplyCount)
	require.NotNil(t, ticket.LastReplyAt)
	require.Equal(t, ticket.ID, reply.TicketID)
	require.Equal(t, "请求返回 500", reply.Content)
	require.Equal(t, domain.TicketAuthorRoleUser, reply.AuthorRole)
	require.Equal(t, 1, len(repo.replies[ticket.ID]))
	require.Empty(t, notifier.calls, "ticket creation must not send email")
}

func TestTicketUserReplyReopensAndRejectsClosed(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	svc := newFakeTicketService(repo)

	openTicket := repo.seedTicket(7, domain.TicketStatusResolved, 1)
	ticket, reply, err := svc.AddUserReply(ctx, 7, openTicket.ID, "还有更多信息")
	require.NoError(t, err)
	require.Equal(t, domain.TicketStatusOpen, ticket.Status)
	require.Equal(t, domain.TicketAuthorRoleUser, ticket.LastReplyBy)
	require.Equal(t, 2, ticket.ReplyCount)
	require.Equal(t, domain.TicketAuthorRoleUser, reply.AuthorRole)
	require.Equal(t, 2, len(repo.replies[openTicket.ID]))

	closedTicket := repo.seedTicket(7, domain.TicketStatusClosed, 1)
	_, _, err = svc.AddUserReply(ctx, 7, closedTicket.ID, "closed")
	require.ErrorIs(t, err, ErrTicketClosed)
	require.Equal(t, 1, len(repo.replies[closedTicket.ID]))
}

func TestTicketOwnershipIsolationAndClosedDetail(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	svc := newFakeTicketService(repo)

	ticket := repo.seedTicket(1, domain.TicketStatusOpen, 1)
	_, err := svc.GetUserTicket(ctx, 2, ticket.ID)
	require.ErrorIs(t, err, ErrTicketNotFound)

	_, _, err = svc.AddUserReply(ctx, 2, ticket.ID, "hijack")
	require.ErrorIs(t, err, ErrTicketNotFound)

	_, err = svc.CloseUserTicket(ctx, 2, ticket.ID)
	require.ErrorIs(t, err, ErrTicketNotFound)

	detail, err := svc.GetUserTicket(ctx, 1, ticket.ID)
	require.NoError(t, err)
	require.Equal(t, ticket.ID, detail.Ticket.ID)
	require.NotEmpty(t, detail.Replies)

	closed, err := svc.CloseUserTicket(ctx, 1, ticket.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TicketStatusClosed, closed.Status)
	again, err := svc.CloseUserTicket(ctx, 1, ticket.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TicketStatusClosed, again.Status)
	require.Equal(t, 1, repo.updateCount)
}

func TestTicketReplyLimitIsEnforced(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	svc := newFakeTicketService(repo)

	ticket := repo.seedTicket(1, domain.TicketStatusOpen, domain.TicketMaxRepliesPerTicket)
	_, _, err := svc.AddUserReply(ctx, 1, ticket.ID, "one more")
	require.ErrorIs(t, err, ErrTicketReplyLimitReached)

	_, err = svc.AdminReply(ctx, 99, ticket.ID, "admin one more", false, nil)
	require.ErrorIs(t, err, ErrTicketReplyLimitReached)
}

func TestTicketAdminReplySendsEmailAndDefaultsPending(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	notifier := &fakeTicketNotifier{}
	svc := newFakeTicketServiceWith(repo, notifier)
	svc.userRepo.(*fakeTicketUserReader).users[7] = &User{ID: 7, Email: "user@example.com", Username: "alice"}

	ticket := repo.seedTicket(7, domain.TicketStatusOpen, 1)
	result, err := svc.AdminReply(ctx, 99, ticket.ID, "已修复，请重试", true, nil)
	require.NoError(t, err)
	require.Equal(t, domain.TicketStatusPending, result.Ticket.Status)
	require.Equal(t, domain.TicketAuthorRoleAdmin, result.Ticket.LastReplyBy)
	require.True(t, result.EmailSent)
	require.Empty(t, result.EmailError)
	require.True(t, result.Reply.EmailNotified)
	require.Contains(t, repo.markedNotified, result.Reply.ID)

	calls := notifier.calls
	require.Len(t, calls, 1)
	require.Equal(t, NotificationEmailEventTicketReply, calls[0].Event)
	require.Equal(t, int64(7), calls[0].UserID)
	require.Equal(t, "user@example.com", calls[0].RecipientEmail)
	require.Equal(t, ticket.Subject, calls[0].Variables["ticket_subject"])
	require.Equal(t, "已修复，请重试", calls[0].Variables["reply_content"])
	require.Equal(t, "1", calls[0].Variables["ticket_id"])

	// notify_email=false 时不发信。
	notifier.calls = nil
	result, err = svc.AdminReply(ctx, 99, ticket.ID, "内部备注", false, nil)
	require.NoError(t, err)
	require.False(t, result.EmailSent)
	require.Empty(t, notifier.calls)
}

func TestTicketAdminReplyCanSetExplicitStatus(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	svc := newFakeTicketService(repo)

	ticket := repo.seedTicket(7, domain.TicketStatusOpen, 1)
	resolved := domain.TicketStatusResolved
	result, err := svc.AdminReply(ctx, 99, ticket.ID, "已处理", false, &resolved)
	require.NoError(t, err)
	require.Equal(t, domain.TicketStatusResolved, result.Ticket.Status)

	invalid := "nope"
	_, err = svc.AdminReply(ctx, 99, ticket.ID, "x", false, &invalid)
	require.ErrorIs(t, err, ErrTicketInvalidStatus)
}

func TestTicketAdminReplyEmailFailureDoesNotRollbackReply(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	notifier := &fakeTicketNotifier{err: errors.New("smtp timeout")}
	svc := newFakeTicketServiceWith(repo, notifier)
	svc.userRepo.(*fakeTicketUserReader).users[7] = &User{ID: 7, Email: "user@example.com"}

	ticket := repo.seedTicket(7, domain.TicketStatusOpen, 1)
	result, err := svc.AdminReply(ctx, 99, ticket.ID, "回复仍应落库", true, nil)
	require.NoError(t, err)
	require.False(t, result.EmailSent)
	require.Contains(t, result.EmailError, "smtp timeout")
	require.Equal(t, domain.TicketStatusPending, result.Ticket.Status)
	require.Equal(t, 2, len(repo.replies[ticket.ID]), "reply must persist even when email fails")
	require.Empty(t, repo.markedNotified)
}

func TestTicketAdminReplySkipsEmailWhenRecipientUnavailable(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	notifier := &fakeTicketNotifier{}
	svc := newFakeTicketServiceWith(repo, notifier)
	svc.userRepo.(*fakeTicketUserReader).users[7] = &User{ID: 7, Email: "   "}

	ticket := repo.seedTicket(7, domain.TicketStatusOpen, 1)
	result, err := svc.AdminReply(ctx, 99, ticket.ID, "no email", true, nil)
	require.NoError(t, err)
	require.False(t, result.EmailSent)
	require.Empty(t, result.EmailError)
	require.Empty(t, notifier.calls)
}

func TestTicketAdminUpdateValidatesStatusAndPriority(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	svc := newFakeTicketService(repo)
	ticket := repo.seedTicket(7, domain.TicketStatusOpen, 1)

	badStatus := "invalid"
	_, err := svc.AdminUpdateTicket(ctx, ticket.ID, &badStatus, nil)
	require.ErrorIs(t, err, ErrTicketInvalidStatus)

	badPriority := "critical"
	_, err = svc.AdminUpdateTicket(ctx, ticket.ID, nil, &badPriority)
	require.ErrorIs(t, err, ErrTicketInvalidPriority)

	empty := "  "
	_, err = svc.AdminUpdateTicket(ctx, ticket.ID, &empty, nil)
	require.ErrorIs(t, err, ErrTicketNothingToUpdate)

	closed := domain.TicketStatusClosed
	high := domain.TicketPriorityHigh
	updated, err := svc.AdminUpdateTicket(ctx, ticket.ID, &closed, &high)
	require.NoError(t, err)
	require.Equal(t, domain.TicketStatusClosed, updated.Status)
	require.Equal(t, domain.TicketPriorityHigh, updated.Priority)
}

func TestTicketAdminListValidatesFiltersAndClampsPagination(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	svc := newFakeTicketService(repo)

	_, _, err := svc.AdminListTickets(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, AdminTicketFilters{Status: "bad"})
	require.ErrorIs(t, err, ErrTicketInvalidStatus)

	_, _, err = svc.AdminListTickets(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, AdminTicketFilters{Priority: "bad"})
	require.ErrorIs(t, err, ErrTicketInvalidPriority)

	_, _, err = svc.AdminListTickets(ctx, pagination.PaginationParams{Page: 0, PageSize: 500}, AdminTicketFilters{Search: strings.Repeat("s", 250)})
	require.NoError(t, err)
	require.Equal(t, 1, repo.adminParams.Page)
	require.Equal(t, domain.TicketMaxPageSize, repo.adminParams.PageSize)
	require.Equal(t, 200, len([]rune(repo.adminFilters.Search)))
}

func TestTicketAuthorNamesUseSiteNameForAdminReplies(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	settings := newFakeTicketSettingRepo()
	settings.values[SettingKeySiteName] = "Acme Support"
	svc := NewTicketService(repo, &fakeTicketUserReader{users: map[int64]*User{}}, settings, &fakeTicketNotifier{})

	ticket := repo.seedTicket(7, domain.TicketStatusOpen, 1)
	result, err := svc.AdminReply(ctx, 99, ticket.ID, "hi", false, nil)
	require.NoError(t, err)
	require.Equal(t, "Acme Support", result.Reply.AuthorName)

	detail, err := svc.GetUserTicket(ctx, 7, ticket.ID)
	require.NoError(t, err)
	require.Equal(t, "Acme Support", detail.Replies[len(detail.Replies)-1].AuthorName)
}

func TestTicketAdminDetailIncludesUserAndStats(t *testing.T) {
	ctx := context.Background()
	repo := newFakeTicketRepository()
	svc := newFakeTicketService(repo)
	svc.userRepo.(*fakeTicketUserReader).users[7] = &User{ID: 7, Email: "user@example.com", Username: "alice"}

	ticket := repo.seedTicket(7, domain.TicketStatusOpen, 1)
	detail, err := svc.AdminGetTicket(ctx, ticket.ID)
	require.NoError(t, err)
	require.NotNil(t, detail.User)
	require.Equal(t, "user@example.com", detail.User.Email)
	require.Equal(t, "alice", detail.User.Username)
	require.NotEmpty(t, detail.Replies)

	repo.seedTicket(8, domain.TicketStatusResolved, 1)
	stats, err := svc.AdminStats(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.Open)
	require.Equal(t, int64(1), stats.Resolved)
	require.Equal(t, int64(2), stats.Total)
}

func TestTicketAdminGetMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	svc := newFakeTicketService(newFakeTicketRepository())
	_, err := svc.AdminGetTicket(ctx, 404)
	require.ErrorIs(t, err, ErrTicketNotFound)
}

// --- test doubles ---

type fakeTicketRepository struct {
	tickets map[int64]*Ticket
	replies map[int64][]TicketReply

	nextTicketID int64
	nextReplyID  int64

	updateCount    int
	markedNotified []int64
	adminParams    pagination.PaginationParams
	adminFilters   AdminTicketFilters
}

func newFakeTicketRepository() *fakeTicketRepository {
	return &fakeTicketRepository{
		tickets: make(map[int64]*Ticket),
		replies: make(map[int64][]TicketReply),
	}
}

func (r *fakeTicketRepository) seedTicket(userID int64, status string, replyCount int) *Ticket {
	r.nextTicketID++
	now := time.Now().UTC()
	item := &Ticket{
		ID:          r.nextTicketID,
		UserID:      userID,
		Subject:     "ticket subject",
		Category:    domain.TicketCategoryGeneral,
		Priority:    domain.TicketPriorityNormal,
		Status:      status,
		ReplyCount:  replyCount,
		LastReplyAt: &now,
		LastReplyBy: domain.TicketAuthorRoleUser,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	r.tickets[item.ID] = item
	if replyCount > 0 {
		r.nextReplyID++
		r.replies[item.ID] = append(r.replies[item.ID], TicketReply{
			ID:         r.nextReplyID,
			TicketID:   item.ID,
			AuthorID:   userID,
			AuthorRole: domain.TicketAuthorRoleUser,
			Content:    "first",
			CreatedAt:  now,
		})
	}
	return item
}

func (r *fakeTicketRepository) CreateWithFirstReply(ctx context.Context, item *Ticket, reply *TicketReply) error {
	r.nextTicketID++
	item.ID = r.nextTicketID
	r.tickets[item.ID] = cloneTicket(item)
	r.nextReplyID++
	reply.ID = r.nextReplyID
	reply.TicketID = item.ID
	r.replies[item.ID] = append(r.replies[item.ID], *reply)
	return nil
}

func (r *fakeTicketRepository) GetByID(ctx context.Context, id int64) (*Ticket, error) {
	item, ok := r.tickets[id]
	if !ok {
		return nil, ErrTicketNotFound
	}
	return cloneTicket(item), nil
}

func (r *fakeTicketRepository) GetByUser(ctx context.Context, id, userID int64) (*Ticket, error) {
	item, ok := r.tickets[id]
	if !ok || item.UserID != userID {
		return nil, ErrTicketNotFound
	}
	return cloneTicket(item), nil
}

func (r *fakeTicketRepository) ListByUser(
	ctx context.Context,
	userID int64,
	params pagination.PaginationParams,
	status string,
) ([]Ticket, *pagination.PaginationResult, error) {
	out := make([]Ticket, 0)
	for _, item := range r.tickets {
		if item.UserID != userID {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		out = append(out, *cloneTicket(item))
	}
	return out, &pagination.PaginationResult{Total: int64(len(out)), Page: params.Page, PageSize: params.Limit()}, nil
}

func (r *fakeTicketRepository) ListAdmin(
	ctx context.Context,
	params pagination.PaginationParams,
	filters AdminTicketFilters,
) ([]Ticket, *pagination.PaginationResult, error) {
	r.adminParams = params
	r.adminFilters = filters
	out := make([]Ticket, 0)
	for _, item := range r.tickets {
		if filters.Status != "" && item.Status != filters.Status {
			continue
		}
		if filters.Priority != "" && item.Priority != filters.Priority {
			continue
		}
		if filters.UserID > 0 && item.UserID != filters.UserID {
			continue
		}
		if filters.Search != "" && !strings.Contains(item.Subject, filters.Search) {
			continue
		}
		copyItem := cloneTicket(item)
		copyItem.UserEmail = "user@example.com"
		out = append(out, *copyItem)
	}
	return out, &pagination.PaginationResult{Total: int64(len(out)), Page: params.Page, PageSize: params.Limit()}, nil
}

func (r *fakeTicketRepository) ListReplies(ctx context.Context, ticketID int64) ([]TicketReply, error) {
	return append([]TicketReply(nil), r.replies[ticketID]...), nil
}

func (r *fakeTicketRepository) AddReply(ctx context.Context, reply *TicketReply, status string) (*Ticket, error) {
	item, ok := r.tickets[reply.TicketID]
	if !ok {
		return nil, ErrTicketNotFound
	}
	if item.Status == domain.TicketStatusClosed {
		return nil, ErrTicketClosed
	}
	r.nextReplyID++
	reply.ID = r.nextReplyID
	r.replies[item.ID] = append(r.replies[item.ID], *reply)
	item.Status = status
	item.ReplyCount++
	item.LastReplyAt = &reply.CreatedAt
	item.LastReplyBy = reply.AuthorRole
	return cloneTicket(item), nil
}

func (r *fakeTicketRepository) UpdateTicket(ctx context.Context, ticketID int64, status, priority *string) (*Ticket, error) {
	item, ok := r.tickets[ticketID]
	if !ok {
		return nil, ErrTicketNotFound
	}
	r.updateCount++
	if status != nil {
		item.Status = *status
	}
	if priority != nil {
		item.Priority = *priority
	}
	return cloneTicket(item), nil
}

func (r *fakeTicketRepository) MarkReplyEmailNotified(ctx context.Context, replyID int64) error {
	r.markedNotified = append(r.markedNotified, replyID)
	for ticketID, replies := range r.replies {
		for i := range replies {
			if replies[i].ID == replyID {
				replies[i].EmailNotified = true
				r.replies[ticketID] = replies
				return nil
			}
		}
	}
	return nil
}

func (r *fakeTicketRepository) Stats(ctx context.Context) (*TicketStats, error) {
	stats := &TicketStats{}
	for _, item := range r.tickets {
		switch item.Status {
		case domain.TicketStatusOpen:
			stats.Open++
		case domain.TicketStatusPending:
			stats.Pending++
		case domain.TicketStatusResolved:
			stats.Resolved++
		case domain.TicketStatusClosed:
			stats.Closed++
		}
		stats.Total++
	}
	return stats, nil
}

func cloneTicket(item *Ticket) *Ticket {
	if item == nil {
		return nil
	}
	out := *item
	if item.LastReplyAt != nil {
		lastReplyAt := *item.LastReplyAt
		out.LastReplyAt = &lastReplyAt
	}
	return &out
}

type fakeTicketUserReader struct {
	users map[int64]*User
}

func (r *fakeTicketUserReader) GetByID(ctx context.Context, id int64) (*User, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

type fakeTicketSettingRepo struct {
	values map[string]string
}

func newFakeTicketSettingRepo() *fakeTicketSettingRepo {
	return &fakeTicketSettingRepo{values: make(map[string]string)}
}

func (r *fakeTicketSettingRepo) Get(ctx context.Context, key string) (*Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *fakeTicketSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *fakeTicketSettingRepo) Set(ctx context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *fakeTicketSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *fakeTicketSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *fakeTicketSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *fakeTicketSettingRepo) Delete(ctx context.Context, key string) error {
	delete(r.values, key)
	return nil
}

type fakeTicketNotifier struct {
	calls []NotificationEmailSendInput
	err   error
}

func (n *fakeTicketNotifier) Send(ctx context.Context, input NotificationEmailSendInput) error {
	n.calls = append(n.calls, input)
	return n.err
}

func newFakeTicketService(repo *fakeTicketRepository) *TicketService {
	return newFakeTicketServiceWith(repo, &fakeTicketNotifier{})
}

func newFakeTicketServiceWith(repo *fakeTicketRepository, notifier *fakeTicketNotifier) *TicketService {
	return NewTicketService(
		repo,
		&fakeTicketUserReader{users: map[int64]*User{}},
		newFakeTicketSettingRepo(),
		notifier,
	)
}

var _ TicketRepository = (*fakeTicketRepository)(nil)
var _ SettingRepository = (*fakeTicketSettingRepo)(nil)
var _ TicketNotifier = (*fakeTicketNotifier)(nil)
