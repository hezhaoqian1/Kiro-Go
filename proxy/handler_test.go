package proxy

import (
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"kiro-go/config"
	"kiro-go/pool"
)

func TestThinkingSourceReasoningFirst(t *testing.T) {
	var source thinkingStreamSource

	if !allowReasoningSource(&source) {
		t.Fatalf("expected reasoning source to be accepted first")
	}
	if source != thinkingSourceReasoningEvent {
		t.Fatalf("expected source to be reasoning, got %v", source)
	}
	if allowTagSource(&source) {
		t.Fatalf("expected tag source to be rejected after reasoning source selected")
	}
}

func TestThinkingSourceTagFirst(t *testing.T) {
	var source thinkingStreamSource

	if !allowTagSource(&source) {
		t.Fatalf("expected tag source to be accepted first")
	}
	if source != thinkingSourceTagBlock {
		t.Fatalf("expected source to be tag, got %v", source)
	}
	if allowReasoningSource(&source) {
		t.Fatalf("expected reasoning source to be rejected after tag source selected")
	}
}

func TestThinkingSourceSameSourceRemainsAllowed(t *testing.T) {
	var source thinkingStreamSource

	if !allowTagSource(&source) {
		t.Fatalf("expected initial tag source selection to succeed")
	}
	if !allowTagSource(&source) {
		t.Fatalf("expected repeated tag source selection to stay allowed")
	}

	source = thinkingSourceUnknown
	if !allowReasoningSource(&source) {
		t.Fatalf("expected initial reasoning source selection to succeed")
	}
	if !allowReasoningSource(&source) {
		t.Fatalf("expected repeated reasoning source selection to stay allowed")
	}
}

func TestValidateOpenAIRequestShapeRejectsAssistantPrefill(t *testing.T) {
	req := &OpenAIRequest{
		Messages: []OpenAIMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "prefill"},
		},
	}

	if msg := validateOpenAIRequestShape(req); msg == "" {
		t.Fatalf("expected assistant-prefill final message to be rejected")
	}
}

func TestValidateOpenAIRequestShapeAllowsToolResultFinalTurn(t *testing.T) {
	req := &OpenAIRequest{
		Messages: []OpenAIMessage{
			{Role: "user", Content: "find weather"},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: "get_weather", Arguments: "{}"},
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
		},
	}

	if msg := validateOpenAIRequestShape(req); msg != "" {
		t.Fatalf("expected tool-result final turn to be valid, got %q", msg)
	}
}

func TestValidateClaudeRequestShapeAllowsAssistantPrefill(t *testing.T) {
	req := &ClaudeRequest{
		Messages: []ClaudeMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "prefill"},
		},
	}

	if msg := validateClaudeRequestShape(req); msg != "" {
		t.Fatalf("expected assistant-prefill final message to be allowed, got %q", msg)
	}
}

func TestMergeUniqueModelsPreservesUnionAcrossAccounts(t *testing.T) {
	base := []ModelInfo{
		{ModelId: "claude-sonnet-4.5", InputTypes: []string{"TEXT"}},
	}
	incoming := []ModelInfo{
		{ModelId: "claude-sonnet-4.5", InputTypes: []string{"image"}},
		{ModelId: "claude-opus-4-7", InputTypes: []string{"text"}},
	}

	merged := mergeUniqueModels(base, incoming)
	if len(merged) != 2 {
		t.Fatalf("expected 2 unique models, got %d", len(merged))
	}
	if !modelSupportsImage(merged[0].InputTypes) {
		t.Fatalf("expected merged input types to preserve image capability, got %#v", merged[0].InputTypes)
	}
	if merged[1].ModelId != "claude-opus-4-7" {
		t.Fatalf("expected second model to be claude-opus-4-7, got %q", merged[1].ModelId)
	}
}

func TestBuildAnthropicModelsResponseGeneratesThinkingVariants(t *testing.T) {
	models := buildAnthropicModelsResponse([]ModelInfo{{
		ModelId:    "claude-sonnet-4.5",
		InputTypes: []string{"text", "image"},
	}}, "-thinking")

	if len(models) != 2 {
		t.Fatalf("expected base model and thinking variant, got %d", len(models))
	}
	if models[0]["id"] != "claude-sonnet-4.5" {
		t.Fatalf("unexpected base model id: %#v", models[0]["id"])
	}
	if models[1]["id"] != "claude-sonnet-4.5-thinking" {
		t.Fatalf("unexpected thinking model id: %#v", models[1]["id"])
	}
	if supportsImage, ok := models[0]["supports_image"].(bool); !ok || !supportsImage {
		t.Fatalf("expected image capability to be preserved, got %#v", models[0]["supports_image"])
	}
}

func TestModelIDsPreservesNonEmptyEntries(t *testing.T) {
	ids := modelIDs([]ModelInfo{
		{ModelId: "claude-opus-4.7"},
		{ModelId: ""},
		{ModelId: "claude-sonnet-4.5"},
	})

	if len(ids) != 2 {
		t.Fatalf("expected 2 model ids, got %d", len(ids))
	}
	if ids[0] != "claude-opus-4.7" || ids[1] != "claude-sonnet-4.5" {
		t.Fatalf("unexpected model ids: %#v", ids)
	}
}

func TestHandleClaudeStreamClosesMessageBeforeError(t *testing.T) {
	originalCall := callKiroAPI
	callKiroAPI = func(account *config.Account, payload *KiroPayload, callback *KiroStreamCallback) error {
		return errors.New("boom")
	}
	defer func() { callKiroAPI = originalCall }()

	cfgFile, err := os.CreateTemp("", "kiro-config-*.json")
	if err != nil {
		t.Fatalf("create temp config: %v", err)
	}
	if _, err := cfgFile.WriteString(`{"password":"test","port":8080,"host":"127.0.0.1","requireApiKey":false,"accounts":[]}`); err != nil {
		t.Fatalf("seed temp config: %v", err)
	}
	cfgFile.Close()
	defer os.Remove(cfgFile.Name())
	if err := config.Init(cfgFile.Name()); err != nil {
		t.Fatalf("init config: %v", err)
	}

	h := &Handler{
		pool:        pool.GetPool(),
		promptCache: newPromptCacheTracker(defaultPromptCacheTTL),
	}
	rec := httptest.NewRecorder()

	h.handleClaudeStream(
		rec,
		&config.Account{ID: "acct-1"},
		&KiroPayload{},
		"claude-opus-4-7",
		false,
		12,
		promptCacheUsage{},
		nil,
	)

	body := rec.Body.String()
	for _, needle := range []string{
		"event: message_start",
		"event: message_delta",
		"\"stop_reason\":\"error\"",
		"event: message_stop",
		"event: error",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected %q in stream body, got %q", needle, body)
		}
	}

	if strings.Index(body, "event: message_stop") > strings.Index(body, "event: error") {
		t.Fatalf("expected message_stop before error, got %q", body)
	}
}
