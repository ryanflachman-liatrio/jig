# 23 Questions Round 1 - Clipboard Yank

Please answer each question below (select one or more options, or add your own notes). Feel free to add additional context under any question.

## Resolution — accepted in conversation, 2026-09-10

The user accepted the refined proposal with “This looks right.” The decisions below supersede the original answer options; no further answers in this file are required.

- `y` copies the current selectable unit; `Y` copies the entire current content source.
- Transcript: `y` copies the full selected message/tool exchange regardless of collapse state; `Y` copies the selected step's entire recorded transcript beyond the viewport/page.
- Output files, including Markdown, JSON, and diagnostics: `Y` copies the entire selected file; `y` is unavailable because these viewers have no line cursor.
- Review source: `y` copies an explicit range, otherwise the current source line; `Y` copies the document.
- Review diff: `y` copies an explicit range, otherwise the current hunk; `Y` copies the current file's diff.
- Review Markdown preview: `y` copies the current block; `Y` copies the document.
- Runs list: `y` copies the run ID; `Y` is unavailable.
- Copy underlying content with source syntax preserved and presentation chrome omitted. Provide contextual help and explicit size-limit feedback without silent truncation.
- Use OSC52 for the MVP. Preserve editor and confirmation key handling. New file-viewer line selection is deferred.

The formal specification is `23-spec-clipboard-yank.md` in this directory.

## Context

Requested source: `docs/plans/open-goals.md`, B1: clipboard yank / OSC52 for run ID, step output, and selection. T2 further describes copying the selected transcript item's collapsed summary or expanded detail. B6 separately covers optional mouse support.

The SDD assessor found no matching spec; sequence 23 is available. This is Phase 1 — Spec Generation. Scope is appropriate for one spec once the copy targets are resolved. No implementation changes have been made.

Repository findings:

- `internal/tui/runs/update.go` preserves selection by run ID.
- `internal/tui/monitor/monitor_model.go` distinguishes selected steps, output-file rows, and transcript items.
- `internal/tui/monitor/monitor_transcript_items.go` builds transcript items from the loaded page only; tool exchanges may include both input and output.
- `CONTEXT.md` defines a transcript item as the navigation and expansion unit. It is distinct from an arbitrary text range.
- `go.mod` pins Bubble Tea v2.0.8 and includes atotto/clipboard indirectly. Existing clipboard dependency presence does not establish an application copy policy.
- Existing confirmation and review handlers use `y`; copy bindings must respect focus, overlays, and text entry.

## 1. Meaning of selection

What should “copy selection” cover in this spec?

- [ ] (A) The selected transcript item: copy its collapsed summary or expanded detail according to its current state, without TUI borders, colors, or wrapping artifacts. No new text-range selection mode.
- [ ] (B) Option A plus a keyboard text-range selection mode within transcript content.
- [ ] (C) Option A plus mouse drag selection implemented by jig.
- [ ] (D) Other (describe)

**Recommended answer(s):** (A)

**Why these are recommended:**

- A follows T2's explicit selected-item wording and reuses the existing transcript navigation model.
- B adds selection state and interaction requirements; C overlaps the separately tracked B6 mouse feature. Both materially broaden this spec.

## 2. Meaning of step output

In addition to copying a run ID and the selected transcript item, which whole-step copy target should B1 provide?

- [ ] (A) The selected step's full recorded transcript as readable plain text, in source order, including content outside the currently loaded page; include a bounded size policy and an explicit message when too large, with no silent truncation.
- [ ] (B) The selected step's structured result (`result.json`) and the contents of a selected output-file row; exclude whole-transcript copying.
- [ ] (C) Both A and B.
- [ ] (D) Selected transcript items are sufficient for this first version; defer a separate whole-step copy action.
- [ ] (E) Other (describe)

**Recommended answer(s):** (A)

**Why these are recommended:**

- A covers step output for both command and agent steps and gives B1's whole-step target a distinct purpose from T2's item copy.
- B emphasizes artifacts rather than the conversation/output stream. C adds file-content and result-format handling. D is smaller but leaves a separate B1 target deferred.
- Whole-step copying should be an explicit bounded read, preserving the existing rule that ordinary transcript rendering and tool pairing operate only on the loaded page.

## 3. Clipboard delivery

Which clipboard support contract should this version implement?

- [ ] (A) OSC52 through Bubble Tea only, with documented terminal/tmux requirements and feedback that a copy was requested rather than claiming verified clipboard delivery.
- [ ] (B) OSC52 by default plus an explicitly selected local system-clipboard mode for terminals where OSC52 is unavailable; no automatic fallback based on assumed delivery failure.
- [ ] (C) Local system clipboard only; defer OSC52 and remote terminal support.
- [ ] (D) Other (describe)

**Current best-practice context:** Bubble Tea v2 provides native OSC52 clipboard operations. tmux and the outer terminal must permit clipboard operations; terminal support and configuration affect delivery. A clipboard helper running on a remote host does not necessarily address the operator's local desktop clipboard.

**Recommended answer(s):** (A)

**Why these are recommended:**

- A directly addresses B1's OSC52 requirement using the existing TUI framework and supports remote use when the terminal chain permits it.
- B adds configuration and platform-specific support but may be useful if a local fallback is a requirement. C omits the explicitly named OSC52 capability.

## Research notes

Research checked on 2026-09-10. These primary sources are maintained documents rather than fixed standards editions:

- [Bubble Tea v2 release notes](https://github.com/charmbracelet/bubbletea/releases): native clipboard support and `tea.SetClipboard`; use the framework's command path for terminal writes, verifying the exact API against the pinned dependency during planning.
- [tmux Clipboard guide](https://github.com/tmux/tmux/wiki/Clipboard): OSC52 delivery depends on terminal support and tmux clipboard configuration. Document prerequisites without modifying the operator's terminal settings.

Once answers are saved, Phase 1 will define exact payload formatting, empty/oversized/error behavior, focus-safe actions, and observable automated and manual clipboard proofs around the selected scope.
