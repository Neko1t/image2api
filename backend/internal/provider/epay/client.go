// Package epay implements the 易支付 mapi (API 下单) interface with MD5 signing.
// POST {api_base}/mapi.php → JSON {code, msg, trade_no, payurl, qrcode}.
// Docs: 请求字段 pid/type/out_trade_no/notify_url/return_url/name/money/sign/sign_type.
package epay

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Config struct {
	APIBase string // 易支付站点根地址,如 https://www.ezfpy.cn(自动拼 /mapi.php)
	PID     string // 商户ID
	Key     string // 商户密钥
}

type CreateRequest struct {
	OutTradeNo string
	Type       string // wxpay | alipay | unionpay
	Name       string
	Money      string // "10.00"
	NotifyURL  string
	ReturnURL  string // 页面跳转通知地址(必填)
	ClientIP   string // unused by mapi; kept for caller convenience
}

type CreateResult struct {
	TradeNo string // 平台订单号
	PayType string // qrcode | jump
	PayInfo string // 二维码内容 或 跳转 url
}

// mapiResp is the raw mapi response. code: 1 或 200 为成功,其它为失败.
type mapiResp struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	TradeNo string `json:"trade_no"`
	PayURL  string `json:"payurl"`
	QRCode  string `json:"qrcode"`
}

var httpClient = &http.Client{Timeout: 20 * time.Second}

// sign builds the MD5 signature: take all params except sign/sign_type and empty
// values, sort keys ASCII-ascending, join as k=v&k=v (raw values), append the
// merchant key, MD5, lowercase hex.
func sign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params[k])
	}
	b.WriteString(key)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Create places a mapi order and returns the payment info (qrcode preferred).
func (c *Config) Create(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	params := map[string]string{
		"pid":          c.PID,
		"type":         req.Type,
		"out_trade_no": req.OutTradeNo,
		"notify_url":   req.NotifyURL,
		"name":         req.Name,
		"money":        req.Money,
		"return_url":   req.ReturnURL,
		"sign_type":    "MD5",
	}
	params["sign"] = sign(params, c.Key)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	endpoint := strings.TrimRight(c.APIBase, "/") + "/mapi.php"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out mapiResp
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("epay: bad response: %s", strings.TrimSpace(string(body)))
	}
	if out.Code != 1 && out.Code != 200 {
		msg := out.Msg
		if msg == "" {
			msg = "下单失败"
		}
		return nil, fmt.Errorf("epay: %s", msg)
	}
	result := &CreateResult{TradeNo: out.TradeNo}
	if out.QRCode != "" {
		result.PayType = "qrcode"
		result.PayInfo = out.QRCode
	} else if out.PayURL != "" {
		result.PayType = "jump"
		result.PayInfo = out.PayURL
	} else {
		return nil, fmt.Errorf("epay: 响应缺少 qrcode/payurl")
	}
	return result, nil
}

// VerifyNotify validates an async-notify callback's MD5 signature.
func (c *Config) VerifyNotify(params map[string]string) bool {
	got := strings.TrimSpace(params["sign"])
	if got == "" {
		return false
	}
	return strings.EqualFold(got, sign(params, c.Key))
}
