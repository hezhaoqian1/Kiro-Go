package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeUpstreamUsageOverridesEstimates(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "buffered"
		if streaming {
			name = "streamed"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "OK"}))
				w.Write(awsEventStreamFrame(t, "contextUsageEvent", map[string]interface{}{"contextUsagePercentage": 12.5}))
				w.Write(awsEventStreamFrame(t, "metadataEvent", map[string]interface{}{"stopReason": "end_turn", "tokenUsage": map[string]interface{}{"uncachedInputTokens": 10, "cacheReadInputTokens": 120, "cacheWriteInputTokens": 30, "outputTokens": 17}}))
			}))
			defer server.Close()
			handler := setupIntegrityPathTest(t, server)
			body, _ := json.Marshal(map[string]interface{}{"model": "claude-sonnet-5", "stream": streaming, "thinking": map[string]string{"type": "disabled"}, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
			response := httptest.NewRecorder()
			handler.handleClaudeMessages(response, httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)))
			var usage map[string]interface{}
			if streaming {
				for _, line := range strings.Split(response.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event map[string]interface{}
					json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event)
					if event["type"] == "message_delta" {
						usage, _ = event["usage"].(map[string]interface{})
					}
				}
			} else {
				var result map[string]interface{}
				json.Unmarshal(response.Body.Bytes(), &result)
				usage, _ = result["usage"].(map[string]interface{})
			}
			if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(17) || usage["cache_read_input_tokens"] != float64(120) || usage["cache_creation_input_tokens"] != float64(30) {
				t.Fatalf("native usage overwritten: %s", response.Body.String())
			}
			if _, ok := usage["cache_creation"]; ok {
				t.Fatalf("TTL breakdown invented: %+v", usage)
			}
		})
	}
}

func TestInvalidStateEventCannotBecomeSuccessfulEOF(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "partial"}))
	stream.Write(awsEventStreamFrame(t, "invalidStateEvent", map[string]interface{}{"reason": "INVALID_STATE", "message": "rejected"}))
	completed := false
	_, err := parseEventStreamTracked(&stream, &KiroStreamCallback{OnComplete: func(int, int) { completed = true }})
	if err == nil || completed {
		t.Fatalf("invalid state was accepted: err=%v completed=%v", err, completed)
	}
}
