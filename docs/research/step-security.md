# Step security: OS-, container-, and cluster-level containment for jig steps

**Research date:** 2026-09-15
**Scope:** Design-space survey and integration proposal that expands on the
existing argument-level guard and out-of-band monitor fleet
([`docs/security-monitoring.md`](../security-monitoring.md),
[ADR 0009](../adr/0009-agent-security-monitoring.md),
[Spec 10](../specs/10-spec-agent-security-monitoring/10-spec-agent-security-monitoring.md)).
**Status:** research only. No spec, ADR, or engine change is implied by this
document.

## 1. Why another layer

Jig already ships two independent security layers on top of the DAG-level
containment primitives (worktree isolation, allowed-tool lists, mutation-path
allowlists):

- **Tier 1** — `sentinel.Guard` inspects tool arguments before the SDK executes
  them. It denies secret-shaped write payloads, non-allowlisted outbound hosts,
  and escalates a few dangerous shell patterns. Prevention, but only for the
  arguments the SDK exposes to the permission callback.
- **Tier 2** — an embedded classifier fleet (prompt-injection, stuck-loop,
  exfil-pattern) reads finalized transcript windows out-of-band and raises
  redacted findings. Detection, but retrospective and best-effort.

Spec 10 explicitly declares OS sandboxing and network isolation **out of
scope**:

> "Sandboxing or network isolation of agents. Containment remains worktree
> isolation + tool allowlists; this spec adds argument-level policy and
> observation."
> — `docs/specs/10-spec-agent-security-monitoring`, Non-Goal 3.

That non-goal is a load-bearing simplification for the current architecture,
not a claim that argument-level and semantic monitoring are sufficient. This
document is the survey of what a third layer — **executor containment** — would
look like, why it is worth having, and how to introduce it without breaking
jig's deterministic orchestration invariants.

### 1.1 Threat model gaps the existing tiers cannot close

| Attack shape | Tier 1 | Tier 2 | Executor containment |
|---|---|---|---|
| Secret-shaped payload in a `Write`/`Edit`/`Bash` argument | Blocked | Redacted after the fact | Cannot help; must live at argument level |
| Outbound network call the SDK routes through `WebFetch`/`Bash curl` | Blocked when allowlist configured | Flagged if the assistant then acts on external instructions | Blocked at the kernel/proxy regardless of tool name |
| Outbound network call the vendor CLI makes directly (telemetry, model API, subprocess) | Invisible | Invisible | Blocked; the only layer that can see it |
| `Bash` command that shells out to `/usr/local/bin/xyz` that reads `~/.ssh` | Not covered by the ruleset | Only visible if it later re-enters the transcript | Blocked by rootfs restriction |
| A step process writing outside `mutation_paths` via a symlink or `os.Rename` | Detected only after diff | Not detected | Blocked by mount policy |
| A CPU/memory/pids-exhausting agent turn that exceeds `max_budget_usd` slowly | `max_budget_usd` still bills tokens | `stuck-loop` may fire eventually | cgroup/rlimit kills instantly |
| A dependency (npm/pip/go/git) pulled by the step containing a supply-chain implant | Invisible | Invisible | Blocked by egress policy or ephemeral rootfs |
| A `command` step's `sh -c` that reaches an internal service on the operator's laptop | Invisible (no SDK seam) | Invisible (no transcript) | Blocked by network namespace |
| `codex-acp`/`claude-code-acp` Node adapter reading `~/.config/*` while running | Invisible | Invisible | Blocked by rootfs restriction |

The last three rows are the important ones. They correspond to the shapes of
harm jig cannot see today because they never traverse the SDK permission
callback: they are either **inside the vendor CLI**, **inside the ACP node
adapter**, or **inside a `command`/`check` step's `sh -c`**, all of which jig
launches on the operator's host machine with the operator's ambient authority.

### 1.2 Design constraints inherited from the rest of jig

Any executor containment mechanism has to compose with the invariants encoded
in [`AGENTS.md`](../../AGENTS.md), [`docs/ARCHITECTURE.md`](../ARCHITECTURE.md),
and [`docs/GRAPH_ENGINEERING.md`](../GRAPH_ENGINEERING.md):

1. **File is truth, bus is liveness.** `transcript.jsonl`, `findings.jsonl`,
   `journal.jsonl`, `session.json`, and the per-step `result.json` live under
   `.jig/runs/<run-id>/…` on the host. A sandbox may not own them; it must be
   granted a bounded surface for writes.
2. **Single owner of run state.** The scheduler is the only writer for engine
   events. Sandbox lifecycle events must reach the scheduler through the
   existing `engine.Reporter` seam.
3. **Persistence-off is supported.** A sandbox driver must have a no-op path
   when there is no run directory, or an explicit fail-closed decision.
4. **Deterministic orchestration.** Retries, routes, resets, and the reopen
   guarantee must survive sandbox teardown or restart. A sandbox is not a
   process the run cannot be restarted without.
5. **Explicit orchestration.** Any new field is authored in TOML and validated
   at load time. No environment-variable-driven mode changes.
6. **Pre-v1: correctness > compatibility.** A new mechanism supersedes the
   compatibility path; obsolete wrappers are removed.

The rest of this document treats those six items as non-negotiable.

## 2. Design space — the containment spectrum

There is no single "sandbox" primitive; there is a spectrum, roughly ordered
from cheapest and weakest to strongest and most operationally expensive. Each
band lists the mechanisms and where they sit relative to jig.

### 2.1 In-process argument policy (already shipped)

- `sentinel.Guard` (Tier 1) at `internal/sentinel/guard.go`.
- Tool allowlist / denylist in `[step.allowed_tools]` / `[step.disallowed_tools]`.
- `permission_mode = "default"` forced when the guard is active so
  `WithCanUseTool` fires for every tool.
- `mutation_paths` diff enforcement in `internal/engine/worktree.go` (post-hoc
  but pre-integration).

Coverage: SDK-mediated tool calls only. Nothing about the wider process.

### 2.2 Same-user, unprivileged process hardening

Cheap, universally available on modern Linux; more limited on macOS and Windows.

