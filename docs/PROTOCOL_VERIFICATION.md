# Deployment protocol verification — 2026-09-19

Verified implementation: `889f46d` (includes `6e0cc4c` diagnostics and `ffc667d` native usage corrections).

The deployed OAuth account now selects `preferredEndpoint=cli`, `endpointFallback=false`, and an empty Thinking suffix. These are deployment settings; repository defaults for other installations remain unchanged.

## Root causes and corrections

- IDE responses on the tested account contain assistant, contextUsage and metering events, but omit stopReason. The same account on CLI runtime returns metadata with real end_turn/tool_use reasons. Selecting CLI resolves the observed false max_tokens/incomplete results without synthesizing a successful stop reason.
- Native input/output counters were overwritten by context-percentage and text estimates. Native counts now take precedence, including explicit zero; absent counters still use estimates.
- invalidStateEvent was ignored. It now fails parsing and cannot be converted to a successful clean EOF.
- HTTP 200 JSON error envelopes were parsed as binary frames. Known permanent errors now surface immediately; known transient envelopes can retry before output.
- Connection failures and 429/5xx previously skipped same-endpoint recovery. They now receive at most one same-endpoint retry with cancellation and the overall deadline retained. Short Retry-After is honored; longer waits are returned as failures without retrying early. Output is never replayed.
- Standalone history cachePoint entries were rejected by the tested upstream. That conversion was removed in `23b2b29`; ordinary history content is retained.
- The unused local cache fingerprint/TTL simulator and its request-path allocation were removed. Cache fields are based on upstream counters; unreported counters and explicitly reported zero are distinct.

## Live regression results

| Request | Duration | Result |
| --- | ---: | --- |
| Claude base model, default Thinking, stream | 3.60 s | end_turn + message_stop |
| Claude explicit thinking.disabled, buffered | 2.47 s | end_turn |
| Claude legacy -thinking alias, stream | 3.38 s | end_turn + message_stop |
| Chat Completions, stream | 2.41 s | finish_reason=stop + DONE |
| Responses, stream | 2.26 s | response.completed |
| Claude tool call, stream | 2.71 s | tool_use + message_stop |
| Claude tool result continuation, stream | 5.67 s | end_turn + message_stop |
| Claude long response, high effort, stream | 54.65 s | end_turn + message_stop |
| Opus Thinking, stream | 4.90 s | thinking deltas + end_turn + message_stop |

These checks validate the observed calls, not a guarantee against future upstream outages. Sonnet can finish without exposing thinking text; Opus did expose it in this run. Absence of visible reasoning alone is not a stalled connection.

## Cache evidence and limits

Compared IDE and CLI with the same deployed account and repeated tool prefixes, including a larger prefix spanning three tools. CLI returned metadata and an end reason, but neither route exposed tokenUsage/cache counters in these successful requests. The parser recognizes the `tokenUsage.uncachedInputTokens`, `cacheReadInputTokens`, `cacheWriteInputTokens`, `outputTokens`, and `totalTokens` layout used by the compared projects; synthetic wire tests verify conversion into Claude streamed and buffered usage.

The larger-prefix CLI pair reported context usage of approximately 1.5467% and metering usage of 0.0639138 then 0.0339498 credits. That reduction does not establish a cache token count, cache TTL, or the effect of cachePoint without a separate controlled billing study. Diagnostics correctly report `cache_status=unreported` and `token_usage_reported=false`. No cache hit is fabricated from repeated text, elapsed time, or credits. Anthropic cache token statistics/discounts cannot currently be promised for this account.

Reference source inspected: `d-kuro/kirocc` (Apache-2.0; CLI request headers, disjoint usage buckets), `dat-lequoc/dsh-kiro` (metadata and missing-usage behavior), `petehsu/KiroProxy` (`9a91b9b`), `justlovemaki/AIClient-2-API` (`b12e25e`), and `jwadow/kiro-gateway` (`a5292ca`). Some map request cache directives or emit zero cache fields; neither is proof of real cache hits. Implementation here is independent; third-party source was not copied.

## Intermittent availability

A Railway 502 was observed while deployment `889f46d` was in progress. The health endpoint returned 200 after rollout and all listed calls succeeded afterward. This establishes a deployment-time interruption in this run, not the cause of earlier user-reported network problems. HTTP validation failures, stream failures and actual network timeouts must be diagnosed separately.

## Repeatable diagnostics

Use the authenticated admin `POST /admin/api/accounts/{id}/diagnose` endpoint documented in the README. It reports bounded, allowlisted metadata without prompt/answer text, tool arguments or credentials. `endpoint=cli` is a request-scoped comparison and does not alter global routing. Test calls consume upstream quota. Read success together with stop_reason, cache_status, attempts and event counts.
