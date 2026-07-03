package ycy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	ErrAuth              = errors.New("ycy auth failed")
	ErrQuotaExhausted    = errors.New("ycy quota exhausted")
	ErrTemporaryUpstream = errors.New("ycy upstream temporary error")
)

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Minute},
	}
}

func (c *Client) CreateVideo(ctx context.Context, baseURL, apiKey, model, prompt, ratio string, references []string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" || apiKey == "" {
		return "", ErrAuth
	}
	payload := map[string]any{
		"model":  model,
		"prompt": prompt,
	}
	if ratio != "" {
		payload["ratio"] = ratio
	}
	var refs []string
	for _, ref := range references {
		if v := strings.TrimSpace(ref); v != "" {
			refs = append(refs, v)
		}
	}
	if len(refs) == 1 {
		payload["image"] = refs[0]
	} else if len(refs) > 1 {
		payload["images"] = refs
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/video/generations", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrTemporaryUpstream, sanitizeErr(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if err := mapStatus(resp.StatusCode, body); err != nil {
		return "", err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("%w: invalid json", ErrTemporaryUpstream)
	}
	if taskID := strings.TrimSpace(stringValue(out["task_id"])); taskID != "" {
		return taskID, nil
	}
	if taskID := strings.TrimSpace(stringValue(out["id"])); taskID != "" {
		return taskID, nil
	}
	return "", fmt.Errorf("%w: missing task_id", ErrTemporaryUpstream)
}

func (c *Client) GenerateVideo(ctx context.Context, baseURL, apiKey, model, prompt, ratio string, references []string, downloadResult bool) ([]byte, string, error) {
	taskID, err := c.CreateVideo(ctx, baseURL, apiKey, model, prompt, ratio, references)
	if err != nil {
		return nil, "", err
	}
	pollInterval := 15 * time.Second
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		status, contentURL, err := c.GetVideo(ctx, baseURL, apiKey, taskID)
		if err != nil {
			return nil, "", err
		}
		switch status {
		case "SUCCESS":
			if contentURL == "" {
				contentURL = strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/v1/videos/" + taskID + "/content"
			}
			if !downloadResult {
				return nil, contentURL, nil
			}
			data, ct, err := c.Download(ctx, contentURL, apiKey)
			if err != nil {
				return nil, "", err
			}
			_ = ct
			return data, contentURL, nil
		case "FAILURE":
			return nil, "", fmt.Errorf("%w: task %s failed", ErrTemporaryUpstream, taskID)
		case "QUEUED", "IN_PROGRESS", "":
			timer := time.NewTimer(pollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, "", ctx.Err()
			case <-timer.C:
			}
		default:
			timer := time.NewTimer(pollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, "", ctx.Err()
			case <-timer.C:
			}
		}
	}
}

func (c *Client) GetVideo(ctx context.Context, baseURL, apiKey, taskID string) (string, string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	taskID = strings.TrimSpace(taskID)
	if baseURL == "" || apiKey == "" {
		return "", "", ErrAuth
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/video/generations/"+taskID, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrTemporaryUpstream, sanitizeErr(err))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if err := mapStatus(resp.StatusCode, body); err != nil {
		return "", "", err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", fmt.Errorf("%w: invalid json", ErrTemporaryUpstream)
	}
	if data, ok := out["data"].(map[string]any); ok {
		out = data
	}
	status := strings.ToUpper(strings.TrimSpace(stringValue(out["status"])))
	if status == "" {
		status = strings.ToUpper(strings.TrimSpace(stringValue(out["state"])))
	}
	contentURL := strings.TrimSpace(stringValue(out["result_url"]))
	if contentURL == "" {
		contentURL = strings.TrimSpace(stringValue(out["content_url"]))
	}
	if contentURL != "" && strings.HasPrefix(contentURL, "/") {
		contentURL = strings.TrimRight(strings.TrimSpace(baseURL), "/") + contentURL
	}
	return status, contentURL, nil
}

func (c *Client) Download(ctx context.Context, url, apiKey string) ([]byte, string, error) {
	url = strings.TrimSpace(url)
	apiKey = strings.TrimSpace(apiKey)
	if url == "" || apiKey == "" {
		return nil, "", ErrAuth
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrTemporaryUpstream, sanitizeErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("%w: download %d", ErrTemporaryUpstream, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("%w: empty download", ErrTemporaryUpstream)
	}
	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if ct == "" {
		ct = "video/mp4"
	}
	return body, ct, nil
}

func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "context deadline exceeded"), strings.Contains(s, "Client.Timeout"), strings.Contains(s, "timeout"):
		return "request timeout"
	case strings.Contains(s, "connection refused"):
		return "connection refused"
	case strings.Contains(s, "no such host"), strings.Contains(s, "dial tcp"), strings.Contains(s, "lookup "):
		return "cannot reach upstream"
	case strings.Contains(s, "tls"), strings.Contains(s, "TLS"), strings.Contains(s, "certificate"):
		return "TLS error"
	case strings.Contains(s, "EOF"), strings.Contains(s, "reset by peer"), strings.Contains(s, "broken pipe"):
		return "connection reset"
	}
	return "upstream request failed"
}

func mapStatus(status int, body []byte) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == 401 || status == 403:
		return fmt.Errorf("%w: %d %s", ErrAuth, status, clip(body, 160))
	case status == 429:
		return fmt.Errorf("%w: %d %s", ErrQuotaExhausted, status, clip(body, 160))
	default:
		return fmt.Errorf("%w: %d %s", ErrTemporaryUpstream, status, clip(body, 160))
	}
}

func clip(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
