package ycy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	nanoid "github.com/matoous/go-nanoid/v2"

	"backend/internal/adapter"
)

// Adapter wraps the existing ycy.Client to implement adapter.UpstreamAdapter.
// YCY currently only supports video generation and expects reference images as
// base64 data-URIs rather than URLs.
type Adapter struct {
	*Client
}

// NewAdapter creates a new YCY adapter.
func NewAdapter() *Adapter {
	return &Adapter{
		Client: NewClient(),
	}
}

// GenerateImage implements adapter.ImageAdapter but YCY does not support image
// generation, so this always returns ErrAdapterUnsupported.
func (a *Adapter) GenerateImage(ctx context.Context, baseURL, apiKey, upstreamModel, prompt, size, quality string, refs [][]byte) ([]byte, error) {
	return nil, adapter.ErrAdapterUnsupported
}

// GenerateVideo implements adapter.VideoAdapter by calling the YCY video API.
// YCY expects reference images as base64 data-URIs (not URLs), so this adapter
// converts the byte arrays to data-URI format.
func (a *Adapter) GenerateVideo(ctx context.Context, baseURL, apiKey, upstreamModel, prompt, size string, duration int, refs [][]byte, downloadResult bool) ([]byte, string, error) {
	// YCY uses aspect ratio (e.g., "16:9") rather than size (e.g., "1280x720").
	// Convert size to ratio for the YCY API.
	ratio := sizeToRatio(size)

	// YCY expects reference images as base64 data-URIs (e.g., "data:image/jpeg;base64,/9j/...").
	// Convert each byte array to a data-URI string.
	refDataURIs := make([]string, 0, len(refs))
	for _, refBytes := range refs {
		if len(refBytes) == 0 {
			continue
		}
		// Encode to base64 data-URI
		dataURI := bytesToDataURI(refBytes)
		refDataURIs = append(refDataURIs, dataURI)
	}

	// Call the YCY client with data-URI references
	videoBytes, contentURL, err := a.Client.GenerateVideo(ctx, baseURL, apiKey, upstreamModel, prompt, ratio, refDataURIs, downloadResult)
	if err != nil {
		return nil, "", mapAdapterError(err)
	}

	return videoBytes, contentURL, nil
}

// bytesToDataURI converts image bytes to a base64 data-URI string.
// Detects image format from magic bytes and uses appropriate MIME type.
func bytesToDataURI(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// Detect image format from magic bytes
	var mimeType string
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		mimeType = "image/jpeg"
	case len(data) >= 8 && string(data[1:4]) == "PNG":
		mimeType = "image/png"
	case len(data) >= 4 && string(data[0:4]) == "RIFF":
		mimeType = "image/webp"
	default:
		// Default to JPEG if unknown
		mimeType = "image/jpeg"
	}

	// Encode to base64 with proper data-URI prefix
	encoded := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
}

// sizeToRatio converts a size string like "1280x720" to a ratio string like "16:9".
// Falls back to the original string if it doesn't match the expected format.
func sizeToRatio(size string) string {
	// Common mappings
	switch size {
	case "1280x720", "1920x1080", "2560x1440", "3840x2160":
		return "16:9"
	case "720x1280", "1080x1920", "1440x2560", "2160x3840":
		return "9:16"
	case "1024x1024", "512x512", "2048x2048":
		return "1:1"
	case "1024x768", "1280x960", "1600x1200":
		return "4:3"
	case "768x1024", "960x1280", "1200x1600":
		return "3:4"
	}
	// If the size is already in ratio format (e.g., "16:9"), return as-is
	return size
}

// randomID generates a random alphanumeric string of the given length using nanoid.
func randomID(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	id, err := nanoid.Generate(charset, n)
	if err != nil {
		// Fallback to a simple pattern on error
		b := make([]byte, n)
		for i := range b {
			b[i] = charset[i%len(charset)]
		}
		return string(b)
	}
	return id
}

// mapAdapterError converts ycy package errors to adapter.ErrAdapter* sentinels.
func mapAdapterError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrAuth):
		return fmt.Errorf("%w: %v", adapter.ErrAdapterAuth, err)
	case errors.Is(err, ErrQuotaExhausted):
		return fmt.Errorf("%w: %v", adapter.ErrAdapterQuotaExhausted, err)
	case errors.Is(err, ErrTemporaryUpstream):
		return fmt.Errorf("%w: %v", adapter.ErrAdapterTemporaryUpstream, err)
	default:
		return fmt.Errorf("%w: %v", adapter.ErrAdapterTemporaryUpstream, err)
	}
}
