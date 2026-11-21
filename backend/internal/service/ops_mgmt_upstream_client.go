package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// OpsUpstreamClient 上游站点只读客户端（design.md §2.2）。
//
// 两种鉴权模式并存，站点表用 auth_mode 选择：
//   - password（v3.0 默认）：POST {base}/api/v1/auth/login 换 access/refresh token，
//     之后仅用普通用户接口 GET /api/v1/groups/available 与 GET /api/v1/groups/rates
//     读取「该账号可见的分组 + 用户专属倍率」。**不调用 /admin/***。
//   - api_key（可选兼容）：上游恰好提供管理员 Key 时，用 x-api-key 调
//     GET /api/v1/admin/groups 与 GET /api/v1/admin/accounts，可拿到全量分组定价
//     （ratio / peak / image / video 独立倍率）与账号（credentials / group_ids /
//     rate_multiplier / schedulable）。没有管理员 Key 的站点不要用这个模式。
//
// 本客户端只读：除 login/refresh 外不发送任何写请求。
const (
	opsUpstreamLoginPath   = "/api/v1/auth/login"
	opsUpstreamRefreshPath = "/api/v1/auth/refresh"
	opsUpstreamGroupsPath  = "/api/v1/groups/available"
	opsUpstreamRatesPath   = "/api/v1/groups/rates"
	opsUpstreamProfilePath = "/api/v1/profile"
	opsAdminGroupsPath     = "/api/v1/admin/groups"
	opsAdminAccountsPath   = "/api/v1/admin/accounts"

	opsUpstreamDefaultTimeout = 20 * time.Second
	opsUpstreamMaxBodyBytes   = 8 << 20
)

// OpsUpstreamToken 登录/续期拿到的令牌。
type OpsUpstreamToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	ExpiresAt    *time.Time
}

// OpsUpstreamRemoteGroup 上游分组（/groups/available 或 /admin/groups 的一行）。
type OpsUpstreamRemoteGroup struct {
	ID                int64
	Name              string
	Platform          string
	Ratio             float64
	RateMultiplier    float64
	PeakEnabled       bool
	PeakStart         string
	PeakEnd           string
	PeakMultiplier    float64
	ImageSeparateRate bool
	ImageMultiplier   float64
	VideoSeparateRate bool
	VideoMultiplier   float64
	IsExclusive       bool
	Status            string
	SubscriptionType  string
}

// OpsUpstreamRemoteAccount 上游账号（仅 api_key 模式可拉）。
type OpsUpstreamRemoteAccount struct {
	ID             int64
	Name           string
	Platform       string
	Type           string
	Status         string
	Schedulable    *bool
	RateMultiplier *float64
	GroupIDs       []int64
	Credentials    map[string]any
}

// OpsUpstreamProfile 当前登录账号的余额/信息（连通性测试的附加信息）。
type OpsUpstreamProfile struct {
	ID       int64
	Email    string
	Balance  float64
	Currency string
}

// OpsUpstreamClient 只读 HTTP 客户端。
type OpsUpstreamClient struct {
	httpClient *http.Client
	now        func() time.Time
}

// NewOpsUpstreamClient 构造客户端；httpClient 为空时使用带超时的默认实现。
func NewOpsUpstreamClient(httpClient *http.Client) *OpsUpstreamClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: opsUpstreamDefaultTimeout}
	}
	return &OpsUpstreamClient{httpClient: httpClient, now: time.Now}
}

// NormalizeOpsBaseURL 规范化站点地址：去空白与尾部斜杠，只接受 http/https。
func NormalizeOpsBaseURL(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", fmt.Errorf("base url is empty")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid base url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("base url must use http or https")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("base url must include host")
	}
	return value, nil
}

type opsUpstreamEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Reason  string          `json:"reason"`
	Data    json.RawMessage `json:"data"`
}

// Login 用账号密码登录上游，换取 access/refresh token。
func (c *OpsUpstreamClient) Login(ctx context.Context, baseURL, username, password string) (*OpsUpstreamToken, error) {
	payload := map[string]any{"email": username, "password": password}
	raw, err := c.doJSON(ctx, http.MethodPost, baseURL, opsUpstreamLoginPath, "", "", payload)
	if err != nil {
		return nil, err
	}
	return c.parseToken(raw)
}

// Refresh 用 refresh_token 续期。
func (c *OpsUpstreamClient) Refresh(ctx context.Context, baseURL, refreshToken string) (*OpsUpstreamToken, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("refresh token is empty")
	}
	payload := map[string]any{"refresh_token": refreshToken}
	raw, err := c.doJSON(ctx, http.MethodPost, baseURL, opsUpstreamRefreshPath, "", "", payload)
	if err != nil {
		return nil, err
	}
	return c.parseToken(raw)
}

func (c *OpsUpstreamClient) parseToken(raw json.RawMessage) (*OpsUpstreamToken, error) {
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if strings.TrimSpace(body.AccessToken) == "" {
		return nil, fmt.Errorf("upstream login response has no access_token")
	}
	token := &OpsUpstreamToken{
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		ExpiresIn:    body.ExpiresIn,
	}
	if body.ExpiresIn > 0 {
		expiresAt := c.now().Add(time.Duration(body.ExpiresIn) * time.Second)
		token.ExpiresAt = &expiresAt
	}
	return token, nil
}

