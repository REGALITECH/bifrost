package handlers

import "github.com/maximhq/bifrost/core/schemas"

// realtimeHeadersForModel returns the upstream handshake headers for a realtime
// session. model must be the requested model after key alias resolution.
// Providers whose headers depend on the model (e.g. Fish Audio's "model"
// header) implement schemas.RealtimeModelHeadersProvider and receive it; all
// others fall back to RealtimeProvider.RealtimeHeaders.
func realtimeHeadersForModel(ctx *schemas.BifrostContext, provider schemas.RealtimeProvider, key schemas.Key, model string) (map[string]string, *schemas.BifrostError) {
	if mp, ok := provider.(schemas.RealtimeModelHeadersProvider); ok {
		return mp.RealtimeHeadersForModel(ctx, key, model)
	}
	return provider.RealtimeHeaders(ctx, key)
}
