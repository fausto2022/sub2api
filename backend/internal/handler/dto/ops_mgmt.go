package dto

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 运营管理（design.md 第二部分 v3.0）的 HTTP 契约对象。
//
// 脱敏约定：
//   - 上游密码、access/refresh token、上游 API Key 一律**不返回**，只用
//     password_set / upstream_api_key_set 布尔表达「是否已配置」；
//   - 站点列表与详情都不含任何密文，避免日志/响应泄露（design 第六部分风险表）。
type OpsSite struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	BaseURL             string     `json:"base_url"`
	AuthMode            string     `json:"auth_mode"`
	Username            string     `json:"username"`
	PasswordSet         bool       `json:"password_set"`
	UpstreamAPIKeySet   bool       `json:"upstream_api_key_set"`
	Status              string     `json:"status"`
	LastSyncedAt        *time.Time `json:"last_synced_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	SyncIntervalMinutes int        `json:"sync_interval_minutes"`
	TokenExpiresAt      *time.Time `json:"token_expires_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// OpsSiteFromService 把站点模型转成脱敏 DTO。
func OpsSiteFromService(site *service.OpsUpstreamSite) *OpsSite {
	if site == nil {
		return nil
	}
	return &OpsSite{
		ID:                  site.ID,
		Name:                site.Name,
		BaseURL:             site.BaseURL,
		AuthMode:            site.AuthMode,
		Username:            site.Username,
		PasswordSet:         site.HasPassword(),
		UpstreamAPIKeySet:   site.HasUpstreamAPIKey(),
		Status:              site.Status,
		LastSyncedAt:        site.LastSyncedAt,
		LastError:           site.LastError,
		SyncIntervalMinutes: site.SyncIntervalMinutes,
		TokenExpiresAt:      site.TokenExpiresAt,
		CreatedAt:           site.CreatedAt,
		UpdatedAt:           site.UpdatedAt,
	}
}

// OpsSitesFromService 批量转换。
func OpsSitesFromService(sites []service.OpsUpstreamSite) []OpsSite {
	out := make([]OpsSite, 0, len(sites))
	for i := range sites {
		if site := OpsSiteFromService(&sites[i]); site != nil {
			out = append(out, *site)
		}
	}
	return out
}

// OpsSiteRequest 新增/编辑上游站点。
//
// 编辑时 Password / UpstreamAPIKey 留空表示「保持不变」，
// clear_upstream_api_key=true 表示清空站点级上游 Key。
type OpsSiteRequest struct {
	Name                string `json:"name"`
	BaseURL             string `json:"base_url"`
	AuthMode            string `json:"auth_mode"`
	Username            string `json:"username"`
	Password            string `json:"password"`
	UpstreamAPIKey      string `json:"upstream_api_key"`
	SyncIntervalMinutes int    `json:"sync_interval_minutes"`
	ClearUpstreamAPIKey bool   `json:"clear_upstream_api_key"`
}

// ToService 转为服务层入参。
func (r OpsSiteRequest) ToService() service.OpsSiteInput {
	return service.OpsSiteInput{
		Name:                r.Name,
		BaseURL:             r.BaseURL,
		AuthMode:            r.AuthMode,
		Username:            r.Username,
		Password:            r.Password,
		UpstreamAPIKey:      r.UpstreamAPIKey,
		SyncIntervalMinutes: r.SyncIntervalMinutes,
		ClearUpstreamAPIKey: r.ClearUpstreamAPIKey,
	}
}

// OpsExpandItem 单个对接请求（扩池 = 对接渠道）。
type OpsExpandItem struct {
	SiteID         int64    `json:"site_id"`
	RemoteGroupID  int64    `json:"remote_group_id"`
	UpstreamAPIKey string   `json:"upstream_api_key"`
	TargetGroupID  int64    `json:"target_group_id"`
	ChannelName    string   `json:"channel_name,omitempty"`
	Description    string   `json:"description,omitempty"`
	RateMultiplier *float64 `json:"rate_multiplier,omitempty"`
}

// OpsExpandRequest 批量/单个对接请求：给 items 走异步任务，否则按单条同步执行。
type OpsExpandRequest struct {
	OpsExpandItem
	Items []OpsExpandItem `json:"items,omitempty"`
}

// ToService 把单条请求转为服务层入参。
func (i OpsExpandItem) ToService() service.OpsExpandInput {
	return service.OpsExpandInput{
		SiteID:         i.SiteID,
		RemoteGroupID:  i.RemoteGroupID,
		UpstreamAPIKey: i.UpstreamAPIKey,
		TargetGroupID:  i.TargetGroupID,
		ChannelName:    i.ChannelName,
		Description:    i.Description,
	}
}

// OpsSettingsRequest 运营配置（局部更新语义：字段缺省即保持不变）。
type OpsSettingsRequest struct {
	AutoMarginProtection     *bool  `json:"auto_margin_protection"`
	AutoRecovery             *bool  `json:"auto_recovery"`
	MinimumMarginBasisPoints *int64 `json:"minimum_margin_basis_points"`
	RiskConfirmations        *int   `json:"risk_confirmations"`
	CostMaxAgeMinutes        *int   `json:"cost_max_age_minutes"`
	GuaranteedRevenueRatioBP *int64 `json:"guaranteed_revenue_ratio_bp"`
	SyncIntervalMinutes      *int   `json:"sync_interval_minutes"`
	ProfitWindowHours        *int   `json:"profit_window_hours"`
}

// Apply 把请求中显式给出的字段覆盖到 base 上。
func (r OpsSettingsRequest) Apply(base service.OpsMgmtSettings) service.OpsMgmtSettings {
	if r.AutoMarginProtection != nil {
		base.AutoMarginProtection = *r.AutoMarginProtection
	}
	if r.AutoRecovery != nil {
		base.AutoRecovery = *r.AutoRecovery
	}
	if r.MinimumMarginBasisPoints != nil {
		base.MinimumMarginBasisPoints = *r.MinimumMarginBasisPoints
	}
	if r.RiskConfirmations != nil {
		base.RiskConfirmations = *r.RiskConfirmations
	}
	if r.CostMaxAgeMinutes != nil {
		base.CostMaxAgeMinutes = *r.CostMaxAgeMinutes
	}
	if r.GuaranteedRevenueRatioBP != nil {
		base.GuaranteedRevenueRatioBP = *r.GuaranteedRevenueRatioBP
	}
	if r.SyncIntervalMinutes != nil {
		base.SyncIntervalMinutes = *r.SyncIntervalMinutes
	}
	if r.ProfitWindowHours != nil {
		base.ProfitWindowHours = *r.ProfitWindowHours
	}
	return base
}

// OpsGroupMarginRequest 分组级最低利润率；basis_points=null 表示继承全局。
type OpsGroupMarginRequest struct {
	BasisPoints *int64 `json:"basis_points"`
	// MinMarginBasisPoints 兼容前端的另一种键名（与 groups 接口命名一致）。
	MinMarginBasisPoints *int64 `json:"min_margin_basis_points"`
}

// Resolve 取请求里的目标值。
func (r OpsGroupMarginRequest) Resolve() *int64 {
	if r.BasisPoints != nil {
		return r.BasisPoints
	}
	return r.MinMarginBasisPoints
}
