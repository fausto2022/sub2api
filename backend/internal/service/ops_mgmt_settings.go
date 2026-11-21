package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 运营配置键（ops_settings.key，value 为 jsonb）。
const (
	// OpsSettingAutoMarginProtection 自动利润保护灰度开关：false（默认）时只记录
	// wouldDisable 并告警，不真停用；true 时才真正停用本地 Channel。
	OpsSettingAutoMarginProtection = "auto_margin_protection"
	// OpsSettingAutoRecovery 利润率恢复后自动解锁并恢复 Channel。
	OpsSettingAutoRecovery = "auto_recovery"
	// OpsSettingMinimumMarginBasisPoints 全局最低利润率（基点，默认 1000 = 10%）。
	OpsSettingMinimumMarginBasisPoints = "minimum_margin_basis_points"
	// OpsSettingRiskConfirmations 连续 N 次 risk 才动作（防抖，默认 2）。
	OpsSettingRiskConfirmations = "risk_confirmations"
	// OpsSettingCostMaxAgeMinutes 成本数据最大时效（分钟，默认 60）。
	OpsSettingCostMaxAgeMinutes = "cost_max_age_minutes"
	// OpsSettingGuaranteedRevenueRatioBP 保底收入比例（基点，默认 10000 = 不打折）。
	OpsSettingGuaranteedRevenueRatioBP = "guaranteed_revenue_ratio_bp"
	// OpsSettingSyncIntervalMinutes 默认同步间隔（分钟，默认 30）。
	OpsSettingSyncIntervalMinutes = "sync_interval_minutes"
	// OpsSettingProfitWindowHours 利润评估的用量回看窗口（小时，默认 24）。
	OpsSettingProfitWindowHours = "profit_window_hours"
)

const (
	// opsDefaultMinimumMarginBasisPoints 默认最低利润率 10%。
	opsDefaultMinimumMarginBasisPoints int64 = 1000
	// opsMaxMarginBasisPoints 最低利润率上限 99%（与 relaydeck 一致）。
	opsMaxMarginBasisPoints int64 = 9900
	// opsDefaultRiskConfirmations 连续风险确认次数。
	opsDefaultRiskConfirmations = 2
	// opsDefaultCostMaxAgeMinutes 成本时效。
	opsDefaultCostMaxAgeMinutes = 60
	// opsDefaultGuaranteedRevenueRatioBP 100% 保底。
	opsDefaultGuaranteedRevenueRatioBP int64 = 10000
	// opsDefaultSyncIntervalMinutes 默认 30 分钟同步一次。
	opsDefaultSyncIntervalMinutes = 30
	// opsDefaultProfitWindowHours 利润评估默认回看 24 小时用量。
	opsDefaultProfitWindowHours = 24
	// opsSettingsCacheTTL 配置读取缓存，避免每次评估都打库。
	opsSettingsCacheTTL = 15 * time.Second
)

// OpsMgmtSettings 运营管理全局配置。
type OpsMgmtSettings struct {
	AutoMarginProtection     bool  `json:"auto_margin_protection"`
	AutoRecovery             bool  `json:"auto_recovery"`
	MinimumMarginBasisPoints int64 `json:"minimum_margin_basis_points"`
	RiskConfirmations        int   `json:"risk_confirmations"`
	CostMaxAgeMinutes        int   `json:"cost_max_age_minutes"`
	GuaranteedRevenueRatioBP int64 `json:"guaranteed_revenue_ratio_bp"`
	SyncIntervalMinutes      int   `json:"sync_interval_minutes"`
	ProfitWindowHours        int   `json:"profit_window_hours"`
}

// DefaultOpsMgmtSettings 返回设计文档约定的默认值：
// AutoMarginProtection=false（灰度，只告警）、AutoRecovery=true（自动恢复）。
func DefaultOpsMgmtSettings() OpsMgmtSettings {
	return OpsMgmtSettings{
		AutoMarginProtection:     false,
		AutoRecovery:             true,
		MinimumMarginBasisPoints: opsDefaultMinimumMarginBasisPoints,
		RiskConfirmations:        opsDefaultRiskConfirmations,
		CostMaxAgeMinutes:        opsDefaultCostMaxAgeMinutes,
		GuaranteedRevenueRatioBP: opsDefaultGuaranteedRevenueRatioBP,
		SyncIntervalMinutes:      opsDefaultSyncIntervalMinutes,
		ProfitWindowHours:        opsDefaultProfitWindowHours,
	}
}

