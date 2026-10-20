# D05 offline build fixture

Implementation and qualification are in progress. Docker workload execution is
currently rejected with `sandbox_runtime_unsupported`; policy permission does
not enable it. The example must never fall back to a host build.

From the repository root, explicitly prepare the image:

```sh
sh testdata/docker/d05/prepare-image.sh
go run ./cmd/jig validate examples/docker-build-jig.toml
```

Preparation uses network access to fetch the pinned Go base and public module
dependencies. Its temporary build context contains only `Dockerfile`, `go.mod`
and `go.sum`. The base supplies Go, Git, POSIX sh and sha256sum. Module versions
and checksums remain in the repository manifests; their source and license
files remain in `/go/pkg/mod`. Runtime must override the image command and user
with the qualified mapped non-root identity and use private writable scratch.
The immutable dependency cache cannot serve as writable build scratch.

The workflow builds a Linux binary, validates it, returns it to host storage,
then passes its bytes to a fresh consumer through `JIG_INPUT_BINARY`. The host
need not be able to execute that binary. This file does not claim those runtime
behaviors are implemented or qualified. OrbStack, Docker Desktop and native
Linux require separate conformance evidence.

## Development measurements

After preparing the image, `python3 testdata/docker/d05/measure.py` explicitly
creates one uniquely named container with 4 CPUs, 4 GiB memory (no swap), and
512 PIDs. It stages only regular committed files from `HEAD`, excludes sensitive
roots, and removes its container before its temporary files. It never mounts
the checkout. This is a sizing probe, not a production staging implementation
or host jig acceptance test. An interrupted controller can leave probe resources;
inspect the exact `jig.d05.probe` label before manually removing them.

On OrbStack, Docker 29.8.2 / Linux arm64, Go 1.25.12, source commit
`b3fd03485b452a9f814e71d8c046a1a218eda4e9`, the 2026-10-16 probe measured:

| Measurement | Observed value |
|---|---:|
| Selected source | 779 files / 5,852,097 bytes |
| Cold build wall time | 11 seconds (one-second clock resolution) |
| Warm build wall time | Less than one timestamp interval |
| Final scratch allocation | 347,524 KiB |
| Peak cgroup memory | 1,173,970,944 bytes |
| Peak cgroup PIDs | 44 |
| Binary size | 46,145,292 bytes |

The host and container SHA-256 matched:
`98be02096e73fd289984d5a2f6ac2495908909b119b0e3b74bf1c6506a8adb58`.
The first probe exhausted a 256 MiB `/tmp`; Go intermediate files now use
`GOTMPDIR=/scratch/go-tmp`. Final scratch allocation is not peak disk usage.
Neither disk-monitor overshoot nor adversarial enforcement has been measured.

## Read-only preflight

Supply an explicit operator-owned D04 policy allowing the selected endpoint,
`jig-d05-go` repository, offline network, actual source root, and intended
storage root, with explicit CPU/memory/PID defaults and ceilings. Do not widen
an existing operator policy for this test. Exact path spelling matters, including
`Repos` versus `repos` on a case-insensitive macOS filesystem.

```sh
JIG_DOCKER_INTEGRATION=1 \
JIG_D05_TEST_POLICY=/absolute/operator/d05-policy.toml \
JIG_D05_TEST_CONTEXT=orbstack \
go test ./internal/sandbox -run '^TestD05DockerPreflight$' -count=1 -v
```

This test checks trusted endpoint selection, negotiated API, native platform,
cached image and reported compute capabilities. It does not create containers.
Missing configuration fails when opted in; the default test suite skips it.
`JIG_D05_TEST_*` variables configure only this test, never runtime selection.

## Source staging implementation bounds

The runner's private workspace preparer now reads the engine-selected commit's
Git blobs, verifies their object hashes, and builds a new synthetic repository.
It does not copy dirty/untracked/ignored checkout content, host Git metadata,
credential paths or caches. Contained relative symlinks are checked before the
synthetic `.git` is created. Frozen script/schema captures and the original
commit/exclusion manifest live outside the writable workspace.

