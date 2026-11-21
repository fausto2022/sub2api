package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// OpsManagementService 运营管理门面：站点 CRUD、同步编排、扩池、利润评估、
// 守护锁、看板、配置与分组级最低利润率。
//
// 依赖注入刻意保持「一个仓储 + 若干可选协作者」的形态：
//   - repo / client / settings 必需；
//   - encryptor 用于上游密码与 Key 的加密（缺失时拒绝写入凭据，而不是存明文）；
//   - channelService 用于扩池创建 Channel（缺失时扩池返回错误）；
//   - dashboard 用于看板聚合（可独立构造，测试可只测 dashboard）。
type OpsManagementService struct {
	repo      OpsMgmtRepository
	encryptor SecretEncryptor
	client    *OpsUpstreamClient
	settings  *opsSettingsCache
	dashboard *OpsDashboardService

	channelSvc *ChannelService
	syncSvc    *OpsSyncService
	tasks      *opsTaskRegistry

	now func() time.Time

	schedulerMu      sync.Mutex
	schedulerStarted bool
}

// NewOpsManagementService 构造运营管理服务。
func NewOpsManagementService(
	repo OpsMgmtRepository,
	encryptor SecretEncryptor,
	client *OpsUpstreamClient,
	channelSvc *ChannelService,
) *OpsManagementService {
	if client == nil {
		client = NewOpsUpstreamClient(nil)
	}
	settings := newOpsSettingsCache(repoSettings(repo))
	svc := &OpsManagementService{
		repo:       repo,
		encryptor:  encryptor,
		client:     client,
		settings:   settings,
		channelSvc: channelSvc,
		tasks:      newOpsTaskRegistry(),
		now:        time.Now,
	}
	if usageRepo, ok := repo.(OpsUsageStatsRepository); ok {
		svc.dashboard = NewOpsDashboardService(usageRepo, repoLocalGroups(repo), settings)
	}
	svc.syncSvc = newOpsSyncService(repo, client, svc, settings)
	return svc
}

// repoSettings 把复合仓储收窄为配置仓储（nil 安全）。
func repoSettings(repo OpsMgmtRepository) OpsSettingsRepository {
	if repo == nil {
		return nil
	}
	return repo
}

// repoLocalGroups 把复合仓储收窄为本地分组仓储（nil 安全）。
func repoLocalGroups(repo OpsMgmtRepository) OpsLocalGroupRepository {
	if repo == nil {
		return nil
	}
	return repo
}

// SetClock 注入时钟（测试用）。会同步传给同步服务与看板。
func (s *OpsManagementService) SetClock(now func() time.Time) {
	if s == nil || now == nil {
		return
	}
	s.now = now
	if s.dashboard != nil {
		s.dashboard.SetClock(now, nil)
	}
	if s.syncSvc != nil {
		s.syncSvc.now = now
	}
	if s.settings != nil {
		s.settings.now = now
	}
}

// Settings 暴露配置读取（供 handler 使用）。
func (s *OpsManagementService) Settings(ctx context.Context) (OpsMgmtSettings, error) {
	if s == nil || s.settings == nil {
		return DefaultOpsMgmtSettings(), nil
	}
	return s.settings.Get(ctx)
}

// UpdateSettings 更新运营配置（校验区间后整体写入）。
func (s *OpsManagementService) UpdateSettings(ctx context.Context, settings OpsMgmtSettings) (OpsMgmtSettings, error) {
	if s == nil || s.settings == nil {
		return OpsMgmtSettings{}, fmt.Errorf("ops settings cache is not configured")
	}
	if err := s.settings.Set(ctx, settings); err != nil {
		return OpsMgmtSettings{}, err
	}
	return s.settings.Get(ctx)
}

// Dashboard 暴露看板服务（可能为 nil，取决于仓储能力）。
func (s *OpsManagementService) Dashboard() *OpsDashboardService {
	if s == nil {
		return nil
	}
	return s.dashboard
}

// Tasks 暴露任务注册表（handler 查询进度用）。
func (s *OpsManagementService) Tasks() *opsTaskRegistry {
	if s == nil {
		return nil
	}
	return s.tasks
}

