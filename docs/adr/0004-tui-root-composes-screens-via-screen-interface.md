# The TUI root composes screens via a compile-time Screen interface

Status: accepted; implementation pending.

The root model drives four mutually-exclusive screens (Selector, Detail, Runs,
Monitor), and adding another (e.g. a dashboard) is an anticipated, recurring change.
Today each screen is threaded through four separate switch sites in `root.go` —
`WindowSizeMsg` sizing, `engineEventMsg` routing, default message routing, and
`View()` — which already duplicates over four screens and drifts silently when one
site is missed (forget the size line and the screen renders unsized; forget event
routing and it goes stale).

We adopt a minimal `Screen` interface (`SetSize` / `Update` / `View`, plus `Init`
where needed) and a static registry built in `New()`, keeping the typed screen
fields alongside the slice so the root can still reach a screen's specifics
(e.g. `m.monitor.runID`). The four switch sites collapse into loops/lookups, so
adding a screen becomes "implement the interface, register once."

## Why this and not the broader "Component interface" pattern

The general broadcast-to-coexisting-siblings pattern is rejected elsewhere: jig's
screens are swapped, never on-screen together, so a broadcast loop is a no-op at the
root. This ADR adopts only the narrow slice of that idea justified by *existing*
duplication (four screens today, satisfying the Rule of Three) rather than a
speculative future.

## Boundary

Registration is compile-time only. This is deliberately NOT a runtime plugin system
— see [ADR-0003](0003-extensibility-lives-in-engine-and-schema.md).
