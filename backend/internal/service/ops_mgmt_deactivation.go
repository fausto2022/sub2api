package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 利润评估与自动停用（design.md §2.5.2，移植 relaydeck EvaluatePool 的核心循环）。
//
// 每次评估对每个「上游分组 × 已对接的本地 Channel」跑一遍：
//
//  1. resolvedCost = 上游分组倍率快照（带 Source/Observed/ExpiresAt/Reliable）
//  2. buildProfitCheck → healthy / risk / unsupported / unknown
//  3. 连续 risk 达 risk_confirmations 次才确认（防抖）
//  4. AutoMarginProtection=true 时真停用本地 Channel 并加 GuardLock（幂等）；
//     =false（默认灰度）时只记录 wouldDisable 并告警
//  5. AutoRecovery=true 且连续 healthy 达确认次数时解锁并恢复 Channel
//
// 成本不可靠（快照过期/缺失）时状态为 unknown，**不动作**，避免误停用。
type OpsEvaluationResult struct {
	SiteID            int64            `json:"site_id"`
	EvaluatedAt       time.Time        `json:"evaluated_at"`
	Source            string           `json:"source"`
	Policy            marginPolicy     `json:"policy"`
	Checks            []OpsProfitCheck `json:"checks"`
	Healthy           int              `json:"healthy"`
	Risk              int              `json:"risk"`
	Unsupported       int              `json:"unsupported"`
	Unknown           int              `json:"unknown"`
	WouldDisable      []string         `json:"would_disable"`
	ProtectionApplied []string         `json:"protection_applied"`
	Recovered         []string         `json:"recovered"`
	Warnings          []string         `json:"warnings,omitempty"`
	GrayMode          bool             `json:"gray_mode"`
}

// OpsEvaluationSummary 评估结果的轻量摘要（任务结果/同步结果内嵌）。
type OpsEvaluationSummary struct {
	Checks       int `json:"checks"`
	Healthy      int `json:"healthy"`
	Risk         int `json:"risk"`
	Unsupported  int `json:"unsupported"`
	Unknown      int `json:"unknown"`
	WouldDisable int `json:"would_disable"`
	Applied      int `json:"applied"`
	Recovered    int `json:"recovered"`
}

// Summary 生成轻量摘要。
func (r *OpsEvaluationResult) Summary() OpsEvaluationSummary {
	if r == nil {
		return OpsEvaluationSummary{}
	}
	return OpsEvaluationSummary{
		Checks:       len(r.Checks),
		Healthy:      r.Healthy,
		Risk:         r.Risk,
		Unsupported:  r.Unsupported,
		Unknown:      r.Unknown,
		WouldDisable: len(r.WouldDisable),
		Applied:      len(r.ProtectionApplied),
		Recovered:    len(r.Recovered),
	}
}

// Evaluate 评估（siteID<=0 表示全部站点）。
func (s *OpsManagementService) Evaluate(ctx context.Context, siteID int64, source string) (*OpsEvaluationResult, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	if siteID > 0 {
		return s.EvaluateSite(ctx, siteID, source)
	}
	sites, err := s.repo.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	merged := &OpsEvaluationResult{Checks: []OpsProfitCheck{}, WouldDisable: []string{}, ProtectionApplied: []string{}, Recovered: []string{}}
	for i := range sites {
		result, evalErr := s.EvaluateSite(ctx, sites[i].ID, source)
		if evalErr != nil {
			merged.Warnings = append(merged.Warnings, fmt.Sprintf("site %d: %v", sites[i].ID, evalErr))
			continue
		}
		merged.EvaluatedAt = result.EvaluatedAt
		merged.Policy = result.Policy
		merged.GrayMode = result.GrayMode
		merged.Checks = append(merged.Checks, result.Checks...)
		merged.Healthy += result.Healthy
		merged.Risk += result.Risk
		merged.Unsupported += result.Unsupported
		merged.Unknown += result.Unknown
		merged.WouldDisable = append(merged.WouldDisable, result.WouldDisable...)
		merged.ProtectionApplied = append(merged.ProtectionApplied, result.ProtectionApplied...)
		merged.Recovered = append(merged.Recovered, result.Recovered...)
		merged.Warnings = append(merged.Warnings, result.Warnings...)
	}
	return merged, nil
}

