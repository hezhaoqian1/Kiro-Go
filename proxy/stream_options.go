package proxy

import (
	"context"
	"fmt"
	"kiro-go/logger"
	"os"
	"strconv"
	"strings"
	"time"
)

type streamOptions struct {
	FirstEvent         time.Duration
	ThinkingFirstEvent time.Duration
	Idle               time.Duration
	Total              time.Duration
	Heartbeat          time.Duration
	WriteTimeout       time.Duration
	StrictEOF          bool
}

func streamDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		seconds, numberErr := strconv.Atoi(value)
		if numberErr == nil && seconds > 0 && seconds <= 86400 {
			duration, err = time.Duration(seconds)*time.Second, nil
		}
	}
	if err != nil || duration <= 0 || duration > 24*time.Hour {
		logger.Warnf("[StreamConfig] invalid %s; using %s", name, fallback)
		return fallback
	}
	return duration
}

func getStreamOptions() streamOptions {
	policy := strings.ToLower(strings.TrimSpace(os.Getenv("KIRO_STREAM_EOF_POLICY")))
	if policy != "" && policy != "compatible" && policy != "strict" {
		logger.Warnf("[StreamConfig] invalid KIRO_STREAM_EOF_POLICY; using compatible")
	}
	return streamOptions{
		FirstEvent:         streamDuration("KIRO_FIRST_EVENT_TIMEOUT", 60*time.Second),
		ThinkingFirstEvent: streamDuration("KIRO_THINKING_FIRST_EVENT_TIMEOUT", 180*time.Second),
		Idle:               streamDuration("KIRO_STREAM_IDLE_TIMEOUT", 90*time.Second),
		Total:              streamDuration("KIRO_STREAM_TOTAL_TIMEOUT", 5*time.Minute),
		Heartbeat:          streamDuration("KIRO_SSE_PING_INTERVAL", 15*time.Second),
		WriteTimeout:       streamDuration("KIRO_SSE_WRITE_TIMEOUT", 15*time.Second),
		StrictEOF:          policy == "strict",
	}
}

type streamTimeoutError struct{ Phase string }

func (err *streamTimeoutError) Error() string { return fmt.Sprintf("upstream %s timeout", err.Phase) }
func (err *streamTimeoutError) Unwrap() error { return context.DeadlineExceeded }

func streamLifetime(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, getStreamOptions().Total, &streamTimeoutError{Phase: "total"})
}
