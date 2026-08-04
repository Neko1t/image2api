package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"backend/internal/model"
)

func TestNormalizeV1ImageResponseFormat(t *testing.T) {
	for input, want := range map[string]string{"": "url", " URL ": "url", "B64_JSON": "b64_json"} {
		got, err := normalizeV1ImageResponseFormat(input)
		if err != nil || got != want {
			t.Fatalf("input=%q got=%q err=%v", input, got, err)
		}
	}
	if _, err := normalizeV1ImageResponseFormat("base64"); err == nil {
		t.Fatal("expected unsupported response format")
	}
}

func TestAPIMediaObjectKeysAreOwnerScoped(t *testing.T) {
	key, err := apiMediaObjectKey("u_123", "video", "evt-123", "video/mp4")
	if err != nil || key != "api/u_123/videos/evt-123.mp4" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	ev := &model.EventLog{ID: "evt-123", UserID: "u_123", File: key}
	if got, ok := validatedAPIMediaObjectKey(ev, "video"); !ok || got != key {
		t.Fatalf("validated key=%q ok=%v", got, ok)
	}
	for _, invalid := range []string{
		"api/other/videos/evt-123.mp4",
		"api/u_123/videos/evt-other.mp4",
		"api/u_123/videos/evt-123.exe",
		"api/u_123/videos/../images/evt.png",
		"https://media.example.com/object",
	} {
		ev.File = invalid
		if _, ok := validatedAPIMediaObjectKey(ev, "video"); ok {
			t.Fatalf("accepted invalid key %q", invalid)
		}
	}
}

func TestDetectAPIMediaTypeUsesMagicBytes(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if got, err := detectAPIMediaType(bytes.NewReader(png), "text/html", "image"); err != nil || got != "image/png" {
		t.Fatalf("png type=%q err=%v", got, err)
	}
	mp4 := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	if got, err := detectAPIMediaType(bytes.NewReader(mp4), "application/octet-stream", "video"); err != nil || got != "video/mp4" {
		t.Fatalf("mp4 type=%q err=%v", got, err)
	}
	if _, err := detectAPIMediaType(bytes.NewReader([]byte("<html>")), "video/mp4", "video"); err == nil {
		t.Fatal("provider content type must not override invalid magic")
	}
}

func TestValidatedArtifactFetchForwardsAuthAndRangeOnTrustedOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Range") != "bytes=10-19" {
			t.Fatalf("range=%q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 10-19/100")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "0123456789")
	}))
	defer server.Close()

	resp, err := openValidatedHTTPArtifact(t.Context(), server.URL+"/content", server.URL, "secret", "bytes=10-19")
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") == "" {
		t.Fatalf("status=%d content-range=%q", resp.StatusCode, resp.Header.Get("Content-Range"))
	}
}

func TestArtifactURLRejectsUntrustedPrivateTargets(t *testing.T) {
	if _, err := validateArtifactURL(t.Context(), "http://127.0.0.1/private", ""); err == nil {
		t.Fatal("expected untrusted private HTTP URL to be rejected")
	}
	if _, err := validateArtifactURL(t.Context(), "http://127.0.0.1/trusted", "http://127.0.0.1"); err != nil {
		t.Fatalf("configured exact origin should be allowed: %v", err)
	}
}

func TestTrustedChatGPTArtifactHosts(t *testing.T) {
	allowed := []string{
		"https://chatgpt.com/backend-api/files/example",
		"https://cdn.chatgpt.com/assets/example",
		"https://files.oaiusercontent.com/file/example",
		"https://FILES.OAIUSERCONTENT.COM./file/example",
	}
	for _, rawURL := range allowed {
		if !trustedHTTPSArtifactHost(rawURL, "oaiusercontent.com", "chatgpt.com") {
			t.Fatalf("rejected trusted ChatGPT artifact URL %q", rawURL)
		}
	}

	rejected := []string{
		"http://chatgpt.com/backend-api/files/example",
		"https://user@chatgpt.com/backend-api/files/example",
		"https://chatgpt.com:8443/backend-api/files/example",
		"https://chatgpt.com.evil.example/file",
		"https://evilchatgpt.com/file",
		"https://oaiusercontent.com.evil.example/file",
	}
	for _, rawURL := range rejected {
		if trustedHTTPSArtifactHost(rawURL, "oaiusercontent.com", "chatgpt.com") {
			t.Fatalf("accepted untrusted ChatGPT artifact URL %q", rawURL)
		}
	}
}

func TestValidatedArtifactDialerRejectsRestrictedResolvedAddresses(t *testing.T) {
	for _, restricted := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1"} {
		t.Run(restricted, func(t *testing.T) {
			lookup := func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(restricted)}}, nil
			}
			dial := validatedArtifactDialer(nil, lookup)
			if _, err := dial(t.Context(), "tcp", "media.example.com:443"); err == nil || !strings.Contains(err.Error(), "restricted") {
				t.Fatalf("dial error=%v", err)
			}
		})
	}
}

func TestValidatedArtifactDialerAllowsConfiguredPrivateOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	trusted, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse trusted URL: %v", err)
	}
	dial := validatedArtifactDialer(trusted, func(context.Context, string) ([]net.IPAddr, error) {
		return nil, errors.New("trusted origin must not require public DNS validation")
	})
	conn, err := dial(t.Context(), "tcp", trusted.Host)
	if err != nil {
		t.Fatalf("dial configured private origin: %v", err)
	}
	_ = conn.Close()
}
