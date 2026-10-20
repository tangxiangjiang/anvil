# 43 — MCP management: status, a manager, and sign-in

**Milestone focus:** 0.44. Statuses live in
[m_0.44_mcp_management.md](../../roadmaps/v1.0/m_0.44_mcp_management.md).

## 1. Today

MCP has been in the SDK since 0.3 and wired into the editor in 0.40.6. What exists is real but
narrow, and 0.40.6 wrote the narrowness down as a contract rather than an accident:

- The internal client connects stdio, HTTP and legacy SSE servers, performs `initialize`,
  lists tools once and caches them ([client.go](../../../source/internal/mcp/client.go),
  [remote.go](../../../source/internal/mcp/remote.go)). There is no connection state, no
  reconnect, no pagination, no `401` handling and no OAuth.
- `MCPServer` in the config parser carries `command`, `args`, `env`, `url` and `headers`
  ([mcpjson.go](../../../source/internal/settings/mcpjson.go)). There is no `enabled` or
  `transport`, and decoding into a struct drops every unknown field.
- Every `Agent` starts its **own** MCP sessions, and any start failure aborts `Create`
  ([agent.go](../../../source/agent.go)). One mistyped server makes the session unusable.
- `ListMCPServers` and the host's `mcp.list` describe *configuration*: name, transport and the
  file that declared it ([mcp.go](../../../source/mcp.go),
  [p_0.40.6.md](../../roadmaps/v1.0/m_0.40/p_0.40.6.md)). The wire has no field for
  "connected" because the host could not observe one.
- The editor draws a read-only card and offers no command, view or write path.

Chapter [39](39-editor-alignment.md) listed "MCP OAuth, remote transports beyond what the SDK
supports" as a non-goal, and chapter [09](09-extensibility.md) said OAuth stays out of scope.
This chapter supersedes those sentences: the capability is now designed, and 0.44 delivers it.

## 2. One runtime, many agents

### 2.1 Why the connections move out of the agent

A manager must answer "is this server up?" when no conversation is open and when several are.
Per-agent connections make that question ambiguous: two tabs would mean two processes, two
independent failures, and a status that belongs to a tab rather than to a server.

The editor host therefore owns one **`anvil.MCPRuntime`** per `(cwd, setting_sources)` pair —
the same key that decides which files a session reads. A session attaches to that runtime via
`Options.MCPRuntime`; it registers tools from the runtime's snapshot instead of starting
processes of its own.

| Object | Owns | Must not close |
|---|---|---|
| `MCPRuntime` | every `*mcp.Session`, OAuth callback listeners, token files | — |
| `Agent` in runtime mode | only its registry entries | runtime sessions |
| host `Server` | the runtime registry | agent registries |
| extension | nothing secret; opens the auth URL | — |

Sources are canonicalised (sorted, de-duplicated) so a client sending `["user","project"]`
cannot create a second runtime beside `["project","user"]`. Different source sets still get
different runtimes, and therefore different processes; that is the correct isolation and a
documented cost, not a bug.

### 2.2 Tools are proxies, not captured sessions

A tool handler registered from the runtime calls `runtime.Call(ctx, server, tool, args)` and
resolves the current session by name on every call. It never captures `*mcp.Session`. This is
what makes reconnect and reload safe: the closure outlives the process it first described.

`Call` holds a per-entry read lock for the duration; `Reload` and `Reconnect` take the write
lock, which waits for in-flight calls before retiring the old session. `internal/mcp.Session`
itself is unchanged and still serialises its own requests with its mutex.

### 2.3 Failure is a state, not an error

`MCPServerStatus.State` is one of:

| State | Meaning |
|---|---|
| `disabled` | the config says `enabled: false`; nothing was started |
| `connecting` | a start is in flight |
| `connected` | `initialize` and `tools/list` succeeded; `Tools` is the catalogue |
| `failed` | the start (or a later reconnect) failed; `Error` says why |
| `auth_required` | an HTTP server answered `401`; sign-in is the next step |

