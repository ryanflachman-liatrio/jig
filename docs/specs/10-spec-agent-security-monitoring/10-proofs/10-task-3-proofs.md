# Task 3.0 Proofs — Tier-1 deterministic tool-use firewall and transcript redaction

## Task Summary

This task implements the hard prevention boundary of the security layer: a
synchronous, LLM-free `Guard.Check` inside `AgentExecutor` that blocks dangerous
tool calls before the SDK executes them, and a redaction filter in `captureStream`
that scrubs detected secrets from `transcript.jsonl` even for denied calls.

Key decisions made during implementation:
- **Seam**: The SDK's `WithCanUseTool` callback only fires under `PermissionModeDefault`.
  `acceptEdits` auto-approves writes without invoking the callback. The runner
  forces `PermissionModeDefault` whenever `req.Guard != nil` (see `2.2-seam-probe.md`).
- **Findings in `captureStream`, blocking in callback**: The callback returns
  `NewPermissionResultDeny(reason)` to block the tool; finding production and
  transcript redaction happen in `captureStream` when it processes the
  AssistantMessage. This keeps the callback minimal and makes the detection path
  unit-testable without a live SDK connection.
- **Rule priority**: `denied-shell` (escalate) is checked before `outbound-host`
  (block) because pipe-to-shell patterns (RCE risk) outrank outbound-host violations.

## What This Task Proves

- `Guard.Check` correctly denies/escalates the five representative rule cases
  from the task spec (PEM key, clean Go code, non-allowlisted host, safe Bash,
  rm -rf /).
- `RedactJSON` replaces detected secret patterns in raw JSON without modifying
  clean payloads or non-write tool inputs.
- `captureStream` with an active guard writes the redacted form to
  `transcript.jsonl` — the raw key never lands on disk.
- A blocked tool call produces exactly one `SecurityFinding` ctrl event via
  `rep.Finding` and one `Finding` record in `findings.jsonl`.
- With no guard (`req.Guard == nil`) the transcript is byte-identical to today.
- The `Reporter` interface now has a `Finding(SecurityFinding)` method routed to
  the ctrl channel (must-not-drop).

## Evidence Summary

- `TestGuardRules` (sentinel): PASS — all 12 table cases pass, including all 5
  from the task spec.
- `TestGuardNilAllowlist` (sentinel): PASS — empty allowlist disables outbound
  rule; secret-in-write still fires.
- `TestRedactJSON` (sentinel): PASS — known secret replaced, clean payload
  unchanged, non-write tool unchanged.
- `TestBlockedAndRedacted` (runner): PASS — scripted SDK channel; Write with AWS
  key → transcript redacted, 1 SecurityFinding emitted, 1 finding on disk,
  clean Write unchanged.
- Full suite: 302 tests, 0 failures (16 new tests: 15 sentinel + 1 runner).
- `go vet ./...` clean; `gofmt -l -w .` clean.

## Artifact: TestGuardRules and TestGuardNilAllowlist

**What it proves:** `Guard.Check` applies the three rules in the correct priority
order and returns the right Action/Monitor for each case.

**Why it matters:** The guard is the primary hard-prevention boundary. If any rule
misfires (allowing a blocked case or blocking a clean case), secrets can leak or
legitimate agent work is disrupted.

**Command:**
```
go test ./internal/sentinel/ -v -count=1 -run "TestGuardRules|TestGuardNilAllowlist|TestRedactJSON"
```

**Result summary:** All 15 tests pass. PEM key → blocked (secret-in-write); clean
Go → allowed; AKIA key → blocked; non-allowlisted WebFetch → blocked; rm -rf / →
escalated (denied-shell); curl piped to sh → escalated (denied-shell, priority over
outbound-host); curl to allowlisted subdomain → allowed.

