# Codex ACP compatibility gate

**Date:** 2026-08-25  
**Installed CLI:** `codex-cli 0.149.1`  
**Adapter:** `@agentclientprotocol/codex-acp@1.6.2`  
**Status:** supported through the ACP adapter; Codex CLI has no native ACP
subcommand in this release.

## Probe

The required invocation was run against the locally installed CLI:

```
codex acp --help
```

It returned the root `Codex CLI` help, whose command list does not contain an
`acp` subcommand. It did not print ACP-specific usage or describe an ACP stdio
server. `acp` is therefore not a verified native subcommand in this release;
the apparent zero exit status is not evidence that an ACP server can be
started.

The adapter supplies the ACP stdio server instead:

```
npx -y @agentclientprotocol/codex-acp@1.6.2
```

It starts the Codex App Server and translates ACP requests and events. jig
uses the operator's pre-existing Codex CLI login and sends no credentials or
authentication configuration. The adapter's initialization advertises auth
methods, but session creation relies on that existing login; a missing login
fails with instructions to run `codex login`.

## Product decision

`backend = "codex"` is supported only with `transport = "acp"`. jig must not
use `codex exec` or Codex's MCP server as an ACP substitute.

The guarded integration test (`JIG_CODEX_ACP_INTEGRATION=1`) verifies
initialize, session creation in a temporary directory, per-session model
selection, a simple prompt, transcript events, a fresh-process session load,
a resumed prompt, and shutdown. It deliberately leaves these capabilities
disabled until their individual live round trips are proven:

- partial streaming: incremental text or thought chunks;
- permission callbacks: an allow/deny `request_permission` exchange;
- user questions: form elicitation;
- session resume: a successful resume exchange;
- structured output: a live schema plus retry test.

Codex session resume uses ACP `session/load`; the harness fails closed if the
adapter does not advertise it. The adapter's documented text and reasoning
chunks support partial streaming; documented permission requests support
permission callbacks. Form elicitation has not been verified, so user questions
remain disabled. The existing prompt-injected structured-output retry loop is
retained and should be covered by a live schema test before expanding its
claims. `JIG_CODEX_ACP_INTEGRATION=1` enables only the integration test; it
never selects a workflow backend.