| Mechanism | Platform | What it constrains |
|---|---|---|
| POSIX rlimits (`RLIMIT_NPROC`, `RLIMIT_AS`, `RLIMIT_FSIZE`, `RLIMIT_CPU`, `RLIMIT_NOFILE`) | Cross-platform (partial on macOS) | Local fork/CPU/memory/file bombs. `syscall.Setrlimit` from `os/exec.Cmd.SysProcAttr` or a pre-exec wrapper |
| Linux cgroups v2 (`memory.max`, `pids.max`, `cpu.max`, `io.max`) | Linux ≥ 5.x | Per-step CPU/memory/pids/IO ceilings. Systemd-scoped or `--slice` when jig runs under systemd; direct `/sys/fs/cgroup` when jig owns the run |
| `prctl(PR_SET_NO_NEW_PRIVS)` + seccomp-bpf | Linux | System-call allowlist. Requires the operator to install a policy or accept a jig-provided baseline |
| Linux capabilities (drop `CAP_NET_RAW`, `CAP_SYS_PTRACE`, …) | Linux | Kernel privilege reduction (mostly relevant when jig runs setuid; not the common case) |
| **Landlock** (kernel 5.13+, path-based; kernel 6.7+ adds network) | Linux | File-tree read/write restriction (`LANDLOCK_ACCESS_FS_*`) and TCP connect/bind restriction (`LANDLOCK_ACCESS_NET_*`). Unprivileged, per-thread |
| `sandbox_init` / `sandbox-exec` (Seatbelt) | macOS | File and network restriction via a SBPL profile. Deprecated by Apple but still functional |
| AppArmor / SELinux MAC profiles | Linux distro-dependent | Mandatory access control, filesystem + network. Needs root to load profiles |
| Job Objects | Windows | Process tree lifetime, CPU/memory caps. AppContainer/HVCI is the newer path |

Suitability for jig: **excellent for `command`/`check` steps.** Both are
already `sh -c <script>` under `exec.Cmd`; a small wrapper (or a `SysProcAttr`
callback) can bracket every command with rlimits + Landlock (or sandbox-exec on
macOS). For agent steps, the vendor CLI is a foreign subprocess that
system-call-filter allowlists cannot be authored blind; a Landlock rootfs
restriction is safer because it deals in paths, not syscalls.

### 2.3 Namespaced OS-level sandboxes (Linux)

Zero-daemon, no image pull, standard on many distros. Each spawns a process
inside a set of Linux namespaces (mount, PID, net, UTS, IPC, user, cgroup) with
a curated rootfs and network posture.

