# Phase 5 Proof — Markdown preview and review-workspace migration

The review workspace parses top-level Markdown blocks with Goldmark, preserves
source-line ranges for preview comments, caches rendered blocks by width, and
invalidates only that cache on resize. The old review-message API and its
monitor/help-chat routing are removed; docs and examples describe array review
targets and atomic sidecar submissions.

## Evidence

```text
go test ./internal/tui/review -count=1
ok   jig/internal/tui/review

go run ./cmd/jig validate examples/feature.toml
ok: "feature" v1 — 15 step(s)
```
