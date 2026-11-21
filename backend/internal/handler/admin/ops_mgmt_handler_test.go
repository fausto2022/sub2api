package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 运营管理 handler 契约测试（design.md 第二部分 v3.0 / §2.8）。
//
// 路由统一 /admin/opsmgmt/*，不得占用既有可观测性模块的 /admin/ops/*。
// 本测试用最小 gin 路由复刻 registerOpsMgmtRoutes 的路径与参数名，
// 断言请求/响应契约（含脱敏、错误码、状态码）。
//
// 仓储用「嵌入接口 + 只实现被测方法」的桩，避免为了测 HTTP 层拉起数据库。

type opsMgmtStubRepo struct {
	service.OpsMgmtRepository

	mu          sync.Mutex
	sites       map[int64]*service.OpsUpstreamSite
	groups      map[int64][]service.OpsUpstreamGroup
	locals      map[int64]service.OpsLocalGroup
	channels    map[int64]*service.OpsChannelLink
	settings    map[string]string
	usage       service.OpsUsageTotals
	usageGroups []service.OpsUsageGroupTotals
	usageDays   int
	nextID      int64
}

func newOpsMgmtStubRepo() *opsMgmtStubRepo {
	return &opsMgmtStubRepo{
		sites:    map[int64]*service.OpsUpstreamSite{},
		groups:   map[int64][]service.OpsUpstreamGroup{},
		locals:   map[int64]service.OpsLocalGroup{},
		channels: map[int64]*service.OpsChannelLink{},
		settings: map[string]string{},
	}
}

func (r *opsMgmtStubRepo) id() int64 {
	r.nextID++
	return r.nextID
}

func (r *opsMgmtStubRepo) ListSites(context.Context) ([]service.OpsUpstreamSite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]service.OpsUpstreamSite, 0, len(r.sites))
	for _, site := range r.sites {
		out = append(out, *site)
	}
	return out, nil
}

func (r *opsMgmtStubRepo) GetSite(_ context.Context, id int64) (*service.OpsUpstreamSite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	site, ok := r.sites[id]
	if !ok {
		return nil, service.ErrOpsSiteNotFound
	}
	copied := *site
	return &copied, nil
}

func (r *opsMgmtStubRepo) SiteNameExists(_ context.Context, name string, excludeID int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, site := range r.sites {
		if id != excludeID && site.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (r *opsMgmtStubRepo) CreateSite(_ context.Context, site *service.OpsUpstreamSite) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.sites {
		if existing.Name == site.Name {
			return service.ErrOpsSiteNameExists
		}
	}
	site.ID = r.id()
	site.CreatedAt = time.Now()
	site.UpdatedAt = site.CreatedAt
	copied := *site
	r.sites[site.ID] = &copied
	return nil
}

func (r *opsMgmtStubRepo) UpdateSite(_ context.Context, site *service.OpsUpstreamSite) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sites[site.ID]; !ok {
		return service.ErrOpsSiteNotFound
	}
	copied := *site
	r.sites[site.ID] = &copied
	return nil
}

func (r *opsMgmtStubRepo) DeleteSite(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sites[id]; !ok {
		return service.ErrOpsSiteNotFound
	}
	delete(r.sites, id)
	return nil
}

func (r *opsMgmtStubRepo) UpdateSiteSyncState(context.Context, int64, *time.Time, string, string) error {
	return nil
}

func (r *opsMgmtStubRepo) UpdateSiteTokens(context.Context, int64, string, string, *time.Time) error {
	return nil
}

func (r *opsMgmtStubRepo) ListUpstreamGroups(_ context.Context, siteID int64) ([]service.OpsUpstreamGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]service.OpsUpstreamGroup(nil), r.groups[siteID]...), nil
}

func (r *opsMgmtStubRepo) GetUpstreamGroup(_ context.Context, siteID, remoteGroupID int64) (*service.OpsUpstreamGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.groups[siteID] {
		if r.groups[siteID][i].RemoteGroupID == remoteGroupID {
			copied := r.groups[siteID][i]
			return &copied, nil
		}
	}
	return nil, service.ErrOpsUpstreamGroupNotFound
}

func (r *opsMgmtStubRepo) ListUpstreamAccounts(context.Context, int64) ([]service.OpsUpstreamAccount, error) {
	return []service.OpsUpstreamAccount{}, nil
}

