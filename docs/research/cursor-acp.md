# Cursor ACP server — research notes

**Date:** 2026-08-18  
**Scope:** How to connect to `cursor-agent acp` from Go, and what a `CursorHarness` would need to do differently than `AcpHarness`.

---

## 1. The command

Cursor's CLI ships a hidden subcommand:

```
cursor-agent acp
```

Confirmed by running `cursor-agent --help` (v2026.08.11-e8db854) and grepping the
bundled `index.js` at
`~/.local/share/cursor-agent/versions/2026.08.11-e8db854/`:

```
be.command("acp",{hidden:!0})
  .description("Start the Cursor Agent as an ACP (Agent Client Protocol) server")
  .action(...)
```

Running `cursor-agent acp --help` returns:

```
Usage: agent acp [options]

Start the Cursor Agent as an ACP (Agent Client Protocol) server

Options:
  -h, --help  Display help for command
```

No flags are documented on the `acp` subcommand itself. Flags that control
authentication and endpoint (`--api-key`, `--endpoint`, `-H`, `--auth-token`)
live on the **root** command and are parsed before the subcommand dispatch
(confirmed by reading the bundled `runAcp` caller in `index.js`).

The `hidden:true` attribute means the command does not appear in `--help` output
on the main menu; it is an internal interface intended for editor/tooling
consumption.

**Source:** live binary at `~/.local/bin/cursor-agent` (a shell wrapper pointing
to `~/.local/share/cursor-agent/versions/2026.08.11-e8db854/cursor-agent`), and
the minified bundle `index.js` + `2996.index.js` in that versioned directory.

---

## 2. Transport

**stdio** — same as Zed's npx adapter and Gemini CLI.

From `2996.index.js` (the `src/commands/acp.ts` chunk), the last lines of
`runAcp` are:

```js
const j = C.W(process.stdout);  // writable stream
const R = C.G(process.stdin);   // readable stream
const L = s.Z2(j, R);           // ACP Connection over those streams
new s.Bi((...) => new f.o(...), L);  // AgentSideConnection bound to L
process.stdin.resume();
```

The `@agentclientprotocol/sdk@0.14.1` `AgentSideConnection` is wired to
`process.stdout` (writes) and `process.stdin` (reads), exactly as the
`acp-go-sdk`'s `NewAgentSideConnection(agent, os.Stdout, os.Stdin)` convention
specifies for agents. The connection layer is line-delimited JSON-RPC 2.0 over
these raw pipes — no HTTP server, no Unix socket, no TLS.

The client (jig) spawns `cursor-agent acp` as a subprocess, wires its
`stdin`/`stdout` pipes to the SDK's `NewClientSideConnection`, and exchanges
JSON-RPC messages directly over those pipes. This is **identical** to how
`harness/acp/conn.go` spawns `npx @zed-industries/claude-code-acp@latest` today.

---

## 3. Differences from Zed's npx adapter

| Dimension | Zed npx adapter | Cursor `cursor-agent acp` |
|---|---|---|
| **Spawn command** | `npx -y @zed-industries/claude-code-acp@latest` | `cursor-agent acp` |
| **Runtime** | Node.js via npx (downloads on first run if not cached) | Bundled Node.js at `~/.local/share/cursor-agent/…/node` |
| **Transport** | stdio (line-delimited JSON-RPC 2.0) | stdio (line-delimited JSON-RPC 2.0) |
| **ACP SDK** | `@zed-industries/claude-code-acp` (wraps Claude Agent SDK TypeScript) | `@agentclientprotocol/sdk@0.14.1` |
| **Authentication** | None required — the npx adapter uses the local Claude credentials already configured | `authenticate()` RPC call with `methodId: "cursor_login"` required before `session/new` |
| **`initialize` capabilities** | `loadSession: false` (confirmed from existing `harness/acp` tests) | `loadSession: true`, MCP capabilities `{http: true, sse: true}`, prompt capabilities `{audio: false, embeddedContext: false, image: true}`, session list capability |
| **Session creation guard** | None — any call to `session/new` works immediately | Throws `authRequired` if `authenticate()` has not been called first |
| **Startup latency** | Slow first run (npx cold-start + npm download) | Fast — bundled binary, no network download |
| **Prerequisites** | Node.js + npx on PATH | `cursor-agent` installed (`~/.local/bin/cursor-agent`) |

---

## 4. Authentication handshake

Cursor's ACP server requires an explicit authenticate step before any session
can be created. From `2996.index.js`:

1. **`initialize`** response includes an `authMethods` array with one entry:
   ```json
   { "id": "cursor_login", "name": "Cursor Login",
     "description": "Authenticate using existing Cursor login credentials. ..." }
   ```

2. The client must call **`authenticate`** with `{ "methodId": "cursor_login" }`.

3. On success, `isAuthenticated` becomes `true` in the agent. Any `session/new`,
   `session/load`, or `session/list` call before this returns `authRequired` with
   message `"Authentication required. Please run 'cursor-agent login' first, then
   call authenticate() with methodId 'cursor_login'."`.

