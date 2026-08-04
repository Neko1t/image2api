package ycy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateVideoSendsReferenceImages(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/video/generations" {
			t.Fatalf("path = %s, want /v1/video/generations", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"task_id":"task_123","status":"queued"}`))
	}))
	defer server.Close()

	_, err := NewClient().CreateVideo(t.Context(), server.URL, "test-key", "video-v1-5s", "move", "16:9", []string{
		"data:image/png;base64,AAAA",
		"data:image/jpeg;base64,BBBB",
	})
	if err != nil {
		t.Fatalf("CreateVideo returned error: %v", err)
	}
	images, ok := payload["images"].([]any)
	if !ok {
		t.Fatalf("payload images missing or wrong type: %#v", payload)
	}
	if len(images) != 2 || images[0] != "data:image/png;base64,AAAA" || images[1] != "data:image/jpeg;base64,BBBB" {
		t.Fatalf("images = %#v", images)
	}
	if _, ok := payload["image"]; ok {
		t.Fatalf("payload should use images for multiple references: %#v", payload)
	}
}

func TestMapStatusExtractsNestedUnicodeMessage(t *testing.T) {
	body := []byte(`{"code":"fail_to_fetch_task","message":"{\"error\":{\"message\":\"\\u8bf7\\u6c42\\u88ab\\u4e0a\\u6e38\\u62d2\\u7edd\"}}"}`)
	err := mapStatus(http.StatusBadRequest, body)
	if !errors.Is(err, ErrTemporaryUpstream) {
		t.Fatalf("mapStatus error = %v, want ErrTemporaryUpstream", err)
	}
	if got, want := UserErrorMessage(err), "请求被上游拒绝"; got != want {
		t.Fatalf("UserErrorMessage() = %q, want %q", got, want)
	}
	if strings.Contains(UserErrorMessage(err), `\u`) {
		t.Fatalf("user message still contains a unicode escape: %q", UserErrorMessage(err))
	}
	if !strings.Contains(err.Error(), "400: fail_to_fetch_task") {
		t.Fatalf("diagnostic error lost status/code: %v", err)
	}
}

func TestMapStatusExtractsPlainMessage(t *testing.T) {
	err := mapStatus(http.StatusTooManyRequests, []byte(`{"code":"rate_limit","message":"请求过于频繁"}`))
	if !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("mapStatus error = %v, want ErrQuotaExhausted", err)
	}
	if got, want := UserErrorMessage(err), "请求过于频繁"; got != want {
		t.Fatalf("UserErrorMessage() = %q, want %q", got, want)
	}
}

func TestMapStatusHidesMalformedBodyFromUser(t *testing.T) {
	err := mapStatus(http.StatusBadGateway, []byte(`<html>internal upstream details</html>`))
	if got := UserErrorMessage(err); got != defaultUserErrorMessage {
		t.Fatalf("UserErrorMessage() = %q, want generic fallback", got)
	}
	if !strings.Contains(err.Error(), "internal upstream details") {
		t.Fatalf("diagnostic error should retain a bounded fallback: %v", err)
	}
}
