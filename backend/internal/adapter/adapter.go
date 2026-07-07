package adapter

import (
	"context"
	"errors"
)

// UpstreamAdapter combines image and video generation capabilities for
// user-configured upstream APIs (OpenAI-compatible, YCY, etc). Implementations
// handle format-specific protocol details and error mapping.
type UpstreamAdapter interface {
	ImageAdapter
	VideoAdapter
}

// ImageAdapter handles image generation for an upstream format.
type ImageAdapter interface {
	GenerateImage(ctx context.Context, baseURL, apiKey, upstreamModel, prompt, size, quality string, refs [][]byte) ([]byte, error)
}

// VideoAdapter handles video generation for an upstream format.
type VideoAdapter interface {
	GenerateVideo(ctx context.Context, baseURL, apiKey, upstreamModel, prompt, size string, duration int, refs [][]byte, downloadResult bool) ([]byte, string, error)
}

// Sentinel errors for adapter implementations to wrap their provider-specific
// errors. The service layer checks these with errors.Is() for failover decisions.
var (
	ErrAdapterAuth              = errors.New("adapter: authentication failed")
	ErrAdapterQuotaExhausted    = errors.New("adapter: quota exhausted")
	ErrAdapterTemporaryUpstream = errors.New("adapter: temporary upstream error")
	ErrAdapterUnsupported       = errors.New("adapter: operation not supported by this format")
)
