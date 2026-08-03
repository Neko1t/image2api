package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/config"
	"github.com/gin-gonic/gin"
)

type fakeImageAccess struct {
	public     bool
	authorized bool
}

func (f *fakeImageAccess) Resolve(user, name string) (string, error) {
	return user + "/" + name, nil
}

func (f *fakeImageAccess) IsPublic(context.Context, string) (bool, error) {
	return f.public, nil
}

func (f *fakeImageAccess) IsAuthorized(context.Context, string, string) (bool, error) {
	return f.authorized, nil
}

type fakeImageStore struct {
	direct      bool
	exists      map[string]bool
	existsCalls []string
	signedKey   string
	rangeHeader string
}

func (f *fakeImageStore) Get(_ context.Context, _ string, rangeHeader string) (*http.Response, error) {
	f.rangeHeader = rangeHeader
	return &http.Response{
		StatusCode: http.StatusPartialContent,
		Header:     http.Header{"Content-Type": []string{"video/mp4"}},
		Body:       io.NopCloser(strings.NewReader("video")),
	}, nil
}

func (f *fakeImageStore) Exists(_ context.Context, key string) (bool, error) {
	f.existsCalls = append(f.existsCalls, key)
	return f.exists[key], nil
}

func (f *fakeImageStore) DirectDeliveryEnabled() bool { return f.direct }

func (f *fakeImageStore) PresignGet(_ context.Context, key string) (string, error) {
	f.signedKey = key
	return "https://media.example.com/" + key + "?signature=redacted", nil
}

func imageTestContext(path, user, name string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Params = gin.Params{{Key: "user", Value: user}, {Key: "name", Value: name}}
	return ctx, recorder
}

func TestImageHandlerDoesNotTouchStorageBeforeAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &fakeImageStore{direct: true, exists: map[string]bool{"u/a.png": true}}
	handler := NewImageHandler(
		&config.Config{SessionCookieName: "session"},
		&fakeImageAccess{public: false, authorized: false},
		store,
	)
	ctx, recorder := imageTestContext("/images/u/a.png", "u", "a.png")

	handler.Serve(ctx)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
	if len(store.existsCalls) != 0 {
		t.Fatalf("storage checked before authorization: %#v", store.existsCalls)
	}
}

func TestImageHandlerRedirectsAuthorizedDirectDelivery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &fakeImageStore{direct: true, exists: map[string]bool{"u/a.png": true}}
	handler := NewImageHandler(
		&config.Config{SessionCookieName: "session"},
		&fakeImageAccess{public: true},
		store,
	)
	ctx, recorder := imageTestContext("/images/u/a.png", "u", "a.png")

	handler.Serve(ctx)

	if recorder.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache-control=%q", got)
	}
	if store.signedKey != "u/a.png" {
		t.Fatalf("signed key=%q", store.signedKey)
	}
}

func TestImageHandlerDirectDeliveryFallsBackToOriginal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &fakeImageStore{
		direct: true,
		exists: map[string]bool{"u/a.png": true},
	}
	handler := NewImageHandler(
		&config.Config{SessionCookieName: "session"},
		&fakeImageAccess{public: true},
		store,
	)
	ctx, recorder := imageTestContext("/images/u/a.png.thumb.jpg", "u", "a.png.thumb.jpg")

	handler.Serve(ctx)

	if recorder.Code != http.StatusTemporaryRedirect || store.signedKey != "u/a.png" {
		t.Fatalf("status=%d signed=%q", recorder.Code, store.signedKey)
	}
	if len(store.existsCalls) != 2 || store.existsCalls[0] != "u/a.png.thumb.jpg" || store.existsCalls[1] != "u/a.png" {
		t.Fatalf("exists calls=%#v", store.existsCalls)
	}
}

func TestImageHandlerKeepsProxyModeAndRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &fakeImageStore{direct: false}
	handler := NewImageHandler(
		&config.Config{SessionCookieName: "session"},
		&fakeImageAccess{public: true},
		store,
	)
	ctx, recorder := imageTestContext("/images/u/a.mp4", "u", "a.mp4")
	ctx.Request.Header.Set("Range", "bytes=100-")

	handler.Serve(ctx)

	if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "video" {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if store.rangeHeader != "bytes=100-" {
		t.Fatalf("range=%q", store.rangeHeader)
	}
}
