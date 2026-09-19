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
	if strings.Count(string(encoded), `"cachePoint":{"type":"default"}`) != 4 {
		t.Fatalf("expected explicit points plus the automatic assistant point, payload=%s", encoded)
	}
	if payload.ConversationState.History[0].UserInputMessage.CachePoint == nil {
		t.Fatal("expected system cache point on the priming user message")
	}
	if payload.ConversationState.History[2].UserInputMessage.CachePoint == nil {
		t.Fatal("expected history cache point nested on the user message")
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

func TestClaudeHistoricalCacheControlUsesNestedMessageField(t *testing.T) {
	for _, role := range []string{"user", "assistant"} {
		t.Run(role, func(t *testing.T) {
			block := map[string]interface{}{
				"type": "text", "text": "Retain this reference text.",
				"cache_control": map[string]interface{}{"type": "ephemeral"},
			}
			request := &ClaudeRequest{Model: "claude-sonnet-5", Messages: []ClaudeMessage{
				{Role: "user", Content: "First question"},
				{Role: "assistant", Content: "First answer"},
				{Role: role, Content: []interface{}{block}},
				{Role: "user", Content: "Reply OK"},
			}}
			payload := ClaudeToKiro(request, false)
			encodedHistory, err := json.Marshal(payload.ConversationState.History)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encodedHistory), `},{"cachePoint"`) {
				t.Fatalf("cache point must not be a standalone history entry: %s", encodedHistory)
			}
			checkpointCount := strings.Count(string(encodedHistory), `"cachePoint":{"type":"default"}`)
			if checkpointCount < 1 || checkpointCount > maxKiroCacheCheckpoints {
				t.Fatalf("unexpected nested cache point count %d: %s", checkpointCount, encodedHistory)
			}
			if !strings.Contains(string(encodedHistory), "Retain this reference text.") {
				t.Fatal("cached message content was lost")
			}
			marked := payload.ConversationState.History[2]
			if role == "user" && (marked.UserInputMessage == nil || marked.UserInputMessage.CachePoint == nil) {
				t.Fatalf("user cache point not nested on message: %#v", marked)
			}
			if role == "assistant" && (marked.AssistantResponseMessage == nil || marked.AssistantResponseMessage.CachePoint == nil) {
				t.Fatalf("assistant cache point not nested on message: %#v", marked)
			}
		})
	}
}

func TestClaudeCurrentMessageCacheControlUsesNestedField(t *testing.T) {
	request := &ClaudeRequest{Model: "claude-sonnet-5", Messages: []ClaudeMessage{{
		Role: "user",
		Content: []interface{}{map[string]interface{}{
			"type":          "text",
			"text":          "cache this current prefix",
			"cache_control": map[string]interface{}{"type": "ephemeral"},
		}},
	}}}

	payload := ClaudeToKiro(request, false)
	if payload.ConversationState.CurrentMessage.UserInputMessage.CachePoint == nil {
		t.Fatal("expected cache point nested on current user message")
	}
}

func TestClaudeIgnoresInvalidCacheControl(t *testing.T) {
	request := &ClaudeRequest{Model: "deepseek-3.2", Messages: []ClaudeMessage{{
		Role: "user",
		Content: []interface{}{map[string]interface{}{
			"type":          "text",
			"text":          "do not cache",
			"cache_control": map[string]interface{}{"type": "permanent"},
		}},
	}}}

	payload := ClaudeToKiro(request, false)
	if payload.ConversationState.CurrentMessage.UserInputMessage.CachePoint != nil {
		t.Fatal("unsupported cache_control type must not produce a Kiro cache point")
	}
}

func TestKiroAutomaticallyAddsStableCachePoints(t *testing.T) {
	request := &ClaudeRequest{
		Model:  "claude-sonnet-5",
		System: "Stable system instructions",
		Messages: []ClaudeMessage{
			{Role: "user", Content: "First turn"},
			{Role: "assistant", Content: "First answer"},
			{Role: "user", Content: "Second turn"},
		},
	}

	payload := ClaudeToKiro(request, false)
	if countKiroCachePoints(payload) != 2 {
		t.Fatalf("expected two automatic cache points, got %d", countKiroCachePoints(payload))
	}
	if payload.ConversationState.History[0].UserInputMessage.CachePoint == nil {
		t.Fatal("first history user message must be a stable cache checkpoint")
	}
	last := payload.ConversationState.History[len(payload.ConversationState.History)-1]
	if last.AssistantResponseMessage == nil || last.AssistantResponseMessage.CachePoint == nil {
		t.Fatal("last history assistant message must be a cache checkpoint")
	}
}

func TestKiroAutomaticCachePointsRespectRequestLimit(t *testing.T) {
	tools := make([]ClaudeTool, 4)
	for index := range tools {
		tools[index] = ClaudeTool{
			Name:         "tool_" + string(rune('a'+index)),
			Description:  "test tool",
			InputSchema:  map[string]interface{}{"type": "object"},
			CacheControl: map[string]interface{}{"type": "ephemeral"},
		}
	}
	request := &ClaudeRequest{
		Model:  "claude-sonnet-4.6",
		System: "Stable system instructions",
		Messages: []ClaudeMessage{
			{Role: "user", Content: "First turn"},
			{Role: "assistant", Content: "First answer"},
			{Role: "user", Content: "Second turn"},
		},
		Tools: tools,
	}

	payload := ClaudeToKiro(request, false)
	if countKiroCachePoints(payload) != maxKiroCacheCheckpoints {
		t.Fatalf("cache checkpoint count exceeded limit: %d", countKiroCachePoints(payload))
	}
	if payload.ConversationState.History[0].UserInputMessage.CachePoint != nil {
		t.Fatal("automatic cache point must not exceed a request already at the limit")
	}
}

func TestOpenAIRequestsReceiveAutomaticKiroCachePoints(t *testing.T) {
	request := &OpenAIRequest{Model: "claude-sonnet-5", Messages: []OpenAIMessage{
		{Role: "system", Content: "Stable system instructions"},
		{Role: "user", Content: "First turn"},
		{Role: "assistant", Content: "First answer"},
		{Role: "user", Content: "Second turn"},
	}}

	payload := OpenAIToKiro(request, false)
	if countKiroCachePoints(payload) != 2 {
		t.Fatalf("expected OpenAI conversion to add two cache points, got %d", countKiroCachePoints(payload))
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
