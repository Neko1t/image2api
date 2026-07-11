package handler

import (
	"errors"
	"net/http"

	"backend/internal/model"
	"backend/internal/service"
	"github.com/gin-gonic/gin"
)

type AdminWriteHandler struct {
	admin *service.AdminWriteService
}

func NewAdminWriteHandler(admin *service.AdminWriteService) *AdminWriteHandler {
	return &AdminWriteHandler{admin: admin}
}

func (h *AdminWriteHandler) CreateUser(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	user, err := h.admin.CreateUser(c.Request.Context(), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": userPublic(*user)})
}

func (h *AdminWriteHandler) UpdateUser(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	user, err := h.admin.UpdateUser(c.Request.Context(), c.Param("user_id"), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": userPublic(*user)})
}

func (h *AdminWriteHandler) DeleteUser(c *gin.Context) {
	if err := h.admin.DeleteUser(c.Request.Context(), c.Param("user_id")); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"detail": "user not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to delete user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteUsersBulk removes multiple users in one call (multi-select).
func (h *AdminWriteHandler) DeleteUsersBulk(c *gin.Context) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	if len(body.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "未选择任何用户"})
		return
	}
	n, err := h.admin.DeleteUsers(c.Request.Context(), body.IDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to delete users"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "deleted": n})
}

func (h *AdminWriteHandler) AdjustUserCredits(c *gin.Context) {
	var body struct {
		Delta float64  `json:"delta"`
		Set   *float64 `json:"set"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	var (
		user *model.User
		err  error
	)
	if body.Set != nil {
		// Absolute set takes precedence over delta (matches Python admin.py).
		user, err = h.admin.SetUserCredits(c.Request.Context(), c.Param("user_id"), *body.Set)
	} else {
		user, err = h.admin.AdjustUserCredits(c.Request.Context(), c.Param("user_id"), body.Delta)
	}
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"detail": "user not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": userPublic(*user)})
}

func (h *AdminWriteHandler) CreateUserAPIKey(c *gin.Context) {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	key, plain, err := h.admin.CreateUserAPIKey(c.Request.Context(), c.Param("user_id"), body.Name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":  true,
		"key": plain,
		"data": gin.H{
			"id":           key.ID,
			"name":         key.Name,
			"key_preview":  key.KeyPreview,
			"created_at":   key.CreatedAt,
			"last_used_at": key.LastUsedAt,
		},
	})
}

func (h *AdminWriteHandler) DeleteUserAPIKey(c *gin.Context) {
	if err := h.admin.DeleteUserAPIKey(c.Request.Context(), c.Param("user_id"), c.Param("key_id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminWriteHandler) CreateShowcase(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	item, err := h.admin.CreateShowcase(c.Request.Context(), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": item})
}

func (h *AdminWriteHandler) UpdateShowcase(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	item, err := h.admin.UpdateShowcase(c.Request.Context(), c.Param("entry_id"), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": item})
}

func (h *AdminWriteHandler) DeleteShowcase(c *gin.Context) {
	if err := h.admin.DeleteShowcase(c.Request.Context(), c.Param("entry_id")); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"detail": "showcase not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to delete showcase"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminWriteHandler) CreateModel(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	item, err := h.admin.CreateModel(c.Request.Context(), body)
	if err != nil {
		if errors.Is(err, service.ErrModelAliasCollision) {
			c.JSON(http.StatusConflict, gin.H{"detail": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": item})
}

func (h *AdminWriteHandler) UpdateModel(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	item, err := h.admin.UpdateModel(c.Request.Context(), c.Param("model_id"), body)
	if err != nil {
		if errors.Is(err, service.ErrModelAliasCollision) {
			c.JSON(http.StatusConflict, gin.H{"detail": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": item})
}

func (h *AdminWriteHandler) DeleteModel(c *gin.Context) {
	if err := h.admin.DeleteModel(c.Request.Context(), c.Param("model_id")); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"detail": "model not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to delete model"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminWriteHandler) ClearLogs(c *gin.Context) {
	removed, err := h.admin.ClearLogs(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to clear logs"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "removed": removed})
}

func (h *AdminWriteHandler) ClearPendingLogs(c *gin.Context) {
	removed, err := h.admin.ClearPendingLogs(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to clear pending logs"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "removed": removed})
}
