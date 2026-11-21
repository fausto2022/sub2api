package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// 上游客户端单测：对齐 design.md 第四部分 B1（站点对接后能拉分组与倍率）。
//
// 关键约束（v3.0 用户澄清）：
//   - 上游**只有账号密码**，没有管理员 Key，因此主路径必须是 POST /api/v1/auth/login
//   - 分组与倍率走**普通用户接口** GET /api/v1/groups/available 与 GET /api/v1/groups/rates
//   - 不得依赖 /admin/* 接口

func newUpstreamTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestOpsUpstreamLoginUsesPasswordEndpoint(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := newUpstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"acc-1","refresh_token":"ref-1","expires_in":3600}}`))
	})

	client := NewOpsUpstreamClient(srv.Client())
	token, err := client.Login(context.Background(), srv.URL, "ops@example.com", "secret")
	require.NoError(t, err)
	require.NotNil(t, token)

	// 必须是密码登录端点，而不是管理员接口
	require.Equal(t, "/api/v1/auth/login", gotPath)
	require.Equal(t, "ops@example.com", gotBody["email"])
	require.Equal(t, "secret", gotBody["password"])

	require.Equal(t, "acc-1", token.AccessToken)
	require.Equal(t, "ref-1", token.RefreshToken)
}

func TestOpsUpstreamRefreshRotatesToken(t *testing.T) {
	var gotPath string
	srv := newUpstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"acc-2","refresh_token":"ref-2","expires_in":1800}}`))
	})

	client := NewOpsUpstreamClient(srv.Client())
	token, err := client.Refresh(context.Background(), srv.URL, "ref-1")
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Contains(t, gotPath, "/api/v1/auth/")
	require.Equal(t, "acc-2", token.AccessToken)
}

func TestOpsUpstreamListGroupsAndRates(t *testing.T) {
	srv := newUpstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// 普通用户接口需要 Bearer 鉴权
		if r.Header.Get("Authorization") != "Bearer acc-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/groups/available":
			_, _ = w.Write([]byte(`{"code":0,"data":[
				{"id":1,"name":"claude-pro","platform":"claude","rate_multiplier":0.85,
				 "peak_enabled":true,"peak_start":"09:00","peak_end":"18:00","peak_multiplier":1.5,
				 "image_separate_rate":true,"image_rate_multiplier":2},
				{"id":2,"name":"gpt-plus","platform":"openai","rate_multiplier":1.2}
			]}`))
		case "/api/v1/groups/rates":
			// 上游真实实现返回 map[int64]float64，JSON 序列化为以分组 ID 为 key 的对象
			_, _ = w.Write([]byte(`{"code":0,"data":{"1":0.8,"2":1.1}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	client := NewOpsUpstreamClient(srv.Client())

	groups, err := client.ListAvailableGroups(context.Background(), srv.URL, "acc-1")
	require.NoError(t, err)
	require.Len(t, groups, 2)

	require.Equal(t, int64(1), groups[0].ID)
	require.Equal(t, "claude-pro", groups[0].Name)
	require.Equal(t, "claude", groups[0].Platform)
	require.InDelta(t, 0.85, groups[0].RateMultiplier, 1e-9)
	// 峰谷与图片独立倍率必须解析出来（利润评估要用）
	require.True(t, groups[0].PeakEnabled)
	require.Equal(t, "09:00", groups[0].PeakStart)
	require.Equal(t, "18:00", groups[0].PeakEnd)
	require.InDelta(t, 1.5, groups[0].PeakMultiplier, 1e-9)
	require.True(t, groups[0].ImageSeparateRate)
	require.InDelta(t, 2.0, groups[0].ImageMultiplier, 1e-9)

	require.Equal(t, "gpt-plus", groups[1].Name)
	require.InDelta(t, 1.2, groups[1].RateMultiplier, 1e-9)

	rates, err := client.ListGroupRates(context.Background(), srv.URL, "acc-1")
	require.NoError(t, err)
	require.Len(t, rates, 2)
	require.InDelta(t, 0.8, rates[1], 1e-9)
	require.InDelta(t, 1.1, rates[2], 1e-9)
}

func TestOpsUpstreamRejectsNonZeroBizCode(t *testing.T) {
	srv := newUpstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":401,"message":"invalid credentials"}`))
	})

	client := NewOpsUpstreamClient(srv.Client())
	token, err := client.Login(context.Background(), srv.URL, "a@b.c", "wrong")
	// 业务码非 0 必须报错，不能当成功返回空 token
	require.Error(t, err)
	require.Nil(t, token)
}

func TestOpsNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://api.example.com":     "https://api.example.com",
		"https://api.example.com/":    "https://api.example.com",
		"https://api.example.com/v1/": "https://api.example.com/v1",
		"  https://x.test/  ":         "https://x.test",
	}
	for input, want := range cases {
		got, err := NormalizeOpsBaseURL(input)
		require.NoError(t, err, "input=%q", input)
		require.Equal(t, want, got, "input=%q", input)
	}

	// 非法输入必须报错
	for _, bad := range []string{"", "   ", "not-a-url", "ftp://x.test"} {
		_, err := NormalizeOpsBaseURL(bad)
		require.Error(t, err, "input=%q", bad)
	}
}
