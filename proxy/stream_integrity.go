package proxy

import (
	"context"
	"kiro-go/config"
	"kiro-go/logger"
	"strings"
)

func runKiroWithIntegrityRetry(
	ctx context.Context,
	account *config.Account,
	payload *KiroPayload,
	callback *KiroStreamCallback,
	measure func() (contentChars, toolCount int, stopReason string, sawReasoning bool),
	reset func(),
	canRetry func() bool,
) error {
	label := accountEmailForLog(account)
	retryable := func() bool {
		if canRetry == nil {
			return true
		}
		return canRetry()
	}

	for attempt := 0; attempt <= maxSameAccountStreamRetries; attempt++ {
		if attempt > 0 && reset != nil {
			reset()
		}

		tracked := KiroStreamCallback{}
		if callback != nil {
			tracked = *callback
		}
		answerSeen := false
		tracked.OnText = func(text string, thinking bool) {
			if !thinking && strings.TrimSpace(text) != "" {
				answerSeen = true
			}
			if callback != nil && callback.OnText != nil {
				callback.OnText(text, thinking)
			}
		}
		err := CallKiroAPIContext(ctx, account, payload, &tracked)
		if err != nil {
			return err
		}

		contentChars, toolCount, stopReason, sawReasoning := measure()
		if strings.TrimSpace(stopReason) == "" && toolCount == 0 && answerSeen && !getStreamOptions().StrictEOF {
			logger.Warnf("[StreamCompletion] completion=missing_stop_reason policy=compatible action=max_tokens account=%s", label)
			if callback != nil && callback.OnStopReason != nil {
				callback.OnStopReason("max_tokens")
			}
			return nil
		}
		integrityErr := classifyStreamIntegrity(contentChars, toolCount, stopReason, sawReasoning)
		if integrityErr == nil {
			return nil
		}

		// A canceled client is not an integrity failure: the turn is over and
		// reissuing it would only burn upstream quota.
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}

		if retryable() && attempt < maxSameAccountStreamRetries {
			logger.Warnf("[StreamIntegrity] %v on %s; retrying same account (%d/%d)",
				integrityErr, label, attempt+1, maxSameAccountStreamRetries)
			continue
		}

		if !retryable() {
			// Bytes already reached the client; reissuing would duplicate output.
			// Return the integrity error so callers emit an error event instead of
			// finishing with a forged end_turn/tool_use success.
			logger.Warnf("[StreamIntegrity] %v after client flush; signaling error (no retry)", integrityErr)
			return integrityErr
		}

		logger.Warnf("[StreamIntegrity] giving up after retries: %v", integrityErr)
		return integrityErr
	}

	// Unreachable: every branch inside the loop returns or continues, and the
	// final iteration cannot continue.
	return errUpstreamTruncatedResponse
}
