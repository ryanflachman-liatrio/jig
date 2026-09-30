# Parallel work order — open goals

Execution plan for the open rows in [`open-goals.md`](open-goals.md) and
[`bugs.md`](bugs.md), sized for **2–3 items in flight at once**. Written
2026-09-25 against `main` @ `7d6ce0e`. Goal IDs refer to `open-goals.md`;
treat that file (and code) as truth when they disagree with this one.

This file is working history (git-ignored under `docs/*`), so fresh worktrees
won't have it. Keep it in the main checkout and copy it over when needed.

---

## Why items conflict

Two items can run in parallel when they touch different files. Open work falls
into three lanes plus one barrier:

| Lane | Code it touches | Items |
|---|---|---|
| **1 · Transcript** | `internal/tui/monitor/monitor_transcript*`, `monitor_tool_*`, `monitor_steps.go` | Bug 3, T1, T3–T8, T10–T20 |
| **2 · Backend / engine / CLI** | `internal/harness`, `helpchat`, `runner`, `engine`, `cmd/jig` | A7, A9, A10, A12, A13, A16, A21, Bug 2 remainder |
| **3 · TUI chrome and input** | `shared/keys.go`, `palette`, `root*`, `home`, `internal/config`, `shared/styles.go` | B2, B3, B4, B5, B7, B8, B9, B12 |
| **Barrier** | ~305 files importing `"jig/…"` | A14 module rename |

Rules that follow:

- **One item at a time in lane 1.** Almost every T item edits the same few
  files (`monitor_transcript_items*.go`, the `chatBody()` path), so two at once
  would keep conflicting.
- **Do A14's `go.mod` rename first, alone, while no branches are open.** It
  touches every import, so any open branch would conflict.
- **`internal/config` is the one shared hotspot.** T3 (opener), T8 (density),
  B2 (bell toggle), B5 (theme) and B7 (keys) each add a section there. The
  conflicts are small but real, so merge promptly and rebase often.

## Dependencies

- A16 → A9, A13
- B3 → B7 → B4, B9
- Bug 3 → T1
- B5 → A15
- T features → T15

---

## Waves

### Wave 0 — one item only

- **A14 rename + basic CI.** Rename the module path, and add a GitHub Actions
  job that runs `go vet` and `go test` for the root module and for
  `harness/acp`. CI then protects all the parallel branches that follow. Leave
  goreleaser and brew for later.

### Wave 1

| Lane | Work |
|---|---|
| L1 | **Bug 3** (it shifts transcript line ranges, so fix it before building on them) → **T1** streaming in the Transcript |
| L2 | **A16** — choose the help-chat and monitor backend via `harness.For` (unblocks A9, A13) |
| L3 | **B2** terminal bell → **B3** named palette actions |

### Wave 2

| Lane | Work |
|---|---|
| L1 | **T3** open `path:line` in an editor → **T4** toggle between diff and full source |
| L2 | **A7** Codex parallel reliability (mostly investigation, good background slot) — or **A9** Gemini |
| L3 | **B7** remappable keys (needs B3: remap named actions, not raw keys) |

### Wave 3

| Lane | Work |
|---|---|
| L1 | **T5** fold tool bursts by default → **T6** secret scan of previews |
| L2 | **A12** reset settled runs + **A13** help agent on historical runs (A13 needs A16) |
| L3 | **B4** which-key overlay + **B9** panel jump (both build on B7's keymap registry) |

### Wave 4

| Lane | Work |
|---|---|
| L1 | **T7** per-exchange tokens + **T8** density modes (T8's setting joins `[tui]`) |
| L2 | **A10** secrets stance, **A21** graph export, **Bug 2** remainder (needs a contract decision before code, per `bugs.md`) |
| L3 | **B5** themes and light mode → **B8** in-monitor run switcher (touches `monitor_model.go`; keep it away from T1) |

### Wave 5 — cross-cutting

- **T15** render caching/virtualization. Do it last in lane 1; doing it
  earlier means redoing the caching every time a transcript feature lands.
- **A15** narrow-terminal contract across all screens, after B5 because both
  restyle every screen.
- Remaining **T10–T20** polish.
- Rest of **A14**: release artifacts and brew.

### Parked until specced

A17, A19, A23, B11, B15–B18. These are design questions, not tasks to slot in.

---

## Running 2 vs 3 at once

- **3 at once:** one item per lane.
- **2 at once:** keep **lane 1 always running**. It holds the top-ranked open
  work (T1, T3, T5) and is the bottleneck. Alternate the second slot between
  lanes 2 and 3.
