// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * The one modal implementation, shared by every feature.
 *
 * Each feature owns a host element (`#del-ui-modal`, `#c-ui-modal`) and passes it
 * in; the behaviour is identical, which is the point — the two features had
 * drifted into different definitions of "modal", and neither was a dialog.
 *
 * What makes it a dialog rather than a div that covers the screen:
 *   - role="dialog", aria-modal, and a label that names it
 *   - Escape closes it
 *   - focus moves inside on open and returns to the invoking element on close
 *   - Tab is trapped, so the page behind cannot be reached by keyboard
 *
 * Focus restore is the part people notice: without it, closing a modal leaves
 * focus on <body>, so the next Tab starts from the top of the page instead of
 * from the button that was pressed.
 */
(function () {
  const FOCUSABLE = [
    "a[href]",
    "button:not([disabled])",
    "input:not([disabled])",
    "select:not([disabled])",
    "textarea:not([disabled])",
    '[tabindex]:not([tabindex="-1"])',
  ].join(",");

  /** Per-host state: where focus came from, and the keydown handler. */
  const state = new WeakMap();

  function focusables(root) {
    return Array.from(root.querySelectorAll(FOCUSABLE)).filter(
      (el) => el.offsetParent !== null || el === document.activeElement,
    );
  }

  function trapTab(e, root) {
    if (e.key !== "Tab") return;
    const items = focusables(root);
    if (items.length === 0) {
      e.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (e.shiftKey && (active === first || !root.contains(active))) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && active === last) {
      e.preventDefault();
      first.focus();
    }
  }

  /**
   * open renders `html` into the modal host and takes focus.
   *
   * `title` becomes the accessible name; it must match the heading the caller
   * renders, which is why it is passed rather than derived — a modal whose
   * aria-label disagrees with its visible title is worse than one with none.
   */
  function open(host, html, { title, onClose } = {}) {
    if (!host) return;
    const prev = state.get(host);
    const returnTo = prev ? prev.returnTo : document.activeElement;

    host.innerHTML = html;
    host.hidden = false;
    host.setAttribute("role", "dialog");
    host.setAttribute("aria-modal", "true");
    if (host.id) host.setAttribute("aria-labelledby", host.id + "-title");
    const card = host.querySelector(".modal-card") || host;
    if (title) {
      card.setAttribute("role", "document");
      // The caller renders the heading; point the label at it when it exists.
      const heading = card.querySelector("h3, h2, h4");
      if (heading && host.id) heading.id = host.id + "-title";
      else if (!heading) host.setAttribute("aria-label", title);
    }

    const onKey = (e) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        close(host);
        return;
      }
      trapTab(e, card);
    };
    document.addEventListener("keydown", onKey, true);

    // Clicking the backdrop closes, but only when the click started on the
    // backdrop: a drag that began inside the card and ended outside must not
    // dismiss the form.
    let downOnBackdrop = false;
    host.onmousedown = (ev) => {
      downOnBackdrop = ev.target === host;
    };
    host.onclick = (ev) => {
      if (ev.target === host && downOnBackdrop) close(host);
      downOnBackdrop = false;
    };

    host.querySelectorAll("[data-close-ui]").forEach((btn) => {
      btn.addEventListener("click", () => close(host));
    });

    state.set(host, { returnTo, onKey, onClose });
    const items = focusables(card);
    if (items.length > 0) items[0].focus();
  }

  function close(host) {
    if (!host || host.hidden) return;
    const st = state.get(host);
    state.delete(host);
    if (st && st.onKey) document.removeEventListener("keydown", st.onKey, true);
    host.hidden = true;
    host.innerHTML = "";
    host.onclick = null;
    host.onmousedown = null;
    host.removeAttribute("aria-modal");
    host.removeAttribute("aria-labelledby");
    host.removeAttribute("aria-label");
    if (st && st.returnTo && typeof st.returnTo.focus === "function") {
      try {
        st.returnTo.focus();
      } catch (_) {
        /* the trigger is gone; nothing to restore */
      }
    }
    if (st && typeof st.onClose === "function") st.onClose();
  }

  function isOpen(host) {
    return !!host && !host.hidden;
  }

  /**
   * requireTyped shows a confirmation that cannot be accepted by a reflex.
   *
   * The destructive actions in this console remove a milestone and its phases,
   * a design and its versions, or seal a generation for good. A plain "are you
   * sure" is answered by muscle memory, so the word has to be typed; the caller
   * supplies it and states what is being removed.
   */
  function requireTyped(host, { title, body, word, confirmLabel, onConfirm }) {
    const id = "typed-confirm";
    open(
      host,
      `
      <div class="modal-card modal-wide">
        <h3 style="margin-top:0">${window.AnvilUI.esc(title)}</h3>
        <div class="muted" style="white-space:pre-wrap;margin-bottom:0.75rem">${window.AnvilUI.esc(body)}</div>
        <label for="${id}-input">Type <code>${window.AnvilUI.esc(word)}</code> to confirm</label>
        <input id="${id}-input" autocomplete="off" spellcheck="false" />
        <div class="toolbar" style="margin-top:0.75rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="${id}-ok" disabled>${window.AnvilUI.esc(confirmLabel || "Delete")}</button>
        </div>
      </div>`,
      { title },
    );
    const input = host.querySelector(`#${id}-input`);
    const ok = host.querySelector(`#${id}-ok`);
    const sync = () => {
      ok.disabled = (input.value || "").trim() !== word;
    };
    input.addEventListener("input", sync);
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !ok.disabled) {
        e.preventDefault();
        ok.click();
      }
    });
    ok.addEventListener("click", async () => {
      ok.disabled = true;
      try {
        await onConfirm();
      } finally {
        close(host);
      }
    });
    sync();
  }

  window.AnvilUI.modal = { open, close, isOpen, requireTyped };
})();