func (r *opsMgmtStubRepo) GetOpsSettings(context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.settings))
	for key, value := range r.settings {
		out[key] = value
	}
	return out, nil
}

func (r *opsMgmtStubRepo) SetOpsSettings(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range values {
		r.settings[key] = value
	}
	return nil
}

func (r *opsMgmtStubRepo) ListLocalGroups(_ context.Context, ids []int64) ([]service.OpsLocalGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]service.OpsLocalGroup, 0, len(ids))
	for _, id := range ids {
		if group, ok := r.locals[id]; ok {
			out = append(out, group)
		}
	}
	return out, nil
}

func (r *opsMgmtStubRepo) ListAllLocalGroups(context.Context) ([]service.OpsLocalGroup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]service.OpsLocalGroup, 0, len(r.locals))
	for _, group := range r.locals {
		out = append(out, group)
	}
	return out, nil
}

func (r *opsMgmtStubRepo) SetLocalGroupMinMargin(_ context.Context, groupID int64, basisPoints *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	group, ok := r.locals[groupID]
	if !ok {
		return fmt.Errorf("%w: group %d", service.ErrOpsInvalidSetting, groupID)
	}
	group.OpsMinMarginBasisPoints = basisPoints
	r.locals[groupID] = group
	return nil
}

func (r *opsMgmtStubRepo) LocalGroupExists(_ context.Context, groupID int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.locals[groupID]
	return ok, nil
}

func (r *opsMgmtStubRepo) SumUsageTotals(context.Context, time.Time, time.Time, *int64) (service.OpsUsageTotals, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage, nil
}

func (r *opsMgmtStubRepo) SumUsageByGroup(context.Context, time.Time, time.Time) ([]service.OpsUsageGroupTotals, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]service.OpsUsageGroupTotals(nil), r.usageGroups...), nil
}

func (r *opsMgmtStubRepo) CountUsageDays(context.Context, time.Time, time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usageDays, nil
}

func (r *opsMgmtStubRepo) GetChannelLink(_ context.Context, channelID int64) (*service.OpsChannelLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	link, ok := r.channels[channelID]
	if !ok {
		return nil, nil
	}
	copied := *link
	return &copied, nil
}

