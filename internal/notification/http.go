package notification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	httpAttemptTimeout = 3 * time.Second
	responseReadCap    = 4 * 1024
)

// HTTPSender is the shared Slack/webhook adapter. Callers inject an
// http.Client so tests can point it at an httptest.NewTLSServer instance;
// production wiring sets one with redirect refusal, TLS verification, and
// bounded timeouts.
type HTTPSender struct {
	Client *http.Client
	Slack  bool
}

// NewHTTPClient returns the production HTTP client shared by Slack and
// generic senders. Redirects are refused so a compromised operator secret
// cannot be leaked to a redirected host.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: httpAttemptTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Send performs one attempt, honoring the caller's ctx deadline.
func (s *HTTPSender) Send(ctx context.Context, target Binding, payload OutboundPayload) SendResult {
	if strings.TrimSpace(target.url) == "" {
		return SendResult{Reason: "url_missing", Err: errors.New("binding url is empty")}
	}
	if !validHTTPS(target.url) {
		return SendResult{Reason: "url_invalid", Err: errors.New("binding url is not a valid https url")}
	}
	body, contentType := s.body(payload)
	if len(body) > MaxRequestBytes {
		return SendResult{Reason: "payload_too_large", Err: fmt.Errorf("request body exceeds %d bytes", MaxRequestBytes)}
	}
	client := s.Client
	if client == nil {
		client = NewHTTPClient()
	}
	// Every send refuses redirects regardless of the injected client's
	// default. A caller-supplied client may leave CheckRedirect unset (the
	// stdlib default follows up to 10 redirects); we override it so a
	// compromised secret cannot be leaked to a redirected host.
	clientCopy := *client
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client = &clientCopy

	attemptCtx, cancel := context.WithTimeout(ctx, httpAttemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, target.url, bytes.NewReader(body))
	if err != nil {
		return SendResult{Reason: "request_build_failed", Err: errors.New("request build failed")}
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "jig/notification")
	if !s.Slack && target.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+target.bearer)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return SendResult{Reason: "timeout", Retry: true, Err: errors.New("attempt timeout"), Duration: time.Since(start)}
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return SendResult{Reason: "cancelled", Err: errors.New("cancelled"), Duration: time.Since(start)}
		}
		if strings.Contains(strings.ToLower(err.Error()), "x509") ||
			strings.Contains(strings.ToLower(err.Error()), "certificate") ||
			strings.Contains(strings.ToLower(err.Error()), "tls") {
			return SendResult{Reason: "tls_error", Err: errors.New("tls error"), Duration: time.Since(start)}
		}
		if strings.Contains(err.Error(), "unsupported protocol scheme") {
			return SendResult{Reason: "url_invalid", Err: errors.New("url invalid"), Duration: time.Since(start)}
		}
		return SendResult{Reason: "transport_error", Retry: true, Err: errors.New("transport error"), Duration: time.Since(start)}
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, responseReadCap)
	preview, _ := io.ReadAll(limited)

	return s.classify(resp, preview, time.Since(start))
}

// classify implements the shared success/retry rules. Success requires HTTP
// 2xx for generic webhooks and Slack's documented body-level `ok` for Slack;
// retry only transient transport failures, HTTP 408/429, or HTTP 5xx.
func (s *HTTPSender) classify(resp *http.Response, body []byte, dur time.Duration) SendResult {
	status := resp.StatusCode
	if s.Slack {
		if status == http.StatusOK && strings.TrimSpace(string(body)) == "ok" {
			return SendResult{Reason: "delivered", Duration: dur}
		}
		return classifyStatus(status, resp, body, dur)
	}
	if status >= 200 && status < 300 {
		return SendResult{Reason: "delivered", Duration: dur}
	}
	return classifyStatus(status, resp, body, dur)
}

// body returns the request body and Content-Type. Slack uses the Block Kit
// text form; the generic sender uses the fixed JSON allowlist.
func (s *HTTPSender) body(payload OutboundPayload) ([]byte, string) {
	if s.Slack {
		type slackReq struct {
			Text string `json:"text"`
		}
		req := slackReq{Text: payload.SlackBody}
		out, _ := jsonMarshalNoHTMLEscape(req)
		return out, "application/json"
	}
	return payload.JSON, "application/json"
}

func classifyStatus(status int, resp *http.Response, body []byte, dur time.Duration) SendResult {
	_ = body
	switch {
	case status == http.StatusRequestTimeout || status == http.StatusTooManyRequests:
		retry := parseRetryAfter(resp.Header.Get("Retry-After"))
		return SendResult{Reason: reasonFromStatus(status), Retry: true, RetryAfter: retry, Duration: dur}
	case status >= 500 && status <= 599:
		retry := parseRetryAfter(resp.Header.Get("Retry-After"))
		return SendResult{Reason: reasonFromStatus(status), Retry: true, RetryAfter: retry, Duration: dur}
	case status >= 400 && status <= 499:
		return SendResult{Reason: reasonFromStatus(status), Duration: dur, Err: fmt.Errorf("http %d", status)}
	default:
		return SendResult{Reason: reasonFromStatus(status), Duration: dur, Err: fmt.Errorf("http %d", status)}
	}
}

func reasonFromStatus(status int) string {
	switch {
	case status == http.StatusRequestTimeout:
		return "http_408"
	case status == http.StatusTooManyRequests:
		return "http_429"
	case status >= 500 && status <= 599:
		return "http_5xx"
	case status >= 400 && status <= 499:
		return "http_4xx"
	default:
		return "http_error"
	}
}

// parseRetryAfter accepts either a delta-seconds integer or an HTTP-date form
// (RFC 7231 Section 7.1.3). Invalid or empty values return zero, meaning the
// dispatcher applies its default retry schedule.
func parseRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		delta := time.Until(t)
		if delta < 0 {
			return 0
		}
		return delta
	}
	return 0
}

func jsonMarshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := jsonEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return out, nil
}

// validateWebhookURL is called before a network request is made so an
// operator can reject a poisoned secret before wiring live delivery.
func validateWebhookURL(raw string) error {
	if !validHTTPS(raw) {
		return errors.New("invalid https url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Fragment != "" {
		return errors.New("url must not contain fragment")
	}
	return nil
}