// EvaluateSite 评估单个站点。
func (s *OpsManagementService) EvaluateSite(ctx context.Context, siteID int64, source string) (*OpsEvaluationResult, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	if source == "" {
		source = "manual"
	}
	settings, err := s.settings.Get(ctx)
	if err != nil {
		settings = DefaultOpsMgmtSettings()
	}
	policy := settings.marginPolicy()
	now := s.now()

	site, err := s.repo.GetSite(ctx, siteID)
	if err != nil {
		return nil, err
	}
	result := &OpsEvaluationResult{
		SiteID:            site.ID,
		EvaluatedAt:       now,
		Source:            source,
		Policy:            policy,
		Checks:            []OpsProfitCheck{},
		WouldDisable:      []string{},
		ProtectionApplied: []string{},
		Recovered:         []string{},
		GrayMode:          !settings.AutoMarginProtection,
	}

	upstreamGroups, err := s.repo.ListUpstreamGroups(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	for i := range upstreamGroups {
		group := upstreamGroups[i]
		cost := opsResolveCostMultiplier(&group, policy, now)
		unsupported, unsupportedReason := opsUnsupportedPricing(&group)
		links, linksErr := s.repo.ListChannelLinksByUpstreamGroup(ctx, site.ID, group.RemoteGroupID)
		if linksErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("list channels for upstream group %d: %v", group.RemoteGroupID, linksErr))
			continue
		}
		if len(links) == 0 {
			// 该上游分组还没对接本地 Channel：没有控制对象，不产生检查（避免噪音）。
			continue
		}
		for j := range links {
			link := links[j]
			check, buildErr := s.buildChannelProfitCheck(ctx, site, &group, &link, cost, unsupported, unsupportedReason, policy, settings, now)
			if buildErr != nil {
				result.Warnings = append(result.Warnings, buildErr.Error())
				continue
			}
			if err := s.repo.AppendProfitCheck(ctx, check); err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("append profit check: %v", err))
				continue
			}
			result.Checks = append(result.Checks, *check)
			switch check.Status {
			case OpsProfitStatusHealthy:
				result.Healthy++
			case OpsProfitStatusRisk:
				result.Risk++
			case OpsProfitStatusUnsupported:
				result.Unsupported++
			default:
				result.Unknown++
			}
			s.applyProtectionDecision(ctx, settings, policy, now, site, check, &link, result)
		}
	}
	return result, nil
}

// buildChannelProfitCheck 组装单个 Channel 的检查（包含金额口径与倍率口径）。
func (s *OpsManagementService) buildChannelProfitCheck(
	ctx context.Context,
	site *OpsUpstreamSite,
	group *OpsUpstreamGroup,
	link *OpsChannelLink,
	cost resolvedCost,
	unsupported bool,
	unsupportedReason string,
	policy marginPolicy,
	settings OpsMgmtSettings,
	now time.Time,
) (*OpsProfitCheck, error) {
	if link == nil {
		return nil, fmt.Errorf("channel link is nil")
	}
	if len(link.GroupIDs) == 0 {
		return nil, fmt.Errorf("channel %d has no local group bound; skip margin check", link.ChannelID)
	}
	localGroups, err := s.repo.ListLocalGroups(ctx, link.GroupIDs)
	if err != nil {
		return nil, fmt.Errorf("load local group for channel %d: %w", link.ChannelID, err)
	}
	if len(localGroups) == 0 {
		return nil, fmt.Errorf("local group of channel %d is missing", link.ChannelID)
	}
	localGroup := localGroups[0]
	effectiveMin, _ := resolveEffectiveMinMargin(group.MinMarginBasisPoints, localGroup.OpsMinMarginBasisPoints, settings.MinimumMarginBasisPoints)
	groupPolicy := policy
	groupPolicy.MinimumMarginBasisPoints = effectiveMin

	channelID := link.ChannelID
	window := time.Duration(settings.ProfitWindowHours) * time.Hour
	if window <= 0 {
		window = time.Duration(opsDefaultProfitWindowHours) * time.Hour
	}
	var revenueMicros, costMicros int64
	if totals, totalsErr := s.repo.SumUsageTotals(ctx, now.Add(-window), now, &channelID); totalsErr == nil {
		revenueMicros = opsMicrosFromFloat(totals.Revenue)
		costMicros = opsMicrosFromFloat(totals.Cost)
	} else {
		logger.LegacyPrintf("service.ops_mgmt", "Warning: sum usage for channel %d failed: %v", channelID, totalsErr)
	}

	check := buildProfitCheck(opsProfitCheckInput{
		SiteID:            site.ID,
		RemoteGroupID:     group.RemoteGroupID,
		GroupName:         group.Name,
		ChannelRef:        opsChannelRef(link.ChannelID),
		SaleMicros:        opsToMicros(localGroup.RateMultiplier),
		Cost:              cost,
		Policy:            groupPolicy,
		RevenueMicros:     revenueMicros,
		CostMicros:        costMicros,
		Unsupported:       unsupported,
		UnsupportedReason: unsupportedReason,
		Now:               now,
	})
	return &check, nil
}