4. Authentication checks the local Cursor credential store (set up by
   `cursor-agent login`). If a `CURSOR_API_KEY` env var or `--api-key` flag is
   passed when spawning the process, that is treated as already-authenticated
   (`isAuthenticated = true` immediately), so the `authenticate()` RPC call
   **may be skippable** when an API key is provided at spawn time.

The `acp-go-sdk` v0.13.5 already includes `Authenticate` in the `AgentSideConnection`
dispatch table (see `agent_gen.go`). A Go client using `ClientSideConnection` can
call it with `conn.Authenticate(ctx, AuthenticateRequest{MethodId: "cursor_login"})`.

**Zed's adapter** has no such gate — `session/new` works immediately after
`initialize`. This is the only **protocol-level** difference; everything else is
operational (which binary to spawn, prereqs).

---

## 5. SDK compatibility — `acp-go-sdk` vs `@agentclientprotocol/sdk`

| Property | Value |
|---|---|
| `acp-go-sdk` schema version | 0.13.5 (see `harness/acp/go.mod`) |
| `@agentclientprotocol/sdk` version used by Cursor | 0.14.1 (from bundle path in `2996.index.js`) |
| ACP protocol integer version | **1** for both (Go SDK `ProtocolVersionNumber = 1`; Cursor sends `i.WP` from the same SDK constant) |

The two SDKs track the same protocol spec at `agentclientprotocol.com`. The
Go SDK is maintained by Coder; the TS SDK is the canonical reference. At protocol
version 1 the wire format is stable: line-delimited JSON-RPC 2.0 with the same
`initialize` / `session/new` / `session/prompt` method names.

**The `acp-go-sdk` `NewClientSideConnection` is compatible with Cursor's ACP
server.** No separate client is needed. The only code change required is:

- Spawn `cursor-agent acp` instead of `npx @zed-industries/…`.
- After `Initialize`, call `Authenticate(ctx, AuthenticateRequest{MethodId: "cursor_login"})` before `NewSession`.

The `AuthenticateRequest` / `AuthenticateResponse` types are already present in
`acp-go-sdk@0.13.5` (see `example_agent_test.go` `Authenticate` method on the
agent side; the client-side call is `conn.Authenticate(...)`).

---

## 6. Summary for CursorHarness

A `CursorHarness` that reuses `harness/acp/conn.go`'s `Connect/NewSession/Prompt`
pattern needs:

1. **Different spawn line.** Replace `npx -y @zed-industries/claude-code-acp@latest`
   with the result of `exec.LookPath("cursor-agent")` + `["acp"]` (plus optional
   `--api-key` / `--endpoint` flags forwarded from config).

2. **One extra RPC after `Initialize`.** Call `conn.rpc.Authenticate(ctx,
   AuthenticateRequest{MethodId: "cursor_login"})` before `conn.rpc.NewSession(...)`.
   If a `CURSOR_API_KEY` is set in the environment at spawn time, Cursor may
   already treat itself as authenticated; the `authenticate()` call is still safe
   to make (it's a no-op when already authenticated — confirmed by the source
   reading `isAuthenticated || P || M` as the pre-check).

3. **No structural changes** to `harness/acp/conn.go`. The `Conn` type's
   `NewSession` and `Prompt` methods need no modification.

4. **No new Go SDK dependency.** `acp-go-sdk@0.13.5` works. The 0.14.1 TS SDK
   bump adds no breaking wire changes at protocol version 1.

5. **Prerequisite check.** Fail-fast on `exec.LookPath("cursor-agent")` with a
   clear message (`cursor-agent not installed; run: cursor-agent --version`), just
   as `conn.go` fails fast on missing `npx`.

---

## Sources

All claims are based on primary sources only.

- **Live binary:** `~/.local/bin/cursor-agent` (shell wrapper) and
  `~/.local/share/cursor-agent/versions/2026.08.11-e8db854/cursor-agent` (versioned
  Node bundle), version `2026.08.11-e8db854`.
- **`cursor-agent acp --help`:** direct invocation confirming the subcommand and
  its description.
- **Bundled JS source:** `index.js`, `2996.index.js`, `8096.index.js` in the
  versioned install directory — minified but greppable for key strings
  (`runAcp`, `cursor_login`, `authMethods`, `process.stdin`, `process.stdout`,
  `@agentclientprotocol/sdk@0.14.1`).
- **`acp-go-sdk@0.13.5` source:** Go module cache at
  `/Users/flachmanr/go/pkg/mod/github.com/coder/acp-go-sdk@v0.13.5/` —
  `connection.go`, `example_agent_test.go`, `example_client_test.go`,
  `constants_gen.go`, `README.md`, `schema/version`.
- **`harness/acp/conn.go`:** `/Users/flachmanr/Repos/jig/harness/acp/conn.go` —
  existing Zed adapter spawn for comparison.
- **Gemini CLI example:** `/Users/flachmanr/go/pkg/mod/github.com/coder/acp-go-sdk@v0.13.5/example/gemini/main.go` —
  reference for how `--experimental-acp` flag pattern is used on a native CLI binary
  (Gemini's equivalent of `cursor-agent acp`).
- **ACP protocol spec:** <https://agentclientprotocol.com> (referenced in
  `acp-go-sdk` README; not fetched directly — all protocol facts derived from source).
