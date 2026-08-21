# Monitor gates are non-blocking focus regions, not modal dialogs

When a step needs a human (review verdict, block_on input, AskUserQuestion, or a
from="user" prompt), the Monitor advertises the pending **Gate** in a compact
action bar beneath the two panels. The Gate is a third focus region: focusing it
opens the full controls as an overlay, while `tab`/`left`/`right` can move away to
read the Steps list and Transcript. It does **not** freeze navigation.

This deliberately reverses the prior behavior, where a pending gate force-set the
list mode and captured all input until resolved. That freeze was a bug — a
reviewer could not inspect the transcript that a verdict is *about* without first
answering. New gates are queued without stealing focus; their numbered queue
position keeps the pending interaction unambiguous.

The overlay does not participate in vertical layout. Reserving the full gate
height permanently left too little transcript space at 80×24, while resizing the
panels only when a gate opened would move the content being reviewed. A one-line
bar plus an overlay keeps viewport dimensions and scroll offsets stable in both
states.

Consequence: key routing is per-focused-region (a focused gate/textarea consumes
keys; Steps-focus reads j/k as select; Transcript-focus reads j/k as scroll), and
gate state (pendingReview/pendingInput/pendingQuestion/pendingPrompt) is tracked
independently of which region holds focus.

Structured agent questions use one shared panel in the Monitor and help-agent
modal. The panel owns draft answers, long-list scrolling, multi-select state,
“Other” text, and a final editable review screen. Its parent owns queueing and
request-ID routing. This keeps drafts intact while users inspect other regions
and prevents transport-specific Claude or ACP payloads from leaking into TUI
state.

Queue navigation and transcript selection remain independent, but a focused
step-level gate offers `ctrl+o` as an explicit context jump. The monitor saves
the prior Steps and Transcript selection, opens the gate's transcript (or review
diff), and uses `ctrl+o` again to return without resolving the gate. Run-level
merge approvals identify their branch or scope instead because they have no
owning workflow step.
