package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const d05ContainerID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func d05ControlClient(t *testing.T, operation string, handler func(http.ResponseWriter, *http.Request)) (*client.Client, *fakeDaemon) {
	t.Helper()
	ep, _, fake := doctorFixture(t, handler)
	binding := BindingData{Endpoint: ep.Summary(), DaemonID: "synthetic-daemon", API: "1.54", NativePlatform: "linux/amd64"}
	target := d05ContainerID
	if operation == "create" {
		target = "jig-synthetic-main"
	}
	sdk, err := newExecutionClient(ep, binding, controlPermission{operation: operation, target: target})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdk.Close() })
	return sdk, fake
}
func d05ControlCall(ctx context.Context, sdk *client.Client, operation string) error {
	switch operation {
	case "create":
		_, err := sdk.ContainerCreate(ctx, client.ContainerCreateOptions{Name: "jig-synthetic-main", Platform: &ocispec.Platform{OS: "linux", Architecture: "amd64"}, Config: &container.Config{Image: "sha256:" + d05ContainerID}})
		return err
	case "start":
		_, err := sdk.ContainerStart(ctx, d05ContainerID, client.ContainerStartOptions{})
		return err
	case "stop":
		seconds := 2
		_, err := sdk.ContainerStop(ctx, d05ContainerID, client.ContainerStopOptions{Timeout: &seconds, Signal: "SIGTERM"})
		return err
	case "kill":
		_, err := sdk.ContainerKill(ctx, d05ContainerID, client.ContainerKillOptions{Signal: "SIGKILL"})
		return err
	case "remove":
		_, err := sdk.ContainerRemove(ctx, d05ContainerID, client.ContainerRemoveOptions{})
		return err
	case "inspect":
		_, err := sdk.ContainerInspect(ctx, d05ContainerID, client.ContainerInspectOptions{})
		return err
	default:
		panic("test operation")
	}
}
func TestD05ControlTransportSDKRoutes(t *testing.T) {
	for _, operation := range []string{"create", "start", "stop", "kill", "remove", "inspect"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("DOCKER_API_VERSION", "1.1")
			t.Setenv("HTTP_PROXY", "http://unreachable.invalid")
			sdk, fake := d05ControlClient(t, operation, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/info") {
					normalMetadata(w, r)
					return
				}
				switch operation {
				case "create":
					fmt.Fprintf(w, `{"Id":%q,"Warnings":[]}`, d05ContainerID)
				case "inspect":
					fmt.Fprintf(w, `{"Id":%q,"State":{"Running":false}}`, d05ContainerID)
				default:
					w.WriteHeader(http.StatusNoContent)
				}
			})
			if err := d05ControlCall(context.Background(), sdk, operation); err != nil {
				t.Fatal(err)
			}
			if err := d05ControlCall(context.Background(), sdk, operation); err == nil {
				t.Fatal("permission reused")
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.calls) != 2 || fake.calls[0] != "GET /v1.54/info" {
				t.Fatal(fake.calls)
			}
		})
	}
}
func TestD05ControlTransportRejectsDriftAndBadReplies(t *testing.T) {
	for _, name := range []string{"daemon", "platform", "info_json", "redirect", "truncated", "oversized", "lost_response", "private_error"} {
		t.Run(name, func(t *testing.T) {
			sdk, fake := d05ControlClient(t, "create", func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/info") {
					switch name {
					case "daemon":
						fmt.Fprint(w, strings.Replace(doctorInfo, "synthetic-daemon", "different-daemon", 1))
					case "platform":
						fmt.Fprint(w, strings.Replace(doctorInfo, "x86_64", "aarch64", 1))
					case "info_json":
						fmt.Fprint(w, "{")
					default:
						normalMetadata(w, r)
					}
					return
				}
				switch name {
				case "redirect":
					w.Header().Set("Location", "http://unreachable.invalid")
					w.WriteHeader(302)
				case "truncated":
					w.Header().Set("Content-Length", "100")
					fmt.Fprint(w, "{")
				case "oversized":
					fmt.Fprint(w, strings.Repeat("x", responseLimit+1))
				case "lost_response":
					connection, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						connection.Close()
					}
				case "private_error":
					w.WriteHeader(500)
					fmt.Fprint(w, `{"message":"PRIVATE_SENTINEL"}`)
				default:
					fmt.Fprintf(w, `{"Id":%q}`, d05ContainerID)
				}
			})
			err := d05ControlCall(context.Background(), sdk, "create")
			if err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
				t.Fatalf("unsafe result: %v", err)
			}
			if err := d05ControlCall(context.Background(), sdk, "create"); err == nil {
				t.Fatal("retried consumed create")
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			expected := 2
			if name == "daemon" || name == "platform" || name == "info_json" {
				expected = 1
			}
			if len(fake.calls) != expected {
				t.Fatal(fake.calls)
			}
		})
	}
}
func TestD05ControlTransportForbiddenSDKCalls(t *testing.T) {
	for _, name := range []string{"wrong_id", "checkpoint", "force_remove", "image_pull", "list", "size", "unbounded_stop"} {
		t.Run(name, func(t *testing.T) {
			operation := "start"
			if name == "force_remove" {
				operation = "remove"
			}
			if name == "size" {
				operation = "inspect"
			}
			if name == "unbounded_stop" {
				operation = "stop"
			}
			sdk, fake := d05ControlClient(t, operation, nil)
			var err error
			switch name {
			case "wrong_id":
				_, err = sdk.ContainerStart(context.Background(), strings.Repeat("b", 64), client.ContainerStartOptions{})
			case "checkpoint":
				_, err = sdk.ContainerStart(context.Background(), d05ContainerID, client.ContainerStartOptions{CheckpointID: "forbidden"})
			case "force_remove":
				_, err = sdk.ContainerRemove(context.Background(), d05ContainerID, client.ContainerRemoveOptions{Force: true})
			case "image_pull":
				_, err = sdk.ImagePull(context.Background(), "private:image", client.ImagePullOptions{})
			case "list":
				_, err = sdk.ContainerList(context.Background(), client.ContainerListOptions{})
			case "size":
				_, err = sdk.ContainerInspect(context.Background(), d05ContainerID, client.ContainerInspectOptions{Size: true})
			case "unbounded_stop":
				seconds := -1
				_, err = sdk.ContainerStop(context.Background(), d05ContainerID, client.ContainerStopOptions{Timeout: &seconds})
			}
			if err == nil {
				t.Fatal("forbidden call accepted")
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.calls) != 0 {
				t.Fatal(fake.calls)
			}
		})
	}
}

