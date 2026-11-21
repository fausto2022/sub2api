package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// 运营管理单元测试的共享伪造实现。
//
// 用「嵌入接口 + 只实现被测方法」的方式保持体积可控：未实现的方法若被调用会 nil panic，
// 让测试失败得足够明显，而不是静默返回零值掩盖问题。
type fakeOpsRepo struct {
	OpsMgmtRepository

	mu sync.Mutex

	sites    map[int64]*OpsUpstreamSite
	groups   map[int64][]OpsUpstreamGroup
	accounts map[int64][]OpsUpstreamAccount
	checks   []OpsProfitCheck
	locks    []OpsGuardLock
	settings map[string]string
	channels map[int64]*OpsChannelLink
	locals   map[int64]OpsLocalGroup

	usage      map[int64]OpsUsageTotals // key: 0 = 全部渠道, >0 = 指定 channel
	usageGroup []OpsUsageGroupTotals
	usageDays  int
	usageErr   error
	// usageByFrom 按窗口起点返回不同总额（看板要区分「今日」与「近 7 日」两条查询）。
	usageByFrom map[time.Time]OpsUsageTotals
	// usageRanges 记录每次聚合的窗口，用于断言看板确实按两个范围查询。
	usageRanges [][2]time.Time

	nextID int64

	// 观察点
	upsertCalls      int
	syncStateCalls   int
	lastSyncState    *time.Time
	lastSyncError    string
	lastSyncStatus   string
	clearedLocks     int
	disabledCalls    int
	lastDisabledWhy  string
	lastDisabledChan int64
}

func newFakeOpsRepo() *fakeOpsRepo {
	return &fakeOpsRepo{
		sites:       map[int64]*OpsUpstreamSite{},
		groups:      map[int64][]OpsUpstreamGroup{},
		accounts:    map[int64][]OpsUpstreamAccount{},
		settings:    map[string]string{},
		channels:    map[int64]*OpsChannelLink{},
		locals:      map[int64]OpsLocalGroup{},
		usage:       map[int64]OpsUsageTotals{},
		usageByFrom: map[time.Time]OpsUsageTotals{},
	}
}

func (r *fakeOpsRepo) id() int64 {
	r.nextID++
	return r.nextID
}

// ---------------------------------------------------------------------------
// 站点
// ---------------------------------------------------------------------------

func (r *fakeOpsRepo) ListSites(_ context.Context) ([]OpsUpstreamSite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OpsUpstreamSite, 0, len(r.sites))
	for _, site := range r.sites {
		out = append(out, *site)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *fakeOpsRepo) GetSite(_ context.Context, id int64) (*OpsUpstreamSite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	site, ok := r.sites[id]
	if !ok {
		return nil, ErrOpsSiteNotFound
	}
	copied := *site
	return &copied, nil
}

func (r *fakeOpsRepo) SiteNameExists(_ context.Context, name string, excludeID int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, site := range r.sites {
		if id != excludeID && site.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeOpsRepo) CreateSite(_ context.Context, site *OpsUpstreamSite) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.sites {
		if existing.Name == site.Name {
			return ErrOpsSiteNameExists
		}
	}
	site.ID = r.id()
	site.CreatedAt = time.Now()
	site.UpdatedAt = site.CreatedAt
	copied := *site
	r.sites[site.ID] = &copied
	return nil
}

func (r *fakeOpsRepo) UpdateSite(_ context.Context, site *OpsUpstreamSite) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sites[site.ID]; !ok {
		return ErrOpsSiteNotFound
	}
	copied := *site
	r.sites[site.ID] = &copied
	return nil
}

func (r *fakeOpsRepo) DeleteSite(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sites[id]; !ok {
		return ErrOpsSiteNotFound
	}
	delete(r.sites, id)
	return nil
}