// ---------------------------------------------------------------------------
// 凭据加解密
// ---------------------------------------------------------------------------

// encryptOpsSecret 加密敏感值。没有加密器时**拒绝**写入，避免明文落库。
func (s *OpsManagementService) encryptOpsSecret(plain string) (string, error) {
	trimmed := strings.TrimSpace(plain)
	if trimmed == "" {
		return "", nil
	}
	if s == nil || s.encryptor == nil {
		return "", fmt.Errorf("secret encryptor is not configured; refusing to store plaintext credential")
	}
	return s.encryptor.Encrypt(trimmed)
}

// decryptOpsSecret 解密敏感值；空输入返回空串。
func (s *OpsManagementService) decryptOpsSecret(cipher string) (string, error) {
	trimmed := strings.TrimSpace(cipher)
	if trimmed == "" {
		return "", nil
	}
	if s == nil || s.encryptor == nil {
		return "", fmt.Errorf("secret encryptor is not configured")
	}
	return s.encryptor.Decrypt(trimmed)
}

// siteAccessToken 取站点当前可用的 access token；过期则用 refresh 续期，
// 续期失败回落账号密码重新登录。成功续期会写回加密缓存。
func (s *OpsManagementService) siteAccessToken(ctx context.Context, site *OpsUpstreamSite) (string, error) {
	if site == nil {
		return "", ErrOpsSiteNotFound
	}
	now := s.now()
	if strings.TrimSpace(site.AccessTokenEnc) != "" {
		token, err := s.decryptOpsSecret(site.AccessTokenEnc)
		if err == nil && strings.TrimSpace(token) != "" {
			if site.TokenExpiresAt == nil || now.Add(time.Minute).Before(*site.TokenExpiresAt) {
				return token, nil
			}
			if strings.TrimSpace(site.RefreshTokenEnc) != "" {
				refreshToken, refreshErr := s.decryptOpsSecret(site.RefreshTokenEnc)
				if refreshErr == nil && strings.TrimSpace(refreshToken) != "" {
					refreshed, refreshErr := s.client.Refresh(ctx, site.BaseURL, refreshToken)
					if refreshErr == nil {
						if err := s.persistSiteToken(ctx, site, refreshed); err == nil {
							return refreshed.AccessToken, nil
						}
					}
				}
			}
		}
	}
	return s.siteLogin(ctx, site)
}

// siteLogin 用账号密码登录并缓存令牌。
func (s *OpsManagementService) siteLogin(ctx context.Context, site *OpsUpstreamSite) (string, error) {
	password, err := s.decryptOpsSecret(site.PasswordEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt site password: %w", err)
	}
	if strings.TrimSpace(password) == "" {
		return "", ErrOpsSiteAuthFailed
	}
	token, err := s.client.Login(ctx, site.BaseURL, site.Username, password)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrOpsSiteAuthFailed, err)
	}
	if err := s.persistSiteToken(ctx, site, token); err != nil {
		// 缓存失败不阻断本次调用（令牌仍在内存里可用）。
		logger.LegacyPrintf("service.ops_mgmt", "Warning: persist upstream token for site %d failed: %v", site.ID, err)
	}
	return token.AccessToken, nil
}

func (s *OpsManagementService) persistSiteToken(ctx context.Context, site *OpsUpstreamSite, token *OpsUpstreamToken) error {
	if s == nil || s.repo == nil || site == nil || token == nil {
		return fmt.Errorf("ops service is not configured")
	}
	accessEnc, err := s.encryptOpsSecret(token.AccessToken)
	if err != nil {
		return err
	}
	refreshEnc := site.RefreshTokenEnc
	if strings.TrimSpace(token.RefreshToken) != "" {
		refreshEnc, err = s.encryptOpsSecret(token.RefreshToken)
		if err != nil {
			return err
		}
	}
	if err := s.repo.UpdateSiteTokens(ctx, site.ID, accessEnc, refreshEnc, token.ExpiresAt); err != nil {
		return err
	}
	site.AccessTokenEnc = accessEnc
	site.RefreshTokenEnc = refreshEnc
	site.TokenExpiresAt = token.ExpiresAt
	return nil
}

