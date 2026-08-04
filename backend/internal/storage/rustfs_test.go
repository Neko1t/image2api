package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRustFSPutStreamSignsAndStreamsBody(t *testing.T) {
	payload := []byte("streamed-rustfs-video")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/bucket/api/u/videos/evt.mp4" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.ContentLength != int64(len(payload)) {
			t.Fatalf("content length=%d", r.ContentLength)
		}
		sum := sha256.Sum256(payload)
		if got := r.Header.Get("x-amz-content-sha256"); got != hex.EncodeToString(sum[:]) {
			t.Fatalf("payload hash=%q", got)
		}
		if r.Header.Get("Authorization") == "" {
			t.Fatal("missing authorization")
		}
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("body=%q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewRustFS(server.URL, "bucket", "access", "secret")
	if err := client.PutStream(t.Context(), "api/u/videos/evt.mp4", bytes.NewReader(payload), int64(len(payload)), "video/mp4"); err != nil {
		t.Fatalf("put stream: %v", err)
	}
}
