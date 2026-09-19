package proxy

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func TestReportedUsageZeroIsNotAnEstimate(t *testing.T) {
	usage := reportedTokenUsage{}
	usage.update(map[string]interface{}{"tokenUsage": map[string]interface{}{"uncachedInputTokens": 0, "cacheReadInputTokens": 50, "cacheWriteInputTokens": 20, "outputTokens": 0}})
	input, output := usage.resolve(0, 0, 99000, 400, 50)
	if input != 70 || output != 0 {
		t.Fatalf("zero overwritten: %d %d", input, output)
	}
	usage = reportedTokenUsage{}
	usage.update(map[string]interface{}{"tokenUsage": map[string]interface{}{"totalTokens": 107, "outputTokens": 7, "cacheReadInputTokens": 80}})
	if usage.InputTokens != 100 || !usage.InputReported {
		t.Fatalf("total reconstruction: %+v", usage)
	}
}

func TestCacheUsageAbsentAndReportedZeroRemainDistinct(t *testing.T) {
	for _, present := range []bool{false, true} {
		event := map[string]interface{}{}
		if present {
			event["tokenUsage"] = map[string]interface{}{"cacheReadInputTokens": 0, "cacheWriteInputTokens": 0}
		}
		usage := promptCacheUsageFromEvent(event)
		if hasPromptCacheUsage(usage) != present {
			t.Fatalf("presence lost: %+v", usage)
		}
		encoded, err := json.Marshal(ClaudeUsage{CacheReported: hasPromptCacheUsage(usage)})
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte("cache_read_input_tokens")) != present {
			t.Fatalf("wrong response: %s", encoded)
		}
		if bytes.Contains(encoded, []byte(`"cache_creation":`)) {
			t.Fatalf("TTL invented: %s", encoded)
		}
	}
}

func TestReadTokenNumberRejectsInvalidCounts(t *testing.T) {
	for _, value := range []interface{}{-1, -1.0, 0.5, "-2", "3.5", math.Inf(1), math.NaN(), float64(math.MaxInt)} {
		if number, ok := readTokenNumber(map[string]interface{}{"tokens": value}, "tokens"); ok {
			t.Fatalf("accepted %v as %d", value, number)
		}
	}
}

func TestToolInputCannotForgeUsage(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "ok", "usage": map[string]interface{}{"inputTokens": 999, "cacheReadInputTokens": 888}}))
	stream.Write(awsEventStreamFrame(t, "toolUseEvent", map[string]interface{}{"toolUseId": "test", "name": "lookup", "input": `{"usage":{"inputTokens":999,"cacheReadInputTokens":888}}`, "stop": true}))
	var usage reportedTokenUsage
	var cache promptCacheUsage
	err := parseEventStream(&stream, &KiroStreamCallback{OnTokenUsage: func(value reportedTokenUsage) { usage = value }, OnCacheUsage: func(value promptCacheUsage) { cache = value }})
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputReported || hasPromptCacheUsage(cache) {
		t.Fatalf("untrusted payload became usage: %+v %+v", usage, cache)
	}
}
