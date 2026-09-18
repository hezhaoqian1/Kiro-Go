package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type sseTransport struct {
	writer       http.ResponseWriter
	mu           sync.Mutex
	stop         chan struct{}
	done         chan struct{}
	once         sync.Once
	cancel       context.CancelCauseFunc
	protocol     string
	terminal     bool
	writeErr     error
	writeTimeout time.Duration
}

func startSSE(ctx context.Context, writer http.ResponseWriter, protocol string) (context.Context, *sseTransport) {
	child, cancel := context.WithCancelCause(ctx)
	transport := &sseTransport{writer: writer, stop: make(chan struct{}), done: make(chan struct{}), cancel: cancel, protocol: protocol}
	transport.writeTimeout = getStreamOptions().WriteTimeout
	writer.Header().Set("X-Accel-Buffering", "no")
	transport.ping()
	go func() {
		defer close(transport.done)
		ticker := time.NewTicker(getStreamOptions().Heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				transport.ping()
			case <-transport.stop:
				return
			case <-child.Done():
				return
			}
		}
	}()
	return child, transport
}

func (transport *sseTransport) Header() http.Header    { return transport.writer.Header() }
func (transport *sseTransport) WriteHeader(status int) {}

func terminalSSE(data []byte) bool {
	text := string(data)
	for _, event := range []string{"message_stop", "error", "response.completed", "response.incomplete", "response.failed"} {
		if strings.HasPrefix(text, "event: "+event+"\n") {
			return true
		}
	}
	if strings.HasPrefix(text, "data: [DONE]") {
		return true
	}
	if strings.HasPrefix(text, "data: ") {
		var envelope map[string]json.RawMessage
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(text, "data: "))), &envelope) == nil && envelope["error"] != nil {
			return true
		}
	}
	return false
}

func (transport *sseTransport) Write(data []byte) (int, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.writeErr != nil {
		return 0, transport.writeErr
	}
	if transport.terminal {
		return 0, io.ErrClosedPipe
	}
	if terminalSSE(data) {
		transport.terminal = true
	}
	_ = http.NewResponseController(transport.writer).SetWriteDeadline(time.Now().Add(transport.writeTimeout))
	count, err := transport.writer.Write(data)
	if err == nil && count != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		transport.writeErr = err
		transport.cancel(err)
	}
	return count, err
}

func (transport *sseTransport) Flush() {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if err := http.NewResponseController(transport.writer).Flush(); err != nil {
		transport.writeErr = err
		transport.cancel(err)
	}
}

func (transport *sseTransport) ping() {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.terminal || transport.writeErr != nil {
		return
	}
	frame := ": keep-alive\n\n"
	if transport.protocol == "claude" {
		frame = "event: ping\ndata: {\"type\":\"ping\"}\n\n"
	}
	_ = http.NewResponseController(transport.writer).SetWriteDeadline(time.Now().Add(transport.writeTimeout))
	_, err := io.WriteString(transport.writer, frame)
	if err == nil {
		err = http.NewResponseController(transport.writer).Flush()
	}
	if err != nil {
		transport.writeErr = err
		transport.cancel(err)
	}
}

func (transport *sseTransport) Close() {
	transport.once.Do(func() { close(transport.stop) })
	<-transport.done
	_ = http.NewResponseController(transport.writer).SetWriteDeadline(time.Time{})
	transport.cancel(context.Canceled)
}

func (transport *sseTransport) Error(kind, message string) {
	if transport.protocol == "responses" {
		data, _ := json.Marshal(map[string]interface{}{"type": "response.failed", "response": map[string]interface{}{"status": "failed", "error": map[string]string{"type": kind, "message": message}}})
		fmt.Fprintf(transport, "event: response.failed\ndata: %s\n\n", data)
		transport.Flush()
		return
	}
	data, _ := json.Marshal(map[string]interface{}{"type": "error", "error": map[string]string{"type": kind, "message": message}})
	if transport.protocol == "claude" {
		fmt.Fprintf(transport, "event: error\ndata: %s\n\n", data)
	} else {
		fmt.Fprintf(transport, "data: %s\n\n", data)
	}
	transport.Flush()
}

func reportStreamCancellation(writer http.ResponseWriter, ctx context.Context) {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return
	}
	message := context.Cause(ctx).Error()
	if transport, ok := writer.(*sseTransport); ok {
		transport.Error("timeout_error", message)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusGatewayTimeout)
	json.NewEncoder(writer).Encode(map[string]interface{}{"type": "error", "error": map[string]string{"type": "timeout_error", "message": message}})
}
