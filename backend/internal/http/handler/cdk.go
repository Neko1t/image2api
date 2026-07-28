package handler

import (
	"errors"
	"net/http"
	"strings"

	"backend/internal/model"
	"backend/internal/service"
	"github.com/gin-gonic/gin"
)

type CDKHandler struct {
	cdks *service.CDKService
}

func NewCDKHandler(cdks *service.CDKService) *CDKHandler {
	return &CDKHandler{cdks: cdks}
}

func (h *CDKHandler) List(c *gin.Context) {
	items, stats, names, err := h.cdks.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to load cdks"})
		return
	}

	// Server-side filtering (状态 未使用/已使用 · 类型 普通/营销 · 搜索兑换码) + pagination.
	statusFilter := strings.TrimSpace(c.Query("status")) // "" | active | used
	typeFilter := strings.TrimSpace(c.Query("type"))     // "" | normal | marketing
	q := strings.ToUpper(strings.TrimSpace(c.Query("q")))
	filtered := make([]model.CDKCode, 0, len(items))
	for _, item := range items {
		switch statusFilter {
		case "active":
			if item.Status != "active" {
				continue
			}
		case "used":
			if item.Status == "active" {
				continue
			}
		}
		switch typeFilter {
		case "marketing":
			if item.Type != "marketing" {
				continue
			}
		case "normal":
			if item.Type == "marketing" {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToUpper(item.Code), q) {
			continue
		}
		filtered = append(filtered, item)
	}

	total := len(filtered)
	limit, offset := pageParams(c, 20)
	page := pageSlice(filtered, limit, offset)
	c.JSON(http.StatusOK, gin.H{"data": cdkPublic(page, names), "total": total, "limit": limit, "offset": offset, "stats": stats})
}

func (h *CDKHandler) Create(c *gin.Context) {
	var body struct {
		Amount int    `json:"amount"`
		Count  int    `json:"count"`
		Note   string `json:"note"`
		Type   string `json:"type"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	items, err := h.cdks.Generate(c.Request.Context(), body.Amount, body.Count, body.Note, body.Type)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "created": cdkPublic(items, nil)})
}

func (h *CDKHandler) Delete(c *gin.Context) {
	if err := h.cdks.Delete(c.Request.Context(), c.Param("code")); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"detail": "cdk not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to delete cdk"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DeleteBulk removes multiple CDK codes in one call (multi-select).
func (h *CDKHandler) DeleteBulk(c *gin.Context) {
	var body struct {
		Codes []string `json:"codes"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	if len(body.Codes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "未选择任何兑换码"})
		return
	}
	n, err := h.cdks.DeleteBulk(c.Request.Context(), body.Codes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to delete cdks"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "deleted": n})
}

func cdkPublic(items []model.CDKCode, nameByID map[string]string) []gin.H {
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		var redeemedByName any
		if item.RedeemedBy != nil && *item.RedeemedBy != "" {
			if name, ok := nameByID[*item.RedeemedBy]; ok {
				redeemedByName = name
			}
		}
		out = append(out, gin.H{
			"code":             item.Code,
			"amount":           item.Amount,
			"status":           item.Status,
			"type":             item.Type,
			"batch_id":         item.BatchID,
			"note":             item.Note,
			"redeemed_by":      item.RedeemedBy,
			"redeemed_by_name": redeemedByName,
			"redeemed_at":      unixSecPtr(item.RedeemedAt),
			"created_at":       unixSec(item.CreatedAt),
		})
	}
	return out
}
