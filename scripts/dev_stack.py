#!/usr/bin/env python3
"""Start a local Anvil stack for Flutter / web / scheduler joint debugging.

Builds binaries if needed, writes configs under .dev/, then runs:
  anvil-gateway (optional) → anvil-scheduler → agentd → anvil-web

Usage (from repo root):
  set UPSTREAM_OPENAI_KEY=sk-...                 # or .dev/upstream.env
  set UPSTREAM_OPENAI_URL=https://api.openai.com/v1   # optional
  python scripts/dev_stack.py                    # gateway + scheduler + agentd + web
  python scripts/dev_stack.py --upstream-key sk-... --upstream-url https://...
  python scripts/dev_stack.py --no-gateway       # skip LLM gateway
  python scripts/dev_stack.py --wizard           # browser install wizard (no env admin)
  python scripts/dev_stack.py --skip-build

Ctrl+C stops all child processes.
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "source"
BIN = ROOT / "bin"
DEV = ROOT / ".dev"

DEFAULT_TOKENS = {
    "ANVIL_SCHEDULER_TOKEN": "sched-dev",
    "ANVIL_GATEWAY_TOKEN": "gw-dev",
    "ANVIL_AGENTD_TOKEN": "agentd-dev",
    "ANVIL_WEB_ADMIN_USER": "admin",
    "ANVIL_WEB_ADMIN_PASSWORD": "adminadmin",
    "ANVIL_API_KEY": "gw-dev",
}

PROCS: list[tuple[str, subprocess.Popen]] = []


def exe(name: str) -> Path:
    suffix = ".exe" if os.name == "nt" else ""
    return BIN / f"{name}{suffix}"


def run(cmd: list[str], *, cwd: Path | None = None, env: dict | None = None) -> None:
    print("+", " ".join(cmd), flush=True)
    subprocess.check_call(cmd, cwd=str(cwd) if cwd else None, env=env)


def build_bins(names: list[str]) -> None:
    BIN.mkdir(parents=True, exist_ok=True)
    for name in names:
        out = exe(name)
        pkg = f"./cmd/{name}"
        run(["go", "build", "-o", str(out), pkg], cwd=SOURCE)


def write_text(path: Path, body: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(body, encoding="utf-8", newline="\n")


DEFAULT_UPSTREAM_URL = "https://api.openai.com/v1"


def write_configs(
    *,
    with_gateway: bool,
    upstream_url: str = DEFAULT_UPSTREAM_URL,
) -> dict[str, Path]:
    DEV.mkdir(parents=True, exist_ok=True)
    db = (DEV / "anvil-web.db").resolve()
    upstream_url = upstream_url.rstrip("/") or DEFAULT_UPSTREAM_URL

    gateway = DEV / "gateway.yaml"
    scheduler = DEV / "scheduler.yaml"
    agentd = DEV / "agentd.yaml"
    web = DEV / "web.yaml"

    if with_gateway:
        write_text(
            gateway,
            f"""listen: 127.0.0.1:8080
token_env: ANVIL_GATEWAY_TOKEN
default_model: private/deepseek-v4-pro
upstreams:
  openai_compat:
    base_url: {upstream_url}
    api_key_env: UPSTREAM_OPENAI_KEY
