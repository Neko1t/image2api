package chatgpt

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	sseDebugKeyRE      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	sseDebugProtocolRE = regexp.MustCompile(`^[A-Za-z0-9_.:/~-]{1,48}$`)
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

// These fields contain upstream-controlled protocol enums. Short identifier
// values help identify protocol changes while free-form content stays redacted.
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

// summarizeSSEChunks records only payload shape and explicitly safe enum
// values. Unknown strings are reduced to their length so prompts and
// credentials cannot leak into logs while diagnosing ChatGPT protocol drift.
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
	summary.KeyPaths = sortedSSEDebugSet(paths)
	summary.Signals = sortedSSEDebugSet(signals)
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

func sortedSSEDebugSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
