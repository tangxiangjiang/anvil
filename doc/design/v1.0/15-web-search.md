# 15 — Web search

**Product version focus:** **0.15** ([m_0.15_web_search.md](../../roadmaps/v1.0/m_0.15_web_search.md))  
**Status:** planned

## Intent

Give the agent a **web_search** (and optional **fetch_url**) path for docs, APIs, and current facts — complementary to cwd **semantic_search** ([13](13-retrieval.md)). Anvil does **not** run its own search crawl farm; it calls a configured **search provider** (HTTP) or an MCP server with the same job.

## Placement

```text
agent tool loop
    │
    ├─ semantic_search ──▶ local index (0.13)
    └─ web_search ───────▶ search provider HTTP / MCP
              │
              └─ fetch_url (optional) ──▶ HTTP GET + extract text
```

Model gateway ([12](12-model-gateway.md)) may **proxy** search if operators want one auth plane; not required — Anvil can call the provider directly via `Options.WebSearch`.

## Builtin tools

### `web_search`

```text
web_search
  query: string
  max_results?: number   # default 5, hard max bounded (e.g. 10)
  freshness?: string     # optional provider hint: day|week|month|any
```

Returns a truncated list of `{ title, url, snippet }` (text or JSON-in-text). No raw HTML dump.

### `fetch_url` (0.15 optional but designed)

```text
fetch_url
  url: string
  max_bytes?: number     # default e.g. 64KiB
```

- http/https only; block private/link-local unless `AllowPrivateURLs` (default false) — SSRF guard.
- Prefer text/HTML → readable text extract; binary → short error.
- Respect tool output truncation ([04](04-tools-and-sandbox.md)).

### Allow / deny

- Tool group name: `web` (for `BuiltinTools` / `DisallowedTools`).
- Default: **off** unless `Options.WebSearch` configured or group explicitly allowed — avoids surprise egress in CI.

## Provider interface

```text
WebSearchProvider
  Search(ctx, query, opts) ([]WebHit, error)

WebHit: Title, URL, Snippet, PublishedAt?
```

Built-in adapters (pick ≥1 in milestone; others via config URL templates):

| Adapter | Notes |
|---------|--------|
| **HTTP JSON** | Generic: POST/GET to operator URL with API key header; map JSONPath/fields in config |
| **Named** | Optional first-party: Brave / Tavily / SerpAPI-shaped — only if keys + ToS allow; keep behind interface |
| **MCP** | Document tool names `web_search` / `fetch_url`; no Anvil-owned MCP binary required |

Secrets: env var names in config only (align 0.8).

## Config (sketch)

```text
Options.WebSearch = {
  Enabled: true
  Provider: "http" | "brave" | … | "mcp"  // mcp = rely on MCPServers only
  BaseURL: "https://…"
  APIKeyEnv: "ANVIL_WEB_SEARCH_KEY"
  Timeout: 15s
  MaxResults: 5
  AllowFetchURL: true
  AllowPrivateURLs: false
}
```

Gateway path: if `BaseURL` points at model gateway `/v1/web/search`, same client token as chat.

## Security

- Egress is explicit and auditable; log query hash / host, not full API keys.
- SSRF: deny RFC1918 / metadata IPs on `fetch_url` by default; no file://.
- Redact secrets in snippets if detected (best-effort).
- Hooks: `beforeMCP` covers MCP search; optional `beforeWebSearch` deny rule in hooks schema (wire in 0.14/0.15 — prefer extend [14](14-hooks-skills.md) matcher or local denyContains on tool name).

## Relation to retrieval

| Tool | Corpus |
|------|--------|
| `grep` / `semantic_search` | Local cwd |
| `web_search` / `fetch_url` | Internet (provider) |

Do not merge indexes; the model chooses the tool.

## Explicit non-goals

- Building a public search engine or page cache at web scale.
- Browser automation / headless Chromium.
- Paying Cursor for bundled web_search_tool.
- Guaranteeing citation legal compliance beyond returning URLs (host policy).

## Roadmap mapping

[m_0.15_web_search.md](../../roadmaps/v1.0/m_0.15_web_search.md) phases `p_0.15.1` … `p_0.15.4`.
