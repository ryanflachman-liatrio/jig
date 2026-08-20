# Cursor ACP Research

Cursor natively exposes an ACP (Agent Client Protocol) server via the `agent acp` subcommand, using JSON-RPC 2.0 over stdio, and launched this feature on March 4, 2026.

## Overview

The [Agent Client Protocol](https://agentclientprotocol.com) is an open standard for communication between code editors and AI coding agents, analogous to LSP for language servers. Editors spawn or connect to an agent process over stdio; the protocol carries JSON-RPC 2.0 messages delimited by newlines.

Cursor added first-class ACP support on March 4, 2026, coinciding with its [JetBrains IDE launch](https://cursor.com/blog/jetbrains-acp). The official documentation lives at [cursor.com/docs/cli/acp](https://cursor.com/docs/cli/acp), which as of this writing returns HTTP 503 intermittently but is confirmed to exist by multiple secondary sources that have cached its content.

## ACP server invocation

The Cursor CLI ships an `agent` binary installed to `~/.local/bin/agent`. The ACP subcommand is:

```
agent acp
```

Full path example: `~/.local/bin/agent acp`

To pass an API key on the command line:

```
agent --api-key "$CURSOR_API_KEY" acp
```

Sources: [cursor.com/docs/cli/acp](https://cursor.com/docs/cli/acp) (content confirmed via cached search excerpts and [acpx issue #51](https://github.com/openclaw/acpx/issues/51)); [zed.dev/acp/agent/cursor](https://zed.dev/acp/agent/cursor).

**Zed editor configuration** (from [zed.dev/acp/agent/cursor](https://zed.dev/acp/agent/cursor)):

```json
{
  "agent_servers": {
    "cursor-agent": {
      "command": "agent",
      "args": ["acp"]
    }
  }
}
```

The command is intentionally hidden from the default CLI help output; it is described as intended for custom ACP clients and advanced integrations rather than normal interactive use ([cursor.com/docs/cli/acp](https://cursor.com/docs/cli/acp) excerpts via search).

**Note on binary name:** The binary shipped by Cursor is named `agent`, not `cursor-agent`. Third-party community adapters (see below) use a separate `cursor-agent` binary path that is also installed by Cursor but is a distinct entrypoint. Community sources disagree slightly on which name to use; the official Zed registry page and acpx issue confirm `~/.local/bin/agent acp` is the authoritative form.

## Transport

Cursor's ACP server uses **stdio** (standard input/output), with messages formatted as newline-delimited JSON (NDJSON). This is the ACP specification's primary transport for local agents.

- Messages are JSON-RPC 2.0 objects separated by `\n`; no embedded newlines are allowed within a message.
- Logs are written to stderr only, keeping stdout clean for protocol traffic.
- The connection is full-duplex: both the client and agent can send requests (the client drives prompts; the agent calls back for permission grants, file reads/writes, and terminal operations).

This matches the ACP spec as documented at [agentclientprotocol.com](https://agentclientprotocol.com), which describes local agents as "editor sub-processes via JSON-RPC over stdio." HTTP/WebSocket transport exists in the spec for remote agents but is not used by Cursor's local `agent acp` command.

Sources: [acpx issue #51](https://github.com/openclaw/acpx/issues/51); [zed.dev/acp/agent/cursor](https://zed.dev/acp/agent/cursor); [github.com/coder/acp-go-sdk README](https://raw.githubusercontent.com/coder/acp-go-sdk/main/README.md); [agentclientprotocol.com](https://agentclientprotocol.com).

## Comparison with Zed's claude-code-acp adapter

| | Cursor `agent acp` | `@zed-industries/claude-code-acp` |
|---|---|---|
| What it is | Native ACP server built into Cursor CLI | npm adapter wrapping the Claude Agent SDK |
| How to start | `agent acp` (binary on PATH) | `npx -y @zed-industries/claude-code-acp@latest` |
| Transport | stdio, NDJSON, JSON-RPC 2.0 | stdio, NDJSON, JSON-RPC 2.0 |
| Authentication | `agent login` / `CURSOR_API_KEY` / `--api-key` flag | Anthropic API key (`ANTHROPIC_API_KEY`) |
| Backend | Cursor's proprietary models and infrastructure | Anthropic Claude via the Claude Agent SDK |
| Session resume | `--resume <resumeId>` flag | Handled internally by SDK |
| Package name | N/A (installed with Cursor app) | Renamed to `@agentclientprotocol/claude-agent-acp` as of v0.16.2 |

The Zed adapter (`@zed-industries/claude-code-acp`) is a TypeScript npm package that implements the ACP agent interface on top of Anthropic's Claude Agent SDK. It is spawned via `npx` and speaks the same stdio NDJSON protocol. The package was renamed to `@agentclientprotocol/claude-agent-acp`; [npmjs.com/@zed-industries/claude-code-acp](https://www.npmjs.com/package/@zed-industries/claude-code-acp) notes "This package has been renamed to @agentclientprotocol/claude-agent-acp."

The `acp-go-sdk` claude-code example ([source](https://raw.githubusercontent.com/coder/acp-go-sdk/main/example/claude-code/main.go)) shows exactly how a Go client spawns this adapter:

```go
cmd := exec.CommandContext(ctx, "npx", "-y", "@zed-industries/claude-code-acp@latest")
// … pipes stdin/stdout to acp.NewClientSideConnection
```

**Key architectural difference:** `@zed-industries/claude-code-acp` is a thin ACP wrapper around the Claude Agent SDK — it itself calls Anthropic's API. `agent acp` is Cursor's own proprietary agent exposed over the same wire protocol; connecting to it means you are talking to Cursor's backend, not Anthropic's API directly.

Sources: [github.com/coder/acp-go-sdk example/claude-code/main.go](https://raw.githubusercontent.com/coder/acp-go-sdk/main/example/claude-code/main.go); [npmjs.com/@zed-industries/claude-code-acp](https://www.npmjs.com/package/@zed-industries/claude-code-acp); [github.com/zed-industries/claude-agent-acp](https://github.com/zed-industries/claude-agent-acp).

## acp-go-sdk compatibility

[github.com/coder/acp-go-sdk](https://github.com/coder/acp-go-sdk) is a Go library that implements the ACP client and agent interfaces over stdio. Its `acp.NewClientSideConnection(client, stdin, stdout)` constructor takes any `io.Reader`/`io.Writer` pair — it is transport-agnostic. You supply the pipes from an `exec.Cmd` subprocess.

**Compatibility verdict:** `acp-go-sdk` is compatible with Cursor's `agent acp` server in principle, because both speak JSON-RPC 2.0 over stdio per the shared ACP spec. The claude-code example already demonstrates the pattern: spawn a process, pipe its stdin/stdout, call `conn.Initialize` → `conn.NewSession` → `conn.Prompt`.

To connect to `agent acp` instead of `@zed-industries/claude-code-acp`, you would change only the subprocess command:

```go
cmd := exec.CommandContext(ctx, "agent", "acp")
// Optionally: cmd.Env = append(os.Environ(), "CURSOR_API_KEY=...")
stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()
cmd.Start()
conn := acp.NewClientSideConnection(client, stdin, stdout)
```

No Cursor-specific fork of the SDK is required. The SDK has no built-in Cursor example (confirmed: the `example/` directory contains `agent`, `claude-code`, `client`, and `gemini` — no `cursor` entry). Any incompatibilities would arise from ACP protocol version mismatches or Cursor-specific extension methods rather than transport differences.

Sources: [github.com/coder/acp-go-sdk README](https://raw.githubusercontent.com/coder/acp-go-sdk/main/README.md); [github.com/coder/acp-go-sdk example/claude-code/main.go](https://raw.githubusercontent.com/coder/acp-go-sdk/main/example/claude-code/main.go); [acpx issue #51](https://github.com/openclaw/acpx/issues/51).

## Authentication and handshake

### Authentication

Cursor's ACP server supports three authentication methods ([cursor.com/docs/cli/acp](https://cursor.com/docs/cli/acp); [acpx issue #51](https://github.com/openclaw/acpx/issues/51)):

1. **Interactive login** — run `agent login` once to store credentials; subsequent `agent acp` invocations reuse the session.
2. **Environment variable** — set `CURSOR_API_KEY=<key>` in the process environment before launching `agent acp`.
3. **CLI flag** — `agent --api-key "$CURSOR_API_KEY" acp`.

Cursor's ACP server advertises `cursor_login` as the ACP authentication method during the initialize handshake. If the binary is not authenticated, third-party adapters surface a message directing the user to run `cursor-agent login` ([github.com/blowmage/cursor-agent-acp-npm issue #13](https://github.com/blowmage/cursor-agent-acp-npm/issues/13); [deepwiki.com/roshan-c/cursor-acp](https://deepwiki.com/roshan-c/cursor-acp)).

By contrast, `@zed-industries/claude-code-acp` uses `ANTHROPIC_API_KEY` — a plain environment variable with no interactive login step.

### Protocol handshake

The ACP handshake is the same for all compliant servers — this is specified by the ACP protocol itself ([agentclientprotocol.com](https://agentclientprotocol.com)):

1. Client sends `initialize` request with `protocolVersion` and `clientCapabilities`.
2. Server responds with `agentCapabilities` (including advertised extensions and auth method).
3. Client sends `session/new` with a working directory and optional MCP servers.
4. Server responds with `sessionId`.
5. Client sends `session/prompt` requests; server streams `SessionUpdate` notifications back.

Cursor supports MCP servers defined in `.cursor/mcp.json` (project-level) or a user-level equivalent, which can be passed at session creation ([cursor.com/docs/cli/acp](https://cursor.com/docs/cli/acp) excerpts).

The `acp-go-sdk` claude-code example shows this handshake verbatim:

```go
initResp, _ := conn.Initialize(ctx, acp.InitializeRequest{
    ProtocolVersion:    acp.ProtocolVersionNumber,
    ClientCapabilities: acp.ClientCapabilities{Fs: acp.FileSystemCapabilities{
        ReadTextFile: true, WriteTextFile: true,
    }},
})
newSess, _ := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: mustCwd(), McpServers: []acp.McpServer{}})
conn.Prompt(ctx, acp.PromptRequest{
    SessionId: newSess.SessionId,
    Prompt:    []acp.ContentBlock{acp.TextBlock(line)},
})
```

Source: [github.com/coder/acp-go-sdk example/claude-code/main.go](https://raw.githubusercontent.com/coder/acp-go-sdk/main/example/claude-code/main.go).

## Gaps and unverified claims

The following could not be verified from primary sources during this research:

1. **Exact flag list for `agent acp`**: The cursor.com/docs/cli/acp page returned HTTP 503 on every direct fetch attempt. All flag details (`--api-key`, hidden subcommand status) come from community forum excerpts and cached search snippets, not the docs page itself. The canonical reference is [cursor.com/docs/cli/acp](https://cursor.com/docs/cli/acp).

2. **Full `agent --help` output**: The complete list of flags accepted by `agent acp` has not been retrieved from a primary source. The `--resume <resumeId>` flag is documented in adapter source code ([deepwiki.com/roshan-c/cursor-acp](https://deepwiki.com/roshan-c/cursor-acp)) but not confirmed in official docs.

3. **ACP protocol version**: Which version of the ACP spec Cursor's server implements (v1 vs v2) could not be confirmed. The agentclientprotocol.com documentation index references both, but the page content was not fully retrieved.

4. **`@zed-industries/claude-code-acp` full README**: npmjs.com returned HTTP 403. Details about this package came from search results and the acp-go-sdk source code, not from the package page or GitHub README directly. The package is confirmed renamed to `@agentclientprotocol/claude-agent-acp`.

5. **`acp-go-sdk` Cursor example**: There is no official example in the SDK for connecting to Cursor's `agent acp`. The compatibility assessment above is inferred from the shared protocol spec and the claude-code example pattern.

6. **Windows/Linux install path**: `~/.local/bin/agent` is confirmed for macOS/Linux by community sources. The Windows equivalent was not found.

7. **Session modes**: "plan" and "ask" modes are mentioned in multiple sources as available via slash commands or flags, but the exact invocation syntax for ACP clients was not found in primary docs.