func (r *opsMgmtStubRepo) SetChannelAutoDisabled(_ context.Context, channelID int64, at time.Time, reason string, disabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	link, ok := r.channels[channelID]
	if !ok {
		return service.ErrOpsChannelNotFound
	}
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

func (r *opsMgmtStubRepo) ClearGuardLock(context.Context, int64, string, string) (bool, error) {
	return true, nil
}

func (r *opsMgmtStubRepo) ListProfitChecks(context.Context, service.OpsProfitCheckFilter) ([]service.OpsProfitCheck, int64, error) {
	return []service.OpsProfitCheck{}, 0, nil
}

func (r *opsMgmtStubRepo) FindChannelLinkByUpstreamKey(context.Context, int64, int64, string) (*service.OpsChannelLink, error) {
	return nil, nil
}

func (r *opsMgmtStubRepo) ListActiveGuardLocks(context.Context, int64) ([]service.OpsGuardLock, error) {
	return []service.OpsGuardLock{}, nil
}

func (r *opsMgmtStubRepo) ListAutoDisabledChannels(context.Context) ([]service.OpsChannelLink, error) {
	return []service.OpsChannelLink{}, nil
}

func (r *opsMgmtStubRepo) ListSitesForTest() {}

// opsMgmtStubEncryptor 测试用加密器。
type opsMgmtStubEncryptor struct{}

func (opsMgmtStubEncryptor) Encrypt(plaintext string) (string, error) {
	return "enc:" + plaintext, nil
}

func (opsMgmtStubEncryptor) Decrypt(ciphertext string) (string, error) {
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

// opsMgmtTestRouter 复刻 registerOpsMgmtRoutes 的路径与参数名（含分组级别名）。
func opsMgmtTestRouter(handler *OpsMgmtHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	admin := router.Group("/api/v1/admin")
	mgmt := admin.Group("/opsmgmt")
	{
		mgmt.GET("/dashboard", handler.GetDashboard)
		mgmt.GET("/sites", handler.ListSites)
		mgmt.POST("/sites", handler.CreateSite)
		mgmt.GET("/sites/:id", handler.GetSite)
		mgmt.PUT("/sites/:id", handler.UpdateSite)
		mgmt.DELETE("/sites/:id", handler.DeleteSite)
		mgmt.POST("/sites/:id/test", handler.TestSite)
		mgmt.POST("/sites/:id/sync", handler.SyncSite)
		mgmt.GET("/sites/:id/groups", handler.ListSiteGroups)
		mgmt.GET("/sites/:id/accounts", handler.ListSiteAccounts)
		mgmt.PUT("/sites/:id/groups/:remote_group_id/margin", handler.SetSiteGroupMargin)
		mgmt.POST("/expand", handler.Expand)
		mgmt.GET("/tasks", handler.ListTasks)
		mgmt.GET("/tasks/:id", handler.GetTask)
		mgmt.GET("/profit/checks", handler.ListProfitChecks)
		mgmt.POST("/profit/evaluate", handler.EvaluateProfit)
		mgmt.GET("/guard-locks", handler.ListGuardLocks)
		mgmt.POST("/channels/:id/restore", handler.RestoreChannel)
		mgmt.GET("/channels/auto-disabled", handler.ListAutoDisabledChannels)
		mgmt.GET("/settings", handler.GetSettings)
		mgmt.PUT("/settings", handler.UpdateSettings)
		mgmt.GET("/group-margins", handler.ListGroupMargins)
		mgmt.PUT("/group-margins/:id", handler.SetGroupMargin)
		admin.PUT("/groups/:id/opsmgmt-margin", handler.SetGroupMargin)
	}
	return router
}

func newOpsMgmtTestSuite(t *testing.T) (*opsMgmtStubRepo, *gin.Engine) {
	t.Helper()
	repo := newOpsMgmtStubRepo()
	svc := service.NewOpsManagementService(repo, opsMgmtStubEncryptor{}, service.NewOpsUpstreamClient(nil), nil)
	handler := NewOpsMgmtHandler(svc)
	return repo, opsMgmtTestRouter(handler)
}

type opsMgmtEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Reason  string          `json:"reason"`
	Data    json.RawMessage `json:"data"`
}

func doOpsMgmtRequest(t *testing.T, router *gin.Engine, method, path string, body any) (*httptest.ResponseRecorder, opsMgmtEnvelope) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	var envelope opsMgmtEnvelope
	if recorder.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope), "响应必须是 JSON 信封: %s", recorder.Body.String())
	}
	return recorder, envelope
}