models:
  - id: private/claude-sonnet-5
    upstream: openai_compat
    upstream_model: claude-sonnet-5
    provider: openai
    owned_by: complex
    vision: true
    params: [temperature]
  - id: private/gpt-5.6-terra
    upstream: openai_compat
    upstream_model: gpt-5.6-terra
    provider: openai
    owned_by: complex
    vision: true
    params: [temperature]
  - id: private/deepseek-v4-pro
    upstream: openai_compat
    upstream_model: deepseek-v4-pro
    provider: openai
    owned_by: general
    params: [temperature]
  - id: private/glm-5.2
    upstream: openai_compat
    upstream_model: glm-5.2
    provider: openai
    owned_by: general
    params: [temperature]
  - id: private/claude-haiku-4-5
    upstream: openai_compat
    upstream_model: claude-haiku-4-5
    provider: openai
    owned_by: light
    vision: true
    params: [temperature]
  - id: private/gemini-3.6-flash
    upstream: openai_compat
    upstream_model: gemini-3.6-flash
    provider: openai
    owned_by: flash
    vision: true
    params: [temperature]
  - id: private/glm-5.3-flash
    upstream: openai_compat
    upstream_model: glm-5.3-flash
    provider: openai
    owned_by: flash
    vision: true
    params: [temperature]
  - id: private/deepseek-v4-flash
    upstream: openai_compat
    upstream_model: deepseek-v4-flash
    provider: openai
    owned_by: batch
    params: [temperature]
  - id: private/deepseek-v4-flash-vision-exp
    upstream: openai_compat
    upstream_model: deepseek-v4-flash-vision-exp
    provider: openai
    owned_by: batch
    vision: true
    params: [temperature]
  - id: private/bge-m3
    upstream: openai_compat
    upstream_model: bge-m3
    provider: openai
    owned_by: embedding
    params: [embedding]
  - id: private/text-embedding-3-large
    upstream: openai_compat
    upstream_model: text-embedding-3-large
    provider: openai
    owned_by: embedding
    params: [embedding]
""",
        )

    write_text(
        scheduler,
        f"""name: Local scheduler
description: Dev stack scheduler (127.0.0.1)
listen_app: 127.0.0.1:17800
listen_cluster: tcp://127.0.0.1:17777
token_env: ANVIL_SCHEDULER_TOKEN
web_sessions_db: {db.as_posix()}
heartbeat_stale_sec: 30
models_cache_sec: 15
# Gateway credentials only; model/provider chosen in the app (default = first catalog entry).
llm:
  base_url: http://127.0.0.1:8080/v1
  api_key_env: ANVIL_GATEWAY_TOKEN
""",
    )

    write_text(
        agentd,
        f"""listen: tcp://127.0.0.1:17654
token_env: ANVIL_AGENTD_TOKEN
max_workers: 0
overload: reject
cancel_grace_sec: 5
node_id: local-1
name: Dev node
description: Local agentd from scripts/dev_stack.py
sandbox_dir: {(DEV / "sandbox").as_posix()}
cluster:
  enabled: true
  url: tcp://127.0.0.1:17777
  node_id: local-1
  token_env: ANVIL_SCHEDULER_TOKEN
  heartbeat_sec: 5
plugins:
  available:
    - sample
    - memory
  deny: []
  search_paths: []
""",
    )

    write_text(
        web,
        f"""listen: 127.0.0.1:17900
db_path: {db.as_posix()}
scheduler_url: http://127.0.0.1:17800
scheduler_token_env: ANVIL_SCHEDULER_TOKEN
session_ttl_hours: 168
admin_user_env: ANVIL_WEB_ADMIN_USER
admin_password_env: ANVIL_WEB_ADMIN_PASSWORD
fleets:
  - id: local
    name: Local cluster
    scheduler_url: http://127.0.0.1:17800
