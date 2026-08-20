# Drive Claude over ACP via the maintained npx adapter, not a custom Go bridge

`AcpHarness` (spec 12) reaches Claude over ACP by spawning
`npx -y @agentclientprotocol/claude-agent-acp@0.70.0` as a subprocess. The
package wraps the TypeScript Claude Agent SDK and re-exposes it over ACP rather
than jig maintaining its own Go-native bridge. The version is pinned because
elicitation schemas and capability negotiation are wire contracts; silently
installing `latest` could change question behavior between identical runs.

This costs an extra process hop and a Node/npx dependency that only the ACP path
needs; jig's default `ClaudeHarness` path has no such requirement. We accept it
because the adapter is the maintained reference implementation for ACP↔Claude.
Revisit if npx cold-start latency or Node availability proves costly in
practice.
