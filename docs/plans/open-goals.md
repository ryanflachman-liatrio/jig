# Open goals — product & TUI gaps

Living backlog from a main-branch audit (2026-09-06). Status: **open** unless marked
done. Treat code + schema as truth when plan files conflict.

Sources: workflow/engine/TUI surface on `main`, Specs 11/14/15, Lazygit polish
phases 0–2, deferred items in `docs/workflow-schema.md` / `docs/engine-design.md`,
and comparison to high-rated TUIs (lazygit, k9s, yazi, btop) plus agent peers
(Claude Code, Codex CLI, OpenCode, Crush, Aider).

---

## How to use this doc

- Goals are **outcomes**, not implementation tickets. Split into specs/tasks when
  starting work.
- Priority bands: **P0** (adoption / trust), **P1** (daily polish), **P2** (power
  users / ecosystem).
- Kind: **S** = already specced/planned somewhere; **C** = competitor-shaped /
  never fully scoped; **R** = regression (was built, now broken).

---

## A. Product / orchestration (developer-want gaps)

### P0

| ID | Goal | Kind | Notes |
|----|------|------|-------|
| A1 | **Headless `jig run`** — non-interactive / CI runner for a workflow TOML | S | **Done** (Spec 19). `jig run` + `internal/headless`; contract in [`docs/headless.md`](../headless.md). Plan: [`docs/specs/19-spec-headless-run/19-implementation-plan.md`](../specs/19-spec-headless-run/19-implementation-plan.md). |
| A2 | **Thin ops CLI** — `status`, `logs`, `doctor`, `resume`, `reset` | C+S | **Done.** Contract: [`docs/operations.md`](../operations.md). Plan: [`a2-thin-ops-cli.md`](a2-thin-ops-cli.md). Historical `diff` remains explicitly deferred until immutable provenance exists. |
| A3 | **Mid-execution crash recovery** — restart workers interrupted mid-agent | S | **Done** (Specs 20 and 21). Worker crash reopen, durable `session.json`, and all unfinished gate/stop/integration parks restore through `Manager.Resume`. Plans: [`20`](../specs/20-spec-mid-crash-recovery/20-implementation-plan.md), [`21`](../specs/21-spec-unfinished-park-reopen/21-implementation-plan.md). |
| A4 | **Cursor harness parity** — `CapSessionResume`, `CapUserQuestion`, `CapPartialStreaming` | S | Plan: [`a4-cursor-harness-parity.md`](a4-cursor-harness-parity.md). Today fail-closed on resume / block_on (`internal/harness/cursor.go`); plan covers native question extensions, replay-safe loading, and streaming previews. |
| A5 | **Claude ACP session resume** — advertise + honor `CapSessionResume` | S | ACP→Claude cannot Stop/Resume or resume `block_on` today. |
| A6 | **Restore Tier-2 security monitors** — ship `examples/agents/monitors/*.md` (or change discovery) | R | **Done.** Fixed embedded roster, run-owned Start/Resume signaling, isolated direct-SDK classifiers, durable budget state, and offline Claude/Cursor/Codex ACP lifecycle coverage. Plan and evidence: [`a6-restore-tier2-security-monitors.md`](a6-restore-tier2-security-monitors.md). Live vendor smoke remains opt-in and was not run. |
| A7 | **Codex parallel ACP reliability** — durable diagnosis + operator-facing fix path | S | Known flakiness under `max_parallel`; diagnostics plan still open. |
| A8 | **Map / foreach fan-out** — N parallel steps from dynamic list data | S | Plan: [`a8-dynamic-foreach-fan-out.md`](a8-dynamic-foreach-fan-out.md). Explicitly deferred in `docs/workflow-schema.md` until this plan is implemented. |

### P1

| ID | Goal | Kind | Notes |
|----|------|------|-------|
| A9 | **Gemini (or next) backend** — real harness before marketing the name | S | Validate rejects; no `harness.For` path. |
| A10 | **Secrets beyond env** — vault / 1Password / age (or clear “env-only” product stance) | S | Names → `JIG_SECRET_*` only today. |
| A11 | **Richer condition language** — `&&` / `\|\|` / comparisons | C+S | **Done.** Bounded expression AST, typed numeric comparisons, compound route proofs, module rewriting, and chart support. Plan: [`a11-richer-condition-language.md`](a11-richer-condition-language.md). |
| A12 | **Reset settled runs** — postmortem rewind after `RunFinished` | S | ADR 0008 / engine deferred. |
| A13 | **Help agent on historical runs** — read-only journal/transcript analysis | S | Spec 11 live-run only today. |
| A14 | **Packaging + CI** — release artifacts, brew/`go install` docs, GitHub Actions | C | Build-from-source friction. |
| A15 | **Narrow / mobile terminal contract** — responsive all screens + display sanitization | S | Partial monitor/review fallbacks only. |
| A16 | **Harness seam for standalone chat + Tier-2 monitors** | S | Spec 14 non-goal residual; still Claude-SDK-centric. |

