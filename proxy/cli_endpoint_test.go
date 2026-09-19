package proxy

import (
	"bytes"
	"encoding/json"
	"kiro-go/config"
	"net/http"
	"strings"
	"testing"
)

func TestOAuthCLIEndpointUsesRuntimeProtocol(t *testing.T) {
	if err := config.Init(t.TempDir() + "/config.json"); err != nil {
		t.Fatal(err)
	}
	config.UpdatePreferredEndpoint("cli")
	config.UpdateEndpointFallback(false)
	account := newKiroRetryTestOAuthAccount()
	account.Region = "us-east-1"
	account.ProfileArn = "arn:aws:codewhisperer:eu-west-1:123456789012:profile/test"
	installKiroStreamTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "runtime.eu-west-1.kiro.dev" || request.Header.Get("Content-Type") != "application/x-amz-json-1.0" || request.Header.Get("TokenType") != "" {
			t.Fatalf("wrong OAuth CLI protocol: %s %+v", request.URL, request.Header)
		}
		if !strings.Contains(request.Header.Get("User-Agent"), "AmazonQ-For-CLI") || request.Header.Get("X-Amz-Target") == "" || request.Header.Get("x-amzn-codewhisperer-optout") != "false" {
			t.Fatal("missing CLI headers")
		}
		var payload KiroPayload
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ProfileArn != account.ProfileArn || payload.ConversationState.CurrentMessage.UserInputMessage.Origin != "KIRO_CLI" {
			t.Fatal("wrong CLI payload")
		}
		return kiroStreamTestResponse(bytes.NewReader(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "ok"}))), nil
	}))
	if err := CallKiroAPI(account, newKiroRetryTestPayload(), &KiroStreamCallback{}); err != nil {
		t.Fatal(err)
	}
	config.UpdateEndpointFallback(true)
	endpoints := endpointsForAccount(account)
	if len(endpoints) != 4 || endpoints[0].Origin != "KIRO_CLI" {
		t.Fatalf("wrong fallback: %+v", endpoints)
	}
	if endpoints := endpointsForAccount(newKiroRetryTestAPIKeyAccount("")); len(endpoints) != 1 {
		t.Fatalf("API key must never fallback to IDE: %+v", endpoints)
	}
}
