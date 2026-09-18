package proxy

import (
	"context"
	"io"
	"sync"
	"time"
)

type streamGuard struct {
	Context  context.Context
	cancel   context.CancelCauseFunc
	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time
	phase    string
	idle     time.Duration
	closed   bool
}

func newStreamGuard(ctx context.Context, thinking bool, options streamOptions) *streamGuard {
	child, cancel := context.WithCancelCause(ctx)
	first := options.FirstEvent
	if thinking {
		first = options.ThinkingFirstEvent
	}
	guard := &streamGuard{Context: child, cancel: cancel, phase: "first_event", idle: options.Idle, deadline: time.Now().Add(first)}
	guard.mu.Lock()
	guard.timer = time.AfterFunc(first, guard.expire)
	guard.mu.Unlock()
	return guard
}

func (guard *streamGuard) expire() {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.closed {
		return
	}
	if remaining := time.Until(guard.deadline); remaining > 0 {
		guard.timer.Reset(remaining)
		return
	}
	guard.cancel(&streamTimeoutError{Phase: guard.phase})
}

func (guard *streamGuard) Activity() {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.closed || guard.Context.Err() != nil {
		return
	}
	guard.phase = "idle"
	guard.deadline = time.Now().Add(guard.idle)
	guard.timer.Reset(guard.idle)
}

func (guard *streamGuard) Close() {
	guard.mu.Lock()
	guard.closed = true
	guard.timer.Stop()
	guard.mu.Unlock()
	guard.cancel(context.Canceled)
}

func (guard *streamGuard) Result(err error) error {
	if cause := context.Cause(guard.Context); cause != nil {
		return cause
	}
	return err
}

type guardedReader struct {
	io.Reader
	guard *streamGuard
}

func (reader guardedReader) Read(buffer []byte) (int, error) {
	count, err := reader.Reader.Read(buffer)
	if count > 0 {
		reader.guard.mu.Lock()
		started := reader.guard.phase == "idle"
		reader.guard.mu.Unlock()
		if started {
			reader.guard.Activity()
		}
	}
	return count, err
}
