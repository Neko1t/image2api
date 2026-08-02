package adobe

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

const (
	refreshURL = "https://adobeid-na1.services.adobe.com/ims/check/v6/token?jslVersion=v2-v0.48.0-1-g1e322cb"
	clientID   = "projectx_webapp"
	scopeValue = "AdobeID,firefly_api,openid"
)

var ErrAdobeCookieEmpty = errors.New("cookie is empty")

type CookieExchangeResult struct {
	AccessToken string
	ExpiresIn   int
	Raw         map[string]any
}

func ExchangeCookieToAccessToken(ctx context.Context, client *http.Client, cookie string) (*CookieExchangeResult, error) {
	_ = client
	tlsClient, err := NewClient(clientID, "").newTLSClient()
	if err != nil {
		return nil, err
	}
	return exchangeCookieWithTLSClient(ctx, tlsClient, cookie)
}

func normalizeCookie(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(strings.ToLower(v), "cookie:") {
		v = strings.TrimSpace(v[len("cookie:"):])
	}
	return v
}