// siteUpstreamAPIKey 解密站点上配置的上游普通 API Key（扩池默认凭据）。
func (s *OpsManagementService) siteUpstreamAPIKey(site *OpsUpstreamSite) (string, error) {
	if site == nil || strings.TrimSpace(site.UpstreamAPIKeyEnc) == "" {
		return "", nil
	}
	return s.decryptOpsSecret(site.UpstreamAPIKeyEnc)
}

// ---------------------------------------------------------------------------
// 站点 CRUD
// ---------------------------------------------------------------------------

// OpsSiteInput 站点新增/编辑入参。
type OpsSiteInput struct {
	Name                string
	BaseURL             string
	AuthMode            string
	Username            string
	Password            string
	UpstreamAPIKey      string
	SyncIntervalMinutes int
	// ClearUpstreamAPIKey 为 true 时清空站点级上游 Key。
	ClearUpstreamAPIKey bool
}

// ListSites 列出全部上游站点。
func (s *OpsManagementService) ListSites(ctx context.Context) ([]OpsUpstreamSite, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	return s.repo.ListSites(ctx)
}

// GetSite 读取单个站点。
func (s *OpsManagementService) GetSite(ctx context.Context, id int64) (*OpsUpstreamSite, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	return s.repo.GetSite(ctx, id)
}

// CreateSite 新增上游站点（密码与可选上游 Key 加密存储）。
func (s *OpsManagementService) CreateSite(ctx context.Context, input OpsSiteInput) (*OpsUpstreamSite, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrOpsInvalidSetting)
	}
	baseURL, err := NormalizeOpsBaseURL(input.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpsInvalidSetting, err)
	}
	username := strings.TrimSpace(input.Username)
	if username == "" {
		return nil, fmt.Errorf("%w: username is required", ErrOpsInvalidSetting)
	}
	authMode := strings.TrimSpace(input.AuthMode)
	if authMode == "" {
		authMode = OpsAuthModePassword
	}
	if authMode != OpsAuthModePassword && authMode != OpsAuthModeAPIKey {
		return nil, fmt.Errorf("%w: unsupported auth_mode %q", ErrOpsInvalidSetting, authMode)
	}
	exists, err := s.repo.SiteNameExists(ctx, name, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrOpsSiteNameExists
	}
	passwordEnc, err := s.encryptOpsSecret(input.Password)
	if err != nil {
		return nil, err
	}
	if passwordEnc == "" && authMode == OpsAuthModePassword {
		return nil, fmt.Errorf("%w: password is required for password auth mode", ErrOpsInvalidSetting)
	}
	apiKeyEnc, err := s.encryptOpsSecret(input.UpstreamAPIKey)
	if err != nil {
		return nil, err
	}
	interval := input.SyncIntervalMinutes
	if interval <= 0 {
		interval = opsDefaultSyncIntervalMinutes
	}
	site := &OpsUpstreamSite{
		Name:                name,
		BaseURL:             baseURL,
		AuthMode:            authMode,
		Username:            username,
		PasswordEnc:         passwordEnc,
		UpstreamAPIKeyEnc:   apiKeyEnc,
		Status:              OpsSiteStatusActive,
		SyncIntervalMinutes: interval,
	}
	if err := s.repo.CreateSite(ctx, site); err != nil {
		return nil, err
	}
	return site, nil
}

