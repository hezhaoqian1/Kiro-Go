package proxy

import (
	"context"
	"encoding/json"
	"kiro-go/config"
	"net/http"
	"time"
)

type diagnosticEndpointKey struct{}

type streamDiagnosticAttempt struct {
	Endpoint string `json:"endpoint"`
	Status   int    `json:"status"`
	AtMS     int64  `json:"at_ms"`
}

type streamDiagnostic struct {
	Success            bool                      `json:"success"`
	Error              string                    `json:"error,omitempty"`
	Model              string                    `json:"model"`
	Thinking           bool                      `json:"thinking"`
	Attempts           []streamDiagnosticAttempt `json:"attempts"`
	Events             map[string]int            `json:"events"`
	TailEvents         []string                  `json:"tail_events"`
	TokenUsageReported bool                      `json:"token_usage_reported"`
	CacheStatus        string                    `json:"cache_status"`
	NumericFields      map[string]float64        `json:"numeric_fields"`
	FirstEventMS       *int64                    `json:"first_event_ms,omitempty"`
	DurationMS         int64                     `json:"duration_ms"`
	TextBytes          int                       `json:"text_bytes"`
	ThinkingBytes      int                       `json:"thinking_bytes"`
	ToolCalls          int                       `json:"tool_calls"`
	StopReason         string                    `json:"stop_reason,omitempty"`
	InputTokens        int                       `json:"input_tokens"`
	OutputTokens       int                       `json:"output_tokens"`
	CacheUsage         map[string]interface{}    `json:"cache_usage"`
	Started            time.Time                 `json:"-"`
}

func (diagnostic *streamDiagnostic) observe(eventType string, event map[string]interface{}) {
	if diagnostic.FirstEventMS == nil {
		elapsed := time.Since(diagnostic.Started).Milliseconds()
		diagnostic.FirstEventMS = &elapsed
	}
	switch eventType {
	case "assistantResponseEvent", "reasoningContentEvent", "toolUseEvent", "metadataEvent", "meteringEvent", "contextUsageEvent", "messageMetadataEvent", "invalidStateEvent", "followupPromptEvent":
	default:
		eventType = "other"
	}
	diagnostic.Events[eventType]++
	diagnostic.TailEvents = append(diagnostic.TailEvents, eventType)
	if len(diagnostic.TailEvents) > 16 {
		diagnostic.TailEvents = diagnostic.TailEvents[1:]
	}
	if eventType != "metadataEvent" && eventType != "meteringEvent" && eventType != "contextUsageEvent" {
		return
	}
	diagnostic.observeNumbers(eventType, event, 0)
}

func (diagnostic *streamDiagnostic) observeNumbers(path string, fields map[string]interface{}, depth int) {
	if depth > 4 || len(diagnostic.NumericFields) >= 128 {
		return
	}
	for _, key := range []string{"inputTokens", "outputTokens", "totalTokens", "uncachedInputTokens", "cacheReadInputTokens", "cacheWriteInputTokens", "cacheCreationInputTokens", "input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "cached_tokens", "ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens", "contextUsagePercentage", "usage"} {
		if value, ok := fields[key].(float64); ok {
			diagnostic.NumericFields[path+"."+key] = value
		}
	}
	for _, key := range []string{"usage", "tokenUsage", "token_usage", "metadata", "cacheCreation", "cache_creation", "input_tokens_details", "prompt_tokens_details"} {
		if nested, ok := fields[key].(map[string]interface{}); ok {
			diagnostic.observeNumbers(path+"."+key, nested, depth+1)
		}
	}
}