func (r *fakeOpsRepo) UpdateSiteSyncState(_ context.Context, id int64, syncedAt *time.Time, lastErr string, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	site, ok := r.sites[id]
	if !ok {
		return ErrOpsSiteNotFound
	}
	site.Status = status
	site.LastError = lastErr
	if syncedAt != nil {
		value := *syncedAt
		site.LastSyncedAt = &value
	}
	r.syncStateCalls++
	r.lastSyncState = syncedAt
	r.lastSyncError = lastErr
	r.lastSyncStatus = status
	return nil
}

func (r *fakeOpsRepo) UpdateSiteTokens(_ context.Context, id int64, accessTokenEnc, refreshTokenEnc string, expiresAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	site, ok := r.sites[id]
	if !ok {
		return ErrOpsSiteNotFound
	}
	site.AccessTokenEnc = accessTokenEnc
	site.RefreshTokenEnc = refreshTokenEnc
	if expiresAt != nil {
		value := *expiresAt
		site.TokenExpiresAt = &value
	}
	return nil
}

// ---------------------------------------------------------------------------
// 上游分组 / 账号
// ---------------------------------------------------------------------------

func (r *fakeOpsRepo) ListUpstreamGroups(_ context.Context, siteID int64) ([]OpsUpstreamGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	groups := r.groups[siteID]
	out := make([]OpsUpstreamGroup, len(groups))
	copy(out, groups)
	return out, nil
}

func (r *fakeOpsRepo) GetUpstreamGroup(_ context.Context, siteID, remoteGroupID int64) (*OpsUpstreamGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.groups[siteID] {
		if r.groups[siteID][i].RemoteGroupID == remoteGroupID {
			copied := r.groups[siteID][i]
			return &copied, nil
		}
	}
	return nil, ErrOpsUpstreamGroupNotFound
}

func (r *fakeOpsRepo) UpsertUpstreamGroups(_ context.Context, siteID int64, groups []OpsUpstreamGroup) (map[int64]OpsUpstreamGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upsertCalls++
	previous := make(map[int64]OpsUpstreamGroup, len(r.groups[siteID]))
	for _, group := range r.groups[siteID] {
		previous[group.RemoteGroupID] = group
	}
	merged := make([]OpsUpstreamGroup, 0, len(groups))
	for _, group := range groups {
		group.SiteID = siteID
		if existing, ok := previous[group.RemoteGroupID]; ok {
			group.ID = existing.ID
			// min_margin 是人工配置，不被同步覆盖。
			group.MinMarginBasisPoints = existing.MinMarginBasisPoints
		} else {
			group.ID = r.id()
		}
		merged = append(merged, group)
	}
	r.groups[siteID] = merged
	return previous, nil
}

func (r *fakeOpsRepo) SetUpstreamGroupMinMargin(_ context.Context, siteID, remoteGroupID int64, basisPoints *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.groups[siteID] {
		if r.groups[siteID][i].RemoteGroupID == remoteGroupID {
			r.groups[siteID][i].MinMarginBasisPoints = basisPoints
			return nil
		}
	}
	return ErrOpsUpstreamGroupNotFound
}

func (r *fakeOpsRepo) ListUpstreamAccounts(_ context.Context, siteID int64) ([]OpsUpstreamAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	accounts := r.accounts[siteID]
	out := make([]OpsUpstreamAccount, len(accounts))
	copy(out, accounts)
	return out, nil
}

func (r *fakeOpsRepo) ReplaceUpstreamAccounts(_ context.Context, siteID int64, accounts []OpsUpstreamAccount) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts[siteID] = append([]OpsUpstreamAccount(nil), accounts...)
	return nil
}

// ---------------------------------------------------------------------------
// 利润检查
// ---------------------------------------------------------------------------

func (r *fakeOpsRepo) AppendProfitCheck(_ context.Context, check *OpsProfitCheck) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	check.ID = r.id()
	r.checks = append(r.checks, *check)
	return nil
}

