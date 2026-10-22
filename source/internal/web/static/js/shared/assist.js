// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * The drafting assistant's client side (p_0.45.8).
 *
 * Replaces the seven per-surface `onSend` handlers that each returned a local
 * string template. One component, because the previous state had seven answers to
 * "what does the assistant say" and three of them wrote to the database.
 *
 * The shape is a proposal list, not a chat reply:
 *
 *   - each proposal renders as **before → after** with its rationale,
 *   - Apply writes the proposed value through the surface's own save path,
 *   - Discard removes it,
 *   - nothing is written by the component itself.
 *
 * That last point is the whole design. A component that saves is a component that
 * can save the wrong thing, and the previous generation did exactly that.
 */
window.AnvilUI = window.AnvilUI || {};

(function () {
  /**
   * ask sends one assist request and renders the proposals into `host`.
   *
   * `onApply(field, value)` is the surface's own save. The component does not know
   * how to persist anything, which is what makes "the assistant cannot write" a
   * structural property rather than a promise.
   */
  async function ask({ surface, field, instruction, cites, current, host, chat, onApply }) {
    if (!host) return;
    const body = { surface, instruction: instruction || "" };
    if (field) body.field = field;
    if (cites && cites.length) body.cites = cites;
    if (current) body.current = current;

    let res;
    try {
      res = await api.v1.deliveryAssist(body);
    } catch (e) {
      renderError(host, e.message || String(e));
      return;
    }

    if (!res || res.available === false) {
      // Off is stated, not faked. This is the one message the old mocks could never
      // send, and it is the difference between a known limit and a lie.
      renderUnavailable(host, (res && res.reason) || "AI assist is not configured");
      return;
    }

    const proposals = res.proposals || [];
    if (proposals.length === 0) {
      if (chat) chat.appendMessage("ai", "No suggestions for that request.");
      return;
    }
    renderProposals(host, proposals, res, { onApply, chat });
  }

  function renderUnavailable(host, reason) {
    host.innerHTML = `<div class="assist-note">${window.AnvilUI.esc(reason)}. Set <code>assist</code> in anvil-web's configuration to enable drafting.</div>`;
  }

  function renderError(host, message) {
    host.innerHTML = `<div class="assist-note bad">${window.AnvilUI.esc(message)}</div>`;
  }

  function renderProposals(host, proposals, res, { onApply, chat }) {
    const esc = window.AnvilUI.esc;
    host.innerHTML =
      `<div class="assist-list">` +
      proposals
        .map(
          (p, i) => `
      <div class="assist-card" data-assist-card="${i}">
        <div class="assist-head">
          <strong>${esc(p.label || p.field)}</strong>
          ${p.rationale ? `<span class="muted">${esc(p.rationale)}</span>` : ""}
        </div>
        ${p.before ? `<details class="assist-before"><summary class="muted">Current</summary><pre>${esc(p.before)}</pre></details>` : ""}
        <pre class="assist-after">${esc(p.value)}</pre>
        <div class="toolbar assist-actions">
          <button type="button" class="ghost" data-assist-discard="${i}">Discard</button>
          <button type="button" data-assist-apply="${i}">Apply to ${esc(p.label || p.field)}</button>
        </div>
      </div>`,
        )
        .join("") +
      `<div class="assist-foot muted">${esc(res.model || "")}${res.usage ? ` · ${res.usage.prompt_tokens || 0}+${res.usage.completion_tokens || 0} tokens` : ""} · nothing is saved until you apply</div>` +
      `</div>`;

    host.querySelectorAll("[data-assist-discard]").forEach((btn) => {
      btn.addEventListener("click", () => {
        btn.closest("[data-assist-card]")?.remove();
      });
    });
    host.querySelectorAll("[data-assist-apply]").forEach((btn) => {
      btn.addEventListener("click", () =>
        window.AnvilUI.withAction(
          btn,
          { key: "assist-apply", runningLabel: "Applying…", host, errorTitle: "Could not apply" },
          async () => {
            const p = proposals[Number(btn.getAttribute("data-assist-apply"))];
            await onApply(p.field, p.value, p);
            btn.closest("[data-assist-card]")?.remove();
            if (chat) chat.appendMessage("ai", `Applied to ${p.label || p.field}.`);
          },
        ),
      );
    });
  }

  window.AnvilUI.assist = { ask };
})();
