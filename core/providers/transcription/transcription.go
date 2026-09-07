package transcription

import (
	"context"

	"github.com/maximhq/bifrost/core/schemas"
)

// Provider accounts for external STT; it never sends inference requests.
type Provider struct{}

var _ schemas.Provider = (*Provider)(nil)

func (p *Provider) GetProviderKey() schemas.ModelProvider { return schemas.Transcription }

func (p *Provider) ListModels(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostListModelsRequest) (*schemas.BifrostListModelsResponse, *schemas.BifrostError) {
	return &schemas.BifrostListModelsResponse{Data: []schemas.Model{}}, nil
}

func (p *Provider) TextCompletion(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostTextCompletionRequest) (*schemas.BifrostTextCompletionResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) TextCompletionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostTextCompletionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ChatCompletion(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ChatCompletionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostChatRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) Responses(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ResponsesStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostResponsesRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) CountTokens(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostResponsesRequest) (*schemas.BifrostCountTokensResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) Compaction(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCompactionRequest) (*schemas.BifrostCompactionResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) Embedding(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostEmbeddingRequest) (*schemas.BifrostEmbeddingResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) Rerank(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostRerankRequest) (*schemas.BifrostRerankResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) OCR(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostOCRRequest) (*schemas.BifrostOCRResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) Speech(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostSpeechRequest) (*schemas.BifrostSpeechResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) SpeechStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostSpeechRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) Transcription(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostTranscriptionRequest) (*schemas.BifrostTranscriptionResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) TranscriptionStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostTranscriptionRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ImageGeneration(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageGenerationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ImageGenerationStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostImageGenerationRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ImageEdit(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageEditRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ImageEditStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, request *schemas.BifrostImageEditRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ImageVariation(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostImageVariationRequest) (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) VideoGeneration(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoGenerationRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) VideoRetrieve(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoRetrieveRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) VideoDownload(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoDownloadRequest) (*schemas.BifrostVideoDownloadResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) VideoDelete(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoDeleteRequest) (*schemas.BifrostVideoDeleteResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) VideoList(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoListRequest) (*schemas.BifrostVideoListResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) VideoRemix(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostVideoRemixRequest) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) BatchCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostBatchCreateRequest) (*schemas.BifrostBatchCreateResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) BatchList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchListRequest) (*schemas.BifrostBatchListResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) BatchRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchRetrieveRequest) (*schemas.BifrostBatchRetrieveResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) BatchCancel(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchCancelRequest) (*schemas.BifrostBatchCancelResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) BatchDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchDeleteRequest) (*schemas.BifrostBatchDeleteResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) BatchResults(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostBatchResultsRequest) (*schemas.BifrostBatchResultsResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) FileUpload(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostFileUploadRequest) (*schemas.BifrostFileUploadResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) FileList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileListRequest) (*schemas.BifrostFileListResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) FileRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileRetrieveRequest) (*schemas.BifrostFileRetrieveResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) FileDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileDeleteRequest) (*schemas.BifrostFileDeleteResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) FileContent(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostFileContentRequest) (*schemas.BifrostFileContentResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) CachedContentCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCachedContentCreateRequest) (*schemas.BifrostCachedContentCreateResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) CachedContentList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostCachedContentListRequest) (*schemas.BifrostCachedContentListResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) CachedContentRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostCachedContentRetrieveRequest) (*schemas.BifrostCachedContentRetrieveResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) CachedContentUpdate(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostCachedContentUpdateRequest) (*schemas.BifrostCachedContentUpdateResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) CachedContentDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostCachedContentDeleteRequest) (*schemas.BifrostCachedContentDeleteResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ContainerCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostContainerCreateRequest) (*schemas.BifrostContainerCreateResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ContainerList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerListRequest) (*schemas.BifrostContainerListResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ContainerRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerRetrieveRequest) (*schemas.BifrostContainerRetrieveResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ContainerDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerDeleteRequest) (*schemas.BifrostContainerDeleteResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ContainerFileCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostContainerFileCreateRequest) (*schemas.BifrostContainerFileCreateResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ContainerFileList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerFileListRequest) (*schemas.BifrostContainerFileListResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ContainerFileRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerFileRetrieveRequest) (*schemas.BifrostContainerFileRetrieveResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ContainerFileContent(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerFileContentRequest) (*schemas.BifrostContainerFileContentResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) ContainerFileDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostContainerFileDeleteRequest) (*schemas.BifrostContainerFileDeleteResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) Passthrough(ctx *schemas.BifrostContext, key schemas.Key, req *schemas.BifrostPassthroughRequest) (*schemas.BifrostPassthroughResponse, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}

func (p *Provider) PassthroughStream(ctx *schemas.BifrostContext, postHookRunner schemas.PostHookRunner, postHookSpanFinalizer func(context.Context), key schemas.Key, req *schemas.BifrostPassthroughRequest) (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
	return nil, &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Message: "transcription is usage-only; inference is not supported"}}
}