// ListAvailableGroups 普通用户接口：该账号可见的全部分组（含平台与倍率）。
func (c *OpsUpstreamClient) ListAvailableGroups(ctx context.Context, baseURL, accessToken string) ([]OpsUpstreamRemoteGroup, error) {
	raw, err := c.doJSON(ctx, http.MethodGet, baseURL, opsUpstreamGroupsPath, accessToken, "", nil)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decode available groups: %w", err)
	}
	groups := make([]OpsUpstreamRemoteGroup, 0, len(items))
	for _, item := range items {
		groups = append(groups, opsGroupFromMap(item))
	}
	return groups, nil
}

// ListGroupRates 普通用户接口：用户专属分组倍率（group_id → 倍率）。
func (c *OpsUpstreamClient) ListGroupRates(ctx context.Context, baseURL, accessToken string) (map[int64]float64, error) {
	raw, err := c.doJSON(ctx, http.MethodGet, baseURL, opsUpstreamRatesPath, accessToken, "", nil)
	if err != nil {
		return nil, err
	}
	rates := make(map[int64]float64)
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return rates, nil
	}
	var rawMap map[string]any
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return nil, fmt.Errorf("decode group rates: %w", err)
	}
	for key, value := range rawMap {
		id, convErr := strconv.ParseInt(strings.TrimSpace(key), 10, 64)
		if convErr != nil {
			continue
		}
		if rate, ok := opsNumber(value); ok {
			rates[id] = rate
		}
	}
	return rates, nil
}

// GetProfile 读取当前登录账号信息（余额/邮箱），用于连通性测试。
func (c *OpsUpstreamClient) GetProfile(ctx context.Context, baseURL, accessToken string) (*OpsUpstreamProfile, error) {
	raw, err := c.doJSON(ctx, http.MethodGet, baseURL, opsUpstreamProfilePath, accessToken, "", nil)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode profile: %w", err)
	}
	profile := &OpsUpstreamProfile{}
	if id, ok := opsInt64(body["id"]); ok {
		profile.ID = id
	}
	if email, ok := body["email"].(string); ok {
		profile.Email = email
	}
	if balance, ok := opsNumber(body["balance"]); ok {
		profile.Balance = balance
	}
	if currency, ok := body["currency"].(string); ok {
		profile.Currency = currency
	}
	return profile, nil
}

// ListAdminGroups x-api-key 模式：管理员分组列表（含 ratio/peak/image/video 独立倍率）。
func (c *OpsUpstreamClient) ListAdminGroups(ctx context.Context, baseURL, apiKey string, pageSize int) ([]OpsUpstreamRemoteGroup, error) {
	if pageSize <= 0 {
		pageSize = 200
	}
	path := fmt.Sprintf("%s?page=1&page_size=%d", opsAdminGroupsPath, pageSize)
	raw, err := c.doJSON(ctx, http.MethodGet, baseURL, path, "", apiKey, nil)
	if err != nil {
		return nil, err
	}
	items, err := opsExtractItems(raw)
	if err != nil {
		return nil, err
	}
	groups := make([]OpsUpstreamRemoteGroup, 0, len(items))
	for _, item := range items {
		groups = append(groups, opsGroupFromMap(item))
	}
	return groups, nil
}