// validate 校验取值区间（越界返回 ErrOpsInvalidSetting / ErrOpsInvalidMargin）。
func (s OpsMgmtSettings) validate() error {
	if s.MinimumMarginBasisPoints < 0 || s.MinimumMarginBasisPoints > opsMaxMarginBasisPoints {
		return ErrOpsInvalidMargin
	}
	if s.RiskConfirmations <= 0 || s.RiskConfirmations > 50 {
		return fmt.Errorf("%w: risk_confirmations must be within 1..50", ErrOpsInvalidSetting)
	}
	if s.CostMaxAgeMinutes <= 0 || s.CostMaxAgeMinutes > 7*24*60 {
		return fmt.Errorf("%w: cost_max_age_minutes must be within 1..10080", ErrOpsInvalidSetting)
	}
	if s.GuaranteedRevenueRatioBP <= 0 || s.GuaranteedRevenueRatioBP > 100000 {
		return fmt.Errorf("%w: guaranteed_revenue_ratio_bp must be within 1..100000", ErrOpsInvalidSetting)
	}
	if s.SyncIntervalMinutes <= 0 || s.SyncIntervalMinutes > 7*24*60 {
		return fmt.Errorf("%w: sync_interval_minutes must be within 1..10080", ErrOpsInvalidSetting)
	}
	if s.ProfitWindowHours <= 0 || s.ProfitWindowHours > 30*24 {
		return fmt.Errorf("%w: profit_window_hours must be within 1..720", ErrOpsInvalidSetting)
	}
	return nil
}

// marginPolicy 移植自 relaydeck：利润率策略。
type marginPolicy struct {
	Mode                     string `json:"mode"`
	MinimumMarginBasisPoints int64  `json:"minimum_margin_basis_points"`
	RiskConfirmations        int    `json:"risk_confirmations"`
	CostMaxAgeMinutes        int    `json:"cost_max_age_minutes"`
}

// marginPolicy 由全局配置构造（Mode=protect/observe 只是审计标签）。
func (s OpsMgmtSettings) marginPolicy() marginPolicy {
	policy := marginPolicy{
		Mode:                     "observe",
		MinimumMarginBasisPoints: s.MinimumMarginBasisPoints,
		RiskConfirmations:        s.RiskConfirmations,
		CostMaxAgeMinutes:        s.CostMaxAgeMinutes,
	}
	if s.AutoMarginProtection {
		policy.Mode = "protect"
	}
	if policy.RiskConfirmations <= 0 {
		policy.RiskConfirmations = opsDefaultRiskConfirmations
	}
	if policy.CostMaxAgeMinutes <= 0 {
		policy.CostMaxAgeMinutes = opsDefaultCostMaxAgeMinutes
	}
	return policy
}

// opsParsedSettings 把 ops_settings 的字符串值解析为强类型配置。
func opsParseSettings(raw map[string]string) OpsMgmtSettings {
	settings := DefaultOpsMgmtSettings()
	for key, value := range raw {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		switch key {
		case OpsSettingAutoMarginProtection:
			if parsed, ok := opsParseBool(trimmed); ok {
				settings.AutoMarginProtection = parsed
			}
		case OpsSettingAutoRecovery:
			if parsed, ok := opsParseBool(trimmed); ok {
				settings.AutoRecovery = parsed
			}
		case OpsSettingMinimumMarginBasisPoints:
			if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
				settings.MinimumMarginBasisPoints = parsed
			}
		case OpsSettingRiskConfirmations:
			if parsed, err := strconv.Atoi(trimmed); err == nil {
				settings.RiskConfirmations = parsed
			}
		case OpsSettingCostMaxAgeMinutes:
			if parsed, err := strconv.Atoi(trimmed); err == nil {
				settings.CostMaxAgeMinutes = parsed
			}
		case OpsSettingGuaranteedRevenueRatioBP:
			if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
				settings.GuaranteedRevenueRatioBP = parsed
			}
		case OpsSettingSyncIntervalMinutes:
			if parsed, err := strconv.Atoi(trimmed); err == nil {
				settings.SyncIntervalMinutes = parsed
			}
		case OpsSettingProfitWindowHours:
			if parsed, err := strconv.Atoi(trimmed); err == nil {
				settings.ProfitWindowHours = parsed
			}
		}
	}
	// 归一化：越界值回落默认，避免坏配置把保护关死。
	if settings.MinimumMarginBasisPoints < 0 || settings.MinimumMarginBasisPoints > opsMaxMarginBasisPoints {
		settings.MinimumMarginBasisPoints = opsDefaultMinimumMarginBasisPoints
	}
	if settings.RiskConfirmations <= 0 {
		settings.RiskConfirmations = opsDefaultRiskConfirmations
	}
	if settings.CostMaxAgeMinutes <= 0 {
		settings.CostMaxAgeMinutes = opsDefaultCostMaxAgeMinutes
	}
	if settings.GuaranteedRevenueRatioBP <= 0 {
		settings.GuaranteedRevenueRatioBP = opsDefaultGuaranteedRevenueRatioBP
	}
	if settings.SyncIntervalMinutes <= 0 {
		settings.SyncIntervalMinutes = opsDefaultSyncIntervalMinutes
	}
	if settings.ProfitWindowHours <= 0 {
		settings.ProfitWindowHours = opsDefaultProfitWindowHours
	}
	return settings
}

func opsParseBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// opsSettingsToMap 把配置序列化为可写入 ops_settings 的 jsonb 文本。
func opsSettingsToMap(settings OpsMgmtSettings) map[string]string {
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			return "null"
		}
		return string(raw)
	}
	return map[string]string{
		OpsSettingAutoMarginProtection:     encode(settings.AutoMarginProtection),
		OpsSettingAutoRecovery:             encode(settings.AutoRecovery),
		OpsSettingMinimumMarginBasisPoints: encode(settings.MinimumMarginBasisPoints),
		OpsSettingRiskConfirmations:        encode(settings.RiskConfirmations),
		OpsSettingCostMaxAgeMinutes:        encode(settings.CostMaxAgeMinutes),
		OpsSettingGuaranteedRevenueRatioBP: encode(settings.GuaranteedRevenueRatioBP),
		OpsSettingSyncIntervalMinutes:      encode(settings.SyncIntervalMinutes),
		OpsSettingProfitWindowHours:        encode(settings.ProfitWindowHours),
	}
}

// opsSettingsCache 运营配置读取缓存（15s TTL，避免评估循环反复打库）。
type opsSettingsCache struct {
	repo OpsSettingsRepository
	now  func() time.Time

	mu        sync.RWMutex
	cached    *OpsMgmtSettings
	expiresAt time.Time
}

func newOpsSettingsCache(repo OpsSettingsRepository) *opsSettingsCache {
	return &opsSettingsCache{repo: repo, now: time.Now}
}

func (c *opsSettingsCache) Get(ctx context.Context) (OpsMgmtSettings, error) {
	if c == nil || c.repo == nil {
		return DefaultOpsMgmtSettings(), nil
	}
	now := c.now()
	c.mu.RLock()
	cached := c.cached
	expiresAt := c.expiresAt
	c.mu.RUnlock()
	if cached != nil && now.Before(expiresAt) {
		return *cached, nil
	}
	raw, err := c.repo.GetOpsSettings(ctx)
	if err != nil {
		return DefaultOpsMgmtSettings(), err
	}
	settings := opsParseSettings(raw)
	c.mu.Lock()
	c.cached = &settings
	c.expiresAt = now.Add(opsSettingsCacheTTL)
	c.mu.Unlock()
	return settings, nil
}

// Set 校验后写入并失效缓存。
func (c *opsSettingsCache) Set(ctx context.Context, settings OpsMgmtSettings) error {
	if c == nil || c.repo == nil {
		return fmt.Errorf("ops settings repository is not configured")
	}
	if err := settings.validate(); err != nil {
		return err
	}
	if err := c.repo.SetOpsSettings(ctx, opsSettingsToMap(settings)); err != nil {
		return err
	}
	c.mu.Lock()
	c.cached = nil
	c.expiresAt = time.Time{}
	c.mu.Unlock()
	return nil
}

// Invalidate 清空缓存（测试与显式刷新用）。
func (c *opsSettingsCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.cached = nil
	c.expiresAt = time.Time{}
	c.mu.Unlock()
}

// resolveEffectiveMinMargin 按「上游分组 > 本地分组 > 全局默认」取出最低利润率（design §2.5.1）。
func resolveEffectiveMinMargin(upstream *int64, local *int64, global int64) (int64, string) {
	if upstream != nil {
		return *upstream, "upstream_group"
	}
	if local != nil {
		return *local, "local_group"
	}
	return global, "global"
}

// opsSHA256Hex 计算 SHA-256 十六进制（上游 Key 去重/审计用，不存明文）。
func opsSHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