// applyProtectionDecision 依据检查结果决定「停用 / 只告警 / 恢复」。
func (s *OpsManagementService) applyProtectionDecision(
	ctx context.Context,
	settings OpsMgmtSettings,
	policy marginPolicy,
	now time.Time,
	site *OpsUpstreamSite,
	check *OpsProfitCheck,
	link *OpsChannelLink,
	result *OpsEvaluationResult,
) {
	if check == nil || link == nil || result == nil {
		return
	}
	ref := opsChannelRef(link.ChannelID)
	recent, err := s.repo.ListRecentProfitChecks(ctx, site.ID, check.RemoteGroupID, check.ChannelRef, policy.RiskConfirmations)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("load recent profit checks for %s: %v", ref, err))
		return
	}

	if check.Status == OpsProfitStatusRisk {
		result.WouldDisable = append(result.WouldDisable, ref)
		if !marginRiskConfirmed(recent, policy) {
			// 防抖：还没连续确认够次数，只记录不动作。
			return
		}
		if !settings.AutoMarginProtection {
			// 灰度模式：只记录 wouldDisable 并告警，不真停用。
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("gray mode: %s would be disabled (margin %d bps < %d bps, confirmed %d times)",
					ref, check.MarginBasisPoints, policy.MinimumMarginBasisPoints, policy.RiskConfirmations))
			logger.LegacyPrintf("service.ops_mgmt",
				"Warning: gray mode would disable channel %d (site %d, group %d): margin %d bps < %d bps",
				link.ChannelID, site.ID, check.RemoteGroupID, check.MarginBasisPoints, policy.MinimumMarginBasisPoints)
			return
		}
		if _, _, lockErr := s.ActivateGuardLock(ctx, site.ID, ref, OpsGuardLockTypeMargin,
			"profit margin remained insufficient", map[string]any{
				"channel_id":                  link.ChannelID,
				"remote_group_id":             check.RemoteGroupID,
				"minimum_margin_basis_points": policy.MinimumMarginBasisPoints,
				"margin_basis_points":         check.MarginBasisPoints,
				"risk_confirmations":          policy.RiskConfirmations,
			}); lockErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("activate guard lock for %s: %v", ref, lockErr))
			return
		}
		if link.AutoDisabledAt == nil {
			if disableErr := s.repo.SetChannelAutoDisabled(ctx, link.ChannelID, now, OpsAutoDisabledReasonMargin, true); disableErr != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("disable channel %d: %v", link.ChannelID, disableErr))
				return
			}
			if s.channelSvc != nil {
				s.channelSvc.InvalidateCache()
			}
			result.ProtectionApplied = append(result.ProtectionApplied, ref)
		}
		return
	}

	// 恢复路径：只有「曾被自动停用」且连续 healthy 达确认次数才恢复。
	if link.AutoDisabledAt == nil || !settings.AutoRecovery {
		return
	}
	if !marginRecoveryConfirmed(recent, policy) {
		return
	}
	if _, clearErr := s.repo.ClearGuardLock(ctx, site.ID, ref, OpsGuardLockTypeMargin); clearErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("clear guard lock for %s: %v", ref, clearErr))
		return
	}
	if restoreErr := s.repo.SetChannelAutoDisabled(ctx, link.ChannelID, now, "", false); restoreErr != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("restore channel %d: %v", link.ChannelID, restoreErr))
		return
	}
	if s.channelSvc != nil {
		s.channelSvc.InvalidateCache()
	}
	result.Recovered = append(result.Recovered, ref)
}

// ListProfitChecks 利润检查历史。
func (s *OpsManagementService) ListProfitChecks(ctx context.Context, filter OpsProfitCheckFilter) ([]OpsProfitCheck, int64, error) {
	if s == nil || s.repo == nil {
		return nil, 0, fmt.Errorf("ops service is not configured")
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return s.repo.ListProfitChecks(ctx, filter)
}

// ListActiveGuardLocks 列出站点下 active 守护锁（审计视图）。
func (s *OpsManagementService) ListActiveGuardLocks(ctx context.Context, siteID int64) ([]OpsGuardLock, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	return s.repo.ListActiveGuardLocks(ctx, siteID)
}
