# jig

jig's domain vocabulary — a glossary, not a spec. Two clusters: the TUI
presentation language, and the execution & code-integration model.

## TUI presentation

The presentation vocabulary of jig's Bubble Tea interface — the terms that name
what the user sees and where their keys act.

**Screen**:
One top-level view in the workflow flow: Selector, Detail, Runs, or Monitor.
Exactly one is active at a time. The standalone streaming Chat client is a separate
root model reached via `go run ./cmd/jig` — not part of the four-screen flow, but it
is paneled with the same helper and focus convention.
_Avoid_: Page, view, tab.

**Panel**:
A rounded border with a title composited into its top edge, wrapping a body of
content. A pure-presentation primitive: it frames and titles a pre-rendered body
and paints the focus color; it never owns viewports, wrapping, or content sizing —
the caller fits the body to the panel's inner area using lipgloss frame helpers.
_Avoid_: Box, frame, pane, window.

**Focus** (of a region):
The property of holding keyboard input. The focused region's border is drawn in the
primary color (Charple); every blurred region's border is dim (Iron). On a
single-panel screen the whole screen is the focused region. In the Monitor, focus
moves between the Steps panel, the Transcript panel, and the Gate — the Gate only
while the input queue is non-empty (the empty input bar is inert and skipped by
the `tab` cycle).
_Avoid_: Active, selected (reserve "selected" for the list cursor row).

**Gate**:
The Monitor surface through which human input is collected. An always-present,
one-line action bar reports pending entries; focusing a pending Gate opens its
controls as an overlay without resizing the Steps or Transcript panels. A pending
Gate does NOT freeze navigation — the user can still move focus between the panels
to read context while the Gate waits.
_Avoid_: Modal, dialog, prompt (reserve "prompt" for the from="user" entry).

**Input queue**:
The ordered set of all steps currently blocked on a human, surfaced through the
Gate. Arrivals append; nothing is dropped when several steps block at once. A
`[N / M]` indicator names the active position and the total. While the Gate holds
focus, `[` selects the previous entry and `]` selects the next entry; both wrap so
the user can answer entries in any order. `tab`/`shift+tab` always move focus
between regions.
_Avoid_: Stack, list (reserve "list" for the Steps list), backlog.

**Input entry** (or **entry**):
One item in the input queue: a review verdict, a `block_on` agent-input box, an
AskUserQuestion option list, or a `from="user"` text prompt. An entry carries the
step ID (and tool-use ID for a question) that routes its response, plus any draft
text the user has typed, preserved as they navigate away and back. A review
entry's diff is not shown in the entry — it renders in the Transcript panel when
its step is selected.
_Avoid_: Request, item, gate (a gate is the strip; an entry is what it displays).

**Footer**:
The single unboxed dim hint line rendered directly below a screen's panel(s),
listing the keybindings available in the current state. Never enclosed in a panel.
_Avoid_: Help line, status bar, keybind bar.

## Execution & code integration

The vocabulary of how jig carries code between steps and lets an operator rewind a
run. Introduced by the run-integration/reset work (spec 05).

**Run branch**:
The single per-run git branch into which every step's code changes are integrated,
one commit per step. It starts at the user's working-branch HEAD and accumulates the
run's work; at run end a single human-gated merge lands it back on the user's branch.
_Avoid_: Integration branch (acceptable synonym), main, trunk.

**Step worktree**:
The isolated git worktree in which one mutating step's agent runs, branched off the
**run branch's current HEAD** — so a step sees the code its upstream steps produced.
Each mutating step gets its own; read-only steps get none.
_Avoid_: Sandbox, checkout.

**Integration**:
Squash-merging a completed step's worktree branch back into the run branch as
**exactly one commit, tagged with the step id**. The per-step commit is what makes a
step addressable by a later reset.
_Avoid_: Merge (reserve "merge" for the final run-branch → user-branch landing).

**Integration conflict**:
A git conflict encountered while integrating a step, or while replaying a survivor
during a reset, when two changes touch the same lines. Surfaced through the Gate as a
human-resolved entry — never auto-resolved.
_Avoid_: Collision, clash.