func (h *Handler) apiDiagnoseAccount(w http.ResponseWriter, r *http.Request, id string) {
	var input struct {
		Request  ClaudeRequest `json:"request"`
		Repeat   int           `json:"repeat"`
		Endpoint string        `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&input); err != nil {
		h.sendClaudeError(w, http.StatusBadRequest, "invalid_request_error", "Expected a diagnostic request up to 256 KiB")
		return
	}
	if input.Repeat == 0 {
		input.Repeat = 1
	}
	if input.Endpoint != "" && input.Endpoint != "cli" {
		h.sendClaudeError(w, http.StatusBadRequest, "invalid_request_error", "endpoint must be omitted or cli")
		return
	}
	if input.Repeat < 1 || input.Repeat > 2 {
		h.sendClaudeError(w, http.StatusBadRequest, "invalid_request_error", "repeat must be 1 or 2")
		return
	}
	if input.Request.Model == "" {
		input.Request.Model = "claude-sonnet-4.5"
	}
	if input.Request.MaxTokens == 0 {
		input.Request.MaxTokens = 2048
	}
	if input.Request.MaxTokens < 1 || input.Request.MaxTokens > 8192 {
		h.sendClaudeError(w, http.StatusBadRequest, "invalid_request_error", "max_tokens must be between 1 and 8192")
		return
	}
	if len(input.Request.Messages) == 0 {
		input.Request.Messages = []ClaudeMessage{{Role: "user", Content: "Reply OK."}}
	}
	if message := validateClaudeRequestShape(&input.Request); message != "" {
		h.sendClaudeError(w, http.StatusBadRequest, "invalid_request_error", message)
		return
	}
	model, thinking := resolveClaudeThinkingMode(input.Request.Model, input.Request.Thinking, config.GetThinkingConfig().Suffix)
	input.Request.Model = model
	if message := validateThinkingModel(model, thinking, input.Request.Thinking, claudeEffort(&input.Request)); message != "" {
		h.sendClaudeError(w, http.StatusBadRequest, "invalid_request_error", message)
		return
	}
	var account *config.Account
	for _, candidate := range config.GetAccounts() {
		if candidate.ID == id {
			copy := candidate
			account = &copy
			break
		}
	}
	if account == nil {
		h.sendClaudeError(w, http.StatusNotFound, "not_found_error", "Account not found")
		return
	}
	ctx, cancel := streamLifetime(r.Context())
	defer cancel()
	if input.Endpoint == "cli" {
		ctx = context.WithValue(ctx, diagnosticEndpointKey{}, "cli")
	}
	if err := h.ensureValidToken(account); err != nil {
		h.sendClaudeError(w, http.StatusBadGateway, "api_error", "Token refresh failed")
		return
	}
	payload := ClaudeToKiro(&input.Request, thinking)
	results := make([]streamDiagnostic, 0, input.Repeat)
	for iteration := 0; iteration < input.Repeat; iteration++ {
		var cacheUsage promptCacheUsage
		diagnostic := streamDiagnostic{Model: model, Thinking: thinking, Events: make(map[string]int), NumericFields: make(map[string]float64), CacheUsage: make(map[string]interface{}), Started: time.Now()}
		callback := &KiroStreamCallback{
			OnTokenUsage: func(usage reportedTokenUsage) {
				diagnostic.TokenUsageReported = usage.InputReported || usage.OutputReported
			},
			OnEvent: diagnostic.observe,
			OnAttempt: func(endpoint string, status int) {
				diagnostic.Attempts = append(diagnostic.Attempts, streamDiagnosticAttempt{Endpoint: endpoint, Status: status, AtMS: time.Since(diagnostic.Started).Milliseconds()})
			},
			OnText: func(text string, isThinking bool) {
				if isThinking {
					diagnostic.ThinkingBytes += len(text)
				} else {
					diagnostic.TextBytes += len(text)
				}
			},
			OnToolUse:    func(KiroToolUse) { diagnostic.ToolCalls++ },
			OnStopReason: func(reason string) { diagnostic.StopReason = reason },
			OnComplete:   func(input, output int) { diagnostic.InputTokens = input; diagnostic.OutputTokens = output },
			OnCacheUsage: func(usage promptCacheUsage) {
				cacheUsage = usage
			},
		}
		err := CallKiroAPIContext(ctx, account, payload, callback)
		diagnostic.CacheUsage = buildClaudeUsageMap(diagnostic.InputTokens, diagnostic.OutputTokens, cacheUsage, hasPromptCacheUsage(cacheUsage))
		diagnostic.CacheStatus = "unreported"
		if hasPromptCacheUsage(cacheUsage) {
			diagnostic.CacheStatus = "reported"
		}
		if cacheUsage.CacheReadInputTokens > 0 {
			diagnostic.CacheStatus = "hit"
		}
		diagnostic.DurationMS = time.Since(diagnostic.Started).Milliseconds()
		diagnostic.Success = err == nil
		if err != nil {
			diagnostic.Error = "upstream_request_failed"
		}
		results = append(results, diagnostic)
		if err != nil || ctx.Err() != nil {
			break
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
}
