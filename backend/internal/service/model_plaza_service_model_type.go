package service

import "strings"

// PlazaModelType describes the model's primary use independently of how it is billed.
type PlazaModelType string

const (
	PlazaModelTypeText      PlazaModelType = "text"
	PlazaModelTypeImage     PlazaModelType = "image"
	PlazaModelTypeVideo     PlazaModelType = "video"
	PlazaModelTypeAudio     PlazaModelType = "audio"
	PlazaModelTypeEmbedding PlazaModelType = "embedding"
	PlazaModelTypeOther     PlazaModelType = "other"
)

func (s *ModelPlazaService) plazaModelType(m *PlazaModel, g *Group, memo map[string]PlazaModelType) PlazaModelType {
	// Only memoize model-level evidence. Billing fallbacks can differ per group or channel.
	modelType, ok := memo[m.Name]
	if !ok {
		modelType = plazaCatalogModelType(s.pricingService.GetIdentifiedModelPricing(m.Name))
		if modelType == "" {
			modelType = plazaKnownModelFamilyType(m.Name)
		}
		memo[m.Name] = modelType
	}
	if modelType != "" {
		return modelType
	}

	pricing := m.Pricing
	if groupPricing := matchGroupModelPricing(g, m.Name); groupPricing != nil {
		pricing = groupPricing
	}
	switch plazaBillingModeOf(pricing) {
	case BillingModeImage:
		return PlazaModelTypeImage
	case BillingModeVideo:
		return PlazaModelTypeVideo
	default:
		return PlazaModelTypeOther
	}
}

func plazaCatalogModelType(pricing *LiteLLMModelPricing) PlazaModelType {
	if pricing == nil {
		return ""
	}
	// Mode describes the catalog model's use. A pricing fallback by loose family
	// match would not provide reliable metadata for the requested model.
	switch strings.ToLower(strings.TrimSpace(pricing.Mode)) {
	case "chat", "completion", "responses":
		return PlazaModelTypeText
	case "image_generation", "image_edit", "image_editing":
		return PlazaModelTypeImage
	case "video_generation", "video_edit", "video_editing":
		return PlazaModelTypeVideo
	case "audio_speech", "audio_transcription", "audio_translation", "realtime":
		return PlazaModelTypeAudio
	case "embedding":
		return PlazaModelTypeEmbedding
	case "":
		return ""
	default:
		return PlazaModelTypeOther
	}
}

func plazaKnownModelFamilyType(name string) PlazaModelType {
	model := lastSegment(strings.ToLower(strings.TrimSpace(name)))
	switch {
	case isOpenAIImageGenerationModel(model), isGeminiCompatibleImageModel(model),
		model == "dall-e-2", model == "dall-e-3", strings.HasPrefix(model, "imagen-"):
		return PlazaModelTypeImage
	case model == "grok-video", model == "grok-video-latest", model == "grok-video-1.5",
		model == "grok-imagine-video", strings.HasPrefix(model, "grok-imagine-video-"), strings.HasPrefix(model, "veo-"):
		return PlazaModelTypeVideo
	case strings.HasPrefix(model, "text-embedding-"), strings.HasPrefix(model, "gemini-embedding-"):
		return PlazaModelTypeEmbedding
	case model == "whisper-1", model == "tts-1", model == "tts-1-hd",
		strings.HasPrefix(model, "gpt-4o-transcribe"), strings.HasPrefix(model, "gpt-4o-mini-transcribe"),
		strings.HasPrefix(model, "gpt-4o-mini-tts"), strings.HasPrefix(model, "gpt-4o-realtime-"),
		strings.HasPrefix(model, "gpt-4o-mini-realtime-"), strings.HasPrefix(model, "gpt-realtime"):
		return PlazaModelTypeAudio
	case strings.HasPrefix(model, "claude-sonnet-"), strings.HasPrefix(model, "claude-opus-"), strings.HasPrefix(model, "claude-haiku-"),
		model == "claude-sonnet", model == "claude-opus", model == "claude-haiku",
		model == "deepseek-chat", model == "deepseek-reasoner":
		return PlazaModelTypeText
	}
	if _, known := normalizeKnownCodexModel(model); known {
		return PlazaModelTypeText
	}
	return ""
}
