// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Create-milestone wizard: fleet/node/worker picker, filesystem, submit.
 *
 * Part of the Delivery feature object. Each surface file assigns its own methods
 * onto the one `window.Features.delivery`, so the mounting contract in app.js
 * (`mount` / `unmount` / `onRoute`) and the router are unchanged: this is a
 * mechanical split of one 6 000-line literal, not a redesign.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  openCreateWizardForInventoryWorker(fleet, node, worker) {
    if (!worker || !worker.id) return;
    const project = String(worker.project_rel || "").replace(/^\/+|\/+$/g, "");
    this._create = {
      step: "folder",
      bound: false,
      engId: null,
      fleets: [],
      nodes: [],
      fleet: fleet || { id: "" },
      node: node || { id: "" },
      worker: {
        id: worker.id,
        label: worker.label || worker.id,
        project_rel: project,
      },
      projectRoot: project,
      fsPath: project,
      selected: "",
      reqsRel: "",
      title: worker.label || "",
      status: "awaiting_design",
      goals: "",
      completion: "",
      versions: [],
      nextVer: null,
      chatOpen: true,
      chatLog: [],
      inventoryBound: true,
    };
    this.renderCreateWizard();
    this.createFsLoad();
  },

  openCreateWizard() {
    this._create = {
      step: "fleet",
      bound: false,
      engId: null,
      fleets: [],
      nodes: [],
      fleet: null,
      node: null,
      worker: null,
      projectRoot: "",
      fsPath: "",
      selected: "",
      reqsRel: "",
      title: "",
      status: "awaiting_design",
      goals: "",
      completion: "",
      versions: [],
      nextVer: null,
      chatOpen: true,
      chatLog: [],
    };
    this.renderCreateWizard();
    this.loadCreateFleets();
  },

  /** Add milestone on an eng-req whose Worker + reqs folder are already bound. */
  async openCreateWizardBound(engId) {
    const eng = this.findEng(engId);
    if (!eng) {
      this.showNotice("Engineering requirement not found.", "Not found");
      return;
    }
    const w = this.findWorker(eng.worker_id) || {};
    const project = String(w.project_rel || "").replace(/^\/+|\/+$/g, "");
    const maj =
      this.developingMajor() != null
        ? this.developingMajor()
        : this.selectedMajor != null
          ? this.selectedMajor
          : 0;
    this._create = {
      step: "config",
      bound: true,
      engId: eng.id,
      fleets: [],
      nodes: [],
      fleet: { id: w.fleet || "" },
      node: { id: w.node || "" },
      worker: {
        id: eng.worker_id,
        label: w.label || eng.worker_id,
        project_rel: project,
      },
      projectRoot: project,
      fsPath: "",
      selected: "",
      reqsRel: eng.reqs_rel || "docs/requirements",
      title: "",
      status: "awaiting_design",
      goals: "",
      completion: "",
      versions: [],
      nextVer: null,
      targetMajor: maj,
      chatOpen: true,
      chatLog: [],
    };
    this.renderCreateWizard();
    try {
      this._create.nextVer = await api.v1.deliveryNextMilestone({
        worker_id: eng.worker_id,
        major: String(maj),
      });
    } catch (_) {
      this._create.nextVer = { major: maj, minor: 1, patch: 1, version: `${maj}.1` };
    }
    this.renderCreateWizard();
  },

  closeCreateWizard() {
    this._create = null;
    if (this._citeBySurface) delete this._citeBySurface["create-ms"];
    if (this._msgsBySurface) delete this._msgsBySurface["create-ms"];
    if (this._chats) delete this._chats["create-ms"];
    const m = this.el?.querySelector("#del-create-modal");
    if (m) {
      m.hidden = true;
      m.innerHTML = "";
    }
  },

  createCrumbs() {
    const c = this._create;
    if (!c) return "";
    if (c.bound || c.inventoryBound) {
      const wLabel = c.worker?.label || c.worker?.id || "Worker";
      return `<div class="del-crumbs">
        <span class="del-crumb muted">${this.esc(wLabel)}</span>
        <span class="muted"> / </span>
        <span class="del-crumb${c.step === "folder" ? "" : " muted"}">${
          c.step === "folder" ? "reqs folder" : `reqs <code>${this.esc(c.reqsRel || "")}</code>`
        }</span>
        <span class="muted"> / </span>
        <span class="del-crumb${c.step === "config" ? "" : " muted"}">Milestone</span>
      </div>`;
    }
    const parts = [];
    const push = (label, step, enabled) => {
      if (enabled) {
        parts.push(
          `<button type="button" class="del-crumb" data-step="${step}">${this.esc(label)}</button>`,
        );
      } else {
        parts.push(`<span class="del-crumb muted">${this.esc(label)}</span>`);
      }
    };
    push("Schedulers", "fleet", c.step !== "fleet");
    if (c.fleet) {
      push(c.fleet.name || c.fleet.id, "node", c.step !== "fleet" && c.step !== "node");
    } else {
      parts.push(`<span class="del-crumb muted">…</span>`);
    }
    if (c.node) {
      push(c.node.name || c.node.id, "worker", ["folder", "config"].includes(c.step));
    }
    if (c.worker) {
      push(c.worker.label || c.worker.id, "folder", c.step === "config");
    }
    if (c.step === "folder") {
      parts.push(`<span class="del-crumb">Reqs folder</span>`);
    }
    if (c.step === "config") {
      push("Reqs folder", "folder", true);
      parts.push(`<span class="del-crumb">Milestone</span>`);
    }
    return `<div class="del-crumbs">${parts.join('<span class="muted"> / </span>')}</div>`;
  },

  renderCreateWizard() {
    const modal = this.el?.querySelector("#del-create-modal");
    if (!modal || !this._create) return;
    const c = this._create;
    let pane = "";
    if (c.step === "fleet") {
      const items = (c.fleets || [])
        .map(
          (f) => `
        <button type="button" class="del-nav-item" data-fleet="${this.esc(f.id)}">
          <strong>${this.esc(f.name || f.id)}</strong>
          <span class="muted">${f.online ? "online" : "offline"} · ${this.esc(f.scheduler_url || "")}</span>
        </button>`,
        )
        .join("");
      pane = `
        <p class="muted">Choose a scheduler (fleet).</p>
        <div class="del-nav-list">${items || `<p class="muted">Loading…</p>`}</div>`;
    } else if (c.step === "node") {
      const items = (c.nodes || [])
        .map(
          (n) => `
        <button type="button" class="del-nav-item" data-node="${this.esc(n.id)}">
          <strong>${this.esc(n.name || n.id)}</strong>
          <span class="muted">${this.esc(n.id)} · workers ${(n.workers || []).length}</span>
        </button>`,
        )
        .join("");
      pane = `
        <p class="muted">Choose a node.</p>
        <div class="del-nav-list">${items || `<p class="muted">No nodes on this scheduler.</p>`}</div>`;
    } else if (c.step === "worker") {
      const workers = c.node?.workers || [];
      const items = workers
        .map((w) => {
          const label = w.git_branch || w.project_rel || w.name || w.id;
          const path = w.project_rel || w.project_path || "";
          return `
        <button type="button" class="del-nav-item" data-worker="${this.esc(w.id)}">
          <strong>${this.esc(label)}</strong>
          <span class="muted"><code>${this.esc(path)}</code> · ${this.esc(w.status || "")}</span>
        </button>`;
        })
        .join("");
      pane = `
        <p class="muted">Choose a worker project.</p>
        <div class="del-nav-list">${items || `<p class="muted">No workers on this node.</p>`}</div>`;
    } else if (c.step === "folder") {
      pane = `
        <p class="muted">Browse under the worker project and select or create the requirements folder.</p>
        <div class="fs-browser fs-browser-tall">
          <div class="fs-bar">
            <button type="button" class="ghost" id="del-fs-up" title="Up">↑</button>
            <code id="del-fs-path" class="fs-path">/</code>
            <button type="button" class="ghost" id="del-fs-use">Use current</button>
            <button type="button" class="ghost" id="del-fs-new">New folder</button>
          </div>
          <div class="fs-table-head"><span>Name</span><span>Size</span><span>Modified</span></div>
          <div id="del-fs-list" class="fs-list fs-list-tall"></div>
          <p class="muted" style="margin:0.35rem 0 0">Selected: <code id="del-fs-sel">${this.esc(c.selected || "—")}</code></p>
        </div>
        <div class="err" id="del-create-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" id="del-create-back">Back</button>
          <button type="button" id="del-create-next">Next</button>
        </div>`;
    } else if (c.step === "config") {
      pane = this.renderMilestoneConfigPane(c);
    }

    const chatBtn =
      c.step === "config"
        ? `<button type="button" class="ghost" id="del-ms-chat-toggle">${
            c.chatOpen ? "Hide AI" : "AI assist"
          }</button>`
        : "";
    const modalCls =
      "modal-card modal-wide modal-tall" + (c.step === "config" && c.chatOpen ? " modal-chat" : "");
    modal.hidden = false;
    modal.innerHTML = `
      <div class="${modalCls}">
        <div class="toolbar" style="margin:0 0 0.75rem;justify-content:space-between;gap:0.5rem">
          <h3 style="margin:0;flex:1">New milestone</h3>
          ${chatBtn}
          <button type="button" class="ghost" id="del-create-close">Close</button>
        </div>
        ${this.createCrumbs()}
        <div class="del-create-pane">${pane}</div>
      </div>`;
    modal.querySelector("#del-ms-chat-toggle")?.addEventListener("click", () => {
      if (c.step === "config") this.captureMilestoneConfig(modal, c);
      c.chatOpen = !c.chatOpen;
      this.renderCreateWizard();
    });

    modal.querySelector("#del-create-close").onclick = () => this.closeCreateWizard();
    modal.querySelectorAll("[data-step]").forEach((btn) => {
      btn.onclick = () => {
        c.step = btn.getAttribute("data-step");
        if (c.step === "fleet") {
          c.fleet = null;
          c.node = null;
          c.worker = null;
        } else if (c.step === "node") {
          c.node = null;
          c.worker = null;
        } else if (c.step === "worker") {
          c.worker = null;
        }
        this.renderCreateWizard();
        if (c.step === "fleet") this.loadCreateFleets();
      };
    });
    modal.querySelectorAll("[data-fleet]").forEach((btn) => {
      btn.onclick = () => this.createPickFleet(btn.getAttribute("data-fleet"));
    });
    modal.querySelectorAll("[data-node]").forEach((btn) => {
      btn.onclick = () => this.createPickNode(btn.getAttribute("data-node"));
    });
    modal.querySelectorAll("[data-worker]").forEach((btn) => {
      btn.onclick = () => this.createPickWorker(btn.getAttribute("data-worker"));
    });
    if (c.step === "folder") {
      modal.querySelector("#del-fs-up").onclick = () => this.createFsUp();
      modal.querySelector("#del-fs-use").onclick = () => this.createFsUse();
      modal.querySelector("#del-fs-new").onclick = () => this.createFsMkdir();
      modal.querySelector("#del-create-back").onclick = () => {
        if (c.inventoryBound) {
          this.closeCreateWizard();
          return;
        }
        c.step = "worker";
        c.worker = null;
        this.renderCreateWizard();
      };
      modal.querySelector("#del-create-next").onclick = () => this.createFolderNext();
      this.createFsLoad();
    }
    if (c.step === "config") {
      this.bindMilestoneConfigPane(modal, c, {
        onSubmit: () => this.createSubmit(),
      });
      this._chats?.["create-ms"]?.mount(modal);
    }
  },

  createDraftVerKey(c, patchIdx) {
    const major = c.nextVer?.major ?? c.targetMajor ?? 0;
    const minor = c.nextVer?.minor || 1;
    return `${major}.${minor}.${patchIdx + 1}`;
  },

  renderMilestoneConfigPane(c) {
    const ver = c.nextVer?.version || "?.?";
    const gen = c.targetMajor === 0 ? "0.x" : `${c.targetMajor}.x`;
    const list = c.versions || [];
    const versionRows = list
      .map(
        (p, i) => `
      <div class="del-ver-row" data-ver-idx="${i}" data-cite-key="${this.esc(this.createDraftVerKey(c, i))}">
        <code class="del-ver-num">${this.esc(this.createDraftVerKey(c, i))}</code>
        <input class="del-ver-title" placeholder="Version title" value="${this.esc(p.title || "")}" />
        <input class="del-ver-delivery" placeholder="What this version delivers" value="${this.esc(p.delivery || "")}" />
        <button type="button" class="ghost del-ver-rm" title="Remove">×</button>
      </div>`,
      )
      .join("");
    const versionsBody = list.length
      ? `<div class="del-ver-head"><span>Version</span><span>Title</span><span>Delivery</span><span></span></div>
         <div id="del-ver-list" class="del-ver-list">${versionRows}</div>`
      : `<p class="muted del-ver-empty" id="del-ver-list">No versions yet — click + Version to add one.</p>`;
    const form = `
      <p class="muted" style="margin-top:0">
        ${
          c.bound || c.inventoryBound
            ? `Worker <strong>${this.esc(c.worker?.label || "")}</strong> · reqs <code>${this.esc(c.reqsRel || "")}</code> (locked).`
            : "Milestone requirement: Goals, version slices, and completion criteria (capabilities to ship)."
        }
      </p>
      <div class="del-ms-config">
        <label>Title</label>
        <input id="del-ms-title" value="${this.esc(c.title)}" placeholder="e.g. MVP" />
        <div class="del-ms-config-meta">
          <div>
            <label>Status</label>
            <p class="del-ms-locked-status">${this.badge("awaiting_design")}
              <span class="muted">Locked — advanced by Design → Roadmap → Develop</span></p>
          </div>
          <div>
            <label>Product version</label>
            <p class="del-ms-product"><code>${this.esc(ver)}.x</code>
              <span class="muted">generation ${this.esc(gen)}</span></p>
          </div>
        </div>
        <label>Goals</label>
        <textarea id="del-ms-goals" rows="4" placeholder="Ship …">${this.esc(c.goals || "")}</textarea>
        <div class="toolbar" style="margin:0 0 0.35rem;align-items:center">
          <label style="margin:0;flex:1">Versions <span class="muted">major.minor.patch</span></label>
          <button type="button" class="ghost" id="del-ver-add">+ Version</button>
        </div>
        ${versionsBody}
        <label>Completion criteria</label>
        <textarea id="del-ms-completion" rows="3" placeholder="Features and capabilities that must work when this milestone is done…">${this.esc(c.completion || "")}</textarea>
      </div>
      <div class="err" id="del-create-err"></div>
      <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
        <button type="button" id="del-create-ok">Create</button>
      </div>`;
    if (!c.chatOpen) return form;
    const chat = this.polishChat("create-ms", {
      id: "ai-chat-create-ms",
      title: "AI assist",
      placeholder: "What should this milestone deliver?",
      tall: true,
      rows: 3,
      allowDrop: false,
      citeNav: [
        { id: "detail", label: "Detail" },
        { id: "version", label: "Version" },
      ],
      hint: "Shared AI Chat — Detail / Version add <code>@</code> chips. Mock fills title, goals, versions, completion criteria.",
      onCiteNav: (navId, ch) => {
        if (navId === "detail") this.openCiteDetailPicker(ch);
        else if (navId === "version") this.openCiteCreateVersionPicker(c, ch);
      },
      // Real drafting for the create wizard: proposals fill the config fields, and
      // the wizard's own submit is what creates the milestone. A proposal for a
      // field the surface does not own is refused server-side.
      onSend: async ({ composed, cites }, ch) => {
        const modal = this.el?.querySelector("#del-create-modal");
        this.captureMilestoneConfig(modal, c);
        await window.AnvilUI.assist.ask({
          surface: "milestone",
          instruction: composed,
          cites,
          current: {
            title: (c.title || "").trim(),
            goals: (c.goals || "").trim(),
            completion: (c.completion || "").trim(),
          },
          host: modal?.querySelector("#ms-config-assist"),
          chat: ch,
          onApply: async (field, value) => {
            if (field === "title") c.title = value;
            else if (field === "goals") c.goals = value;
            else if (field === "completion") c.completion = value;
            this.renderCreateWizard();
          },
        });
      },    });
    return `
      <div class="del-ms-config-split">
        <div class="del-ms-config-main">${form}</div>
        <div>
          ${chat.render()}
          <div id="ms-config-assist" class="assist-host"></div>
        </div>
      </div>`;
  },

  captureMilestoneConfig(root, c) {
    if (!root || !c) return;
    c.title = (root.querySelector("#del-ms-title")?.value || "").trim();
    c.status = "awaiting_design";
    c.goals = root.querySelector("#del-ms-goals")?.value || "";
    c.completion = root.querySelector("#del-ms-completion")?.value || "";
    c.versions = [];
    root.querySelectorAll(".del-ver-row").forEach((row) => {
      c.versions.push({
        title: (row.querySelector(".del-ver-title")?.value || "").trim(),
        delivery: (row.querySelector(".del-ver-delivery")?.value || "").trim(),
      });
    });
  },

  bindMilestoneConfigPane(root, c, { onSubmit }) {
    const rebindVersions = () => {
      root.querySelectorAll(".del-ver-rm").forEach((btn) => {
        btn.onclick = () => {
          this.captureMilestoneConfig(root, c);
          const idx = Number(btn.closest(".del-ver-row")?.getAttribute("data-ver-idx"));
          if (!Number.isNaN(idx)) c.versions.splice(idx, 1);
          this.renderCreateWizard();
        };
      });
    };
    root.querySelector("#del-ver-add")?.addEventListener("click", () => {
      this.captureMilestoneConfig(root, c);
      if (!c.versions) c.versions = [];
      c.versions.push({ title: "", delivery: "" });
      this.renderCreateWizard();
    });
    rebindVersions();
    root.querySelector("#del-create-ok").onclick = onSubmit;
  },

  createFolderNext() {
    const c = this._create;
    const err = this.el.querySelector("#del-create-err");
    if (!c) return;
    const reqsRel = this.reqsRelFromPath(c.selected || "");
    if (!c.selected || reqsRel === "" || c.selected.replace(/\/+$/, "") === c.projectRoot) {
      if (err) err.textContent = "Select or create a requirements folder under the project.";
      return;
    }
    if (err) err.textContent = "";
    if (!c.title) c.title = c.worker?.label || "";
    c.step = "config";
    this.renderCreateWizard();
  },

  async loadCreateFleets() {
    const c = this._create;
    if (!c) return;
    try {
      const data = await api.v1.clusters();
      c.fleets = data.clusters || [];
      this.renderCreateWizard();
    } catch (e) {
      this.showNotice(e.message || e, "Error");
    }
  },

  createPickFleet(id) {
    const c = this._create;
    const f = (c.fleets || []).find((x) => x.id === id);
    if (!f) return;
    c.fleet = f;
    c.nodes = f.nodes || [];
    c.node = null;
    c.worker = null;
    c.step = "node";
    this.renderCreateWizard();
  },

  createPickNode(id) {
    const c = this._create;
    const n = (c.nodes || []).find((x) => x.id === id);
    if (!n) return;
    c.node = n;
    c.worker = null;
    c.step = "worker";
    this.renderCreateWizard();
  },

  async createPickWorker(id) {
    const c = this._create;
    const w = (c.node?.workers || []).find((x) => x.id === id);
    if (!w) return;
    const project = (w.project_rel || w.project_path || "").replace(/^\/+|\/+$/g, "");
    if (!project) {
      this.showNotice("Worker has no project folder.", "Cannot open");
      return;
    }
    c.worker = {
      id: w.id,
      label: w.git_branch || w.name || w.project_rel || w.id,
      project_rel: project,
      status: w.status,
    };
    c.projectRoot = project;
    c.fsPath = project;
    c.selected = "";
    c.title = c.title || c.worker.label;
    c.step = "folder";
    const maj =
      this.developingMajor() != null
        ? this.developingMajor()
        : this.selectedMajor != null
          ? this.selectedMajor
          : 0;
    c.targetMajor = maj;
    try {
      c.nextVer = await api.v1.deliveryNextMilestone({
        worker_id: w.id,
        major: String(maj),
      });
    } catch (_) {
      c.nextVer = { major: maj, minor: 1, patch: 1, version: `${maj}.1` };
    }
    this.renderCreateWizard();
  },

  async createFsLoad() {
    const c = this._create;
    const list = this.el.querySelector("#del-fs-list");
    const pathEl = this.el.querySelector("#del-fs-path");
    if (!c || !list || !pathEl) return;
    const root = c.projectRoot;
    const rel = (c.fsPath || root).startsWith(root)
      ? (c.fsPath || root).slice(root.length).replace(/^\//, "")
      : "";
    pathEl.textContent = "/" + (c.fsPath || root);
    list.innerHTML = `<div class="muted">Loading…</div>`;
    try {
      const data = await api.v1.listSandbox(c.fleet.id, c.node.id, c.fsPath || root);
      const entries = (data.entries || []).slice().sort((a, b) => {
        if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
        return String(a.name).localeCompare(b.name);
      });
      list.innerHTML = "";
      if (!entries.length) {
        list.innerHTML = `<div class="muted">Empty — use New folder</div>`;
        return;
      }
      for (const e of entries) {
        const row = document.createElement("button");
        row.type = "button";
        row.className =
          "fs-row fs-row-grid" +
          (e.is_dir && c.selected === e.path ? " selected" : "");
        const size = e.is_dir ? "—" : String(e.size || 0);
        const mtime = e.mtime ? new Date(e.mtime * 1000).toLocaleString() : "—";
        row.innerHTML = `
          <span class="fs-name">${e.is_dir ? "📁" : "📄"} ${this.esc(e.name)}</span>
          <span class="fs-meta">${this.esc(size)}</span>
          <span class="fs-meta">${this.esc(mtime)}</span>`;
        if (e.is_dir) {
          row.onclick = () => {
            c.selected = e.path;
            const sel = this.el.querySelector("#del-fs-sel");
            if (sel) sel.textContent = this.reqsRelFromPath(e.path);
            list.querySelectorAll(".fs-row").forEach((r) => r.classList.remove("selected"));
            row.classList.add("selected");
          };
          row.ondblclick = async () => {
            c.fsPath = e.path;
            c.selected = e.path;
            await this.createFsLoad();
          };
        } else {
          row.disabled = true;
        }
        list.appendChild(row);
      }
      void rel;
    } catch (e) {
      list.innerHTML = `<div class="err">${this.esc(e.message || e)}</div>`;
    }
  },

  reqsRelFromPath(absPath) {
    const c = this._create;
    const root = (c?.projectRoot || "").replace(/\/+$/, "");
    let p = (absPath || "").replace(/\\/g, "/");
    if (root && (p === root || p.startsWith(root + "/"))) {
      p = p.slice(root.length).replace(/^\//, "");
    }
    return p || "docs/requirements";
  },

  async createFsUp() {
    const c = this._create;
    if (!c) return;
    const root = c.projectRoot;
    const p = c.fsPath || root;
    if (p === root || !p.startsWith(root)) return;
    const i = p.lastIndexOf("/");
    c.fsPath = i <= root.length ? root : p.slice(0, i);
    await this.createFsLoad();
  },

  createFsUse() {
    const c = this._create;
    if (!c) return;
    const p = c.fsPath || c.projectRoot;
    if (!p || p === c.projectRoot) {
      const err = this.el.querySelector("#del-create-err");
      if (err) err.textContent = "Open or create a folder under the project (not the project root alone).";
      return;
    }
    c.selected = p;
    const sel = this.el.querySelector("#del-fs-sel");
    if (sel) sel.textContent = this.reqsRelFromPath(p);
    const err = this.el.querySelector("#del-create-err");
    if (err) err.textContent = "";
  },

  /**
   * createFsMkdir asks for a folder name in the app's own dialog.
   *
   * It used window.prompt, which blocks the event loop and cannot be styled or
   * cancelled with Escape — and it rendered on top of the create wizard, so two
   * dialogs disagreed about which was modal.
   */
  async createFsMkdir() {
    const c = this._create;
    if (!c) return;
    const err = this.el.querySelector("#del-create-err");
    if (err) err.textContent = "";
    window.AnvilUI.modal.open(
      this.uiModal(),
      `
      <div class="modal-card">
        <h3 style="margin-top:0">New folder</h3>
        <p class="muted">Under <code>${this.esc(this.reqsRelFromPath(c.fsPath || c.projectRoot || "") || ".")}</code></p>
        <label for="del-fs-new">Name</label>
        <input id="del-fs-new" autocomplete="off" spellcheck="false" />
        <div class="toolbar" style="margin-top:0.75rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="del-fs-new-ok">Create</button>
        </div>
      </div>`,
      { title: "New folder" },
    );
    const modal = this.uiModal();
    const input = modal.querySelector("#del-fs-new");
    const go = async () => {
      const name = (input.value || "").trim();
      if (!name) return;
      this.closeUiModal();
      const base = (c.fsPath || c.projectRoot || "").replace(/\/+$/, "");
      const path = base + "/" + name;
      try {
        await api.v1.mkdirSandbox(c.fleet.id, c.node.id, path);
        c.selected = path;
        const sel = this.el.querySelector("#del-fs-sel");
        if (sel) sel.textContent = this.reqsRelFromPath(path);
        await this.createFsLoad();
      } catch (e) {
        if (err) err.textContent = e.message || String(e);
      }
    };
    modal.querySelector("#del-fs-new-ok").addEventListener("click", go);
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        go();
      }
    });
  },

  async createSubmit() {
    const c = this._create;
    const err = this.el.querySelector("#del-create-err");
    if (!c?.worker) return;
    this.captureMilestoneConfig(this.el.querySelector("#del-create-modal") || this.el, c);
    const title = (c.title || "").trim() || "Untitled milestone";
    let reqsRel = "";
    if (c.bound) {
      reqsRel = (c.reqsRel || "").trim() || "docs/requirements";
    } else {
      reqsRel = this.reqsRelFromPath(c.selected || "");
      if (!c.selected || reqsRel === "" || c.selected.replace(/\/+$/, "") === c.projectRoot) {
        if (err) err.textContent = "Select or create a requirements folder under the project.";
        return;
      }
    }
    const versions = (c.versions || [])
      .filter((p) => (p.title || "").trim() || (p.delivery || "").trim())
      .map((p) => ({
        title: (p.title || "").trim(),
        delivery: (p.delivery || "").trim(),
      }));
    const existing = c.bound && c.engId ? this.findEng(c.engId) : null;
    const engTitle = c.bound ? existing?.title || c.worker.label || title : title;
    try {
      const eng = await api.v1.deliveryCreateEng({
        worker_id: c.worker.id,
        label: c.worker.label,
        fleet: c.fleet?.id || "",
        node: c.node?.id || "",
        project_rel: c.worker.project_rel,
        title: engTitle,
        reqs_rel: reqsRel,
        major: c.nextVer?.major ?? c.targetMajor ?? 0,
        minor: c.nextVer?.minor || 0,
        milestone_title: title,
        status: "awaiting_design",
        goals: c.goals || "",
        success: c.completion || "",
        phases: versions,
      });
      this.closeCreateWizard();
      await this.reload();
      const ms = this.latestMilestone(eng) || (eng.versions || [])[0];
      this.goReq(eng.id);
    } catch (e) {
      if (err) err.textContent = e.message || String(e);
    }
  },
});
