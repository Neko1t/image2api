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

func TestSizeToRatio(t *testing.T) {
	tests := []struct {
		name string
		size string
		want string
	}{
		{name: "landscape 1080p", size: "1920x1080", want: "16:9"},
		{name: "portrait 1080p", size: "1080x1920", want: "9:16"},
		{name: "square 720p", size: "720x720", want: "1:1"},
		{name: "square 1080p", size: "1080x1080", want: "1:1"},
		{name: "arbitrary reducible size", size: "1000x750", want: "4:3"},
		{name: "existing ratio", size: "16:9", want: "16:9"},
		{name: "invalid size", size: "auto", want: "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sizeToRatio(tt.size); got != tt.want {
				t.Fatalf("sizeToRatio(%q) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

func TestGenerateVideoSelectsUpstreamModelByDuration(t *testing.T) {
	var submittedModel string
	var submittedRatio string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/video/generations":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			submittedModel, _ = payload["model"].(string)
			submittedRatio, _ = payload["ratio"].(string)
			_, _ = w.Write([]byte(`{"task_id":"task_10s"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/video/generations/task_10s":
			_, _ = w.Write([]byte(`{"status":"SUCCESS","result_url":"https://example.com/video.mp4"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, _, err := NewAdapter().GenerateVideo(
		t.Context(), server.URL, "test-key", "configured-model", "move", "1080x1080", 10, nil, false,
	)
	if err != nil {
		t.Fatalf("GenerateVideo returned error: %v", err)
	}
	if submittedModel != "video-v1-10s" {
		t.Fatalf("submitted model = %q, want %q", submittedModel, "video-v1-10s")
	}
	if submittedRatio != "1:1" {
		t.Fatalf("submitted ratio = %q, want %q", submittedRatio, "1:1")
	}
}

func TestCreateVideoTaskReturnsAfterSubmitWithoutPolling(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/video/generations" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if payload["model"] != "video-v1-15s" || payload["ratio"] != "9:16" {
			t.Fatalf("payload = %#v", payload)
		}
		_, _ = w.Write([]byte(`{"task_id":"task_async"}`))
	}))
	defer server.Close()

	taskID, err := NewAdapter().CreateVideoTask(t.Context(), server.URL, "test-key", "move", "1080x1920", 15, nil)
	if err != nil {
		t.Fatalf("CreateVideoTask returned error: %v", err)
	}
	if taskID != "task_async" || requests != 1 {
		t.Fatalf("taskID=%q requests=%d, want task_async and one POST", taskID, requests)
	}
}

func TestMapAdapterErrorPreservesUserMessage(t *testing.T) {
	upstreamErr := mapStatus(http.StatusBadRequest, []byte(`{"code":"rejected","message":"{\"error\":{\"message\":\"请求被拒绝\"}}"}`))
	err := mapAdapterError(upstreamErr)
	if !errors.Is(err, adapter.ErrAdapterTemporaryUpstream) {
		t.Fatalf("mapAdapterError error = %v, want ErrAdapterTemporaryUpstream", err)
	}
	if got, want := UserErrorMessage(err), "请求被拒绝"; got != want {
		t.Fatalf("UserErrorMessage() = %q, want %q", got, want)
	}
}
