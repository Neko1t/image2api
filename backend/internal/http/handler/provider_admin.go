package handler

import (
	"errors"
	"net/http"

	"backend/internal/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ProviderAdminHandler struct {
	tokens  *service.TokenService
	refresh *service.RefreshProfileService
}

func NewProviderAdminHandler(tokens *service.TokenService, refresh *service.RefreshProfileService) *ProviderAdminHandler {
	return &ProviderAdminHandler{
		tokens:  tokens,
		refresh: refresh,
	}
}

func (h *ProviderAdminHandler) TokensList(c *gin.Context) {
	data, err := h.tokens.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to load tokens"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *ProviderAdminHandler) TokensCreate(c *gin.Context) {
	var body struct {
		Pool  string `json:"pool"`
		Value string `json:"value"`
		ID    string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	item, err := h.tokens.Add(c.Request.Context(), body.Pool, body.Value, body.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": item.ID})
}

func (h *ProviderAdminHandler) ImportChatGPTToken(c *gin.Context) {
	var body struct {
		AccessToken string `json:"access_token"`
		Value       string `json:"value"`
		Name        string `json:"name"`
		ID          string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	token := body.AccessToken
	if token == "" {
		token = body.Value
	}
	name := body.Name
	if name == "" {
		name = body.ID
	}
	item, err := h.tokens.ImportChatGPTToken(c.Request.Context(), token, name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": item.ID, "status": item.Status, "pending": item.Status == "pending"})
}

func (h *ProviderAdminHandler) ImportRunwayToken(c *gin.Context) {
	var body struct {
		AccessToken string `json:"access_token"`
		Value       string `json:"value"`
		Name        string `json:"name"`
		ID          string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	token := body.AccessToken
	if token == "" {
		token = body.Value
	}
	name := body.Name
	if name == "" {
		name = body.ID
	}
	item, err := h.tokens.ImportRunwayToken(c.Request.Context(), token, name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": item.ID, "status": item.Status, "pending": item.Status == "pending"})
}

func (h *ProviderAdminHandler) ImportGrokToken(c *gin.Context) {
	var body struct {
		AccessToken string `json:"access_token"`
		Value       string `json:"value"`
		SSO         string `json:"sso"`
		Name        string `json:"name"`
		ID          string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	token := body.AccessToken
	for _, v := range []string{body.Value, body.SSO} {
		if token == "" {
			token = v
		}
	}
	name := body.Name
	if name == "" {
		name = body.ID
	}
	item, err := h.tokens.ImportGrokToken(c.Request.Context(), token, name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": item.ID, "status": item.Status, "pending": item.Status == "pending"})
}

func (h *ProviderAdminHandler) ImportCustomAccount(c *gin.Context) {
	var body struct {
		BaseURL     string `json:"base_url"`
		URL         string `json:"url"`
		Key         string `json:"key"`
		APIKey      string `json:"api_key"`
		Models      string `json:"models"`
		Name        string `json:"name"`
		Weight      int    `json:"weight"`
		Concurrency int    `json:"concurrency"`
		ID          string `json:"id"`
		AdapterType string `json:"adapter_type"` // NEW: adapter format type
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	baseURL := body.BaseURL
	if baseURL == "" {
		baseURL = body.URL
	}
	key := body.Key
	if key == "" {
		key = body.APIKey
	}
	// Default to "openai" if not specified
	adapterType := body.AdapterType
	if adapterType == "" {
		adapterType = "openai"
	}
	item, err := h.tokens.ImportCustomAccountWithAdapter(c.Request.Context(), baseURL, key, body.Models, body.Name, body.Weight, body.Concurrency, adapterType, body.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": item.ID, "status": item.Status})
}

func (h *ProviderAdminHandler) ImportYCYAccount(c *gin.Context) {
	var body struct {
		BaseURL     string `json:"base_url"`
		URL         string `json:"url"`
		Key         string `json:"key"`
		APIKey      string `json:"api_key"`
		Models      string `json:"models"`
		Name        string `json:"name"`
		Weight      int    `json:"weight"`
		Concurrency int    `json:"concurrency"`
		ID          string `json:"id"`
		AdapterType string `json:"adapter_type"` // NEW: adapter format type
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	baseURL := body.BaseURL
	if baseURL == "" {
		baseURL = body.URL
	}
	key := body.Key
	if key == "" {
		key = body.APIKey
	}
	// Default to "ycy" if not specified
	adapterType := body.AdapterType
	if adapterType == "" {
		adapterType = "ycy"
	}
	item, err := h.tokens.ImportYCYAccountWithAdapter(c.Request.Context(), baseURL, key, body.Models, body.Name, body.Weight, body.Concurrency, adapterType, body.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": item.ID, "status": item.Status})
}

func (h *ProviderAdminHandler) ImportKreaCookie(c *gin.Context) {
	var body struct {
		Cookie string `json:"cookie"`
		Value  string `json:"value"`
		Name   string `json:"name"`
		ID     string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	cookie := body.Cookie
	if cookie == "" {
		cookie = body.Value
	}
	name := body.Name
	if name == "" {
		name = body.ID
	}
	item, err := h.tokens.ImportKreaCookie(c.Request.Context(), cookie, name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": item.ID, "status": item.Status, "pending": item.Status == "pending"})
}

func (h *ProviderAdminHandler) ImportImagineToken(c *gin.Context) {
	var body struct {
		Cookie string `json:"cookie"`
		Value  string `json:"value"`
		Name   string `json:"name"`
		ID     string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	cred := body.Cookie
	if cred == "" {
		cred = body.Value
	}
	name := body.Name
	if name == "" {
		name = body.ID
	}
	item, err := h.tokens.ImportImagineToken(c.Request.Context(), cred, name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": item.ID, "status": item.Status, "pending": item.Status == "pending"})
}

func (h *ProviderAdminHandler) ImportLeonardoCookie(c *gin.Context) {
	var body struct {
		Cookie string `json:"cookie"`
		Value  string `json:"value"`
		Name   string `json:"name"`
		ID     string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	cookie := body.Cookie
	if cookie == "" {
		cookie = body.Value
	}
	name := body.Name
	if name == "" {
		name = body.ID
	}
	item, err := h.tokens.ImportLeonardoCookie(c.Request.Context(), cookie, name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": item.ID, "status": item.Status, "pending": item.Status == "pending"})
}

func (h *ProviderAdminHandler) ImportAdobeCookie(c *gin.Context) {
	var body struct {
		Cookie string `json:"cookie"`
		Value  string `json:"value"`
		Name   string `json:"name"`
		ID     string `json:"id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	cookie := body.Cookie
	if cookie == "" {
		cookie = body.Value
	}
	name := body.Name
	if name == "" {
		name = body.ID
	}
	item, profile, err := h.tokens.ImportAdobeCookie(c.Request.Context(), cookie, name)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"profile_id": profile.ID,
		"id":         item.ID,
		"status":     item.Status,
		"pending":    item.Status == "pending",
	})
}

func (h *ProviderAdminHandler) TokenUpdate(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	item, err := h.tokens.Update(c.Request.Context(), c.Param("pool"), c.Param("id"), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": item})
}

func (h *ProviderAdminHandler) TokenDelete(c *gin.Context) {
	if err := h.tokens.Delete(c.Request.Context(), c.Param("pool"), c.Param("id")); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"detail": "token not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// TokenDeleteBulk removes multiple accounts in one call (account multi-select).
func (h *ProviderAdminHandler) TokenDeleteBulk(c *gin.Context) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	if len(body.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "未选择任何账号"})
		return
	}
	n, err := h.tokens.DeleteBulk(c.Request.Context(), body.IDs)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "deleted": n})
}

func (h *ProviderAdminHandler) AccountsList(c *gin.Context) {
	data, err := h.tokens.Accounts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to load accounts"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *ProviderAdminHandler) AccountQuota(c *gin.Context) {
	data, err := h.tokens.Quota(c.Request.Context(), c.Param("pool"), c.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"detail": "account not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *ProviderAdminHandler) AccountEmail(c *gin.Context) {
	data, err := h.tokens.Email(c.Request.Context(), c.Param("pool"), c.Param("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"detail": "account not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

func (h *ProviderAdminHandler) RefreshProfiles(c *gin.Context) {
	items, err := h.refresh.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to load refresh profiles"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func (h *ProviderAdminHandler) RefreshNow(c *gin.Context) {
	if err := h.refresh.RefreshNow(c.Request.Context(), c.Param("profile_id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *ProviderAdminHandler) RefreshUpdate(c *gin.Context) {
	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	item, err := h.refresh.Update(c.Request.Context(), c.Param("profile_id"), body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": item})
}

func (h *ProviderAdminHandler) RefreshDelete(c *gin.Context) {
	if err := h.refresh.Delete(c.Request.Context(), c.Param("profile_id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
