# Phase 4 Proof — Monitor integration and routing

The Monitor now owns one review workspace per queued review entry. Review
events create a workspace from the immutable session descriptors and any saved
draft; queue navigation leaves each workspace independent, while key and editor
messages are routed only to the active workspace. Drafts use the review store's
atomic writer, failures are shown on the owning entry, and completed workspaces
emit `ReviewSubmissionMsg` for root-level `Run.ResolveReview` routing.

Evidence:

```text
go test ./internal/tui/monitor ./internal/tui/shared ./internal/tui/review -count=1
PASS

go test ./internal/tui/... -race -count=1
PASS

go build ./...
PASS

go vet ./...
PASS

go test ./... -count=1
PASS
```