```
=== RUN   TestGuardRules
=== RUN   TestGuardRules/Edit_writing_PEM_private_key_→_deny
=== RUN   TestGuardRules/Edit_writing_clean_Go_code_→_allow
=== RUN   TestGuardRules/Write_with_AWS_AKIA_key_→_deny
=== RUN   TestGuardRules/WebFetch_to_non-allowlisted_host_→_deny
=== RUN   TestGuardRules/WebFetch_to_allowlisted_host_→_allow
=== RUN   TestGuardRules/Bash_go_test_→_allow
=== RUN   TestGuardRules/Bash_rm_-rf_/_→_escalate
=== RUN   TestGuardRules/Bash_chmod_777_→_escalate
=== RUN   TestGuardRules/Bash_curl_piped_to_sh_→_escalate
=== RUN   TestGuardRules/Bash_curl_to_non-allowlisted_host_(no_pipe)_→_deny
=== RUN   TestGuardRules/Bash_curl_to_allowlisted_subdomain_→_allow
=== RUN   TestGuardRules/Read_tool_(not_a_write_tool)_→_always_allow
--- PASS: TestGuardRules (0.00s)
    --- PASS: TestGuardRules/Edit_writing_PEM_private_key_→_deny (0.00s)
    --- PASS: TestGuardRules/Edit_writing_clean_Go_code_→_allow (0.00s)
    --- PASS: TestGuardRules/Write_with_AWS_AKIA_key_→_deny (0.00s)
    --- PASS: TestGuardRules/WebFetch_to_non-allowlisted_host_→_deny (0.00s)
    --- PASS: TestGuardRules/WebFetch_to_allowlisted_host_→_allow (0.00s)
    --- PASS: TestGuardRules/Bash_go_test_→_allow (0.00s)
    --- PASS: TestGuardRules/Bash_rm_-rf_/_→_escalate (0.00s)
    --- PASS: TestGuardRules/Bash_chmod_777_→_escalate (0.00s)
    --- PASS: TestGuardRules/Bash_curl_piped_to_sh_→_escalate (0.00s)
    --- PASS: TestGuardRules/Bash_curl_to_non-allowlisted_host_(no_pipe)_→_deny (0.00s)
    --- PASS: TestGuardRules/Bash_curl_to_allowlisted_subdomain_→_allow (0.00s)
    --- PASS: TestGuardRules/Read_tool_(not_a_write_tool)_→_always_allow (0.00s)
=== RUN   TestGuardNilAllowlist
--- PASS: TestGuardNilAllowlist (0.00s)
=== RUN   TestRedactJSON
--- PASS: TestRedactJSON (0.00s)
PASS
ok  	jig/internal/sentinel	0.188s
```

## Artifact: TestBlockedAndRedacted

**What it proves:** End-to-end path from scripted AssistantMessage → guard
detection → transcript redaction → finding production → SecurityFinding ctrl event.
With no raw secret reaching disk and the clean Write block unchanged.

**Why it matters:** This is the primary integration proof for the firewall. It
confirms that all three outputs (redacted transcript, findings.jsonl entry,
SecurityFinding event) materialize together for a single blocked tool call.

**Command:**
```
go test ./internal/runner/ -v -count=1 -run "TestBlockedAndRedacted"
```

**Result summary:** PASS. The transcript block for the Write containing the AWS
key has `[aws-key:…MPLE]` in the Input field. The raw key `AKIAIOSFODNN7EXAMPLE`
is absent. The clean Write block is unmodified. One SecurityFinding with
`Tier=guard, Action=blocked, Monitor=secret-in-write` was emitted. One finding on
disk with the same properties and no raw key in Detail.

```
=== RUN   TestBlockedAndRedacted
--- PASS: TestBlockedAndRedacted (0.00s)
PASS
ok  	jig/internal/runner	0.192s
```

## Artifact: Full test suite

**What it proves:** No regressions from the new sentinel, engine, and runner code.

**Command:**
```
go test ./...
```

**Result summary:** 302 tests, 0 failures (16 new: 15 sentinel guard tests + 1
runner integration test). All pre-existing tests continue to pass.

## Reviewer Conclusion

The Tier-1 firewall is in place:
- `sentinel.Guard` applies three rules (secret-in-write, denied-shell,
  outbound-host) in priority order, returning typed `Decision` values.
- `internal/sentinel/rules.go` implements the starter ruleset covering AWS/GCP/GitHub
  tokens, PEM keys, high-entropy strings, dangerous shell patterns, and allowlist-based
  outbound control.
- The runner forces `PermissionModeDefault` and registers `WithCanUseTool` when
  `req.Guard != nil`, blocking denied tools before execution.
- `captureStream` redacts secret patterns from tool_use inputs before writing to
  `transcript.jsonl` and produces `Finding` records and `SecurityFinding` ctrl events.
- The `Reporter` interface has a new `Finding(SecurityFinding)` method routed through
  the ctrl channel (must-not-drop). All existing Reporter implementors updated.
- With no guard, the transcript is byte-identical to the pre-firewall path.
