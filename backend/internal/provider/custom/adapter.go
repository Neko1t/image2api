package custom

import (
	"context"
	"errors"
	"fmt"

	"backend/internal/service"
)

// Adapter wraps the existing custom.Client to implement service.UpstreamAdapter.
// It adapts OpenAI-compatible upstream APIs (image and video generation) to the
// unified adapter interface.
type Adapter struct {
	*Client
}

// NewAdapter creates a new OpenAI-compatible adapter.
func NewAdapter() *Adapter {
	return &Adapter{Client: NewClient()}
}

// GenerateImage implements service.ImageAdapter by calling the OpenAI-compatible
// /v1/images/generations or /v1/images/edits endpoint.
func (a *Adapter) GenerateImage(ctx context.Context, baseURL, apiKey, upstreamModel, prompt, size, quality string, refs [][]byte) ([]byte, error) {
	imageBytes, err := a.Client.GenerateImage(ctx, baseURL, apiKey, upstreamModel, prompt, size, quality, refs)
	if err != nil {
		return nil, mapAdapterError(err)
	}
	return imageBytes, nil
}

// GenerateVideo implements service.VideoAdapter by calling the OpenAI Sora-style
// async video API: POST /v1/videos → poll /v1/videos/{id} → GET /v1/videos/{id}/content.
func (a *Adapter) GenerateVideo(ctx context.Context, baseURL, apiKey, upstreamModel, prompt, size string, duration int, refs [][]byte, downloadResult bool) ([]byte, string, error) {
	// Note: custom.Client.GenerateVideo expects duration in seconds as an int,
	// which matches our interface. The size parameter is passed directly (OpenAI
	// uses size strings like "1280x720").
	videoBytes, contentURL, err := a.Client.GenerateVideo(ctx, baseURL, apiKey, upstreamModel, prompt, size, duration, downloadResult)
	if err != nil {
		return nil, "", mapAdapterError(err)
	}
	return videoBytes, contentURL, nil
}

// mapAdapterError converts custom package errors to service.ErrAdapter* sentinels
// for uniform error handling in the service layer.
func mapAdapterError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrAuth):
		return fmt.Errorf("%w: %v", service.ErrAdapterAuth, err)
	case errors.Is(err, ErrQuotaExhausted):
		return fmt.Errorf("%w: %v", service.ErrAdapterQuotaExhausted, err)
	case errors.Is(err, ErrTemporaryUpstream):
		return fmt.Errorf("%w: %v", service.ErrAdapterTemporaryUpstream, err)
	default:
		// Unknown errors are treated as temporary by default (safer for failover)
		return fmt.Errorf("%w: %v", service.ErrAdapterTemporaryUpstream, err)
	}
}
