package chatgpt

import "testing"

func TestContainsAsyncMarkerRecognizesStructuredImageToolCall(t *testing.T) {
	payload := `{"type":"message_stream_event","v":{"message":{"author":{"role":"assistant"},"recipient":"t2uay3k.sj1i4kz","content":{"content_type":"code","language":"json","text":"sensitive image arguments"},"status":"finished_successfully"}}}`
	if !containsAsyncMarker(payload) {
		t.Fatal("expected obfuscated structured image tool call to start async polling")
	}
}

func TestContainsAsyncMarkerRejectsNonToolMessages(t *testing.T) {
	tests := []string{
		`{"v":{"message":{"author":{"role":"assistant"},"recipient":"all","content":{"content_type":"code","language":"json"}}}}`,
		`{"v":{"message":{"author":{"role":"assistant"},"recipient":"tool-id","content":{"content_type":"text","language":"json"}}}}`,
		`{"v":{"message":{"author":{"role":"assistant"},"recipient":"tool-id","content":{"content_type":"code","language":"python"}}}}`,
		`{"v":{"message":{"author":{"role":"user"},"recipient":"tool-id","content":{"content_type":"code","language":"json"}}}}`,
		`not-json image_gen_task_missing`,
	}
	for _, payload := range tests {
		if containsAsyncMarker(payload) {
			t.Fatalf("unexpected async marker match for %s", payload)
		}
	}
}

func TestContainsAsyncMarkerKeepsLegacyMarkers(t *testing.T) {
	if !containsAsyncMarker(`{"image_gen_async":true}`) {
		t.Fatal("expected legacy image_gen_async marker to remain supported")
	}
}
