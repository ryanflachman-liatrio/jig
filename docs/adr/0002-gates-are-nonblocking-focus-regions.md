# Monitor gates are non-blocking focus regions, not modal dialogs

When a step needs a human (review verdict, block_on input, AskUserQuestion, or a
from="user" prompt), the Monitor renders a **Gate** as a full-width strip beneath
the two panels. The Gate is a third focus region: the user can `tab`/`left`/`right`
away from it to read the Steps list and Transcript while it waits, then focus back
to answer. It does **not** freeze navigation.

This deliberately reverses the prior behavior, where a pending gate force-set the
list mode and captured all input until resolved. That freeze was a bug — a
reviewer could not inspect the transcript that a verdict is *about* without first
answering. New gates are queued without stealing focus; their numbered queue
position keeps the pending interaction unambiguous.

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
