package proxy

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUpstreamTransientHTTPFailuresRetryBeforeOutput(t *testing.T) {
	for _, status := range []int{429, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls, waits := 0, 0
			installKiroStreamTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"message":"temporary"}`)), Header: http.Header{"Retry-After": []string{"1"}}}, nil
				}
				return kiroStreamTestResponse(bytes.NewReader(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "ok"}))), nil
			}))
			installKiroRetryWait(t, func(delay time.Duration) {
				waits++
				if delay < time.Second {
					t.Fatalf("ignored Retry-After: %v", delay)
				}
			})
			err := CallKiroAPI(newKiroRetryTestAPIKeyAccount(""), newKiroRetryTestPayload(), &KiroStreamCallback{})
			if err != nil || calls != 2 || waits != 1 {
				t.Fatalf("err=%v calls=%d waits=%d", err, calls, waits)
			}
		})
	}
}

func TestUpstreamTransientTransportFailureRetriesSameEndpoint(t *testing.T) {
	calls, waits := 0, 0
	installKiroStreamTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("connection reset")
		}
		return kiroStreamTestResponse(bytes.NewReader(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "ok"}))), nil
	}))
	installKiroRetryWait(t, func(time.Duration) { waits++ })
	err := CallKiroAPI(newKiroRetryTestAPIKeyAccount(""), newKiroRetryTestPayload(), &KiroStreamCallback{})
	if err != nil || calls != 2 || waits != 1 {
		t.Fatalf("err=%v calls=%d waits=%d", err, calls, waits)
	}
}

func TestUpstreamJSONErrorEnvelopeDoesNotReachFrameParser(t *testing.T) {
	installKiroStreamTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/x-amz-json-1.0"}}, Body: io.NopCloser(strings.NewReader(`{"__type":"AccessDeniedException","message":"no access"}`))}, nil
	}))
	installKiroRetryWait(t, func(time.Duration) { t.Fatal("permanent error retried") })
	err := CallKiroAPI(newKiroRetryTestAPIKeyAccount(""), newKiroRetryTestPayload(), &KiroStreamCallback{})
	if err == nil || strings.Contains(err.Error(), "frame length") || !strings.Contains(err.Error(), "AccessDeniedException") {
		t.Fatalf("misclassified envelope: %v", err)
	}
}

func TestUpstreamLongRetryAfterDoesNotRetryEarly(t *testing.T) {
	calls := 0
	installKiroStreamTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"60"}}, Body: io.NopCloser(strings.NewReader(`{"message":"throttled"}`))}, nil
	}))
	installKiroRetryWait(t, func(time.Duration) { t.Fatal("long Retry-After should return instead of retrying early") })
	err := CallKiroAPI(newKiroRetryTestAPIKeyAccount(""), newKiroRetryTestPayload(), &KiroStreamCallback{})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestUpstreamHTTP200TransientJSONEnvelopeRecovers(t *testing.T) {
	calls, waits := 0, 0
	installKiroStreamTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"__type":"com.amazon#ThrottlingException"}`))}, nil
		}
		return kiroStreamTestResponse(bytes.NewReader(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "ok"}))), nil
	}))
	installKiroRetryWait(t, func(time.Duration) { waits++ })
	err := CallKiroAPI(newKiroRetryTestAPIKeyAccount(""), newKiroRetryTestPayload(), &KiroStreamCallback{})
	if err != nil || calls != 2 || waits != 1 {
		t.Fatalf("err=%v calls=%d waits=%d", err, calls, waits)
	}
}