func TestOpsMgmtHandlerSiteCRUDMasksSecrets(t *testing.T) {
	t.Parallel()

	repo, router := newOpsMgmtTestSuite(t)
	repo.sites[1] = &service.OpsUpstreamSite{
		ID:                  1,
		Name:                "up-main",
		BaseURL:             "https://up.example.com",
		AuthMode:            service.OpsAuthModePassword,
		Username:            "ops@example.com",
		PasswordEnc:         "enc:top-secret",
		UpstreamAPIKeyEnc:   "enc:sk-upstream",
		Status:              service.OpsSiteStatusActive,
		SyncIntervalMinutes: 30,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	recorder, envelope := doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/sites", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 0, envelope.Code)
	require.NotContains(t, recorder.Body.String(), "top-secret", "响应不得回显密码")
	require.NotContains(t, recorder.Body.String(), "enc:", "响应不得回显密文")
	require.NotContains(t, recorder.Body.String(), "password_enc")

	var list struct {
		Items []dto.OpsSite `json:"items"`
		Total int           `json:"total"`
	}
	require.NoError(t, json.Unmarshal(envelope.Data, &list))
	require.Equal(t, 1, list.Total)
	require.True(t, list.Items[0].PasswordSet)
	require.True(t, list.Items[0].UpstreamAPIKeySet)
	require.Equal(t, "ops@example.com", list.Items[0].Username)
	require.Empty(t, list.Items[0].LastError)

	// 新增：201 + 脱敏；重名 409 + reason 码。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPost, "/api/v1/admin/opsmgmt/sites", dto.OpsSiteRequest{
		Name:                "up-new",
		BaseURL:             "https://up-new.example.com/",
		Username:            "new@example.com",
		Password:            "pw",
		UpstreamAPIKey:      "sk-new",
		SyncIntervalMinutes: 15,
	})
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var created dto.OpsSite
	require.NoError(t, json.Unmarshal(envelope.Data, &created))
	require.Equal(t, "up-new", created.Name)
	require.Equal(t, "https://up-new.example.com", created.BaseURL, "尾部斜杠被规范化")
	require.True(t, created.PasswordSet)
	require.True(t, created.UpstreamAPIKeySet)
	require.NotContains(t, recorder.Body.String(), "enc:")

	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPost, "/api/v1/admin/opsmgmt/sites", dto.OpsSiteRequest{
		Name:     "up-new",
		BaseURL:  "https://other.example.com",
		Username: "x@example.com",
		Password: "pw",
	})
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, "OPS_SITE_NAME_EXISTS", envelope.Reason)

	// 详情 / 404 / 删除。
	recorder, _ = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/sites/1", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/sites/999", nil)
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, "OPS_SITE_NOT_FOUND", envelope.Reason)
	recorder, _ = doOpsMgmtRequest(t, router, http.MethodDelete, "/api/v1/admin/opsmgmt/sites/1", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	recorder, _ = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/sites/1", nil)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 非法 id 返回 400，而不是把 "abc" 当成 0。
	recorder, _ = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/sites/abc", nil)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestOpsMgmtHandlerSettingsContract(t *testing.T) {
	t.Parallel()

	_, router := newOpsMgmtTestSuite(t)

	recorder, envelope := doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/settings", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	var settings service.OpsMgmtSettings
	require.NoError(t, json.Unmarshal(envelope.Data, &settings))
	require.False(t, settings.AutoMarginProtection, "灰度开关默认关闭")
	require.True(t, settings.AutoRecovery, "自动恢复默认开启")
	require.Equal(t, int64(10000), settings.GuaranteedRevenueRatioBP, "保底比例默认 100%")
	require.Equal(t, int64(1000), settings.MinimumMarginBasisPoints)
	require.Equal(t, 2, settings.RiskConfirmations)
	require.Equal(t, 60, settings.CostMaxAgeMinutes)
	require.Equal(t, 30, settings.SyncIntervalMinutes)

	// 局部更新：只传需要改的键。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPut, "/api/v1/admin/opsmgmt/settings", map[string]any{
		"auto_margin_protection":      true,
		"guaranteed_revenue_ratio_bp": 9000,
		"minimum_margin_basis_points": 1200,
		"risk_confirmations":          3,
	})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(envelope.Data, &settings))
	require.True(t, settings.AutoMarginProtection)
	require.Equal(t, int64(9000), settings.GuaranteedRevenueRatioBP)
	require.Equal(t, int64(1200), settings.MinimumMarginBasisPoints)
	require.Equal(t, 3, settings.RiskConfirmations)
	require.Equal(t, 60, settings.CostMaxAgeMinutes, "未传的字段保持原值")

	// 越界利润率 → 400 + OPS_INVALID_MARGIN。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPut, "/api/v1/admin/opsmgmt/settings", map[string]any{
		"minimum_margin_basis_points": 12000,
	})
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "OPS_INVALID_MARGIN", envelope.Reason)
}