func (r *fakeOpsRepo) ListProfitChecks(_ context.Context, filter OpsProfitCheckFilter) ([]OpsProfitCheck, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	matched := make([]OpsProfitCheck, 0, len(r.checks))
	for _, check := range r.checks {
		if filter.SiteID > 0 && check.SiteID != filter.SiteID {
			continue
		}
		if filter.RemoteGroupID > 0 && check.RemoteGroupID != filter.RemoteGroupID {
			continue
		}
		if filter.ChannelRef != "" && check.ChannelRef != filter.ChannelRef {
			continue
		}
		if filter.Status != "" && check.Status != filter.Status {
			continue
		}
		matched = append(matched, check)
	}
	sortChecksNewestFirst(matched)
	return matched, int64(len(matched)), nil
}

func (r *fakeOpsRepo) ListRecentProfitChecks(_ context.Context, siteID, remoteGroupID int64, channelRef string, limit int) ([]OpsProfitCheck, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	matched := make([]OpsProfitCheck, 0, len(r.checks))
	for _, check := range r.checks {
		if check.SiteID == siteID && check.RemoteGroupID == remoteGroupID && check.ChannelRef == channelRef {
			matched = append(matched, check)
		}
	}
	sortChecksNewestFirst(matched)
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func sortChecksNewestFirst(checks []OpsProfitCheck) {
	sort.SliceStable(checks, func(i, j int) bool {
		if checks[i].CreatedAt.Equal(checks[j].CreatedAt) {
			return checks[i].ID > checks[j].ID
		}
		return checks[i].CreatedAt.After(checks[j].CreatedAt)
	})
}

// ---------------------------------------------------------------------------
// 守护锁（幂等语义与 SQL 部分唯一索引一致）
// ---------------------------------------------------------------------------

func (r *fakeOpsRepo) ActivateGuardLock(_ context.Context, lock *OpsGuardLock) (bool, *OpsGuardLock, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.locks {
		existing := r.locks[i]
		if existing.Active && existing.SiteID == lock.SiteID && existing.TargetRef == lock.TargetRef && existing.LockType == lock.LockType {
			copied := existing
			return false, &copied, nil
		}
	}
	lock.ID = r.id()
	lock.Active = true
	copied := *lock
	r.locks = append(r.locks, copied)
	return true, &copied, nil
}

func (r *fakeOpsRepo) ClearGuardLock(_ context.Context, siteID int64, targetRef, lockType string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cleared := false
	now := time.Now()
	for i := range r.locks {
		if r.locks[i].Active && r.locks[i].SiteID == siteID && r.locks[i].TargetRef == targetRef && r.locks[i].LockType == lockType {
			r.locks[i].Active = false
			r.locks[i].ClearedAt = &now
			cleared = true
		}
	}
	if cleared {
		r.clearedLocks++
	}
	return cleared, nil
}

func (r *fakeOpsRepo) ListActiveGuardLocks(_ context.Context, siteID int64) ([]OpsGuardLock, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OpsGuardLock, 0, len(r.locks))
	for _, lock := range r.locks {
		if !lock.Active {
			continue
		}
		if siteID > 0 && lock.SiteID != siteID {
			continue
		}
		out = append(out, lock)
	}
	return out, nil
}

