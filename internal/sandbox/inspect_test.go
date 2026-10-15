package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

const doctorInfo = `{"ID":"synthetic-daemon","OSType":"linux","Architecture":"x86_64","MemoryLimit":true,"SwapLimit":true,"CpuCfsPeriod":true,"CpuCfsQuota":true,"PidsLimit":true,"CgroupVersion":"2","CgroupDriver":"systemd","SecurityOptions":["name=rootless"],"Warnings":["PRIVATE_SENTINEL"]}`
const doctorVersion = `{"ApiVersion":"1.54","MinAPIVersion":"1.40","Os":"linux","Arch":"amd64"}`

type fakeDaemon struct {
	mu      sync.Mutex
	calls   []string
	handler func(http.ResponseWriter, *http.Request)
}

func (f *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	if f.handler != nil {
		f.handler(w, r)
		return
	}
	normalMetadata(w, r)
}
func normalMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/_ping":
		w.Header().Set("API-Version", "1.54")
		w.WriteHeader(200)
	case strings.HasSuffix(r.URL.Path, "/version"):
		fmt.Fprint(w, doctorVersion)
	case strings.HasSuffix(r.URL.Path, "/info"):
		fmt.Fprint(w, doctorInfo)
	case strings.Contains(r.URL.Path, "/images/"):
		fmt.Fprint(w, `{"Id":"sha256:synthetic","Os":"linux","Architecture":"amd64","Config":{"Env":["PRIVATE_SENTINEL"]}}`)
	default:
		w.WriteHeader(500)
	}
}
func doctorFixture(t *testing.T, handler func(http.ResponseWriter, *http.Request)) (*Endpoint, *Policy, *fakeDaemon) {
	t.Helper()
	in, data := policyFixture(t)
	dir, err := os.MkdirTemp("/tmp", "jig-doc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeDaemon{handler: handler}
	server := &http.Server{Handler: fake}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ln) }()
	t.Cleanup(func() { server.Close(); <-done })
	p := loadFixture(t, in, strings.Replace(data, "unix:///synthetic/docker.sock", "unix://"+socket, 1))
	ep, err := SelectEndpoint(EndpointInput{Cwd: in.Cwd, Project: in.Project, Home: in.Home, Host: "unix://" + socket}, p)
	if err != nil {
		t.Fatal(err)
	}
	return ep, p, fake
}
func TestD04DoctorReadOnlyTrace(t *testing.T) {
	ep, p, fake := doctorFixture(t, nil)
	st := dockerStep(t)
	result, err := Inspect(context.Background(), ep, p, []workflow.Step{*st, *st}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding == nil || result.Binding.NativePlatform != "linux/amd64" || result.Binding.API != "1.54" || result.Binding.DaemonID != "synthetic-daemon" || !result.Capabilities.Ready || result.Capabilities.Enforcement != "unverified" || len(result.Images) != 1 || result.Steps[0].NanoCPUs != 1e9 {
		t.Fatalf("inspection=%+v", result)
	}
	fake.mu.Lock()
	calls := append([]string(nil), fake.calls...)
	fake.mu.Unlock()
	want := []string{"HEAD /_ping", "GET /v1.54/version", "GET /v1.54/info", "GET /v1.54/images/docker.io/library/alpine:latest/json"}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") || result.Requests != 4 {
		t.Fatalf("calls=%v", calls)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "PRIVATE_SENTINEL") {
		t.Fatal("raw daemon metadata leaked")
	}
	t.Logf("actual API trace: %v; image inspect deduplicated; no mutation/helper/registry requests", calls)
	expected := *result.Binding
	expected.DaemonID = "different"
	if _, err := Inspect(context.Background(), ep, p, []workflow.Step{*st}, &expected); err == nil {
		t.Fatal("daemon identity drift accepted")
	}
}
func TestD04DoctorBoundsAndFailures(t *testing.T) {
	for _, mode := range []string{"redirect", "body-limit", "header-limit", "malformed", "old-api", "wrong-os", "missing-id", "unknown-arch", "head-error"} {
		t.Run(mode, func(t *testing.T) {
			ep, p, fake := doctorFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "old-api" && r.URL.Path == "/_ping" {
					w.Header().Set("API-Version", "1.39")
					return
				}
				if mode == "head-error" {
					w.WriteHeader(500)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/info") {
					switch mode {
					case "redirect":
						w.Header().Set("Location", "http://PRIVATE_SENTINEL")
						w.WriteHeader(302)
					case "body-limit":
						fmt.Fprint(w, strings.Repeat("x", responseLimit+1))
					case "header-limit":
						w.Header().Set("X-Large", strings.Repeat("x", headerLimit+1))
						fmt.Fprint(w, doctorInfo)
					case "malformed":
						fmt.Fprint(w, `{"ID":`)
					case "wrong-os":
						fmt.Fprint(w, strings.Replace(doctorInfo, "linux", "windows", 1))
					case "missing-id":
						fmt.Fprint(w, strings.Replace(doctorInfo, "synthetic-daemon", "", 1))
					case "unknown-arch":
						fmt.Fprint(w, strings.Replace(doctorInfo, "x86_64", "unknown", 1))
					default:
						normalMetadata(w, r)
					}
					return
				}
				normalMetadata(w, r)
			})
			result, err := Inspect(context.Background(), ep, p, []workflow.Step{*dockerStep(t)}, nil)
			if err == nil {
				t.Fatalf("%s succeeded %+v", mode, result)
			}
			if strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
				t.Fatal("raw error leaked")
			}
			fake.mu.Lock()
			calls := append([]string(nil), fake.calls...)
			fake.mu.Unlock()
			if mode == "head-error" && len(calls) != 1 {
				t.Fatalf("SDK retried failed HEAD: %v", calls)
			}
			for _, call := range calls {
				if strings.HasPrefix(call, "POST ") {
					t.Fatal("mutation")
				}
			}
		})
	}
}
func TestD04DoctorCapabilitiesAndImages(t *testing.T) {
	for _, mode := range []string{"missing-capability", "false-capability", "rootless-v1", "missing-image", "pull-never", "emulation", "image-cap"} {
		t.Run(mode, func(t *testing.T) {
			ep, p, _ := doctorFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/info") {
					data := doctorInfo
					switch mode {
					case "missing-capability":
						data = strings.Replace(data, `"MemoryLimit":true,`, "", 1)
					case "false-capability":
						data = strings.Replace(data, `"MemoryLimit":true`, `"MemoryLimit":false`, 1)
					case "rootless-v1":
						data = strings.Replace(data, `"CgroupVersion":"2"`, `"CgroupVersion":"1"`, 1)
					}
					fmt.Fprint(w, data)
					return
				}
				if strings.Contains(r.URL.Path, "/images/") && (mode == "missing-image" || mode == "pull-never") {
					w.WriteHeader(404)
					fmt.Fprint(w, `{"message":"PRIVATE_SENTINEL"}`)
					return
				}
				normalMetadata(w, r)
			})
			st := dockerStep(t)
			if mode == "pull-never" {
				st.SnapshotSandbox.Profile.Pull = "never"
			}
			if mode == "emulation" {
				st.SnapshotSandbox.Profile.Platform = "linux/arm64"
			}
			steps := []workflow.Step{*st}
			if mode == "image-cap" {
				steps = nil
				for i := 0; i < 33; i++ {
					copy := *st
					request := st.ResolvedSandbox()
					request.Profile.Image = fmt.Sprintf("alpine:tag%d", i)
					request.Profile.NormalizedImage = "docker.io/library/" + request.Profile.Image
					copy.SnapshotSandbox = &request
					steps = append(steps, copy)
				}
			}
			result, err := Inspect(context.Background(), ep, p, steps, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing-capability":
				if result.Capabilities.Memory != nil || result.Capabilities.Ready {
					t.Fatal("invented capability")
				}
			case "false-capability":
				if result.Capabilities.Memory == nil || *result.Capabilities.Memory || result.Capabilities.Ready {
					t.Fatal("lost false capability")
				}
			case "rootless-v1":
				if result.Capabilities.Ready {
					t.Fatal("rootless cgroup-v1 certified")
				}
			case "missing-image":
				if result.Images[0].Status != "acquisition_required" {
					t.Fatalf("image=%+v", result.Images[0])
				}
			case "pull-never":
				if result.Images[0].Status != "cache_required_missing" {
					t.Fatal("missing cache ignored")
				}
			case "emulation":
				if result.Steps[0].Reason != "sandbox_emulation_denied" {
					t.Fatal("emulation allowed")
				}
			case "image-cap":
				if result.Requests != 35 || len(result.Images) != 33 || result.Images[32].Status != "uninspected" {
					t.Fatalf("cap=%+v", result)
				}
			}
		})
	}
}
func TestD04DoctorCancellation(t *testing.T) {
	entered := make(chan struct{})
	ep, p, _ := doctorFixture(t, func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := Inspect(ctx, ep, p, nil, nil); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled inspection succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("inspection ignored cancellation")
	}
}

