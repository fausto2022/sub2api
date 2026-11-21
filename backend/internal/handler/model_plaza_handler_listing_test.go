package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type plazaListingGroups struct{ service.GroupRepository }

func (plazaListingGroups) ListActive(context.Context) ([]service.Group, error) {
	return []service.Group{
		{ID: 1, Name: "Public empty", Platform: service.PlatformOpenAI},
		{ID: 2, Name: "Public subscription", Platform: service.PlatformOpenAI, SubscriptionType: "subscription"},
		{ID: 3, Name: "Explicit private", Platform: service.PlatformOpenAI, IsExclusive: true},
		{ID: 4, Name: "Subscribed private empty", Platform: service.PlatformOpenAI, IsExclusive: true, SubscriptionType: "subscription"},
		{ID: 5, Name: "Hidden private", Description: "hidden-description", Platform: service.PlatformOpenAI, IsExclusive: true, SubscriptionType: "subscription"},
	}, nil
}

type plazaListingChannels struct{ service.ChannelRepository }

func (plazaListingChannels) ListAll(context.Context) ([]service.Channel, error) {
	return []service.Channel{
		{Status: service.StatusActive, GroupIDs: []int64{2}, ModelPricing: []service.ChannelModelPricing{
			{Platform: service.PlatformOpenAI, Models: []string{"gpt-image-2"}, BillingMode: service.BillingModeToken, InputPrice: new(0.01)},
		}},
		{Status: service.StatusActive, GroupIDs: []int64{3}, ModelPricing: []service.ChannelModelPricing{
			{Platform: service.PlatformOpenAI, Models: []string{"private-granted-model"}},
		}},
		{Status: service.StatusActive, GroupIDs: []int64{5}, ModelPricing: []service.ChannelModelPricing{
			{Platform: service.PlatformOpenAI, Models: []string{"private-hidden-model"}},
		}},
	}, nil
}

type plazaListingUsers struct{ service.UserRepository }

func (plazaListingUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	user := &service.User{ID: id, RestrictPublicGroups: true}
	if id == 11 {
		user.AllowedGroups = []int64{3, 99}
	}
	return user, nil
}

type plazaListingSubscriptions struct {
	service.UserSubscriptionRepository
	subscriptions []service.UserSubscription
}

func (r plazaListingSubscriptions) ListActiveByUserID(_ context.Context, id int64) ([]service.UserSubscription, error) {
	var active []service.UserSubscription
	for _, sub := range r.subscriptions {
		if sub.UserID == id && sub.IsActive() {
			active = append(active, sub)
		}
	}
	return active, nil
}

type plazaListingSettings struct {
	service.SettingRepository
	values map[string]string
}

func (r plazaListingSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}

func TestModelPlazaHandler_ListingVisibilityAndPrivacy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	groups := plazaListingGroups{}
	now := time.Now()
	subs := plazaListingSubscriptions{subscriptions: []service.UserSubscription{
		{UserID: 12, GroupID: 4, Status: service.SubscriptionStatusActive, ExpiresAt: now.Add(time.Hour)},
		{UserID: 12, GroupID: 5, Status: service.SubscriptionStatusActive, ExpiresAt: now.Add(-time.Hour)},
		{UserID: 13, GroupID: 5, Status: service.SubscriptionStatusActive, ExpiresAt: now.Add(time.Hour)},
	}}
	h := NewModelPlazaHandler(
		service.NewModelPlazaService(plazaListingChannels{}, groups, nil, nil, nil),
		service.NewAPIKeyService(nil, plazaListingUsers{}, groups, subs, nil, nil, cfg),
		service.NewSettingService(plazaListingSettings{}, cfg),
	)

	for _, tc := range []struct {
		name   string
		userID int64
		ids    []int64
	}{
		{name: "anonymous", ids: []int64{1, 2}},
		{name: "authenticated without private access", userID: 10, ids: []int64{1, 2}},
		{name: "explicit private access", userID: 11, ids: []int64{1, 2, 3}},
		{name: "active subscription", userID: 12, ids: []int64{1, 2, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(middleware.CORS(config.CORSConfig{AllowedOrigins: []string{"https://a.example", "https://b.example"}}))
			r.GET("/api/v1/model-plaza", func(c *gin.Context) {
				if tc.userID != 0 {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: tc.userID})
				}
				h.Get(c)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza", nil)
			req.Header.Set("Origin", "https://a.example")
			req.Header.Set("Accept-Language", "en")
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
			require.Equal(t, []string{"Origin", "Accept-Language"}, w.Header().Values("Vary"))
			require.NotContains(t, w.Body.String(), "hidden-description")
			require.NotContains(t, w.Body.String(), "private-hidden-model")
			if tc.userID != 11 {
				require.NotContains(t, w.Body.String(), "private-granted-model")
			}

			var body struct{ Data modelPlazaResponse }
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			var ids []int64
			for _, group := range body.Data.Groups {
				ids = append(ids, group.ID)
				switch group.ID {
				case 1, 4:
					require.NotNil(t, group.Models, "empty group models must be [] in JSON")
					require.Empty(t, group.Models)
					require.Zero(t, group.CatalogModelCount)
				case 2:
					require.Len(t, group.Models, 1)
					require.Equal(t, service.PlazaModelTypeImage, group.Models[0].ModelType)
					require.Equal(t, "token", group.Models[0].Pricing.BillingMode)
					require.InDelta(t, 0.01, *group.Models[0].Pricing.InputPrice, 1e-12)
				case 3:
					require.Len(t, group.Models, 1)
					require.Equal(t, service.PlazaModelTypeOther, group.Models[0].ModelType)
				}
			}
			require.ElementsMatch(t, tc.ids, ids)
		})
	}
}

func TestModelPlazaHandler_ListingRespectsRuntimeGates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]string
		status int
	}{
		{name: "disabled", values: map[string]string{service.SettingKeyModelPlazaEnabled: "false"}, status: http.StatusNotFound},
		{name: "requires authentication", values: map[string]string{service.SettingKeyModelPlazaRequireAuth: "true"}, status: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewModelPlazaHandler(nil, nil, service.NewSettingService(plazaListingSettings{values: tc.values}, &config.Config{}))
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza", nil)
			h.Get(c)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
		})
	}
}