| Tool | Provenance | Strength | Fit for jig |
|---|---|---|---|
| [`bubblewrap`](https://github.com/containers/bubblewrap) (`bwrap`) | Container-native, used by Flatpak | Small, focused; the reference unprivileged sandbox | `bwrap --ro-bind /usr /usr --bind $WORKTREE /w --dev /dev --tmpfs /tmp --unshare-net --die-with-parent -- sh -c "$RUN"` |
| [`nsjail`](https://github.com/google/nsjail) | Google | Higher-level, well-suited for one-shot subprocess isolation with time/CPU/memory caps and seccomp | `nsjail --config nsjail.cfg -- sh -c "$RUN"` |
| [`firejail`](https://firejail.wordpress.com) | SUID-based, widely packaged | Rich profile language; SUID surface makes it less attractive for CI | Not preferred in modern setups |
| `systemd-nspawn` | Systemd | A "light container" with an OS tree; heavy for step-scoped work | Better for long-lived dev-shell isolation than per-step |
| `unshare(1)` + custom mounts | util-linux | The primitive underneath everything above | Building block, not a driver |

Suitability for jig: **the best Linux-native default** for command/check steps
and — with a helper that mounts the ACP adapter's `node` binary read-only — for
ACP agent steps. Bubblewrap in particular is the primitive that gVisor uses
under the covers.

### 2.4 OCI containers

The middle of the spectrum. Rich tooling, portable images, mature network and
volume story; strongest supply-chain leverage (pull a specific pinned digest,
scanned, signed).

| Runtime | Rootless? | Notes |
|---|---|---|
| Docker Engine (dockerd + runc) | Yes on Linux (rootless mode); Docker Desktop virtualizes on macOS/Windows | Universally available in developer environments |
| Podman | Yes, first-class rootless with `crun` | Daemonless; better fit for CLI-driven, per-step invocations |
| containerd + `nerdctl` | Yes | Closer to Kubernetes internals |
| Colima / Lima / OrbStack | Docker-API compatible on macOS via a Linux VM | The pragmatic path on Apple Silicon |
| Docker Desktop | Same, on any host | Simplest onboarding, hardest to reason about egress |

Suitability for jig: **the primary MVP driver.** OCI has a well-defined
lifecycle (`create` → `start` → `exec` → `wait` → `rm`), a well-defined stdio
protocol (attach/exec), and a well-defined way to pipe environment, volumes,
and network policy in. Every jig user with a Node backend (Cursor ACP, Codex
ACP, or Claude ACP through the Zed adapter) already runs `npx`, which is a
strict superset of what an OCI runtime asks for.

### 2.5 Kernel- and hardware-isolated runtimes

When a shared kernel is not enough (multi-tenant workflows, agents run against
untrusted repos, or workflows that fetch third-party binaries).

| Runtime | Mechanism | Trade-offs |
|---|---|---|
| [gVisor / `runsc`](https://gvisor.dev) | Userspace kernel intercepting syscalls | Strong isolation; syscall compatibility gaps (some Node/Go binaries misbehave). Runs as a Docker/K8s `RuntimeClass` |
| [Kata Containers](https://katacontainers.io) | Lightweight per-container VM (Firecracker or QEMU) | Container UX, VM boundary. Higher startup latency (hundreds of ms) |
| [Firecracker](https://firecracker-microvm.github.io) | MicroVM under KVM | Direct API, sub-second boot, minimal device set. Requires KVM (Linux/EC2/Fly bare-metal) |
| [Cloud Hypervisor](https://www.cloudhypervisor.org) | MicroVM under KVM | Similar to Firecracker, richer device model |
| QEMU/KVM | Full VM | The mainstream escape hatch when nothing else is trusted |
| WSL2 | Hyper-V lightweight VM | On Windows, hosts Linux for jig; not per-step isolation on its own |
| Apple Virtualization.framework, [Apple Container](https://developer.apple.com/apple-silicon/) | Apple-signed VMs on macOS | Native macOS path; still bootstrapping tooling |
| Windows Sandbox / Windows Containers (Hyper-V isolation) | Hyper-V | Windows-native strong isolation |

Suitability for jig: **an opt-in stronger tier**, especially for CI or shared
runners. Kata Containers is the easiest onramp because it slots into the same
OCI/K8s surface as gVisor.

### 2.6 WebAssembly

Notable because it inverts the model: rather than isolate a whole process, it
runs the step body as a WASM module under an allowlist-based host API (WASI).

| Runtime | Notes |
|---|---|
| [Wasmtime](https://wasmtime.dev), [wasmer](https://wasmer.io) | WASI 0.2 preview 2 covers filesystem, sockets, HTTP |
| [Spin](https://developer.fermyon.com/spin) / [wasmCloud](https://wasmcloud.com) | Higher-level frameworks |
| [WasmEdge](https://wasmedge.org) | Container-shaped WASM runtime |

Suitability for jig: **not for agent steps** (the vendor CLIs are not WASM).
Interesting future path for authoring reusable, portable `check` steps whose
capabilities are declared, not sandboxed after the fact.

### 2.7 Cluster / remote runtimes

The endgame: a jig control loop that dispatches steps as ephemeral pods,
machines, or Firecracker instances.

| Substrate | Notes |
|---|---|
| Kubernetes (Job or ephemeral Pod per step) | Uses `RuntimeClass` to switch to gVisor or Kata; `NetworkPolicy` for egress; `PodSecurityStandards` for baseline |
| K3s / kind / minikube | Local single-node Kubernetes for jig operators who want the same lifecycle as CI |
| Fly.io Machines / Fly Kubernetes | Firecracker-per-step, boot time under a second |
| Nomad + Firecracker driver | Same idea, without the K8s dependency |
| Nix build sandbox | Path-restricted, network-off subprocess with content-addressed store |
| GitHub Actions Larger Runners / Codespaces | External CI/dev-container path already familiar to operators |

Suitability for jig: **the "single-machine only"** limitation in
[`docs/plans/open-goals.md`](../plans/open-goals.md) A19 makes clustering a
follow-on rather than an MVP. But the architecture proposed below is designed
so a Kubernetes driver is a peer of the Docker driver, not a rewrite.

### 2.8 Network isolation, independently

Because network is where secrets leave and where attacker code arrives, it
deserves its own inventory:

| Primitive | Layer | Notes |
|---|---|---|
| `outbound_allowlist` in `sentinel.Guard` | SDK-argument, in-process | Ships today. Bypassed by anything not routed through `WebFetch`/`Bash curl` |
| Linux network namespace, `--network=none` | Kernel | Strongest local option — no connectivity at all |
| iptables/nftables egress filter | Kernel | Host-wide, hard to make per-step |
| Cilium / eBPF (`CiliumNetworkPolicy`) | K8s | Per-pod DNS+L7 policy |
| Kubernetes `NetworkPolicy` | K8s | Namespace-level L3/L4 |
| HTTP CONNECT proxy (Squid, `mitmproxy`, `sso-proxy`) | Userspace | The universal cross-platform path; jig sets `HTTP_PROXY`/`HTTPS_PROXY` and enforces via proxy ACL |
| Envoy / Istio egress gateway | Sidecar | Rich but heavy; for K8s |
| Landlock TCP restrictions (Linux 6.7+) | Kernel, unprivileged | Emerging; not yet universal |
| `pf` (Packet Filter) rules on macOS | Kernel | Practical if operator is admin |

Suitability for jig: **run a proxy driven by `outbound_allowlist`** is the
most portable design. It composes with Docker (`--network` + proxy env), K8s
(egress gateway or NetworkPolicy), and bare processes (`HTTP(S)_PROXY` env).
It is also observable — proxy logs are a clean audit trail.

## 3. Per-step-kind analysis

Not every containment mechanism is a fit for every jig step kind. This section
maps the step-kind × mechanism matrix.

### 3.1 `agent` steps

An agent step spawns a vendor CLI (Claude via SDK, Claude ACP through Zed's
`npx` adapter, Cursor via `cursor-agent acp`, Codex via
`@agentclientprotocol/codex-acp`). See
[`AGENTS.md` Backend selection](../../AGENTS.md#backend-selection).

Three options for where the sandbox boundary sits:

1. **Sandbox only the tool subprocesses**
   The vendor CLI runs on the host; jig intercepts `Bash`/`Edit`/`Write` and
   re-executes them inside a sandbox. Rejected: the CLI still has full ambient
   authority for its own network/model calls, and the interception seam is not
   universal (the SDK exposes tool arguments; the ACP transports rewrite them
   through varied schemas).

2. **Sandbox the whole vendor process** (recommended for a driver)
   `jig` launches the whole vendor CLI or ACP adapter inside the sandbox, with
   a bind-mount for the worktree and a stdio bridge back to jig. This is the
   only shape that treats the vendor CLI as untrusted. Cursor ACP, Codex ACP,
   and Claude ACP all use stdio, which is easy to relay through
   `docker exec -i`, `kubectl exec -i`, `firecracker vsock`, or `bwrap`.

3. **Colocate jig inside the sandbox**
   Ship the whole jig process into a container/VM. Rejected as the default: the
   operator loses the TUI, the on-disk `.jig/` under their repo, and the local
   Git worktree lifecycle — none of which the sandbox has any reason to see.

Option 2 requires the harness layer to know it is talking to a sandboxed
process. Concretely: the `harness.For(backend, transport)` call today
constructs a Session that eventually invokes `exec.Command(...)`. Introducing
a **sandbox driver** in front of that `exec.Command` is a small refactor
(section 4).

The Claude *SDK* transport is different: the SDK is a Go library, not a
subprocess. Sandboxing it would require sandboxing the entire jig process. The
practical implication is that **strong containment for Claude requires
`transport = "acp"`**, so the vendor process is external. This is worth stating
in TOML so operators do not misread the guarantee.

### 3.2 `command` and `check` steps

Both today are `sh -c <script>` via `os/exec` in the step's execution snapshot
(worktree or execution view). They are the easiest to sandbox because jig owns
the entire command line.

Recommended defaults, in order of aggressiveness:

- **Baseline (all platforms):** enforce a per-step deadline, `RLIMIT_AS`,
  `RLIMIT_NPROC`, `RLIMIT_FSIZE`, and a bounded output byte cap. All achievable
  today with a `SysProcAttr` extension inside `CommandExecutor`. No opt-in
  needed.
- **Linux:** wrap with `bwrap` when configured, exposing only the execution
  directory + `.jig/runs/<id>/steps/<id>/`. Deny network unless the step
  declares `allowed_outbound = "on"` (or the workflow declares
  `outbound_allowlist`).
- **macOS:** wrap with `sandbox-exec` and an SBPL profile derived from
  `mutation_paths` and `outbound_allowlist`.
- **Container driver:** run the command inside `docker run --rm
  --network=<policy> -v $ExecutionDir:/w -w /w <image> sh -c "$RUN"`.
- **K8s driver:** create a per-step Job whose container has `securityContext:
  { runAsNonRoot: true, readOnlyRootFilesystem: true }`, `volumeMounts` for
  the execution dir, and a `NetworkPolicy` referencing `outbound_allowlist`.

### 3.3 `review` steps

Review steps do not execute anything untrusted; they render evidence for a
human. No sandbox layer is required beyond what the TUI already does. Any
future proxy for markdown rendering (e.g. remote images) should route through
the same proxy the run uses, but that is a rendering concern.

### 3.4 `subworkflow` steps

Sub-workflow steps expand to their contained steps before dispatch; the sandbox
policy is a property of the contained steps. The `[defaults.sandbox]` block at
root inherits into modules exactly as `[defaults.security]` does today.

### 3.5 Foreach children

`[step.foreach]` families produce runtime children that are ordinary steps
(section "Runtime identity and item delivery" in
[`docs/workflow-schema.md`](../workflow-schema.md)). Every containment field is
inherited from the template. A driver that pools sandbox instances should key
its pool on template ID + item digest so retries reuse an unchanged instance.

## 4. Proposed architecture

The design goal: introduce **executor containment as an optional driver
layer** while preserving the current Executor/Reporter contract so the engine
package remains free of `os/exec`, image pulls, and any container SDK.

### 4.1 A new `internal/sandbox` package

Peer of `internal/harness`:

```text
internal/sandbox/
    driver.go          # Driver interface + PolicyDecision types
    select.go          # For(driver, spec) → Driver, mirroring harness.For
    inprocess.go       # Baseline hardening: rlimits + syscall.SysProcAttr
    bubblewrap.go      # bwrap wrapper, Linux
    sandboxexec.go     # sandbox-exec wrapper, macOS
    docker.go          # OCI wrapper, dockerd or podman socket
    kubernetes.go      # k8s Job driver
    nsjail.go, firecracker.go, gvisor.go   # future
    policy.go          # normalized Policy struct
```

### 4.2 The `Driver` interface

The engine already inverts on `engine.Executor` (see
[ADR 0003](../adr/0003-extensibility-lives-in-engine-and-schema.md)). A sandbox
sits between the executor and the outside process. Sketch:

```go
type Driver interface {
    Name() string
    Capabilities() Capabilities
    // Prepare creates the isolated environment (namespace, container, pod)
    // and returns a Handle. Idempotent per attempt.
    Prepare(ctx context.Context, spec Spec) (Handle, error)
}

type Handle interface {
    // Exec runs one process inside the sandbox and streams its stdio.
    Exec(ctx context.Context, cmd Command, io Streams) (ExitStatus, error)
    // Close tears the sandbox down. Idempotent, safe for defer.
    Close() error
}

type Spec struct {
    ExecutionDir string        // host path bind-mounted read-write
    ReadOnly     []string      // host paths bind-mounted read-only
    Tmpfs        []string      // in-sandbox tmpfs paths
    Env          []string      // sanitized env; secrets injected via Secrets
    Secrets      map[string]string
    Network      NetworkPolicy // none | loopback | proxy | allowlist | host
    Resources    Resources     // cpu, memory, pids, tmpfs bytes, wall clock
    Image        string        // "" for driver default (host tools) or an OCI ref
    Entrypoint   []string      // "" for driver default (sh -c)
    RunID, StepID, Attempt string
}
```

Every field maps one-to-one to a TOML surface (section 5). The driver is free
to reject a spec it cannot satisfy (e.g. Bubblewrap has no image; Docker has no
Landlock rules), which surfaces as a load-time error rather than a runtime
degrade.

### 4.3 Where the driver plugs into the runner

Today's flow (simplified):

```text
engine.Run  →  runner.AgentExecutor.Execute       (or CommandExecutor.Execute)
                       ├── harness.For(...).Open()  (agent)
                       └── exec.CommandContext(...) (command/check)
```

Proposed:

```text
engine.Run  →  runner.AgentExecutor.Execute
                       ├── sandbox.For(policy).Prepare(spec)
                       ├── harness.For(...).OpenIn(handle, spec)   [new capability]
                       └── captureStream(handle.Messages())

               runner.CommandExecutor.Execute
                       ├── sandbox.For(policy).Prepare(spec)
                       └── handle.Exec(...)
```

`harness.OpenIn` is the seam that lets a harness reach a subprocess through the
sandbox handle instead of `exec.Command`. For SDK-transport Claude, `OpenIn`
either (a) refuses when the sandbox policy requires kernel-level containment,
or (b) delegates to a wrapper that spawns the SDK inside a subprocess of jig's
choosing. Option (a) is the safer default; option (b) is a follow-on.

### 4.4 What has to change in the engine

Little. The engine already carries the containment vocabulary it needs:

- `StepRequest.Guard` and `StepRequest.Secrets` are the same shape.
- `Reporter.Finding` already routes an `Action = "escalated"` finding to the
  recovery gate.
- The journal registry already has room for one more event kind — a
  `SandboxLifecycle{Op, State, ExitCode}` event on the **ctrl** channel would
  give the TUI a first-class surface for "container started", "sandbox
  denied", "sandbox killed for OOM", …

The journal exhaustiveness guard added in Spec 10 Unit 1 keeps this honest.

### 4.5 Interaction with existing invariants

- **Worktrees.** The mutating worktree is bind-mounted read-write; read-only
  execution views are bind-mounted read-only. The `mutation_paths` diff check
  runs on the host after the sandbox exits — unchanged. Symlink escapes from
  inside the sandbox cannot reach host paths outside the mount.
- **Reset / reopen.** A sandbox handle is scoped to one attempt. Reset destroys
  the handle. Reopen re-`Prepare`s. This lines up with the "no per-step mutable
  state" property.
- **Persistence-off.** Sandbox driver `none` (the default) makes every path a
  no-op, so the persistence-off harness/runner suites are unaffected.
- **Cost accounting.** Container image pulls and VM boot are not charged
  against `max_budget_usd` (which is a per-agent model spend) but are worth
  surfacing on the Monitor's step-detail row (see
  [`docs/observability.md`](../observability.md)).
- **Findings.** A sandbox-policy denial (e.g. seccomp kill, egress rejection)
  is a `Finding{Tier: "sandbox", Action: "blocked"|"escalated"}` on the same
  `findings.jsonl` path. This means the existing pane and recovery gate keep
  working with no TUI-side change beyond a new label.

## 5. Configuration surface (TOML proposal)

The additive proposal mirrors `[defaults.security]` / `[step.security]`.

### 5.1 Root defaults

```toml
[defaults.sandbox]
driver = "docker"        # none | inprocess | bubblewrap | sandbox-exec | nsjail
                         # docker | podman | gvisor | kata | firecracker | k8s
image  = "ghcr.io/liatrio/jig-runner:0.1@sha256:…"  # only for OCI/K8s drivers
network = "proxy"        # none | loopback | proxy | allowlist | host
cpu     = "2"
memory  = "2Gi"
pids    = 512
tmpfs   = "512Mi"
walltime = "20m"
readonly_rootfs = true
drop_caps = ["ALL"]
add_caps  = []
seccomp   = "runtime/default"   # or a profile path
apparmor  = "runtime/default"
proxy_env = ["HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"]

  [defaults.sandbox.mounts]
  execution_dir = "rw"       # rw | ro
  jig_state     = "hidden"   # hidden | ro   (never rw)
  home          = "hidden"   # hidden | ro-selective (SSH agent socket only)

  [defaults.sandbox.egress]
  hosts = ["api.anthropic.com", "api.openai.com", "codeium.dev"]
  # inherits [defaults.security].outbound_allowlist when not set
```

Notes:

- `driver = "none"` is the default so existing workflows keep running exactly
  as before.
- `driver = "inprocess"` is the minimum-viable driver: `os/exec` with rlimits
  and Landlock/sandbox-exec applied. No image, no daemon. Portable.
- `driver = "docker"` supersedes `"podman"` at the API level (they share the
  Docker Engine API); `"podman"` is a hint at socket path selection.
- `[defaults.sandbox.egress]` inherits from `[defaults.security]` when
  unspecified, so the Tier-1 allowlist and the sandbox proxy share one source
  of truth.
- The Docker/K8s images are pinned by digest, not tag, aligning with the
  supply-chain posture jig already uses for module locking.

### 5.2 Per-step overrides

```toml
[[step]]
id    = "publish"
type  = "command"
run   = "./scripts/publish.sh"
secrets = ["release_token"]

  [step.sandbox]
  driver  = "docker"
  image   = "ghcr.io/liatrio/publish@sha256:…"
  network = "allowlist"
  cpu     = "1"
  memory  = "1Gi"
  walltime = "5m"

    [step.sandbox.egress]
    hosts = ["github.com", "api.github.com", "objects.githubusercontent.com"]

    [step.sandbox.mounts]
    execution_dir = "rw"
```

Inheritance is the same zero-value precedence rule the security block uses:
step → defaults → engine baseline. A step may narrow but never widen a policy
supplied by defaults (e.g. an operator cannot re-enable network on a step when
defaults say `network = "none"`; that must be an explicit workflow-wide
opt-in, mirroring how `[defaults.security]` is authoritative).

### 5.3 Interaction with `[defaults.security]`

`[defaults.security]` stays the argument-level and monitor-fleet policy. The
egress allowlist and secret-detection rules live there. `[defaults.sandbox]`
inherits `outbound_allowlist` when its own `[defaults.sandbox.egress]` is
unset, so operators can start with one list and split it later.

### 5.4 Validation at load time

Per the "fail at load time" rule in [`AGENTS.md`](../../AGENTS.md), the loader
validates:

- Driver names against `sandbox.For` known set.
- Every `image` reference by digest, not tag, when the driver is OCI-shaped.
- CPU/memory/walltime as Go durations / IEC sizes.
- Every hostname entry as a valid DNS name; `.example.com` and exact host both
  legal.
- An explicitly inherited incompatible pair (e.g. Bubblewrap + `image`) fails
  at load, not run. Mirrors backend/transport rejection.
- `permission_mode = "acceptEdits"` and `[step.security].tier1_enabled = true`
  and a driver that supports permission callbacks stay compatible; a driver
  that cannot forward the SDK callback (e.g. running the CLI in a VM with a
  separate stdio bridge) must fail-closed on Tier-1 policies that depend on
  arguments.

## 6. Local implementations

This section is the practical "how do I run jig with `driver = X` on my
laptop" table.

### 6.1 Baseline (`driver = "inprocess"`)

- Uses only stdlib (`syscall.Setrlimit`, `unix.LandlockRulesetCreate` on Linux,
  `sandbox-exec` on macOS).
- No installation. Ships with jig.
- Wraps `command` and `check` steps. Agent steps run unchanged (no vendor CLI
  containment).
- Suitable for the operator who wants "belt on top of Tier 1" without a
  container runtime.

### 6.2 Bubblewrap (`driver = "bubblewrap"`)

- Requires `bwrap` in `$PATH`. Trivial to install on Ubuntu/Debian
  (`apt install bubblewrap`), Fedora (`dnf install bubblewrap`), or via
  Homebrew Linux.
- Zero daemon. Every step is a fresh namespace.
- Agent steps: bind-mount the ACP adapter's `node` executable plus the vendor
  CLI (`claude`, `cursor-agent`, `codex`) read-only into the sandbox. Bind
  `$ExecutionDir` read-write. Everything else denied.
- Network: `--unshare-net` unless `network = "proxy"`, in which case the
  network namespace is joined to a shared proxy namespace jig runs on start.
- Failure modes: bubblewrap has no Windows or macOS support; jig should refuse
  to select this driver on those platforms.

### 6.3 sandbox-exec (`driver = "sandbox-exec"`)

- macOS built-in. Deprecated by Apple but still functional through Sonoma.
- Uses an SBPL profile jig assembles from the sandbox spec.
- Coverage similar to bubblewrap: filesystem paths, network hosts, subprocess
  execution.
- Not on iOS-family sandboxes. Not on Apple Silicon Virtualization framework's
  guest OS.

### 6.4 Docker/Podman (`driver = "docker"`)

- Requires a running Docker Engine or Podman socket.
- The MVP driver. Portable across all three OS families through Docker Desktop
  or Colima/Lima on macOS, Docker Desktop or WSL2 Docker on Windows, native
  Podman/Docker on Linux.
- One base image per backend (`jig-runner:node20`, `jig-runner:python`, …), or
  operator-supplied images. A `[defaults.sandbox.image]` root default is the
  common case.
- Volume policy: `$ExecutionDir` (or the whole worktree) bind-mounted at
  `/workspace`; `.jig/runs/<id>/steps/<id>/transcript.jsonl` bind-mounted at
  `/jig/transcript.jsonl` in append-only mode. Everything else in the container
  is ephemeral.
- Network policy:
  - `network = "none"` → `--network=none`.
  - `network = "loopback"` → `--network=none` with a shared UNIX socket for
    the ACP stdio bridge.
  - `network = "proxy"` → the container joins a `jig-egress` network on which
    a squid/tinyproxy sidecar enforces `outbound_allowlist`.
  - `network = "host"` → `--network=host`. Reserved for opt-in debugging.
- Rollout risk: **the container image is the largest supply-chain surface a
  jig operator touches.** Every default image needs a Cosign signature and a
  pinned digest. The runner refuses images referenced by tag when the driver
  is `docker`.

### 6.5 gVisor / Kata (`driver = "gvisor"` / `"kata"`)

Same interface as Docker; the driver just adds `--runtime=runsc` or
`--runtime=kata` when spawning the container. Requires the runtime to be
installed and registered with the Docker/Podman daemon. jig probes for it at
`doctor` time.

### 6.6 Firecracker (`driver = "firecracker"`)

- Requires KVM. Not available on macOS/Windows outside nested virtualization.
- Boot latency (~125 ms) is compatible with jig's per-step model but not
  free.
- The driver exposes vsock as the stdio bridge to the guest. Guest image is
  a minimal Alpine/Debian rootfs with `node` and the vendor CLIs.
- Best fit: a self-hosted CI runner or a jig-in-cloud deployment.

### 6.7 Kubernetes (`driver = "k8s"`)

- The driver expects a `kubeconfig` and a namespace. It creates a Job per step
  attempt, streams logs through `kubectl exec` (or the client-go equivalent),
  and cleans up the Job when the attempt terminates.
- `RuntimeClass` selects gVisor/Kata when present.
- `NetworkPolicy` for egress; the `jig` cluster ideally has a Cilium ClusterRole
  that translates `outbound_allowlist` into a `CiliumNetworkPolicy`.
- `PodSecurityStandards: restricted` at namespace level. The Job template
  refuses `runAsRoot`, `hostPath`, and elevated capabilities.
- `Secret` objects hold the same names as `secrets = […]` in the workflow;
  jig maps `JIG_SECRET_<NAME>` from a projected Secret. **Secrets never live
  in a workflow file.**

## 7. How jig manages sandbox lifecycle

The engine's rules for step lifecycle transfer to sandbox handles:

1. **Prepare before dispatch.** The scheduler calls `sandbox.For(policy)
   .Prepare(spec)` synchronously in the same worker goroutine that runs
   `Executor.Execute`. If preparation fails, the step becomes `failed` and
   routes through the recovery gate with a typed error (`ErrSandboxPrepare`,
   `ErrImagePull`, `ErrPolicyRejected`, …).
2. **One handle per attempt.** A retry (from `[step.retry]`) tears down the
   old handle and prepares a new one. Idempotent = true requirements survive
   because the handle carries no memory beyond the attempt.
3. **Stop cancels through the handle.** Today's `Run.Stop` cancels the
   context passed to `Executor.Execute`. The driver's `Exec` must honor that
   context so `Stop` → sandbox teardown → step `stopped`. Kill semantics:
   docker signals SIGTERM, waits `stop_grace_period`, then SIGKILL. K8s Jobs
   use `terminationGracePeriodSeconds`. Bubblewrap and Firecracker are
   process/VM kills.
4. **Reopen after crash.** Sandbox handles are not durable; a crashed jig
   process cannot reattach to a Docker `docker exec` stream. The reopen
   path treats a step whose sandbox is gone the same way it treats a step
   whose SDK session is gone: escalate to the recovery gate with
   `Recover(retry|resume|abort)`. Session resume falls back to fresh dispatch
   when the harness cannot reattach.
5. **Journal events.** A minimal event set:
   ```
   SandboxLifecycle{Op: "prepare"|"exec"|"close", State: "ok"|"denied"|"killed", Detail}
   ```
   Rides ctrl. Adds one member to the journal exhaustiveness registry
   ([Spec 10 Unit 1](../specs/10-spec-agent-security-monitoring/10-spec-agent-security-monitoring.md)).
6. **TUI surface.** The Monitor's step-detail pane gains a small "Sandbox"
   row: driver, image (or "none"), state, resource usage. On denial, the row
   links to the associated Finding.
7. **Ops CLI (`jig doctor`).** Probes the selected driver: is Docker daemon
   reachable? Is `bwrap` installed? Are the required cgroup controllers
   enabled? Does the selected `RuntimeClass` exist in the cluster? Aligns
   with the existing checks in [`docs/operations.md`](../operations.md).

## 8. Secrets, egress, and the credential broker question

Sandboxing changes how secrets reach a step.

- **Today.** `secrets = ["release_token"]` becomes `JIG_SECRET_RELEASE_TOKEN`
  in the environment of a `command` step, populated from an operator-supplied
  resolver (`internal/secrets` — env-only today, per open goal A10).
- **Under a sandbox driver.** The driver receives `spec.Secrets` and mounts
  each value using the sandbox's native mechanism: Docker `--env`, K8s
  projected `Secret` at `/run/secrets/<name>`, bubblewrap `--setenv`,
  Firecracker guest agent RPC.
- **Never** write a secret to a bind-mounted host path a container can `readdir`.
  Follow the "value is never in a workflow or run snapshot" rule.
- **Broker future.** Open goal A10 (secrets beyond env — Vault / 1Password / age)
  is an orthogonal upgrade. It lands in `internal/secrets`; the sandbox driver
  is a consumer.
- **Egress proxy as a broker.** The proxy jig runs for `network = "proxy"` is
  the natural place to add per-host authentication (e.g. inject a GitHub token
  only for `api.github.com`) without the step process seeing the token at all.
  Follow-on work, not MVP.

## 9. Testing strategy

Test surfaces to add, aligned with [`docs/TESTING.md`](../TESTING.md):

- **Unit — `internal/sandbox`.** A `FakeDriver` that records `Prepare`/`Exec`/
  `Close` calls, plus per-driver unit tests for the argv/argfile the wrapper
  builds. Table-driven Policy → command-line mapping.
- **Integration — command driver.** Tests requiring `docker` are gated by an
  env var (`JIG_SANDBOX_DOCKER=1`) and use a distroless image checked in as a
  digest.
- **Integration — agent driver.** ACP adapters run inside the container; the
  existing ACP integration tests are re-run with `driver = "docker"` when the
  daemon is available.
- **Regression — persistence-off.** With no run dir, all sandbox paths are
  no-ops. Guards: same shape as the persistence-off tests for the finding
  sink.
- **Journal exhaustiveness.** New `SandboxLifecycle` variant added to the
  registry with round-trip coverage.
- **Doctor.** `jig doctor` grows a per-driver readiness probe.
- **Kubernetes.** A `kind`- or `k3s`-based test, gated on
  `JIG_SANDBOX_K8S=1`, that stands up a namespace, applies a `NetworkPolicy`,
  and runs a small command step. Not in the default CI matrix; opt-in.

## 10. Rollout plan

A conservative sequencing that keeps every intermediate state shippable:

1. **`inprocess` baseline.** Ship `sandbox.Driver` interface plus the
   `inprocess` driver that adds rlimits + Landlock/sandbox-exec to
   `CommandExecutor`. TOML: `driver` only.
2. **Bubblewrap driver.** Wraps `command`/`check` steps in a namespace. TOML:
   `network`, `mounts`. Documented Linux-only.
3. **Docker driver, command/check only.** Adds `image`, `resources`,
   `readonly_rootfs`. Egress via proxy sidecar. Documented as the recommended
   default for shared runners.
4. **Docker driver, agent-ACP transports.** Extends `harness.OpenIn` for ACP
   backends. Claude SDK transport stays out-of-sandbox and documented as such.
5. **Kubernetes driver.** Minimal Job-per-step. Alignment with the
   observability plan A18 makes span/label export a first-class feature here.
6. **gVisor/Kata RuntimeClass hints.** Same driver as Docker/K8s, one more
   config knob.
7. **Firecracker.** Self-hosted / cloud runners. Requires KVM.

Every step is independently useful. Every step is off by default.

## 11. What this document does not decide

- The specific image supply chain (base image, signing policy, update
  cadence). That belongs in a separate proposal alongside operations.
- The specific egress-proxy implementation (Squid vs tinyproxy vs a Go
  implementation embedded in jig). Depends on the licensing and portability
  requirements of the release.
- Multi-operator / shared jig cluster deployments (open goal A23). The design
  above supports one operator per jig process; scaling to a team-shared jig
  service is orthogonal.
- macOS-native VM drivers (Apple Virtualization.framework, Apple Container).
  Tooling is still maturing; the Docker Desktop / Colima path covers macOS in
  the meantime.
- Whether the argument-level guard should ever move *into* the sandbox
  (belt-and-suspenders). The security-monitoring ADR is clear that Tier 1 is
  in-process; sandboxing does not change that. A sandbox denial is a distinct,
  additive finding.

## 12. Recommendations

1. **Adopt the `Driver` abstraction now, ship `inprocess` first.** Even
   without images or a container runtime, rlimits + Landlock/sandbox-exec
   close the gap for the majority of `command`/`check` workflows. It is the
   smallest change that gets jig off the "everything runs with operator
   ambient authority" default.
2. **Make `driver = "docker"` the recommended default for shared runners and
   CI**, with a signed base image and an egress proxy. This is where the
   biggest threat-model reduction lives: it closes the vendor CLI blind spot,
   the ACP adapter blind spot, and the `sh -c` blind spot in one move.
3. **Split the network-egress mechanism from the driver.** A proxy sidecar
   works whether the driver is Docker, Kubernetes, Bubblewrap-with-shared-netns,
   or bare inprocess. Reuse `outbound_allowlist` as the single source of
   truth.
4. **Fail closed on incompatible pairs at load time.** Bubblewrap + `image`,
   `inprocess` + `readonly_rootfs`, Claude SDK transport + a driver that
   cannot host the SDK — all rejected by `jig validate`.
5. **Treat sandbox denials as `sentinel.Finding{Tier: "sandbox"}`.** Reuse
   the recovery gate, the pane, the fingerprint dedup, the redaction path.
   No new severity axis; no new "kill the run" mode. Consistent with
   [ADR 0009](../adr/0009-agent-security-monitoring.md).
6. **Do not sandbox Claude SDK transport at first.** Document the guarantee
   explicitly: strong containment requires `transport = "acp"`. Consider
   moving to a subprocess model for the SDK later.
7. **Fold `outbound_allowlist` and `[defaults.sandbox.egress].hosts` into
   one resolved list** at load time so operators cannot desync them.

## Appendix A — Cross-platform driver matrix

| Driver | Linux | macOS | Windows | Notes |
|---|---|---|---|---|
| `inprocess` | rlimits + Landlock | rlimits + sandbox-exec | Job Objects | Ships in-binary |
| `bubblewrap` | Yes | No | No | Requires `bwrap` |
| `sandbox-exec` | No | Yes | No | Requires `sandbox-exec`; Apple-deprecated |
| `docker` | Native | Docker Desktop / Colima | Docker Desktop / WSL2 | Universal but daemon-dependent |
| `podman` | Native, rootless | Podman machine | Podman Desktop | No daemon; per-invocation |
| `gvisor` | Requires `runsc` runtime | No (needs Linux kernel) | No | Docker/K8s RuntimeClass |
| `kata` | Requires Kata runtime | No | No | Docker/K8s RuntimeClass |
| `firecracker` | KVM required | No (native) | No | Also drives Fly/EC2 |
| `k8s` | kubectl/kubeconfig | kubectl/kubeconfig | kubectl/kubeconfig | Local kind/k3s; cloud clusters |

## Appendix B — Suggested `jig doctor` probes

For each driver the operator selects:

| Driver | Probes |
|---|---|
| `inprocess` | Landlock ABI version (Linux); `sandbox-exec` in `$PATH` (macOS); rlimit availability |
| `bubblewrap` | `bwrap --version`; `/proc/self/uid_map` writeable (user namespaces); `newuidmap` present |
| `docker` / `podman` | Socket reachable; API version; base image digest resolvable; `--network=none` supported |
| `gvisor` / `kata` | Runtime registered with daemon; kernel version compatible |
| `firecracker` | `/dev/kvm` present and readable; jailer binary present |
| `k8s` | Kubeconfig valid; namespace exists; permissions for `create job`, `pods/log`, `pods/exec`; RuntimeClass present when requested |

## Appendix C — Worked example: `command` step under Docker

Given the TOML:

```toml
[defaults.sandbox]
driver = "docker"
image  = "ghcr.io/liatrio/jig-runner-node20@sha256:abc…"
network = "proxy"
cpu     = "2"
memory  = "2Gi"
walltime = "10m"
readonly_rootfs = true

  [defaults.sandbox.egress]
  hosts = ["registry.npmjs.org", "github.com"]

[[step]]
id = "audit"
type = "command"
run = "npm ci && npm audit --json > audit.json"
output = "audit.json"
```

The Docker driver executes (elided flags shown for clarity):

```bash
docker run --rm \
  --name jig-<run-id>-<step-id>-<attempt> \
  --network jig-egress-<run-id> \
  --cpus 2 --memory 2g --pids-limit 512 \
  --read-only --tmpfs /tmp:size=512m \
  --cap-drop ALL --security-opt no-new-privileges \
  --user 1000:1000 \
  -v /repo/.jig/runs/<run-id>/executions/<step-id>:/workspace:rw \
  -v /repo/.jig/runs/<run-id>/steps/<step-id>/transcript.jsonl:/jig/transcript.jsonl:rw \
  -w /workspace \
  -e HTTP_PROXY=http://jig-egress-<run-id>:3128 \
  -e HTTPS_PROXY=http://jig-egress-<run-id>:3128 \
  -e JIG_INPUT_… \
  -e JIG_SECRET_… \
  ghcr.io/liatrio/jig-runner-node20@sha256:abc… \
  sh -c 'npm ci && npm audit --json > audit.json'
```

Egress: the `jig-egress-<run-id>` network runs a tinyproxy container whose
`Allow` list is exactly `registry.npmjs.org` and `github.com`. A step trying
to reach anywhere else logs a proxy `403` and the driver emits a
`sentinel.Finding{Tier: "sandbox", Monitor: "egress-denied"}`.

Result: the `npm ci` step succeeds, `audit.json` lands in the execution
directory via the bind mount, and jig's normal integration flow picks it up.
No secrets left the container. No filesystem outside `/workspace` was
touched. The step used at most 2 CPUs and 2 GiB of memory. All of the above
is visible to the operator on the Monitor and durable in the journal.

---

## References

- [`docs/security-monitoring.md`](../security-monitoring.md) — the current two
  tier layer.
- [ADR 0009](../adr/0009-agent-security-monitoring.md) — raise-don't-kill and
  out-of-band monitor rationale.
- [Spec 10](../specs/10-spec-agent-security-monitoring/10-spec-agent-security-monitoring.md)
  — the security-monitoring spec, Non-Goal 3 in particular.
- [`docs/ARCHITECTURE.md`](../ARCHITECTURE.md) — package boundaries the driver
  proposal composes with.
- [`docs/workflow-schema.md`](../workflow-schema.md) — inheritance semantics
  the `[defaults.sandbox]` block mirrors.
- [`docs/plans/open-goals.md`](../plans/open-goals.md) A10 (secrets beyond env),
  A19 (remote/distributed workers), A23 (multi-operator).
- [Bubblewrap](https://github.com/containers/bubblewrap),
  [gVisor](https://gvisor.dev), [Kata Containers](https://katacontainers.io),
  [Firecracker](https://firecracker-microvm.github.io),
  [Landlock](https://landlock.io), [nsjail](https://github.com/google/nsjail),
  [Kubernetes NetworkPolicy](https://kubernetes.io/docs/concepts/services-networking/network-policies/),
  [Pod Security Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/).
