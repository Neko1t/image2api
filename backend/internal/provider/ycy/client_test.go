package ycy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
