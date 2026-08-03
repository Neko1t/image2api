package handler

import (
	"context"
	"io"
	"net/http"

	"backend/internal/config"
	"backend/internal/service"
	"github.com/gin-gonic/gin"
)

type ImageHandler struct {
	cfg         *config.Config
	imageAccess imageAccess
	store       imageStore
}

type imageAccess interface {
	Resolve(user, name string) (string, error)
	IsPublic(ctx context.Context, rel string) (bool, error)
	IsAuthorized(ctx context.Context, sessionCookie, owner string) (bool, error)
}

type imageStore interface {
	Get(ctx context.Context, key, rangeHeader string) (*http.Response, error)
	Exists(ctx context.Context, key string) (bool, error)
	DirectDeliveryEnabled() bool
	PresignGet(ctx context.Context, key string) (string, error)
}

func NewImageHandler(cfg *config.Config, imageAccess imageAccess, store imageStore) *ImageHandler {
	return &ImageHandler{
		cfg:         cfg,
		imageAccess: imageAccess,
		store:       store,
	}
}

// Serve gates access (public showcase images, or a logged-in cookie). It then
// either proxies bytes from storage or redirects to a short-lived private OSS
// URL when direct delivery is enabled.
func (h *ImageHandler) Serve(c *gin.Context) {
	user := c.Param("user")
	name := c.Param("name")

	rel, err := h.imageAccess.Resolve(user, name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid path"})
		return
	}

	// A thumbnail shares its original's visibility, and old images without a
	// stored thumb fall back to the original object.
	origRel := rel
	if service.IsThumbKey(rel) {
		origRel = service.OrigKey(rel)
	} else if service.IsLastFrameKey(rel) {
		origRel = service.LastFrameOrigKey(rel)
	}

	public, err := h.imageAccess.IsPublic(c.Request.Context(), origRel)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to authorize image"})
		return
	}
	if !public {
		authorized, err := h.imageAccess.IsAuthorized(
			c.Request.Context(),
			readCookie(c, h.cfg.SessionCookieName),
			user,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to authorize image"})
			return
		}
		if !authorized {
			c.JSON(http.StatusUnauthorized, gin.H{"detail": "需要登录后访问"})
			return
		}
	}

	if h.store.DirectDeliveryEnabled() {
		deliveryKey := rel
		exists, err := h.store.Exists(c.Request.Context(), deliveryKey)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"detail": "failed to check object"})
			return
		}
		if !exists && origRel != rel {
			deliveryKey = origRel
			exists, err = h.store.Exists(c.Request.Context(), deliveryKey)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"detail": "failed to check object"})
				return
			}
		}
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"detail": "not found"})
			return
		}
		signedURL, err := h.store.PresignGet(c.Request.Context(), deliveryKey)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"detail": "failed to authorize object delivery"})
			return
		}
		c.Header("Cache-Control", "private, no-store")
		c.Redirect(http.StatusTemporaryRedirect, signedURL)
		return
	}

	// Forward Range so the browser can seek within videos.
	resp, err := h.store.Get(c.Request.Context(), rel, c.GetHeader("Range"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"detail": "failed to fetch object"})
		return
	}
	if resp.StatusCode == http.StatusNotFound && origRel != rel {
		resp.Body.Close()
		resp, err = h.store.Get(c.Request.Context(), origRel, c.GetHeader("Range"))
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"detail": "failed to fetch object"})
			return
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		c.JSON(http.StatusNotFound, gin.H{"detail": "not found"})
		return
	}
	for _, hdr := range []string{"Content-Type", "Content-Length", "Accept-Ranges", "Content-Range", "Last-Modified", "ETag", "Cache-Control"} {
		if v := resp.Header.Get(hdr); v != "" {
			c.Header(hdr, v)
		}
	}
	c.Status(resp.StatusCode)
	_, _ = io.Copy(c.Writer, resp.Body)
}

func readCookie(c *gin.Context, name string) string {
	v, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return v
}
