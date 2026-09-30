# Open goals — product & TUI gaps

Living backlog. First written from a main-branch audit (2026-09-06); every row
re-verified against code, tests, and git history on 2026-09-24 (HEAD `c714ec2`).
Status: **open** unless marked done. Treat code + schema as truth when this file
conflicts with them.

This file is working history (git-ignored under `docs/*`). Specs, plans, ADRs,
and proofs it once linked were untracked by `c714ec2`; rows now cite code paths
and commits instead. Only the tracked reference docs in `AGENTS.md`'s reading
map are linked.

Sources: workflow/engine/TUI surface on `main`, and comparison to high-rated
TUIs (lazygit, k9s, yazi, btop) plus agent peers (Claude Code, Codex CLI,
OpenCode, Crush, Aider).

---

## How to use this doc

- Goals are **outcomes**, not implementation tickets. Split into specs/tasks when
  starting work.
- Priority bands: **P0** (adoption / trust), **P1** (daily polish), **P2** (power
  users / ecosystem).
- Kind: **S** = already specced/planned somewhere; **C** = competitor-shaped /
  never fully scoped; **R** = regression (was built, now broken).
- Architecture since the first audit: the Claude Agent SDK is gone (`b2a6799`,
  `b5d0cb8`); ACP is the only transport (`claude` / `cursor` / `codex`).
  User/project settings live in layered `config.toml` (`internal/config`);
  `.jig/tui.json` and `.jig/telemetry.json` are retired.

---

## A. Product / orchestration (developer-want gaps)

### P0

| ID | Goal | Kind | Notes |
|----|------|------|-------|
| A1 | **Headless `jig run`** — non-interactive / CI runner for a workflow TOML | S | **Done.** `cmd/jig/run.go`, `internal/headless`; contract in [`docs/headless.md`](../headless.md). |
| A2 | **Thin ops CLI** — `status`, `logs`, `doctor`, `resume`, `reset` | C+S | **Done.** `cmd/jig/{ops,control}.go`, `internal/ops`; contract in [`docs/operations.md`](../operations.md). Historical `diff` remains deferred until immutable provenance exists (`operations.md` "Deferred diff contract"). |
| A3 | **Mid-execution crash recovery** — restart workers interrupted mid-agent | S | **Done.** `Manager.Resume` (`internal/engine/resume.go`) reopens interrupted workers and restores review/input/question/recovery/integration parks; durable `session.json` in `internal/datastore/session.go`. |
| A4 | **Cursor harness parity** — `CapSessionResume`, `CapUserQuestion`, `CapPartialStreaming` | S | **Done** (`a411564`). `internal/harness/cursor.go` advertises all caps; resume via ACP `LoadSession` with replay suppression; native `_cursor/ask_question` mapped in `cursor_question.go` (single/multi select only). Live vendor smoke is opt-in (`examples/cursor-parity-smoke.toml`). |
| A5 | **Claude ACP session resume** — advertise + honor `CapSessionResume` | S | **Done** (`b5d0cb8`). `AcpHarness` resumes via `LoadSession`; `SupportsLoadSession` is now populated from Initialize. Runtime still depends on the pinned Claude ACP adapter advertising `loadSession`; tests use a fixture agent. |
| A6 | **Restore Tier-2 security monitors** | R | **Done** structurally: embedded roster (`internal/runner/monitors.go`), run-owned lifecycle, `MonitorAdapter` on ACP (`85bc159`). The required Haiku model is now applied (G1, fixed). Live vendor smoke is opt-in. |
| A7 | **Codex parallel ACP reliability** — durable diagnosis + operator-facing fix path | S | **Partial.** Diagnostics landed before the first audit (`3b76a63`): `harness/acp/diagnostics.go` (`acp-diagnostics.jsonl`, adapter stderr log), process RSS/FD sampling, and an opt-in probe (`JIG_CODEX_ACP_INTEGRATION`). Open: root-cause diagnosis, any fix or throttle, operator guidance in `jig doctor`, recorded threshold results. Note `.agents/jig/sdd.toml` runs codex at `max_parallel=5`. |
| A8 | **Map / foreach fan-out** — N parallel steps from dynamic list data | S | **Done** (`b14429f`). `internal/engine/fanout.go`, schema/validation in `internal/workflow`, resume/reset replay; example `examples/dynamic-fanout.toml`; documented in [`docs/workflow-schema.md`](../workflow-schema.md). |

