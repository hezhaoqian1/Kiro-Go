package proxy

type reportedTokenUsage struct {
	InputTokens    int
	OutputTokens   int
	InputReported  bool
	OutputReported bool
}

func (usage *reportedTokenUsage) update(event map[string]interface{}) {
	candidates := []map[string]interface{}{event}
	collectUsageMaps(event, &candidates)
	for _, candidate := range candidates {
		if value, ok := readTokenNumber(candidate, "outputTokens", "completionTokens", "totalOutputTokens", "output_tokens", "completion_tokens", "total_output_tokens"); ok {
			usage.OutputTokens, usage.OutputReported = value, true
		}
		cacheRead, _ := readTokenNumber(candidate, "cacheReadInputTokens", "cache_read_input_tokens")
		cacheWrite, _ := readTokenNumber(candidate, "cacheWriteInputTokens", "cache_write_input_tokens", "cacheCreationInputTokens", "cache_creation_input_tokens")
		if uncached, ok := readTokenNumber(candidate, "uncachedInputTokens", "uncached_input_tokens"); ok {
			usage.InputTokens, usage.InputReported = uncached+cacheRead+cacheWrite, true
		} else if value, ok := readTokenNumber(candidate, "inputTokens", "promptTokens", "totalInputTokens", "input_tokens", "prompt_tokens", "total_input_tokens"); ok {
			usage.InputTokens, usage.InputReported = value, true
		} else if total, ok := readTokenNumber(candidate, "totalTokens", "total_tokens"); ok && usage.OutputReported && total >= usage.OutputTokens {
			usage.InputTokens, usage.InputReported = total-usage.OutputTokens, true
		}
	}
}

func (usage reportedTokenUsage) resolve(input, output, contextEstimate, inputEstimate, outputEstimate int) (int, int) {
	if usage.InputReported {
		input = usage.InputTokens
	} else if input <= 0 {
		input = inputEstimate
		if contextEstimate > 0 {
			input = contextEstimate
		}
	}
	if usage.OutputReported {
		output = usage.OutputTokens
	} else if output <= 0 {
		output = outputEstimate
	}
	return input, output
}
