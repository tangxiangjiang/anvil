/**
 * Shared AI Chat dock for Delivery surfaces.
 *
 * Used by: Requirements polish, Design, Roadmap (graph/node), future Test→Polish.
 * Host owns domain pickers + apply patches; this component owns shell, @ chips, drop, send.
 *
 * @example
 * const chat = AiChat.create({
 *   id: "ms-polish",
 *   title: "Polish chat",
 *   citeNav: [{ id: "detail", label: "Detail" }, { id: "version", label: "Version" }],
 *   onCiteNav(id) { ... open picker ... },
 *   onScrollCite(key) { ... flash field ... },
 *   onSend({ text, cites, composed }) { ... },
 * });
 * html += chat.render();
 * chat.mount(pageRoot);
 * chat.bindDropSources(pageRoot); // View: drag [data-cite-key] / [data-cite-drag]
 */
window.AiChat = {
  CITE_MIME: "application/x-anvil-cite",

  esc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  },

  isVersionKey(key) {
    return /^\d+\.\d+\.\d+$/.test(key || "");
  },

  flash(el) {
    if (!el) return;
    el.classList.remove("ai-chat-flash");
    void el.offsetWidth;
    el.classList.add("ai-chat-flash");
    setTimeout(() => el.classList.remove("ai-chat-flash"), 1400);
  },

  /**
   * @param {object} opts
   * @param {string} [opts.id]
   * @param {string} [opts.title]
   * @param {string} [opts.placeholder]
   * @param {string} [opts.hint]
   * @param {number} [opts.rows]
   * @param {boolean} [opts.tall]
   * @param {boolean} [opts.allowDrop]
   * @param {boolean} [opts.showLog] default true — conversation context is the primary pane
   * @param {{id:string,label:string}[]} [opts.citeNav]
   * @param {string} [opts.sendLabel]
   * @param {string[]} [opts.citeKeys]
   * @param {{role:string,text:string}[]} [opts.messages]
   * @param {(p:{text:string,cites:string[],composed:string})=>void|Promise<void>} [opts.onSend]
   * @param {(navId:string)=>void} [opts.onCiteNav]
   * @param {(key:string)=>void} [opts.onScrollCite]
   */
  create(opts = {}) {
    const cfg = {
      id: opts.id || "ai-chat",
      title: opts.title || "AI chat",
      placeholder: opts.placeholder || "Ask the assistant…",
      hint: opts.hint || "",
      emptyHint: opts.emptyHint || "",
      rows: opts.rows != null ? opts.rows : 3,
      tall: opts.tall !== false,
      allowDrop: opts.allowDrop !== false,
      showLog: opts.showLog !== false,
      citeNav: Array.isArray(opts.citeNav) ? opts.citeNav : [],
      sendLabel: opts.sendLabel || "Send",
      onSend: typeof opts.onSend === "function" ? opts.onSend : async () => {},
      onCiteNav: typeof opts.onCiteNav === "function" ? opts.onCiteNav : () => {},
      onScrollCite: typeof opts.onScrollCite === "function" ? opts.onScrollCite : () => {},
    };
    const state = {
      citeKeys: Array.isArray(opts.citeKeys) ? opts.citeKeys.slice() : [],
      messages: Array.isArray(opts.messages) ? opts.messages.slice() : [],
    };
    let mountedRoot = null;

    const self = {
      id: cfg.id,
      get citeKeys() {
        return state.citeKeys;
      },
      get messages() {
        return state.messages;
      },
      setCiteKeys(keys) {
        state.citeKeys = Array.isArray(keys) ? keys.slice() : [];
        self.refreshChips();
      },
      addCite(key) {
        const k = (key || "").trim();
        if (!k) return;
        if (!state.citeKeys.includes(k)) state.citeKeys.push(k);
        self.refreshChips();
      },
      removeCite(key) {
        state.citeKeys = state.citeKeys.filter((c) => c !== key);
        self.refreshChips();
      },
      composedMessage(text) {
        const cites = state.citeKeys.map((k) => `@${k}`).join(" ");
        const t = (text || "").trim();
        if (cites && t) return cites + "\n" + t;
        return cites || t;
      },
      setMessages(msgs) {
        state.messages = Array.isArray(msgs) ? msgs.slice() : [];
        self.refreshLog();
      },
      appendMessage(role, text) {
        state.messages.push({ role, text: String(text ?? "") });
        self.refreshLog();
      },
      clearInput() {
        const input = self._input();
        if (input) input.value = "";
      },
      _root() {
        if (mountedRoot && mountedRoot.isConnected) return mountedRoot;
        return null;
      },
      _input() {
        return self._root()?.querySelector("[data-ai-input]");
      },
      _chips() {
        return self._root()?.querySelector("[data-ai-chips]");
      },
      _log() {
        return self._root()?.querySelector("[data-ai-log]");
      },

      _navHtml() {
        const esc = window.AiChat.esc;
        return (cfg.citeNav || [])
          .map((n) => {
            const kids = Array.isArray(n.children) ? n.children : [];
            if (!kids.length) {
              return `<button type="button" class="ghost" data-ai-cite-nav="${esc(n.id)}">${esc(n.label)}</button>`;
            }
            const items = kids
              .map(
                (c) =>
                  `<button type="button" class="ghost ai-cite-fold-item" data-ai-cite-nav="${esc(c.id)}">${esc(c.label)}</button>`,
              )
              .join("");
            return `
            <div class="ai-cite-fold" data-ai-cite-fold>
              <button type="button" class="ghost ai-cite-fold-toggle" data-ai-cite-fold-toggle aria-expanded="false">
                ${esc(n.label)} <span class="ai-cite-fold-chevron">▾</span>
              </button>
              <div class="ai-cite-fold-menu" hidden>${items}</div>
            </div>`;
          })
          .join("");
      },

      render() {
        const esc = window.AiChat.esc;
        const nav = this._navHtml();
        const log = cfg.showLog
          ? `<div class="ai-chat-log" data-ai-log role="log" aria-live="polite">${self._logHtml()}</div>`
          : "";
        const hint = cfg.hint
          ? `<p class="muted ai-chat-hint">${cfg.hint}</p>`
          : "";
        const tall = cfg.tall ? " ai-chat-tall del-chat-tall" : "";
        return `
<aside class="ai-chat del-chat${tall} card" data-ai-chat="${esc(cfg.id)}" id="${esc(cfg.id)}">
  <div class="toolbar ai-chat-head" style="margin:0 0 0.5rem;align-items:center">
    <h3 style="margin:0;flex:1">${esc(cfg.title)}</h3>
    ${nav ? `<div class="ai-chat-nav del-cite-nav">${nav}</div>` : ""}
  </div>
  ${hint}
  <div class="ai-chat-chips del-cite-chips" data-ai-chips></div>
  ${log}
  <div class="ai-chat-composer">
    <textarea data-ai-input rows="${cfg.rows}" placeholder="${esc(cfg.placeholder)}"></textarea>
    <div class="ai-chat-composer-actions">
      <button type="button" data-ai-send>${esc(cfg.sendLabel)}</button>
    </div>
  </div>
</aside>`;
      },

      _emptyHint() {
        if (cfg.emptyHint) return cfg.emptyHint;
        const labels = [];
        for (const n of cfg.citeNav || []) {
          if (n.label) labels.push(n.label);
        }
        if (labels.length) {
          return `Conversation appears here. Use ${labels.join(" / ")} to add @ cites, then send.`;
        }
        return "Conversation appears here. Send a message to start.";
      },

      _logHtml() {
        const esc = window.AiChat.esc;
        if (!state.messages.length) {
          return `<div class="ai-chat-empty muted">${esc(this._emptyHint())}</div>`;
        }
        return state.messages
          .map((m) => {
            const role = m.role === "user" ? "user" : "ai";
            const label = role === "user" ? "You" : "AI";
            return `<div class="ai-chat-msg ${role}">
              <div class="ai-chat-msg-role">${label}</div>
              <div class="ai-chat-msg-body">${esc(m.text)}</div>
            </div>`;
          })
          .join("");
      },

      refreshLog() {
        const log = self._log();
        if (!log) return;
        log.innerHTML = self._logHtml();
        log.scrollTop = log.scrollHeight;
      },

      refreshChips() {
        const chips = self._chips();
        if (!chips) return;
        const esc = window.AiChat.esc;
        chips.innerHTML = state.citeKeys
          .map(
            (k) => `
      <span class="del-cite-chip ai-chat-chip">
        <button type="button" class="del-cite-chip-go" data-ai-scroll-cite="${esc(k)}">@${esc(k)}</button>
        <button type="button" class="del-cite-chip-x" data-ai-uncite="${esc(k)}" title="Remove" aria-label="Remove">×</button>
      </span>`,
          )
          .join("");
      },

      mount(pageRoot) {
        const root =
          pageRoot.querySelector(`[data-ai-chat="${cfg.id}"]`) ||
          pageRoot.querySelector(`#${CSS.escape(cfg.id)}`);
        if (!root) return self;
        mountedRoot = root;
        self.refreshChips();
        self.refreshLog();

        root.querySelectorAll("[data-ai-cite-fold-toggle]").forEach((btn) => {
          btn.addEventListener("click", (e) => {
            e.stopPropagation();
            const fold = btn.closest("[data-ai-cite-fold]");
            const menu = fold?.querySelector(".ai-cite-fold-menu");
            if (!menu) return;
            const open = menu.hidden;
            // Close other folds in this chat.
            root.querySelectorAll(".ai-cite-fold-menu").forEach((m) => {
              m.hidden = true;
            });
            root.querySelectorAll("[data-ai-cite-fold-toggle]").forEach((t) => {
              t.setAttribute("aria-expanded", "false");
              t.classList.remove("open");
            });
            menu.hidden = !open;
            btn.setAttribute("aria-expanded", open ? "true" : "false");
            btn.classList.toggle("open", open);
          });
        });
        root.querySelectorAll("[data-ai-cite-nav]").forEach((btn) => {
          btn.addEventListener("click", () => {
            const fold = btn.closest("[data-ai-cite-fold]");
            if (fold) {
              const menu = fold.querySelector(".ai-cite-fold-menu");
              if (menu) menu.hidden = true;
              fold.querySelector("[data-ai-cite-fold-toggle]")?.setAttribute("aria-expanded", "false");
              fold.querySelector("[data-ai-cite-fold-toggle]")?.classList.remove("open");
            }
            cfg.onCiteNav(btn.getAttribute("data-ai-cite-nav"));
          });
        });
        // Click outside closes ADR fold menus.
        if (!root._aiCiteFoldDocBound) {
          root._aiCiteFoldDocBound = true;
          document.addEventListener("click", (e) => {
            if (e.target.closest?.(`[data-ai-chat="${cfg.id}"]`)) return;
            root.querySelectorAll(".ai-cite-fold-menu").forEach((m) => {
              m.hidden = true;
            });
            root.querySelectorAll("[data-ai-cite-fold-toggle]").forEach((t) => {
              t.setAttribute("aria-expanded", "false");
              t.classList.remove("open");
            });
          });
        }

        const chips = root.querySelector("[data-ai-chips]");
        if (chips) {
          chips.onclick = (e) => {
            const x = e.target.closest("[data-ai-uncite]");
            if (x) {
              e.preventDefault();
              e.stopPropagation();
              self.removeCite(x.getAttribute("data-ai-uncite"));
              return;
            }
            const go = e.target.closest("[data-ai-scroll-cite]");
            if (go) cfg.onScrollCite(go.getAttribute("data-ai-scroll-cite"));
          };
        }

        const send = async () => {
          const input = self._input();
          const text = (input?.value || "").trim();
          if (!text && !state.citeKeys.length) return;
          const composed = self.composedMessage(text);
          self.appendMessage("user", composed);
          self.clearInput();
          await cfg.onSend({
            text,
            cites: state.citeKeys.slice(),
            composed,
          });
        };
        root.querySelector("[data-ai-send]")?.addEventListener("click", () => {
          send().catch((err) => console.error("ai_chat:", err));
        });
        root.querySelector("[data-ai-input]")?.addEventListener("keydown", (e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            send().catch((err) => console.error("ai_chat:", err));
          }
        });

        if (cfg.allowDrop) self._bindDrop(root);
        return self;
      },

      _bindDrop(chatRoot) {
        const onOver = (e) => {
          const types = e.dataTransfer?.types || [];
          const ok =
            types.includes(window.AiChat.CITE_MIME) || types.includes("text/plain");
          if (!ok) return;
          e.preventDefault();
          e.dataTransfer.dropEffect = "copy";
          chatRoot.classList.add("cite-drop-over");
        };
        chatRoot.addEventListener("dragover", onOver);
        chatRoot.addEventListener("dragenter", onOver);
        chatRoot.addEventListener("dragleave", (e) => {
          if (!chatRoot.contains(e.relatedTarget)) {
            chatRoot.classList.remove("cite-drop-over");
          }
        });
        chatRoot.addEventListener("drop", (e) => {
          e.preventDefault();
          chatRoot.classList.remove("cite-drop-over");
          const key =
            e.dataTransfer.getData(window.AiChat.CITE_MIME) ||
            e.dataTransfer.getData("text/plain");
          if (key) self.addCite(key.trim());
        });
      },

      /** View mode: drag detail fields / version cards onto this chat. */
      bindDropSources(pageRoot) {
        if (!cfg.allowDrop || !pageRoot) return self;
        pageRoot.querySelectorAll("[data-cite-key].cite-drag").forEach((el) => {
          el.addEventListener("dragstart", (e) => {
            // Editing textareas/inputs: never steal selection into a cite drag.
            if (e.target.closest("textarea, input, select, button, label")) {
              e.preventDefault();
              return;
            }
            if (el.draggable === false || !el.classList.contains("cite-drag")) {
              e.preventDefault();
              return;
            }
            const key = el.getAttribute("data-cite-key");
            e.dataTransfer.setData(window.AiChat.CITE_MIME, key);
            e.dataTransfer.setData("text/plain", key);
            e.dataTransfer.effectAllowed = "copy";
            el.classList.add("cite-dragging");
          });
          el.addEventListener("dragend", () => el.classList.remove("cite-dragging"));
        });
        pageRoot.querySelectorAll("[data-cite-drag]").forEach((card) => {
          card.addEventListener("dragstart", (e) => {
            if (e.target.closest("button, textarea, input, select, label")) {
              e.preventDefault();
              return;
            }
            const key =
              card.getAttribute("data-ver-key") ||
              card.getAttribute("data-cite-key") ||
              "";
            e.dataTransfer.setData(window.AiChat.CITE_MIME, key);
            e.dataTransfer.setData("text/plain", key);
            e.dataTransfer.effectAllowed = "copy";
            card.classList.add("cite-dragging");
          });
          card.addEventListener("dragend", () => card.classList.remove("cite-dragging"));
        });
        return self;
      },
    };

    return self;
  },
};
