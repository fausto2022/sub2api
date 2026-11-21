package domain

// Ticket status constants. 合法状态值固定为 4 个。
const (
	TicketStatusOpen     = "open"
	TicketStatusPending  = "pending"
	TicketStatusResolved = "resolved"
	TicketStatusClosed   = "closed"
)

// Ticket priority constants.
const (
	TicketPriorityLow    = "low"
	TicketPriorityNormal = "normal"
	TicketPriorityHigh   = "high"
	TicketPriorityUrgent = "urgent"
)

// Ticket category constants.
const (
	TicketCategoryGeneral = "general"
	TicketCategoryBilling = "billing"
	TicketCategoryAPI     = "api"
	TicketCategoryAccount = "account"
	TicketCategoryOther   = "other"
)

// Ticket author role constants.
const (
	TicketAuthorRoleUser  = "user"
	TicketAuthorRoleAdmin = "admin"
)

// Ticket limits.
const (
	// TicketSubjectMaxLength 是标题的最大字符数（runes）。
	TicketSubjectMaxLength = 200
	// TicketContentMaxLength 是单条回复/首条内容的最大字符数（runes）。
	TicketContentMaxLength = 8000
	// TicketMaxRepliesPerTicket 是单工单回复上限（含首条）。
	TicketMaxRepliesPerTicket = 500
	// TicketMaxPageSize 是列表分页上限。
	TicketMaxPageSize = 100
)

var ticketStatuses = map[string]struct{}{
	TicketStatusOpen:     {},
	TicketStatusPending:  {},
	TicketStatusResolved: {},
	TicketStatusClosed:   {},
}

var ticketPriorities = map[string]struct{}{
	TicketPriorityLow:    {},
	TicketPriorityNormal: {},
	TicketPriorityHigh:   {},
	TicketPriorityUrgent: {},
}

var ticketCategories = map[string]struct{}{
	TicketCategoryGeneral: {},
	TicketCategoryBilling: {},
	TicketCategoryAPI:     {},
	TicketCategoryAccount: {},
	TicketCategoryOther:   {},
}

var ticketAuthorRoles = map[string]struct{}{
	TicketAuthorRoleUser:  {},
	TicketAuthorRoleAdmin: {},
}

// IsValidTicketStatus 报告 status 是否为合法工单状态。
func IsValidTicketStatus(status string) bool {
	_, ok := ticketStatuses[status]
	return ok
}

// IsValidTicketPriority 报告 priority 是否为合法优先级。
func IsValidTicketPriority(priority string) bool {
	_, ok := ticketPriorities[priority]
	return ok
}

// IsValidTicketCategory 报告 category 是否为合法分类。
func IsValidTicketCategory(category string) bool {
	_, ok := ticketCategories[category]
	return ok
}

// IsValidTicketAuthorRole 报告 role 是否为合法作者角色。
func IsValidTicketAuthorRole(role string) bool {
	_, ok := ticketAuthorRoles[role]
	return ok
}

// IsTicketClosed 报告工单是否已关闭（关闭后拒绝新增回复）。
func IsTicketClosed(status string) bool {
	return status == TicketStatusClosed
}
