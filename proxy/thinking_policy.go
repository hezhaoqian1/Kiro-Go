package proxy

import (
	"fmt"
	"strings"
)

type thinkingCapability struct {
	Mode    string   `json:"mode"`
	Efforts []string `json:"efforts,omitempty"`
}

func modelThinkingCapability(model string) thinkingCapability {
	model, _ = ParseModelAndThinking(model, "-thinking")
	switch strings.ToLower(model) {
	case "claude-sonnet-5", "claude-opus-5", "claude-opus-4.8", "claude-opus-4.7":
		return thinkingCapability{Mode: "adaptive", Efforts: []string{"low", "medium", "high", "xhigh", "max"}}
	case "claude-sonnet-4.6", "claude-opus-4.6":
		return thinkingCapability{Mode: "adaptive", Efforts: []string{"low", "medium", "high", "max"}}
	case "claude-sonnet-4", "claude-sonnet-4.5", "claude-opus-4", "claude-opus-4.1", "claude-opus-4.5", "claude-haiku-4.5":
		return thinkingCapability{Mode: "enabled"}
	default:
		return thinkingCapability{Mode: "unverified"}
	}
}

func modelSupportsThinking(model string) bool {
	return modelThinkingCapability(model).Mode != "unverified"
}

type ClaudeOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

func canonicalStopReason(reason string) (string, error) {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(reason)))
	switch normalized {
	case "":
		return "", nil
	case "endturn", "stop":
		return "end_turn", nil
	case "maxtokens", "maxoutputtokens", "length":
		return "max_tokens", nil
	case "tooluse":
		return "tool_use", nil
	case "stopsequence":
		return "stop_sequence", nil
	case "pauseturn":
		return "pause_turn", nil
	case "modelcontextwindowexceeded", "contextwindowexceeded":
		return "model_context_window_exceeded", nil
	case "refusal", "contentfilter", "contentfiltered", "guardrailintervened":
		return "refusal", nil
	default:
		return "", fmt.Errorf("unrecognized upstream stop reason %q", reason)
	}
}

func validateThinkingModel(model string, enabled bool, thinking *ClaudeThinkingConfig, effort string) string {
	capability := modelThinkingCapability(model)
	if enabled && capability.Mode == "unverified" {
		return fmt.Sprintf("thinking is not verified for model %s; use the base model without thinking", model)
	}
	if thinking != nil && strings.EqualFold(thinking.Type, "adaptive") && capability.Mode != "adaptive" {
		return fmt.Sprintf("adaptive thinking is not supported for model %s", model)
	}
	if effort != "" {
		for _, supported := range capability.Efforts {
			if effort == supported {
				return ""
			}
		}
		return fmt.Sprintf("unsupported reasoning effort %q for model %s", effort, model)
	}
	return ""
}

func claudeEffort(req *ClaudeRequest) string {
	if req.OutputConfig == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(req.OutputConfig.Effort))
}

func buildThinkingPrompt(model string, thinking *ClaudeThinkingConfig, maxTokens int, effort string) string {
	mode := modelThinkingCapability(model).Mode
	if thinking != nil && strings.TrimSpace(thinking.Type) != "" {
		mode = strings.ToLower(strings.TrimSpace(thinking.Type))
	}
	if mode == "adaptive" {
		if effort == "" {
			effort = "medium"
		}
		return fmt.Sprintf("<thinking_mode>adaptive</thinking_mode>\n<thinking_effort>%s</thinking_effort>", effort)
	}
	budget := 8192
	if thinking != nil && thinking.BudgetTokens > 0 {
		budget = thinking.BudgetTokens
	} else if maxTokens > 0 {
		budget = min(budget, max(1, maxTokens/2))
	}
	if maxTokens > 0 && budget >= maxTokens {
		budget = max(1, maxTokens-1)
	}
	return fmt.Sprintf("<thinking_mode>enabled</thinking_mode>\n<max_thinking_length>%d</max_thinking_length>", budget)
}

func applyThinkingPolicy(payload *KiroPayload, model string, enabled bool, effort string) {
	payload.ThinkingEnabled = enabled
	if len(modelThinkingCapability(model).Efforts) == 0 {
		return
	}
	if effort == "" && enabled {
		effort = "medium"
	}
	if effort != "" {
		payload.AdditionalModelRequestFields = map[string]interface{}{"output_config": map[string]string{"effort": effort}}
	}
}