func (r *fakeOpsRepo) GetActiveGuardLock(_ context.Context, siteID int64, targetRef, lockType string) (*OpsGuardLock, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, lock := range r.locks {
		if lock.Active && lock.SiteID == siteID && lock.TargetRef == targetRef && lock.LockType == lockType {
			copied := lock
			return &copied, nil
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// 配置
// ---------------------------------------------------------------------------

func (r *fakeOpsRepo) GetOpsSettings(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.settings))
	for key, value := range r.settings {
		out[key] = value
	}
	return out, nil
}

func (r *fakeOpsRepo) SetOpsSettings(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range values {
		r.settings[key] = value
	}
	return nil
}

// ---------------------------------------------------------------------------
// 本地 Channel
// ---------------------------------------------------------------------------

func (r *fakeOpsRepo) SetChannelOpsLink(_ context.Context, channelID, siteID, remoteGroupID int64, upstreamKeyEnc, upstreamKeyHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	link, ok := r.channels[channelID]
	if !ok {
		return ErrOpsChannelNotFound
	}
	link.SiteID = &siteID
	link.RemoteGroupID = &remoteGroupID
	link.UpstreamKeyHash = upstreamKeyHash
	return nil
}

func (r *fakeOpsRepo) GetChannelLink(_ context.Context, channelID int64) (*OpsChannelLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	link, ok := r.channels[channelID]
	if !ok {
		return nil, nil
	}
	copied := *link
	return &copied, nil
}

func (r *fakeOpsRepo) ListChannelLinksByUpstreamGroup(_ context.Context, siteID, remoteGroupID int64) ([]OpsChannelLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []OpsChannelLink{}
	for _, link := range r.channels {
		if link.SiteID != nil && *link.SiteID == siteID && link.RemoteGroupID != nil && *link.RemoteGroupID == remoteGroupID {
			out = append(out, *link)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChannelID < out[j].ChannelID })
	return out, nil
}

func (r *fakeOpsRepo) ListAutoDisabledChannels(_ context.Context) ([]OpsChannelLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []OpsChannelLink{}
	for _, link := range r.channels {
		if link.AutoDisabledAt != nil {
			out = append(out, *link)
		}
	}
	return out, nil
}

func (r *fakeOpsRepo) FindChannelLinkByUpstreamKey(_ context.Context, siteID, remoteGroupID int64, upstreamKeyHash string) (*OpsChannelLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, link := range r.channels {
		if link.SiteID != nil && *link.SiteID == siteID &&
			link.RemoteGroupID != nil && *link.RemoteGroupID == remoteGroupID &&
			link.UpstreamKeyHash == upstreamKeyHash {
			copied := *link
			return &copied, nil
		}
	}
	return nil, nil
}

func (r *fakeOpsRepo) SetChannelAutoDisabled(_ context.Context, channelID int64, at time.Time, reason string, disabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	link, ok := r.channels[channelID]
	if !ok {
		return ErrOpsChannelNotFound
	}
	r.disabledCalls++
	r.lastDisabledChan = channelID
	r.lastDisabledWhy = reason
	if disabled {
		value := at
		link.AutoDisabledAt = &value
		link.AutoDisabledReason = reason
		link.Status = "disabled"
		return nil
	}
	link.AutoDisabledAt = nil
	link.AutoDisabledReason = ""
	link.Status = "active"
	return nil
}

// ---------------------------------------------------------------------------
// 本地分组
// ---------------------------------------------------------------------------

func (r *fakeOpsRepo) ListLocalGroups(_ context.Context, ids []int64) ([]OpsLocalGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OpsLocalGroup, 0, len(ids))
	for _, id := range ids {
		if group, ok := r.locals[id]; ok {
			out = append(out, group)
		}
	}
	return out, nil
}

func (r *fakeOpsRepo) ListAllLocalGroups(_ context.Context) ([]OpsLocalGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]OpsLocalGroup, 0, len(r.locals))
	for _, group := range r.locals {
		out = append(out, group)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *fakeOpsRepo) SetLocalGroupMinMargin(_ context.Context, groupID int64, basisPoints *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	group, ok := r.locals[groupID]
	if !ok {
		return fmt.Errorf("%w: group %d", ErrOpsInvalidSetting, groupID)
	}
	group.OpsMinMarginBasisPoints = basisPoints
	r.locals[groupID] = group
	return nil
}

func (r *fakeOpsRepo) LocalGroupExists(_ context.Context, groupID int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.locals[groupID]
	return ok, nil
}

// ---------------------------------------------------------------------------
// usage_logs 聚合
// ---------------------------------------------------------------------------

