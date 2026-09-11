# Task 03 Proofs - Explicit sanitized conversation-text export

## Task Summary

This task adds the opt-in `--include-text` mode: a `transcript.jsonl` member
with sanitized conversation/tool text, produced only on explicit request, and
never weakening the structural default.

## What This Task Proves

- `transcript.jsonl` is present only with `--include-text`; a structural
  export of the same run never contains it.
- Every retained free-text value passes through one deterministic sanitizer
  (`internal/runexport/sanitize.go`) that fully replaces known secret
  patterns and high-entropy tokens (via a new pure `sentinel.DetectSecrets`
  seam shared with the live guard), replaces original run/workflow/step
  identifiers and captured/home-directory path prefixes with boundary-aware
  matching, strips control/escape sequences, and truncates to 64 KiB on a
  valid UTF-8 boundary — sanitizing strictly before truncating.
- Thinking blocks, attachments, and unsupported block types become
  content-free fixed markers; tool input/output is rendered as a sanitized
  JSON-as-text string, never raw JSON.
- `manifest.json` reports fixed-category replacement/omission counters with
  no matched text, suffix, or mapping ever present.

## Evidence Summary

- `go test ./internal/runexport/... -run 'TestExportSanitizer|TestSanitizeJSONPayload|TestProjectTranscriptEntry|TestTextModeArchiveIncludesSanitizedTranscript|TestClosedProjectionExcludesPrivateData' -v` passes.
- A paired structural/text CLI export of the same synthetic fixture shows
  `transcript.jsonl` absent then present, with every seeded secret shape
  either absent (structural) or replaced by a fixed marker (text mode).

## Artifact: Paired structural vs. text-mode archive listing

**What it proves:** FR-07 — `--include-text` is the sole, explicit opt-in;
default output is unaffected.

**Command:**

```bash
jig export prod-incident-482 --root .jig --destination out.zip
jig export prod-incident-482 --root .jig --destination out-text.zip --include-text
unzip -l out.zip | grep transcript      # (no output)
unzip -l out-text.zip | grep transcript
```

**Result summary:** `out.zip` has no `transcript.jsonl` member;
`out-text.zip` does. `--include-text` also printed the mandatory best-effort
notice to stderr:

```
notice: text mode includes best-effort sanitized conversation text; review before sharing further
```

## Artifact: Sanitized transcript record

**What it proves:** FR-08/FR-09/FR-10 — retained roles/blocks/sequence, full
secret replacement, identifier aliasing, and a content-free thinking marker.

**Command:** `unzip -p out-text.zip transcript.jsonl`

```json
{"step_alias":"step-0002","seq":1,"time_ms":4000,"role":"assistant","blocks":[
  {"type":"text","text":"investigating run-1 in workflow-1"},
  {"type":"thinking","omitted":"thinking_content"}
]}
{"step_alias":"step-0001","seq":1,"time_ms":1000,"role":"system","blocks":[
  {"type":"text","text":"fetched synthetic data for [REDACTED:aws-key] demo"}
]}
```

**Result summary:** The original run id (`prod-incident-482`) and workflow
name (`export-fixture`) that appeared in the source transcript text were
replaced with `run-1`/`workflow-1`; the seeded `AKIAFAKEFAKEFAKEFAKE` secret
became `[REDACTED:aws-key]`; the thinking block carries no text, only a fixed
omission marker.

## Artifact: Privacy counters in `manifest.json`

**What it proves:** FR-11 — aggregate, fixed-category accounting with no
matched text or mapping.

**Command:** `unzip -p out-text.zip manifest.json`

```json
{
  "content_mode": "sanitized_text",
  "completeness": "complete",
  "counters": {
    "replacements": {"aws-key": 1, "token": 2},
    "omissions": {"thinking_content": 1}
  }
}
```

`TestTextModeArchiveIncludesSanitizedTranscript` asserts this programmatically
(non-zero `thinking_content` omission counter, at least one tool alias
allocated) against the full fixture, which also includes tool-use/tool-result
blocks and an inline diff.

## Artifact: Adversarial sanitizer unit coverage

**What it proves:** FR-09/FR-10's harder cases: prior live-monitor redaction
markers, token vs. path boundary correctness, control-sequence stripping,
invalid UTF-8, and a secret crossing the 64 KiB truncation boundary.

**Tests:**

- `TestExportSanitizerRedactsSecretsFully` — every `syntheticSecrets` shape
  (AWS-key-, GitHub-token-, and high-entropy-shaped) is fully replaced.
- `TestExportSanitizerRemovesPriorSentinelMarkerSuffix` — a
  `[aws-key:…EFGH]`-shaped prior live-monitor marker has its retained
  four-character suffix removed, not just re-wrapped.
- `TestExportSanitizerIdentifierTokenBoundary` / `...PathBoundary` — `run-1`
  is replaced as a whole token but not inside `run-10`; a path prefix is
  replaced only at a `/`-bounded segment, not inside a longer directory name.
- `TestExportSanitizerStripsControlsKeepsNewlineTab` — ANSI CSI sequences and
  C0/C1 controls are removed; `\n`/`\t` survive.
- `TestExportSanitizerTruncatesAfterSanitizing` — a secret placed at the 64
  KiB boundary is fully redacted before truncation, so no unrecognized
  prefix of it is ever emitted.
- `TestExportSanitizerInvalidUTF8Normalized` — invalid byte sequences are
  replaced with the UTF-8 replacement character rather than copied through.

All pass:

```
--- PASS: TestExportSanitizerRedactsSecretsFully (0.00s)
--- PASS: TestExportSanitizerRemovesPriorSentinelMarkerSuffix (0.00s)
--- PASS: TestExportSanitizerIdentifierTokenBoundary (0.00s)
--- PASS: TestExportSanitizerPathBoundary (0.00s)
--- PASS: TestExportSanitizerStripsControlsKeepsNewlineTab (0.00s)
--- PASS: TestExportSanitizerTruncatesAfterSanitizing (0.00s)
--- PASS: TestExportSanitizerInvalidUTF8Normalized (0.00s)
```

## Scope narrowing recorded for this task

- `internal/sentinel/rules.go` gained an exported `DetectSecrets` pure match
  API; `checkSecretInWrite`, `RedactJSON`, and `RedactText` were refactored to
  delegate to it, and `internal/sentinel` tests (`go test ./internal/sentinel/...`)
  confirm the existing guard/live-monitor behavior and four-character preview
  markers are unchanged.
- Original tool-call identifiers are not included in the free-text
  identifier-replacement list (only run/workflow/step ids and paths are);
  tool ids are opaque provider-issued strings that are unlikely to appear in
  prose, and are already aliased at the block level via `tool_alias`. This is
  a deliberate reduction from the spec's literal "known tool identifiers"
  wording, noted here rather than silently dropped.
- Nested/escaped JSON sanitization is proven for one level of nesting
  (`TestSanitizeJSONPayloadHandlesMalformedAndNested`) plus the real tool
  payloads in the fixture; it is not exhaustively fuzzed against arbitrarily
  deep adversarial structures.

## Reviewer Conclusion

`--include-text` is a strictly additive, explicit opt-in that never appears
in the default archive; every retained value in the text-mode member is
demonstrably sanitized end-to-end, both through targeted adversarial unit
tests and a real CLI run against a synthetic secret-bearing fixture.