// ListAdminAccounts x-api-key 模式：管理员账号列表（含 credentials/group_ids/rate_multiplier/schedulable）。
func (c *OpsUpstreamClient) ListAdminAccounts(ctx context.Context, baseURL, apiKey string, pageSize int) ([]OpsUpstreamRemoteAccount, error) {
	if pageSize <= 0 {
		pageSize = 200
	}
	path := fmt.Sprintf("%s?page=1&page_size=%d", opsAdminAccountsPath, pageSize)
	raw, err := c.doJSON(ctx, http.MethodGet, baseURL, path, "", apiKey, nil)
	if err != nil {
		return nil, err
	}
	items, err := opsExtractItems(raw)
	if err != nil {
		return nil, err
	}
	accounts := make([]OpsUpstreamRemoteAccount, 0, len(items))
	for _, item := range items {
		account := OpsUpstreamRemoteAccount{}
		if id, ok := opsInt64(item["id"]); ok {
			account.ID = id
		}
		if account.ID <= 0 {
			continue
		}
		account.Name, _ = item["name"].(string)
		account.Platform, _ = item["platform"].(string)
		account.Type, _ = item["type"].(string)
		account.Status, _ = item["status"].(string)
		if value, ok := item["schedulable"].(bool); ok {
			account.Schedulable = &value
		}
		if value, ok := opsNumber(item["rate_multiplier"]); ok {
			account.RateMultiplier = &value
		}
		account.GroupIDs = opsInt64Slice(item["group_ids"])
		if credentials, ok := item["credentials"].(map[string]any); ok {
			account.Credentials = credentials
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

// opsExtractItems 兼容 {items:[...]} 与裸数组两种响应体。
func opsExtractItems(raw json.RawMessage) ([]map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var items []map[string]any
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fmt.Errorf("decode items array: %w", err)
		}
		return items, nil
	}
	var wrapper struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err != nil {
		return nil, fmt.Errorf("decode items wrapper: %w", err)
	}
	return wrapper.Items, nil
}

// opsGroupFromMap 从上游分组 JSON 提取字段，键名兼容 snake_case 变体。
func opsGroupFromMap(item map[string]any) OpsUpstreamRemoteGroup {
	group := OpsUpstreamRemoteGroup{}
	if id, ok := opsInt64(item["id"]); ok {
		group.ID = id
	}
	group.Name, _ = item["name"].(string)
	group.Platform, _ = item["platform"].(string)
	group.Status, _ = item["status"].(string)
	group.SubscriptionType, _ = item["subscription_type"].(string)
	if value, ok := opsNumber(item["ratio"]); ok {
		group.Ratio = value
	}
	if value, ok := opsNumber(firstNonNil(item["rate_multiplier"], item["rateMultiplier"])); ok {
		group.RateMultiplier = value
	}
	if group.RateMultiplier <= 0 && group.Ratio > 0 {
		group.RateMultiplier = group.Ratio
	}
	if value, ok := item["peak_enabled"].(bool); ok {
		group.PeakEnabled = value
	} else if value, ok := item["peak_rate_enabled"].(bool); ok {
		group.PeakEnabled = value
	}
	group.PeakStart, _ = firstNonNil(item["peak_start"], item["peakStart"]).(string)
	group.PeakEnd, _ = firstNonNil(item["peak_end"], item["peakEnd"]).(string)
	if value, ok := opsNumber(firstNonNil(item["peak_multiplier"], item["peak_rate_multiplier"])); ok {
		group.PeakMultiplier = value
	}
	if value, ok := item["image_separate_rate"].(bool); ok {
		group.ImageSeparateRate = value
	} else if value, ok := item["image_rate_independent"].(bool); ok {
		group.ImageSeparateRate = value
	}
	if value, ok := opsNumber(firstNonNil(item["image_separate_rate_multiplier"], item["image_rate_multiplier"])); ok {
		group.ImageMultiplier = value
	}
	if value, ok := item["video_separate_rate"].(bool); ok {
		group.VideoSeparateRate = value
	} else if value, ok := item["video_rate_independent"].(bool); ok {
		group.VideoSeparateRate = value
	}
	if value, ok := opsNumber(firstNonNil(item["video_separate_rate_multiplier"], item["video_rate_multiplier"])); ok {
		group.VideoMultiplier = value
	}
	if value, ok := item["is_exclusive"].(bool); ok {
		group.IsExclusive = value
	}
	return group
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func opsNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func opsInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func opsInt64Slice(value any) []int64 {
	raw, ok := value.([]any)
	if !ok {
		return []int64{}
	}
	out := make([]int64, 0, len(raw))
	for _, item := range raw {
		if id, ok := opsInt64(item); ok {
			out = append(out, id)
		}
	}
	return out
}

// doJSON 发送请求并解包 {code,message,data} 信封，返回 data 的原始 JSON。
func (c *OpsUpstreamClient) doJSON(ctx context.Context, method, baseURL, path, bearerToken, apiKey string, payload any) (json.RawMessage, error) {
	normalized, err := NormalizeOpsBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encode payload: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, normalized+path, body)
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token := strings.TrimSpace(bearerToken); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		request.Header.Set("x-api-key", key)
	}

	// 注意：错误信息只带状态码与上游 message，绝不回显 token / api key。
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request upstream %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(response.Body, opsUpstreamMaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read upstream %s response: %w", path, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: upstream returned HTTP %d: %s", path, response.StatusCode, opsUpstreamErrorText(raw))
	}
	var envelope opsUpstreamEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%s: decode upstream envelope: %w", path, err)
	}
	if envelope.Code != 0 {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = strings.TrimSpace(envelope.Reason)
		}
		if message == "" {
			message = "unknown upstream error"
		}
		return nil, fmt.Errorf("%s: upstream error code %d: %s", path, envelope.Code, message)
	}
	if len(bytes.TrimSpace(envelope.Data)) == 0 {
		return json.RawMessage("null"), nil
	}
	return envelope.Data, nil
}

func opsUpstreamErrorText(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "empty response"
	}
	var envelope opsUpstreamEnvelope
	if err := json.Unmarshal(raw, &envelope); err == nil {
		if message := strings.TrimSpace(envelope.Message); message != "" {
			return message
		}
		if reason := strings.TrimSpace(envelope.Reason); reason != "" {
			return reason
		}
	}
	if len(trimmed) > 200 {
		return trimmed[:200]
	}
	return trimmed
}

// OpsUpstreamAPIKeyHash 计算上游 Key 的 SHA-256（去重与审计展示，不存明文）。
func OpsUpstreamAPIKeyHash(apiKey string) string {
	return opsSHA256Hex(strings.TrimSpace(apiKey))
}
