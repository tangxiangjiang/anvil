# 62 — Browser and computer use

**Milestone focus:** 0.62. **Design status:** proposed. **Risk:** critical.

## 1. Problem

Anvil supports web search and static image understanding, but cannot operate an interactive web
application or desktop UI. Browser and computer use add value for reproducing bugs, validating
front ends, collecting evidence, and completing workflows that have no API. They also create a
new trust boundary: rendered content is untrusted instruction, and clicks can perform external
writes.

## 2. Goals

- Provide a controlled browser runtime with DOM, accessibility-tree, screenshot, console, and
  network evidence.
- Separate read/navigation actions from external side effects.
- Add an opt-in desktop interaction tier only after browser policy is proven.
- Make every action observable, interruptible, bounded, and replayable at the evidence level.
- Integrate browser tasks with permissions, durable runs, structured plans, and Worktree evidence.

## 3. Capability ladder

The feature has explicit levels:

1. `browser_read`: open approved origins, inspect DOM/accessibility tree, screenshot, console, and
   network metadata.
2. `browser_interact`: click, type non-secret text, select, scroll, upload an approved workspace
   file, and download into a quarantine directory.
3. `browser_external_write`: submit forms, create/update remote records, purchase, publish, send,
   or delete; always requires a fresh approval with a before-action summary.
4. `desktop_interact`: opt-in OS-window targeting and input for applications without a browser
   surface; never enabled by inheritance.

Permission modes cannot collapse these levels. `bypass` does not silently authorize financial,
authentication, publication, messaging, or destructive actions.

## 4. Browser session model

```text
BrowserSession {
  session_id, run_id, worker_id?, profile_id,
  allowed_origins, download_root, state, revision
}

BrowserObservation {
  sequence, url, title, accessibility_digest,
  screenshot_id?, console_summary, network_summary
}

BrowserAction {
  sequence, kind, target_fingerprint, input_class,
  approval_id?, before_digest, after_digest, outcome
}
```

Selectors supplied by the model are hints. Before action, the host resolves a current element and
records a stable target fingerprint from role, accessible name, bounds, and relevant attributes.
Stale or ambiguous targets require a new observation.

## 5. Prompt-injection boundary

Web content is evidence, never authority:

- page text cannot change system rules, permissions, origins, or tool availability;
- instructions from a page are labelled untrusted in model context;
- hidden/off-screen content and accessibility discrepancies are surfaced;
- navigation to a new origin is re-evaluated against policy;
- downloaded files are quarantined and never executed automatically;
- browser storage, cookies, and credentials are profile-scoped and excluded from transcripts.

## 6. Authentication and secrets

- Login is user-driven or uses host-owned credential integrations.
- Passwords, tokens, cookies, one-time codes, and payment fields are never returned to the model.
- Secret typing uses opaque handles and an approval that names the destination origin and field,
  not the secret value.
- Browser profiles are isolated per workspace/account policy and have explicit retention.

## 7. Runtime boundary

The browser runtime is a separate supervised process. It has:

- a generated profile directory;
- origin/network policy;
- bounded tabs, downloads, storage, CPU, memory, and wall time;
- no inherited provider credentials;
- deterministic shutdown and orphan reconciliation;
- an append-only action/evidence journal.

Playwright/CDP may be used behind this contract, but protocol types do not expose implementation
objects or arbitrary evaluation. Initial JavaScript execution is limited to host-owned probes.

## 8. Desktop tier

Desktop interaction is a later phase of 0.62 and requires:

- explicit window/process selection;
- capture masking for protected applications and regions;
- one-window input confinement;
- emergency stop and focus-loss cancellation;
- no terminal, password manager, security settings, or privilege-elevation interaction;
- OS-specific capability reporting.

Unsupported platforms report unavailable capability rather than browser fallback.

## 9. VS Code surface

Provide a Browser task view with:

- current URL/origin, tab count, permission level, and live stop;
- screenshots and action timeline;
- console/network/download evidence;
- approval cards with target preview and external effect classification;
- “take over” for user login or ambiguous UI;
- exportable trace with secrets removed.

The transcript summarizes actions; full images and traces open in dedicated read-only documents.

## 10. Acceptance

- Read-only browsing cannot submit, download outside quarantine, or navigate to disallowed origins.
- Prompt injection fixtures cannot alter permission or secret handling.
- External writes always require an action-specific approval.
- Stale/ambiguous targets fail without clicking.
- Credentials, cookies, protected fields, and authorization headers never enter model-visible data.
- Stop, timeout, crash, and editor restart terminate or reconcile sessions deterministically.
- Browser traces can prove what target was acted on and the observed outcome.

## 11. Non-goals

- CAPTCHA bypass, anti-bot evasion, covert automation, or bypassing site policy.
- Unattended financial, legal, account-recovery, or destructive workflows.
- Arbitrary JavaScript execution from model input.
- General unrestricted desktop control.
