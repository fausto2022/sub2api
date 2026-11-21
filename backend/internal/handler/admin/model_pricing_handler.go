package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ModelPricingHandler 是内置模型价格与人民币计费口径的管理端入口。
type ModelPricingHandler struct {
	service *service.ModelPricingAdminService
}

// NewModelPricingHandler 创建模型价格管理 handler。
func NewModelPricingHandler(modelPricingService *service.ModelPricingAdminService) *ModelPricingHandler {
	return &ModelPricingHandler{service: modelPricingService}
}

// List 返回内置价格列表（分页/搜索/供应商与覆盖筛选）。
// GET /api/v1/admin/model-pricing
func (h *ModelPricingHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	filter := service.ModelPricingListFilter{
		Page:     page,
		PageSize: pageSize,
		Search:   c.Query("search"),
		Provider: c.Query("provider"),
		Platform: c.Query("platform"),
	}
	if raw := strings.TrimSpace(c.Query("overridden")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			filter.Overridden = &parsed
		}
	}
	if raw := strings.TrimSpace(c.Query("custom")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			filter.Custom = &parsed
		}
	}
	if raw := strings.TrimSpace(c.Query("peak_enabled")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			filter.PeakEnabled = &parsed
		}
	}
	result := h.service.List(filter)
	response.Success(c, dto.ModelPricingListFromService(result))
}

// UpdateOverride 写入单个模型的 USD 覆盖与 `_catalog` 元数据（字段级 null 删除）。
// PUT /api/v1/admin/model-pricing/overrides/:model
func (h *ModelPricingHandler) UpdateOverride(c *gin.Context) {
	model := strings.TrimSpace(c.Param("model"))
	if model == "" {
		response.ErrorFrom(c, service.NewInvalidModelPriceError("model is required"))
		return
	}
	var req dto.UpdateModelPricingOverrideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, service.NewInvalidModelPriceError("invalid request body: "+err.Error()))
		return
	}
	body, err := dto.ParseUpdateModelPricingOverride(req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	entry, err := h.service.UpdateOverride(service.ModelPricingOverrideInput{
		Model:       model,
		Fields:      body.Fields,
		Platform:    body.Platform,
		Custom:      body.Custom,
		PeakEnabled: body.PeakEnabled,
		Peak:        body.Peak,
		OffPeak:     body.OffPeak,
		UpdatedBy:   modelPricingOperatorID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ModelPricingItemFromService(entry))
}

// CreateModel 新增模型（允许目录外）；与目录/自定义条目重名返回 409 MODEL_ALREADY_EXISTS。
// POST /api/v1/admin/model-pricing/models
func (h *ModelPricingHandler) CreateModel(c *gin.Context) {
	var req dto.CreateModelPricingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, service.NewInvalidModelPriceError("invalid request body: "+err.Error()))
		return
	}
	entry, err := h.service.CreateModel(service.ModelPricingCreateInput{
		Model:       req.Model,
		Platform:    req.Platform,
		Fields:      req.Fields(),
		PeakEnabled: req.PeakEnabled,
		Peak:        req.Peak,
		OffPeak:     req.OffPeak,
		UpdatedBy:   modelPricingOperatorID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ModelPricingItemFromService(entry))
}

// DeleteModel 删除管理员自定义的模型。
// DELETE /api/v1/admin/model-pricing/models/:model
func (h *ModelPricingHandler) DeleteModel(c *gin.Context) {
	model := strings.TrimSpace(c.Param("model"))
	if model == "" {
		response.ErrorFrom(c, service.NewInvalidModelPriceError("model is required"))
		return
	}
	if err := h.service.DeleteModel(model); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"model": model, "deleted": true})
}

// GetPeakSettings 返回全局峰谷时段设置。
// GET /api/v1/admin/model-pricing/peak-settings
func (h *ModelPricingHandler) GetPeakSettings(c *gin.Context) {
	settings := h.service.PeakSettings(c.Request.Context())
	response.Success(c, dto.PeakPricingSettingsResponseFromService(settings))
}