These are enforced development limits, not measured runtime capacity promises:

| Bound | Value | Enforcement / failure |
|---|---:|---|
| Source setup | 60 seconds, or earlier attempt deadline | Context cancellation; managed Git helper shutdown |
| Git tree listing | 8 MiB | Bounded writer; staging output limit error |
| Enumerated entries, including exclusions | 20,000 | Bounded split and entry-count check |
| Distinct file/directory paths | 40,000 | Canonical path inventory; traversal limit error |
| Source bytes | 256 MiB total; 32 MiB per blob | Tree-size admission and bounded copy/hash |
| Paths | 4,096 bytes; 255 bytes/component; 64 components | Parser rejects oversized/nonportable paths |
| Symlink targets | 4,096 bytes | Bounded object read and confined resolution |
| Each frozen script/schema | 1 MiB | Verified authoring capture and size check |
| Git output drain after cancellation | 1 second | `exec.Cmd.WaitDelay`, with process-group confirmation |
| Helper termination confirmation | 2 seconds after wait | Existing independent process-group shutdown bound; uncertainty retains staging |

The source byte limit excludes synthetic Git object/index overhead. Whole-stage
and runtime scratch monitoring, persistence-off source selection,
output destinations, crash cleanup and actual Docker wiring remain unfinished.
Missing immutable source identity, integrating commands, unsnapshotted file inputs,
required history/submodules/LFS, unsupported tree modes and unsafe links fail
before container creation. No container is created by these staging tests:

```sh
go test ./internal/runner ./internal/engine -run '^TestD05Container|^TestD05DispatchPinsSourceCommit' -count=1
```

Cleanup checks the staging root's filesystem identity. A replaced root is
retained rather than deleted, and uncertain helper termination retains staging
for the owner to fence and inspect. A future runtime must call cleanup only
after all containers using the workspace are confirmed stopped.

## Declared input staging bounds

The private preparer accepts only named whole-output references matching the
step declarations, with an absolute run-owned artifact path and accepted
SHA-256, byte-size and delivery-mode metadata. Engine resolution records only
metadata, avoiding binary reads on the scheduler; the worker performs confined
reads beneath the run artifact directory.
It copies binary bytes to a separate input directory, checks the digest and
size, strips write permissions, and applies executable permission from the accepted
publication metadata. Environment bindings contain only `/inputs/<ordinal>`. The
future runtime must mount this directory read-only at `/inputs`; file modes
alone are not the runtime immutability boundary.

| Bound | Value | Enforcement / failure |
|---|---:|---|
| File inputs | 64 per attempt | Count admission before copying |
| Input bytes | 128 MiB per file, 256 MiB total | Descriptor size admission and bounded copy |
| Copy buffer | 32 KiB | Fixed buffer, cancellation between reads |
| Input setup | Shared 60-second source setup deadline | Context checked before open and between reads |

These are engineering limits, not measured capacity claims. Missing files,
symlinks, nonregular files, changed identity, size/digest mismatches and alias
collisions fail preparation and remove private partial staging. No failed read
can become a literal path-string input. Tests use only synthetic files.
The independent output collector emits the accepted mode/integrity metadata;
runtime finalizer integration remains pending and
persistence-off input ownership is pending. Missing metadata is rejected. This
preparer does not enable Docker execution or prove container mounts.

## Declared output collection

The independently tested collector streams the declared regular output into
an execution-specific artifact directory, with SHA-256, size and delivery mode
in `metadata.json`. A directory rename publishes both files together. Existing
nonce directories are never reused. Source symlinks (including parent links),
multiply linked files, private paths and files above 128 MiB are rejected.
Delivery modes are only 0444 or 0555; arbitrary source permission bits are not
preserved. Required file sync, metadata writes, close and directory sync errors
prevent returning an accepted artifact reference.

Collection has a 30-second cap or the earlier attempt deadline, a 128 MiB byte
cap and a 32 KiB copy buffer. These are development bounds, not measured
throughput guarantees. Cancellation is checked between reads and publication
operations; it cannot interrupt a kernel filesystem call already in progress.
Failures before rename clean the private pending directory. Failure after
rename retains an unaccepted execution-specific directory, with no returned
path or metadata; recovery must not reinterpret its presence as success.

