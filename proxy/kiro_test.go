package proxy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kiro-go/config"
)

func TestNormalizeChunkBasicProgression(t *testing.T) {
	prev := ""

	if got := normalizeChunk("abc", &prev); got != "abc" {
		t.Fatalf("expected first chunk to pass through, got %q", got)
	}
	if got := normalizeChunk("abcde", &prev); got != "de" {
		t.Fatalf("expected appended delta, got %q", got)
	}
}

func TestNormalizeChunkPrefixRewindDoesNotReplay(t *testing.T) {
	prev := ""

	_ = normalizeChunk("abcde", &prev)
	if got := normalizeChunk("abc", &prev); got != "" {
		t.Fatalf("expected rewind chunk to be ignored, got %q", got)
	}
	if prev != "abcde" {
		t.Fatalf("expected previous snapshot to remain longest version, got %q", prev)
	}
	if got := normalizeChunk("abcdef", &prev); got != "f" {
		t.Fatalf("expected only unseen suffix after rewind, got %q", got)
	}
}

func TestNormalizeChunkOverlapDelta(t *testing.T) {
	prev := "hello world"

	if got := normalizeChunk("world!!!", &prev); got != "!!!" {
		t.Fatalf("expected overlap suffix delta, got %q", got)
	}
}

func TestSupportsAmazonQModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4.6", true},
		{"claude-haiku-4.5", true},
		{"claude-opus-4.7", false},
		{"claude-opus-4-7", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := supportsAmazonQModel(tc.model); got != tc.want {
			t.Fatalf("supportsAmazonQModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestFilterEndpointsForModelSkipsAmazonQForUnsupportedModels(t *testing.T) {
	endpoints := filterEndpointsForModel(kiroEndpoints, "claude-opus-4.7", "auto")
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].Name != "CodeWhisperer" {
		t.Fatalf("expected CodeWhisperer only, got %s", endpoints[0].Name)
	}
}

func TestFilterEndpointsForModelKeepsAmazonQWhenExplicitlyPreferred(t *testing.T) {
	endpoints := filterEndpointsForModel(getSortedEndpoints("amazonq"), "claude-opus-4.7", "amazonq")
	if len(endpoints) != 2 {
		t.Fatalf("expected both endpoints to remain when amazonq is explicit, got %d", len(endpoints))
	}
	if endpoints[0].Name != "AmazonQ" {
		t.Fatalf("expected AmazonQ to stay first, got %s", endpoints[0].Name)
	}
}

func TestCallKiroAPITimesOutWithoutFirstEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	originalEndpoints := kiroEndpoints
	originalTimeout := kiroFirstEventTimeout
	kiroEndpoints = []kiroEndpoint{{
		URL:       server.URL,
		Origin:    "AI_EDITOR",
		AmzTarget: "Test.GenerateAssistantResponse",
		Name:      "TestEndpoint",
	}, {
		URL:       server.URL,
		Origin:    "CLI",
		AmzTarget: "Test.SendMessage",
		Name:      "TestEndpoint2",
	}}
	kiroFirstEventTimeout = 50 * time.Millisecond
	defer func() {
		kiroEndpoints = originalEndpoints
		kiroFirstEventTimeout = originalTimeout
	}()

	payload := &KiroPayload{}
	payload.ConversationState.CurrentMessage.UserInputMessage.ModelID = "claude-opus-4.7"

	err := CallKiroAPI(&config.Account{
		ID:          "acct-1",
		AccessToken: "token",
		MachineId:   "machine",
	}, payload, &KiroStreamCallback{})
	if !errors.Is(err, ErrKiroBootstrapTimeout) {
		t.Fatalf("expected bootstrap timeout, got %v", err)
	}
	if !strings.Contains(err.Error(), "bootstrap timeout") {
		t.Fatalf("expected timeout error message, got %v", err)
	}
}
