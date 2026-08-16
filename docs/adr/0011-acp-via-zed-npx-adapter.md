# Drive Claude over ACP via Zed's npx adapter, not a custom Go bridge

`AcpHarness` (spec 12) reaches Claude over ACP by spawning
`npx -y @zed-industries/claude-code-acp@latest` as a subprocess — Zed's own
Node package that wraps the TypeScript Claude Agent SDK and re-exposes it over
ACP — rather than jig writing its own Go-native bridge from ACP to Claude. This
costs an extra process hop and a new runtime dependency (Node/npx) that only
the ACP path needs; jig's default `ClaudeHarness` path has no such
requirement. We accepted this because Zed's adapter is the proven, actively
maintained reference implementation for ACP↔Claude — building and maintaining
an equivalent bridge ourselves would duplicate that work for no protocol
benefit, and this spec's goal is to prove ACP's plumbing (especially the
permission round-trip) against a trusted backend, not to avoid Node. Revisit
if npx cold-start latency or Node availability proves costly in practice
(tracked as an open question in spec 12).
