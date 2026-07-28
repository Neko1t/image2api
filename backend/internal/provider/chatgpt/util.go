package chatgpt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	baseURL                  = "https://chatgpt.com"
	defaultUserAgent         = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36 Edg/149.0.0.0"
	defaultClientVersion     = "prod-db390ebea64862bf1899c420a4c736e0cf639747"
	defaultClientBuildNumber = "7904904"
	defaultPOWScript         = "https://chatgpt.com/backend-api/sentinel/sdk.js"
	// sseAsyncGrace bounds how long startImageGeneration keeps reading the SSE
	// waiting for the image_gen_async marker after the conversation id is known.
	// The marker normally arrives within ~1s; when it never streams (intermittent
	// on gpt-5-5-thinking) we must not hold the stream open for the whole ctx
	// budget, so we break after this grace and fall through to polling.
	sseAsyncGrace = 10 * time.Second
)

var (
	fileServiceIDPattern = regexp.MustCompile(`file-service://([A-Za-z0-9_-]+)`)
	sedimentIDPattern    = regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`)
	realImageIDPattern   = regexp.MustCompile(`\bfile_00000000[a-f0-9]{24}\b`)
	conversationIDRE     = regexp.MustCompile(`"conversation_id"\s*:\s*"([^"]+)"`)
	scriptSrcRE          = regexp.MustCompile(`<script[^>]+src="([^"]+)"`)
	dataBuildPathRE      = regexp.MustCompile(`c/[^/]*/_`)
	htmlDataBuildRE      = regexp.MustCompile(`<html[^>]*data-build="([^"]*)"`)
	sseDebugKeyRE        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	sseDebugProtocolRE   = regexp.MustCompile(`^[A-Za-z0-9_.:/~-]{1,48}$`)

	// asyncMarkers signal that ChatGPT accepted the prompt and switched to the
	// async image pipeline (image is delivered later via conversation polling
	// rather than inline in the SSE stream). Their presence means "generating —
	// keep polling", NOT failure.
	asyncMarkers = []string{"image_gen_async", "image_gen_task_id", "trigger_async_ux"}

	// contentPolicyMarkers are stable substrings of ChatGPT's content-audit
	// refusal message. When one appears in an assistant turn the prompt was
	// rejected upstream — no image will ever arrive, so we must fail fast
	// instead of polling until timeout.
	contentPolicyMarkers = []string{
		"this request may violate our content polic",
		"this prompt may violate our content polic",
		"may violate our content policies",
		"i can't help with",
		"i can\u2019t help with",
		"i cannot help with",
		"i can't assist with",
		"i can\u2019t assist with",
		"i cannot assist with",
		"i'm unable to help with",
		"i\u2019m unable to help with",
		"can't create images",
		"can\u2019t create images",
		"cannot create images",
		"can't generate images",
		"can\u2019t generate images",
		"cannot generate images",
		"unable to create images",
		"unable to generate images",
	}
)

type sseDebugSummary struct {
	ChunkCount        int      `json:"chunk_count"`
	JSONChunkCount    int      `json:"json_chunk_count"`
	NonJSONChunkCount int      `json:"non_json_chunk_count"`
	ByteCount         int      `json:"byte_count"`
	KeyPaths          []string `json:"key_paths"`
	Signals           []string `json:"signals"`
}

var sseDebugSignalKeys = map[string]struct{}{
	"code":                 {},
	"channel":              {},
	"content_type":         {},
	"default_model_slug":   {},
	"end_turn":             {},
	"error_code":           {},
	"event":                {},
	"finish_type":          {},
	"is_complete":          {},
	"kind":                 {},
	"language":             {},
	"marker":               {},
	"message_type":         {},
	"model_slug":           {},
	"recipient":            {},
	"response_format_name": {},
	"role":                 {},
	"status":               {},
	"type":                 {},
}