### P1

| ID | Goal | Kind | Notes |
|----|------|------|-------|
| A9 | **Gemini (or next) backend** — real harness before marketing the name | S | Open. `harness.For` handles only `claude`/`cursor`/`codex`; validate rejects others. |
| A10 | **Secrets beyond env** — vault / 1Password / age (or clear “env-only” product stance) | S | Open. Only resolver is `JIG_SECRET_<NAME>` (`cmd/jig/wire.go` `resolveNamedSecret`); the engine's `SecretResolver` hook is pluggable. Listed as deferred in `workflow-schema.md`. |
| A11 | **Richer condition language** — `&&` / `\|\|` / comparisons | C+S | **Done.** Bounded AST (`internal/workflow/condition.go`), typed numeric comparisons, compound route proofs, module rewriting, chart labels; grammar in `workflow-schema.md`. |
| A12 | **Reset settled runs** — postmortem rewind after `RunFinished` | S | Open. `Run.Reset` returns `settled` once done; `operations.md` lists it as deferred. |
| A13 | **Help agent on historical runs** — read-only journal/transcript analysis | S | Open. Replayed runs get `helpchat.NewUnavailable()`; helpchat tools require a live `*engine.Run`. |
| A14 | **Packaging + CI** — release artifacts, brew/`go install` docs, GitHub Actions | C | Open. No `.github/`, goreleaser, or version ldflags; README is build-from-source. **Blocker:** `go.mod` declares `module jig`, so `go install …@latest` cannot work until the module path is renamed. |
| A15 | **Narrow / mobile terminal contract** — responsive all screens + display sanitization | S | Partial. Monitor single-panel fallback, Review <90-col branches, Home stacks <100 cols, ASCII glyph preset. No cross-screen contract; sanitization limited to tool summaries, diffs, and clipboard. |
| A16 | **Harness seam for help chat + Tier-2 monitors** | S | Partial. SDK coupling is gone; both use narrow harness seams (`internal/runner/monitor.go`, `internal/helpchat/cmds.go`) but are hard-wired to `harness.NewAcpHarness()` (Claude). Open: backend selection via `harness.For`. Standalone chat was removed (B14). |

### P2