// UpdateSite 编辑站点；Password / UpstreamAPIKey 为空表示「保持不变」。
func (s *OpsManagementService) UpdateSite(ctx context.Context, id int64, input OpsSiteInput) (*OpsUpstreamSite, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	site, err := s.repo.GetSite(ctx, id)
	if err != nil {
		return nil, err
	}
	if name := strings.TrimSpace(input.Name); name != "" && name != site.Name {
		exists, existsErr := s.repo.SiteNameExists(ctx, name, id)
		if existsErr != nil {
			return nil, existsErr
		}
		if exists {
			return nil, ErrOpsSiteNameExists
		}
		site.Name = name
	}
	if baseURL := strings.TrimSpace(input.BaseURL); baseURL != "" && baseURL != site.BaseURL {
		normalized, normalizeErr := NormalizeOpsBaseURL(baseURL)
		if normalizeErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrOpsInvalidSetting, normalizeErr)
		}
		site.BaseURL = normalized
	}
	if username := strings.TrimSpace(input.Username); username != "" {
		site.Username = username
	}
	if authMode := strings.TrimSpace(input.AuthMode); authMode != "" {
		if authMode != OpsAuthModePassword && authMode != OpsAuthModeAPIKey {
			return nil, fmt.Errorf("%w: unsupported auth_mode %q", ErrOpsInvalidSetting, authMode)
		}
		site.AuthMode = authMode
	}
	if password := strings.TrimSpace(input.Password); password != "" {
		passwordEnc, encryptErr := s.encryptOpsSecret(password)
		if encryptErr != nil {
			return nil, encryptErr
		}
		site.PasswordEnc = passwordEnc
		// 密码变更后旧令牌失效，清空缓存强制重新登录。
		site.AccessTokenEnc = ""
		site.RefreshTokenEnc = ""
		site.TokenExpiresAt = nil
	}
	if input.ClearUpstreamAPIKey {
		site.UpstreamAPIKeyEnc = ""
	} else if key := strings.TrimSpace(input.UpstreamAPIKey); key != "" {
		keyEnc, encryptErr := s.encryptOpsSecret(key)
		if encryptErr != nil {
			return nil, encryptErr
		}
		site.UpstreamAPIKeyEnc = keyEnc
	}
	if input.SyncIntervalMinutes > 0 {
		site.SyncIntervalMinutes = input.SyncIntervalMinutes
	}
	if site.SyncIntervalMinutes <= 0 {
		site.SyncIntervalMinutes = opsDefaultSyncIntervalMinutes
	}
	if err := s.repo.UpdateSite(ctx, site); err != nil {
		return nil, err
	}
	return site, nil
}

// DeleteSite 删除站点（分组/账号快照级联删除；本地 Channel 的站点标签置空）。
func (s *OpsManagementService) DeleteSite(ctx context.Context, id int64) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("ops service is not configured")
	}
	return s.repo.DeleteSite(ctx, id)
}

// OpsSiteTestResult 登录连通性测试结果。
type OpsSiteTestResult struct {
	OK           bool      `json:"ok"`
	AuthMode     string    `json:"auth_mode"`
	LatencyMS    int64     `json:"latency_ms"`
	GroupCount   int       `json:"group_count"`
	AccountCount int       `json:"account_count"`
	ProfileEmail string    `json:"profile_email,omitempty"`
	Balance      float64   `json:"balance,omitempty"`
	Currency     string    `json:"currency,omitempty"`
	Error        string    `json:"error,omitempty"`
	TestedAt     time.Time `json:"tested_at"`
}

// TestSite 登录连通性测试（POST /auth/login；api_key 模式探测 /admin/groups）。
func (s *OpsManagementService) TestSite(ctx context.Context, id int64) (*OpsSiteTestResult, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	site, err := s.repo.GetSite(ctx, id)
	if err != nil {
		return nil, err
	}
	result := &OpsSiteTestResult{AuthMode: site.AuthMode, TestedAt: s.now()}
	started := s.now()
	if site.AuthMode == OpsAuthModeAPIKey {
		apiKey, keyErr := s.siteUpstreamAPIKey(site)
		if keyErr != nil || strings.TrimSpace(apiKey) == "" {
			if keyErr != nil {
				result.Error = "decrypt upstream api key failed"
			} else {
				result.Error = "upstream api key is not configured"
			}
			result.LatencyMS = s.now().Sub(started).Milliseconds()
			return result, nil
		}
		groups, groupsErr := s.client.ListAdminGroups(ctx, site.BaseURL, apiKey, 200)
		result.LatencyMS = s.now().Sub(started).Milliseconds()
		if groupsErr != nil {
			result.Error = groupsErr.Error()
			return result, nil
		}
		result.OK = true
		result.GroupCount = len(groups)
		return result, nil
	}

	token, loginErr := s.client.Login(ctx, site.BaseURL, site.Username, mustOpsDecrypt(s, site.PasswordEnc))
	if loginErr != nil {
		result.LatencyMS = s.now().Sub(started).Milliseconds()
		result.Error = loginErr.Error()
		return result, nil
	}
	groups, groupsErr := s.client.ListAvailableGroups(ctx, site.BaseURL, token.AccessToken)
	result.LatencyMS = s.now().Sub(started).Milliseconds()
	if groupsErr != nil {
		result.Error = groupsErr.Error()
		return result, nil
	}
	result.OK = true
	result.GroupCount = len(groups)
	if profile, profileErr := s.client.GetProfile(ctx, site.BaseURL, token.AccessToken); profileErr == nil && profile != nil {
		result.ProfileEmail = profile.Email
		result.Balance = profile.Balance
		result.Currency = profile.Currency
	}
	return result, nil
}