func TestD04DoctorTransportFence(t *testing.T) {
	ep, _, fake := doctorFixture(t, nil)
	for _, test := range []struct{ method, path string }{{"POST", "/v1.54/containers/create"}, {"GET", "/v1.54/images/json"}, {"GET", "/v1.55/info"}, {"GET", "/v1.54/images/other/json"}, {"GET", "/v1.54/info?x=1"}} {
		g := newInspectionTransport(ep, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, _ := http.NewRequestWithContext(ctx, test.method, "http://synthetic"+test.path, nil)
		if _, err := g.RoundTrip(req); err == nil {
			t.Fatalf("forbidden request %s %s", test.method, test.path)
		}
		cancel()
		g.base.CloseIdleConnections()
	}
	fake.mu.Lock()
	n := len(fake.calls)
	fake.mu.Unlock()
	if n != 0 {
		t.Fatalf("forbidden requests reached server: %d", n)
	}
	g := newInspectionTransport(ep, nil)
	defer g.base.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < requestLimit; i++ {
		req, _ := http.NewRequestWithContext(ctx, "HEAD", "http://synthetic/_ping", nil)
		resp, err := g.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	req, _ := http.NewRequestWithContext(ctx, "HEAD", "http://synthetic/_ping", nil)
	if _, err := g.RoundTrip(req); err == nil {
		t.Fatal("37th request accepted")
	}
	fake.mu.Lock()
	n = len(fake.calls)
	fake.mu.Unlock()
	if n != requestLimit {
		t.Fatalf("wire count=%d", n)
	}
	t.Log("forbidden methods/paths: wire calls=0; request cap: exactly 36 calls, 37th rejected")
}

func TestD04DoctorPingFallbackAndBodyBoundary(t *testing.T) {
	ep, p, _ := doctorFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.WriteHeader(405)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/info") {
			data := doctorInfo + strings.Repeat(" ", responseLimit-len(doctorInfo))
			fmt.Fprint(w, data)
			return
		}
		normalMetadata(w, r)
	})
	result, err := Inspect(context.Background(), ep, p, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Requests != 4 {
		t.Fatalf("negotiation fallback requests=%d", result.Requests)
	}
	// A shorter caller deadline caps the entire SDK negotiation/inspection.
	ep, p, _ = doctorFixture(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Inspect(ctx, ep, p, nil, nil); err == nil || ctx.Err() == nil {
		t.Fatal("caller deadline ignored")
	}
}
