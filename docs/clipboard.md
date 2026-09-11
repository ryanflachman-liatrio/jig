# Clipboard (`y` / `Y`) — copy from jig's TUI

jig's Home, Monitor, and Review surfaces expose two context-sensitive copy
keys that emit their payload over
[OSC52](https://invisible-island.net/xterm/ctlseqs/ctlseqs.html#h4-Operating-System-Commands):

- **`y`** copies the **current selectable unit** on the focused surface —
  a run ID, a transcript item, a source line or hunk.
- **`Y`** copies the **whole current source** — the recorded transcript, an
  output file, or a review document / file-diff.

jig **never reads** the clipboard, never spawns `xclip` / `pbcopy` /
`clip.exe` helpers, and never logs source content. It simply asks the
terminal to write the payload through OSC52 and shows a nonmodal
`Copy requested: <target> (N bytes)` notice at the bottom of the screen.
Delivery is a property of the terminal, not of jig — see
[Terminal / multiplexer prerequisites](#terminal--multiplexer-prerequisites).

Related tracking rows in [`docs/plans/open-goals.md`](plans/open-goals.md):
**B1 · Clipboard yank / OSC52** and **T2 · Copy / yank selected transcript
item**. Both are implemented by
[spec 23](specs/23-spec-clipboard-yank/23-spec-clipboard-yank.md).

## Mapping at a glance

| Surface                                | `y` copies                        | `Y` copies                                          |
|----------------------------------------|-----------------------------------|-----------------------------------------------------|
| Home · Runs list                       | selected **run ID** (no newline)  | *(not bound)*                                       |
| Monitor · Transcript panel (messages)  | selected **transcript item**      | recorded **step transcript** snapshot               |
| Monitor · Transcript panel (file view) | *(not bound — no line cursor)*    | selected **output file**                            |
| Monitor · Steps overview               | *(not bound)*                     | current step's **recorded transcript**              |
| Review · source browse                 | current **source line**           | whole **document**                                  |
| Review · select-range (`v`)            | inclusive **source range**        | whole **document**                                  |
| Review · Markdown preview              | current **preview block** source  | whole **document**                                  |
| Review · parsed diff                   | current **hunk** (whole diff text)| current **file diff** (metadata + all hunks)       |
| Review · compose / summary editor      | *literal `y` / `Y` in the editor* | *literal `Y` in the editor*                        |

Contextual help under `?` and in the compact footer follows the same
mapping — a disabled binding is either hidden or shown greyed out; jig never
advertises a key that will not run.

## Source semantics

- **Runs and files** copy the raw bytes as recorded on disk (subject to the
  limits below). CRLF line endings and the presence or absence of a final
  newline survive the round trip.
- **Recorded transcript (`Y` in messages / overview)** emits every block
  regardless of the current filter, search, or expand state, in the durable
  block order recorded on disk. Each block is preceded by a
  `== <role> <type> · seq N · gen G · iter I · attempt A ==` header so
  downstream tooling can reconstruct chronology from the coordinates. Tool
  activity is emitted as an indented JSON body under the header. Malformed
  complete records are counted and reported in the notice; an incomplete
  trailing record is ignored.
- **Transcript item (`y` in messages)** copies only what is loaded on the
  current page. Missing tool counterparts, unsupported block types, and
  durably-truncated content are labeled in the notice — jig never
  scans backward through the transcript to reconstruct a partial item.
- **Review documents** are copied from the immutable content of the round
  as it was loaded. A later working-tree change never leaks into the payload,
  and the byte offsets used for line / range / hunk / file slicing walk the
  raw content so terminators (`\n`, `\r\n`) survive.
- **Parsed diffs** copy the exact patch bytes for the current hunk or file
  section, including `diff --git` / `index` / mode / rename metadata and the
  no-newline marker where present. A malformed diff falls back to source
  line / range / document semantics with matching help labels.
- **Copy never modifies state.** Cursor, follow mode, expand/collapse,
  scroll position, review draft, comments, verdict, and reviewed-flag
  selections are unchanged by a copy request. `y` / `Y` inside the review
  composer or summary editor are literal characters, not commands.

## Feedback

- **Success:** `Copy requested: <label> (N bytes)`. The `(N bytes)` count
  is the sanitized payload size that jig sent to the terminal — it is
  **not** proof that the terminal delivered it to the system clipboard.
  A `· K malformed records skipped` suffix reports transcript export
  omissions when applicable.
- **Rejection:** `Copy skipped (<label>): <reason>`. Reasons include:
  - `nothing to copy` — source is empty or would sanitize to empty.
  - `selection is too large to copy` — see the byte limits below.
  - `selection is not valid UTF-8 text` — text files only; jig refuses
    to copy binary or malformed input.
  - `selection contains binary data` — the sanitizer found a NUL byte.
  - `copy target is unavailable` — file missing, review has no active
    document, source shrank between open and read, etc.
  - `another copy is in progress` — a previous request has not yet
    finished; wait for the notice to update, then retry.

## Byte limits

Both limits are declared in
[`internal/tui/shared/clipboard.go`](../internal/tui/shared/clipboard.go);
`docs/clipboard.md` and the code always stay in lockstep — see
`TestClipboardDocumentationContract` in
[`internal/tui/clipboard_documentation_contract_test.go`](../internal/tui/clipboard_documentation_contract_test.go).

- **`ClipboardMaxPayloadBytes = 256 KiB (262144 bytes)`** — the maximum
  sanitized payload jig will send to the terminal in one copy. The check
  is inclusive: exactly the limit succeeds; a single byte more is
  rejected without touching the clipboard.
- **`ClipboardMaxTranscriptScanBytes = 8 MiB (8388608 bytes)`** — the
  maximum raw JSONL a whole-step transcript export may scan for one
  copy. Higher than the payload cap because many records yield little
  copied text; the sanitized serialized output must still fit under
  `ClipboardMaxPayloadBytes`.

There is no partial-payload dispatch. If either cap trips, jig refuses
the copy and the previous clipboard contents on the operator's system
are left untouched.

## Sanitization

Before emission, `shared.PrepareClipboardPayload` runs on every payload:

- Rejects empty raw input, invalid UTF-8, and any NUL byte anywhere in
  the content.
- Strips ANSI CSI / OSC / simple escape sequences and unsafe C0/C1
  control bytes so a paste into another terminal cannot inject cursor
  motions or hyperlinks.
- Preserves `\t`, `\n`, and `\r` so source indentation and line
  terminators survive.
- Rejects if the sanitized result is empty, so sanitization alone
  can never turn a copy into a clipboard-clearing write.

## Terminal / multiplexer prerequisites

OSC52 delivery is opt-in on many terminals and passthroughs. If a
`Copy requested` notice appears but nothing pastes:

- **iTerm2** — Preferences → General → Selection → *Applications in
  terminal may access clipboard*.
- **kitty** — set `clipboard_control write-clipboard` in `kitty.conf`.
- **Alacritty / WezTerm / foot / Ghostty** — OSC52 write is enabled by
  default; no configuration should be needed.
- **tmux** — set `set -g set-clipboard on` in `tmux.conf` and make
  sure the underlying terminal has OSC52 enabled.
- **GNU screen** — pass OSC52 with `termcapinfo xterm* Ms=\E]52;c;%p2%s\007`
  (or upgrade to tmux, which handles this natively).
- **SSH / mosh** — the *client* terminal must own the clipboard; jig
  running on the remote host emits OSC52 which the client's terminal
  interprets. If the client terminal blocks OSC52, no ssh flag will
  restore it.

jig does not probe the terminal, does not shell out to a helper on
paste failure, and does not change the terminal's OSC52 configuration.
Silence-on-write is a terminal / multiplexer setting; jig's contract
ends at "payload sanitized, OSC52 command emitted".

## Troubleshooting

**"Copy requested" appears but the paste is empty.**
Terminal / multiplexer OSC52 write is disabled. See
[Terminal / multiplexer prerequisites](#terminal--multiplexer-prerequisites).
jig never falls back to `xclip` / `pbcopy` / `clip.exe`.

**"another copy is in progress" repeats.**
A previous loader is still running (usually a large transcript export
across a slow disk). Wait for the notice to advance, or press the key
again once the notice clears. jig admits exactly one request at a
time so two copies cannot race into the terminal.

**"selection is too large to copy".**
Either the source (file / transcript / review document) exceeds
`ClipboardMaxPayloadBytes` after sanitization, or a whole-transcript
export exceeds `ClipboardMaxTranscriptScanBytes`. Copy a narrower
window (a single item, a single hunk, or a line range in review)
instead of the whole source.

**"copy target is unavailable".**
The runtime source disappeared between admission and read (deleted
file, evicted review round, closed review workspace), or the item
cursor points at a boundary the loaded page cannot fill (e.g. a
tool result whose paired `tool_use` is on an older page).

**A key seems to do the wrong thing in a modal.**
`y` / `Y` are literal characters inside the delete-confirm overlay
(`y` confirms the delete), inside the help overlay (`y` is unused,
`k`/`j` scroll), inside the command palette (`y` filters), and
inside the review composer and summary editor. jig intentionally
does **not** intercept copy keys behind those modals so the modal's
semantics stay predictable.

## Deferred / not yet implemented

- **Monitor file-line selection.** Copying an arbitrary line range
  from an output file is *not* wired. `Y` on a file copies the whole
  file; there is no line cursor in file view. Tracked as a follow-up
  under B1.