func TestOpsMgmtHandlerDashboardContract(t *testing.T) {
	t.Parallel()

	repo, router := newOpsMgmtTestSuite(t)
	now := time.Now()
	repo.usage = service.OpsUsageTotals{Revenue: 88.5, Cost: 48.5, Requests: 12, LastAt: &now}
	repo.usageDays = 7
	repo.locals[5] = service.OpsLocalGroup{ID: 5, Name: "claude-pro", RateMultiplier: 1}
	repo.usageGroups = []service.OpsUsageGroupTotals{{GroupID: 5, Revenue: 88.5, Cost: 48.5, Requests: 12}}

	recorder, envelope := doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/dashboard?range=today", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	var summary service.OpsProfitSummary
	require.NoError(t, json.Unmarshal(envelope.Data, &summary))
	require.True(t, summary.Available)
	require.Equal(t, "today", summary.Range)
	require.InDelta(t, 88.5, summary.TodayRevenue, 1e-9)
	require.InDelta(t, 48.5, summary.TodayCost, 1e-9)
	require.InDelta(t, 40.0, summary.TodayProfit, 1e-9)
	require.Equal(t, int64(4520), summary.TodayMarginBasisPoints)
	require.InDelta(t, 88.5, summary.TodayGuaranteedRevenue, 1e-9, "默认 100% 保底 = 收入")
	require.Equal(t, int64(10000), summary.GuaranteedRevenueRatioBP)
	require.Equal(t, 7, summary.SampledDays)
	require.True(t, summary.Complete)
	require.Len(t, summary.TopGroups, 1)
	require.Equal(t, "claude-pro", summary.TopGroups[0].GroupName)

	// range=7d 也接受。
	recorder, _ = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/dashboard?range=7d", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestOpsMgmtHandlerGroupMarginContract(t *testing.T) {
	t.Parallel()

	repo, router := newOpsMgmtTestSuite(t)
	repo.locals[5] = service.OpsLocalGroup{ID: 5, Name: "claude-pro", RateMultiplier: 1}
	repo.locals[6] = service.OpsLocalGroup{ID: 6, Name: "gpt-pro", RateMultiplier: 1}

	// 设置本地分组最低利润率。
	recorder, envelope := doOpsMgmtRequest(t, router, http.MethodPut, "/api/v1/admin/opsmgmt/group-margins/5", map[string]any{
		"basis_points": 1500,
	})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var group service.OpsLocalGroup
	require.NoError(t, json.Unmarshal(envelope.Data, &group))
	require.Equal(t, int64(5), group.ID)
	require.NotNil(t, group.OpsMinMarginBasisPoints)
	require.Equal(t, int64(1500), *group.OpsMinMarginBasisPoints)
	require.Equal(t, int64(1500), group.EffectiveMinMarginBasisPoints)
	require.Equal(t, "local_group", group.MinMarginSource)

	// null = 继承全局（默认 1000）。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPut, "/api/v1/admin/opsmgmt/group-margins/5", map[string]any{
		"min_margin_basis_points": nil,
	})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(envelope.Data, &group))
	require.Nil(t, group.OpsMinMarginBasisPoints)
	require.Equal(t, int64(1000), group.EffectiveMinMarginBasisPoints)
	require.Equal(t, "global", group.MinMarginSource)

	// design §2.8 别名：PUT /admin/groups/:id/opsmgmt-margin。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPut, "/api/v1/admin/groups/6/opsmgmt-margin", map[string]any{
		"basis_points": 2500,
	})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(envelope.Data, &group))
	require.Equal(t, int64(2500), group.EffectiveMinMarginBasisPoints)

	// 越界 → 400；分组不存在 → 400。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPut, "/api/v1/admin/opsmgmt/group-margins/6", map[string]any{
		"basis_points": -1,
	})
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "OPS_INVALID_MARGIN", envelope.Reason)

	recorder, _ = doOpsMgmtRequest(t, router, http.MethodPut, "/api/v1/admin/opsmgmt/group-margins/404", map[string]any{"basis_points": 100})
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	// 列表带生效值与来源。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/group-margins", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, string(envelope.Data), "min_margin_source")
}