A failed server never fails `Reload` and never fails `session.new`. The session opens with the
tools that did connect; the panel shows the one that did not, with its error. Inline
`Options.MCPServers` without a runtime, and the file layers of an SDK caller that has no
runtime, keep the historical fail-fast behaviour — this milestone adds a layer, it does not
change what existing callers already rely on.

Start failures inside a runtime are recorded per server and the rest proceed. A server that
hangs is bounded by `ConnectTimeout` (default 15 s) and reported as `failed`.

### 2.4 Tools change between turns, never during one

`MCPRuntime.Version()` increments whenever the tool snapshot changes. `Agent.Send` compares it
with the version the registry was built from, under the same `a.mu` lock that flips `running`,
and calls `RefreshMCP()` before the run starts. `RefreshMCP` unregisters and re-registers only
`mcp_*` tools; plugins, LSP and hooks are untouched. A run in flight always sees a stable
registry; a configuration change made while a turn runs applies to the next turn.

`Reload` in the SDK (the existing full reload of file MCP, rules and hooks) keeps working
without a runtime. With a runtime, it no longer starts or closes MCP sessions; it refreshes the
registry from the runtime's snapshot.

## 3. The config file

### 3.1 New fields

`mcpServers.<name>` gains:

- `enabled` (boolean, absent means `true`). A disabled entry still **shadows** a same-named
  lower layer: turning the project's entry off does not silently fall back to the user file.
  It is reported as `disabled` rather than disappearing.
- `transport` (`stdio`, `http`, `sse`; `streamable-http` is accepted as an alias of `http`).
  `type` is accepted as a legacy alias. If both are present with different values the file is
  reported as malformed. Precedence remains: explicit transport, then a command means stdio,
  then a URL means http.

### 3.2 Editing is raw and atomic

Add, update, remove and toggle are exported from package `anvil`
([mcpconfig.go](../../../source/mcpconfig.go)) because the host may import nothing else. They:

- read the file as `map[string]json.RawMessage`, keeping unknown top-level fields and unknown
  fields inside every other server entry;
- validate before writing — name non-empty, transport in the enum, stdio needs a command,
  remote needs an absolute `http(s)` URL, command and URL are mutually exclusive, env/header
  keys non-empty;
- write a temporary file in the same directory and `rename` it into place, so a crash cannot
  leave half a configuration;
- refuse to write at all when the file does not parse. A malformed file is read-only until a
  person fixes it; the manager shows the parser error, it does not replace the file.

`enabled: false` is written for a disabled entry; enabling deletes the key, so the file keeps
the shape the other MCP clients expect.

### 3.3 Secrets never travel outward

`mcp.list` reports env and header **names**, never values: a `.cursor/mcp.json` commonly holds
bearer tokens and API keys in `headers`. Update accepts new values, and an omitted map means
"keep what is there"; only an explicit empty map clears one. Removing a server from one scope
may reveal a same-named entry in the other; the listing says which file won.

## 4. Status and the host protocol

`mcp.list` grows additively:

```json
{
  "runtime": true,
  "servers": [{
    "name": "docs", "transport": "http", "source": "E:\\ws\\.cursor\\mcp.json",
    "enabled": true, "state": "connected", "tool_count": 3,
    "tools": [{"name": "search", "description": "..."}],
    "error": "", "connected_at": "2026-10-20T09:00:00Z",
    "env_names": ["TOKEN"], "header_names": ["Authorization"]
  }],
  "sources": [{"source": "project", "path": "...", "exists": true}],
  "problem": {"path": "...", "message": "...", "line": 12, "column": 3}
}
```

- `runtime: true` marks a host that can observe state. An older host omits it, and the
  extension falls back to the configuration-only card.
- `tools` is present only when the request sets `include_tools`.
- `problem` is a *result*, not an RPC error, for a malformed enabled file: the panel can show
  the last good statuses next to the parser error and make the document read-only.

Management methods, all returning a fresh `mcp.list` result so the document renders what a new
session will read:

- `mcp.add {scope, server}`, `mcp.update {scope, server}` — the draft carries name, transport,
  command, args, env, url, headers, enabled;