""",
    )

    return {
        "gateway": gateway,
        "scheduler": scheduler,
        "agentd": agentd,
        "web": web,
        "db": db,
    }


def spawn(name: str, args: list[str], env: dict[str, str]) -> subprocess.Popen:
    log_path = DEV / f"{name}.log"
    log_f = open(log_path, "w", encoding="utf-8", errors="replace")
    print(f"starting {name} -> {log_path}", flush=True)
    p = subprocess.Popen(
        args,
        cwd=str(DEV),
        env=env,
        stdout=log_f,
        stderr=subprocess.STDOUT,
    )
    p._anvil_log = log_f  # type: ignore[attr-defined]
    PROCS.append((name, p))
    return p


def stop_all() -> None:
    for _name, p in reversed(PROCS):
        if p.poll() is not None:
            continue
        try:
            p.terminate()
        except Exception:
            pass
    deadline = time.time() + 5
    while time.time() < deadline:
        if all(p.poll() is not None for _n, p in PROCS):
            break
        time.sleep(0.1)
    for _name, p in PROCS:
        if p.poll() is None:
            try:
                p.kill()
            except Exception:
                pass
        log_f = getattr(p, "_anvil_log", None)
        if log_f is not None:
            try:
                log_f.close()
            except Exception:
                pass


def wait_http(url: str, *, headers: dict[str, str] | None = None, timeout: float = 20.0) -> bool:
    import urllib.request

    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            req = urllib.request.Request(url, method="GET", headers=headers or {})
            with urllib.request.urlopen(req, timeout=1.5) as resp:
                if 200 <= resp.status < 500:
                    return True
        except Exception:
            pass
        time.sleep(0.25)
    return False


def http_json(method: str, url: str, *, headers: dict[str, str], body: dict | None = None) -> tuple[int, str]:
    import json
    import urllib.error
    import urllib.request

    data = None if body is None else json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status, resp.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")


def load_dotenv_files() -> dict[str, str]:
    out: dict[str, str] = {}
    for path in (DEV / "upstream.env", ROOT / ".env"):
        if not path.is_file():
            continue
        for line in path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip().strip('"').strip("'")
    return out


def load_upstream_var(name: str, cli_value: str = "") -> str:
    if cli_value.strip():
        return cli_value.strip()
    if os.environ.get(name, "").strip():
        return os.environ[name].strip()
    return load_dotenv_files().get(name, "").strip()


def seed_scheduler_llm(sched_token: str, gw_token: str) -> None:
    """Point scheduler (and joined agentd) at local gateway."""
    code, text = http_json(
        "PUT",
        "http://127.0.0.1:17800/v1/llm",
        headers={
            "Authorization": f"Bearer {sched_token}",
            "Content-Type": "application/json",
        },
        body={
            "base_url": "http://127.0.0.1:8080/v1",
            "api_key": gw_token,
        },
    )
    if 200 <= code < 300:
        print("seeded scheduler LLM -> http://127.0.0.1:8080/v1", flush=True)
    else:
        print(f"warning: PUT /v1/llm failed ({code}): {text[:200]}", flush=True)


def main() -> int:
    ap = argparse.ArgumentParser(description="Start Anvil local joint-debug stack")
    ap.add_argument("--skip-build", action="store_true", help="do not go build")
    ap.add_argument(
        "--no-gateway",
        action="store_true",
        help="skip anvil-gateway (jobs need force_stub or external LLM)",
    )
    ap.add_argument(
        "--upstream-key",
        default="",
        help="OpenAI upstream key for gateway (env UPSTREAM_OPENAI_KEY or .dev/upstream.env)",
    )
    ap.add_argument(
        "--upstream-url",
        default="",
        help="OpenAI-compatible upstream base URL (env UPSTREAM_OPENAI_URL; default api.openai.com/v1)",
    )
    ap.add_argument(
        "--wizard",
        action="store_true",
        help="skip env admin bootstrap; reset .dev/anvil-web.db and use browser install wizard",
    )
    args = ap.parse_args()

    if not SOURCE.is_dir():
        print("source/ not found; run from anvil repo", file=sys.stderr)
        return 2

    with_gateway = not args.no_gateway
    names = ["anvil-scheduler", "agentd", "anvil-web"]
    if with_gateway:
        names.insert(0, "anvil-gateway")

    if not args.skip_build:
        print("building binaries...", flush=True)
        build_bins(names)
    else:
        missing = [n for n in names if not exe(n).is_file()]
        if missing:
            print(f"missing binaries: {missing}; drop --skip-build", file=sys.stderr)
            return 2

    upstream_key = load_upstream_var("UPSTREAM_OPENAI_KEY", args.upstream_key)
    upstream_url = (
        load_upstream_var("UPSTREAM_OPENAI_URL", args.upstream_url) or DEFAULT_UPSTREAM_URL
    )
    configs = write_configs(
        with_gateway=with_gateway,
        upstream_url=upstream_url,
    )

    env = os.environ.copy()
    env.update(DEFAULT_TOKENS)
    # Workers must call local gateway with ANVIL_GATEWAY_TOKEN — never leak a
    # shell ANVIL_BASE_URL that points at the upstream (Athene) with gw-dev.
    env.pop("ANVIL_BASE_URL", None)
    if with_gateway:
        env["ANVIL_BASE_URL"] = "http://127.0.0.1:8080/v1"
    if args.wizard:
        db_path = DEV / "anvil-web.db"
        if db_path.is_file():
            db_path.unlink()
            print(f"removed {db_path} for fresh install wizard", flush=True)
        env.pop("ANVIL_WEB_ADMIN_USER", None)
        env.pop("ANVIL_WEB_ADMIN_PASSWORD", None)
        print("wizard mode: open http://127.0.0.1:17900 to create admin", flush=True)
    if with_gateway:
        if upstream_key:
            env["UPSTREAM_OPENAI_KEY"] = upstream_key
            env["UPSTREAM_OPENAI_URL"] = upstream_url
            print(f"gateway upstream url: {upstream_url}", flush=True)
            print("gateway upstream key: set", flush=True)
        else:
            print(
                "error: gateway stack needs UPSTREAM_OPENAI_KEY.\n"
                "  set env, or: python scripts/dev_stack.py --upstream-key sk-...\n"
                "  or write .dev/upstream.env with UPSTREAM_OPENAI_KEY=...\n"
                "  optional: UPSTREAM_OPENAI_URL=https://your-compat/v1\n"
                "  (use --no-gateway only if you do not need real LLM)",
                file=sys.stderr,
            )
            return 2

    try:
        if with_gateway:
            spawn(
                "gateway",
                [str(exe("anvil-gateway")), "--config", str(configs["gateway"])],
                env,
            )
            print("waiting for anvil-gateway...", flush=True)
            if not wait_http("http://127.0.0.1:8080/healthz"):
                print("gateway did not become ready; check .dev/gateway.log", file=sys.stderr)
                return 1
            print("gateway ready", flush=True)

        spawn(
            "scheduler",
            [str(exe("anvil-scheduler")), "--config", str(configs["scheduler"])],
            env,
        )
        time.sleep(0.5)

        spawn(
            "agentd",
            [str(exe("agentd")), "--config", str(configs["agentd"])],
            env,
        )
        time.sleep(0.8)

        if with_gateway:
            seed_scheduler_llm(
                DEFAULT_TOKENS["ANVIL_SCHEDULER_TOKEN"],
                DEFAULT_TOKENS["ANVIL_GATEWAY_TOKEN"],
            )

        spawn(
            "web",
            [str(exe("anvil-web")), "--config", str(configs["web"])],
            env,
        )

        print("waiting for anvil-web...", flush=True)
        if not wait_http("http://127.0.0.1:17900/"):
            print("web did not become ready; check .dev/web.log", file=sys.stderr)
        else:
            print("web ready", flush=True)

        print(
            f"""