func TestOpsMgmtHandlerExpandAndRestoreContract(t *testing.T) {
	t.Parallel()

	repo, router := newOpsMgmtTestSuite(t)
	repo.sites[1] = &service.OpsUpstreamSite{
		ID: 1, Name: "up-main", BaseURL: "https://up.example.com",
		AuthMode: service.OpsAuthModePassword, Username: "ops@example.com",
		PasswordEnc: "enc:pw", Status: service.OpsSiteStatusActive,
	}
	repo.groups[1] = []service.OpsUpstreamGroup{{ID: 1, SiteID: 1, RemoteGroupID: 11, Name: "claude-pro", RateMultiplier: 0.9}}
	repo.locals[5] = service.OpsLocalGroup{ID: 5, Name: "claude-pro", RateMultiplier: 1}

	// 缺参数 → 400 OPS_EXPAND_INVALID_INPUT。
	recorder, envelope := doOpsMgmtRequest(t, router, http.MethodPost, "/api/v1/admin/opsmgmt/expand", map[string]any{})
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "OPS_EXPAND_INVALID_INPUT", envelope.Reason)

	// 站点不存在 → 404。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPost, "/api/v1/admin/opsmgmt/expand", map[string]any{
		"site_id": 999, "remote_group_id": 11, "target_group_id": 5, "upstream_api_key": "sk",
	})
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, "OPS_SITE_NOT_FOUND", envelope.Reason)

	// 上游分组快照不存在 → 404。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPost, "/api/v1/admin/opsmgmt/expand", map[string]any{
		"site_id": 1, "remote_group_id": 404, "target_group_id": 5, "upstream_api_key": "sk",
	})
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, "OPS_UPSTREAM_GROUP_NOT_FOUND", envelope.Reason)

	// 参数齐备但没有 ChannelService（单测未注入）→ 500，而不是静默 200。
	recorder, _ = doOpsMgmtRequest(t, router, http.MethodPost, "/api/v1/admin/opsmgmt/expand", map[string]any{
		"site_id": 1, "remote_group_id": 11, "target_group_id": 5, "upstream_api_key": "sk",
	})
	require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())

	// restore：未自动停用的 Channel → 409；自动停用后可恢复。
	repo.channels[77] = &service.OpsChannelLink{ChannelID: 77, ChannelName: "ups-77", Status: "active"}
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPost, "/api/v1/admin/opsmgmt/channels/77/restore", nil)
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, "OPS_CHANNEL_NOT_AUTO_DISABLED", envelope.Reason)

	disabledAt := time.Now()
	repo.channels[78] = &service.OpsChannelLink{
		ChannelID: 78, ChannelName: "ups-78", Status: "disabled",
		AutoDisabledAt: &disabledAt, AutoDisabledReason: "margin",
		SiteID: opsMgmtPtrInt64(1),
	}
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPost, "/api/v1/admin/opsmgmt/channels/78/restore", nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var restored service.OpsChannelLink
	require.NoError(t, json.Unmarshal(envelope.Data, &restored))
	require.Equal(t, "active", restored.Status)
	require.Nil(t, restored.AutoDisabledAt)
}

func opsMgmtPtrInt64(value int64) *int64 { return &value }

func TestOpsMgmtHandlerTasksAndProfitContract(t *testing.T) {
	t.Parallel()

	_, router := newOpsMgmtTestSuite(t)

	recorder, envelope := doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/tasks", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, string(envelope.Data), "items")

	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/tasks/missing", nil)
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, "OPS_TASK_NOT_FOUND", envelope.Reason)

	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/profit/checks?site_id=1&page=1&page_size=10", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, string(envelope.Data), "page_size")

	recorder, _ = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/guard-locks", nil)
	require.Equal(t, http.StatusOK, recorder.Code)

	recorder, _ = doOpsMgmtRequest(t, router, http.MethodGet, "/api/v1/admin/opsmgmt/channels/auto-disabled", nil)
	require.Equal(t, http.StatusOK, recorder.Code)

	// 站点不存在时同步 → 404（不创建任务）。
	recorder, envelope = doOpsMgmtRequest(t, router, http.MethodPost, "/api/v1/admin/opsmgmt/sites/999/sync", nil)
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, "OPS_SITE_NOT_FOUND", envelope.Reason)
}

// 保证既有可观测性路由不被占用：本模块所有路径都在 /admin/opsmgmt 下，
// 唯一例外是 design §2.8 约定的分组级别名 /admin/groups/:id/opsmgmt-margin。
func TestOpsMgmtRoutePrefixIsIsolated(t *testing.T) {
	t.Parallel()

	router := opsMgmtTestRouter(NewOpsMgmtHandler(nil))
	seenAlias := false
	for _, route := range router.Routes() {
		if strings.HasSuffix(route.Path, "/admin/groups/:id/opsmgmt-margin") {
			seenAlias = true
			continue
		}
		require.Contains(t, route.Path, "/admin/opsmgmt/", "运营管理路由必须挂在 /admin/opsmgmt 下: %s", route.Path)
		require.NotContains(t, route.Path, "/admin/ops/", "不得占用既有可观测性前缀: %s", route.Path)
	}
	require.NotEmpty(t, router.Routes())
	require.True(t, seenAlias, "分组级最低利润率别名必须注册")
}
