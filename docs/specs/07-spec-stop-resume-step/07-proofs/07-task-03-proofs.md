# Task 03 Proofs — SDK session id captured at session start

## Task Summary

This task resolves the spec's Open Question 1 and proves the SDK session id is
captured **as early as the SDK surfaces it** — from the first `StreamEvent` (or
the init `SystemMessage`) — so a step stopped mid-turn still carries a resumable
session id, replacing the previous capture-on-`ResultMessage`-only behavior.

## Open Question 1 — resolved

With partial messages enabled (`buildOptions` sets `WithIncludePartialMessages(true)`),
`StreamEvent.SessionID` (SDK `internal/shared/message.go:359`) is populated on
**every** stream event, well before the terminal `ResultMessage`. The init
`SystemMessage` carries `session_id` in its preserved `Data` too. **The session
id is therefore available pre-`ResultMessage`, so resume does not need to degrade
to a fresh restart** — the degrade remains only as the documented fallback for
the (unusual) case where no id is surfaced at all.

## What This Task Proves

- A stream that carries a session id and then closes **before** any
  `ResultMessage` (exactly what a cancelled worker sees) still returns a Result
  with that `SessionID`.
- The init `SystemMessage` is honored as an early session-id source.

## Evidence Summary

`TestSessionIDCapturedAtStart` and `TestSessionIDCapturedFromSystemMessage` pass.

## Artifact: early session-id capture tests

**Command:** `go test ./internal/runner -race -run 'TestSessionIDCapturedAtStart|TestSessionIDCapturedFromSystemMessage' -v`

**Artifact path:** `B2.0-session-id-at-start.txt`

**Result summary:** both PASS. The connection-closed (cancel) path returns
`SessionID == "sess-early"` from a `StreamEvent` with no `ResultMessage`, and
`"sess-sys"` from an init `SystemMessage`.

```
--- PASS: TestSessionIDCapturedAtStart (0.00s)
--- PASS: TestSessionIDCapturedFromSystemMessage (0.00s)
```

## Reviewer Conclusion

The session id survives a mid-turn stop, which is what makes resume-as-continue
(Task 04) possible rather than a forced fresh restart.