// These fields are upstream-controlled protocol enums. Their short identifier
// values are useful for identifying a newly routed tool while user-controlled
// content fields remain length-only.
var sseDebugProtocolEnumKeys = map[string]struct{}{
	"channel":              {},
	"default_model_slug":   {},
	"error_code":           {},
	"event":                {},
	"kind":                 {},
	"language":             {},
	"marker":               {},
	"message_type":         {},
	"model_slug":           {},
	"recipient":            {},
	"response_format_name": {},
	"type":                 {},
}

var sseDebugSafeEnums = map[string]struct{}{
	"assistant":              {},
	"code":                   {},
	"delta":                  {},
	"done":                   {},
	"error":                  {},
	"failed":                 {},
	"finished":               {},
	"finished_successfully":  {},
	"in_progress":            {},
	"message":                {},
	"model_editable_context": {},
	"multimodal_text":        {},
	"rate_limit_exceeded":    {},
	"system":                 {},
	"text":                   {},
	"thoughts":               {},
	"token_expired":          {},
	"tool":                   {},
	"user":                   {},
}

// summarizeSSEChunks records only payload shape and explicitly safe enum values.
// Unknown strings are reduced to their length, so prompts and credentials cannot
// leak into logs while diagnosing private ChatGPT SSE protocol drift.
func summarizeSSEChunks(chunks []string) string {
	summary := sseDebugSummary{ChunkCount: len(chunks)}
	paths := make(map[string]struct{})
	signals := make(map[string]struct{})
	for _, chunk := range chunks {
		summary.ByteCount += len(chunk)
		var payload any
		if err := json.Unmarshal([]byte(chunk), &payload); err != nil {
			summary.NonJSONChunkCount++
			continue
		}
		summary.JSONChunkCount++
		collectSSEDebugShape(payload, "", 0, paths, signals)
	}
	summary.KeyPaths = sortedSet(paths)
	summary.Signals = sortedSet(signals)
	body, err := json.Marshal(summary)
	if err != nil {
		return `{"error":"summary_marshal_failed"}`
	}
	return string(body)
}

func collectSSEDebugShape(value any, path string, depth int, paths, signals map[string]struct{}) {
	if depth >= 8 || len(paths) >= 256 {
		return
	}
	switch item := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(item))
		for key := range item {
			if sseDebugKeyRE.MatchString(key) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			paths[childPath] = struct{}{}
			if _, ok := sseDebugSignalKeys[key]; ok {
				if signal, ok := safeSSEDebugSignal(key, item[key]); ok {
					signals[childPath+"="+signal] = struct{}{}
				}
			}
			collectSSEDebugShape(item[key], childPath, depth+1, paths, signals)
			if len(paths) >= 256 {
				return
			}
		}
	case []any:
		arrayPath := path + "[]"
		paths[arrayPath] = struct{}{}
		for _, child := range item {
			collectSSEDebugShape(child, arrayPath, depth+1, paths, signals)
			if len(paths) >= 256 {
				return
			}
		}
	}
}

