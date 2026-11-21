package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 上游同步单测（design.md 第四部分 B1：站点添加后能拉分组/倍率，含倍率变动标记）。
//
// 上游用 httptest mock 成 sub2api 形态：
//   - POST /api/v1/auth/login → access/refresh token
//   - GET  /api/v1/groups/available → 该账号可见分组（含 peak/image/subscription 字段）
//   - GET  /api/v1/groups/rates     → 用户专属倍率
//
// password 模式禁止访问 /admin/*（mock 里用计数器断言）。

type opsUpstreamMock struct {
	mu sync.Mutex

	loginCalls         int
	groupsCalls        int
	ratesCalls         int
	adminGroupsCalls   int
	adminAccountsCalls int
	lastLoginEmail     string
	lastLoginPassword  string
	lastBearer         string
	lastAPIKey         string

	failGroups bool
}

func (m *opsUpstreamMock) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.loginCalls++
		m.lastLoginEmail = body.Email
		m.lastLoginPassword = body.Password
		m.mu.Unlock()
		writeOpsEnvelope(w, map[string]any{
			"access_token":  "tok-1",
			"refresh_token": "ref-1",
			"expires_in":    3600,
			"token_type":    "Bearer",
		})
	})
	mux.HandleFunc("/api/v1/groups/available", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.groupsCalls++
		m.lastBearer = r.Header.Get("Authorization")
		fail := m.failGroups
		m.mu.Unlock()
		if fail {
			http.Error(w, `{"code":500,"message":"boom"}`, http.StatusInternalServerError)
			return
		}
		writeOpsEnvelope(w, []map[string]any{
			{
				"id":                   11,
				"name":                 "claude-pro",
				"platform":             "anthropic",
				"rate_multiplier":      1.0,
				"peak_rate_enabled":    true,
				"peak_start":           "22:00",
				"peak_end":             "02:00",
				"peak_rate_multiplier": 1.5,
				"status":               "active",
			},
			{
				"id":                     12,
				"name":                   "gpt-pro",
				"platform":               "openai",
				"rate_multiplier":        0.8,
				"image_rate_independent": true,
				"video_rate_independent": false,
				"subscription_type":      "subscription",
				"is_exclusive":           true,
			},
		})
	})
	mux.HandleFunc("/api/v1/groups/rates", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.ratesCalls++
		m.mu.Unlock()
		writeOpsEnvelope(w, map[string]float64{"11": 1.20})
	})
	mux.HandleFunc("/api/v1/admin/groups", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.adminGroupsCalls++
		m.lastAPIKey = r.Header.Get("x-api-key")
		m.mu.Unlock()
		writeOpsEnvelope(w, map[string]any{"items": []map[string]any{
			{"id": 21, "name": "admin-claude", "platform": "anthropic", "rate_multiplier": 0.9, "ratio": 0.95},
		}})
	})
	mux.HandleFunc("/api/v1/admin/accounts", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.adminAccountsCalls++
		m.mu.Unlock()
		writeOpsEnvelope(w, map[string]any{"items": []map[string]any{
			{
				"id":              301,
				"name":            "up-account",
				"platform":        "anthropic",
				"type":            "apikey",
				"status":          "active",
				"schedulable":     true,
				"rate_multiplier": 1.1,
				"group_ids":       []int64{21},
				"credentials":     map[string]any{"api_key": "sk-upstream"},
			},
		}})
	})
	return mux
}

func writeOpsEnvelope(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "success", "data": data})
}

func newOpsSyncTestService(t *testing.T, repo *fakeOpsRepo, serverURL string, now time.Time) *OpsManagementService {
	t.Helper()
	svc := NewOpsManagementService(repo, fakeOpsEncryptor{},
		NewOpsUpstreamClient(&http.Client{Timeout: 5 * time.Second}), nil)
	svc.SetClock(func() time.Time { return now })
	// 关闭自动保护，避免同步后的评估在无关场景里写停用（评估逻辑另有专测）。
	repo.setSetting(OpsSettingAutoMarginProtection, "false")
	repo.setSetting(OpsSettingRiskConfirmations, "2")
	return svc
}

