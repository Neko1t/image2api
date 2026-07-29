package ycy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"backend/internal/adapter"
)

func TestVideoModelForDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration int
		want     string
	}{
		{name: "five seconds", duration: 5, want: "video-v1-5s"},
		{name: "ten seconds", duration: 10, want: "video-v1-10s"},
		{name: "fifteen seconds", duration: 15, want: "video-v1-15s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := videoModelForDuration(tt.duration)
			if err != nil {
				t.Fatalf("videoModelForDuration(%d) returned error: %v", tt.duration, err)
			}
			if got != tt.want {
				t.Fatalf("videoModelForDuration(%d) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestVideoModelForDurationRejectsUnsupportedDuration(t *testing.T) {
	_, err := videoModelForDuration(8)
	if !errors.Is(err, adapter.ErrAdapterUnsupported) {
		t.Fatalf("videoModelForDuration(8) error = %v, want ErrAdapterUnsupported", err)
	}
}

func TestGenerateVideoSelectsUpstreamModelByDuration(t *testing.T) {
	var submittedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/video/generations":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			submittedModel, _ = payload["model"].(string)
			_, _ = w.Write([]byte(`{"task_id":"task_10s"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/video/generations/task_10s":
			_, _ = w.Write([]byte(`{"status":"SUCCESS","result_url":"https://example.com/video.mp4"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, _, err := NewAdapter().GenerateVideo(
		t.Context(), server.URL, "test-key", "configured-model", "move", "16:9", 10, nil, false,
	)
	if err != nil {
		t.Fatalf("GenerateVideo returned error: %v", err)
	}
	if submittedModel != "video-v1-10s" {
		t.Fatalf("submitted model = %q, want %q", submittedModel, "video-v1-10s")
	}
}
