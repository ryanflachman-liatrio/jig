# Implementation Plan: Spec 18 Phase 0

## Approach

Compose Home in `internal/tui` from existing `selector` + `runs` children; demote
Detail to an overlay; retarget Monitor leave to Home; extend first-wait focus
consumption; align footers to CompactHint.

## Delivery order

1. Home dual-pane + ADR 0004 amend + root tests
2. Esc/`q` leave bindings + help copy + dirty-leave confirm
3. Home/Monitor CompactHint footers + golden width tests
4. Gate first-wait for all kinds + GATE title chrome

## Primary files

| Area | Files |
|---|---|
| Spec / ADR | `docs/specs/18-…`, `docs/adr/0004-…` |
| Home / root | `internal/tui/root.go`, `root_update.go`, `home.go`, `home_test.go` |
| Selector / runs | `selector/*`, `runs/*`, `detail/keys.go` |
| Monitor leave / focus | `monitor/keys.go`, `msgs.go`, `monitor_update.go`, `monitor_events.go`, `monitor_gate_view.go`, `monitor_view.go` |
| Shared footer | `shared/keys.go` (FooterBar helper if needed) |