### P2

| ID | Goal | Kind | Notes |
|----|------|------|-------|
| A17 | Forge / PR automation (open PR, push, checks) | C | Local merge gate only. |
| A18 | OTel / Prometheus / OTLP export | C | Tokens/cost stay in TUI + `result.json`. |
| A19 | Remote / distributed workers | S | Single-machine only. |
| A20 | `jig init` / workflow scaffold | C | **Done** (Spec 22). [`jig init` scaffold specification](../specs/22-spec-jig-init-scaffold/22-spec-jig-init-scaffold.md). |
| A21 | Graph export (Mermaid / DOT / SVG) | C | In-TUI chart only. |
| A22 | Run share / anonymized export bundle | C | Local `.jig/runs` only. |
| A23 | Multi-operator shared run store | C | Single local operator. |
| A24 | Notifications (desktop / Slack / webhook on gate or failure) | C | Visual gate chrome only. |
| A25 | Chart crossing-min + gate labels | S | Schema MVP exclusions. |
| A26 | Charm clickable selector rows | S | Keyboard-only. |

---

## B. TUI chrome vs highest-rated TUIs

Peers: **lazygit, k9s, yazi, btop, lazydocker**; agent peers **Claude Code, Codex, OpenCode, Crush**.

Phases 0–2 closed the Lazygit *composition* gap (panels, focus badges, footer honesty,
palette, status line, simple mode, Esc). Remaining distance is operator amenities.

### P0 / P1 — universal amenities

| ID | Goal | Who has it | jig today |
|----|------|------------|-----------|
| B1 | **Clipboard yank / OSC52** — copy run id, step output, selection | lazygit, Crush, nvim | Missing |
| B2 | **Attention signal on gate wait** — terminal bell / optional notify | Claude Code, ops TUIs | Visual only |
| B3 | **Fuzzy command palette** + richer named actions | fzf, k9s, gum, Crush | Substring + key-redispatch |
| B4 | **Which-key / next-keys overlay** after prefixes | Helix, nvim | Silent `gg` only |
| B5 | **Theme skins + light mode** | Almost all admired TUIs | Dark-only Pantera |
| B6 | **Optional mouse** — click-to-focus + wheel (not mouse-first) | lazygit, k9s, yazi, btop | Keyboard-primary by plan |
| B7 | **Remappable keybindings** (`.jig/` or XDG) | lazygit, k9s, Helix, Crush | Hardcoded `*Keys` |
| B8 | **In-monitor run/session switcher** | Crush `ctrl+s`, OpenCode | Esc → Home |

### P1 / P2 — maturity

| ID | Goal | Notes |
|----|------|-------|
| B9 | Panel jump `1–n` + resize | lazygit muscle memory |
| B10 | Richer `.jig/tui.json` (beyond `simple_mode`) | Themes, keys, bell, density |
| B11 | Custom commands / plugins | lazygit `customCommands`, k9s plugins |
| B12 | Suspend `ctrl+z` | lazygit, Crush |
| B13 | In-TUI export / screendump | k9s, glow |
| B14 | Wire standalone `chat` into Home↔Monitor IA **or remove** | Polish D8 left chat out of root |
| B15 | Slash / agent-ops commands while LIVE (`/compact`-like, permissions, model display) | Claude Code / Codex expectation |
| B16 | Context % / compact / rewind UI | Agent peers |
| B17 | Mid-run model switch | Crush / OpenCode |
| B18 | Vim composer mode | Claude Code / Codex niche |

---

## C. Transcript experience — path to best-in-class

### Why this section exists

Spec 15 landed the right *shape*: prose-first, one quiet row per tool exchange,
semantic summaries, bounded expand, thinking collapsed by default. That matches
Zed Agent Panel hierarchy.