**Reset** (to a step):
The operator action that rewinds a run to an earlier target step: `git reset` the run
branch to before the target's dependency closure, replay the survivor commits outside
that closure, and return the target and its downstream to `pending` to re-run. Only on
an unfinished run.
_Avoid_: Retry, rerun (reserve "retry" for the automatic `on_failure = "retry"`).

**Stop / Resume** (a step):
**Stop** interrupts a single running step (the run stays alive and becomes quiescent);
**resume** continues that step's agent session with a new message. Stop is the way to
reach quiescence mid-run so a reset can proceed; resume continues the conversation, not
the exact interrupted turn.
_Avoid_: Pause; Cancel (reserve "cancel" for tearing down the whole run).

**Quiescent**:
A run with no worker in flight that has not yet settled — the only state in which a
reset is safe. Reached at a gate or by stopping the running step.
_Avoid_: Idle, paused, done.

**Generation**:
The per-step counter of manual re-runs, distinct from **`Attempt`** (automatic
failure-retries under `on_failure = "retry"`) and **`Iteration`** (loop passes). Bumped
when a step is manually reset so its transcript shows a legible boundary; unlike
`Attempt`, it gates no budget.
_Avoid_: Attempt, retry, version, epoch.

## Harness abstraction

The vocabulary of `internal/harness`, the seam between `AgentExecutor` and the
agent process it drives. Introduced by the ACP↔Claude harness work (spec 12).

**Harness**:
The jig-owned Go type implementing the `Harness` interface (`ClaudeHarness`,
`AcpHarness`, `CursorHarness`) — the code that translates a `SessionSpec` into one specific
transport's lifecycle and normalizes its output into jig's transcript model.
_Avoid_: Backend (reserve for the vendor/model being driven), adapter, driver.

**Backend**:
The vendor, CLI, or model a Harness talks to (Claude today; Cursor, Codex,
Gemini later) — the *target*, not the jig code that talks to it. Selected in
the workflow TOML (`backend` / `transport` on `[defaults]` / `[[step]]`), never
via `JIG_HARNESS`. One Harness could in principle target more than one backend
(an ACP Harness could drive Cursor as well as Claude via the same transport).
Today `AcpHarness` reaches **Claude** only (Zed’s `claude-code-acp` adapter),
not Cursor.
_Avoid_: Harness (a backend is who you're talking to; a Harness is the code
that talks).

**Session**:
One jig-level value returned by `Harness.Open`, live for exactly one
`AgentExecutor.Execute` call. Mid-turn interaction (an `AskUserQuestion`
answer, a queued tool result) is sent to the *same* live Session via `Send`.
Resuming a stopped step does not reach back into a prior Session object —
`AgentExecutor` calls `Open` again with `SessionSpec.Resume` set to the prior
conversation ID, and the Harness/backend continues that conversation under a
new Session value, exactly as today's SDK path opens a new client with
`WithResume`.
_Avoid_: Conversation (reserve for the backend-side concept a Resume ID
points at), connection.

**Capability** / **`CapabilitySet`**:
A named, boolean feature a Harness advertises **before** `Open` is called
(`CapPermissionCallback`, `CapInProcessMCP`, `CapSessionResume`,
`CapStructuredOutput`, `CapPartialStreaming`) — queried explicitly, never
inferred by runtime type assertion after the fact. `AgentExecutor` fails
closed (rejects the step) when a step needs a capability the active Harness
does not advertise, rather than silently degrading.
_Avoid_: Feature flag, trait.

**`PermissionFn`**:
The jig-owned callback type a Harness invokes synchronously before a tool
executes, when `CapPermissionCallback` is advertised and `SessionSpec.Permission`
is set. `AgentExecutor` constructs it by closing over the step's
[[spec-10-agent-security-monitoring|`sentinel.Guard`]] — `internal/harness`
itself never imports `sentinel`, mirroring how `engine` never imports
`runner`.
_Avoid_: Guard (reserve for the concrete `sentinel.Guard` firewall type),
callback (too generic once `PermissionFn` is named).
