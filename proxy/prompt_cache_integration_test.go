package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeCacheControlConvertsToKiroCachePoints(t *testing.T) {
	request := &ClaudeRequest{
		Model: "claude-sonnet-5",
		System: []interface{}{
			map[string]interface{}{
				"type":          "text",
				"text":          strings.Repeat("system ", 20),
				"cache_control": map[string]interface{}{"type": "ephemeral"},
			},
		},
		Messages: []ClaudeMessage{
			{Role: "user", Content: []interface{}{map[string]interface{}{
				"type":          "text",
				"text":          strings.Repeat("history ", 20),
				"cache_control": map[string]interface{}{"type": "ephemeral"},
			}}},
			{Role: "assistant", Content: "ack"},
			{Role: "user", Content: "latest"},
		},
		Tools: []ClaudeTool{{
			Name:         "lookup",
			Description:  "lookup data",
			InputSchema:  map[string]interface{}{"type": "object"},
			CacheControl: map[string]interface{}{"type": "ephemeral"},
		}},
	}

	payload := ClaudeToKiro(request, false)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(encoded), `"cachePoint":{"type":"default"}`) != 2 {
		t.Fatalf("expected message and tool cache points, payload=%s", encoded)
	}
	if len(payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools) != 2 {
		t.Fatalf("expected tool plus cache point, got %#v", payload.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools)
	}
	var wire struct {
		ConversationState struct {
			CurrentMessage struct {
				UserInputMessage struct {
					UserInputMessageContext struct {
						Tools []map[string]json.RawMessage `json:"tools"`
					} `json:"userInputMessageContext"`
				} `json:"userInputMessage"`
			} `json:"currentMessage"`
		} `json:"conversationState"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	tools := wire.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	if len(tools) != 2 {
		t.Fatalf("expected two serialized tools, got %d", len(tools))
	}
	if _, ok := tools[1]["toolSpecification"]; ok {
		t.Fatalf("cache point must not contain an empty tool specification: %s", encoded)
	}
	if string(tools[1]["cachePoint"]) != `{"type":"default"}` {
		t.Fatalf("unexpected serialized cache point: %s", tools[1]["cachePoint"])
	}
}

func TestClaudeCacheUsageOnlyReportsUpstreamSignals(t *testing.T) {
	if usage := promptCacheUsageFromEvent(map[string]interface{}{
		"usage": map[string]interface{}{
			"cacheReadInputTokens":     120,
			"cacheCreationInputTokens": 30,
			"cacheCreation": map[string]interface{}{
				"ephemeral_5m_input_tokens": 30,
			},
		},
	}); usage.CacheReadInputTokens != 120 || usage.CacheCreationInputTokens != 30 || usage.CacheCreation5mInputTokens != 30 {
		t.Fatalf("wrong upstream cache usage: %+v", usage)
	}
	if hasPromptCacheUsage(promptCacheUsage{}) {
		t.Fatal("empty upstream usage must not be reported as a cache hit")
	}
	direct := promptCacheUsageFromEvent(map[string]interface{}{
		"cache_read_input_tokens":     90,
		"cache_creation_input_tokens": 20,
		"ephemeral_5m_input_tokens":   20,
	})
	if direct.CacheReadInputTokens != 90 || direct.CacheCreationInputTokens != 20 || direct.CacheCreation5mInputTokens != 20 {
		t.Fatalf("wrong direct upstream cache usage: %+v", direct)
	}
}