| ID | Goal | Kind | Notes |
|----|------|------|-------|
| A17 | Forge / PR automation (open PR, push, checks) | C | Open. Local merge gate only. |
| A18 | OTel / Prometheus / OTLP export | C | **Done.** OTLP gRPC/HTTP, Prometheus scrape, stdout (`internal/telemetry`); off by default, redacted always. Config resolves env → `config.toml [telemetry]` → workflow. Contract: [`docs/observability.md`](../observability.md). |
| A19 | Remote / distributed workers | S | Open. Single machine; local `flock` run lock. |
| A20 | `jig init` / workflow scaffold | C | **Done.** `cmd/jig/init.go`, `internal/scaffold` (`minimal`, `starter` templates). |
| A21 | Graph export (Mermaid / DOT / SVG) | C | Open. In-TUI chart only (`internal/tui/chart`). |
| A22 | Run share / anonymized export bundle | C | **Done.** `jig export RUN_ID --destination PATH [--include-text]`, `internal/runexport`; contract in [`docs/operations.md`](../operations.md#export-a-run). |
| A23 | Multi-operator shared run store | C | Open. Single local operator. |
| A24 | Notifications (desktop / Slack / webhook on gate or failure) | C | **Done.** `internal/notification`: desktop/slack/webhook for `attention_required`, `run_failed`, `run_succeeded`. Destinations in `config.toml [notifications]`; `jig notifications check`. |
| A25 | Chart crossing-min + gate labels | S | **Done** (`020aaa6`, `6e438b6`). Bounded barycentric sweeps (`internal/tui/chart/layout.go`), gate label as node third line. |
| A26 | Charm clickable selector rows | S | **Done** (`7031fee`, `2562750`, `abb7425`). `internal/tui/selector/hit.go`; click selects/focuses, never executes. Documented in [`docs/TUI.md`](../TUI.md). |

---

## B. TUI chrome vs highest-rated TUIs

Peers: **lazygit, k9s, yazi, btop, lazydocker**; agent peers **Claude Code, Codex, OpenCode, Crush**.

Lazygit-polish phases 0–2 closed the *composition* gap (panels, focus badges,
footer honesty, palette, status line, simple mode, Esc). Remaining distance is
operator amenities.

### P0 / P1 — universal amenities

| ID | Goal | Who has it | jig today |
|----|------|------------|-----------|
| B1 | **Clipboard yank / OSC52** — copy run id, step output, selection | lazygit, Crush, nvim | **Done.** See [`docs/clipboard.md`](../clipboard.md). Open follow-up: Monitor output-file line selection (file copies whole). |
| B2 | **Attention signal on gate wait** — terminal bell / optional notify | Claude Code, ops TUIs | **Partial.** Gate-bar pulse (`c0a6ca0`) + opt-in desktop/Slack/webhook on `attention_required` (A24). Open: terminal bell and a TUI-level toggle. |
| B3 | **Fuzzy command palette** + richer named actions | fzf, k9s, gum, Crush | **Partial.** Fuzzy ranking + highlights (`sahilm/fuzzy`, `8af22d4`). Open: named actions — the catalog is still built from bindings and re-dispatches keys. |
| B4 | **Which-key / next-keys overlay** after prefixes | Helix, nvim | Open. Silent `gg` only. |
| B5 | **Theme skins + light mode** | Almost all admired TUIs | Open. Single dark "Pantera" theme; `[ui] glyph_preset` switches symbols, not colors. |
| B6 | **Optional mouse** — click-to-focus + wheel (not mouse-first) | lazygit, k9s, yazi, btop | **Done** for Home (click + wheel) and Monitor (click focus, transcript click-to-toggle, wheel); Detail wheel only. Review, diffview, and question panels have no mouse. |
| B7 | **Remappable keybindings** (config) | lazygit, k9s, Helix, Crush | Open. Hardcoded `*Keys`; no keys table in `config.toml`. |
| B8 | **In-monitor run/session switcher** | Crush `ctrl+s`, OpenCode | Open. Esc → Home. |

### P1 / P2 — maturity

| ID | Goal | Notes |
|----|------|-------|
| B9 | Panel jump `1–n` + resize | Open. Digits are gate verdicts only. |
| B10 | Richer TUI config | Partial. `config.toml` has `[ui] glyph_preset`, `[tui] simple_mode`, `[tui] compact_tool_groups`. Open: theme, keys, bell, density. |
| B11 | Custom commands / plugins | Open. |
| B12 | Suspend `ctrl+z` | Open. |
| B13 | In-TUI export / screendump | Open in the TUI. Nearby: CLI `jig export` (A22), `Y` copies a recorded transcript. |
| B14 | Wire standalone `chat` into Home↔Monitor IA **or remove** | **Done — removed** (`internal/tui/chat` deleted in `b5d0cb8`). Only the in-Monitor help agent remains. |
| B15 | Slash / agent-ops commands while LIVE (`/compact`-like, permissions, model display) | Open. |
| B16 | Context % / compact / rewind UI | Open. Per-step token counts only. |
| B17 | Mid-run model switch | Open. |
| B18 | Vim composer mode | Open. |

---

## C. Transcript experience — path to best-in-class

### Why this section exists

Spec 15 landed the right *shape*: prose-first, one quiet row per tool exchange,
semantic summaries, bounded expand. The omp-parity slices (card primitive,
truncation vocabulary, diff rendering, grouping, inline thinking, turn metadata,
boundary banners, liveness, glyph presets, inline args) added most of the
chrome.

What still makes Claude Code, Codex, Crush, and OpenCode feel **easier to read**
is *feel*: live streaming in the transcript, actionable paths, burst density,
and safer noise control.

Current strengths (keep):

- Conversation-first items (`internal/tui/monitor/monitor_transcript_items*.go`)
- Semantic tool one-liners (`monitor_tool_summary.go`) — `Read foo.go`, `Run go test`
- Matched use+result as one row; failed rows with text+glyph (not color-only)
- Page-local search (`/`) + filters (`F`); `n/N`; follow (`f`/`G`, `gg` top);
  expand-all (`o`); `c` compact groups; `x` clear; `[`/`]` paging; click to
  select/toggle. Simple mode (default) hides several of these from the footer
  but the keys still work.
- Themed Glamour for assistant prose; tool/command output stays verbatim
- File-is-truth JSONL + windowed paging (`internal/transcript`)

### What “absolute best” transcript needs

Ranked. Status: **have** / **partial** / **missing**.

#### Must-haves

| ID | Goal | Status | Notes |
|----|------|--------|-------|
| T1 | **Token-stream polish in the monitor** — in-progress assistant text with a live cursor in the Transcript; finalize to markdown when the block completes | partial | The live typing tail renders in the **Steps** panel (last 10 raw lines, no cursor, `monitor_steps.go`); the Transcript shows only finalized entries plus a spinning `LIVE` title and pulsing thinking label. The old `internal/tui/chat` reference no longer exists; `internal/helpchat` re-renders markdown per delta. |
| T2 | **Copy / yank selected transcript item** | have | `y` item (collapse-independent; group header copies members), `Y` whole recorded step transcript. See [`docs/clipboard.md`](../clipboard.md). |
| T3 | **Open location** — `path:line` in `$EDITOR` or configured opener | missing | Locations render as inert text. |
| T4 | **In-transcript edit cards with unified diff** | partial | Inline `Diff · <path>` with line numbers and intra-line highlights is now the **default** when old text exists; `+N/-M` badge; falls back to "New code". Open: toggle between diff and full new source. |
| T5 | **Smart burst folding** — collapse consecutive successful tool rows between prose | partial | Reads always group into a tree (`79498d1`); same-kind compact groups (first 3 · "… N more" · last) bridge hidden reasoning (`a2fef23`) but are **opt-in** (`c` / `[tui] compact_tool_groups`, default off). Open: default-on policy, mixed-kind bursts. |
| T6 | **Transcript noise & secrets policy** | partial | Collapsed arg previews redact secret-named keys and summarize objects/arrays (`7490319`); configured secrets replaced at write time; oversized user/thinking text collapses to a size label; tool output clamped (4 KiB / 12 rows) and never markdown-interpreted. Open: secret scan of error-hint/result previews; "N bytes" hiding for huge JSON. |
| T7 | **Per-turn / per-exchange timing + tokens** | partial | Turn metadata row (time, `Δ`, iter/attempt) at each generation/iteration/attempt boundary (`monitor_transcript_metadata.go`). Cost/tokens only on the step-end row. Open: per user↔assistant exchange spend. |
| T8 | **Density + optional metadata modes** — compact / comfortable; optional seq/timestamp/cost chrome | missing | Simple mode only trims footer/help. |

#### Strong polish

| ID | Goal | Status | Notes |
|----|------|--------|-------|
| T9 | **Turn grouping** — visual rhythm for user↔assistant units | have (basic) | Tinted user bubbles, unlabeled rule between assistant responses (`b5f45f1`), relationship-based spacing. Turn headers not planned. |
| T10 | **Richer tool lifecycle** — running → done/error with elapsed | partial | Running state is a glyph + running border (no text); running detail pins to newest lines; panel-title liveness. Open: per-tool elapsed/progress. |
| T11 | **Inline permission / question cards in transcript** (or deep-link into Gate) | partial | Questions stay in the Gate (fitted AskUserQuestion panel, `3b59132`); transcript shows an "Ask …" row; `ctrl+o` jumps Gate → context. Open: transcript → Gate link, inline approvals. |
| T12 | **Jump affinities** — next error / tool / edit | partial | `n/N` + `F` filters (errors/tools/reasoning/retries/role). No dedicated affinity keys. |
| T13 | **Full-run search index (optional)** | partial | Page-local by design; `transcript.Reader` has no search API. |
| T14 | **Sticky turn / step context header** while scrolling | missing | |
| T15 | **Virtualized / incremental render** | partial | Markdown, card-header, and read-group caches; 100 ms frame coalescing. Open: every key rebuilds the full `chatBody()` string; detail bodies/diffs uncached (`computeDiff` reruns per render); `setChatPage` clears the card cache. |
| T16 | **Markdown excellence** — tables, fences, soft-wrap, link actions | partial | Themed Glamour + width-aware Chroma. No OSC 8 / link actions. |
| T17 | **Thinking presentation** | partial | Inline italic muted prose; >4 KiB collapses; live thinking pulses. Settled reasoning is now **hidden** unless the `reasoning` filter is on (`52fead4`). Open: streaming thinking affordance (thinking appears only when finalized). |
| T18 | **Parallel-tool fan visualization** | missing | Compact groups exclude running calls. |
| T19 | **Result truncation UX** — “showing 12/400 lines · enter to expand · yank all” | partial | `… N more lines [y: Copy full]` / `… N earlier lines` on bounded expanded details (G5 fixed), write-time and diff clamp notes. Open: "N/M" counts. |
| T20 | **Retry / generation dividers as story beats** | have (plain) | Centered labeled rules ("retry N" / "iteration N" / "reset N") plus page-edge banner (`monitor_transcript_banner.go`). Open: cause text (why it retried). |

#### Explicit non-goals (unless a later spec overturns)

- Changing durable JSONL schema for cosmetics alone (presentation layer first).
- Unbounded history scan to repair tool pairing (Spec 15 invariant).
- Mouse-first transcript selection (optional click-to-toggle exists; keyboard stays primary).
- Reintroducing Spec 11’s heavy two-level group chrome (prefer T5 light folding).

### Transcript north-star (one paragraph)

An operator watching a LIVE step should see **assistant prose streaming cleanly**,
**tools as a quiet scannable sidebar of activity**, **edits as readable code or
diffs one keystroke away**, **failures obvious without color-only cues**, and
**one key to copy or open any piece of evidence** — without ever feeling like they
are reading a JSONL dump. Density modes and optional metadata exist for forensics;
the default view stays conversation-first.

### Suggested transcript delivery order

1. **T1** streaming in the Transcript panel (T2 done)
2. **T3** open path + **T4** diff ↔ source toggle — actionable evidence
3. **T5** default burst folding + **T6** preview secret scan — long runs stay readable
4. **T7** + **T8** economics + density — trust and forensics
5. Remaining T10–T19 polish

---

## D. Ranked top 20 (cross-cutting)

Done since the first audit, struck through; open items keep their rank.

1. ~~A1 Headless `jig run`~~ **done**
2. **T1 Transcript streaming polish**
3. ~~T2 Clipboard yank~~ **done**
4. ~~A6 Restore Tier-2 monitors~~ **done** (G1 model fix landed)
5. ~~A4 / A5 Cursor + Claude ACP resume parity~~ **done**
6. B2 Gate attention — **partial**: pulse + opt-in notify landed; bell open
7. ~~A2 Thin ops CLI~~ **done**
8. **T3 Open location**
9. **T5 Smart burst folding** — partial; default-on + mixed-kind open
10. B3 Richer palette actions — fuzzy done; named actions open
11. ~~A3 Mid-crash recovery~~ **done**
12. **T4 Diff ↔ source toggle** — inline diff done
13. ~~A8 Map/foreach fan-out~~ **done**
14. A7 Codex parallel reliability — diagnostics done; fix/operator path open
15. ~~B1~~ **done** / B4 Which-key
16. **T6 Noise & secrets policy** — partial
17. B5 Theme skins + light mode
18. **T7 Per-turn tokens/timing** — partial
19. B8 In-monitor run switcher
20. A14 Packaging + CI — module rename is the first step

---

## E. Already in good shape (do not reopen casually)

- Lazygit-style panel composition, focus badges, contextual footer, `?` help
- Simple / advanced mode (`[tui] simple_mode` in `config.toml`; `ctrl+shift+a` toggles in-session without writing to disk)
- Non-blocking gates
- Spec 15 prose-first transcript item model
- Validate-at-load workflow schema; DAG + bounded routes; foreach fan-out
- Worktree integration + file-backed reviews
- Transcript-as-truth (bus is liveness only)
- ACP harness parity across `claude` / `cursor` / `codex` (resume, questions, partial streaming, model selection); Cursor questions have no typed answer (its callback carries option IDs only)

---

## F. Related docs

Tracked references only:

- [`docs/workflow-schema.md`](../workflow-schema.md) — schema, fan-out, conditions, deferred constructs
- [`docs/headless.md`](../headless.md) — headless `jig run` (A1)
- [`docs/operations.md`](../operations.md) — ops CLI, reset, export (A2, A22)
- [`docs/observability.md`](../observability.md) — telemetry export (A18)
- [`docs/security-monitoring.md`](../security-monitoring.md) — Tier-1/Tier-2 security (A6)
- [`docs/clipboard.md`](../clipboard.md) — `y` / `Y` contract (B1, T2)
- [`docs/TUI.md`](../TUI.md) — TUI engineering, mouse contract (A26, B6)

---

## G. Defects surfaced by the 2026-09-24 audit

Not goals — bugs and drift found while verifying the rows above. All fixed;
open bugs are queued one-per-agent in [`bugs.md`](bugs.md).

| ID | Defect | Evidence | Status |
|----|--------|----------|--------|
| G1 | `SessionSpec.Model` / `Effort` ignored for `claude` and `cursor`; only `codex` applied them. Tier-2 monitors and help chat never got their required `claude-haiku-4-5-20251001`, and workflow `model` on claude/cursor steps was silently ignored. | `internal/harness/{acp,cursor,acp_config}.go`; test `internal/harness/acp_session_config_integration_test.go` | **fixed** — Claude applies model (adapter-resolved) + effort; Cursor applies exact model and fails closed on effort (no `thought_level` selector). Per-backend contract in `workflow-schema.md`. Still unapplied on every ACP harness: `allowed_tools`/`disallowed_tools`/`permission_mode` (bugs.md bug 1) and `fallback_model`/`max_turns`/`max_thinking_tokens`/`max_budget_usd` (bug 2). |
| G2 | Custom "Other…" answers were dropped: the question panel forced `AllowCustom` on every select field (`ad3ec79`), the engine rejected the custom answer against the original request and returned silently, and the step sat in needs_input with its panel already dismissed. Only Cursor was affected — the Claude adapter (and Codex, via the shared `_askUserQuestionCustomAnswer` marker) sends a companion field for every question. | `internal/tui/question/model.go` `New`, `internal/engine/commands.go` `agentQuestionAnswerMsg` | **fixed** — panel honors `AllowCustom`; engine re-announces a rejected question instead of dropping it |
| G3 | Transcript search input, match status, filter summary, and filter picker rendered only in the empty-transcript branch, so `/` and `F` were invisible once a transcript had items. | `internal/tui/monitor/monitor_transcript.go` `chatBody` early return | **fixed** — pinned above the viewport (`transcriptChrome`/`setChatContent`); viewport shrinks by the pinned rows; clicks offset; opening `/`/`F` no longer jumps to top |
| G4 | `TestBoundaryBannerFoldsIntoClosingItemLineRange` failed: user bubbles start with a tinted blank padding row (`8c84fcc`), so the "turn B at range start" assertion was stale. | `internal/tui/monitor/monitor_transcript_banner_view_test.go` | **fixed** — asserts the first non-blank row of the range |
| G5 | T19 hint "[enter: Expand]" printed on bounded detail bodies, which render only for already-expanded items, where `enter` collapses. | `monitor_transcript_items_view.go` (`writeItemDetail`, `writeDiffSection`, `writeResultingSourceCard`) | **fixed** — hint is now `[y: Copy full]` (`shared.CopyFullHint`, live `CopyItem` key); `y` copies the item's full recorded content |
| G6 | Stale comments/docs: "skip SDK connect" (`monitor_update.go`); gate label "not yet drawn" (`chart/layout.go`); `docs/TUI.md` said groups need ≥2 calls (singletons group since `79498d1`); harness comments claimed `Open` rejects unadvertised capability fields (only `runner/agent.go` gates). | as cited | **fixed** — all four corrected |
| G7 | `c714ec2` untracked docs that tests read: clipboard doc contract and three step-context goldens. | — | **fixed** — `docs/clipboard.md` restored and tracked; goldens moved to `internal/{step,engine}/testdata/`; contract test no longer reads this untracked file |
