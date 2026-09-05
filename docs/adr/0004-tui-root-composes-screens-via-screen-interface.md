# The TUI root composes screens via a compile-time Screen interface

Status: accepted; implementation pending (interface). Screen graph amended 2026-09
for Lazygit Phase 0 Home.

The root model drives two mutually-exclusive top-level surfaces — **Home**
(workflows + runs dual-pane, with Detail/chart as a Home-owned overlay) and
**Monitor** — and adding another (e.g. a dashboard) is an anticipated, recurring
change. Today each surface is threaded through separate switch sites in
`root.go` / `root_update.go` — `WindowSizeMsg` sizing, engine-event routing,
default message routing, and `View()` — which duplicates and drifts silently when
one site is missed.

We adopt a minimal `Screen` interface (`SetSize` / `Update` / `View`, plus `Init`
where needed) and a static registry built in `New()`, keeping the typed screen
fields alongside the slice so the root can still reach a screen's specifics
(e.g. `m.monitor.RunID`). The switch sites collapse into loops/lookups, so
adding a screen becomes "implement the interface, register once."

Phase 0 deliberately keeps the switch-based root while landing Home↔Monitor;
this ADR's interface remains the follow-up when a third top-level surface (or
more switch-site drift) justifies it.

## Why this and not the broader "Component interface" pattern

The general broadcast-to-coexisting-siblings pattern is rejected elsewhere: jig's
screens are swapped, never on-screen together, so a broadcast loop is a no-op at the
root. This ADR adopts only the narrow slice of that idea justified by *existing*
duplication (Rule of Three) rather than a speculative future.

## Boundary

Registration is compile-time only. This is deliberately NOT a runtime plugin system
— see [ADR-0003](0003-extensibility-lives-in-engine-and-schema.md).
