# Monitor gates are non-blocking focus regions, not modal dialogs

When a step needs a human (review verdict, block_on input, AskUserQuestion, or a
from="user" prompt), the Monitor renders a **Gate** as a full-width strip beneath
the two panels. The Gate is a third focus region: the user can `tab`/`left`/`right`
away from it to read the Steps list and Transcript while it waits, then focus back
to answer. It does **not** freeze navigation.

This deliberately reverses the prior behavior, where a pending gate force-set the
list mode and captured all input until resolved. That freeze was a bug — a
reviewer could not inspect the transcript that a verdict is *about* without first
answering. We keep the "a gate is never ambiguous" property by auto-focusing the
gate the moment it appears, so answering is still a zero-navigation default; the
difference is that inspecting context is now possible.

Consequence: key routing is per-focused-region (a focused gate/textarea consumes
keys; Steps-focus reads j/k as select; Transcript-focus reads j/k as scroll), and
gate state (pendingReview/pendingInput/pendingQuestion/pendingPrompt) is tracked
independently of which region holds focus.
