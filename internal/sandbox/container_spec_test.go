package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/ryanflachman-liatrio/jig/internal/step"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func d05CreateFixture(t *testing.T) containerCreateSpec {
	t.Helper()
	wf, err := workflow.Load(filepath.Join("..", "..", "examples", "docker-build-jig.toml"))
	if err != nil {
		t.Fatal(err)
	}
	st := &wf.Steps[0]
	var image client.ImageInspectResult
	raw := fmt.Sprintf(`{"Id":"sha256:%s","Os":"linux","Architecture":"amd64","Config":{"Env":["PRIVATE_IMAGE_ENV=sentinel"],"Entrypoint":["unsafe-entry"],"Cmd":["unsafe-command"],"User":"0","WorkingDir":"/unsafe","Healthcheck":{"Test":["CMD","unsafe-health"]}}}`, d05ContainerID)
	if err := json.Unmarshal([]byte(raw), &image); err != nil {
		t.Fatal(err)
	}
	nonce := strings.Repeat("a", 32)
	return containerCreateSpec{step: st, effective: Effective{Request: st.ResolvedSandbox().Clone(), NanoCPUs: 2_000_000_000, MemoryBytes: 2 << 30, PIDs: 256}, image: image, binding: BindingData{DaemonID: "synthetic-daemon", NativePlatform: "linux/amd64", API: "1.54"}, phase: step.ContainerPhase{RunID: "synthetic-run", StepID: st.ID, Nonce: nonce, Name: "jig-" + nonce + "-main", Phase: "main", State: step.ContainerIntent, DaemonID: "synthetic-daemon", EndpointID: "synthetic-endpoint", ImageID: image.ID}, stagingRoot: filepath.Join(t.TempDir(), "private-stage"), uid: 1000, gid: 1000}
}
func TestD05ContainerCreateConstraints(t *testing.T) {
	s := d05CreateFixture(t)
	options, err := buildContainerCreate(s)
	if err != nil {
		t.Fatal(err)
	}
	c, h := options.Config, options.HostConfig
	if c.Image != s.image.ID || c.User != "1000:1000" || c.WorkingDir != "/workspace" || !reflect.DeepEqual(c.Entrypoint, []string{"/usr/bin/env"}) || c.Cmd[0] != "-i" || c.Cmd[len(c.Cmd)-1] != s.step.Run {
		t.Fatalf("bad process config: %+v", c)
	}
	if strings.Contains(strings.Join(c.Cmd, " "), "PRIVATE_IMAGE_ENV") || len(c.Env) != 0 || c.Tty || c.OpenStdin || c.AttachStdin || !c.AttachStdout || !c.AttachStderr || !c.NetworkDisabled || c.Healthcheck.Test[0] != "NONE" || c.StopSignal != "SIGTERM" || *c.StopTimeout != 2 {
		t.Fatal("inherited or unsafe process configuration")
	}
	if !h.ReadonlyRootfs || h.NetworkMode != "none" || !reflect.DeepEqual(h.CapDrop, []string{"ALL"}) || !reflect.DeepEqual(h.SecurityOpt, []string{"no-new-privileges:true"}) || h.IpcMode != "private" || h.CgroupnsMode != "private" || !*h.Init {
		t.Fatalf("missing controls: %+v", h)
	}
	if h.Privileged || h.AutoRemove || h.PublishAllPorts || h.PidMode != "" || h.UTSMode != "" || h.UsernsMode != "" || len(h.Devices) != 0 || len(h.Binds) != 0 || len(h.PortBindings) != 0 || len(h.VolumesFrom) != 0 || len(h.CapAdd) != 0 || h.RestartPolicy.Name != "no" || h.LogConfig.Type != "none" {
		t.Fatal("unapproved host authority")
	}
	if h.NanoCPUs != s.effective.NanoCPUs || h.Memory != s.effective.MemoryBytes || h.MemorySwap != h.Memory || *h.PidsLimit != s.effective.PIDs {
		t.Fatal("resource limits changed")
	}
	if len(h.Mounts) != 4 || len(h.Tmpfs) != 2 || h.ShmSize != 16<<20 {
		t.Fatal("mount inventory changed")
	}
	for i, m := range h.Mounts {
		if filepath.Dir(m.Source) != s.stagingRoot || m.BindOptions.CreateMountpoint || !m.BindOptions.NonRecursive || m.BindOptions.Propagation != "rprivate" || m.ReadOnly != (i >= 2) {
			t.Fatalf("unsafe mount: %+v", m)
		}
	}
	for _, options := range h.Tmpfs {
		if !strings.Contains(options, "size=67108864") || !strings.Contains(options, "noexec") || !strings.Contains(options, "uid=1000,gid=1000") {
			t.Fatal(options)
		}
	}
	// Options own their maps/pointers independently of authoring and effective limits.
	c.Labels["jig.run"] = "changed"
	*h.PidsLimit = 1
	if s.phase.RunID != "synthetic-run" || s.effective.PIDs != 256 {
		t.Fatal("configuration aliases caller state")
	}
}
func TestD05ContainerCreateRejects(t *testing.T) {
	for _, name := range []string{"root_uid", "root_gid", "relative_stage", "root_stage", "missing_limits", "wrong_authority", "pull", "network", "secrets", "history", "integration", "foreign_platform", "tag_image", "volumes", "missing_image_config", "wrong_daemon", "wrong_image", "wrong_phase", "wrong_name", "protected_env", "input_host_path", "oversized_command"} {
		t.Run(name, func(t *testing.T) {
			s := d05CreateFixture(t)
			switch name {
			case "root_uid":
				s.uid = 0
			case "root_gid":
				s.gid = 0
			case "relative_stage":
				s.stagingRoot = "stage"
			case "root_stage":
				s.stagingRoot = "/"
			case "missing_limits":
				s.effective.PIDs = 0
			case "wrong_authority":
				s.effective.Request.Profile.Network = "bridge"
			case "pull":
				s.step.SnapshotSandbox.Profile.Pull = "if_missing"
			case "network":
				s.step.SnapshotSandbox.Profile.Network = "bridge"
			case "secrets":
				s.step.Secrets = []string{"SYNTHETIC"}
			case "history":
				s.step.WorkspaceRequirements = []string{"source_history"}
			case "integration":
				s.step.Isolation = workflow.IsolationWorktree
			case "foreign_platform":
				s.image.Architecture = "arm64"
			case "tag_image":
				s.image.ID = "image:tag"
			case "volumes":
				s.image.Config.Volumes = map[string]struct{}{"/hidden": {}}
			case "missing_image_config":
				s.image.Config = nil
			case "wrong_daemon":
				s.phase.DaemonID = "other"
			case "wrong_image":
				s.phase.ImageID = "sha256:" + strings.Repeat("b", 64)
			case "wrong_phase":
				s.phase.State = step.ContainerCreated
			case "wrong_name":
				s.phase.Name = "jig-other"
			case "protected_env":
				s.step.SnapshotSandbox.Profile.Env["HOME"] = "/host"
			case "oversized_command":
				s.step.Run = strings.Repeat("x", executionRequestBytes)
			case "input_host_path":
				s.step.Inputs = []workflow.Input{{Ref: "producer", As: "binary"}}
				s.inputEnv = map[string]string{"JIG_INPUT_BINARY": "/host/private"}
			}
			if _, err := buildContainerCreate(s); err == nil {
				t.Fatal("unsupported configuration accepted")
			}
		})
	}
}
func TestD05ContainerCreatePhases(t *testing.T) {
	s := d05CreateFixture(t)
	s.step.Run = ""
	s.step.Script = "frozen.sh"
	options, err := buildContainerCreate(s)
	if err != nil {
		t.Fatal(err)
	}
	if options.Config.Cmd[len(options.Config.Cmd)-1] != "/captures/command.sh" {
		t.Fatal(options.Config.Cmd)
	}
	s.phase.Phase = "validation"
	s.phase.Name = "jig-" + s.phase.Nonce + "-validation"
	options, err = buildContainerCreate(s)
	if err != nil {
		t.Fatal(err)
	}
	if options.Config.Cmd[len(options.Config.Cmd)-1] != s.step.Validate.Command {
		t.Fatal(options.Config.Cmd)
	}
	s.step.Validate = nil
	if _, err := buildContainerCreate(s); err == nil {
		t.Fatal("undeclared validator accepted")
	}
}
func TestD05ContainerCreateSDKWire(t *testing.T) {
	s := d05CreateFixture(t)
	var got container.CreateRequest
	ep, _, _ := doctorFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/info") {
			normalMetadata(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		fmt.Fprintf(w, `{"Id":%q}`, d05ContainerID)
	})
	s.binding.Endpoint = ep.Summary()
	options, err := buildContainerCreate(s)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := newExecutionClient(ep, s.binding, controlPermission{operation: "create", target: s.phase.Name})
	if err != nil {
		t.Fatal(err)
	}
	defer sdk.Close()
	if _, err := sdk.ContainerCreate(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if got.Image != s.image.ID || got.HostConfig.MemorySwap != s.effective.MemoryBytes || len(got.HostConfig.Mounts) != 4 || got.Labels["jig.execution"] != s.phase.Nonce {
		t.Fatalf("wire config lost controls: %+v", got)
	}
}

func TestD05ContainerCreateNativeARMAndInputs(t *testing.T) {
	s := d05CreateFixture(t)
	s.image.Architecture = "arm64"
	s.image.Variant = "v8"
	s.binding.NativePlatform = "linux/arm64"
	s.step.Inputs = []workflow.Input{{Ref: "producer", As: "binary"}}
	s.inputEnv = map[string]string{"JIG_INPUT_BINARY": "/inputs/001"}
	options, err := buildContainerCreate(s)
	if err != nil {
		t.Fatal(err)
	}
	if options.Platform.Architecture != "arm64" {
		t.Fatal(options.Platform)
	}
	found := false
	for _, arg := range options.Config.Cmd {
		if arg == "JIG_INPUT_BINARY=/inputs/001" {
			found = true
		}
	}
	if !found {
		t.Fatal("input binding absent")
	}
	s.inputEnv["JIG_INPUT_BINARY"] = "/changed"
	if _, err := buildContainerCreate(s); err == nil {
		t.Fatal("changed input accepted")
	}
}
