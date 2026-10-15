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
	"strconv"
	"strings"
	"sync"
	"time"
)

const inspectionTimeout = 10 * time.Second
const inspectionDialTimeout = 2 * time.Second
const responseLimit = 4 << 20
const headerLimit = 64 << 10
const requestLimit = 36
const imageLimit = 32

// inspectionTransport is the final network boundary underneath the SDK. Each
// allowed request uses a fresh connection, preventing net/http stale-connection
// retries from escaping the request counter. It buffers bounded bodies before
// any SDK decoder sees them and never returns raw transport errors to callers.
type inspectionTransport struct {
	base     *http.Transport
	endpoint *Endpoint
	images   map[string]bool
	mu       sync.Mutex
	requests int
	failed   bool
	reason   string
	presence computePresence
}
type computePresence struct {
	Memory          *bool     `json:"MemoryLimit"`
	Swap            *bool     `json:"SwapLimit"`
	CPUPeriod       *bool     `json:"CpuCfsPeriod"`
	CPUQuota        *bool     `json:"CpuCfsQuota"`
	PIDs            *bool     `json:"PidsLimit"`
	SecurityOptions *[]string `json:"SecurityOptions"`
}

func newInspectionTransport(endpoint *Endpoint, images []string) *inspectionTransport {
	g := &inspectionTransport{endpoint: endpoint, images: map[string]bool{}}
	for _, image := range images {
		g.images["/images/"+image+"/json"] = true
	}
	dialer := &net.Dialer{Timeout: inspectionDialTimeout}
	g.base = &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: headerLimit, ResponseHeaderTimeout: inspectionTimeout,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if err := endpoint.CheckBound(); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, "unix", endpoint.SocketPath())
		},
	}
	return g
}
func (g *inspectionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fail := func(reason string) (*http.Response, error) {
		g.failed = true
		g.reason = reason
		return nil, endpointFailure(reason, "inspection")
	}
	if g.failed {
		return nil, endpointFailure(g.reason, "inspection")
	}
	if g.requests >= requestLimit {
		return fail("sandbox_inspection_limit")
	}
	if err := g.endpoint.CheckBound(); err != nil {
		return fail("sandbox_endpoint_changed")
	}
	path := req.URL.Path
	if strings.HasPrefix(path, "/v") && path != "/version" {
		prefix, rest, ok := strings.Cut(strings.TrimPrefix(path, "/v"), "/")
		if !ok || !supportedAPI(prefix) {
			return fail("sandbox_inspection_forbidden")
		}
		path = "/" + rest
	}
	allowed := (path == "/_ping" && (req.Method == http.MethodHead || req.Method == http.MethodGet)) || req.Method == http.MethodGet && (path == "/version" || path == "/info" || g.images[path])
	if !allowed || req.URL.RawQuery != "" || req.URL.Fragment != "" || req.URL.User != nil || req.URL.EscapedPath() != (&url.URL{Path: req.URL.Path}).EscapedPath() {
		return fail("sandbox_inspection_forbidden")
	}
	if _, ok := req.Context().Deadline(); !ok {
		return fail("sandbox_inspection_unbounded")
	}
	g.requests++
	resp, err := g.base.RoundTrip(req)
	if err != nil {
		return fail("sandbox_daemon_unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	closeErr := resp.Body.Close()
	if err != nil || closeErr != nil {
		return fail("sandbox_daemon_unavailable")
	}
	if len(body) > responseLimit {
		return fail("sandbox_inspection_limit")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fail("sandbox_inspection_redirect")
	}
	if path == "/_ping" && resp.StatusCode != 200 && !(req.Method == http.MethodHead && (resp.StatusCode == 405 || resp.StatusCode == 501)) {
		return fail("sandbox_daemon_unavailable")
	}
	if path != "/_ping" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if !json.Valid(body) {
			return fail("sandbox_daemon_invalid")
		}
		if path == "/info" {
			if err := json.Unmarshal(body, &g.presence); err != nil {
				return fail("sandbox_daemon_invalid")
			}
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, nil
}
func supportedAPI(v string) bool { minor, ok := apiMinor(v); return ok && minor >= 40 && minor <= 54 }
func apiMinor(v string) (int, bool) {
	major, minor, ok := strings.Cut(v, ".")
	if !ok || major != "1" {
		return 0, false
	}
	n, err := strconv.Atoi(minor)
	return n, err == nil && fmt.Sprintf("%d", n) == minor
}
