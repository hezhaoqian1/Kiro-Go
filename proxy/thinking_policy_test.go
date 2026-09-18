package proxy

import (
	"encoding/json"
	"kiro-go/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThinkingPolicySerializesOnlySupportedFields(t *testing.T) {
	for _, scenario := range []struct {
		model, effort, mode string
		explicit            *ClaudeThinkingConfig
		wantField           bool
	}{
		{"claude-sonnet-5", "xhigh", "adaptive", nil, true},
		{"claude-opus-4.6", "max", "adaptive", nil, true},
		{"claude-sonnet-4.5", "", "enabled", nil, false},
		{"claude-opus-4.5", "", "enabled", &ClaudeThinkingConfig{Type: "enabled", BudgetTokens: 2048}, false},
	} {
		t.Run(scenario.model, func(t *testing.T) {
			request := &ClaudeRequest{Model: scenario.model, MaxTokens: 4096, Thinking: scenario.explicit, OutputConfig: &ClaudeOutputConfig{Effort: scenario.effort}, Messages: []ClaudeMessage{{Role: "user", Content: "hello"}}}
			payload := ClaudeToKiro(request, true)
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "additionalModelRequestFields") != scenario.wantField {
				t.Fatalf("payload=%s", encoded)
			}
			if !strings.Contains(payload.ConversationState.History[0].UserInputMessage.Content, ">"+scenario.mode+"</thinking_mode>") {
				t.Fatalf("wrong mode: %s", encoded)
			}
			if !payload.ThinkingEnabled {
				t.Fatal("missing normalization policy")
			}
			if strings.Contains(string(encoded), "ThinkingEnabled") {
				t.Fatal("internal policy leaked")
			}
		})
	}
}

func TestThinkingCapabilitiesRejectUnknownCombinations(t *testing.T) {
	for _, scenario := range []struct {
		model, effort string
		config        *ClaudeThinkingConfig
	}{
		{"claude-sonnet-99", "", nil},
		{"claude-sonnet-4.5", "high", nil},
		{"claude-sonnet-4.6", "xhigh", nil},
		{"claude-opus-4.5", "", &ClaudeThinkingConfig{Type: "adaptive"}},
	} {
		if validateThinkingModel(scenario.model, true, scenario.config, scenario.effort) == "" {
			t.Fatalf("accepted unsupported combination: %+v", scenario)
		}
	}
	models := buildAnthropicModelsResponse([]ModelInfo{{ModelId: "claude-sonnet-5"}, {ModelId: "unknown-model"}}, "-thinking")
	if len(models) != 3 {
		t.Fatalf("unverified thinking variant advertised: %#v", models)
	}
}

func TestThinkingValidationRunsBeforeUpstreamAcrossProtocols(t *testing.T) {
	for _, scenario := range []struct{ path, body string }{
		{"/v1/messages", `{"model":"claude-sonnet-4.5","max_tokens":4096,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/chat/completions", `{"model":"claude-sonnet-4.5","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/responses", `{"model":"claude-sonnet-4.5","reasoning":{"effort":"high"},"input":"hi","store":false}`},
	} {
		t.Run(scenario.path, func(t *testing.T) {
			if err := config.Init(t.TempDir() + "/config.json"); err != nil {
				t.Fatal(err)
			}
			handler := &Handler{}
			request := httptest.NewRequest(http.MethodPost, scenario.path, strings.NewReader(scenario.body))
			recorder := httptest.NewRecorder()
			switch scenario.path {
			case "/v1/messages":
				handler.handleClaudeMessages(recorder, request)
			case "/v1/chat/completions":
				handler.handleOpenAIChat(recorder, request)
			default:
				handler.handleOpenAIResponses(recorder, request)
			}
			if recorder.Code != 400 {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