=== Anvil dev stack ===
  web:       http://127.0.0.1:17900
  scheduler: http://127.0.0.1:17800
  agentd:    tcp://127.0.0.1:17654
  gateway:   {"http://127.0.0.1:8080  (worker LLM path)" if with_gateway else "(skipped)"}
  sandbox:   {DEV / "sandbox"}
  configs:   {DEV}
  db:        {configs["db"]}

  admin:     {"(install wizard at / — localhost or 192.168.x.x only)" if args.wizard else f"{DEFAULT_TOKENS['ANVIL_WEB_ADMIN_USER']} / {DEFAULT_TOKENS['ANVIL_WEB_ADMIN_PASSWORD']}"}
  sched tok: {DEFAULT_TOKENS["ANVIL_SCHEDULER_TOKEN"]}
  gw tok:    {DEFAULT_TOKENS["ANVIL_GATEWAY_TOKEN"]}

Flutter:
  cd app && flutter run
  login -> web http://127.0.0.1:17900  scheduler http://127.0.0.1:17800
  (Android emulator: use 10.0.2.2 instead of 127.0.0.1)

Ctrl+C to stop.
""",
            flush=True,
        )

        while True:
            for label, p in PROCS:
                code = p.poll()
                if code is not None:
                    print(
                        f"{label} exited with {code}; see .dev/{label}.log",
                        file=sys.stderr,
                    )
                    return code or 1
            time.sleep(0.5)
    except KeyboardInterrupt:
        print("\nstopping...", flush=True)
        return 0
    finally:
        stop_all()


if __name__ == "__main__":
    sys.exit(main())
