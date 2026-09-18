package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"kiro-go/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamGuardFirstEventAndIdleTimeouts(t *testing.T) {
	for _, phase := range []string{"first_event", "idle"} {
		t.Run(phase, func(t *testing.T) {
			if err := config.Init(t.TempDir() + "/config.json"); err != nil {
				t.Fatal(err)
			}
			t.Setenv("KIRO_FIRST_EVENT_TIMEOUT", "80ms")
			t.Setenv("KIRO_STREAM_IDLE_TIMEOUT", "80ms")
			if err := config.UpdatePreferredEndpoint("kiro"); err != nil {
				t.Fatal(err)
			}
			if err := config.UpdateEndpointFallback(false); err != nil {
				t.Fatal(err)
			}
			upstreamCanceled := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				io.Copy(io.Discard, request.Body)
				request.Body.Close()
				defer close(upstreamCanceled)
				if phase == "idle" {
					writer.Write(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "hello"}))
					writer.(http.Flusher).Flush()
				}
				select {
				case <-request.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			t.Cleanup(swapKiroEndpointsForTest(t, server))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := CallKiroAPIContext(ctx, integrityTestAccount(), integrityTestPayload(), &KiroStreamCallback{OnText: func(string, bool) {}})
			var timeout *streamTimeoutError
			if !errors.As(err, &timeout) || timeout.Phase != phase {
				t.Fatalf("error=%v", err)
			}
			select {
			case <-upstreamCanceled:
			case <-time.After(time.Second):
				t.Fatal("upstream request not canceled")
			}
		})
	}
}

func TestStreamGuardThinkingHasSeparateFirstEventBudget(t *testing.T) {
	options := streamOptions{FirstEvent: time.Millisecond, ThinkingFirstEvent: 200 * time.Millisecond, Idle: time.Second}
	guard := newStreamGuard(context.Background(), true, options)
	defer guard.Close()
	if time.Until(guard.deadline) < 100*time.Millisecond {
		t.Fatal("thinking inherited short first-event budget")
	}
	guard.Activity()
	if guard.phase != "idle" {
		t.Fatal("first event did not transition to idle budget")
	}
}

func TestStreamTotalDeadlineCannotBeExtendedByActivity(t *testing.T) {
	t.Setenv("KIRO_STREAM_TOTAL_TIMEOUT", "100ms")
	ctx, cancel := streamLifetime(context.Background())
	defer cancel()
	guard := newStreamGuard(ctx, false, streamOptions{FirstEvent: time.Second, Idle: time.Second})
	defer guard.Close()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			guard.Activity()
		case <-guard.Context.Done():
			var timeout *streamTimeoutError
			if !errors.As(context.Cause(guard.Context), &timeout) || timeout.Phase != "total" {
				t.Fatalf("cause=%v", context.Cause(guard.Context))
			}
			return
		}
	}
}

func TestSSEHeartbeatsAreSerializedAndStopAtTerminalEvent(t *testing.T) {
	t.Setenv("KIRO_SSE_PING_INTERVAL", "5ms")
	for _, protocol := range []string{"claude", "openai", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			recorder.Header().Set("Content-Type", "text/event-stream")
			_, stream := startSSE(context.Background(), recorder, protocol)
			var workers sync.WaitGroup
			for index := 0; index < 8; index++ {
				workers.Add(1)
				go func(value int) {
					defer workers.Done()
					fmt.Fprintf(stream, "data: {\"index\":%d}\n\n", value)
					stream.Flush()
				}(index)
			}
			workers.Wait()
			time.Sleep(15 * time.Millisecond)
			terminal := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			if protocol == "openai" {
				terminal = "data: [DONE]\n\n"
			}
			if protocol == "responses" {
				terminal = "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
			}
			io.WriteString(stream, terminal)
			stream.Flush()
			time.Sleep(15 * time.Millisecond)
			stream.Close()
			body := recorder.Body.String()
			if !strings.HasSuffix(body, terminal) {
				t.Fatalf("heartbeat or output after terminal: %s", body)
			}
			if strings.Count(body, `"index":`) != 8 {
				t.Fatalf("lost frames: %s", body)
			}
			if !strings.Contains(body, "ping") && !strings.Contains(body, "keep-alive") {
				t.Fatal("no heartbeat")
			}
			if recorder.Header().Get("X-Accel-Buffering") != "no" {
				t.Fatal("proxy buffering enabled")
			}
		})
	}
}

type failingSSEWriter struct{ header http.Header }

func (writer *failingSSEWriter) Header() http.Header       { return writer.header }
func (writer *failingSSEWriter) WriteHeader(int)           {}
func (writer *failingSSEWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (writer *failingSSEWriter) Flush()                    {}

func TestSSEWriteFailureCancelsUpstream(t *testing.T) {
	ctx, stream := startSSE(context.Background(), &failingSSEWriter{header: make(http.Header)}, "claude")
	defer stream.Close()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("client failure did not cancel request")
	}
	if !errors.Is(context.Cause(ctx), io.ErrClosedPipe) {
		t.Fatalf("cause=%v", context.Cause(ctx))
	}
}

func TestStreamOptionsValidateDurations(t *testing.T) {
	for _, invalid := range []string{"0", "-1s", "invalid", "9999999999999999999", "25h"} {
		t.Setenv("KIRO_FIRST_EVENT_TIMEOUT", invalid)
		if getStreamOptions().FirstEvent != time.Minute {
			t.Fatalf("invalid duration accepted: %s", invalid)
		}
	}
	t.Setenv("KIRO_FIRST_EVENT_TIMEOUT", "42")
	if getStreamOptions().FirstEvent != 42*time.Second {
		t.Fatal("integer seconds not accepted")
	}
}
