package app

// Empty and auto retain existing per-model routing and learned endpoint repair.
// Explicit policies are authoritative for text inference; embeddings and audio
// retain their own endpoints. APIFormat still selects auth and wire family.
func providerInferenceEndpoint(provider Provider) string {
	if provider.APIFormat == "anthropic" {
		return "messages"
	}
	switch provider.InferenceEndpoint {
	case "chat", "responses":
		return provider.InferenceEndpoint
	default:
		return "auto"
	}
}

func applyProviderInferenceEndpoint(provider Provider, model *modelInput) {
	if model.SupportsEmbeddings || audioModelType(model.UpstreamModel, model.ModelType) != "" {
		return
	}
	switch providerInferenceEndpoint(provider) {
	case "responses":
		model.SupportsChat, model.SupportsResponses, model.SupportsMessages = false, true, true
	case "chat":
		model.SupportsChat, model.SupportsResponses, model.SupportsMessages = true, false, true
	}
}