What still makes Claude Code, Codex, Crush, and OpenCode feel **easier to read**
is not another schema — it is *feel*: live streaming polish, actionable paths,
copy, turn economics, burst density, and safer noise control.

Current strengths (keep):

- Conversation-first items (`internal/tui/monitor/monitor_transcript_items*.go`)
- Semantic tool one-liners (`monitor_tool_summary.go`) — `Read foo.go`, `$ go test`
- Matched use+result as one row; failed rows with text+glyph (not color-only)
- Page-local search (`/`) + filters (`F`); follow (`f`/`G`); expand-all (`o`)
- Themed Glamour for assistant prose; tool/command output stays verbatim
- File-is-truth JSONL + windowed paging (`internal/transcript`)

### What “absolute best” transcript needs

Ranked. Status: **have** / **partial** / **missing**.

#### Must-haves (do these and the gap vs Claude/Codex mostly closes)

| ID | Goal | Status | Why it changes the feel |
|----|------|--------|-------------------------|
| T1 | **Token-stream polish in the monitor** — show in-progress assistant text with a live cursor; finalize to markdown only when the block completes (same strategy as `internal/tui/chat`) | partial | Peers feel “alive”; jig monitor mostly reloads pages + a typing tail. Flicker-free streaming is the #1 readability difference. |
| T2 | **Copy / yank selected transcript item** — collapsed summary *or* expanded detail via OSC52 / system clipboard | missing | Operators cannot paste evidence into PRs/issues without leaving the TUI. Highest daily friction. |
| T3 | **Open location** — from tool locations / edit paths, open `path:line` in `$EDITOR` or configured opener | missing | Zed/IDE agents make locations actionable; jig renders paths as inert text. |
| T4 | **In-transcript edit cards with optional unified diff** — keep “New code” default; toggle before/after hunks without leaving Transcript | partial | New-code Glamour cards exist; peers show patch impact inline. Full diff today lives mainly in review/diffview. |
| T5 | **Smart burst folding** — presentation-only collapse of consecutive successful tool rows between prose (Claude-style tool storms) | missing | Spec 15 removed heavy Spec 11 group chrome; without *light* burst folding, parallel tool spam still overwhelms. |
| T6 | **Transcript noise & secrets policy** — redact secret-like tokens in collapsed previews; default-hide huge JSON behind “N bytes”; never markdown-interpret tool output | partial | Summaries sanitize; expanded output is verbatim. Collapsed previews can still leak or flood. |
| T7 | **Per-turn / per-exchange timing + tokens** — muted duration/cost on expand or in a density mode | missing | Step/run totals exist on the status line; peers show spend *per turn*, which builds trust mid-run. |
| T8 | **Density + optional metadata modes** — compact / comfortable; optional seq+timestamp+cost chrome without changing durable schema | missing | Spec 15 correctly hid log chrome by default; operators still need a debug density for forensics. |

#### Strong polish (best-in-class, not merely “good”)

| ID | Goal | Status | Notes |
|----|------|--------|-------|
| T9 | **Turn grouping** — visual rhythm for user↔assistant units (spacing/headers without event-log chrome) | missing | Items are block-derived; spacing helpers only. |
| T10 | **Richer tool lifecycle** — start → running (progress/spinner) → done/error with elapsed | partial | `· running` exists; no progress or duration. |
| T11 | **Inline permission / question cards in transcript** (or clear deep-link into Gate) | partial | Questions/gates are Gate panel; tool failures hint in-row. Peers keep approvals in-stream. |
| T12 | **Jump affinities** — next error / next tool / next edit without manually setting filters | partial | `n/N` + `F` filters; no dedicated affinity keys. |
| T13 | **Full-run search index (optional)** — page-local `/` is correct for bounds; offer “search all pages” as an explicit opt-in | partial | By design page-local today. |
| T14 | **Sticky turn / step context header** while scrolling a long page | missing | Easy to lose “where am I” in long steps. |
| T15 | **Virtualized / incremental render** — avoid full-page string rebuild on every key | partial | Windowed I/O yes; viewport still rebuilds large strings. |
| T16 | **Markdown excellence** — tables, fences, soft-wrap, clickable/openable links where terminal allows | partial | Themed Glamour + Chroma; no link actions. |
| T17 | **Thinking presentation parity** — collapsed by default (have); optional streaming “thinking…” affordance without dumping CoT | partial | Expand works; live thinking feel lags peers. |
| T18 | **Parallel-tool fan visualization** — when multiple tools run concurrently, show a compact multi-row or stacked “running” cluster | missing | Related to T5; Codex/Claude communicate parallelism. |
| T19 | **Result truncation UX** — clear “showing 12/400 lines · enter to expand · yank all” | partial | Hard bounds (4KiB / 12 rows / 3-row tail) exist; chrome explaining the bound is weak. |
| T20 | **Retry / generation dividers that read as story beats** — not log separators | partial | Dividers exist for coordinate transitions; can still feel like log chrome. |