- `mcp.remove {scope, name}`;
- `mcp.set_enabled {scope, name, enabled}`;
- `mcp.reconnect {name}`;
- `mcp.authenticate {name}` → `{server, auth_url, expires_at}`;
- `mcp.sign_out {name}`.

After a write the host reloads the runtime, then calls `RefreshMCP` on every idle session for
that runtime. A session with a run in flight is left alone; its next `Send` refreshes. Every
state transition is pushed as an `mcp.status` notification carrying the changed server, so a
panel left open follows background connection attempts and OAuth completion.

The host keeps the runtime registry, closes every runtime on shutdown, and still imports only
the public `anvil` package (`independence_test.go`).

## 5. Remote transport

`transport: "http"` becomes Streamable HTTP:

- `POST` with `Accept: application/json, text/event-stream`;
- `Mcp-Session-Id` returned by `initialize` is sent on every later request;
- a response body is parsed as JSON or as an SSE stream, waiting for the JSON-RPC id;
- notifications accept `202`/`204`; a `4xx` is an error;
- `initialize` sends `2025-06-18`, records the server's answer, and sends
  `MCP-Protocol-Version` afterwards; if the server rejects the version, one retry with
  `2024-11-05` follows;
- `401` becomes `*AuthRequiredError` carrying the `WWW-Authenticate` challenge;
- `404` on a session with an id becomes `*SessionExpiredError` and a `failed` state.

Legacy `transport: "sse"` is unchanged. Servers that answer a POST with plain JSON keep
working, which is what makes the upgrade backward compatible.

## 6. OAuth

### 6.1 The flow

1. A connect or call answers `401`. The runtime parses `WWW-Authenticate`, preferring
   `resource_metadata`, otherwise deriving
   `{scheme}://{host}/.well-known/oauth-protected-resource{path}` (RFC 9728).
2. The protected-resource metadata is validated: its `resource` must equal the configured MCP
   URL (normalised). Its `authorization_servers[0]` is the issuer.
3. Authorization-server metadata (RFC 8414, falling back to OpenID discovery) supplies the
   endpoints and the supported PKCE methods.
4. Without a cached client id, dynamic registration (RFC 7591) registers `Anvil` with the
   loopback redirect URI, `authorization_code` + `refresh_token`, and `token_endpoint_auth_method: none`.
5. A verifier (32 random bytes) and an S256 challenge, plus a random `state`, are generated.
6. A listener binds `127.0.0.1:0`; its `/callback` becomes the exact `redirect_uri` used in
   registration and in the authorization request, together with `resource=<MCP URL>` (RFC 8707).
7. The authorization URL is returned to the caller. The extension opens it with
   `vscode.env.openExternal`; the host never opens a browser itself.
8. The callback checks `state` in constant time, rejects an `error` parameter, and exchanges
   the code with the verifier, the exact redirect URI and the same `resource`.
9. Tokens are written, the server is reconnected, and `mcp.status` announces the new state.

The listener lives five minutes, accepts one request, and is closed on completion, timeout or
runtime shutdown. A second `Authenticate` while one is pending returns the pending URL rather
than starting a second flow.

### 6.2 Tokens

- Directory `~/.anvil/mcp/oauth/` (0700), file
  `sha256(server + "\n" + normalisedURL)[:32] + ".json"`, written through a temp file and
  `rename` with mode 0600. On Windows this is a best-effort mode; the file stays under the user
  profile, outside every workspace.
- The record holds server, URL, issuer, resource, client id/secret, scope, token type, access
  and refresh tokens, and `expires_at`.
- A request refreshes 60 seconds before expiry using the refresh token and the same
  `resource`. `invalid_grant`, or a missing refresh token, clears the tokens and returns the
  server to `auth_required`; the client registration is kept so the next sign-in skips DCR.
- `SignOut` deletes the file. Removing a server deletes it only when no remaining declaration
  uses the same name and URL.
- Tokens, client secrets and secret header values never appear in logs, errors, statuses or
  any protocol message. URLs are logged without query strings.

### 6.3 What this cannot promise

