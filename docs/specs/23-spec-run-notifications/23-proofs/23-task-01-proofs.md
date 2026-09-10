# Task 01 Proofs — Notification policy and local readiness

## Task Summary

Task 1 implements FR-01–FR-06: strict root-only notification policy,
project-local reusable profiles, operator-owned bindings, and
`jig notifications check WORKFLOW.toml [--root PATH]`. Delivery, dispatch,
TUI diagnostics, and snapshot/reopen integration remain Tasks 2–4.

## What This Task Proves

- Policy fields inherit by presence; explicit empty lists clear inheritance.
  Routes replace the whole inherited list and duplicate aliases merge events.
- Profiles are found from the root workflow's project, including nested workflow
  directories and the non-Git fallback. Invalid profiles and forbidden nested
  policy declarations fail validation. Root policy survives module expansion.
- Enablement defaults off. Only requested, enabled network destinations resolve
  named secrets. Local checks enforce binding types and HTTPS requirements.
- Invalid local configuration and secret/helper failures produce fixed safe
  status codes. Readiness reports retain requested aliases on config errors.
- Checks return 0 for ready/disabled/empty, 1 for readiness/validation problems,
  and 2 for usage; they have no delivery or subprocess execution capability.

## Evidence Summary

The focused workflow, configuration, and CLI tests pass. The repository test
suite, focused race tests, vet, build, changed-file formatting, and project
workflow validation pass; exact command outputs are linked below. CLI examples
show a shared profile and an override containing only the replacement alias.

## Artifact: Inline policy and local readiness

**What it proves:** Static validation is independent of operator configuration.
Absent local config disables notifications; synthetic named URL configuration
can pass local readiness without exposing the URL.

**Why it matters:** Authors cannot enable delivery or specify network bindings.

**Artifact path:** [1.0/policy-check.txt](1.0/policy-check.txt)

**Result summary:** The inline workflow validates. Checks exit 0 for missing
config and locally ready synthetic config, explicitly disclaim verified delivery.
Temporary fixture paths are replaced with `<temporary-fixture>`.

## Artifact: Two workflows reuse one profile

**What it proves:** The profiled workflow requests `desktop` and `team-alerts`;
the override requests only `ops`. Events remain inherited.

**Why it matters:** Overrides replace complete lists rather than silently merging.

**Artifact path:** [1.0/shared-profile.txt](1.0/shared-profile.txt)

**Result summary:** Both workflows validate and report disabled-by-default
readiness without requesting any secrets.

## Artifact: Validation, failure isolation, and no-send checks

**What it proves:** Table cases exercise valid and invalid policy/configuration,
secret resolution, route filtering, disabled destinations, unsupported/missing
desktop prerequisites, output sanitization, and all three command exit codes.

**Why it matters:** Readiness must be safe to run even with broken local bindings.

**Artifact paths:** [1.0/no-send-check.txt](1.0/no-send-check.txt),
[1.0/quality-checks.txt](1.0/quality-checks.txt)

**Result summary:** `TestNotificationsCheck` records **0 receiver-spy requests**
against a local TLS server. Instead of introducing a fake delivery dependency
just for testing, `Inspection` has no sender or command runner at all. Desktop
prerequisite tests inject executable lookup and environment inspection; the
production path calls only `exec.LookPath` and environment reads, never a helper
process. No real desktop or Slack notification was submitted.

The workflow characterization tests first failed to compile because notification
policy types/accessors did not exist, then passed after implementation. Module
fixtures were corrected to declare the existing required exports and use the
repository's `__` expansion separator.

## Artifact: Repository quality and compatibility

**What it proves:** Existing packages and workflow examples remain valid.

**Why it matters:** Schema changes affect every workflow load, not just readiness.

**Artifact paths:** [1.0/quality-checks.txt](1.0/quality-checks.txt),
[1.0/format-check.txt](1.0/format-check.txt),
[1.0/examples-check.txt](1.0/examples-check.txt),
[1.0/secret-scan.txt](1.0/secret-scan.txt)

**Result summary:** Full tests, focused race checks, vet, build, formatting, and
all `.agents/jig/*.toml` validations succeed. Synthetic resolved URL/token
canaries are absent from captures. The Go build cache was placed in a temporary
directory; tests needed sandbox escalation for local loopback listeners and
existing subprocess fixtures. Adding the profile under `.agents/jig` also needed
sandbox escalation. Both operations were approved and completed.

## Reviewer Conclusion

The first usable slice is complete: authors can validate and share notification
policy, and operators can inspect local readiness without sending messages.
The passing planning audit remains applicable. This proof does not claim live
notification delivery, snapshot behavior, native desktop visibility, or completion
of A24.
