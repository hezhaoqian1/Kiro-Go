package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamingDeadlinesEndToEnd(t *testing.T) {
	for _, protocol := range []string{"messages", "chat/completions", "responses"} {
		for _, phase := range []string{"first_event", "total"} {
			t.Run(protocol+"/"+phase, func(t *testing.T) {
				t.Setenv("KIRO_FIRST_EVENT_TIMEOUT", "100ms")
				t.Setenv("KIRO_THINKING_FIRST_EVENT_TIMEOUT", "100ms")
				t.Setenv("KIRO_STREAM_TOTAL_TIMEOUT", "300ms")
				t.Setenv("KIRO_STREAM_IDLE_TIMEOUT", "1s")
				t.Setenv("KIRO_SSE_PING_INTERVAL", "10ms")
				canceled := make(chan struct{}, 4)
				release := make(chan struct{})
				frame := awsEventStreamFrame(t, "metadataEvent", map[string]interface{}{"usage": 1})
				if phase == "total" {
					frame = awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": strings.Repeat("answer ", 32)})
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					io.Copy(io.Discard, request.Body)
					request.Body.Close()
					ticker := time.NewTicker(5 * time.Millisecond)
					defer ticker.Stop()
					defer func() { canceled <- struct{}{} }()
					for {
						select {
						case <-ticker.C:
							writer.Write(frame)
							writer.(http.Flusher).Flush()
						case <-request.Context().Done():
							return
						case <-release:
							return
						}
					}
				}))
				defer upstream.Close()
				defer close(release)
				handler := setupIntegrityPathTest(t, upstream)
				gateway := httptest.NewServer(matrixHandler(handler, protocol))
				defer gateway.Close()
				request := matrixRequest(protocol, true)
				request.URL.Scheme, request.URL.Host, request.RequestURI = "http", strings.TrimPrefix(gateway.URL, "http://"), ""
				client := &http.Client{Timeout: 3 * time.Second}
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				data, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				body := string(data)
				if !strings.Contains(body, phase+" timeout") || !strings.Contains(body, `"error":`) {
					t.Fatalf("missing timeout error: %s", body)
				}
				ping := ": keep-alive"
				if protocol == "messages" {
					ping = "event: ping"
				}
				if strings.Count(body, ping) < 2 {
					t.Fatalf("missing heartbeat during generation: %s", body)
				}
				for _, terminal := range []string{"event: message_stop\n", "event: response.completed\n", "data: [DONE]"} {
					if strings.Contains(body, terminal) {
						t.Fatalf("timeout falsely completed: %s", body)
					}
				}
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("deadline did not cancel upstream")
				}
			})
		}
	}
}
