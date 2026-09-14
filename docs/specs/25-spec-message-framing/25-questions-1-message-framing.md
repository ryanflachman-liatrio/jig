# 25 Questions Round 1 - Message Framing

Please answer each question below (select one or more options, or add your own
notes). Feel free to add additional context under any question.

Context from pre-spec investigation (epic slice 09, Q-09.1): jig's transcript
`Entry` has **no** operator-vs-synthetic provenance field. RoleUser *text*
entries are written from exactly two places today:

- `internal/runner/agent.go:311` — the operator-typed resume/guidance message
  (`req.Message`), recorded when a session resumes.
- `internal/harness/acp.go:463` — the ACP harness's engine-injected
  structured-output retry prompt, emitted as a user text turn.

The Claude harness never emits user-turn text at all (it processes only
`ToolResultBlock` in user messages), and the step's assembled prompt/context
goes to the `input.md` artifact, not the transcript. omp's "collapse by
provenance" model therefore has no existing signal to key off; adding one is a
transcript-format change.

## 1. Collapse Trigger (resolves slice Q-09.1)

What should decide that a text item renders as a collapsed summary row instead
of full markdown?

- [x] (A) Size threshold only: collapse any eligible text block whose raw
      content exceeds a named threshold. No transcript format change.
- [ ] (B) Add a provenance field to `transcript.Entry` (set by the two writers
      above), collapse synthetic entries always, and keep a size threshold as a
      fallback for old transcripts that lack the field.
- [ ] (C) Provenance only (exact omp behavior): synthetic always collapses,
      operator input never does. Requires the format change and leaves
      pre-existing transcripts without any collapse behavior.
- [ ] (D) Other (describe)

**Current best-practice context:** omp collapses by provenance, but its
transcript records attribution at write time. jig's format is deliberately
unversioned and read-tolerant (`internal/transcript/transcript.go`), so a new
field is cheap to add — but every reader still needs a fallback for entries
written before the field existed, which means the threshold path must be built
in full either way.

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- In a user-role turn, size is an effective proxy for provenance: operator
  resume messages are typed and small, while anything enormous in a user turn
  is pasted or injected by machinery. `(A)` gets omp's performance win without
  touching the persisted format or the two writers.
- `(B)` and `(C)` grow the spec across `internal/transcript` and
  `internal/runner` for a distinction that today separates only the small ACP
  retry prompt from operator guidance — little payoff for the added surface.
- `(A)` keeps this slice purely a TUI change, matching the epic's slicing so
  far; provenance can be layered on later without undoing anything.

## 2. Which Roles Are Eligible to Collapse

FR-09.4 in the slice says "a text item" without naming a role, but omp never
collapses assistant prose. Which text items may collapse when oversized?

- [x] (A) Only user-role text items. Assistant prose always renders in full,
      matching omp. *(Answered in chat: user-role only.)*
- [ ] (B) Any text item over the threshold, including assistant prose —
      maximal protection against pathological render cost.
- [ ] (C) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- The performance evidence (omp issue #6308) is specifically about large
  injected *inputs*; assistant answers are the primary content an operator
  opened the transcript to read, and auto-hiding a long final answer behind a
  summary row is a UX regression omp deliberately avoids.
- Transcript text blocks are already clamped to 256 KiB at write time
  (`MaxBlockBytes`), so the worst-case assistant render is bounded even
  without collapse.
- `(B)` is defensible on the literal FR text — choose it if you want the lazy
  build to cover every text surface — but it trades away readability of the
  transcript's main content for protection against a case jig has not
  exhibited.

## 3. Collapse Threshold Value and Units

The slice requires a named constant beside the existing budgets in
`monitor_model.go` (`chatExpandMax = 4096`, `chatWindowMax = 300`), expressed
in bytes or lines — note "rendered lines" cannot work, since counting them
would require the very render the collapse is meant to skip, so a line-based
option must count raw newlines.

- [x] (A) Bytes, 4 KiB — aligned with `chatExpandMax`. Anything above this in
      a user turn is almost certainly pasted or injected, not typed.
- [ ] (B) Bytes, 16 KiB — more conservative; a large pasted stack trace in
      operator guidance stays inline, and only clearly synthetic dumps
      collapse.
- [ ] (C) Raw line count (e.g. 200 lines of source text).
- [ ] (D) Other (describe)

**Recommended answer(s):** [(A)]

**Why these are recommended:**

- 4 KiB reuses an existing, already-reasoned budget magnitude in the same
  file, so the constant reads as part of one coherent budget family rather
  than a new arbitrary number.
- Even when a 5 KiB pasted log collapses, the summary row states label, size,
  and line count and one keypress expands it — the failure mode of collapsing
  slightly too eagerly is mild, while `(B)`'s failure mode (a 15 KiB injected
  document rendering through glamour on first paint) is the exact cost this
  slice exists to remove.
- `(C)` behaves inconsistently across content shapes: a 300 KiB single-line
  JSON blob would never collapse under a line rule but is the worst glamour
  input of all.