// B1：站点添加后能拉分组 + 用户专属倍率，并识别倍率变动。
func TestOpsSyncSitePullsGroupsAndRates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	mock := &opsUpstreamMock{}
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	repo := newFakeOpsRepo()
	site := repo.seedSite("up-main", server.URL)
	// 旧快照：0.95，用于验证 0.95 → 1.20 的倍率变动标记。
	repo.seedUpstreamGroup(site.ID, 11, 0.95, now.Add(-time.Hour))

	svc := newOpsSyncTestService(t, repo, server.URL, now)
	result, err := svc.SyncSite(ctx, site.ID)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, result.GroupCount)
	require.Equal(t, 0, result.AccountCount, "password 模式不拉账号")
	require.Equal(t, OpsAuthModePassword, result.AuthMode)

	// 登录用的是账号密码，且后续请求带 Bearer。
	require.Equal(t, 1, mock.loginCalls)
	require.Equal(t, "ops@example.com", mock.lastLoginEmail)
	require.Equal(t, "secret", mock.lastLoginPassword)
	require.Equal(t, "Bearer tok-1", mock.lastBearer)
	require.Equal(t, 0, mock.adminGroupsCalls, "password 模式禁止调用 /admin/*")
	require.Equal(t, 0, mock.adminAccountsCalls)

	// 用户专属倍率覆盖标价倍率：ratio=1.00（available），rate_multiplier=1.20（rates）。
	group, err := repo.GetUpstreamGroup(ctx, site.ID, 11)
	require.NoError(t, err)
	require.InDelta(t, 1.00, group.Ratio, 1e-9)
	require.InDelta(t, 1.20, group.RateMultiplier, 1e-9)

	// 倍率变动标记：0.95 → 1.20。
	require.Len(t, result.RateChanges, 1)
	require.Equal(t, int64(11), result.RateChanges[0].RemoteGroupID)
	require.True(t, result.RateChanges[0].RateChanged)
	require.NotNil(t, result.RateChanges[0].PreviousRateMultiplier)
	require.InDelta(t, 0.95, *result.RateChanges[0].PreviousRateMultiplier, 1e-9)
	require.InDelta(t, 1.20, result.RateChanges[0].RateMultiplier, 1e-9)

	// 峰时与非线性定价字段完整落库。
	group12, err := repo.GetUpstreamGroup(ctx, site.ID, 12)
	require.NoError(t, err)
	require.InDelta(t, 0.8, group12.RateMultiplier, 1e-9)
	require.True(t, group12.ImageSeparateRate)
	require.False(t, group12.VideoSeparateRate)
	require.Equal(t, "subscription", group12.SubscriptionType)
	require.True(t, group12.IsExclusive)
	require.True(t, group.PeakEnabled)
	require.Equal(t, "22:00", group.PeakStart)
	require.Equal(t, "02:00", group.PeakEnd)
	require.InDelta(t, 1.5, group.PeakMultiplier, 1e-9)

	// 站点同步状态 + 登录态加密缓存。
	stored, err := repo.GetSite(ctx, site.ID)
	require.NoError(t, err)
	require.Equal(t, OpsSiteStatusActive, stored.Status)
	require.Empty(t, stored.LastError)
	require.NotNil(t, stored.LastSyncedAt)
	require.True(t, stored.LastSyncedAt.Equal(now))
	require.Equal(t, "enc:tok-1", stored.AccessTokenEnc, "access token 必须加密缓存")
	require.Equal(t, "enc:ref-1", stored.RefreshTokenEnc)
	require.NotNil(t, stored.TokenExpiresAt)
}

// 同步失败必须保留旧快照，只记录 last_error（不覆盖已有数据）。
func TestOpsSyncFailureKeepsPreviousSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	mock := &opsUpstreamMock{failGroups: true}
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	repo := newFakeOpsRepo()
	site := repo.seedSite("up-broken", server.URL)
	oldSynced := now.Add(-2 * time.Hour)
	repo.seedUpstreamGroup(site.ID, 11, 0.95, oldSynced)

	svc := newOpsSyncTestService(t, repo, server.URL, now)
	result, err := svc.SyncSite(ctx, site.ID)
	require.Error(t, err)
	require.Nil(t, result)

	// 旧快照仍在（Upsert 未被调用），内容与时间戳不变。
	require.Equal(t, 0, repo.upsertCalls, "失败时不得写入快照")
	group, err := repo.GetUpstreamGroup(ctx, site.ID, 11)
	require.NoError(t, err)
	require.InDelta(t, 0.95, group.RateMultiplier, 1e-9)
	require.True(t, group.SyncedAt.Equal(oldSynced))

	// 站点记录错误，但不刷新 last_synced_at（否则过期快照会被「续命」）。
	stored, err := repo.GetSite(ctx, site.ID)
	require.NoError(t, err)
	require.Equal(t, OpsSiteStatusError, stored.Status)
	require.NotEmpty(t, stored.LastError)
	require.Nil(t, repo.lastSyncState, "失败时不更新 synced_at")
	require.Equal(t, OpsSiteStatusError, repo.lastSyncStatus)
}

