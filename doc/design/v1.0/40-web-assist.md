# 40 — Web drafting assistant

**Product version focus:** **0.46** ([m_0.46_web_polish.md](../../roadmaps/v1.0/m_0.46_web_polish.md), phase `p_0.46.6`)
**Status:** done

## Why this chapter exists

[23-yaml-config.md](23-yaml-config.md) states the boundary this chapter appears to cross:

> agentd does **not** proxy LLM HTTP. Control plane stays agentd; cognition stays in the worker.

That rule is about **execution**. The Delivery console had no assistant at all and faked seven of
them with string templates — one of which wrote the operator's own words into a persisted
requirement. Making the assistant real means anvil-web calling a provider, and a rule that says "no
LLM HTTP" has to be either respected or explicitly amended. Amending it silently would be worse than
either.

## The distinction

| | Worker execution (0.11–0.39) | Web drafting (this chapter) |
|---|---|---|
| Who calls the model | the worker, inside a job | anvil-web, inside a request |
| What it may do | run tools, edit files, run commands | emit text for one field |
| Lifetime | a conversation with history | one request, no state |
| Credential | gateway config + job env | anvil-web's own `assist` block |
| Failure blast radius | the job | nothing: a proposal is refused or discarded |

They are different acts with different privileges. A drafting call has no tools, no filesystem, no
conversation, and no ability to write — it returns text that a person accepts or discards.

## The contract

`POST /api/v1/delivery/assist`

```json
{ "surface": "requirement_version",
  "field": "success",
  "instruction": "make done testable",
  "cites": ["@Goals: ..."],
  "current": { "success": "old text" } }
```

→

```json
{ "available": true,
  "model": "…",
  "proposals": [
    { "field": "success", "label": "Completion criteria",
      "before": "old text", "value": "- [ ] …", "rationale": "…" } ] }
```

Three properties make it safe, and all three are enforced **server-side**:

1. **Structured patches, not prose.** A response that is not `{proposals:[…]}` is a `502`, not a
   fallback. Accepting prose is how the old mock ended up writing a sentence into an ADR.
2. **A per-surface field allowlist.** A proposal naming a field the surface does not own is refused
   before it reaches the client. The client cannot be trusted to limit where a patch lands.
3. **Nothing is applied here.** The endpoint proposes; the operator accepts; the surface saves
   through its own `PATCH`. [20-plugins.md](20-plugins.md)'s neighbour rule applies — a suggestion
   becomes a record only through an explicit act.

## Off is stated, not faked

With no `assist` block configured the endpoint answers `available:false, reason:"AI assist is not
configured"`. It does not produce a canned reply. A demo that looks real is how the previous
generation of these surfaces became untrustworthy: seven of them answered with a template, and three
of those wrote to the store.

## Configuration

```yaml
assist:
  base_url: http://127.0.0.1:8080/v1   # OpenAI-compatible root
  model: private/draft
  api_key_env: ANVIL_ASSIST_API_KEY   # name, never the key
  timeout_sec: 60
```

The key is read from the environment. It never appears in the config file, on the wire, or in a log —
the same discipline the VS Code client follows for its provider key.

## Non-goals

- Streaming the draft. A patch is small; a spinner is enough.
- Tool use, multi-turn refinement, or planning. One request, one set of proposals.
- Letting the model choose its own model or prompt: both are server-side, so a client cannot ask
  for something the deployment did not configure.
- Autonomy. No path here writes without an acceptance.

## Acceptance

1. With `assist` configured, asking for tighter Completion criteria returns proposals which, when
   applied, change exactly that field and nothing else.
2. With no configuration, every assistant surface says it is not configured and writes nothing.
   No `(mock)` string is reachable.
3. A proposal for a field outside the surface's allowlist is refused by the server with a message
   naming the refusal.
4. The store is byte-identical before and after an assist call
   (`TestAssistNeverWritesToTheStore`).
5. A provider that returns prose produces an error, not an empty proposal list — "no suggestions" and
   "the model ignored the contract" are different statements.
