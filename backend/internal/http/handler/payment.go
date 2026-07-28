package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"backend/internal/model"
	"backend/internal/service"
	"github.com/gin-gonic/gin"
)

type PaymentHandler struct {
	pay *service.PaymentService
}

func NewPaymentHandler(pay *service.PaymentService) *PaymentHandler {
	return &PaymentHandler{pay: pay}
}

func orderJSON(o *model.Order) gin.H {
	h := gin.H{
		"id":            o.ID,
		"amount":        o.Amount,
		"points":        o.Points,
		"pay_type":      o.PayType,
		"status":        o.Status,
		"pay_info":      o.PayInfo,
		"pay_info_type": o.PayInfoType,
		"source":        o.Source,
		"remark":        o.Remark,
		"created_at":    o.CreatedAt.Unix(),
		"expires_at":    o.ExpiresAt.Unix(),
		"server_now":    time.Now().Unix(), // lets the popup count down on server time
	}
	if o.PaidAt != nil {
		h["paid_at"] = o.PaidAt.Unix()
	}
	return h
}

// ---- user ----

// Config returns the recharge config for the user UI (enabled, methods, min, ratio).
func (h *PaymentHandler) Config(c *gin.Context) {
	c.JSON(http.StatusOK, h.pay.Public(c.Request.Context()))
}

func (h *PaymentHandler) Recharge(c *gin.Context) {
	user := currentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"detail": "未登录或会话已过期"})
		return
	}
	var body struct {
		Amount float64 `json:"amount"`
		Method string  `json:"method"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	base := requestBaseURL(c)
	if strings.HasPrefix(base, "http://") {
		base = "https://" + strings.TrimPrefix(base, "http://")
	}
	order, err := h.pay.CreateOrder(c.Request.Context(), user, body.Amount, body.Method, base, clientIP(c))
	if err != nil {
		h.writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, orderJSON(order))
}

func (h *PaymentHandler) MyOrders(c *gin.Context) {
	user := currentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"detail": "未登录或会话已过期"})
		return
	}
	limit := parseInt(c.Query("limit"), 20)
	offset := parseInt(c.Query("offset"), 0)
	orders, total, err := h.pay.ListByUser(c.Request.Context(), user.ID, c.Query("status"), strings.TrimSpace(c.Query("q")), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to load orders"})
		return
	}
	out := make([]gin.H, 0, len(orders))
	for i := range orders {
		out = append(out, orderJSON(&orders[i]))
	}
	c.JSON(http.StatusOK, gin.H{"data": out, "total": total})
}

func (h *PaymentHandler) OrderStatus(c *gin.Context) {
	user := currentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"detail": "未登录或会话已过期"})
		return
	}
	order, err := h.pay.GetForUser(c.Request.Context(), user.ID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "订单不存在"})
		return
	}
	c.JSON(http.StatusOK, orderJSON(order))
}

func (h *PaymentHandler) ContinueOrder(c *gin.Context) {
	user := currentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"detail": "未登录或会话已过期"})
		return
	}
	base := requestBaseURL(c)
	if strings.HasPrefix(base, "http://") {
		base = "https://" + strings.TrimPrefix(base, "http://")
	}
	order, err := h.pay.Continue(c.Request.Context(), user, c.Param("id"), base, clientIP(c))
	if err != nil {
		h.writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, orderJSON(order))
}

// ---- admin ----

func (h *PaymentHandler) AdminOrders(c *gin.Context) {
	status := c.Query("status")
	limit := parseInt(c.Query("limit"), 100)
	offset := parseInt(c.Query("offset"), 0)
	orders, total, err := h.pay.ListAll(c.Request.Context(), status, strings.TrimSpace(c.Query("source")), strings.TrimSpace(c.Query("q")), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "failed to load orders"})
		return
	}
	names := h.pay.UserNames(c.Request.Context())
	out := make([]gin.H, 0, len(orders))
	for i := range orders {
		row := orderJSON(&orders[i])
		row["user_name"] = names[orders[i].UserID]
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"data": out, "total": total})
}

func (h *PaymentHandler) SettingsGet(c *gin.Context) {
	c.JSON(http.StatusOK, h.pay.Settings(c.Request.Context()))
}

func (h *PaymentHandler) SettingsSave(c *gin.Context) {
	var in service.PaySettings
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request body"})
		return
	}
	if err := h.pay.SaveSettings(c.Request.Context(), in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": strings.TrimSpace(err.Error())})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- public async notify (no auth — called by the 易支付 server) ----

func (h *PaymentHandler) Notify(c *gin.Context) {
	params := map[string]string{}
	for k, v := range c.Request.URL.Query() {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	if _, err := h.pay.HandleNotify(c.Request.Context(), params); err != nil {
		c.String(http.StatusOK, "fail")
		return
	}
	// 易支付 expects the literal string "success" to stop re-notifying.
	c.String(http.StatusOK, "success")
}

func (h *PaymentHandler) writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrPayDisabled):
		c.JSON(http.StatusForbidden, gin.H{"detail": "充值已关闭"})
	case errors.Is(err, service.ErrPayNotConfig):
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "支付未配置"})
	case errors.Is(err, service.ErrPayMethod):
		c.JSON(http.StatusBadRequest, gin.H{"detail": "不支持的支付方式"})
	case errors.Is(err, service.ErrPayAmount):
		c.JSON(http.StatusBadRequest, gin.H{"detail": "金额低于最低充值额"})
	case errors.Is(err, service.ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"detail": "订单不存在"})
	case errors.Is(err, service.ErrOrderPaid):
		c.JSON(http.StatusBadRequest, gin.H{"detail": "订单已支付"})
	default:
		c.JSON(http.StatusBadGateway, gin.H{"detail": strings.TrimSpace(err.Error())})
	}
}
