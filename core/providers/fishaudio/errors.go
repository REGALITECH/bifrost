package fishaudio

import (
	"github.com/valyala/fasthttp"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
)

// parseFishError parses a Fish Audio HTTP error response (used by ListModels).
// Fish returns either {"detail": "..."} or {"message": "..."} depending on the
// endpoint, so both are accepted.
func parseFishError(resp *fasthttp.Response) *schemas.BifrostError {
	var errorResp FishAudioError
	bifrostErr := providerUtils.HandleProviderAPIError(resp, &errorResp)

	message := errorResp.Detail
	if message == "" {
		message = errorResp.Message
	}

	if message != "" {
		if bifrostErr.Error == nil {
			bifrostErr.Error = &schemas.ErrorField{}
		}
		bifrostErr.Error.Message = message
	}

	return bifrostErr
}
