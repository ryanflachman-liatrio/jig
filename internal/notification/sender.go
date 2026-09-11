package notification

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Sender is the narrow contract shared by every destination adapter. The
// dispatcher never inspects concrete adapters — it passes a resolved Binding
// and a pre-built payload and interprets the returned SendResult.
type Sender interface {
	Send(ctx context.Context, target Binding, payload OutboundPayload) SendResult
}

// SendResult captures the outcome of one Send attempt. Retry classifies
// whether the dispatcher should retry the attempt; Duration is recorded for
// diagnostics.
type SendResult struct {
	Reason     string
	Retry      bool
	RetryAfter time.Duration
	Err        error
	Duration   time.Duration
}

// Success returns true when the send was accepted by the destination.
func (r SendResult) Success() bool { return r.Reason == "delivered" }

// OutboundPayload is the fully formatted request for one destination binding.
// Different destinations format different fields: Slack uses SlackBody, the
// generic webhook uses JSON, and desktop senders use Title + DesktopBody.
type OutboundPayload struct {
	JSON         []byte
	SlackBody    string
	DesktopTitle string
	DesktopBody  string
}

// SenderRegistry routes bindings to their concrete Sender adapters. Callers
// register desktop, slack, and webhook senders once at wire time and the
// dispatcher looks up the right one at Send time.
type SenderRegistry struct {
	desktop Sender
	slack   Sender
	webhook Sender
}

// NewSenderRegistry constructs a registry with the given adapters. Any nil
// adapter is treated as unavailable: Send returns a diagnosed failure without
// attempting delivery.
func NewSenderRegistry(desktop, slack, webhook Sender) *SenderRegistry {
	return &SenderRegistry{desktop: desktop, slack: slack, webhook: webhook}
}

// Send routes to the matching adapter. Missing adapters diagnose without
// blocking and never retry.
func (r *SenderRegistry) Send(ctx context.Context, target Binding, payload OutboundPayload) SendResult {
	if r == nil {
		return SendResult{Reason: "adapter_missing", Err: errors.New("sender registry not configured")}
	}
	switch target.Type {
	case Desktop:
		if r.desktop == nil {
			return SendResult{Reason: "adapter_missing", Err: errors.New("desktop adapter not configured")}
		}
		return r.desktop.Send(ctx, target, payload)
	case Slack:
		if r.slack == nil {
			return SendResult{Reason: "adapter_missing", Err: errors.New("slack adapter not configured")}
		}
		return r.slack.Send(ctx, target, payload)
	case Webhook:
		if r.webhook == nil {
			return SendResult{Reason: "adapter_missing", Err: errors.New("webhook adapter not configured")}
		}
		return r.webhook.Send(ctx, target, payload)
	default:
		return SendResult{Reason: "adapter_missing", Err: fmt.Errorf("unknown destination type %q", target.Type)}
	}
}