#### Explicit non-goals (unless a later spec overturns)

- Changing durable JSONL schema for cosmetics alone (presentation layer first).
- Unbounded history scan to repair tool pairing (Spec 15 invariant).
- Mouse-first transcript selection (keyboard-first; optional mouse is B6).
- Reintroducing Spec 11’s heavy two-level group chrome (prefer T5 light folding).

### Transcript north-star (one paragraph)

An operator watching a LIVE step should see **assistant prose streaming cleanly**,
**tools as a quiet scannable sidebar of activity**, **edits as readable code or
diffs one keystroke away**, **failures obvious without color-only cues**, and
**one key to copy or open any piece of evidence** — without ever feeling like they
are reading a JSONL dump. Density modes and optional metadata exist for forensics;
the default view stays conversation-first.

### Suggested transcript delivery order

1. **T1** streaming polish + **T2** yank — immediate “this feels like Claude/Codex”
2. **T3** open path + **T4** inline diff toggle — actionable evidence
3. **T5** burst folding + **T6** noise/secrets — long runs stay readable
4. **T7** + **T8** economics + density — trust and forensics
5. T9–T20 as follow-on polish

---

## D. Ranked top 20 (cross-cutting)

If only twenty goals get attention:

1. ~~A1 Headless `jig run`~~ **done** — see [`docs/headless.md`](../headless.md)
2. **T1 Transcript streaming polish**
3. **T2 Clipboard yank**
4. A6 Restore Tier-2 monitors
5. A4 / A5 Cursor + Claude ACP resume parity
6. B2 Gate attention bell/notify
7. ~~A2 Thin ops CLI~~ **done** — see [`docs/operations.md`](../operations.md)
8. **T3 Open location**
9. **T5 Smart burst folding**
10. B3 Fuzzy palette + richer actions
11. ~~A3 Mid-crash recovery~~ **done** — Specs 20 and 21: [`20`](../specs/20-spec-mid-crash-recovery/20-implementation-plan.md), [`21`](../specs/21-spec-unfinished-park-reopen/21-implementation-plan.md)
12. **T4 Inline edit/diff cards**
13. A8 Map/foreach fan-out
14. A7 Codex parallel reliability
15. B1 (covered by T2) / B4 Which-key
16. **T6 Noise & secrets policy**
17. B5 Theme skins + light mode
18. **T7 Per-turn tokens/timing**
19. B8 In-monitor run switcher
20. A14 Packaging + CI

---

## E. Already in good shape (do not reopen casually)

- Lazygit-style panel composition, focus badges, contextual footer, `?` help
- Simple / advanced mode (`.jig/tui.json`)
- Non-blocking gates (ADR 0002)
- Spec 15 prose-first transcript item model
- Validate-at-load workflow schema; DAG + bounded routes
- Worktree integration + file-backed reviews
- Transcript-as-truth (bus is liveness only)
- Claude SDK harness feature completeness relative to other backends

---

## F. Related docs

- `docs/workflow-schema.md` — Deferred constructs
- `docs/engine-design.md` — headless / recovery notes
- `docs/specs/15-spec-zed-style-monitor-transcript/` — transcript presentation baseline
- `docs/specs/14-spec-per-step-harness/` — backend/transport matrix
- `docs/specs/19-spec-headless-run/` — headless `jig run` (A1 done)
- `docs/specs/20-spec-mid-crash-recovery/` — mid-execution crash recovery / interrupted workers (A3)
- `docs/specs/21-spec-unfinished-park-reopen/` — reopen needs_input / stopped / integration / pre-crash recovery parks after process death (A3 residual)
- `docs/plans/tui-lazygit-polish/` — chrome polish (phases 0–2 landed)
- `docs/plan-codex-acp-concurrency-diagnostics.md`
- `docs/plan-acp-structured-edit-telemetry.md`
- ADRs 0002, 0003, 0008
