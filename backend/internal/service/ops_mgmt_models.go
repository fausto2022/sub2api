package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 运营管理（design.md 第二部分 v3.0）的领域模型与仓储契约。
//
// 与 relaydeck 的关系：只移植「逻辑层」（marginPolicy / resolvedCost /
// buildProfitCheck / GuardLock / 防抖），数据层与语义按本仓库改造——
//   - 上游用「账号密码登录 + 普通用户接口」（/auth/login、/groups/available、
//     /groups/rates），站点也可选配管理员 Key（auth_mode=api_key）时才走 /admin/*；
//   - 控制对象是本地 Channel（扩池 = 对接渠道）；
//   - 利润数据源是本系统 usage_logs（只读聚合，不改计费链路）。
const (
	// OpsAuthModePassword 账号密码登录（v3.0 默认也是唯一必需模式）。
	OpsAuthModePassword = "password"
	// OpsAuthModeAPIKey 可选的 x-api-key 只读模式：上游恰好提供管理员 Key 时才能拉
	// 全量分组（含 ratio/peak/image/video）与账号（含 credentials/group_ids）。
	OpsAuthModeAPIKey = "api_key"

	// OpsSiteStatusActive / OpsSiteStatusError 站点状态（同步失败置 error，保留旧快照）。
	OpsSiteStatusActive = "active"
	OpsSiteStatusError  = "error"

	// 利润检查状态（对齐 relaydeck ProfitCheck.Status）。
	OpsProfitStatusHealthy     = "healthy"
	OpsProfitStatusRisk        = "risk"
	OpsProfitStatusUnsupported = "unsupported"
	OpsProfitStatusUnknown     = "unknown"

	// 守护锁类型。
	OpsGuardLockTypeMargin = "margin"

	// OpsAutoDisabledReasonMargin 本地 Channel 被自动停用的原因标记。
	OpsAutoDisabledReasonMargin = "margin"
)

var (
	// ErrOpsSiteNotFound 站点不存在。
	ErrOpsSiteNotFound = infraerrors.NotFound("OPS_SITE_NOT_FOUND", "upstream ops site not found")
	// ErrOpsSiteNameExists 站点名重复。
	ErrOpsSiteNameExists = infraerrors.Conflict("OPS_SITE_NAME_EXISTS", "upstream ops site name already exists")
	// ErrOpsSiteAuthFailed 登录/续期失败（上游账号密码错误或不可达）。
	ErrOpsSiteAuthFailed = infraerrors.BadRequest("OPS_SITE_AUTH_FAILED", "upstream login failed")
	// ErrOpsSiteUnreachable 上游不可达。
	ErrOpsSiteUnreachable = infraerrors.ServiceUnavailable("OPS_SITE_UNREACHABLE", "upstream site is unreachable")
	// ErrOpsUpstreamGroupNotFound 上游分组快照不存在（未同步或已被上游删除）。
	ErrOpsUpstreamGroupNotFound = infraerrors.NotFound("OPS_UPSTREAM_GROUP_NOT_FOUND", "upstream group snapshot not found")
	// ErrOpsExpandDuplicate 同一 (站点, 上游分组, 上游 Key) 已对接过。
	ErrOpsExpandDuplicate = infraerrors.Conflict("OPS_EXPAND_DUPLICATE", "this upstream group is already connected")
	// ErrOpsExpandInvalidInput 扩池参数缺失。
	ErrOpsExpandInvalidInput = infraerrors.BadRequest("OPS_EXPAND_INVALID_INPUT", "site, upstream group, upstream api key and target group are required")
	// ErrOpsChannelNotFound 本地 Channel 不存在。
	ErrOpsChannelNotFound = infraerrors.NotFound("OPS_CHANNEL_NOT_FOUND", "channel not found")
	// ErrOpsChannelNotAutoDisabled 目标 Channel 不是「被运营管理自动停用」的，不能走 restore。
	ErrOpsChannelNotAutoDisabled = infraerrors.Conflict("OPS_CHANNEL_NOT_AUTO_DISABLED", "channel was not disabled by ops margin protection")
	// ErrOpsTaskNotFound 任务不存在或已过期。
	ErrOpsTaskNotFound = infraerrors.NotFound("OPS_TASK_NOT_FOUND", "ops task not found")
	// ErrOpsInvalidSetting 运营配置取值非法。
	ErrOpsInvalidSetting = infraerrors.BadRequest("OPS_INVALID_SETTING", "invalid ops setting")
	// ErrOpsInvalidMargin 最低利润率超出 0%–99% 区间。
	ErrOpsInvalidMargin = infraerrors.BadRequest("OPS_INVALID_MARGIN", "minimum margin must be between 0% and 99%")
)