func mustOpsDecrypt(s *OpsManagementService, cipher string) string {
	plain, err := s.decryptOpsSecret(cipher)
	if err != nil {
		return ""
	}
	return plain
}

// ListSiteGroups 读取某站点的上游分组快照（含倍率与变动标记）。
func (s *OpsManagementService) ListSiteGroups(ctx context.Context, siteID int64) ([]OpsUpstreamGroup, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	if _, err := s.repo.GetSite(ctx, siteID); err != nil {
		return nil, err
	}
	return s.repo.ListUpstreamGroups(ctx, siteID)
}

// ListSiteAccounts 读取某站点的上游账号快照（password 模式为空）。
func (s *OpsManagementService) ListSiteAccounts(ctx context.Context, siteID int64) ([]OpsUpstreamAccount, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	if _, err := s.repo.GetSite(ctx, siteID); err != nil {
		return nil, err
	}
	return s.repo.ListUpstreamAccounts(ctx, siteID)
}

// ---------------------------------------------------------------------------
// 守护锁（幂等）
// ---------------------------------------------------------------------------

// ActivateGuardLock 幂等加锁：已存在 active 锁时返回既有锁且 created=false。
func (s *OpsManagementService) ActivateGuardLock(ctx context.Context, siteID int64, targetRef, lockType, reason string, metadata map[string]any) (*OpsGuardLock, bool, error) {
	if s == nil || s.repo == nil {
		return nil, false, fmt.Errorf("ops service is not configured")
	}
	ref := strings.TrimSpace(targetRef)
	if ref == "" {
		return nil, false, fmt.Errorf("%w: target_ref is required", ErrOpsInvalidSetting)
	}
	if strings.TrimSpace(lockType) == "" {
		lockType = OpsGuardLockTypeMargin
	}
	lock := &OpsGuardLock{
		SiteID:      siteID,
		TargetRef:   ref,
		LockType:    lockType,
		Active:      true,
		Reason:      strings.TrimSpace(reason),
		Metadata:    metadata,
		ActivatedAt: s.now(),
	}
	created, existing, err := s.repo.ActivateGuardLock(ctx, lock)
	if err != nil {
		return nil, false, err
	}
	if !created && existing != nil {
		return existing, false, nil
	}
	return lock, true, nil
}

// ClearGuardLock 清除守护锁（可恢复）。
func (s *OpsManagementService) ClearGuardLock(ctx context.Context, siteID int64, targetRef, lockType string) (bool, error) {
	if s == nil || s.repo == nil {
		return false, fmt.Errorf("ops service is not configured")
	}
	if strings.TrimSpace(lockType) == "" {
		lockType = OpsGuardLockTypeMargin
	}
	return s.repo.ClearGuardLock(ctx, siteID, strings.TrimSpace(targetRef), lockType)
}

// ---------------------------------------------------------------------------
// 分组级最低利润率
// ---------------------------------------------------------------------------

