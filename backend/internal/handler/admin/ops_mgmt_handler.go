package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// OpsMgmtHandler 运营管理（design.md 第二部分 v3.0）HTTP 处理器。
//
// 命名说明：仓库既有 OpsHandler 属于可观测性模块（/admin/ops/*），
// 本模块统一使用 /admin/ops_mgmt/* 与 ops_mgmt_ 前缀，两者互不覆盖。
type OpsMgmtHandler struct {
	service *service.OpsManagementService
}

// NewOpsMgmtHandler 构造 handler。
func NewOpsMgmtHandler(svc *service.OpsManagementService) *OpsMgmtHandler {
	return &OpsMgmtHandler{service: svc}
}

func (h *OpsMgmtHandler) ensureService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.Error(c, 503, "Ops management service is not available")
		return false
	}
	return true
}

func parseOpsMgmtID(c *gin.Context, name string) (int64, bool) {
	raw := strings.TrimSpace(c.Param(name))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid "+name)
		return 0, false
	}
	return id, true
}

// GetDashboard 运营看板：今日/7 日收入-成本-利润-利润率 + 分组盈利榜。
// GET /admin/ops_mgmt/dashboard?range=today|7d
func (h *OpsMgmtHandler) GetDashboard(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	dashboard := h.service.Dashboard()
	if dashboard == nil {
		response.Error(c, 503, "Ops dashboard is not available")
		return
	}
	summary, err := dashboard.ProfitSummary(c.Request.Context(), c.Query("range"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, summary)
}

// ListSites 上游站点列表。
// GET /admin/ops_mgmt/sites
func (h *OpsMgmtHandler) ListSites(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	sites, err := h.service.ListSites(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": dto.OpsSitesFromService(sites), "total": len(sites)})
}

// CreateSite 新增上游站点。
// POST /admin/ops_mgmt/sites
func (h *OpsMgmtHandler) CreateSite(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	var req dto.OpsSiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	site, err := h.service.CreateSite(c.Request.Context(), req.ToService())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, dto.OpsSiteFromService(site))
}

// GetSite 站点详情。
// GET /admin/ops_mgmt/sites/:id
func (h *OpsMgmtHandler) GetSite(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	site, err := h.service.GetSite(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.OpsSiteFromService(site))
}

// UpdateSite 编辑站点（密码/上游 Key 留空表示不变）。
// PUT /admin/ops_mgmt/sites/:id
func (h *OpsMgmtHandler) UpdateSite(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	var req dto.OpsSiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	site, err := h.service.UpdateSite(c.Request.Context(), id, req.ToService())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.OpsSiteFromService(site))
}

