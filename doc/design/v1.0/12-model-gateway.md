# 12 — Model gateway (catalog + auth)

**Product version focus:** **0.12** ([m_0.12_model_gateway.md](../../roadmaps/v1.0/m_0.12_model_gateway.md))  
**Status:** done (implementation through p_0.12.1–p_0.12.4; OpenAI-compat primary)

## Intent

Provide a **model gateway** in front of Anvil (and other hosts): unified **auth**, **upstream routing**, and a **model catalog** so callers no longer need Cursor `models.list` or per-provider key sprawl.

Anvil’s LLM adapters stay OpenAI-compatible / Anthropic HTTP ([05](05-llm-provider.md)). The gateway is the default `BaseURL` target; Anvil optionally grows a thin **catalog client**, not a second chat protocol.

## Placement

```text
anvil / agent-worker / CLI
        │  OpenAI-compatible or Anthropic
        ▼
┌───────────────────┐
│  model gateway    │  auth · catalog · route · limit
└─────────┬─────────┘
          ├─▶ upstream A (OpenAI-compat)
          ├─▶ upstream B (Anthropic)
          └─▶ upstream C (private vLLM / …)
```

**Repo posture:** Prefer `source/cmd/anvil-gateway` (or `gateway/`) inside this monorepo for 0.12 so cloud-agent / agentd share one binary; shipping as a sibling repo is allowed if the **wire contract** below is unchanged.

## Responsibilities

| Concern | Gateway | Anvil |
|---------|---------|--------|
| API keys to upstream | holds / injects | only holds gateway token (or none if mTLS) |
| Model id → upstream | routes | passes `Options.Model` through |
| Catalog | `GET /v1/models` (+ Anvil helper) | optional `anvil.ListModels` |
| Chat / messages | proxies streaming | existing Completer |
| Rate limit / quota | enforced | surfaces provider errors (0.7 codes) |

## Wire contract

### Auth

- Client → gateway: `Authorization: Bearer <gateway_token>` (or documented header).
- Gateway → upstream: provider-specific keys from **env / secret files only** (never from client body).
- Reject missing/invalid client token with stable HTTP 401; do not leak upstream credentials in error bodies.

### Catalog

`GET /v1/models` (OpenAI-shaped list at minimum):

```json
{
  "object": "list",
  "data": [
    {
      "id": "private/deepseek-v4-pro",
      "object": "model",
      "owned_by": "gateway",
      "anvil": {
        "provider": "openai",
        "upstream_model": "deepseek-chat",
        "params": ["temperature"]
      }
    }
  ]
}
```

- Public `id` is what Anvil/`Options.Model` use.
- Optional `anvil` extension object is advisory (provider hint, param names); clients may ignore unknown fields.
- Catalog is **config-driven** (YAML/JSON on gateway); no live scrape of Cursor Dashboard.

### Proxy

- OpenAI-compatible: `POST /v1/chat/completions` (stream + non-stream).
- Anthropic (optional in 0.12): `POST /v1/messages` with Anthropic headers, or gateway normalizes everything to OpenAI-compat only — **pick one in p_0.12.1 and document**; recommendation: **OpenAI-compat primary**, Anthropic passthrough if time allows.
- Preserve SSE/stream framing enough for Anvil’s existing stream parsers.

### Routing

Config maps `model id` → `{ upstream, upstream_model, provider }`. Unknown model → 404/400 with clear body. No silent remap to a default model unless explicitly configured (`default_model`).

## Anvil SDK touchpoints (minimal)

- Document: set `BaseURL` + `APIKey` to gateway; `Provider=openai` for the common path.
- Additive API (0.12): `ListModels(ctx, opts) ([]ModelInfo, error)` hitting `{BaseURL}/models` or `/v1/models`.
- No requirement to embed gateway code inside `anvil.Create`.

## Security

- Gateway binds localhost or private network by default; public expose needs TLS + token rotation notes.
- Redact secrets in logs (align 0.8).
- Per-token quotas optional but designed (config stub OK in early phases).

## Explicit non-goals

- Training, fine-tuning, or hosting weights.
- Cursor account / Router `auto-smart` clone.
- Replacing cloud-agent Gateway (task/workflow plane).
- Multi-region control plane or billing product.

## Roadmap mapping

[m_0.12_model_gateway.md](../../roadmaps/v1.0/m_0.12_model_gateway.md) phases `p_0.12.1` … `p_0.12.4`.