Remote setups where the extension runs on a different machine from the host (VS Code Remote /
SSH) may have a browser that cannot reach the host's loopback listener. The design says so in
the panel's error text; a manual code-paste fallback is deliberately left to a later phase.
Authorization servers without dynamic registration and without a pre-registered client id are
reported as `failed` with an actionable message rather than guessed at, and `oauth.client_id` /
`oauth.client_secret` on a server entry are honoured when a registration must be supplied by
hand. Device-code flow, revocation endpoints and OS keychains are non-goals.

## 7. The editor surface

A dedicated **MCP panel**, modelled on the permissions manager
([permissionsPanel.ts](../../../editors/vscode/src/permissionsPanel.ts)): one webview document
per window, nonce and CSP, every value through `textContent`, a malformed file rendered
read-only with a banner.

The panel shows each server's state dot and word, transport, source file, tool list, error, and
per-server actions: enable/disable, reconnect, delete (confirmed), sign in when
`auth_required`, sign out. Adding and editing use the same form: scope (project or user),
transport, command/args/env or URL/headers, enabled. The chat card gains the same state word
and tool count; it keeps working against a host that predates `runtime` by showing
configuration only.

Entry points: a `anvil.openMcp` command, an `anvil.openMcpFile` command, and `/mcp` in the
composer. The extension still refuses untrusted and virtual workspaces, and the panel goes
through the same `HostManager` trust gate as every other call.

## 8. Tests

- **Streamable HTTP**: `httptest` servers that assert the session id, parse SSE responses, and
  return `401`/`404` in the shapes the typed errors expect.
- **OAuth**: one `httptest` authorization server serving resource metadata, AS metadata,
  registration and token endpoints. The test calls `Authenticate`, reads the URL, and issues
  the callback request itself with a code and the right state; the fake AS verifies the PKCE
  verifier against the challenge, the exact redirect URI and the `resource` parameter. A fake
  MCP server returns `401` until it sees the issued bearer. Refresh is exercised by expiring
  the token; `invalid_grant` is exercised by rejecting it.
- **Runtime**: one good and one broken server; config diff closes removed processes; an
  in-flight call survives a reload; `go test -race` over concurrent call/reload.
- **Host**: a down server or malformed file no longer fails `session.new`; `mcp.list` reports
  state; writes land in the selected file; a run-in-progress defers the refresh.
- **Extension**: `fake-host.mjs` gains a scripted MCP state machine; unit and integration tests
  pin the exact wire params for every management method and the `mcp.status` path. The panel's
  source is asserted the way the permissions panel is: text-only rendering, no `innerHTML`.

## 9. Non-goals

- MCP resources, prompts, sampling, elicitation, completion.
- Background GET SSE and automatic `tools/list_changed` refresh; a reconnect or the next turn
  is how a changed catalogue is picked up.
- Parallel calls to one server, load balancing, idle runtime shutdown.
- Device-code flow, token revocation endpoints, OS keychain storage.
- Editing an existing server's env/header values in the panel without re-entering them.
- Transports other than stdio, legacy SSE and Streamable HTTP; config files other than the two
  Cursor paths.
- Any token or secret header value on the protocol pipe.

## 10. Delivery

| Phase | Deliverable |
|---|---|
| [p_0.44.1](../../roadmaps/v1.0/m_0.44/p_0.44.1.md) | Config model (`enabled`, `transport`) and atomic, field-preserving writers |
| [p_0.44.2](../../roadmaps/v1.0/m_0.44/p_0.44.2.md) | `MCPRuntime`, agent attachment, `RefreshMCP`, failure isolation |
| [p_0.44.3](../../roadmaps/v1.0/m_0.44/p_0.44.3.md) | Streamable HTTP, OAuth discovery/PKCE/tokens/refresh |
| [p_0.44.4](../../roadmaps/v1.0/m_0.44/p_0.44.4.md) | Host runtime registry, status on `mcp.list`, management RPCs, notifications |
| [p_0.44.5](../../roadmaps/v1.0/m_0.44/p_0.44.5.md) | Extension protocol, status in the chat card, fake-host state machine |
| [p_0.44.6](../../roadmaps/v1.0/m_0.44/p_0.44.6.md) | MCP manager panel, `/mcp`, commands, docs |
