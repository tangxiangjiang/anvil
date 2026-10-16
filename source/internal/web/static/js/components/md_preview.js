// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Lightweight Markdown + mermaid Edit/Preview for Delivery ADR sections.
 * Usage:
 *   MdPreview.render(md) -> HTML string (async mermaid via MdPreview.mountPreview)
 *   MdPreview.bindToggle(root) wires [data-md-mode] buttons
 */
window.MdPreview = {
  _mermaidReady: null,

  esc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  },

  ensureMermaid() {
    if (this._mermaidReady) return this._mermaidReady;
    this._mermaidReady = new Promise((resolve) => {
      const run = () => {
        if (!window.mermaid) {
          resolve(false);
          return;
        }
        try {
          window.mermaid.initialize({
            startOnLoad: false,
            theme: "dark",
            securityLevel: "strict",
            fontFamily: "inherit",
          });
        } catch (_) {}
        resolve(true);
      };
      if (window.mermaid) {
        run();
        return;
      }
      // Script may still be loading
      let n = 0;
      const t = setInterval(() => {
        n++;
        if (window.mermaid || n > 40) {
          clearInterval(t);
          run();
        }
      }, 50);
    });
    return this._mermaidReady;
  },

  /** Sync HTML from markdown (mermaid fences become placeholders). */
  render(md) {
    const src = String(md ?? "").replace(/\r\n/g, "\n");
    if (!src.trim()) {
      return `<p class="muted md-empty">Empty — switch to Edit to write Markdown.</p>`;
    }
    const blocks = [];
    let i = 0;
    const fenceRe = /^```([a-zA-Z0-9_-]*)\s*\n([\s\S]*?)```/gm;
    let last = 0;
    let m;
    while ((m = fenceRe.exec(src))) {
      if (m.index > last) {
        blocks.push({ type: "md", text: src.slice(last, m.index) });
      }
      const lang = (m[1] || "").toLowerCase();
      const body = m[2] || "";
      if (lang === "mermaid") {
        const id = "mmd-" + i++;
        blocks.push({ type: "mermaid", id, text: body.trim() });
      } else {
        blocks.push({
          type: "code",
          lang,
          text: body.replace(/\n$/, ""),
        });
      }
      last = m.index + m[0].length;
    }
    if (last < src.length) {
      blocks.push({ type: "md", text: src.slice(last) });
    }
    return blocks
      .map((b) => {
        if (b.type === "mermaid") {
          return `<div class="md-mermaid" data-mermaid-id="${this.esc(b.id)}"><pre class="md-mermaid-src">${this.esc(b.text)}</pre></div>`;
        }
        if (b.type === "code") {
          return `<pre class="md-code"><code>${this.esc(b.text)}</code></pre>`;
        }
        return this.renderInlineBlocks(b.text);
      })
      .join("");
  },

  renderInlineBlocks(text) {
    const lines = String(text || "").split("\n");
    const out = [];
    let para = [];
    let listType = null; // ul | ol
    let listItems = [];
    const flushPara = () => {
      if (!para.length) return;
      const joined = para.join(" ").trim();
      if (joined) out.push(`<p>${this.inline(joined)}</p>`);
      para = [];
    };
    const flushList = () => {
      if (!listItems.length) return;
      const tag = listType || "ul";
      out.push(
        `<${tag}>${listItems.map((li) => `<li>${this.inline(li)}</li>`).join("")}</${tag}>`,
      );
      listItems = [];
      listType = null;
    };
    for (const raw of lines) {
      const line = raw;
      if (/^\s*$/.test(line)) {
        flushPara();
        flushList();
        continue;
      }
      const h = /^(#{1,3})\s+(.+)$/.exec(line);
      if (h) {
        flushPara();
        flushList();
        const level = h[1].length;
        out.push(`<h${level + 2}>${this.inline(h[2])}</h${level + 2}>`);
        continue;
      }
      const ul = /^[-*]\s+(.+)$/.exec(line);
      if (ul) {
        flushPara();
        if (listType && listType !== "ul") flushList();
        listType = "ul";
        listItems.push(ul[1]);
        continue;
      }
      const ol = /^\d+\.\s+(.+)$/.exec(line);
      if (ol) {
        flushPara();
        if (listType && listType !== "ol") flushList();
        listType = "ol";
        listItems.push(ol[1]);
        continue;
      }
      if (/^---+$/.test(line.trim())) {
        flushPara();
        flushList();
        out.push("<hr />");
        continue;
      }
      flushList();
      para.push(line.trim());
    }
    flushPara();
    flushList();
    return out.join("") || `<p class="muted md-empty">Empty</p>`;
  },

  inline(s) {
    let t = this.esc(s);
    t = t.replace(/`([^`]+)`/g, "<code>$1</code>");
    t = t.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
    t = t.replace(/\*([^*]+)\*/g, "<em>$1</em>");
    t = t.replace(
      /\[([^\]]+)\]\((https?:[^)\s]+)\)/g,
      '<a href="$2" target="_blank" rel="noopener">$1</a>',
    );
    return t;
  },

  async mountPreview(container) {
    if (!container) return;
    const nodes = container.querySelectorAll(".md-mermaid");
    if (!nodes.length) return;
    const ok = await this.ensureMermaid();
    if (!ok || !window.mermaid) {
      nodes.forEach((n) => {
        n.innerHTML = `<p class="err">Mermaid unavailable</p><pre class="md-code">${n.querySelector(".md-mermaid-src")?.textContent || ""}</pre>`;
      });
      return;
    }
    for (const n of nodes) {
      const src = n.querySelector(".md-mermaid-src")?.textContent || "";
      const id = n.getAttribute("data-mermaid-id") || "mmd";
      try {
        const { svg } = await window.mermaid.render(id + "-" + Date.now(), src);
        n.innerHTML = svg;
      } catch (e) {
        n.innerHTML = `<p class="err">Mermaid error: ${this.esc(e.message || e)}</p><pre class="md-code">${this.esc(src)}</pre>`;
      }
    }
  },

  /**
   * Wire Edit/Preview toggles inside root.
   * Expects .md-pane[data-md-key] with .md-edit / .md-preview and buttons [data-md-mode="edit|preview"].
   */
  bindToggle(root, { onMode } = {}) {
    if (!root) return;
    root.querySelectorAll(".md-pane").forEach((pane) => {
      const edit = pane.querySelector(".md-edit");
      const preview = pane.querySelector(".md-preview");
      const setMode = async (mode) => {
        const isEdit = mode === "edit";
        if (edit) edit.hidden = !isEdit;
        if (preview) preview.hidden = isEdit;
        pane.querySelectorAll("[data-md-mode]").forEach((btn) => {
          btn.classList.toggle("active", btn.getAttribute("data-md-mode") === mode);
        });
        // Edit = no drag (text selection); Preview = cite-drag ok when marked.
        if (pane.getAttribute("data-cite-drag-ok") === "1") {
          pane.draggable = !isEdit;
          pane.classList.toggle("cite-drag", !isEdit);
        } else if (!pane.querySelector("[data-md-mode]")) {
          // Plain text ADR sections stay non-draggable while editing.
          pane.draggable = false;
          pane.classList.remove("cite-drag");
        }
        if (!isEdit && preview) {
          const ta = edit?.querySelector("textarea");
          const md = ta ? ta.value : "";
          preview.innerHTML = this.render(md);
          await this.mountPreview(preview);
        }
        onMode?.(pane.getAttribute("data-md-key"), mode);
      };
      pane.querySelectorAll("[data-md-mode]").forEach((btn) => {
        btn.addEventListener("click", () => setMode(btn.getAttribute("data-md-mode")));
      });
      // Default: edit when writable textarea present and not readonly
      const ta = edit?.querySelector("textarea");
      if (ta && ta.readOnly) {
        setMode("preview");
      } else {
        setMode("edit");
      }
    });
  },

  templates: {
    architecture: `\`\`\`mermaid
flowchart TB
  Client --> API
  API --> Service
  Service --> Store
\`\`\`
`,
    sequence: `\`\`\`mermaid
sequenceDiagram
  participant U as User
  participant W as Web
  participant S as Scheduler
  U->>W: Request
  W->>S: Forward
  S-->>W: Result
  W-->>U: Response
\`\`\`
`,
  },
};