// api_key 模式（可选）：用 x-api-key 拉全量分组与账号（credentials/group_ids/rate_multiplier/schedulable）。
func TestOpsSyncAPIKeyModePullsGroupsAndAccounts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	mock := &opsUpstreamMock{}
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	repo := newFakeOpsRepo()
	enc := fakeOpsEncryptor{}
	apiKeyEnc, err := enc.Encrypt("sk-admin-key")
	require.NoError(t, err)
	site := &OpsUpstreamSite{
		ID:                  900,
		Name:                "up-admin",
		BaseURL:             server.URL,
		AuthMode:            OpsAuthModeAPIKey,
		Username:            "ops@example.com",
		PasswordEnc:         "enc:secret",
		UpstreamAPIKeyEnc:   apiKeyEnc,
		Status:              OpsSiteStatusActive,
		SyncIntervalMinutes: 30,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	repo.mu.Lock()
	repo.sites[site.ID] = site
	repo.mu.Unlock()

	svc := newOpsSyncTestService(t, repo, server.URL, now)
	result, err := svc.SyncSite(ctx, site.ID)
	require.NoError(t, err)
	require.Equal(t, 1, result.GroupCount)
	require.Equal(t, 1, result.AccountCount)
	require.Equal(t, 1, mock.adminGroupsCalls)
	require.Equal(t, 1, mock.adminAccountsCalls)
	require.Equal(t, "sk-admin-key", mock.lastAPIKey, "必须用 x-api-key 鉴权")
	require.Equal(t, 0, mock.loginCalls, "api_key 模式不需要登录")

	accounts, err := repo.ListUpstreamAccounts(ctx, site.ID)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(301), accounts[0].RemoteAccountID)
	require.True(t, accounts[0].Schedulable != nil && *accounts[0].Schedulable)
	require.NotNil(t, accounts[0].RateMultiplier)
	require.InDelta(t, 1.1, *accounts[0].RateMultiplier, 1e-9)
	require.Equal(t, []int64{21}, accounts[0].GroupIDs)
	require.Equal(t, "sk-upstream", accounts[0].Credentials["api_key"])
}

// 定时同步：按站点 sync_interval_minutes（默认 30 分钟）判断到期，只同步到期的站点。
func TestOpsSyncDueSitesRespectsInterval(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	mock := &opsUpstreamMock{}
	server := httptest.NewServer(mock.handler())
	defer server.Close()

	repo := newFakeOpsRepo()
	dueSite := repo.seedSite("up-due", server.URL)
	recentSite := repo.seedSite("up-recent", server.URL)

	dueAt := now.Add(-31 * time.Minute)
	recentAt := now.Add(-5 * time.Minute)
	repo.mu.Lock()
	repo.sites[dueSite.ID].LastSyncedAt = &dueAt
	repo.sites[recentSite.ID].LastSyncedAt = &recentAt
	repo.mu.Unlock()

	svc := newOpsSyncTestService(t, repo, server.URL, now)
	results := svc.syncSvc.SyncDueSites(ctx)
	require.Len(t, results, 1, "只有超过 30 分钟未同步的站点会触发")
	require.Equal(t, dueSite.ID, results[0].SiteID)
	require.Equal(t, 1, mock.loginCalls, "近期已同步的站点不重复登录")

	// 默认间隔来自配置（30 分钟）。
	settings, err := svc.Settings(ctx)
	require.NoError(t, err)
	require.Equal(t, 30, settings.SyncIntervalMinutes)
}

// 上游接口返回非 0 code 时也要判失败（不能把空数据当成功覆盖快照）。
func TestOpsSyncTreatsUpstreamErrorEnvelopeAsFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		writeOpsEnvelope(w, map[string]any{"access_token": "tok-1", "refresh_token": "ref-1", "expires_in": 3600})
	})
	mux.HandleFunc("/api/v1/groups/available", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":403,"message":"permission denied","data":null}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	repo := newFakeOpsRepo()
	site := repo.seedSite("up-denied", server.URL)
	repo.seedUpstreamGroup(site.ID, 11, 0.9, now.Add(-time.Hour))

	svc := newOpsSyncTestService(t, repo, server.URL, now)
	_, err := svc.SyncSite(ctx, site.ID)
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "permission denied")
	require.Equal(t, 0, repo.upsertCalls)
	stored, getErr := repo.GetSite(ctx, site.ID)
	require.NoError(t, getErr)
	require.Equal(t, OpsSiteStatusError, stored.Status)
}
