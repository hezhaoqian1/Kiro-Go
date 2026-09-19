package proxy

type promptCacheUsage struct {
	Reported                   bool
	BreakdownReported          bool
	CacheCreationInputTokens   int
	CacheReadInputTokens       int
	CacheCreation5mInputTokens int
	CacheCreation1hInputTokens int
}

func billedClaudeInputTokens(inputTokens int, usage promptCacheUsage) int {
	return maxInt(inputTokens-usage.CacheCreationInputTokens-usage.CacheReadInputTokens, 0)
}

func hasPromptCacheUsage(usage promptCacheUsage) bool {
	return usage.Reported || usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0 ||
		usage.CacheCreation5mInputTokens > 0 || usage.CacheCreation1hInputTokens > 0
}

func buildClaudeUsageMap(inputTokens, outputTokens int, usage promptCacheUsage, includeCache bool) map[string]interface{} {
	result := map[string]interface{}{
		"input_tokens":  billedClaudeInputTokens(inputTokens, usage),
		"output_tokens": outputTokens,
	}
	if !includeCache {
		return result
	}
	result["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
	result["cache_read_input_tokens"] = usage.CacheReadInputTokens
	if hasPromptCacheBreakdown(usage) {
		result["cache_creation"] = map[string]int{
			"ephemeral_5m_input_tokens": usage.CacheCreation5mInputTokens,
			"ephemeral_1h_input_tokens": usage.CacheCreation1hInputTokens,
		}
	}
	return result
}

func hasPromptCacheBreakdown(usage promptCacheUsage) bool {
	return usage.BreakdownReported || usage.CacheCreation5mInputTokens > 0 || usage.CacheCreation1hInputTokens > 0
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