// OpsUpstreamSite 上游站点。
//
// 密文字段只在服务内部与仓储之间流转；handler 输出 DTO 时必须脱敏
// （只回 password_set / upstream_api_key_set 布尔）。
type OpsUpstreamSite struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	BaseURL             string     `json:"base_url"`
	AuthMode            string     `json:"auth_mode"`
	Username            string     `json:"username"`
	PasswordEnc         string     `json:"-"`
	AccessTokenEnc      string     `json:"-"`
	RefreshTokenEnc     string     `json:"-"`
	TokenExpiresAt      *time.Time `json:"token_expires_at,omitempty"`
	UpstreamAPIKeyEnc   string     `json:"-"`
	Status              string     `json:"status"`
	LastSyncedAt        *time.Time `json:"last_synced_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	SyncIntervalMinutes int        `json:"sync_interval_minutes"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// HasPassword / HasUpstreamAPIKey 供 DTO 判断是否已配置凭据（不泄露内容）。
func (s *OpsUpstreamSite) HasPassword() bool {
	return s != nil && strings.TrimSpace(s.PasswordEnc) != ""
}

// HasUpstreamAPIKey 是否已配置该站点的上游普通 API Key。
func (s *OpsUpstreamSite) HasUpstreamAPIKey() bool {
	return s != nil && strings.TrimSpace(s.UpstreamAPIKeyEnc) != ""
}

// OpsUpstreamGroup 上游分组快照。
type OpsUpstreamGroup struct {
	ID                   int64     `json:"id"`
	SiteID               int64     `json:"site_id"`
	RemoteGroupID        int64     `json:"remote_group_id"`
	Name                 string    `json:"name"`
	Platform             string    `json:"platform,omitempty"`
	Ratio                float64   `json:"ratio"`
	RateMultiplier       float64   `json:"rate_multiplier"`
	PeakEnabled          bool      `json:"peak_enabled"`
	PeakStart            string    `json:"peak_start,omitempty"`
	PeakEnd              string    `json:"peak_end,omitempty"`
	PeakMultiplier       float64   `json:"peak_multiplier"`
	ImageSeparateRate    bool      `json:"image_separate_rate"`
	VideoSeparateRate    bool      `json:"video_separate_rate"`
	IsExclusive          bool      `json:"is_exclusive"`
	SubscriptionType     string    `json:"subscription_type,omitempty"`
	MinMarginBasisPoints *int64    `json:"min_margin_basis_points"`
	SyncedAt             time.Time `json:"synced_at"`

	// RateChanged / PreviousRateMultiplier 是同步时算出的「倍率变动」标记：
	// 只用于列表提示与触发评估，不落库。
	RateChanged            bool     `json:"rate_changed"`
	PreviousRateMultiplier *float64 `json:"previous_rate_multiplier,omitempty"`
}

// OpsUpstreamAccount 上游账号快照（password 模式下为空）。
type OpsUpstreamAccount struct {
	ID              int64          `json:"id"`
	SiteID          int64          `json:"site_id"`
	RemoteAccountID int64          `json:"remote_account_id"`
	Name            string         `json:"name,omitempty"`
	Platform        string         `json:"platform,omitempty"`
	Type            string         `json:"type,omitempty"`
	Status          string         `json:"status,omitempty"`
	Schedulable     *bool          `json:"schedulable,omitempty"`
	RateMultiplier  *float64       `json:"rate_multiplier,omitempty"`
	GroupIDs        []int64        `json:"group_ids"`
	Credentials     map[string]any `json:"-"`
	SyncedAt        time.Time      `json:"synced_at"`
}

// OpsProfitCheck 单次利润评估留痕（对齐 relaydeck ProfitCheck）。
type OpsProfitCheck struct {
	ID                   int64      `json:"id"`
	SiteID               int64      `json:"site_id"`
	RemoteGroupID        int64      `json:"remote_group_id"`
	GroupName            string     `json:"group_name,omitempty"`
	ChannelRef           string     `json:"channel_ref,omitempty"`
	SaleMultiplierMicros int64      `json:"sale_multiplier_micros"`
	CostMultiplierMicros int64      `json:"cost_multiplier_micros"`
	RevenueMicros        int64      `json:"revenue_micros"`
	CostMicros           int64      `json:"cost_micros"`
	MarginBasisPoints    int64      `json:"margin_basis_points"`
	Status               string     `json:"status"`
	CostSource           string     `json:"cost_source,omitempty"`
	CostObservedAt       *time.Time `json:"cost_observed_at,omitempty"`
	CostReliable         bool       `json:"cost_reliable"`
	CostReason           string     `json:"cost_reason,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
}

// OpsGuardLock 守护锁（幂等：同一 (site, target, lock_type) 只有一条 active）。
type OpsGuardLock struct {
	ID          int64          `json:"id"`
	SiteID      int64          `json:"site_id"`
	TargetRef   string         `json:"target_ref"`
	LockType    string         `json:"lock_type"`
	Active      bool           `json:"active"`
	Reason      string         `json:"reason,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	ActivatedAt time.Time      `json:"activated_at"`
	ClearedAt   *time.Time     `json:"cleared_at,omitempty"`
}