// DeleteSite 删除站点。
// DELETE /admin/ops_mgmt/sites/:id
func (h *OpsMgmtHandler) DeleteSite(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteSite(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// TestSite 登录连通性测试（密码模式：POST /auth/login；api_key 模式：探测 /admin/groups）。
// POST /admin/ops_mgmt/sites/:id/test
func (h *OpsMgmtHandler) TestSite(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	result, err := h.service.TestSite(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// SyncSite 手动触发同步（异步任务）。
// POST /admin/ops_mgmt/sites/:id/sync
func (h *OpsMgmtHandler) SyncSite(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	task, err := h.service.SyncSiteAsync(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, task)
}

// ListSiteGroups 上游分组快照（含倍率与变动标记）。
// GET /admin/ops_mgmt/sites/:id/groups
func (h *OpsMgmtHandler) ListSiteGroups(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	groups, err := h.service.ListSiteGroups(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": groups, "total": len(groups)})
}

// ListSiteAccounts 上游账号快照（password 模式为空）。
// GET /admin/ops_mgmt/sites/:id/accounts
func (h *OpsMgmtHandler) ListSiteAccounts(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	accounts, err := h.service.ListSiteAccounts(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": accounts, "total": len(accounts)})
}

// SetSiteGroupMargin 上游分组级最低利润率。
// PUT /admin/ops_mgmt/sites/:id/groups/:remote_group_id/margin
func (h *OpsMgmtHandler) SetSiteGroupMargin(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	siteID, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	remoteGroupID, ok := parseOpsMgmtID(c, "remote_group_id")
	if !ok {
		return
	}
	var req dto.OpsGroupMarginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.service.SetUpstreamGroupMargin(c.Request.Context(), siteID, remoteGroupID, req.Resolve()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"site_id": siteID, "remote_group_id": remoteGroupID, "min_margin_basis_points": req.Resolve()})
}

// Expand 对接渠道（扩池）：items 非空时异步批量，否则同步单条。
// POST /admin/ops_mgmt/expand
func (h *OpsMgmtHandler) Expand(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	var req dto.OpsExpandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.Items) > 0 {
		inputs := make([]service.OpsExpandInput, 0, len(req.Items))
		for _, item := range req.Items {
			inputs = append(inputs, item.ToService())
		}
		task, err := h.service.ExpandBatchAsync(c.Request.Context(), inputs)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Accepted(c, task)
		return
	}
	result, err := h.service.Expand(c.Request.Context(), req.OpsExpandItem.ToService())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if result != nil && result.Skipped {
		response.Success(c, result)
		return
	}
	response.Created(c, result)
}

// ListTasks 任务列表（含进度）。
// GET /admin/ops_mgmt/tasks
func (h *OpsMgmtHandler) ListTasks(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	tasks := h.service.ListTasks(limit)
	response.Success(c, gin.H{"items": tasks, "total": len(tasks)})
}

// GetTask 任务详情。
// GET /admin/ops_mgmt/tasks/:id
func (h *OpsMgmtHandler) GetTask(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.BadRequest(c, "Invalid task id")
		return
	}
	task, err := h.service.GetTask(id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, task)
}

// ListProfitChecks 利润检查历史。
// GET /admin/ops_mgmt/profit/checks?site_id=&remote_group_id=&channel_ref=&status=&page=&page_size=
func (h *OpsMgmtHandler) ListProfitChecks(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	filter := service.OpsProfitCheckFilter{
		SiteID:        parseOpsMgmtInt64Query(c, "site_id"),
		RemoteGroupID: parseOpsMgmtInt64Query(c, "remote_group_id"),
		ChannelRef:    strings.TrimSpace(c.Query("channel_ref")),
		Status:        strings.TrimSpace(c.Query("status")),
	}
	page := parseIntQueryDefault(c, "page", 1)
	pageSize := parseIntQueryDefault(c, "page_size", 50)
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}
	filter.Limit = pageSize
	filter.Offset = (page - 1) * pageSize
	checks, total, err := h.service.ListProfitChecks(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, response.PaginatedData{
		Items:    checks,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    pagesForOpsMgmt(total, pageSize),
	})
}

// EvaluateProfit 手动触发利润评估（异步任务，灰度观察用）。
// POST /admin/ops_mgmt/profit/evaluate
func (h *OpsMgmtHandler) EvaluateProfit(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	var req struct {
		SiteID int64 `json:"site_id"`
	}
	// body 可选：无 body 时评估全部站点。
	if c.Request != nil && c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	task, err := h.service.EvaluateAsync(c.Request.Context(), req.SiteID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, task)
}

// RestoreChannel 恢复被自动停用的本地 Channel。
// POST /admin/ops_mgmt/channels/:id/restore
func (h *OpsMgmtHandler) RestoreChannel(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	link, err := h.service.RestoreChannel(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, link)
}

// ListAutoDisabledChannels 被运营管理自动停用的 Channel 列表。
// GET /admin/ops_mgmt/channels/auto-disabled
func (h *OpsMgmtHandler) ListAutoDisabledChannels(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	links, err := h.service.ListAutoDisabledChannels(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": links, "total": len(links)})
}

// GetSettings 全局运营配置。
// GET /admin/ops_mgmt/settings
func (h *OpsMgmtHandler) GetSettings(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	settings, err := h.service.Settings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

// UpdateSettings 更新全局运营配置（局部更新：缺省字段保持不变）。
// PUT /admin/ops_mgmt/settings
func (h *OpsMgmtHandler) UpdateSettings(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	var req dto.OpsSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	current, err := h.service.Settings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updated, err := h.service.UpdateSettings(c.Request.Context(), req.Apply(current))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated)
}

// ListGroupMargins 本地分组最低利润率列表（含生效值与来源）。
// GET /admin/ops_mgmt/group-margins
func (h *OpsMgmtHandler) ListGroupMargins(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	groups, err := h.service.ListGroupMargins(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": groups, "total": len(groups)})
}

// SetGroupMargin 设置本地分组最低利润率（basis_points=null 表示继承全局）。
// PUT /admin/ops_mgmt/group-margins/:id
// PUT /admin/groups/:id/ops_mgmt-margin
func (h *OpsMgmtHandler) SetGroupMargin(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	id, ok := parseOpsMgmtID(c, "id")
	if !ok {
		return
	}
	var req dto.OpsGroupMarginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	group, err := h.service.SetGroupMargin(c.Request.Context(), id, req.Resolve())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, group)
}

// ListGuardLocks active 守护锁（审计视图）。
// GET /admin/ops_mgmt/guard-locks?site_id=
func (h *OpsMgmtHandler) ListGuardLocks(c *gin.Context) {
	if !h.ensureService(c) {
		return
	}
	locks, err := h.service.ListActiveGuardLocks(c.Request.Context(), parseOpsMgmtInt64Query(c, "site_id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": locks, "total": len(locks)})
}

func parseOpsMgmtInt64Query(c *gin.Context, name string) int64 {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func parseIntQueryDefault(c *gin.Context, name string, fallback int) int {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func pagesForOpsMgmt(total int64, pageSize int) int {
	if pageSize <= 0 {
		return 0
	}
	pages := int(total) / pageSize
	if int(total)%pageSize != 0 {
		pages++
	}
	return pages
}

// ensure infraerrors 导入被使用（错误映射由 response.ErrorFrom 统一处理）。
var _ = infraerrors.BadRequest
