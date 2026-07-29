package chatgpt

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummarizeSSEChunksReportsShapeWithoutSensitiveValues(t *testing.T) {
	chunks := []string{
		`{"conversation_id":"conv-secret-id","message":{"author":{"role":"assistant"},"status":"finished_successfully","end_turn":true,"content":{"content_type":"text","parts":["private prompt text"]},"metadata":{"new_image_task_state":"queued"}},"access_token":"secret-at-value","cookie":"secret-cookie","email":"person@example.com"}`,
		`not-json private prompt text secret-at-value`,
	}

	got := summarizeSSEChunks(chunks)
	for _, secret := range []string{
		"conv-secret-id",
		"private prompt text",
		"secret-at-value",
		"secret-cookie",
		"person@example.com",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("summary leaked sensitive value %q: %s", secret, got)
		}
	}
	for _, expected := range []string{
		`"chunk_count":2`,
		`"json_chunk_count":1`,
		`"non_json_chunk_count":1`,
		`message.content.parts[]`,
		`message.metadata.new_image_task_state`,
		`message.author.role=assistant`,
		`message.status=finished_successfully`,
		`message.end_turn=true`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("summary missing %q: %s", expected, got)
		}
	}
}

func TestSummarizeSSEChunksRedactsFreeTextSignalValues(t *testing.T) {
	got := summarizeSSEChunks([]string{
		`{"status":"secret-token-value","type":"message","code":"another-secret-token"}`,
	})

	for _, secret := range []string{"secret-token-value", "another-secret-token"} {
		if strings.Contains(got, secret) {
			t.Fatalf("summary leaked signal value %q: %s", secret, got)
		}
	}
	var summary sseDebugSummary
	if err := json.Unmarshal([]byte(got), &summary); err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	for _, expected := range []string{`type=message`, `code=<string:length=20>`} {
		if !containsExactSSEDebugValue(summary.Signals, expected) {
			t.Fatalf("summary missing safe signal %q: %s", expected, got)
		}
	}
	if !containsExactSSEDebugValue(summary.Signals, `status=<string:length=18>`) {
		t.Fatalf("summary missing redacted status classification: %s", got)
	}
}

func TestSummarizeSSEChunksReportsProtocolEnums(t *testing.T) {
	got := summarizeSSEChunks([]string{
		`{"type":"message_stream_event","event":"delta","v":{"error_code":"tool_unavailable","message":{"recipient":"image_gen_tool","metadata":{"model_slug":"gpt-5-2"}}}}`,
	})
	var summary sseDebugSummary
	if err := json.Unmarshal([]byte(got), &summary); err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	for _, expected := range []string{
		"type=message_stream_event",
		"event=delta",
		"v.error_code=tool_unavailable",
		"v.message.recipient=image_gen_tool",
		"v.message.metadata.model_slug=gpt-5-2",
	} {
		if !containsExactSSEDebugValue(summary.Signals, expected) {
			t.Fatalf("summary missing protocol enum %q: %s", expected, got)
		}
	}
}

func containsExactSSEDebugValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
