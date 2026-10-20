// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Designs surface: design list, ADR + versions detail, compose/approve.
 *
 * Part of the Delivery feature object. Each surface file assigns its own methods
 * onto the one `window.Features.delivery`, so the mounting contract in app.js
 * (`mount` / `unmount` / `onRoute`) and the router are unchanged: this is a
 * mechanical split of one 6 000-line literal, not a redesign.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  designCandidateMilestones(onlyMajor) {
    const have = new Set(
      (this.data.designs || []).map((d) => `${d.eng_req_id}:${d.major}.${d.minor}`),
    );
    const out = [];
    for (const eng of this.data.eng_reqs || []) {
      const seen = new Set();
      for (const v of eng.versions || []) {
        if (onlyMajor != null && v.major !== onlyMajor) continue;
        const key = `${eng.id}:${v.major}.${v.minor}`;
        if (seen.has(key) || have.has(key)) continue;
        seen.add(key);
        out.push({
          eng,
          major: v.major,
          minor: v.minor,
          title: v.title,
          status: v.status,
        });
      }
    }
    return out;
  },

  openDeleteDesignModal(id) {
    const d = (this.data.designs || []).find((x) => x.id === id);
    if (!d) return;
    const versionCount = (d.versions || []).length;
    const lines = [
      d.title || d.id,
      `Version ${d.major}.${d.minor}` + (d.status ? ` · ${this.lifecycleLabel(d.status)}` : ""),
      "",
      `This removes ${versionCount} design version${versionCount === 1 ? "" : "s"} and the ADR`
        + (d.status === "approved" ? ", including the approved text." : "."),
      "The bound milestone is not changed. It cannot be undone.",
    ];
    window.AnvilUI.modal.requireTyped(this.uiModal(), {
      title: "Delete design?",
      body: lines.join("\n"),
      word: "DELETE",
      confirmLabel: "Delete design",
      onConfirm: async () => {
        try {
          await api.v1.deliveryDeleteDesign(id);
          await this.reload();
          this.render();
        } catch (e) {
          this.showNotice(e.message || e, "Delete failed");
        }
      },
    });
  },

  openCreateDesignModal() {
    const route = this.parseRoute();
    const onlyMajor =
      route.routeMajor != null ? route.routeMajor : this.selectedMajor;
    const cands = this.designCandidateMilestones(onlyMajor);
    if (!cands.length) {
      const scope =
        onlyMajor != null
          ? ` in ${this.majorLabel(onlyMajor)}`
          : "";
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">New design</h3>
          <p class="muted">Every milestone already has a design${this.esc(scope)}, or there are no requirements yet.</p>
          <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
            <button type="button" data-close-ui>OK</button>
          </div>
        </div>`);
      return;
    }
    const opts = cands
      .map((c, i) => {
        const w = this.findWorker(c.eng.worker_id);
        const label = `${w ? w.label || w.id : c.eng.worker_id} · ${c.major}.${c.minor} — ${c.title || "untitled"}`;
        return `<option value="${i}">${this.esc(label)}</option>`;
      })
      .join("");
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">New design</h3>
        <p class="muted" style="margin-top:0">Bind a milestone, author ADR + Versions, then Save → View → Approve (Bootstrap only after Approve).</p>
        <label>Milestone</label>
        <select id="des-create-pick">${opts}</select>
        <label>Title (optional)</label>
        <input id="des-create-title" placeholder="Defaults to milestone title" />
        <div class="err" id="des-create-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="des-create-ok">Create</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    modal.querySelector("#des-create-ok").onclick = async () => {
      const err = modal.querySelector("#des-create-err");
      err.textContent = "";
      const idx = Number(modal.querySelector("#des-create-pick").value);
      const c = cands[idx];
      if (!c) return;
      try {
        const d = await api.v1.deliveryCreateDesign({
          eng_req_id: c.eng.id,
          major: c.major,
          minor: c.minor,
          title: modal.querySelector("#des-create-title").value.trim(),
        });
        this.closeUiModal();
        await this.reload();
        location.hash = "#/delivery/designs/" + d.id;
      } catch (e) {
        err.textContent = e.message || String(e);
      }
    };
  },

  viewDesList(routeMajor) {
    const filter = this._desFilter || "all";
    const majLabel = this.majorLabel(routeMajor);
    const list = (this.data.designs || []).filter((d) => {
      if (d.major !== routeMajor) return false;
      if (filter === "all") return true;
      return (d.status || "") === filter;
    });
    const cards = list
      .map((d) => {
        const eng = this.findEng(d.eng_req_id);
        const w = eng ? this.findWorker(eng.worker_id) : null;
        const vers = d.versions || [];
        const nVer = vers.length;
        const nReady = vers.filter((v) => (v.status || "") === "ready").length;
        return `
      <div class="del-des-card" role="link" tabindex="0" data-des-open="${this.esc(d.id)}">
        <button type="button" class="ghost del-des-card-x" data-des-delete="${this.esc(d.id)}"
          title="Delete design">×</button>
        <div class="del-des-card-top">
          <strong class="del-des-card-title">${this.esc(d.title)}</strong>
          ${this.badge(d.status)}
        </div>
        <div class="muted del-des-card-meta">
          <span>${this.esc(w ? w.label || w.id : eng?.title || d.eng_req_id)}</span>
          <span>·</span>
          <code>${this.esc(`${d.major}.${d.minor}`)}</code>
          <span>·</span>
          <span>${nVer} version${nVer === 1 ? "" : "s"}</span>
        </div>
        <div class="del-des-card-foot muted">
          ${
            d.status === "approved"
              ? "Approved"
              : nVer === 0
                ? "No Versions yet"
                : `${nReady}/${nVer} ready`
          }
        </div>
      </div>`;
      })
      .join("");
    return `
      <p class="del-crumbs">
        <a href="#/delivery/designs">Generations</a>
        <span class="muted">/</span>
        <span>${this.esc(majLabel)}</span>
      </p>
      <div class="toolbar del-des-list-bar">
        <label class="muted" style="margin:0">Status</label>
        <select id="des-filter" style="width:auto;margin:0">
          <option value="all"${filter === "all" ? " selected" : ""}>All</option>
          <option value="draft"${filter === "draft" ? " selected" : ""}>Draft</option>
          <option value="approved"${filter === "approved" ? " selected" : ""}>Approved</option>
        </select>
        <span class="muted" style="flex:1">${list.length} design${list.length === 1 ? "" : "s"} in ${this.esc(majLabel)}</span>
      </div>
      <div class="del-des-grid">
        ${cards || `<p class="muted" style="grid-column:1/-1">No designs in ${this.esc(majLabel)} yet.</p>`}
        <button type="button" class="del-des-card del-des-card-add" id="des-new-card"
          title="New design">
          <span class="del-req-plus">+</span>
          <span class="muted">New design</span>
        </button>
      </div>`;
  },

  adrSectionDefs() {
    return [
      {
        key: "adr_context",
        cite: "ADR.Context",
        label: "Context",
        hint: "Why a decision is needed…",
        preview: false,
        rows: 3,
      },
      {
        key: "adr_decision",
        cite: "ADR.Decision",
        label: "Decision",
        hint: "What we chose and why it wins…",
        preview: false,
        rows: 3,
      },
      {
        key: "adr_consequences",
        cite: "ADR.Consequences",
        label: "Consequences",
        hint: "Follow-on risks, costs, constraints…",
        preview: false,
        rows: 3,
      },
      {
        key: "adr_spec",
        cite: "ADR.Spec",
        label: "Spec",
        hint: "Interfaces, contracts, execution notes (Markdown)…",
        preview: true,
        rows: 6,
      },
      {
        key: "adr_architecture",
        cite: "ADR.Architecture",
        label: "Architecture",
        hint: "Component topology — use a ```mermaid flowchart fence",
        preview: true,
        rows: 8,
        template: "architecture",
      },
      {
        key: "adr_sequence",
        cite: "ADR.Sequence",
        label: "Sequence",
        hint: "Key flows — use a ```mermaid sequenceDiagram fence",
        preview: true,
        rows: 8,
        template: "sequence",
      },
      {
        key: "adr_sample",
        cite: "ADR.Sample",
        label: "Sample & capacity",
        hint: "Samples, capacity / redundancy notes (Markdown)…",
        preview: true,
        rows: 5,
      },
    ];
  },

  readAdrFields(root) {
    const out = {};
    for (const s of this.adrSectionDefs()) {
      const ta = root.querySelector(`textarea[data-adr-field="${s.key}"]`);
      out[s.key] = ta ? ta.value : "";
    }
    return out;
  },

  adrFieldValue(d, key) {
    if (!d) return "";
    const v = d[key];
    if (v) return v;
    // Legacy: single adr blob maps to Decision when sections empty.
    if (
      key === "adr_decision" &&
      !d.adr_context &&
      !d.adr_decision &&
      !d.adr_consequences &&
      !d.adr_spec &&
      !d.adr_architecture &&
      !d.adr_sequence &&
      !d.adr_sample
    ) {
      return d.adr || "";
    }
    return "";
  },

  renderAdrPane(sec, d, approved) {
    const val = this.adrFieldValue(d, sec.key);
    if (approved) {
      // Approved: preview-only — no Edit / Template / Save surface.
      return `
      <div class="md-pane del-cite-field del-pane-ro" data-md-key="${this.esc(sec.key)}"
        data-cite-key="${this.esc(sec.cite)}" data-cite-drag-ok="0" draggable="false"
        data-md-ro="1">
        <div class="md-pane-head">
          <label>${this.esc(sec.label)}</label>
          <span class="muted md-ro-tag">read-only</span>
        </div>
        <div class="md-preview md-preview-ro" data-md-ro-body>${this.esc(val)}</div>
        <textarea data-adr-field="${this.esc(sec.key)}" hidden readonly>${this.esc(val)}</textarea>
      </div>`;
    }
    const modeBtns = sec.preview
      ? `<div class="md-mode-toggle">
          <button type="button" class="ghost active" data-md-mode="edit">Edit</button>
          <button type="button" class="ghost" data-md-mode="preview">Preview</button>
        </div>`
      : "";
    const insert = sec.template
      ? `<button type="button" class="ghost md-insert-tpl" data-md-insert="${this.esc(sec.template)}" title="Insert mermaid stub">Template</button>`
      : "";
    return `
      <div class="md-pane del-cite-field"
        data-md-key="${this.esc(sec.key)}" data-cite-key="${this.esc(sec.cite)}"
        data-cite-drag-ok="${sec.preview ? "1" : "0"}" draggable="false">
        <div class="md-pane-head">
          <label>${this.esc(sec.label)}</label>
          ${insert}
          ${modeBtns}
        </div>
        <div class="md-edit">
          <textarea data-adr-field="${this.esc(sec.key)}" rows="${sec.rows}"
            placeholder="${this.esc(sec.hint)}">${this.esc(val)}</textarea>
        </div>
        ${sec.preview ? `<div class="md-preview" hidden></div>` : ""}
      </div>`;
  },

  /** Enable cite-drag only when an ADR pane is in Preview (not Edit). */
  setAdrPaneDrag(pane, mode) {
    if (!pane || pane.getAttribute("data-cite-drag-ok") !== "1") return;
    const allow = mode === "preview";
    pane.draggable = allow;
    pane.classList.toggle("cite-drag", allow);
  },

  versionFieldDefs() {
    return [
      {
        field: "gate",
        label: "Gate",
        citeSuffix: "Gate",
        hint: "Entry/exit criteria, DoD, human check…",
        rows: 4,
        template: null,
      },
      {
        field: "sub_architecture",
        label: "Sub-architecture",
        citeSuffix: "Arch",
        hint: "Slice of the system for this Version — mermaid OK",
        rows: 6,
        template: "architecture",
      },
      {
        field: "class_design",
        label: "Class design",
        citeSuffix: "Class",
        hint: "Types / interfaces / modules AI may touch…",
        rows: 6,
        template: null,
      },
    ];
  },

  versionsReady(vers) {
    if (!vers || !vers.length) return false;
    return vers.every(
      (v) =>
        String(v.gate || "").trim() &&
        String(v.sub_architecture || "").trim() &&
        String(v.class_design || "").trim(),
    );
  },

  /** Merge live form fields from Design detail into a snapshot for View/export. */
  readLiveDesignSnapshot(root, d) {
    const snap = {
      ...d,
      title: d.title,
      versions: (d.versions || []).map((v) => ({ ...v })),
    };
    if (!root) return snap;
    const adr = this.readAdrFields(root);
    Object.assign(snap, adr);
    const cards = [...root.querySelectorAll("[data-ver-id]")];
    for (const card of cards) {
      const vid = card.getAttribute("data-ver-id");
      const ver = snap.versions.find((v) => v.id === vid);
      if (!ver) continue;
      ver.title = card.querySelector("[data-ver-title]")?.value ?? ver.title;
      ver.gate = card.querySelector('textarea[data-ver-field="gate"]')?.value ?? ver.gate;
      ver.sub_architecture =
        card.querySelector('textarea[data-ver-field="sub_architecture"]')?.value ??
        ver.sub_architecture;
      ver.class_design =
        card.querySelector('textarea[data-ver-field="class_design"]')?.value ??
        ver.class_design;
      const depBoxes = card.querySelectorAll("[data-ver-dep]");
      if (depBoxes.length) {
        ver.depends_on = [...depBoxes]
          .filter((cb) => cb.checked)
          .map((cb) => cb.value);
      }
    }
    return snap;
  },

  /** Full Design Markdown document (ADR + Versions) for View / export. */
  composeDesignMarkdown(d) {
    const lines = [];
    const push = (s) => lines.push(s);
    const section = (title, body) => {
      const t = String(body ?? "").trim();
      push(`### ${title}`);
      push("");
      push(t || "_(empty)_");
      push("");
    };
    push(`# ${d.title || "Untitled design"}`);
    push("");
    push(
      `> Bound \`${d.major}.${d.minor}\` · status **${d.status || "draft"}**` +
        (d.version ? ` · req \`${d.version}\`` : ""),
    );
    push("");
    push("## ADR");
    push("");
    for (const s of this.adrSectionDefs()) {
      section(s.label, this.adrFieldValue(d, s.key));
    }
    push("## Versions");
    push("");
    const vers = d.versions || [];
    if (!vers.length) {
      push("_(No Versions yet.)_");
      push("");
    } else {
      const byId = Object.fromEntries(vers.map((v) => [v.id, v]));
      vers.forEach((v, i) => {
        const ord = v.sort_order || i + 1;
        const name = (v.title || "").trim() || `Version ${ord}`;
        push(`### v${ord} — ${name}`);
        push("");
        const deps = v.depends_on || [];
        if (deps.length) {
          const labels = deps.map((id) => {
            const p = byId[id];
            if (!p) return id;
            const po = p.sort_order || "?";
            return `v${po} — ${(p.title || "").trim() || "Untitled"}`;
          });
          push(`**Depends on:** ${labels.join(", ")}`);
          push("");
        } else {
          push("**Depends on:** _(none)_");
          push("");
        }
        section("Gate", v.gate);
        section("Sub-architecture", v.sub_architecture);
        section("Class design", v.class_design);
      });
    }
    return lines.join("\n").replace(/\n{3,}/g, "\n\n");
  },

  markDesignViewed(id) {
    if (!this._desViewedIds) this._desViewedIds = {};
    this._desViewedIds[id] = true;
  },

  /**
   * Save → validate Versions → Approve → reopen View with Bootstrap.
   * Always gives feedback (never a silent disabled click).
   */
  async runDesignApprove(designId, { root, save, errEl } = {}) {
    const setErr = (msg) => {
      if (errEl) errEl.textContent = msg || "";
      else if (msg) this.showNotice(msg, "Cannot Approve");
    };
    setErr("");
    try {
      const d0 = (this.data.designs || []).find((x) => x.id === designId);
      if (!d0) {
        setErr("Design not found.");
        return false;
      }
      if (d0.status === "approved") {
        this.openDesignViewModal(designId, { root: null, save: null });
        return true;
      }
      const live = this.readLiveDesignSnapshot(root || this.el, d0);
      if (!(live.versions || []).length) {
        setErr("Add at least one Version, then fill Gate / Sub-architecture / Class design.");
        return false;
      }
      if (!this.versionsReady(live.versions)) {
        setErr("Every Version needs Gate, Sub-architecture, and Class design before Approve.");
        return false;
      }
      if (typeof save === "function") await save();
      else {
        // Persist live snapshot when opened without a bound save (e.g. after reload).
        const adr = {};
        for (const s of this.adrSectionDefs()) {
          adr[s.key] = this.adrFieldValue(live, s.key);
        }
        await api.v1.deliveryPatchDesign(designId, adr);
        for (const v of live.versions || []) {
          await api.v1.deliveryPatchDesignVersion(designId, v.id, {
            title: v.title || "",
            gate: v.gate || "",
            sub_architecture: v.sub_architecture || "",
            class_design: v.class_design || "",
            depends_on: v.depends_on || [],
          });
        }
      }
      this.markDesignViewed(designId);
      await api.v1.deliveryApproveDesign(designId);
      await this.reload();
      this.render();
      this.openDesignViewModal(designId, { root: null, save: null });
      return true;
    } catch (e) {
      setErr(e.message || String(e));
      return false;
    }
  },

  openDesignViewModal(designId, { root, save } = {}) {
    const d = (this.data.designs || []).find((x) => x.id === designId);
    if (!d) return;
    const snap = this.readLiveDesignSnapshot(root || this.el, d);
    const md = this.composeDesignMarkdown(snap);
    const approved = d.status === "approved";
    const versOk = this.versionsReady(snap.versions || []);
    this.markDesignViewed(designId);
    this.showUiModal(`
      <div class="modal-card modal-doc">
        <div class="modal-doc-head">
          <h3>Design document</h3>
          ${this.badge(d.status)}
          <div class="modal-doc-actions">
            <button type="button" class="ghost active" data-des-doc-mode="preview">Preview</button>
            <button type="button" class="ghost" data-des-doc-mode="raw">Markdown</button>
          </div>
        </div>
        <div class="modal-doc-body">
          <div class="md-preview modal-doc-preview" id="des-doc-preview"></div>
          <textarea class="modal-doc-raw" id="des-doc-raw" readonly hidden></textarea>
        </div>
        <div class="modal-doc-foot">
          <span class="muted" id="des-doc-hint">
            ${
              approved
                ? "Approved — publish phases to Roadmap with Bootstrap, or Close and use the toolbar."
                : versOk
                  ? "Review the document, then Approve to lock editing and unlock Bootstrap roadmap."
                  : "Complete Gate / Sub-architecture / Class design on every Version before Approve."
            }
          </span>
          <button type="button" class="ghost" data-close-ui>Close</button>
          ${
            !approved
              ? `<button type="button" id="des-doc-approve">Approve</button>`
              : `<button type="button" id="des-doc-bootstrap">Bootstrap roadmap</button>`
          }
        </div>
        <div class="err" id="des-doc-err"></div>
      </div>`);
    const modal = this.uiModal();
    const preview = modal.querySelector("#des-doc-preview");
    const raw = modal.querySelector("#des-doc-raw");
    if (raw) raw.value = md;
    const showMode = async (mode) => {
      const isPrev = mode === "preview";
      if (preview) preview.hidden = !isPrev;
      if (raw) raw.hidden = isPrev;
      modal.querySelectorAll("[data-des-doc-mode]").forEach((btn) => {
        btn.classList.toggle("active", btn.getAttribute("data-des-doc-mode") === mode);
      });
      if (isPrev && preview && window.MdPreview) {
        preview.innerHTML = window.MdPreview.render(md);
        await window.MdPreview.mountPreview(preview);
      }
    };
    modal.querySelectorAll("[data-des-doc-mode]").forEach((btn) => {
      btn.addEventListener("click", () => showMode(btn.getAttribute("data-des-doc-mode")));
    });
    showMode("preview");

    modal.querySelector("#des-doc-approve")?.addEventListener("click", async () => {
      const errEl = modal.querySelector("#des-doc-err");
      await this.runDesignApprove(designId, { root, save, errEl });
    });
    modal.querySelector("#des-doc-bootstrap")?.addEventListener("click", async () => {
      const errEl = modal.querySelector("#des-doc-err");
      if (errEl) errEl.textContent = "";
      try {
        await api.v1.deliveryBootstrapDesign(designId);
        this.closeUiModal();
        await this.reload();
        const fresh = (this.data.designs || []).find((x) => x.id === designId) || d;
        location.hash = `#/delivery/roadmap/m/${fresh.major}/ms_${fresh.eng_req_id}_${fresh.major}_${fresh.minor}`;
      } catch (e) {
        if (errEl) errEl.textContent = e.message || String(e);
        else this.showNotice(e.message || String(e), "Bootstrap failed");
      }
    });
  },

  renderVersionFieldPane(ver, def, approved) {
    const cite = `V${ver.sort_order}.${def.citeSuffix}`;
    const mdKey = `ver:${ver.id}:${def.field}`;
    const val =
      def.field === "gate"
        ? ver.gate || ""
        : def.field === "sub_architecture"
          ? ver.sub_architecture || ""
          : ver.class_design || "";
    if (approved) {
      return `
      <div class="md-pane del-cite-field del-pane-ro" data-md-key="${this.esc(mdKey)}"
        data-cite-key="${this.esc(cite)}" data-cite-drag-ok="0" draggable="false" data-md-ro="1">
        <div class="md-pane-head">
          <label>${this.esc(def.label)}</label>
          <span class="muted md-ro-tag">read-only</span>
        </div>
        <div class="md-preview md-preview-ro" data-md-ro-body>${this.esc(val)}</div>
        <textarea data-ver-field="${this.esc(def.field)}" hidden readonly>${this.esc(val)}</textarea>
      </div>`;
    }
    const insert = def.template
      ? `<button type="button" class="ghost md-insert-tpl" data-md-insert="${this.esc(def.template)}" title="Insert mermaid stub">Template</button>`
      : "";
    return `
      <div class="md-pane del-cite-field" data-md-key="${this.esc(mdKey)}"
        data-cite-key="${this.esc(cite)}" data-cite-drag-ok="1" draggable="false">
        <div class="md-pane-head">
          <label>${this.esc(def.label)}</label>
          ${insert}
          <div class="md-mode-toggle">
            <button type="button" class="ghost active" data-md-mode="edit">Edit</button>
            <button type="button" class="ghost" data-md-mode="preview">Preview</button>
          </div>
        </div>
        <div class="md-edit">
          <textarea data-ver-field="${this.esc(def.field)}" rows="${def.rows}"
            placeholder="${this.esc(def.hint)}">${this.esc(val)}</textarea>
        </div>
        <div class="md-preview" hidden></div>
      </div>`;
  },

  renderVersionDepends(ver, allVers, approved) {
    const deps = new Set(ver.depends_on || []);
    const others = (allVers || []).filter((x) => x.id !== ver.id);
    if (!others.length) {
      return `<div class="del-ver-deps muted">Depends on — no other Versions yet.</div>`;
    }
    if (approved) {
      const byId = Object.fromEntries(others.map((x) => [x.id, x]));
      const chips = [...deps]
        .map((id) => {
          const p = byId[id];
          if (!p) return null;
          return `<span class="del-ver-dep-chip">v${p.sort_order} · ${this.esc(p.title || "Untitled")}</span>`;
        })
        .filter(Boolean);
      return `
        <div class="del-ver-deps del-ver-deps-ro">
          <div class="del-ver-deps-label muted">Depends on</div>
          <div class="del-ver-dep-chips">${chips.join("") || `<span class="muted">None</span>`}</div>
        </div>`;
    }
    const boxes = others
      .map((o) => {
        const checked = deps.has(o.id) ? " checked" : "";
        return `<label class="del-ver-dep-opt">
          <input type="checkbox" data-ver-dep value="${this.esc(o.id)}"${checked} />
          <span>v${o.sort_order} · ${this.esc(o.title || "Untitled")}</span>
        </label>`;
      })
      .join("");
    return `
      <div class="del-ver-deps">
        <div class="del-ver-deps-label">Depends on</div>
        <p class="muted del-ver-deps-hint">Prerequisites that must finish before this Version.</p>
        <div class="del-ver-dep-list">${boxes}</div>
      </div>`;
  },

  renderVersionCard(ver, allVers, approved) {
    const fields = this.versionFieldDefs()
      .map((def) => this.renderVersionFieldPane(ver, def, approved))
      .join("");
    const depsBlock = this.renderVersionDepends(ver, allVers, approved);
    const ready = (ver.status || "") === "ready" || this.versionsReady([ver]);
    if (approved) {
      return `
      <div class="card del-ver del-ver-ro" data-ver-id="${this.esc(ver.id)}" id="des-ver-${this.esc(ver.id)}">
        <div class="toolbar del-ver-head">
          <span class="muted del-ver-ord">v${ver.sort_order}</span>
          <strong class="del-ver-title-ro">${this.esc(ver.title || `Version ${ver.sort_order}`)}</strong>
          <span class="badge ok">Locked</span>
        </div>
        ${depsBlock}
        ${fields}
      </div>`;
    }
    return `
      <div class="card del-ver" data-ver-id="${this.esc(ver.id)}" id="des-ver-${this.esc(ver.id)}">
        <div class="toolbar del-ver-head">
          <span class="muted del-ver-ord">v${ver.sort_order}</span>
          <input class="del-ver-title-input" data-ver-title value="${this.esc(ver.title || "")}"
            placeholder="Version title" />
          ${ready ? `<span class="badge ok">Ready</span>` : `<span class="badge">Draft</span>`}
          <button type="button" class="ghost" data-ver-delete="${this.esc(ver.id)}" title="Remove Version">×</button>
        </div>
        ${depsBlock}
        ${fields}
      </div>`;
  },

  /** Mount Markdown preview into approved read-only panes. */
  mountDesignReadOnlyPreviews(root) {
    if (!root || !window.MdPreview) return;
    root.querySelectorAll("[data-md-ro='1']").forEach(async (pane) => {
      const body = pane.querySelector("[data-md-ro-body]");
      const ta = pane.querySelector("textarea");
      if (!body) return;
      const md = ta ? ta.value : body.textContent || "";
      body.innerHTML = window.MdPreview.render(md);
      await window.MdPreview.mountPreview(body);
    });
  },

  desCrumbs(designId) {
    const d = (this.data.designs || []).find((x) => x.id === designId);
    const root = "#/delivery/designs";
    const items = [{ label: "Designs", href: root }];
    if (d) {
      items.push({ label: this.majorLabel(d.major), href: `${root}/m/${d.major}` });
      items.push({ label: d.title || d.id, href: null });
    }
    return this.breadcrumbHtml(items);
  },

  viewDesDetail(id) {
    const d = this.data.designs.find((x) => x.id === id);
    if (!d) return `<p class="err">Design not found.</p>`;
    const eng = this.findEng(d.eng_req_id);
    const w = eng ? this.findWorker(eng.worker_id) : null;
    const approved = d.status === "approved";
    const vers = d.versions || [];
    const msKey = `${d.major}.${d.minor}`;
    const reqHref = `#/delivery/requirements/${this.esc(d.eng_req_id)}/${d.major}/${d.minor}`;
    const verCards = vers.map((v) => this.renderVersionCard(v, vers, approved)).join("");
    const adrSections = this.adrSectionDefs();
    const adrHtml = adrSections.map((s) => this.renderAdrPane(s, d, approved)).join("");
    const fieldDefs = this.versionFieldDefs();
    const citeNav = [
      {
        id: "adr",
        label: "ADR",
        children: adrSections.map((s) => ({
          id: "adr:" + s.key,
          label: s.label,
        })),
      },
      {
        id: "versions",
        label: "Versions",
        children: vers.flatMap((v) => {
          const base = v.title || `Version ${v.sort_order}`;
          return fieldDefs.map((f) => ({
            id: `verfield:${v.id}:${f.field}`,
            label: `${base} · ${f.label}`,
          }));
        }),
      },
    ];
    const chat = this.polishChat(`des:${d.id}`, {
      id: "ai-chat-des",
      title: "Polish chat",
      placeholder: "How should this design be polished?",
      tall: true,
      rows: 3,
      allowDrop: !approved,
      citeNav,
      emptyHint: "Open ADR ▾ or Versions ▾, cite a field, then send.",
      onCiteNav: (navId, c) => {
        if (navId.startsWith("adr:")) {
          const key = navId.slice(4);
          const sec = adrSections.find((x) => x.key === key);
          if (sec) c.addCite(sec.cite);
        } else if (navId.startsWith("verfield:")) {
          const rest = navId.slice("verfield:".length);
          const i = rest.indexOf(":");
          if (i < 0) return;
          const vid = rest.slice(0, i);
          const field = rest.slice(i + 1);
          const ver = vers.find((x) => x.id === vid);
          const def = fieldDefs.find((x) => x.field === field);
          if (ver && def) c.addCite(`V${ver.sort_order}.${def.citeSuffix}`);
        } else if (navId.startsWith("ver:")) {
          const vid = navId.slice(4);
          const ver = vers.find((x) => x.id === vid);
          if (ver) c.addCite(ver.title || `Version ${ver.sort_order}`);
        }
      },
      onScrollCite: (key) => {
        const root = this.el;
        const pane = [...(root?.querySelectorAll("[data-cite-key]") || [])].find(
          (el) => el.getAttribute("data-cite-key") === key,
        );
        if (pane) {
          pane.scrollIntoView({ behavior: "smooth", block: "center" });
          const field = pane.querySelector("textarea");
          field?.focus?.();
          window.AiChat.flash(pane);
          return;
        }
        const card = [...(root?.querySelectorAll("[data-ver-id]") || [])].find((el) => {
          const vid = el.getAttribute("data-ver-id");
          const ver = vers.find((x) => x.id === vid);
          return ver && (ver.title === key || `Version ${ver.sort_order}` === key);
        });
        if (card) {
          card.scrollIntoView({ behavior: "smooth", block: "center" });
          window.AiChat.flash(card);
        }
      },
      // Real drafting (p_0.46.6). This surface used to append the operator's own
      // text into the ADR Decision and, when a version was cited, into that
      // version's Gate - on Send, with no confirmation. The Gate it touched
      // gates real phase jobs. Now the assistant proposes and the operator
      // applies; nothing reaches the record without that second act.
      onSend: async ({ composed, cites }, c) => {
        if (approved) return;
        const citeVer = vers.find(
          (v) =>
            composed.includes("@" + (v.title || "")) ||
            composed.includes(`@V${v.sort_order}.Gate`),
        );
        if (citeVer) {
          await window.AnvilUI.assist.ask({
            surface: "design_version",
            field: "gate",
            instruction: composed,
            cites,
            current: { gate: (citeVer.gate || "").trim() },
            host: this.el.querySelector("#des-assist"),
            chat: c,
            onApply: async (_field, value) => {
              await api.v1.deliveryPatchDesignVersion(d.id, citeVer.id, { gate: value });
              await this.reload();
              this.render();
            },
          });
          return;
        }
        await window.AnvilUI.assist.ask({
          surface: "design",
          field: "adr_decision",
          instruction: composed,
          cites,
          current: { adr_decision: (this.adrFieldValue(d, "adr_decision") || "").trim() },
          host: this.el.querySelector("#des-assist"),
          chat: c,
          onApply: async (_field, value) => {
            await api.v1.deliveryPatchDesign(d.id, { adr_decision: value });
            await this.reload();
            this.render();
          },
        });
      },
    });
    const actions = [];
    if (!approved) {
      actions.push(
        `<button type="button" class="ghost" id="des-save" title="Save temporary draft">Save</button>`,
      );
      actions.push(
        `<button type="button" class="ghost" id="des-view" title="Preview full Design Markdown">View</button>`,
      );
      actions.push(
        `<button type="button" id="des-approve" title="Save, validate Versions, then Approve">Approve</button>`,
      );
    } else {
      // Approved: editing locked — View document + Bootstrap to publish roadmap.
      actions.push(
        `<button type="button" class="ghost" id="des-view" title="Open approved Design Markdown">View</button>`,
      );
      actions.push(
        `<button type="button" id="des-bootstrap" title="Seed roadmap phases from Versions">Bootstrap roadmap</button>`,
      );
    }
    const addVerBtn = approved
      ? ""
      : `<button type="button" class="ghost" id="des-ver-add">+ Version</button>`;
    const flowHint = approved
      ? "Approved — editing locked. Bootstrap roadmap to publish Versions as phases."
      : "Flow: Save (draft) → View (full Markdown) → Approve → Bootstrap roadmap.";
    // The assist host sits beside the chat: the assistant proposes, and the
    // proposals render here until applied or discarded.
    const chatHtml = approved
      ? ""
      : chat.render() + `<div id="des-assist" class="assist-host"></div>`;
    return `
      <p class="del-crumbs">
        <a href="#/delivery/designs">Generations</a>
        <span class="muted">/</span>
        <a href="#/delivery/designs/m/${d.major}">${this.esc(this.majorLabel(d.major))}</a>
        <span class="muted">/</span>
        <span>${this.esc(d.title)}</span>
      </p>
      ${this.auditHtml("design", d.id)}
      <div class="del-split${approved ? "" : " del-split-chatwide"}">
        <div class="del-main">
          <div class="card${approved ? " del-des-locked" : ""}">
            <div class="toolbar">
              <div style="flex:1;min-width:0">
                <div class="muted">Bound
                  <a href="#/delivery/requirements/${this.esc(d.eng_req_id)}">${this.esc(w ? w.label || w.id : eng?.title || d.eng_req_id)}</a>
                  · <a href="${reqHref}"><code>${this.esc(d.version || msKey)}</code></a>
                  · ${approved ? "approved · view only" : "draft workspace"}</div>
                <h3 style="margin:0.25rem 0 0">${this.esc(d.title)}</h3>
                <p class="del-flow-hint">${this.esc(flowHint)}</p>
              </div>
              ${this.badge(d.status)}
              ${actions.join("")}
            </div>
            <div class="err" id="des-err"></div>
            <div class="del-adr-block">
              <div class="del-adr-head">
                <h4>ADR</h4>
                <span class="muted">${approved ? "Locked after Approve" : "Context · Decision · Consequences · Spec · Architecture · Sequence · Sample"}</span>
              </div>
              ${adrHtml}
            </div>
          </div>
          <div class="toolbar del-des-ver-head">
            <h4 style="margin:0;flex:1">Versions</h4>
            <span class="muted">${vers.length} card${vers.length === 1 ? "" : "s"}${approved ? " · locked" : ""}</span>
            ${addVerBtn}
          </div>
          <div class="del-des-ver-list">${verCards || `<p class="muted">No Versions yet — add one to define AI execution boundaries.</p>`}</div>
        </div>
        ${chatHtml}
      </div>`;
  },

  /** Milestones under one generation (major): graph + card list. */
  findDesignForMilestone(ms) {
    if (!ms) return null;
    const bind = this.parseRoadmapMsBind(ms.id);
    const designs = this.data.designs || [];
    if (bind) {
      return (
        designs.find(
          (d) =>
            d.eng_req_id === bind.engId &&
            d.major === bind.major &&
            d.minor === bind.minor,
        ) || null
      );
    }
    return designs.find((d) => d.title === ms.title) || null;
  },

  /** Single milestone workspace — Versions are phases under it. */
});
