package adobe

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestCurrentProjectXIdentity(t *testing.T) {
	if clientID != "projectx_webapp" {
		t.Fatalf("clientID = %q, want projectx_webapp", clientID)
	}
	if scopeValue != "AdobeID,firefly_api,openid" {
		t.Fatalf("scopeValue = %q", scopeValue)
	}
	if got := NewClient("", "").apiKey; got != clientID {
		t.Fatalf("default api key = %q, want %q", got, clientID)
	}
}

func TestGPTImagePayloadUsesModelSpecificSize(t *testing.T) {
	candidates := BuildImagePayloadCandidates("firefly-gpt-image-2", "test", "16:9", "2K", nil)
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(candidates))
	}
	payload := candidates[0]
	if _, ok := payload["size"]; ok {
		t.Fatalf("top-level size must be absent: %#v", payload["size"])
	}
	modelPayload, ok := payload["modelSpecificPayload"].(map[string]any)
	if !ok {
		t.Fatalf("modelSpecificPayload = %#v, want object", payload["modelSpecificPayload"])
	}
	if modelPayload["size"] != "2560x1440" {
		t.Fatalf("modelSpecificPayload.size = %#v, want 2560x1440", modelPayload["size"])
	}
}

func TestARPSessionIDShape(t *testing.T) {
	parseARPSessionID(t, buildARPSessionID())
}

func TestExtractUserIDFromEncodedCookie(t *testing.T) {
	const want = "4BDA81F069FC6DA40A495FAB@AdobeID"
	cookie := "foo=bar; adobe_identity=4BDA81F069FC6DA40A495FAB%40AdobeID"
	if got := extractUserIDFromCookie(cookie); got != want {
		t.Fatalf("user id = %q, want %q", got, want)
	}
}

func TestNormalizePollURL(t *testing.T) {
	raw := "https://firefly-epo1234.adobe.io/v2/jobs/abc-123"
	want := "https://bks-epo1234.adobe.io/v2/jobs/result/abc-123?host=firefly-epo1234.adobe.io/"
	if got := normalizePollURL(raw); got != want {
		t.Fatalf("normalizePollURL() = %q, want %q", got, want)
	}
	other := "https://example.com/jobs/abc-123"
	if got := normalizePollURL(other); got != other {
		t.Fatalf("non-Adobe URL changed to %q", got)
	}
}

func parseARPSessionID(t *testing.T, encoded string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode ARP session: %v", err)
	}
	var payload struct {
		SID string `json:"sid"`
		FTR string `json:"ftr"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal ARP session: %v", err)
	}
	if payload.SID == "" {
		t.Fatal("ARP sid is empty")
	}
	parts := strings.SplitN(payload.FTR, "_", 4)
	if len(parts) != 4 || len(parts[0]) != 32 || parts[3] != "dUAL43-mnts-ants-d4_31ck__tt" {
		t.Fatalf("unexpected ARP feature shape %q", payload.FTR)
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		t.Fatalf("invalid ARP timestamp %q: %v", parts[1], err)
	}
	pid, err := strconv.Atoi(parts[2])
	if err != nil || pid < 1000 || pid > 99999 {
		t.Fatalf("invalid ARP pid %q", parts[2])
	}
}
