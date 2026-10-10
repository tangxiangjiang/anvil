# 23 — YAML ops config (agentd + gateway)

**Product version focus:** **0.23** ([m_0.23_yaml_config.md](../../roadmaps/v1.0/m_0.23_yaml_config.md))  
**Status:** planned

## Intent

Let operators configure **agentd** and **anvil-gateway** with **YAML** (while keeping JSON). Put LLM/gateway defaults in **agentd** so embedded workers receive Options from the daemon — not a separate worker config file.

## Architecture note

agentd does **not** proxy LLM HTTP. Control plane stays agentd; cognition stays in the worker / embedded Anvil Completer, which calls `Options.BaseURL` (typically the model gateway).

```text
Controller → agentd (job.assign)
                ↓ inject llm defaults into Options
           Embedded worker / agent-worker
                ↓ HTTP
           anvil-gateway or provider
```

## Load rules

- Extension selects decoder: `.yaml` / `.yml` → YAML; `.json` → JSON.
- Same Go structs; secrets still via `*_env` names (never inline keys in files).
- Project Cursor files (`.cursor/mcp.json`, hooks, etc.) stay JSON — out of scope.

## agentd `llm` block

```yaml
llm:
  base_url: http://127.0.0.1:8080/v1
  api_key_env: ANVIL_API_KEY
  model: private/demo
  provider: openai
  max_steps: 128
```

- Applied when job fields / ambient env leave values empty.
- `job.assign` may still override model / provider / max_steps / plugins.

## Explicit non-goals

- Web admin UI.
- agentd as LLM reverse proxy.
- Worker-owned YAML config file.

## Roadmap mapping

[m_0.23_yaml_config.md](../../roadmaps/v1.0/m_0.23_yaml_config.md) phases `p_0.23.1` … `p_0.23.4`.
