# 19 — Vision (image / screenshot)

**Product version focus:** **0.19** ([m_0.19_vision.md](../../roadmaps/v1.0/m_0.19_vision.md))  
**Status:** done

## Intent

Let the agent **attach images** (screenshots, UI mocks, error dialogs) into the multimodal LLM request so visual bugs and design references work when the provider supports vision.

This is a **capability of the Completer path**, plus a small tool to load images from the cwd sandbox (or accept host-provided attachments on Send).

## Surfaces

### A — Host attachments (primary)

```text
Send / Prompt options:
  Attachments: []ImageAttachment{ Path | Bytes, MIME, Detail? }
```

- Paths must pass path sandbox.
- Bytes path for hosts that already have screenshots in memory (agentd / cloud-agent).
- Mapped to OpenAI-compatible `image_url` / Anthropic image blocks per [05](05-llm-provider.md).

### B — Tool `read_image` (optional)

```text
read_image
  path: string
  max_side?: number   # downscale before send
```

- Loads file → tool result is text `image queued (…)`; runtime drains the queue after tool results and injects a synthetic user message with image `Parts` before the **next** Completer call in the same run.
- Host attachments on Send/Prompt become `Parts` on the initial user message (no tool required).

### Tool group

`vision`. Default **off** unless `Options.Vision.Enabled` or model advertises vision via gateway catalog ([12](12-model-gateway.md)).

## Provider mapping

| Provider | Mechanism |
|----------|-----------|
| OpenAI-compatible | `content` parts: `image_url` (base64 data URL or https URL if allowed) |
| Anthropic | `image` source base64 |

- Size limits: max dimensions / max bytes before downscale (e.g. longest side 2048, max 4MiB encoded).
- `https` image URLs: opt-in only (`AllowRemoteImageURL`); default base64 from local files to avoid SSRF.

## Security

- Path sandbox; deny exotic formats if decode fails.
- No automatic screen capture API in 0.19 (host supplies screenshot); optional later OS capture is out of scope.
- Redact: do not log raw base64.

## Explicit non-goals

- Video / realtime camera.
- OCR-only local pipeline without LLM (may MCP).
- Guaranteeing every gateway model supports vision — catalog should mark `anvil.vision=true`.

## Roadmap mapping

[m_0.19_vision.md](../../roadmaps/v1.0/m_0.19_vision.md) phases `p_0.19.1` … `p_0.19.3`.
