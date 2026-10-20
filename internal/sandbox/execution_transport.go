package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const executionControlTimeout = 10 * time.Second
const executionRequestBytes = 1 << 20

// controlPermission scopes one SDK call. It is not policy or lifecycle
// authority: the future runtime must obtain engine acknowledgement before
// constructing create/start clients, and verify exact ownership before removal.
// Lost replies consume the permission; reconciliation needs a separate inspect.
type controlPermission struct{ operation, target string }

func (p controlPermission) route(platform string) (method, path, query string, err error) {
	if p.operation == "create" {
		if len(p.target) < 5 || len(p.target) > 128 || !strings.HasPrefix(p.target, "jig-") {
			return "", "", "", fmt.Errorf("invalid container name")
		}
		for _, r := range p.target {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", "", "", fmt.Errorf("invalid container name")
			}
		}
		return http.MethodPost, "/containers/create", url.Values{"name": {p.target}, "platform": {platform}}.Encode(), nil
	}
	// Full IDs only: no abbreviated IDs, names, paths or wildcard selection.
	if len(p.target) != 64 {
		return "", "", "", fmt.Errorf("invalid container identity")
	}
	for _, r := range p.target {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return "", "", "", fmt.Errorf("invalid container identity")
		}
	}
	path = "/containers/" + p.target
	switch p.operation {
	case "inspect":
		return http.MethodGet, path + "/json", "", nil
	case "start":
		return http.MethodPost, path + "/start", "", nil
	case "stop":
		return http.MethodPost, path + "/stop", "signal=SIGTERM&t=2", nil
	case "kill":
		return http.MethodPost, path + "/kill", "signal=SIGKILL", nil
	case "remove":
		return http.MethodDelete, path, "", nil // never force-remove or remove volumes
	default:
		return "", "", "", fmt.Errorf("unsupported control operation")
	}
}

type executionTransport struct {
	base       *http.Transport
	endpoint   *Endpoint
	binding    BindingData
	permission controlPermission
	mu         sync.Mutex
	used       bool
}

// newExecutionClient deliberately does not negotiate versions or consult the
// environment. The pinned binding comes from completed read-only inspection.
// Kept package-private until the runtime and qualification gates are wired.
func newExecutionClient(endpoint *Endpoint, binding BindingData, permission controlPermission) (*client.Client, error) {
	if err := endpoint.CheckBound(); err != nil {
		return nil, err
	}
	if binding.Endpoint.URI != endpoint.Summary().URI || binding.DaemonID == "" || len(binding.DaemonID) > 512 || !supportedAPI(binding.API) || (binding.NativePlatform != "linux/amd64" && binding.NativePlatform != "linux/arm64") {
		return nil, endpointFailure("sandbox_binding_invalid", "execution")
	}
	if _, _, _, err := permission.route(binding.NativePlatform); err != nil {
		return nil, endpointFailure("sandbox_execution_forbidden", "execution")
	}
	g := &executionTransport{endpoint: endpoint, binding: binding, permission: permission}
	dialer := &net.Dialer{Timeout: inspectionDialTimeout}
	g.base = &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: headerLimit, ResponseHeaderTimeout: executionControlTimeout,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if err := endpoint.CheckBound(); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, "unix", endpoint.SocketPath())
		}}
	hc := &http.Client{Transport: g, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect denied") }}
	return client.New(client.WithHost("unix://"+endpoint.SocketPath()), client.WithAPIVersion(binding.API), client.WithHTTPClient(hc), client.WithTraceProvider(tracenoop.NewTracerProvider()), client.WithTraceOptions(otelhttp.WithMeterProvider(metricnoop.NewMeterProvider())))
}
func (g *executionTransport) CloseIdleConnections() { g.base.CloseIdleConnections() }
func (g *executionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer req.Body.Close()
	}
	g.mu.Lock()
	if g.used {
		g.mu.Unlock()
		return nil, endpointFailure("sandbox_execution_consumed", "execution")
	}
	g.used = true
	g.mu.Unlock()
	fail := func(reason string) (*http.Response, error) { return nil, endpointFailure(reason, "execution") }
	method, path, query, err := g.permission.route(g.binding.NativePlatform)
	if err != nil || req.Method != method || req.URL.Path != "/v"+g.binding.API+path || req.URL.RawQuery != query || req.URL.Fragment != "" || req.URL.User != nil || req.URL.RawPath != "" {
		return fail("sandbox_execution_forbidden")
	}
	// SDK never needs ambient credentials, custom routing, or upgrade headers for
	// these JSON control calls. Attach/streaming requires its own boundary.
	for key := range req.Header {
		switch strings.ToLower(key) {
		case "authorization", "proxy-authorization", "x-registry-auth", "x-registry-config", "upgrade":
			return fail("sandbox_execution_forbidden")
		}
	}
	ctx, cancel := context.WithTimeout(req.Context(), executionControlTimeout)
	defer cancel()
	if err := g.endpoint.CheckBound(); err != nil {
		return fail("sandbox_endpoint_changed")
	}
	var body []byte
	if req.Body != nil {
		body, err = io.ReadAll(io.LimitReader(req.Body, executionRequestBytes+1))
		closeErr := req.Body.Close()
		if err != nil || closeErr != nil || len(body) > executionRequestBytes {
			return fail("sandbox_execution_limit")
		}
	}
	if g.permission.operation == "create" {
		if !json.Valid(body) {
			return fail("sandbox_execution_invalid")
		}
	} else if len(body) != 0 {
		return fail("sandbox_execution_forbidden")
	}
	// Recheck daemon identity immediately before each operation, including
	// reconciliation reads. Socket identity alone cannot detect a daemon restart.
	infoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/v"+g.binding.API+"/info", nil)
	if err != nil {
		return fail("sandbox_execution_invalid")
	}
	infoResponse, infoBody, err := g.exchange(infoReq)
	if err != nil {
		return fail("sandbox_daemon_unavailable")
	}
	var info struct{ ID, OSType, Architecture string }
	if infoResponse.StatusCode != 200 || json.Unmarshal(infoBody, &info) != nil || info.ID != g.binding.DaemonID || nativePlatform(info.OSType, info.Architecture, "") != g.binding.NativePlatform {
		return fail("sandbox_endpoint_changed")
	}
	if err := g.endpoint.CheckBound(); err != nil {
		return fail("sandbox_endpoint_changed")
	}
	request := req.Clone(ctx)
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.GetBody = nil
	response, data, err := g.exchange(request)
	if err != nil {
		return fail("sandbox_execution_outcome_unknown")
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return fail("sandbox_execution_redirect")
	}
	if response.StatusCode >= 400 {
		// Preserve status for not-found/conflict classification without leaking raw
		// daemon diagnostics, paths, environment, or image names through SDK errors.
		data = []byte(`{"message":"container control request rejected"}`)
	} else if (g.permission.operation == "create" || g.permission.operation == "inspect") && !json.Valid(data) {
		return fail("sandbox_execution_outcome_unknown")
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	response.Header = make(http.Header)
	response.Header.Set("Content-Type", "application/json")
	return response, nil
}
func (g *executionTransport) exchange(req *http.Request) (*http.Response, []byte, error) {
	response, err := g.base.RoundTrip(req)
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil || len(data) > responseLimit {
		return nil, nil, fmt.Errorf("bounded control response failed")
	}
	return response, data, nil
}