func (r *fakeOpsRepo) SumUsageTotals(_ context.Context, from, to time.Time, channelID *int64) (OpsUsageTotals, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.usageErr != nil {
		return OpsUsageTotals{}, r.usageErr
	}
	r.usageRanges = append(r.usageRanges, [2]time.Time{from, to})
	if totals, ok := r.usageByFrom[from]; ok && channelID == nil {
		return totals, nil
	}
	key := int64(0)
	if channelID != nil {
		key = *channelID
	}
	return r.usage[key], nil
}

func (r *fakeOpsRepo) recordedUsageRanges() [][2]time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][2]time.Time, len(r.usageRanges))
	copy(out, r.usageRanges)
	return out
}

func (r *fakeOpsRepo) SumUsageByGroup(_ context.Context, _, _ time.Time) ([]OpsUsageGroupTotals, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.usageErr != nil {
		return nil, r.usageErr
	}
	out := make([]OpsUsageGroupTotals, len(r.usageGroup))
	copy(out, r.usageGroup)
	return out, nil
}

func (r *fakeOpsRepo) CountUsageDays(_ context.Context, _, _ time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.usageErr != nil {
		return 0, r.usageErr
	}
	return r.usageDays, nil
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// fakeOpsEncryptor 测试用「加密器」：前缀标记即可验证密文落库。
type fakeOpsEncryptor struct{}

func (fakeOpsEncryptor) Encrypt(plaintext string) (string, error) {
	return "enc:" + plaintext, nil
}

func (fakeOpsEncryptor) Decrypt(ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, "enc:") {
		return "", fmt.Errorf("invalid ciphertext")
	}
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

// seedOpsSite 写入一个站点（密码/上游 Key 已加密）。
func (r *fakeOpsRepo) seedSite(name, baseURL string) *OpsUpstreamSite {
	enc := fakeOpsEncryptor{}
	passwordEnc, _ := enc.Encrypt("secret")
	site := &OpsUpstreamSite{
		ID:                  r.id(),
		Name:                name,
		BaseURL:             baseURL,
		AuthMode:            OpsAuthModePassword,
		Username:            "ops@example.com",
		PasswordEnc:         passwordEnc,
		Status:              OpsSiteStatusActive,
		SyncIntervalMinutes: opsDefaultSyncIntervalMinutes,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	r.mu.Lock()
	r.sites[site.ID] = site
	r.mu.Unlock()
	return site
}

// seedUpstreamGroup 写入上游分组快照。
func (r *fakeOpsRepo) seedUpstreamGroup(siteID, remoteGroupID int64, rate float64, syncedAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	group := OpsUpstreamGroup{
		ID:             r.id(),
		SiteID:         siteID,
		RemoteGroupID:  remoteGroupID,
		Name:           fmt.Sprintf("remote-%d", remoteGroupID),
		Platform:       "anthropic",
		Ratio:          rate,
		RateMultiplier: rate,
		PeakMultiplier: 1,
		SyncedAt:       syncedAt,
	}
	r.groups[siteID] = append(r.groups[siteID], group)
}

// seedLocalGroup 写入本地分组。
func (r *fakeOpsRepo) seedLocalGroup(groupID int64, name string, rate float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.locals[groupID] = OpsLocalGroup{ID: groupID, Name: name, Platform: "anthropic", RateMultiplier: rate}
}

// seedChannel 写入一个已对接的本地 Channel。
func (r *fakeOpsRepo) seedChannel(channelID, siteID, remoteGroupID int64, localGroupID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	site := siteID
	remote := remoteGroupID
	r.channels[channelID] = &OpsChannelLink{
		ChannelID:     channelID,
		ChannelName:   fmt.Sprintf("ups-%d", channelID),
		Status:        "active",
		SiteID:        &site,
		RemoteGroupID: &remote,
		GroupIDs:      []int64{localGroupID},
	}
}

// setSetting 直接写运营配置（绕过服务缓存）。
func (r *fakeOpsRepo) setSetting(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings[key] = value
}

func (r *fakeOpsRepo) activeLockCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, lock := range r.locks {
		if lock.Active {
			count++
		}
	}
	return count
}
