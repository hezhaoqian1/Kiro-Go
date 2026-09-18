package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func matrixRequest(protocol string, streaming bool) *http.Request {
	body := fmt.Sprintf(`{"model":"claude-sonnet-5-thinking","max_tokens":4096,"input":"hi","messages":[{"role":"user","content":"hi"}],"store":false,"stream":%t}`, streaming)
	return httptest.NewRequest(http.MethodPost, "/v1/"+protocol, strings.NewReader(body))
}

func matrixHandler(handler *Handler, protocol string) http.HandlerFunc {
	switch protocol {
	case "messages":
		return handler.handleClaudeMessages
	case "chat/completions":
		return handler.handleOpenAIChat
	default:
		return handler.handleOpenAIResponses
	}
}

func TestSharedPipelineProtocolMatrix(t *testing.T) {
	for _, protocol := range []string{"messages", "chat/completions", "responses"} {
		for _, streaming := range []bool{false, true} {
			for _, scenario := range []struct {
				name, text, stop, policy string
				broken, failed           bool
			}{
				{name: "explicit", text: "<think>inspect</think>answer", stop: "END_TURN"},
				{name: "compatible", text: "answer"},
				{name: "strict", text: strings.Repeat("answer ", 32), policy: "strict", failed: true},
				{name: "reasoning_only", text: "<think>inspect</think>", failed: true},
				{name: "whitespace", text: "  ", failed: true},
				{name: "open_thinking", text: "<think>unfinished", failed: true},
				{name: "limit", text: "<think>unfinished", stop: "MAX_TOKENS"},
				{name: "context_limit", text: "<think>unfinished", stop: "CONTEXT_WINDOW_EXCEEDED"},
				{name: "broken_frame", text: strings.Repeat("answer ", 32), broken: true, failed: true},
				{name: "unknown_stop", text: "answer", stop: "SOMETHING_NEW", failed: true},
				{name: "literal_tags", text: "Example: <thinking>literal</thinking>", stop: "end_turn"},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", protocol, streaming, scenario.name), func(t *testing.T) {
					t.Setenv("KIRO_STREAM_EOF_POLICY", scenario.policy)
					var hits atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
						hits.Add(1)
						writer.Write(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": scenario.text}))
						if scenario.stop != "" {
							writer.Write(awsEventStreamFrame(t, "metadataEvent", map[string]interface{}{"stopReason": scenario.stop}))
						}
						if scenario.broken {
							writer.Write([]byte{0, 0, 0})
						}
					}))
					defer server.Close()
					handler := setupIntegrityPathTest(t, server)
					recorder := httptest.NewRecorder()
					matrixHandler(handler, protocol)(recorder, matrixRequest(protocol, streaming))
					body := recorder.Body.String()
					failed := strings.Contains(body, `"error":`)
					if failed != scenario.failed {
						t.Fatalf("failed=%t expected=%t: %s", failed, scenario.failed, body)
					}
					if scenario.failed && streaming {
						for _, terminal := range []string{"event: message_stop\n", "event: response.completed\n", "event: response.incomplete\n", "data: [DONE]"} {
							if strings.Contains(body, terminal) {
								t.Fatalf("failure incorrectly completed: %s", body)
							}
						}
					}
					if !scenario.failed && hits.Load() != 1 {
						t.Fatalf("successful or compatible result replayed: %d", hits.Load())
					}
					if streaming && (scenario.name == "broken_frame" || scenario.name == "strict") && hits.Load() != 1 {
						t.Fatalf("visible answer replayed: %d", hits.Load())
					}
					if scenario.name == "compatible" {
						marker := `"stop_reason":"max_tokens"`
						if protocol == "chat/completions" {
							marker = `"finish_reason":"length"`
						} else if protocol == "responses" {
							marker = `"status":"incomplete"`
						}
						if !strings.Contains(body, marker) {
							t.Fatalf("missing incomplete marker: %s", body)
						}
					}
					if scenario.name == "literal_tags" {
						encoded, _ := json.Marshal(scenario.text)
						if !strings.Contains(body, string(encoded)) {
							t.Fatalf("literal tags changed: %s", body)
						}
					}
				})
			}
		}
	}
}

func TestResponsesOutputKeepsStableIDsAndThinking(t *testing.T) {
	var events []map[string]interface{}
	output := newResponsesStreamOutput(func(_ string, value interface{}) {
		encoded, _ := json.Marshal(value)
		var event map[string]interface{}
		json.Unmarshal(encoded, &event)
		events = append(events, event)
	})
	output.text("inspect", true)
	output.text("before tool", false)
	output.tool(KiroToolUse{ToolUseID: "call_1", Name: "lookup", Input: map[string]interface{}{"query": "hello"}})
	output.text("after tool", false)
	output.finish("incomplete")
	if len(output.items) != 4 || output.items[0].Summary[0].Text != "inspect" || output.items[3].Status != "incomplete" {
		t.Fatalf("items=%+v", output.items)
	}
	seen := make(map[string]bool)
	for _, item := range output.items {
		if seen[item.ID] {
			t.Fatal("duplicate item ID")
		}
		seen[item.ID] = true
	}
	for _, event := range events {
		index := int(event["output_index"].(float64))
		if item, ok := event["item"].(map[string]interface{}); ok && item["id"] != output.items[index].ID {
			t.Fatalf("changed item ID: %v", event)
		}
		if id, ok := event["item_id"].(string); ok && id != output.items[index].ID {
			t.Fatalf("wrong delta ID: %v", event)
		}
	}
}
