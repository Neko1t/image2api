package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/internal/service"
	"github.com/gin-gonic/gin"
)

func TestWriteMediaDeliveryRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/content", nil)

	(&V1Handler{}).writeMediaDelivery(ctx, &service.MediaDelivery{RedirectURL: "https://media.example.com/file?signature=redacted"})

	if recorder.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status=%d", recorder.Code)
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache-control=%q", recorder.Header().Get("Cache-Control"))
	}
}

func TestWriteMediaDeliveryPreservesPartialContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
	delivery := &service.MediaDelivery{Response: &http.Response{
		StatusCode: http.StatusPartialContent,
		Header: http.Header{
			"Content-Type":  []string{"video/mp4"},
			"Content-Range": []string{"bytes 10-19/100"},
			"Accept-Ranges": []string{"bytes"},
			"Cache-Control": []string{"public, max-age=86400"},
		},
		Body: io.NopCloser(strings.NewReader("0123456789")),
	}}

	(&V1Handler{}).writeMediaDelivery(ctx, delivery)

	if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "0123456789" {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Range") != "bytes 10-19/100" {
		t.Fatalf("content-range=%q", recorder.Header().Get("Content-Range"))
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache-control=%q", recorder.Header().Get("Cache-Control"))
	}
}
