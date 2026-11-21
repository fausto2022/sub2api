package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelPlazaModelType(t *testing.T) {
	price := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"known-vision-chat": {Mode: "chat", InputCostPerImageToken: 0.1},
		"gpt-image-99":      {Mode: "embedding"},
		"sample-video":      {Mode: "video_generation"},
		"sample-image":      {Mode: "image_generation"},
		"sample-audio":      {Mode: "audio_transcription"},
		"sample-rerank":     {Mode: "rerank"},
		"claude-opus-4":     {Mode: "chat"},
	}}
	svc := &ModelPlazaService{pricingService: price}
	memo := make(map[string]PlazaModelType)
	for _, tc := range []struct {
		name, model string
		mode        BillingMode
		want        PlazaModelType
	}{
		{"text with image input", "known-vision-chat", BillingModeImage, PlazaModelTypeText},
		{"catalog wins over family and billing", "gpt-image-99", BillingModeVideo, PlazaModelTypeEmbedding},
		{"catalog video", "sample-video", BillingModeToken, PlazaModelTypeVideo},
		{"catalog image", "sample-image", BillingModeToken, PlazaModelTypeImage},
		{"catalog audio", "sample-audio", BillingModePerRequest, PlazaModelTypeAudio},
		{"catalog unsupported mode", "sample-rerank", BillingModeVideo, PlazaModelTypeOther},
		{"concrete image family", "models/gemini-3-pro-image-preview", BillingModeToken, PlazaModelTypeImage},
		{"concrete video family", "xai/grok-imagine-video-1.5", BillingModePerRequest, PlazaModelTypeVideo},
		{"concrete audio family", "gpt-4o-mini-tts", BillingModeToken, PlazaModelTypeAudio},
		{"concrete embedding family", "text-embedding-3-small", BillingModeToken, PlazaModelTypeEmbedding},
		{"concrete text family", "claude-sonnet-4", BillingModePerRequest, PlazaModelTypeText},
		{"explicit image billing", "custom-model", BillingModeImage, PlazaModelTypeImage},
		{"explicit video billing", "custom-model", BillingModeVideo, PlazaModelTypeVideo},
		{"token alone", "custom-model", BillingModeToken, PlazaModelTypeOther},
		{"per request alone", "custom-model", BillingModePerRequest, PlazaModelTypeOther},
		{"loose pricing family match", "custom-opus-model", BillingModeToken, PlazaModelTypeOther},
		{"unknown GPT family", "gpt-unrelated-custom", BillingModeToken, PlazaModelTypeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &PlazaModel{Name: tc.model, Pricing: &ChannelModelPricing{BillingMode: tc.mode}}
			require.Equal(t, tc.want, svc.plazaModelType(model, &Group{}, memo))
		})
	}

	t.Run("explicit group billing wins over channel billing", func(t *testing.T) {
		model := &PlazaModel{Name: "custom-model", Pricing: &ChannelModelPricing{BillingMode: BillingModeImage}}
		group := &Group{ModelPricing: []ChannelModelPricing{{Models: []string{"custom-model"}, BillingMode: BillingModeVideo}}}
		require.Equal(t, PlazaModelTypeVideo, svc.plazaModelType(model, group, memo))
	})
}
