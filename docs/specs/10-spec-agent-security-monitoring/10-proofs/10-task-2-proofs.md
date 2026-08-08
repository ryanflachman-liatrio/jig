# Task 2.0 Proofs — Finding model, findings.jsonl sink, SecurityFinding event, and journal exhaustiveness guard

## Task Summary

This task establishes the shared data model and persistence foundation for both
security tiers before any producer exists — mirroring the transcript design ("file
is truth, bus is liveness"). It also builds the journal exhaustiveness guard that the
spec's first draft assumed already existed and fixes three pre-existing silent-drop
bugs where `InputRequest`, `PromptRequest`, and `AgentQuestion` fell through to the
"unknown" catch-all in `eventKind` with no corresponding decoder.

## What This Task Proves

- `Redact` never leaks a raw secret into a finding; it stores pattern name + last-4 suffix only.
- `NewFingerprint` correctly deduplicates identical repeats within a step while distinguishing different rules, steps, or offending calls.
- The `findings.jsonl` sink round-trips all three findings in append order; a `""` path is a silent no-op (persistence-off).
- `SecurityFinding` round-trips through `MarshalEnvelope → UnmarshalEnvelope` with the correct `"security_finding"` kind.
- `TestEventExhaustiveness` fails if any `Event` union member has no `eventKind` case or no decoder — and all 18 members (including the 3 previously missing + the new one) now pass.

## Evidence Summary

- `TestRedactionFingerprint` (sentinel): PASS — raw key absent from `Detail`, pattern name + last-4 present, fingerprint deduplication and distinction correct.
- `TestFindingsSinkRoundtrip` (sentinel): PASS — 3 findings written, 3 read back in order; `""` path no-ops cleanly.
- `TestEventExhaustiveness` (engine): PASS — all 18 event types have non-`"unknown"` kinds and matching decoders.
- `TestSecurityFindingJournal` (engine): PASS — `SecurityFinding` round-trips; env.Kind == `"security_finding"`.
- Full suite: 286 tests, 0 failures (4 new tests added).

## Artifact: TestRedactionFingerprint and TestFindingsSinkRoundtrip

**What it proves:** `Redact` never stores raw secrets; `NewFingerprint` deduplicates correctly; the JSONL sink preserves append order; the persistence-off path (`""`) is a silent no-op.

**Command:**
```
go test ./internal/sentinel/ -v -count=1 -run "TestRedactionFingerprint|TestFindingsSinkRoundtrip"
```

**Result summary:** Both tests pass. Redacted string contains `[aws-key:…MPLE]` (not the raw key). Three findings round-trip in order. No-op writer does not error.

```
=== RUN   TestRedactionFingerprint
--- PASS: TestRedactionFingerprint (0.00s)
=== RUN   TestFindingsSinkRoundtrip
--- PASS: TestFindingsSinkRoundtrip (0.00s)
PASS
ok  	jig/internal/sentinel	0.168s
```

## Artifact: TestEventExhaustiveness and TestSecurityFindingJournal

**What it proves:** Every Event union member now has a stable non-`"unknown"` journal kind and a matching decoder. The three pre-existing gaps (`InputRequest`, `PromptRequest`, `AgentQuestion`) are fixed. `SecurityFinding` round-trips correctly.

**Why it matters:** A must-not-drop ctrl event (`SecurityFinding`) riding a journal that silently drops unknown kinds is a contradiction. The exhaustiveness guard makes that invariant testable.

**Command:**
```
go test ./internal/engine/ -v -count=1 -run "TestEventExhaustiveness|TestSecurityFindingJournal"
```

**Result summary:** Both tests pass. All 18 event types are covered.

```
=== RUN   TestEventExhaustiveness
--- PASS: TestEventExhaustiveness (0.00s)
=== RUN   TestSecurityFindingJournal
--- PASS: TestSecurityFindingJournal (0.00s)
PASS
ok  	jig/internal/engine	0.175s
```

## Reviewer Conclusion

The security layer's data foundation is in place: the `Finding` type, `Redact`/`NewFingerprint`
helpers, the `findings.jsonl` sink, `FindingsPath` in datastore, the `SecurityFinding` event,
and a real journal exhaustiveness guard. The three pre-existing silent-drop bugs are fixed. All 286 tests pass.