// ListGroupMargins 列出本地分组的最低利润率（含生效值与来源）。
func (s *OpsManagementService) ListGroupMargins(ctx context.Context) ([]OpsLocalGroup, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	settings, err := s.settings.Get(ctx)
	if err != nil {
		settings = DefaultOpsMgmtSettings()
	}
	groups, err := s.repo.ListAllLocalGroups(ctx)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		effective, source := resolveEffectiveMinMargin(nil, groups[i].OpsMinMarginBasisPoints, settings.MinimumMarginBasisPoints)
		groups[i].EffectiveMinMarginBasisPoints = effective
		groups[i].MinMarginSource = source
	}
	return groups, nil
}

// SetGroupMargin 设置分组级最低利润率；nil 表示继承全局。
func (s *OpsManagementService) SetGroupMargin(ctx context.Context, groupID int64, basisPoints *int64) (*OpsLocalGroup, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	if basisPoints != nil && (*basisPoints < 0 || *basisPoints > opsMaxMarginBasisPoints) {
		return nil, ErrOpsInvalidMargin
	}
	exists, err := s.repo.LocalGroupExists(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: group %d", ErrOpsInvalidSetting, groupID)
	}
	if err := s.repo.SetLocalGroupMinMargin(ctx, groupID, basisPoints); err != nil {
		return nil, err
	}
	settings, err := s.settings.Get(ctx)
	if err != nil {
		settings = DefaultOpsMgmtSettings()
	}
	groups, err := s.repo.ListLocalGroups(ctx, []int64{groupID})
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("%w: group %d", ErrOpsInvalidSetting, groupID)
	}
	group := groups[0]
	effective, source := resolveEffectiveMinMargin(nil, group.OpsMinMarginBasisPoints, settings.MinimumMarginBasisPoints)
	group.EffectiveMinMarginBasisPoints = effective
	group.MinMarginSource = source
	return &group, nil
}

// SetUpstreamGroupMargin 设置上游分组级最低利润率（就近覆盖的最高优先级）。
func (s *OpsManagementService) SetUpstreamGroupMargin(ctx context.Context, siteID, remoteGroupID int64, basisPoints *int64) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("ops service is not configured")
	}
	if basisPoints != nil && (*basisPoints < 0 || *basisPoints > opsMaxMarginBasisPoints) {
		return ErrOpsInvalidMargin
	}
	return s.repo.SetUpstreamGroupMinMargin(ctx, siteID, remoteGroupID, basisPoints)
}

// ---------------------------------------------------------------------------
// 恢复被自动停用的 Channel
// ---------------------------------------------------------------------------

// RestoreChannel 恢复被运营管理自动停用的本地 Channel 并清锁。
func (s *OpsManagementService) RestoreChannel(ctx context.Context, channelID int64) (*OpsChannelLink, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	link, err := s.repo.GetChannelLink(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, ErrOpsChannelNotFound
	}
	if link.AutoDisabledAt == nil {
		return nil, ErrOpsChannelNotAutoDisabled
	}
	if err := s.repo.SetChannelAutoDisabled(ctx, channelID, s.now(), "", false); err != nil {
		return nil, err
	}
	if link.SiteID != nil {
		if _, clearErr := s.repo.ClearGuardLock(ctx, *link.SiteID, opsChannelRef(channelID), OpsGuardLockTypeMargin); clearErr != nil {
			logger.LegacyPrintf("service.ops_mgmt", "Warning: clear guard lock for channel %d failed: %v", channelID, clearErr)
		}
	}
	if s.channelSvc != nil {
		s.channelSvc.InvalidateCache()
	}
	return s.repo.GetChannelLink(ctx, channelID)
}

// ListAutoDisabledChannels 列出被运营管理自动停用的 Channel。
func (s *OpsManagementService) ListAutoDisabledChannels(ctx context.Context) ([]OpsChannelLink, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("ops service is not configured")
	}
	return s.repo.ListAutoDisabledChannels(ctx)
}

// opsChannelRef 守护锁 / 利润检查里的 Channel 引用格式。
func opsChannelRef(channelID int64) string {
	return "channel:" + strconv.FormatInt(channelID, 10)
}