// UpdatePeakSettings 保存全局峰谷时段设置（整体替换，保存后热生效）。
// PUT /api/v1/admin/model-pricing/peak-settings
func (h *ModelPricingHandler) UpdatePeakSettings(c *gin.Context) {
	var req dto.UpdatePeakPricingSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, service.NewInvalidPeakPricingSettingsError("invalid request body: "+err.Error()))
		return
	}
	periods := make([]service.PeakPricingPeriod, 0, len(req.Periods))
	for _, period := range req.Periods {
		periods = append(periods, service.PeakPricingPeriod{Start: period.Start, End: period.End})
	}
	saved, err := h.service.UpdatePeakSettings(c.Request.Context(), service.PeakPricingSettings{
		Timezone:       req.Timezone,
		WeekdayOnly:    req.WeekdayOnly,
		WeekendOffPeak: req.WeekendOffPeak,
		Periods:        periods,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.PeakPricingSettingsResponseFromService(saved))
}

// modelPricingOperatorID 从上下文取管理员用户 ID，用于 `_catalog.updated_by`。
// 取不到（如内部调用/测试）时返回 0，不阻断写入。
func modelPricingOperatorID(c *gin.Context) int64 {
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		return subject.UserID
	}
	return 0
}

// DeleteOverride 删除该模型的覆盖（恢复内置）。
// DELETE /api/v1/admin/model-pricing/overrides/:model
func (h *ModelPricingHandler) DeleteOverride(c *gin.Context) {
	model := strings.TrimSpace(c.Param("model"))
	if model == "" {
		response.ErrorFrom(c, service.NewInvalidModelPriceError("model is required"))
		return
	}
	entry, err := h.service.DeleteOverride(model)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ModelPricingItemFromService(entry))
}

// GetCurrency 返回人民币口径配置与逐模型人民币价。
// GET /api/v1/admin/model-pricing/currency
func (h *ModelPricingHandler) GetCurrency(c *gin.Context) {
	response.Success(c, dto.ModelPricingCurrencyFromService(h.service.Currency()))
}

// UpdateCurrency 更新人民币口径（mode + cny_per_usd）。
// PUT /api/v1/admin/model-pricing/currency
func (h *ModelPricingHandler) UpdateCurrency(c *gin.Context) {
	var req dto.UpdateModelPricingCurrencyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, service.NewInvalidCNYModeError("invalid request body: "+err.Error()))
		return
	}
	state, err := h.service.UpdateCurrency(req.Mode, req.CNYPerUSD)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ModelPricingCurrencyFromService(state))
}

// UpsertCurrencyModel 写入单个模型的人民币原生价。
// PUT /api/v1/admin/model-pricing/currency/models/:model
func (h *ModelPricingHandler) UpsertCurrencyModel(c *gin.Context) {
	model := strings.TrimSpace(c.Param("model"))
	if model == "" {
		response.ErrorFrom(c, service.NewInvalidCNYPriceError("model is required"))
		return
	}
	var req dto.UpdateModelPricingCNYModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, service.NewInvalidCNYPriceError("invalid request body: "+err.Error()))
		return
	}
	state, err := h.service.UpsertCurrencyModel(model, map[string]*float64(req))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ModelPricingCurrencyFromService(state))
}

// DeleteCurrencyModel 删除该模型的人民币登记。
// DELETE /api/v1/admin/model-pricing/currency/models/:model
func (h *ModelPricingHandler) DeleteCurrencyModel(c *gin.Context) {
	model := strings.TrimSpace(c.Param("model"))
	if model == "" {
		response.ErrorFrom(c, service.NewInvalidCNYPriceError("model is required"))
		return
	}
	state, err := h.service.DeleteCurrencyModel(model)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ModelPricingCurrencyFromService(state))
}
