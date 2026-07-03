package service

import (
	"context"
	"errors"
)

// UpstreamAdapter is the interface every user-configured upstream format must
// implement. Adding a new wire protocol means implementing this interface —
// no changes to v1.go dispatch are needed.
//
// Adapters are registered at bootstrap in a map[string]UpstreamAdapter where
// the key is the adapter type (e.g., "openai", "ycy", "midjourney"). The
// adapter type is stored in TokenAccount.Metadata["adapter_type"].
//
// All adapters must return standard sentinel errors (ErrAdapter*) for uniform
// error handling in the service layer.
type UpstreamAdapter interface {
	ImageAdapter
	VideoAdapter
}

// ImageAdapter handles image generation for a specific upstream wire format.
// Adapters that only support video should return ErrAdapterUnsupported.
type ImageAdapter interface {
	// GenerateImage calls the upstream image generation API and returns raw bytes.
	//
	// Parameters:
	//   - ctx: cancellable context (may have 8-minute timeout)
	//   - baseURL: upstream API base URL (e.g., "https://api.example.com")
	//   - apiKey: authentication token/key
	//   - upstreamModel: model identifier the upstream expects
	//   - prompt: text prompt
	//   - size: OpenAI-style size string (e.g., "1024x1024", "1792x1024")
	//   - quality: quality hint (e.g., "standard", "hd") - may be empty
	//   - refs: decoded reference image bytes (PNG/JPEG/WebP) for img2img
	//
	// Returns:
	//   - image bytes (PNG/JPEG/WebP)
	//   - error (must wrap ErrAdapter* sentinels)
	GenerateImage(ctx context.Context, baseURL, apiKey, upstreamModel, prompt, size, quality string, refs [][]byte) ([]byte, error)
}

// VideoAdapter handles video generation for a specific upstream wire format.
// Adapters that only support images should return ErrAdapterUnsupported.
type VideoAdapter interface {
	// GenerateVideo calls the upstream video generation API.
	//
	// Parameters:
	//   - ctx: cancellable context (may have 12-minute timeout)
	//   - baseURL: upstream API base URL
	//   - apiKey: authentication token/key
	//   - upstreamModel: model identifier the upstream expects
	//   - prompt: text prompt
	//   - size: size string (e.g., "1280x720", "1920x1080")
	//   - duration: duration in seconds
	//   - refs: decoded reference/frame image bytes for video conditioning
	//   - downloadResult: if true, download and return video bytes; if false,
	//     return only the content URL (for async /v1/videos API)
	//
	// Returns:
	//   - video bytes (MP4) if downloadResult=true, nil otherwise
	//   - content URL (always returned, used for streaming)
	//   - error (must wrap ErrAdapter* sentinels)
	GenerateVideo(ctx context.Context, baseURL, apiKey, upstreamModel, prompt, size string, duration int, refs [][]byte, downloadResult bool) ([]byte, string, error)
}

// Sentinel errors that all adapters must use for uniform error handling.
// Adapters should wrap these with fmt.Errorf("%w: detail", ErrAdapter*, detail).
var (
	// ErrAdapterAuth indicates authentication failure (401, 403, invalid token).
	// Service layer marks the account as failed and fails over to next account.
	ErrAdapterAuth = errors.New("adapter: authentication failed")

	// ErrAdapterQuotaExhausted indicates the account has no credits/quota left.
	// Service layer marks the account as quota-limited and fails over.
	ErrAdapterQuotaExhausted = errors.New("adapter: quota exhausted")

	// ErrAdapterTemporaryUpstream indicates a retryable upstream error (5xx,
	// timeout, rate limit, connection issues). Service layer may retry the same
	// account or fail over depending on the error detail.
	ErrAdapterTemporaryUpstream = errors.New("adapter: temporary upstream error")

	// ErrAdapterUnsupported indicates the adapter does not support the requested
	// operation (e.g., video-only adapter called for image generation). Service
	// layer treats this as a permanent failure and does not retry.
	ErrAdapterUnsupported = errors.New("adapter: operation not supported")
)
