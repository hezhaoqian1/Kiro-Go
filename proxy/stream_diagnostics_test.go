package proxy

import (
	"encoding/json"
	"kiro-go/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamDiagnosticOnlyCapturesSafeMetadata(t *testing.T) {
	diagnostic := streamDiagnostic{Started: time.Now(), Events: make(map[string]int), NumericFields: make(map[string]float64)}
	diagnostic.observe("assistantResponseEvent", map[string]interface{}{"content": "private-answer", "usage": float64(999)})
	diagnostic.observe("toolUseEvent", map[string]interface{}{"input": map[string]interface{}{"usage": float64(999)}, "name": "private-tool"})
	diagnostic.observe("private-event", map[string]interface{}{"content": "private-answer"})
	diagnostic.observe("metadataEvent", map[string]interface{}{"tokenUsage": map[string]interface{}{"cacheReadInputTokens": float64(123)}, "accessToken": "secret", "content": "private-answer"})
	encoded, err := json.Marshal(diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private", "secret", "accessToken", "999"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("leaked content: %s", encoded)
		}
	}
	if diagnostic.NumericFields["metadataEvent.tokenUsage.cacheReadInputTokens"] != 123 || len(diagnostic.NumericFields) != 1 {
		t.Fatalf("wrong fields: %+v", diagnostic.NumericFields)
	}
}

func TestStreamDiagnosticAdminRepeatsAndReportsNativeUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "private-answer"}))
		w.Write(awsEventStreamFrame(t, "metadataEvent", map[string]interface{}{"stopReason": "end_turn", "tokenUsage": map[string]interface{}{"uncachedInputTokens": 10, "cacheReadInputTokens": 120, "cacheWriteInputTokens": 30, "outputTokens": 2}}))
	}))
	defer server.Close()
	handler := setupIntegrityPathTest(t, server)
	body := `{"repeat":2,"request":{"model":"claude-sonnet-5","thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"private-prompt"}]}}`
	request := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/test-account/diagnose", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/admin/api/accounts/test-account/diagnose", strings.NewReader(body))
	request.Header.Set("X-Admin-Password", config.GetPassword())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var decoded struct {
		Results []streamDiagnostic `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 2 {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
	for _, result := range decoded.Results {
		if !result.Success || result.CacheUsage["input_tokens"] != float64(10) || result.CacheUsage["cache_read_input_tokens"] != float64(120) || result.Events["metadataEvent"] != 1 || len(result.Attempts) != 1 {
			t.Fatalf("wrong diagnostic: %+v", result)
		}
	}
	if strings.Contains(response.Body.String(), "private-") || strings.Contains(response.Body.String(), "token-test") {
		t.Fatalf("private diagnostic: %s", response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
}

func TestStreamDiagnosticRejectsUnboundedRequests(t *testing.T) {
	handler := &Handler{}
	for _, body := range []string{`{"repeat":3}`, `{"request":{"max_tokens":99999}}`, strings.Repeat(" ", 256*1024) + `{}`} {
		response := httptest.NewRecorder()
		handler.apiDiagnoseAccount(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), "missing")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status %d", response.Code)
		}
	}
}