// OpsChannelLink 本地 Channel 上的运营标签（channels 扩展列的只读投影）。
type OpsChannelLink struct {
	ChannelID          int64      `json:"channel_id"`
	ChannelName        string     `json:"channel_name"`
	Status             string     `json:"status"`
	SiteID             *int64     `json:"site_id,omitempty"`
	RemoteGroupID      *int64     `json:"remote_group_id,omitempty"`
	UpstreamKeyHash    string     `json:"upstream_key_hash,omitempty"`
	AutoDisabledAt     *time.Time `json:"auto_disabled_at,omitempty"`
	AutoDisabledReason string     `json:"auto_disabled_reason,omitempty"`
	GroupIDs           []int64    `json:"group_ids"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// OpsLocalGroup 本地分组的最小只读投影（倍率与分组级最低利润率）。
type OpsLocalGroup struct {
	ID                            int64   `json:"id"`
	Name                          string  `json:"name"`
	Platform                      string  `json:"platform,omitempty"`
	RateMultiplier                float64 `json:"rate_multiplier"`
	OpsMinMarginBasisPoints       *int64  `json:"ops_min_margin_basis_points"`
	EffectiveMinMarginBasisPoints int64   `json:"effective_min_margin_basis_points"`
	MinMarginSource               string  `json:"min_margin_source"`
}

// OpsUsageTotals usage_logs 的窗口聚合（收入=actual_cost，成本=total_cost）。
type OpsUsageTotals struct {
	Revenue  float64    `json:"revenue"`
	Cost     float64    `json:"cost"`
	Requests int64      `json:"requests"`
	LastAt   *time.Time `json:"last_at,omitempty"`
}

// OpsUsageGroupTotals 按分组维度的窗口聚合（分组盈利榜）。
type OpsUsageGroupTotals struct {
	GroupID  int64   `json:"group_id"`
	Revenue  float64 `json:"revenue"`
	Cost     float64 `json:"cost"`
	Requests int64   `json:"requests"`
}

// OpsProfitCheckFilter 利润检查历史查询条件。
type OpsProfitCheckFilter struct {
	SiteID        int64
	RemoteGroupID int64
	ChannelRef    string
	Status        string
	Limit         int
	Offset        int
}

// OpsMgmtRepository 运营管理仓储（单实现 opsMgmtRepository）。
//
// 拆成多个小接口是为了让 service 层的单元测试可以用更小的伪造实现，
// 同时仍然保持 wire 只注入一个具体类型。
type OpsMgmtRepository interface {
	OpsSiteRepository
	OpsUpstreamGroupRepository
	OpsUpstreamAccountRepository
	OpsProfitCheckRepository
	OpsGuardLockRepository
	OpsSettingsRepository
	OpsChannelRepository
	OpsLocalGroupRepository
	OpsUsageStatsRepository
}

// OpsSiteRepository 站点持久化。
type OpsSiteRepository interface {
	ListSites(ctx context.Context) ([]OpsUpstreamSite, error)
	GetSite(ctx context.Context, id int64) (*OpsUpstreamSite, error)
	// SiteNameExists 判断站点名是否已被占用（excludeID>0 时排除自身）。
	SiteNameExists(ctx context.Context, name string, excludeID int64) (bool, error)
	CreateSite(ctx context.Context, site *OpsUpstreamSite) error
	UpdateSite(ctx context.Context, site *OpsUpstreamSite) error
	DeleteSite(ctx context.Context, id int64) error
	UpdateSiteSyncState(ctx context.Context, id int64, syncedAt *time.Time, lastErr string, status string) error
	UpdateSiteTokens(ctx context.Context, id int64, accessTokenEnc, refreshTokenEnc string, expiresAt *time.Time) error
}

// OpsUpstreamGroupRepository 上游分组快照持久化。
type OpsUpstreamGroupRepository interface {
	ListUpstreamGroups(ctx context.Context, siteID int64) ([]OpsUpstreamGroup, error)
	GetUpstreamGroup(ctx context.Context, siteID, remoteGroupID int64) (*OpsUpstreamGroup, error)
	// UpsertUpstreamGroups 以 (site_id, remote_group_id) 幂等写入快照，
	// 返回写入前的旧快照（用于计算倍率变动标记）。
	UpsertUpstreamGroups(ctx context.Context, siteID int64, groups []OpsUpstreamGroup) (map[int64]OpsUpstreamGroup, error)
	SetUpstreamGroupMinMargin(ctx context.Context, siteID, remoteGroupID int64, basisPoints *int64) error
}

// OpsUpstreamAccountRepository 上游账号快照持久化。
type OpsUpstreamAccountRepository interface {
	ListUpstreamAccounts(ctx context.Context, siteID int64) ([]OpsUpstreamAccount, error)
	// ReplaceUpstreamAccounts 以 (site_id, remote_account_id) 幂等写入并删除快照中已消失的账号。
	ReplaceUpstreamAccounts(ctx context.Context, siteID int64, accounts []OpsUpstreamAccount) error
}

// OpsProfitCheckRepository 利润检查历史。
type OpsProfitCheckRepository interface {
	AppendProfitCheck(ctx context.Context, check *OpsProfitCheck) error
	ListProfitChecks(ctx context.Context, filter OpsProfitCheckFilter) ([]OpsProfitCheck, int64, error)
	// ListRecentProfitChecks 返回某 (site, upstream group, channel) 最近 limit 条检查，按时间倒序。
	ListRecentProfitChecks(ctx context.Context, siteID, remoteGroupID int64, channelRef string, limit int) ([]OpsProfitCheck, error)
}

// OpsGuardLockRepository 守护锁。
type OpsGuardLockRepository interface {
	// ActivateGuardLock 幂等：已存在 active 锁时不新建，返回 created=false 与既有锁。
	ActivateGuardLock(ctx context.Context, lock *OpsGuardLock) (created bool, existing *OpsGuardLock, err error)
	ClearGuardLock(ctx context.Context, siteID int64, targetRef, lockType string) (bool, error)
	ListActiveGuardLocks(ctx context.Context, siteID int64) ([]OpsGuardLock, error)
	GetActiveGuardLock(ctx context.Context, siteID int64, targetRef, lockType string) (*OpsGuardLock, error)
}

// OpsSettingsRepository 运营配置（ops_settings.key + jsonb value）。
type OpsSettingsRepository interface {
	GetOpsSettings(ctx context.Context) (map[string]string, error)
	SetOpsSettings(ctx context.Context, values map[string]string) error
}

// OpsChannelRepository 本地 Channel 的运营标签与自动停用状态。
type OpsChannelRepository interface {
	SetChannelOpsLink(ctx context.Context, channelID, siteID, remoteGroupID int64, upstreamKeyEnc, upstreamKeyHash string) error
	GetChannelLink(ctx context.Context, channelID int64) (*OpsChannelLink, error)
	ListChannelLinksByUpstreamGroup(ctx context.Context, siteID, remoteGroupID int64) ([]OpsChannelLink, error)
	ListAutoDisabledChannels(ctx context.Context) ([]OpsChannelLink, error)
	FindChannelLinkByUpstreamKey(ctx context.Context, siteID, remoteGroupID int64, upstreamKeyHash string) (*OpsChannelLink, error)
	SetChannelAutoDisabled(ctx context.Context, channelID int64, at time.Time, reason string, disabled bool) error
}

// OpsLocalGroupRepository 本地分组的倍率与分组级最低利润率。
type OpsLocalGroupRepository interface {
	ListLocalGroups(ctx context.Context, ids []int64) ([]OpsLocalGroup, error)
	ListAllLocalGroups(ctx context.Context) ([]OpsLocalGroup, error)
	SetLocalGroupMinMargin(ctx context.Context, groupID int64, basisPoints *int64) error
	LocalGroupExists(ctx context.Context, groupID int64) (bool, error)
}

// OpsUsageStatsRepository usage_logs 只读聚合（利润数据源）。
type OpsUsageStatsRepository interface {
	SumUsageTotals(ctx context.Context, from, to time.Time, channelID *int64) (OpsUsageTotals, error)
	SumUsageByGroup(ctx context.Context, from, to time.Time) ([]OpsUsageGroupTotals, error)
	CountUsageDays(ctx context.Context, from, to time.Time) (int, error)
}