Host output predicates use a regular-file descriptor and the binary bound for
`output_exists`. `output_contains` and frozen JSON-schema validation retain the
8 MiB text bound. The caller must first confirm container quiescence and
complete executable validation; neither helper launches validators or grants
permission to publish. Runtime finalizer wiring and lifecycle publication facts
remain pending. Persistence-off destinations remain unsupported.

Synthetic tests cover final validator-edited bytes, binary return after producer
staging removal, a separate consumer copy, unsafe paths/links/FIFOs, immutable
attempt identities, cancellation and publication fault injection. They are not
real Docker round-trip or container-termination proof.

## Execution control transport

A private SDK control client now supports one exact create/start/stop/kill/
inspect/remove operation per client. Every permitted operation first checks
the pinned daemon ID and native platform through `/info`; the client pins the
inspected API and Unix socket and never renegotiates or selects an endpoint
from ambient environment. Control requests use fresh connections, no proxy,
no redirect following and no automatic retry. A consumed permission cannot be
reused after success, failure, cancellation or a lost reply.

Control calls have a 10-second total context cap covering both daemon identity
and operation, a 2-second dial cap, 64 KiB response headers, 1 MiB request bodies
and 4 MiB response bodies. Stop explicitly requests SIGTERM with two seconds
before daemon kill; explicit kill uses SIGKILL. Removal never requests force,
volume deletion or link deletion. Other routes, abbreviated IDs, checkpoints,
size scans and credential headers are rejected before network effects.

These are transport development limits, not platform qualification evidence.
Fake-daemon tests cover actual pinned SDK request shapes, daemon/platform/socket
drift, lost/truncated/oversized replies, forbidden routes, private error
sanitation, concurrent permission reuse, cancellation and oversized requests.
The runtime must still supply acknowledged create/start authority, validated
create configuration, exact ownership checks and bounded reconciliation. No
public Docker command path uses this transport yet. Doctor remains read-only.

## Constrained create configuration

The pure create builder accepts only non-integrating offline command steps, a
matching frozen authorization result, native Linux amd64/arm64 image identity,
complete phase identity and positive non-root UID/GID. It creates by image ID
and rejects image-declared volumes. CPU/memory/PID values come unchanged from
operator authorization; memory-plus-swap equals memory to request no swap.

The request disables healthchecks, restart, auto-removal, TTY/stdin, daemon log
storage and networking; drops all capabilities; requests no-new-privileges,
private IPC/cgroup namespaces and init; and makes the image root read-only.
The builder supplies no devices, published ports, privileged flag, added
capabilities, host namespaces or inherited volume bindings. Default daemon
seccomp must still be checked and qualified before any workload is enabled.

Only staging `workspace` and `scratch` bind mounts are writable. `inputs` and
`captures` are read-only. All four binds are nonrecursive, use private
propagation and prohibit automatic source creation. `/tmp` and `/home/jig`
are disposable 64 MiB tmpfs mounts each, with noexec/nosuid/nodev and the
explicit non-root owner; `/dev/shm` is capped at 16 MiB. These are hard-limit
requests, not measured enforcement claims. Scratch disk monitoring remains
unimplemented. The runtime must verify private staging ownership and UID/GID
mapping before create.

The image must provide `/usr/bin/env` and `/bin/sh`. The absolute `env -i`
entrypoint clears inherited image environment before the command shell, with
a deterministic runtime environment plus validated profile variables and
container-local input bindings. Go module/cache paths point to the prepared
image; the recipe's profile points writable Go build/temp storage at scratch.
Inline commands retain shell semantics; scripts use the frozen capture; a
validation phase uses only the declared validation command.

Pure configuration and fake-daemon SDK serialization tests cover control
settings, image startup overrides, native platforms, input bindings, rejection
of incompatible requests and exact operator limits. Image locks, mount/owner
checks, seccomp qualification, streams and runner wiring remain pending; this
configuration builder does not enable Docker execution.