func TestD05ControlTransportCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	sdk, fake := d05ControlClient(t, "start", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d05ControlCall(ctx, sdk, "start") }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("identity request not reached")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled call succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not bound request")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.calls) != 1 {
		t.Fatal(fake.calls)
	}
}
func TestD05ControlTransportSingleConcurrentUse(t *testing.T) {
	sdk, fake := d05ControlClient(t, "start", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/info") {
			normalMetadata(w, r)
		} else {
			w.WriteHeader(204)
		}
	})
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- d05ControlCall(context.Background(), sdk, "start") }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d", successes)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.calls) != 2 {
		t.Fatal(fake.calls)
	}
}
func TestD05ControlTransportSocketLoss(t *testing.T) {
	ep, _, fake := doctorFixture(t, nil)
	binding := BindingData{Endpoint: ep.Summary(), DaemonID: "synthetic-daemon", API: "1.54", NativePlatform: "linux/amd64"}
	sdk, err := newExecutionClient(ep, binding, controlPermission{operation: "start", target: d05ContainerID})
	if err != nil {
		t.Fatal(err)
	}
	defer sdk.Close()
	if err := os.Remove(ep.SocketPath()); err != nil {
		t.Fatal(err)
	}
	if err := d05ControlCall(context.Background(), sdk, "start"); err == nil {
		t.Fatal("lost socket accepted")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.calls) != 0 {
		t.Fatal(fake.calls)
	}
}
func TestD05ControlPermissionRejects(t *testing.T) {
	for _, p := range []controlPermission{{"start", "short"}, {"start", strings.Repeat("A", 64)}, {"start", "../escape"}, {"create", "unowned"}, {"create", "jig-../escape"}, {"exec", d05ContainerID}, {"prune", d05ContainerID}} {
		if _, _, _, err := p.route("linux/amd64"); err == nil {
			t.Fatal("accepted permission", p)
		}
	}
}

func TestD05ControlTransportRequestLimit(t *testing.T) {
	sdk, fake := d05ControlClient(t, "create", nil)
	_, err := sdk.ContainerCreate(context.Background(), client.ContainerCreateOptions{Name: "jig-synthetic-main", Platform: &ocispec.Platform{OS: "linux", Architecture: "amd64"}, Config: &container.Config{Image: "sha256:" + d05ContainerID, Env: []string{strings.Repeat("x", executionRequestBytes+1)}}})
	if err == nil {
		t.Fatal("oversized create accepted")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.calls) != 0 {
		t.Fatal(fake.calls)
	}
}
