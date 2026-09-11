# Task 03 recorded limitations

## Pre-existing `internal/harness` failure

**Test:** `TestTier2MixedACPHarnessRunKeepsStepWindowsIsolated`
(`internal/harness/security_integration_test.go`).

**Failure mode:** The assertion in `security_integration_test.go:699`
reports "exfil-pattern missed enabled backend marker" against a trusted
monitor context payload that is unrelated to Monitor UI code.

**Slice-02 scope check:** Reproduced on the slice-02 base commit before
any slice-02 work was applied (git stash → run → git stash pop).
The failure exists on the base branch and is not introduced by slice 02.
Slice 02 changes no harness/backend, wire-format, workflow schema, or
security-monitor code path.

**Ownership:** Belongs to the harness security-monitor scenario. Recorded
here so a reviewer running `go test ./...` on slice-02 changes can
distinguish it from a slice-02 regression.

## PNG rendering pipeline

**Environment note:** The initial `google-chrome --headless` invocation
in this VM hung during startup; the `--headless=new --disable-features=
BackForwardCache --virtual-time-budget=2000` flag combination reliably
produces the PNGs. The HTML captures are the deterministic ground truth;
the PNGs are a rendering of that HTML at a fixed window size.

**Reviewer note:** Reviewers who prefer to regenerate the PNGs from the
`.html` files can do so with the same headless-Chrome command block used
by slice 01; the recorded PNGs match the HTML sources byte-for-byte in
their embedded ANSI conversion.

## Deferred slice-02 scope

Every non-goal listed in the slice-02 spec remains untouched by this
change:

- Detail-section conversion to sub-cards → slice 05.
- Diff badge population → slice 07.
- Grouped-read summarization → slice 08.
- Spinner tick animation in the Icon slot → slice 13.
- Inline argument previews → slice 15.
- Truncation vocabulary change → slice 06.
- Glyph-preset table → slice 14.
- Wire-format, harness, or backend change → outside the epic.