func safeSSEDebugSignal(key string, value any) (string, bool) {
	switch item := value.(type) {
	case bool:
		return strconv.FormatBool(item), true
	case float64:
		return "<number>", true
	case string:
		item = strings.TrimSpace(item)
		if _, ok := sseDebugSafeEnums[item]; ok {
			return item, true
		}
		if _, ok := sseDebugProtocolEnumKeys[key]; ok && sseDebugProtocolRE.MatchString(item) {
			return item, true
		}
		return fmt.Sprintf("<string:length=%d>", len(item)), true
	default:
		return "", false
	}
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// containsAsyncMarker reports whether the SSE payload indicates the async image
// pipeline was engaged.
func containsAsyncMarker(text string) bool {
	for _, m := range asyncMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// detectContentPolicyRejection reports whether text contains a ChatGPT content
// audit refusal. Matching is case-insensitive for the English variants.
func detectContentPolicyRejection(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, m := range contentPolicyMarkers {
		if strings.Contains(text, m) || strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// collectText concatenates every string found under value (recursively) into sb.
func collectText(value any, sb *strings.Builder) {
	switch x := value.(type) {
	case string:
		sb.WriteString(x)
		sb.WriteByte('\n')
	case map[string]any:
		for _, item := range x {
			collectText(item, sb)
		}
	case []any:
		for _, item := range x {
			collectText(item, sb)
		}
	}
}

// conversationRejected scans assistant turns of a fetched conversation for a
// content-policy refusal.
func conversationRejected(conversation map[string]any) bool {
	mapping, _ := conversation["mapping"].(map[string]any)
	for _, rawNode := range mapping {
		node, _ := rawNode.(map[string]any)
		message, _ := node["message"].(map[string]any)
		if message == nil {
			continue
		}
		author, _ := message["author"].(map[string]any)
		role := strings.ToLower(strings.TrimSpace(stringValue(author["role"])))
		if role != "assistant" {
			continue
		}
		var sb strings.Builder
		collectText(message["content"], &sb)
		if detectContentPolicyRejection(sb.String()) {
			return true
		}
	}
	return false
}

// conversationEndedWithoutImage reports whether the model finished its turn
// with a plain-text answer and never engaged the async image pipeline (no tool
// turn, no image_gen task). ChatGPT localizes audit refusals, so this catches
// rejections in ANY language structurally: a closed text-only turn can never
// produce an image, and polling further only burns the budget.
func conversationEndedWithoutImage(conversation map[string]any) bool {
	mapping, _ := conversation["mapping"].(map[string]any)
	if len(mapping) == 0 {
		return false
	}
	for _, rawNode := range mapping {
		node, _ := rawNode.(map[string]any)
		message, _ := node["message"].(map[string]any)
		if message == nil {
			continue
		}
		if metadata, _ := message["metadata"].(map[string]any); metadata != nil {
			if stringValue(metadata["image_gen_task_id"]) != "" || metadata["image_gen_async"] == true {
				return false
			}
		}
		author, _ := message["author"].(map[string]any)
		role := strings.ToLower(strings.TrimSpace(stringValue(author["role"])))
		if role == "tool" {
			return false
		}
		if role == "assistant" {
			if content, _ := message["content"].(map[string]any); content != nil {
				// Only tool-invoking content types mean generation is underway.
				// Context nodes (model_editable_context, thoughts, …) also appear
				// on refused turns and must NOT suppress the detection.
				switch stringValue(content["content_type"]) {
				case "code", "multimodal_text":
					return false
				}
			}
		}
	}
	for _, rawNode := range mapping {
		node, _ := rawNode.(map[string]any)
		message, _ := node["message"].(map[string]any)
		if message == nil {
			continue
		}
		author, _ := message["author"].(map[string]any)
		if strings.ToLower(strings.TrimSpace(stringValue(author["role"]))) != "assistant" {
			continue
		}
		if message["end_turn"] != true || stringValue(message["status"]) != "finished_successfully" {
			continue
		}
		var sb strings.Builder
		collectText(message["content"], &sb)
		if strings.TrimSpace(sb.String()) != "" {
			return true
		}
	}
	return false
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func intValue(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case float32:
		return int(x)
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	default:
		return 0
	}
}

func decodeJWTPayload(token string) map[string]any {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func newUUID() string {
	return uuid.NewString()
}

func clip(v []byte, n int) string {
	s := strings.TrimSpace(string(v))
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func parsePOWResources(html string) ([]string, string) {
	matches := scriptSrcRE.FindAllStringSubmatch(html, -1)
	sources := make([]string, 0, len(matches))
	dataBuild := ""
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		src := strings.TrimSpace(match[1])
		if src == "" {
			continue
		}
		sources = append(sources, src)
		if dataBuild == "" {
			if path := dataBuildPathRE.FindString(src); path != "" {
				dataBuild = path
			}
		}
	}
	if dataBuild == "" {
		if match := htmlDataBuildRE.FindStringSubmatch(html); len(match) >= 2 {
			dataBuild = strings.TrimSpace(match[1])
		}
	}
	if len(sources) == 0 {
		sources = []string{defaultPOWScript}
	}
	return sources, dataBuild
}

func timeMillis() int64 {
	return time.Now().UnixMilli()
}
