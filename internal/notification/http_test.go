package notification

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jig/internal/workflow"
)

func newTestBinding(url string, typ DestinationType, bearer string) Binding {
	return NewBinding("alias", typ, []workflow.NotificationEvent{workflow.RunFailed}, url, bearer)
}

func newTestNotification() Notification {
	return Notification{
		ID:        "n-1",
		Event:     workflow.RunFailed,
		Timestamp: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
		Workflow:  "wf",
		RunID:     "r-1",
	}
}

func newTestPayload() OutboundPayload {
	return BuildOutboundPayload(newTestNotification())
}

func TestHTTPSenderWebhookSuccess(t *testing.T) {
	var captured atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.Store(map[string]string{
			"content_type":  r.Header.Get("Content-Type"),
			"authorization": r.Header.Get("Authorization"),
			"body":          string(body),
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := &HTTPSender{Client: srv.Client()}
	res := sender.Send(context.Background(), newTestBinding(srv.URL, Webhook, "token123"), newTestPayload())
	if !res.Success() {
		t.Fatalf("expected delivered, got %+v", res)
	}
	got := captured.Load().(map[string]string)
	if got["content_type"] != "application/json" {
		t.Fatalf("content-type=%q", got["content_type"])
	}
	if got["authorization"] != "Bearer token123" {
		t.Fatalf("authorization=%q", got["authorization"])
	}
	if !strings.Contains(got["body"], `"schema_version":1`) {
		t.Fatalf("body missing schema_version: %s", got["body"])
	}
}

func TestHTTPSenderNoRedirect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://elsewhere.example.invalid/")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer srv.Close()
	sender := &HTTPSender{Client: srv.Client()}
	res := sender.Send(context.Background(), newTestBinding(srv.URL, Webhook, ""), newTestPayload())
	if res.Success() {
		t.Fatalf("redirect should not be treated as success: %+v", res)
	}
	if res.Retry {
		t.Fatalf("permanent redirect should not retry: %+v", res)
	}
}

func TestHTTPSenderRetryClassification(t *testing.T) {
	tests := []struct {
		status int
		retry  bool
	}{
		{408, true},
		{429, true},
		{500, true},
		{503, true},
		{400, false},
		{401, false},
		{404, false},
	}
	for _, tc := range tests {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		func() {
			defer srv.Close()
			sender := &HTTPSender{Client: srv.Client()}
			res := sender.Send(context.Background(), newTestBinding(srv.URL, Webhook, ""), newTestPayload())
			if res.Success() {
				t.Fatalf("status %d: expected failure", tc.status)
			}
			if res.Retry != tc.retry {
				t.Fatalf("status %d: retry=%t want=%t", tc.status, res.Retry, tc.retry)
			}
		}()
	}
}

func TestHTTPSenderRetryAfter(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	sender := &HTTPSender{Client: srv.Client()}
	res := sender.Send(context.Background(), newTestBinding(srv.URL, Webhook, ""), newTestPayload())
	if !res.Retry || res.RetryAfter != 2*time.Second {
		t.Fatalf("expected retry with 2s delay, got %+v", res)
	}
}

func TestSlackSenderSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	sender := &HTTPSender{Client: srv.Client(), Slack: true}
	res := sender.Send(context.Background(), newTestBinding(srv.URL, Slack, ""), newTestPayload())
	if !res.Success() {
		t.Fatalf("slack: expected delivered, got %+v", res)
	}
}

func TestSlackSenderRejectsHTTP200NonOK(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid_channel"))
	}))
	defer srv.Close()
	sender := &HTTPSender{Client: srv.Client(), Slack: true}
	res := sender.Send(context.Background(), newTestBinding(srv.URL, Slack, ""), newTestPayload())
	if res.Success() {
		t.Fatalf("slack: expected failure for 200 body, got %+v", res)
	}
}

func TestHTTPSenderRejectsInvalidURL(t *testing.T) {
	sender := &HTTPSender{Client: NewHTTPClient()}
	res := sender.Send(context.Background(), newTestBinding("http://not.https/", Webhook, ""), newTestPayload())
	if res.Reason != "url_invalid" {
		t.Fatalf("expected url_invalid, got %+v", res)
	}
}
