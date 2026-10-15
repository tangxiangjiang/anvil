window.Features = window.Features || {};

/** Delivery UI — SQLite-backed via /api/v1/delivery (0.33 schema). */
window.Features.delivery = {
  async reload() {
    this.data = await api.v1.delivery();
    if (!this.data.eng_reqs) this.data.eng_reqs = [];
    if (!this.data.designs) this.data.designs = [];
    if (!this.data.workers) this.data.workers = [];
    if (!this.data.gates) this.data.gates = [];
    if (!this.data.majors) this.data.majors = [];
    if (!this.data.roadmap) this.data.roadmap = { milestones: [], edges: [] };
    if (this.selectedMajor == null) {
      const dev = (this.data.majors || []).find((m) => m.status === "developing");
      this.selectedMajor = dev ? dev.major : (this.data.majors[0] && this.data.majors[0].major);
    }
    return this.data;
  },

  majorStatusLabel(st) {
    if (st === "sealed") return "Sealed";
    if (st === "developing") return "In development";
    return "Planning";
  },

  developingMajor() {
    const m = (this.data.majors || []).find((x) => x.status === "developing");
    return m ? m.major : null;
  },

  async reset() {
    this.data = await api.v1.deliveryReset();
    this._clustersLoaded = false;
    this._clusters = null;
    return this.data;
  },

  verKey(v) {
    return `${v.major}.${v.minor}.${v.patch}`;
  },

  findEng(id) {
    return (this.data.eng_reqs || []).find((e) => e.id === id);
  },

  findWorker(id) {
    return (this.data.workers || []).find((w) => w.id === id);
  },

  findVersion(eng, major, minor, patch) {
    return (eng.versions || []).find(
      (v) => v.major === major && v.minor === minor && v.patch === patch,
    );
  },

  sectionMeta: {
    requirements: {
      title: "Requirements",
      sub: "Workspace → Worker → milestones (major.minor.patch)",
    },
    designs: {
      title: "Designs",
      sub: "Option cards · ADR · Approve unlocks roadmap bootstrap",
    },
    roadmap: {
      title: "Roadmap",
      sub: "Graph · Gates (execution constraints) · phase chain",
    },
  },

  lifecycleLabel(st) {
    const map = {
      awaiting_design: "Awaiting design",
      awaiting_roadmap: "Awaiting roadmap",
      awaiting_development: "Awaiting development",
      in_development: "In development",
      testing: "Testing",
      complete: "Complete",
    };
    return map[st] || st;
  },

  parseRoute() {
    let hash = location.hash || "#/delivery/requirements";
    // Legacy top-level Gates → under Roadmap
    if (hash === "#/delivery/gates" || hash.startsWith("#/delivery/gates/")) {
      hash = "#/delivery/roadmap/gates";
      location.hash = hash;
    }
    const parts = hash.replace(/^#\/?/, "").split("/").filter(Boolean);
    // delivery/requirements[/:engId[/:M/:m/:p]]
    // delivery/designs[/:id]
    // delivery/roadmap[/gates|/msId]
    const section = parts[1] || "requirements";
    const roadmapTab = section === "roadmap" && parts[2] === "gates" ? "gates" : "graph";
    const msId =
      section === "roadmap" && parts[2] && parts[2] !== "gates" ? parts[2] : null;
    return {
      section,
      engId: parts[2] || null,
      major: parts[3] != null ? Number(parts[3]) : null,
      minor: parts[4] != null ? Number(parts[4]) : null,
      patch: parts[5] != null ? Number(parts[5]) : null,
      desId: section === "designs" ? parts[2] || null : null,
      msId,
      roadmapTab,
    };
  },

  onRoute() {
    if (!this.el || !this.data) return;
    this.render();
  },

  goReq(engId, major, minor, patch) {
    let h = "#/delivery/requirements";
    if (engId) h += "/" + engId;
    if (major != null && minor != null) {
      h += `/${major}/${minor}`;
      if (patch != null) h += `/${patch}`;
    }
    location.hash = h;
  },

  isAwaitingStatus(st) {
    return String(st || "").toLowerCase().startsWith("awaiting_");
  },

  milestoneVersions(eng, major, minor) {
    return (eng.versions || [])
      .filter((v) => v.major === major && v.minor === minor)
      .slice()
      .sort((a, b) => a.patch - b.patch);
  },

  go(section, id) {
    let h = "#/delivery/" + section;
    if (id) h += "/" + id;
    location.hash = h;
  },

  esc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  },

  badge(status) {
    const s = (status || "").toLowerCase();
    let cls = "";
    if (
      s === "approved" ||
      s === "done" ||
      s === "active" ||
      s === "complete" ||
      s === "awaiting_development"
    )
      cls = "ok";
    else if (
      s === "draft" ||
      s === "planned" ||
      s === "pending" ||
      s === "in_progress" ||
      s === "awaiting_design" ||
      s === "awaiting_roadmap" ||
      s === "in_development" ||
      s === "testing"
    )
      cls = "warn";
    else if (s === "rejected" || s === "failed") cls = "bad";
    const label = this.lifecycleLabel(status);
    return `<span class="badge ${cls}">${this.esc(label)}</span>`;
  },

  async mount(el) {
    this.el = el;
    el.innerHTML = `<p class="muted">Loading Delivery…</p>`;
    try {
      await this.reload();
      this.render();
    } catch (e) {
      el.innerHTML = `<p class="err">Failed to load Delivery: ${this.esc(e.message || e)}</p>`;
    }
  },

  unmount() {
    this.el = null;
  },

  render() {
    if (!this.el) return;
    if (!this.data) {
      this.el.innerHTML = `<p class="muted">Loading Delivery…</p>`;
      return;
    }
    const route = this.parseRoute();
    const meta = this.sectionMeta[route.section] || {
      title: "Delivery",
      sub: "",
    };

    let body = "";
    if (route.section === "requirements") {
      if (!route.engId) body = this.viewWorkspace();
      else if (route.major == null) body = this.viewWorkerHome(route.engId);
      else if (route.patch == null)
        body = this.viewMilestoneView(route.engId, route.major, route.minor);
      else
        body = this.viewVersionDetail(
          route.engId,
          route.major,
          route.minor,
          route.patch,
        );
    } else if (route.section === "designs") {
      body = route.desId ? this.viewDesDetail(route.desId) : this.viewDesList();
    } else if (route.section === "roadmap") {
      const tabs = `
        <div class="del-subtabs">
          <a class="del-subtab${route.roadmapTab === "graph" ? " active" : ""}" href="#/delivery/roadmap">Graph</a>
          <a class="del-subtab${route.roadmapTab === "gates" ? " active" : ""}" href="#/delivery/roadmap/gates">Gates</a>
        </div>`;
      body =
        tabs +
        (route.roadmapTab === "gates"
          ? this.viewGates()
          : this.viewRoadmap(route.msId));
    } else {
      body = `<p class="muted">Unknown section. Open a Delivery item in the sidebar.</p>`;
    }

    const isWorkspace =
      route.section === "requirements" && !route.engId;
    const workerHome =
      route.section === "requirements" && route.engId && route.major == null;
    const engHome = workerHome ? this.findEng(route.engId) : null;
    const wHome = engHome ? this.findWorker(engHome.worker_id) : null;
    const designsList = route.section === "designs" && !route.desId;
    let headActions =
      isWorkspace || workerHome
        ? ""
        : `<button type="button" class="ghost" id="del-reset">Reset demo data</button>`;
    if (designsList) {
      headActions =
        `<button type="button" id="des-new">New design</button>` +
        (headActions ? " " + headActions : "");
    }
    let metaTitle = meta.title;
    let metaSub = meta.sub;
    if (route.section === "roadmap" && route.roadmapTab === "gates") {
      metaTitle = "Roadmap · Gates";
      metaSub = "Execution constraints · pending human checks";
    } else if (isWorkspace) {
      metaTitle = "Workspace";
      metaSub = "Pick a Worker · requirements stay bound to that project";
    } else if (workerHome) {
      metaTitle = wHome ? wHome.label || engHome.worker_id : engHome?.title || "Worker";
      metaSub = "Generation plan · milestones for this Worker";
    }

    this.el.innerHTML = `
      <div class="del-head">
        <div>
          <h2>${this.esc(metaTitle)}</h2>
          <p class="muted del-sub">${this.esc(metaSub)}</p>
        </div>
        ${headActions ? `<div class="toolbar" style="margin:0">${headActions}</div>` : ""}
      </div>
      <div class="del-body">${body}</div>
      <div id="del-ui-modal" class="modal" hidden></div>`;

    this.el.querySelector("#del-reset")?.addEventListener("click", async () => {
      try {
        await this.reset();
        this.render();
      } catch (e) {
        alert(e.message || e);
      }
    });
    this.el.querySelector("#des-new")?.addEventListener("click", () => {
      this.openCreateDesignModal();
    });
    this.el.querySelector("#del-req-add")?.addEventListener("click", () => {
      this.openNewMilestoneEntry();
    });
    this.el.querySelector("#del-ms-add-bound")?.addEventListener("click", () => {
      const engId = this.el.querySelector("#del-ms-add-bound")?.getAttribute("data-eng");
      if (engId) this.openCreateWizardBound(engId);
    });
    this.bindMajorPlan();
    this.bindWorkspaceCards();
    this.bindMilestoneCards();
    this.bindVerTips();
    this.bind(route);
    if (isWorkspace) this.loadWorkspaceInventory();
  },

  /** Fixed-position version tips so .main overflow does not clip them. */
  bindVerTips() {
    const el = this.el;
    if (!el) return;
    el.querySelectorAll(".del-ver-tip").forEach((tip) => {
      const panel = tip.querySelector(".del-ver-tip-panel");
      if (!panel) return;
      let hideTimer = null;
      const place = () => {
        if (hideTimer) {
          clearTimeout(hideTimer);
          hideTimer = null;
        }
        tip.classList.add("open");
        panel.style.display = "block";
        const r = tip.getBoundingClientRect();
        const pad = 8;
        const pw = panel.offsetWidth || 240;
        const ph = panel.offsetHeight || 120;
        let left = r.left;
        let top = r.top - ph - 6;
        if (left + pw > window.innerWidth - pad) {
          left = Math.max(pad, window.innerWidth - pad - pw);
        }
        if (left < pad) left = pad;
        if (top < pad) top = r.bottom + 6;
        if (top + ph > window.innerHeight - pad) {
          top = Math.max(pad, window.innerHeight - pad - ph);
        }
        panel.style.left = `${left}px`;
        panel.style.top = `${top}px`;
      };
      const hide = () => {
        tip.classList.remove("open");
        panel.style.display = "";
      };
      const scheduleHide = () => {
        if (hideTimer) clearTimeout(hideTimer);
        hideTimer = setTimeout(hide, 140);
      };
      tip.addEventListener("mouseenter", place);
      tip.addEventListener("mouseleave", scheduleHide);
      panel.addEventListener("mouseenter", place);
      panel.addEventListener("mouseleave", scheduleHide);
      tip.addEventListener("focusin", place);
      tip.addEventListener("focusout", (e) => {
        if (!tip.contains(e.relatedTarget)) scheduleHide();
      });
    });
  },

  bindWorkspaceCards() {
    const el = this.el;
    if (!el) return;
    el.querySelectorAll("[data-ws-open]").forEach((card) => {
      const open = () => {
        const engId = card.getAttribute("data-eng-id");
        if (engId) {
          this.goReq(engId);
          return;
        }
        let fleet = null;
        let node = null;
        let worker = null;
        try {
          fleet = JSON.parse(card.getAttribute("data-fleet") || "null");
          node = JSON.parse(card.getAttribute("data-node") || "null");
          worker = JSON.parse(card.getAttribute("data-worker") || "null");
        } catch (_) {}
        if (worker) this.openCreateWizardForInventoryWorker(fleet, node, worker);
      };
      card.addEventListener("click", open);
      card.addEventListener("keydown", (e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          open();
        }
      });
    });
  },

  bindMilestoneCards() {
    const el = this.el;
    if (!el) return;
    el.querySelectorAll("[data-ms-open]").forEach((card) => {
      const open = () => {
        this.goReq(
          card.getAttribute("data-eng"),
          Number(card.getAttribute("data-major")),
          Number(card.getAttribute("data-minor")),
        );
      };
      card.addEventListener("click", (e) => {
        if (e.target.closest(".del-ver-tip")) return;
        open();
      });
      card.addEventListener("keydown", (e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          open();
        }
      });
    });
  },

  async loadWorkspaceInventory() {
    if (this._wsLoading || this._clustersLoaded) return;
    this._wsLoading = true;
    try {
      const data = await api.v1.clusters();
      this._clusters = data.clusters || [];
      this._clustersError = "";
    } catch (e) {
      this._clustersError = e.message || String(e);
      this._clusters = [];
    }
    this._clustersLoaded = true;
    this._wsLoading = false;
    const route = this.parseRoute();
    if (this.el && route.section === "requirements" && !route.engId) {
      this.render();
    }
  },

  findEngByWorker(workerId) {
    return (this.data.eng_reqs || []).find((e) => e.worker_id === workerId) || null;
  },

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

  bindMajorPlan() {
    const el = this.el;
    el.querySelectorAll("[data-major-select]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.selectedMajor = Number(btn.getAttribute("data-major-select"));
        this.render();
      });
    });
    el.querySelector("#del-major-add")?.addEventListener("click", () => {
      this.openAddMajorModal();
    });
    el.querySelectorAll("[data-major-advance]").forEach((btn) => {
      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        this.openAdvanceMajorModal(
          Number(btn.getAttribute("data-major-advance")),
          btn.getAttribute("data-from"),
          btn.getAttribute("data-to"),
        );
      });
    });
    el.querySelectorAll("[data-major-edit]").forEach((btn) => {
      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        this.openEditMajorModal(Number(btn.getAttribute("data-major-edit")));
      });
    });
    el.querySelectorAll("[data-major-delete]").forEach((btn) => {
      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        this.openDeleteMajorModal(Number(btn.getAttribute("data-major-delete")));
      });
    });
    el.querySelector("#del-trash-open")?.addEventListener("click", () => {
      this.openTrashModal();
    });
  },

  openEditMajorModal(major) {
    const m = (this.data.majors || []).find((x) => x.major === major);
    if (!m) return;
    if (m.status === "sealed") {
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">Cannot edit</h3>
          <p class="muted">Sealed majors are read-only.</p>
          <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
            <button type="button" data-close-ui>OK</button>
          </div>
        </div>`);
      return;
    }
    const label = major === 0 ? "0.x" : major + ".x";
    const chat = this.polishChat("edit-major", {
      id: "ai-chat-edit-major",
      title: "AI assist",
      placeholder: "How should this generation be described?",
      tall: true,
      rows: 3,
      allowDrop: false,
      citeNav: [
        { id: "title", label: "Title" },
        { id: "summary", label: "Summary" },
      ],
      emptyHint: "Cite Title / Summary, then describe how to polish this generation.",
      onCiteNav: (navId, ch) => {
        if (navId === "title") ch.addCite("Title");
        else if (navId === "summary") ch.addCite("Summary");
      },
      onScrollCite: (key) => {
        const modal = this.uiModal();
        const map = { Title: "#del-edit-maj-title", Summary: "#del-edit-maj-sum" };
        const sel = map[key];
        const field = sel && modal?.querySelector(sel);
        if (field) {
          field.scrollIntoView({ behavior: "smooth", block: "center" });
          field.focus?.();
          window.AiChat.flash(field);
        }
      },
      onSend: async ({ composed }, ch) => {
        const modal = this.uiModal();
        const reply = this.applyMajorChatAssist(
          composed,
          modal?.querySelector("#del-edit-maj-title"),
          modal?.querySelector("#del-edit-maj-sum"),
          { forceTitle: true },
        );
        ch.appendMessage("ai", reply);
      },
    });
    this.showUiModal(`
      <div class="modal-card modal-chat del-maj-assist-modal">
        <div class="toolbar del-maj-assist-head">
          <h3 style="margin:0">Edit major ${this.esc(label)}</h3>
        </div>
        <div class="del-maj-assist-split">
          <div class="del-maj-assist-form">
            <label>Title</label>
            <input id="del-edit-maj-title" value="${this.esc(m.title || "")}" data-cite-key="Title"
              placeholder="Short name for this generation" />
            <label>Summary</label>
            <textarea id="del-edit-maj-sum" rows="8" data-cite-key="Summary"
              placeholder="What this generation covers, goals, and boundaries">${this.esc(m.summary || "")}</textarea>
            <div class="err" id="del-edit-maj-err"></div>
            <div class="toolbar del-maj-assist-actions">
              <button type="button" class="ghost" data-close-ui>Cancel</button>
              <button type="button" id="del-edit-maj-ok">Save</button>
            </div>
          </div>
          ${chat.render()}
        </div>
      </div>`);
    const modal = this.uiModal();
    chat.mount(modal);
    modal.querySelector("#del-edit-maj-ok").onclick = async () => {
      const err = modal.querySelector("#del-edit-maj-err");
      err.textContent = "";
      try {
        await api.v1.deliveryPatchMajor(major, {
          title: modal.querySelector("#del-edit-maj-title").value.trim(),
          summary: modal.querySelector("#del-edit-maj-sum").value,
        });
        this.closeUiModal();
        await this.reload();
        this.render();
      } catch (e) {
        err.textContent = e.message || String(e);
      }
    };
  },

  nextMajorStatus(st) {
    if (st === "planning") return "developing";
    if (st === "developing") return "sealed";
    return "";
  },

  advanceActionLabel(to) {
    if (to === "developing") return "Start development";
    if (to === "sealed") return "Seal";
    return "Advance";
  },

  advanceCardLabel(to) {
    if (to === "developing") return "Start →";
    if (to === "sealed") return "Seal →";
    return "Advance →";
  },

  openAdvanceMajorModal(major, from, to) {
    if (to === "developing" && !this.prevMajorSealed(major)) {
      const prevLabel = major - 1 === 0 ? "0.x" : major - 1 + ".x";
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">Cannot start yet</h3>
          <p>Seal previous major <code>${this.esc(prevLabel)}</code> before starting this generation.</p>
          <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
            <button type="button" data-close-ui>OK</button>
          </div>
        </div>`);
      return;
    }
    const label = major === 0 ? "0.x" : major + ".x";
    const fromL = this.majorStatusLabel(from);
    const toL = this.majorStatusLabel(to);
    let detail = "";
    if (to === "developing") {
      detail =
        "This generation becomes the active development line. New milestones will attach here.";
    } else if (to === "sealed") {
      detail =
        "Sealed generations are locked — no new milestones can be added under this major.";
    }
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Advance major ${this.esc(label)}?</h3>
        <p>
          <span class="badge warn">${this.esc(fromL)}</span>
          <span class="muted"> → </span>
          <span class="badge ${to === "sealed" ? "bad" : "ok"}">${this.esc(toL)}</span>
        </p>
        <p class="muted">${this.esc(detail)}</p>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="del-adv-maj-ok">${this.esc(this.advanceActionLabel(to))}</button>
        </div>
      </div>`);
    this.uiModal().querySelector("#del-adv-maj-ok").onclick = async () => {
      try {
        await api.v1.deliveryAdvanceMajor(major);
        this.closeUiModal();
        await this.reload();
        this.render();
      } catch (e) {
        alert(e.message || e);
      }
    };
  },

  uiModal() {
    return this.el?.querySelector("#del-ui-modal");
  },

  closeUiModal() {
    const m = this.uiModal();
    if (m) {
      m.hidden = true;
      m.innerHTML = "";
    }
    for (const key of ["add-major", "edit-major"]) {
      if (this._citeBySurface) delete this._citeBySurface[key];
      if (this._msgsBySurface) delete this._msgsBySurface[key];
      if (this._chats) delete this._chats[key];
    }
  },

  showUiModal(html) {
    const m = this.uiModal();
    if (!m) return;
    m.hidden = false;
    m.innerHTML = html;
    m.querySelectorAll("[data-close-ui]").forEach((btn) => {
      btn.addEventListener("click", () => this.closeUiModal());
    });
  },

  applyMajorChatAssist(msg, titleEl, sumEl, opts = {}) {
    const text = (msg || "").trim();
    if (!text) return "Describe what this generation should cover.";
    const short =
      text.length > 56 ? text.slice(0, 53).replace(/\s+\S*$/, "") + "…" : text;
    const cleaned = short.replace(/^["']|["']$/g, "");
    if (titleEl && (opts.forceTitle || !(titleEl.value || "").trim())) {
      titleEl.value = cleaned;
    }
    if (sumEl) {
      const line = "· " + text;
      sumEl.value = (sumEl.value || "").trim()
        ? sumEl.value.trim() + "\n" + line
        : "This generation covers:\n" + line;
    }
    return `Applied to Title${titleEl?.value ? ` “${titleEl.value}”` : ""} and Summary. Review, then save.`;
  },

  openAddMajorModal() {
    const next =
      (this.data.majors || []).reduce((a, m) => Math.max(a, m.major), -1) + 1;
    const label = next === 0 ? "0.x" : next + ".x";
    const chat = this.polishChat("add-major", {
      id: "ai-chat-add-major",
      title: "AI assist",
      placeholder: "What should this generation cover?",
      tall: true,
      rows: 3,
      allowDrop: false,
      citeNav: [
        { id: "title", label: "Title" },
        { id: "summary", label: "Summary" },
      ],
      emptyHint: "Cite Title / Summary, then describe this generation.",
      onCiteNav: (navId, ch) => {
        if (navId === "title") ch.addCite("Title");
        else if (navId === "summary") ch.addCite("Summary");
      },
      onScrollCite: (key) => {
        const modal = this.uiModal();
        const map = { Title: "#del-add-maj-title", Summary: "#del-add-maj-sum" };
        const sel = map[key];
        const field = sel && modal?.querySelector(sel);
        if (field) {
          field.scrollIntoView({ behavior: "smooth", block: "center" });
          field.focus?.();
          window.AiChat.flash(field);
        }
      },
      onSend: async ({ composed }, ch) => {
        const modal = this.uiModal();
        const reply = this.applyMajorChatAssist(
          composed,
          modal?.querySelector("#del-add-maj-title"),
          modal?.querySelector("#del-add-maj-sum"),
        );
        ch.appendMessage("ai", reply);
      },
    });
    this.showUiModal(`
      <div class="modal-card modal-chat del-maj-assist-modal">
        <div class="toolbar del-maj-assist-head">
          <h3 style="margin:0">Add major</h3>
          <span class="muted">Next <code>${this.esc(label)}</code> · starts as Planning</span>
        </div>
        <div class="del-maj-assist-split">
          <div class="del-maj-assist-form">
            <label>Title</label>
            <input id="del-add-maj-title" value="" data-cite-key="Title"
              placeholder="Short name for this generation (e.g. first stable release)" />
            <label>Summary</label>
            <textarea id="del-add-maj-sum" rows="8" data-cite-key="Summary"
              placeholder="What this generation covers, goals, and boundaries"></textarea>
            <div class="err" id="del-add-maj-err"></div>
            <div class="toolbar del-maj-assist-actions">
              <button type="button" class="ghost" data-close-ui>Cancel</button>
              <button type="button" id="del-add-maj-ok">Add</button>
            </div>
          </div>
          ${chat.render()}
        </div>
      </div>`);
    const modal = this.uiModal();
    chat.mount(modal);
    modal.querySelector("#del-add-maj-ok").onclick = async () => {
      const err = modal.querySelector("#del-add-maj-err");
      err.textContent = "";
      try {
        await api.v1.deliveryAddMajor({
          major: next,
          title: modal.querySelector("#del-add-maj-title").value.trim(),
          summary: modal.querySelector("#del-add-maj-sum").value,
        });
        this.closeUiModal();
        await this.reload();
        this.render();
      } catch (e) {
        err.textContent = e.message || String(e);
      }
    };
  },

  async openDeleteMajorModal(major) {
    let preview = { milestone_count: 0, version_count: 0 };
    try {
      preview = await api.v1.deliveryPreviewDeleteMajor(major);
    } catch (e) {
      alert(e.message || e);
      return;
    }
    const label = major === 0 ? "0.x" : major + ".x";
    const hasMs = (preview.milestone_count || 0) > 0;
    const warn = hasMs
      ? `<p class="err" style="min-height:0">
           This major has <strong>${preview.milestone_count}</strong> milestone(s)
           (${preview.version_count} version row(s)).
           Deleting it moves those milestone plans to the <strong>Trash</strong>.
           You can restore them later from Trash.
         </p>`
      : `<p class="muted">No milestones under this major. It will be moved to Trash.</p>`;
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Delete major ${this.esc(label)}?</h3>
        ${warn}
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="del-del-maj-ok">Delete</button>
        </div>
      </div>`);
    this.uiModal().querySelector("#del-del-maj-ok").onclick = async () => {
      try {
        await api.v1.deliveryDeleteMajor(major);
        this.closeUiModal();
        if (this.selectedMajor === major) this.selectedMajor = null;
        await this.reload();
        this.render();
      } catch (e) {
        alert(e.message || e);
      }
    };
  },

  async openTrashModal() {
    let items = [];
    try {
      const data = await api.v1.deliveryTrash();
      items = data.items || [];
    } catch (e) {
      alert(e.message || e);
      return;
    }
    const rows = items
      .map((it) => {
        const p = it.preview || {};
        return `
        <div class="del-trash-row">
          <div style="flex:1">
            <div>${this.esc(it.label)}</div>
            <div class="muted">${this.esc(it.deleted_at || "")}
              ${p.milestone_count != null ? ` · ${p.milestone_count} milestone(s)` : ""}
            </div>
          </div>
          <button type="button" class="ghost" data-trash-restore="${this.esc(it.id)}">Restore</button>
          <button type="button" class="danger" data-trash-purge="${this.esc(it.id)}">Discard</button>
        </div>`;
      })
      .join("");
    this.showUiModal(`
      <div class="modal-card modal-wide">
        <div class="toolbar" style="margin:0 0 0.75rem;justify-content:space-between">
          <h3 style="margin:0">Trash</h3>
          <button type="button" class="ghost" data-close-ui>Close</button>
        </div>
        <p class="muted">Deleted majors and their milestone plans.</p>
        <div class="del-trash-list">${rows || `<p class="muted">Trash is empty.</p>`}</div>
      </div>`);
    this.uiModal().querySelectorAll("[data-trash-restore]").forEach((btn) => {
      btn.onclick = () => this.restoreTrashItem(btn.getAttribute("data-trash-restore"));
    });
    this.uiModal().querySelectorAll("[data-trash-purge]").forEach((btn) => {
      btn.onclick = async () => {
        try {
          await api.v1.deliveryPurgeTrash(btn.getAttribute("data-trash-purge"));
          await this.openTrashModal();
          await this.reload();
        } catch (e) {
          alert(e.message || e);
        }
      };
    });
  },

  async restoreTrashItem(id, mode) {
    try {
      const res = await api.v1.deliveryRestoreTrash(id, mode || "");
      this.closeUiModal();
      await this.reload();
      this.render();
      return res;
    } catch (e) {
      if (e.status === 409 && e.data && e.data.conflict) {
        const maj = e.data.major?.major;
        const label = maj === 0 ? "0.x" : maj + ".x";
        this.showUiModal(`
          <div class="modal-card">
            <h3 style="margin-top:0">Major conflict</h3>
            <p>${this.esc(e.data.message || "Major already exists.")}</p>
            <p class="muted">Major <code>${this.esc(label)}</code> is already in the plan.</p>
            <div class="toolbar" style="margin-top:1rem;flex-direction:column;align-items:stretch">
              <button type="button" id="del-restore-overwrite">Overwrite existing milestones</button>
              <button type="button" class="ghost" id="del-restore-append">Append to existing milestones</button>
              <button type="button" class="ghost" data-close-ui>Cancel</button>
            </div>
          </div>`);
        this.uiModal().querySelector("#del-restore-overwrite").onclick = () =>
          this.restoreTrashItem(id, "overwrite");
        this.uiModal().querySelector("#del-restore-append").onclick = () =>
          this.restoreTrashItem(id, "append");
        return;
      }
      alert(e.message || e);
    }
  },

  bind(route) {
    const el = this.el;

    if (
      route.section === "requirements" &&
      route.engId &&
      route.major != null &&
      route.patch == null
    ) {
      this.bindMilestoneView(route);
    }

    if (
      route.section === "requirements" &&
      route.engId &&
      route.major != null &&
      route.patch != null
    ) {
      const eng = this.findEng(route.engId);
      const ver = eng && this.findVersion(eng, route.major, route.minor, route.patch);
      if (!ver) return;
      const verEditKey = `${route.engId}:${route.major}.${route.minor}.${route.patch}`;
      const editing = this._msEditing === verEditKey;
      el.querySelector("#ver-edit")?.addEventListener("click", () => {
        this._msEditing = verEditKey;
        this.render();
      });
      el.querySelector("#ver-save")?.addEventListener("click", async () => {
        try {
          await api.v1.deliveryPatchVersion(route.engId, route.major, route.minor, route.patch, {
            title: el.querySelector("#ver-title").value.trim(),
            problem: el.querySelector("#ver-problem").value,
            goals: el.querySelector("#ver-goals").value,
            success: el.querySelector("#ver-success").value,
          });
          this._msEditing = null;
          await this.reload();
          this.render();
        } catch (e) {
          alert(e.message || e);
        }
      });
      const verChat = this._chats?.[`ver:${verEditKey}`];
      if (verChat) {
        verChat.mount(el);
        if (!editing) verChat.bindDropSources(el);
      }
    }

    el.querySelectorAll("[data-ms-view]").forEach((btn) => {
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        this._msEditing = null;
        this.goReq(
          btn.getAttribute("data-eng"),
          Number(btn.getAttribute("data-major")),
          Number(btn.getAttribute("data-minor")),
        );
      });
    });

    el.querySelector("#ver-add")?.addEventListener("click", async () => {
      const eng = this.findEng(route.engId);
      if (!eng) return;
      const major = Number(el.querySelector("#new-major").value);
      const minor = Number(el.querySelector("#new-minor").value);
      const patch = Number(el.querySelector("#new-patch").value);
      const title = el.querySelector("#new-ver-title").value.trim() || "Untitled";
      if ([major, minor, patch].some((n) => Number.isNaN(n))) {
        alert("major / minor / patch must be numbers");
        return;
      }
      try {
        await api.v1.deliveryAddVersion(eng.id, { major, minor, patch, title });
        await this.reload();
        this.goReq(eng.id, major, minor, patch);
      } catch (e) {
        alert(e.message || e);
      }
    });

    el.querySelectorAll("[data-ms-delete]").forEach((btn) => {
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        this.openDeleteMilestoneModal(
          btn.getAttribute("data-eng"),
          Number(btn.getAttribute("data-major")),
          Number(btn.getAttribute("data-minor")),
        );
      });
    });

    if (route.section === "designs" && !route.desId) {
      el.querySelector("#des-filter")?.addEventListener("change", (ev) => {
        this._desFilter = ev.target.value || "all";
        this.render();
      });
      el.querySelectorAll("[data-des-open]").forEach((card) => {
        card.addEventListener("click", () => {
          location.hash = "#/delivery/designs/" + card.getAttribute("data-des-open");
        });
      });
    }

    if (route.section === "designs" && route.desId) {
      const des = (this.data.designs || []).find((d) => d.id === route.desId);
      if (!des) return;
      el.querySelectorAll("[data-choose-opt]").forEach((btn) => {
        btn.addEventListener("click", async () => {
          try {
            await api.v1.deliveryPatchDesign(des.id, {
              chosen_option_id: btn.getAttribute("data-choose-opt"),
            });
            await this.reload();
            this.render();
          } catch (e) {
            alert(e.message || e);
          }
        });
      });
      el.querySelector("#des-save")?.addEventListener("click", async () => {
        const err = el.querySelector("#des-err");
        if (err) err.textContent = "";
        try {
          const options = [...el.querySelectorAll("[data-opt-id]")].map((card) => ({
            id: card.getAttribute("data-opt-id"),
            title: card.querySelector("[data-opt-title]")?.value ?? "",
            summary: card.querySelector("[data-opt-summary]")?.value ?? "",
            tradeoffs: card.querySelector("[data-opt-tradeoffs]")?.value ?? "",
            risks: card.querySelector("[data-opt-risks]")?.value ?? "",
            out_of_scope: card.querySelector("[data-opt-oos]")?.value ?? "",
          }));
          const adr = el.querySelector("#des-adr")?.value ?? "";
          await api.v1.deliveryPatchDesign(des.id, { adr, options });
          await this.reload();
          this.render();
        } catch (e) {
          if (err) err.textContent = e.message || String(e);
          else alert(e.message || e);
        }
      });
      el.querySelector("#des-approve")?.addEventListener("click", async () => {
        try {
          await api.v1.deliveryApproveDesign(des.id);
          await this.reload();
          this.render();
        } catch (e) {
          alert(e.message || e);
        }
      });
      el.querySelector("#des-bootstrap")?.addEventListener("click", () => {
        this.go("roadmap");
      });
      const desChat = this._chats?.[`des:${des.id}`];
      if (desChat) desChat.mount(el);
    }

    if (route.section === "roadmap") {
      el.querySelectorAll("[data-ms]").forEach((node) => {
        node.addEventListener("click", () => {
          this.go("roadmap", node.getAttribute("data-ms"));
        });
      });
      el.querySelectorAll("[data-ms-ready]").forEach((btn) => {
        btn.addEventListener("click", async () => {
          const engId = btn.getAttribute("data-eng");
          const major = Number(btn.getAttribute("data-major"));
          const minor = Number(btn.getAttribute("data-minor"));
          try {
            await api.v1.deliverySetMilestoneStatus(
              engId,
              major,
              minor,
              "awaiting_development",
            );
            await this.reload();
            this.render();
          } catch (e) {
            alert(e.message || e);
          }
        });
      });
      el.querySelectorAll("[data-gate-open]").forEach((a) => {
        a.addEventListener("click", (e) => {
          e.preventDefault();
          const href = a.getAttribute("data-href");
          if (href) location.hash = href;
        });
      });
    }
  },

  latestMilestone(eng) {
    let best = null;
    for (const v of eng.versions || []) {
      if (
        !best ||
        v.major > best.major ||
        (v.major === best.major && v.minor > best.minor)
      ) {
        best = v;
      }
    }
    return best;
  },

  prevMajorSealed(major) {
    if (major <= 0) return true;
    const prev = (this.data.majors || []).find((m) => m.major === major - 1);
    return !!(prev && prev.status === "sealed");
  },

  viewMajorPlan() {
    const majors = this.data.majors || [];
    const lastMajor = majors.length
      ? majors.reduce((a, m) => Math.max(a, m.major), majors[0].major)
      : null;
    const cards = majors
      .map((m) => {
        const sel =
          this.selectedMajor === m.major ? " del-major-card-selected" : "";
        const stCls =
          m.status === "developing"
            ? "ok"
            : m.status === "sealed"
              ? "bad"
              : "warn";
        const canDelete =
          m.status === "planning" && lastMajor != null && m.major === lastMajor;
        const canEdit = m.status === "planning" || m.status === "developing";
        const next = this.nextMajorStatus(m.status);
        const canStart =
          next !== "developing" || this.prevMajorSealed(m.major);
        const actions = [];
        if (canEdit) {
          actions.push(
            `<button type="button" class="ghost del-major-edit" data-major-edit="${m.major}">Edit</button>`,
          );
        }
        if (next === "developing" && !canStart) {
          const prevLabel = m.major - 1 === 0 ? "0.x" : m.major - 1 + ".x";
          actions.push(
            `<span class="muted del-major-blocked">Seal ${this.esc(prevLabel)} first</span>`,
          );
        } else if (next) {
          actions.push(`<button type="button" class="del-major-adv"
            data-major-advance="${m.major}" data-from="${this.esc(m.status)}" data-to="${this.esc(next)}">
            ${this.esc(this.advanceCardLabel(next))}
          </button>`);
        }
        if (canDelete) {
          actions.push(`<button type="button" class="ghost danger del-major-del" data-major-delete="${m.major}">Delete</button>`);
        }
        return `
      <div class="del-major-card${sel}">
        <button type="button" class="del-major-hit" data-major-select="${m.major}">
          <div class="del-major-num">${m.major === 0 ? "0.x" : m.major + ".x"}</div>
          <div class="del-major-title">${this.esc(m.title || "Untitled")}</div>
        </button>
        <div class="del-major-status-row">
          <span class="badge ${stCls}">${this.esc(this.majorStatusLabel(m.status))}</span>
        </div>
        <p class="muted del-major-sum">${this.esc(m.summary || "")}</p>
        <div class="del-major-actions">${actions.join("") || `<span class="muted del-major-final">Sealed</span>`}</div>
      </div>`;
      })
      .join("");
    const trashN = this.data.trash_count || 0;
    return `
      <div class="del-major-plan">
        <div class="del-major-plan-head">
          <h3 style="margin:0">Generation plan</h3>
          <span class="muted">major line · e.g. 0.x → 1.0</span>
          <button type="button" class="ghost" id="del-trash-open" style="margin-left:auto">
            Trash${trashN ? ` (${trashN})` : ""}
          </button>
        </div>
        <div class="del-major-grid">
          ${cards}
          <button type="button" class="del-major-card del-major-card-add" id="del-major-add" title="Add major">
            <span class="del-req-plus">+</span>
            <span class="muted">Add major</span>
          </button>
        </div>
      </div>`;
  },

  versionTipHtml(versions) {
    if (!versions.length) {
      return `<div class="del-ver-tip-empty muted">No versions</div>`;
    }
    return versions
      .slice()
      .sort((a, b) =>
        a.major !== b.major
          ? a.major - b.major
          : a.minor !== b.minor
            ? a.minor - b.minor
            : a.patch - b.patch,
      )
      .map(
        (v) => `
      <div class="del-ver-tip-row">
        <code>${this.esc(this.verKey(v))}</code>
        <span>${this.esc(v.title || "Untitled")}</span>
      </div>`,
      )
      .join("");
  },

  /** Requirements entry: workers grouped by scheduler (fleet). */
  viewWorkspace() {
    if (!this._clustersLoaded) {
      return `<p class="muted">Loading workers from schedulers…</p>
        <div id="del-create-modal" class="modal" hidden></div>`;
    }
    if (this._clustersError) {
      return `<p class="err">${this.esc(this._clustersError)}</p>
        <p class="muted">Open Clusters to check scheduler connectivity, then refresh.</p>
        <div id="del-create-modal" class="modal" hidden></div>`;
    }
    const fleets = this._clusters || [];
    if (!fleets.length) {
      return `
        <div class="card">
          <p class="muted">No schedulers configured. Add a fleet under Clusters, then return here.</p>
        </div>
        <div id="del-create-modal" class="modal" hidden></div>`;
    }
    const sections = fleets
      .map((f) => {
        const workers = [];
        for (const n of f.nodes || []) {
          for (const w of n.workers || []) {
            workers.push({ fleet: f, node: n, worker: w });
          }
        }
        const cards = workers
          .map(({ fleet, node, worker }) => {
            const eng = this.findEngByWorker(worker.id);
            const vers = eng ? eng.versions || [] : [];
            const ms = eng ? this.latestMilestone(eng) : null;
            const msKey = ms ? `${ms.major}.${ms.minor}` : "—";
            const bound = eng
              ? this.badge(eng.status)
              : `<span class="badge warn">unbound</span>`;
            const fleetJson = this.esc(JSON.stringify({ id: fleet.id, name: fleet.name }));
            const nodeJson = this.esc(JSON.stringify({ id: node.id, name: node.name }));
            const workerJson = this.esc(
              JSON.stringify({
                id: worker.id,
                label: worker.label || worker.id,
                project_rel: worker.project_rel || "",
              }),
            );
            return `
      <div class="del-req-card" role="link" tabindex="0" data-ws-open
        data-eng-id="${eng ? this.esc(eng.id) : ""}"
        data-fleet="${fleetJson}" data-node="${nodeJson}" data-worker="${workerJson}">
        <div class="del-req-card-top">
          <strong class="del-req-card-title">${this.esc(worker.label || worker.id)}</strong>
          ${bound}
        </div>
        <div class="muted del-req-card-meta">
          <span>node ${this.esc(node.name || node.id)}</span>
          <span>·</span>
          <code>${this.esc(worker.project_rel || "—")}</code>
        </div>
        <div class="del-req-card-foot">
          <code class="del-ms-ver">${this.esc(msKey)}</code>
          <span class="del-ver-tip">
            <span class="del-ver-tip-label muted">${vers.length} version${vers.length === 1 ? "" : "s"}</span>
            <div class="del-ver-tip-panel" role="tooltip">${this.versionTipHtml(vers)}</div>
          </span>
        </div>
      </div>`;
          })
          .join("");
        const online = f.online
          ? `<span class="badge ok">online</span>`
          : `<span class="badge bad">offline</span>`;
        return `
      <section class="del-ws-fleet">
        <div class="del-ws-fleet-head">
          <h3 style="margin:0">${this.esc(f.name || f.id)}</h3>
          ${online}
          <span class="muted">${this.esc(f.scheduler_url || f.listen_app || "")}</span>
        </div>
        <div class="del-req-grid">
          ${
            cards ||
            `<p class="muted" style="grid-column:1/-1">No workers on this scheduler. Create workers under Clusters.</p>`
          }
        </div>
      </section>`;
      })
      .join("");
    return `${sections}<div id="del-create-modal" class="modal" hidden></div>`;
  },

  /** Worker home "+ Milestone" — always bound to current eng. */
  openNewMilestoneEntry() {
    const route = this.parseRoute();
    if (route.engId) {
      this.openCreateWizardBound(route.engId);
      return;
    }
    this.openCreateWizard();
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
      alert("Engineering requirement not found.");
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
      onSend: async ({ composed }, ch) => {
        const modal = this.el?.querySelector("#del-create-modal");
        this.captureMilestoneConfig(modal, c);
        const reply = this.applyMilestoneChatAssist(c, composed);
        ch.appendMessage("ai", reply);
        this.renderCreateWizard();
      },
    });
    return `
      <div class="del-ms-config-split">
        <div class="del-ms-config-main">${form}</div>
        ${chat.render()}
      </div>`;
  },

  /** Mock AI: turn a free-text brief into milestone field patches. */
  applyMilestoneChatAssist(c, msg) {
    const text = (msg || "").trim();
    if (!text) return "Say what this milestone should deliver.";
    const short =
      text.length > 72 ? text.slice(0, 69).replace(/\s+\S*$/, "") + "…" : text;
    if (!(c.title || "").trim()) {
      c.title = short.replace(/^["']|["']$/g, "");
    }
    const goalLine = "· " + text;
    c.goals = (c.goals || "").trim()
      ? c.goals.trim() + "\n" + goalLine
      : "Ship:\n" + goalLine;
    if (!c.versions) c.versions = [];
    const parts = text
      .split(/[,;，；]|\band\b|\bwith\b|\bplus\b/i)
      .map((s) => s.trim())
      .filter((s) => s.length >= 3)
      .slice(0, 4);
    const seeds = parts.length ? parts : [text.slice(0, 80)];
    for (const s of seeds) {
      const title = s.length > 48 ? s.slice(0, 45) + "…" : s;
      if (c.versions.some((v) => (v.title || "").toLowerCase() === title.toLowerCase())) {
        continue;
      }
      c.versions.push({ title, delivery: s });
    }
    const crit = "· " + text.slice(0, 160) + (text.length > 160 ? "…" : "");
    c.completion = (c.completion || "").trim()
      ? c.completion.trim() + "\n" + crit
      : "Capabilities that must work:\n" + crit;
    const n = seeds.length;
    return `Applied to this milestone: title${c.title ? ` “${c.title}”` : ""}, goals, ${n} version row(s), completion criteria. Edit the form, then Create.`;
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
      alert(e.message || e);
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
      alert("Worker has no project folder.");
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

  async createFsMkdir() {
    const c = this._create;
    const err = this.el.querySelector("#del-create-err");
    if (!c) return;
    if (err) err.textContent = "";
    const name = prompt("New folder name");
    if (!name) return;
    const base = (c.fsPath || c.projectRoot || "").replace(/\/+$/, "");
    const path = base + "/" + name.trim();
    try {
      await api.v1.mkdirSandbox(c.fleet.id, c.node.id, path);
      c.selected = path;
      const sel = this.el.querySelector("#del-fs-sel");
      if (sel) sel.textContent = this.reqsRelFromPath(path);
      await this.createFsLoad();
    } catch (e) {
      if (err) err.textContent = e.message || String(e);
    }
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

  bindMilestoneView(route) {
    const el = this.el;
    const eng = this.findEng(route.engId);
    const versions = eng
      ? this.milestoneVersions(eng, route.major, route.minor)
      : [];
    const head = versions[0];
    if (!eng || !head) return;
    const editKey = `${route.engId}:${route.major}.${route.minor}`;
    const editing = this._msEditing === editKey;
    if (editing) this.ensureMsVerDraft(editKey, versions);

    el.querySelector("#ms-edit")?.addEventListener("click", () => {
      this._msEditing = editKey;
      this._verCardEdit = null;
      this.ensureMsVerDraft(editKey, versions);
      this.render();
    });
    el.querySelector("#ms-delete")?.addEventListener("click", () => {
      this.openDeleteMilestoneModal(route.engId, route.major, route.minor);
    });
    el.querySelector("#ms-cancel-edit")?.addEventListener("click", () => {
      this._msEditing = null;
      this._msVerDraft = null;
      this._msVerDraftKey = null;
      this._verCardEdit = null;
      this.render();
    });
    el.querySelector("#ms-save")?.addEventListener("click", async () => {
      try {
        this.captureMsVerCardEdits(el);
        const msTitle = el.querySelector("#ms-title").value.trim();
        const msProblem = el.querySelector("#ms-problem").value;
        const draft = this._msVerDraft || [];
        const headDraft = draft.find((d) => d.patch === head.patch);
        if (headDraft) {
          headDraft.title = msTitle;
          headDraft.problem = msProblem;
        }
        await api.v1.deliveryPatchVersion(
          route.engId,
          route.major,
          route.minor,
          head.patch,
          {
            title: msTitle || "Untitled",
            problem: msProblem,
            goals: el.querySelector("#ms-goals").value,
            success: el.querySelector("#ms-success").value,
          },
        );
        for (const d of draft) {
          if (d.patch === head.patch) continue;
          await api.v1.deliveryPatchVersion(
            route.engId,
            route.major,
            route.minor,
            d.patch,
            { title: (d.title || "").trim() || "Untitled", problem: d.problem || "" },
          );
        }
        this._msEditing = null;
        this._msVerDraft = null;
        this._msVerDraftKey = null;
        this._verCardEdit = null;
        await this.reload();
        this.render();
      } catch (e) {
        alert(e.message || e);
      }
    });
    el.querySelector("#ms-ver-add")?.addEventListener("click", async () => {
      const nextPatch =
        versions.reduce((m, v) => Math.max(m, v.patch), 0) + 1;
      try {
        this.captureMsVerCardEdits(el);
        await api.v1.deliveryAddVersion(eng.id, {
          major: route.major,
          minor: route.minor,
          patch: nextPatch,
          title: "Untitled",
        });
        await this.reload();
        const fresh = this.findEng(route.engId);
        const vers = fresh
          ? this.milestoneVersions(fresh, route.major, route.minor)
          : [];
        // Keep prior draft content; append new slot
        if (this._msVerDraftKey === editKey && this._msVerDraft) {
          const byPatch = new Map(this._msVerDraft.map((d) => [d.patch, d]));
          this._msVerDraft = vers.map((v) => {
            const prev = byPatch.get(v.patch);
            return prev
              ? { patch: v.patch, title: prev.title, problem: prev.problem }
              : { patch: v.patch, title: v.title, problem: v.problem || "" };
          });
        } else {
          this.ensureMsVerDraft(editKey, vers);
        }
        this._verCardEdit = nextPatch;
        this.render();
      } catch (e) {
        alert(e.message || e);
      }
    });
    this.bindMsVerCards(el, route, editing);
    const msChat = this._chats?.[`ms:${editKey}`];
    if (msChat) {
      msChat.mount(el);
      if (!editing) msChat.bindDropSources(el);
    }
    if (this._pendingScrollVer) {
      const k = this._pendingScrollVer;
      this._pendingScrollVer = null;
      requestAnimationFrame(() => this.scrollToCite(k));
    }
  },

  ensureMsVerDraft(editKey, versions) {
    if (this._msVerDraftKey === editKey && Array.isArray(this._msVerDraft)) return;
    this._msVerDraftKey = editKey;
    this._msVerDraft = versions.map((v) => ({
      patch: v.patch,
      title: v.title || "",
      problem: v.problem || "",
    }));
  },

  captureMsVerCardEdits(root) {
    if (!this._msVerDraft) return;
    root.querySelectorAll(".del-ms-ver-card").forEach((card) => {
      const patch = Number(card.getAttribute("data-patch"));
      const d = this._msVerDraft.find((x) => x.patch === patch);
      if (!d) return;
      const titleEl = card.querySelector(".del-ms-ver-edit-title");
      const noteEl = card.querySelector(".del-ms-ver-edit-note");
      if (titleEl) d.title = titleEl.value;
      if (noteEl) d.problem = noteEl.value;
    });
  },

  cancelMsVerCardEdit() {
    if (this._verEditCancelTimer) {
      clearTimeout(this._verEditCancelTimer);
      this._verEditCancelTimer = null;
    }
    const snap = this._verCardEditSnap;
    if (snap && this._msVerDraft) {
      const d = this._msVerDraft.find((x) => x.patch === snap.patch);
      if (d) {
        d.title = snap.title;
        d.problem = snap.problem;
      }
    }
    this._verCardEdit = null;
    this._verCardEditSnap = null;
  },

  beginMsVerCardEdit(patch) {
    if (this._verEditCancelTimer) {
      clearTimeout(this._verEditCancelTimer);
      this._verEditCancelTimer = null;
    }
    if (this._verCardEdit && this._verCardEdit !== patch) {
      this.cancelMsVerCardEdit();
    }
    const d = (this._msVerDraft || []).find((x) => x.patch === patch);
    this._verCardEditSnap = d
      ? { patch, title: d.title, problem: d.problem }
      : { patch, title: "", problem: "" };
    this._verCardEdit = patch;
  },

  bindMsVerCards(el, route, editing) {
    if (!editing) return;
    const list = el.querySelector("#ms-ver-cards");
    if (!list) return;

    list.querySelectorAll(".del-ms-ver-card").forEach((card) => {
      card.addEventListener("click", (e) => {
        if (!this._verCardEdit) return;
        if (e.target.closest("button, input, textarea")) return;
        const patch = Number(card.getAttribute("data-patch"));
        if (!patch || patch === this._verCardEdit) return;
        // Defer so a double-click can open edit on the other card instead.
        if (this._verEditCancelTimer) clearTimeout(this._verEditCancelTimer);
        this._verEditCancelTimer = setTimeout(() => {
          this._verEditCancelTimer = null;
          if (!this._verCardEdit || this._verCardEdit === patch) return;
          this.cancelMsVerCardEdit();
          this.render();
        }, 220);
      });
      card.addEventListener("dblclick", (e) => {
        if (e.target.closest("input, textarea, button")) return;
        const patch = Number(card.getAttribute("data-patch"));
        if (!patch) return;
        if (this._verCardEdit === patch) return;
        this.beginMsVerCardEdit(patch);
        this.render();
      });
      card.addEventListener("dragstart", (e) => {
        if (e.target.closest("button, input, textarea, .del-ms-ver-edit-actions")) {
          e.preventDefault();
          return;
        }
        if (this._verEditCancelTimer) {
          clearTimeout(this._verEditCancelTimer);
          this._verEditCancelTimer = null;
        }
        this.captureMsVerCardEdits(el);
        this._verCardEdit = null;
        this._verCardEditSnap = null;
        const patch = card.getAttribute("data-patch");
        e.dataTransfer.setData("text/plain", patch);
        e.dataTransfer.effectAllowed = "move";
        card.classList.add("dragging");
      });
      card.addEventListener("dragend", () => {
        card.classList.remove("dragging");
        if (!this._verCardEdit) this.render();
      });
      card.addEventListener("dragover", (e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
        card.classList.add("drag-over");
      });
      card.addEventListener("dragleave", () => card.classList.remove("drag-over"));
      card.addEventListener("drop", (e) => {
        e.preventDefault();
        card.classList.remove("drag-over");
        const fromPatch = Number(e.dataTransfer.getData("text/plain"));
        const toPatch = Number(card.getAttribute("data-patch"));
        if (!fromPatch || !toPatch || fromPatch === toPatch) return;
        this.captureMsVerCardEdits(el);
        this.swapMsVerDraftContent(fromPatch, toPatch);
        this._verCardEdit = null;
        this._verCardEditSnap = null;
        this.render();
      });
    });

    list.querySelectorAll("[data-ver-done-edit]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.captureMsVerCardEdits(el);
        this._verCardEdit = null;
        this._verCardEditSnap = null;
        if (this._verEditCancelTimer) {
          clearTimeout(this._verEditCancelTimer);
          this._verEditCancelTimer = null;
        }
        this.render();
      });
    });
    list.querySelectorAll("[data-ver-cancel-edit]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.cancelMsVerCardEdit();
        this.render();
      });
    });
    list.querySelectorAll("[data-ver-delete]").forEach((btn) => {
      btn.addEventListener("pointerdown", (e) => e.stopPropagation());
      btn.addEventListener("mousedown", (e) => e.stopPropagation());
      btn.addEventListener("dragstart", (e) => {
        e.preventDefault();
        e.stopPropagation();
      });
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        const patch = Number(btn.getAttribute("data-ver-delete"));
        if (!patch) return;
        const cards = list.querySelectorAll(".del-ms-ver-card");
        if (cards.length <= 1) {
          this.showUiModal(`
            <div class="modal-card">
              <h3 style="margin-top:0">Cannot delete</h3>
              <p class="muted">At least one version is required for a milestone.</p>
              <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
                <button type="button" data-close-ui>OK</button>
              </div>
            </div>`);
          return;
        }
        this.captureMsVerCardEdits(el);
        this.openDeleteVersionModal(route.engId, route.major, route.minor, patch);
      });
    });
  },

  openDeleteVersionModal(engId, major, minor, patch) {
    const key = `${major}.${minor}.${patch}`;
    const draft = (this._msVerDraft || []).find((d) => d.patch === patch);
    const title = (draft && draft.title) || "Untitled";
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Delete version ${this.esc(key)}?</h3>
        <p class="muted" style="margin:0.35rem 0 0.75rem">
          <strong>${this.esc(title)}</strong>
        </p>
        <p class="muted">
          Later versions under this milestone will renumber forward
          (e.g. delete <code>.2</code> → former <code>.3</code> becomes <code>.2</code>).
          This cannot be undone from Trash.
        </p>
        <div class="err" id="del-del-ver-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="del-del-ver-ok">Delete</button>
        </div>
      </div>`);
    this.uiModal().querySelector("#del-del-ver-ok").onclick = async () => {
      const errEl = this.uiModal()?.querySelector("#del-del-ver-err");
      if (errEl) errEl.textContent = "";
      try {
        await api.v1.deliveryDeleteVersion(engId, major, minor, patch);
        this.closeUiModal();
        this._verCardEdit = null;
        this._verCardEditSnap = null;
        this._msVerDraft = null;
        this._msVerDraftKey = null;
        await this.reload();
        this.render();
      } catch (err) {
        if (errEl) errEl.textContent = err.message || String(err);
        else alert(err.message || err);
      }
    };
  },

  /** Swap content only; patch numbers stay fixed. */
  swapMsVerDraftContent(fromPatch, toPatch) {
    const draft = this._msVerDraft || [];
    const a = draft.find((d) => d.patch === fromPatch);
    const b = draft.find((d) => d.patch === toPatch);
    if (!a || !b) return;
    const tmp = { title: a.title, problem: a.problem };
    a.title = b.title;
    a.problem = b.problem;
    b.title = tmp.title;
    b.problem = tmp.problem;
    // Keep milestone title fields in sync with patch-1 slot after swap
    const head = draft.reduce((m, d) => Math.min(m, d.patch), Infinity);
    const h = draft.find((d) => d.patch === head);
    const titleEl = this.el?.querySelector("#ms-title");
    const problemEl = this.el?.querySelector("#ms-problem");
    if (h && titleEl) titleEl.value = h.title || "";
    if (h && problemEl) problemEl.value = h.problem || "";
  },

  DETAIL_CITE_FIELDS: [
    { key: "Title", hint: "Milestone / version title" },
    { key: "Delivery note", hint: "Problem / delivery note" },
    { key: "Goals", hint: "Goals" },
    { key: "Completion criteria", hint: "Acceptance / completion criteria" },
  ],

  /**
   * Shared AI Chat dock (Requirements / Design / Roadmap / Test→Polish).
   * Preserves @ chips + conversation across re-renders for the same surfaceKey.
   */
  polishChat(surfaceKey, opts) {
    if (!this._citeBySurface) this._citeBySurface = {};
    if (!this._msgsBySurface) this._msgsBySurface = {};
    if (this._chats && this._chats[surfaceKey]) {
      this._citeBySurface[surfaceKey] = this._chats[surfaceKey].citeKeys.slice();
      this._msgsBySurface[surfaceKey] = this._chats[surfaceKey].messages.slice();
    }
    const self = this;
    const persist = () => {
      self._citeBySurface[surfaceKey] = chat.citeKeys.slice();
      self._msgsBySurface[surfaceKey] = chat.messages.slice();
    };
    const chat = window.AiChat.create({
      ...opts,
      showLog: opts.showLog !== false,
      rows: opts.rows != null ? opts.rows : 3,
      citeKeys: this._citeBySurface[surfaceKey] || [],
      messages: this._msgsBySurface[surfaceKey] || [],
      onCiteNav(navId) {
        if (typeof opts.onCiteNav === "function") opts.onCiteNav(navId, chat);
      },
      onScrollCite(key) {
        if (typeof opts.onScrollCite === "function") opts.onScrollCite(key);
        else self.scrollToCite(key);
      },
      async onSend(payload) {
        persist();
        if (typeof opts.onSend === "function") await opts.onSend(payload, chat);
        persist();
      },
    });
    const add = chat.addCite.bind(chat);
    const remove = chat.removeCite.bind(chat);
    const append = chat.appendMessage.bind(chat);
    chat.addCite = (key) => {
      add(key);
      persist();
    };
    chat.removeCite = (key) => {
      remove(key);
      persist();
    };
    chat.appendMessage = (role, text) => {
      append(role, text);
      persist();
    };
    if (!this._chats) this._chats = {};
    this._chats[surfaceKey] = chat;
    return chat;
  },

  scrollToCite(key) {
    if (!key || !this.el) return;
    if (!window.AiChat.isVersionKey(key)) {
      const field = this.el.querySelector(`[data-cite-key="${key}"]`);
      if (field) {
        field.scrollIntoView({ behavior: "smooth", block: "center" });
        window.AiChat.flash(field);
      }
      return;
    }
    const row = this.el.querySelector(`[data-ver-key="${key}"]`);
    if (row) {
      row.scrollIntoView({ behavior: "smooth", block: "center" });
      window.AiChat.flash(row);
      return;
    }
    const parts = key.split(".").map(Number);
    const route = this.parseRoute();
    if (route.engId && parts.length === 3 && parts.every((n) => !Number.isNaN(n))) {
      this._pendingScrollVer = key;
      this.goReq(route.engId, parts[0], parts[1]);
    }
  },

  openCiteDetailPicker(chat) {
    const rows = this.DETAIL_CITE_FIELDS.map(
      (f) => `
      <button type="button" class="del-nav-item" data-cite-detail="${this.esc(f.key)}">
        <strong>@${this.esc(f.key)}</strong>
        <span class="muted">${this.esc(f.hint)}</span>
      </button>`,
    ).join("");
    this.showUiModal(`
      <div class="modal-card modal-wide">
        <h3 style="margin-top:0">Cite detail</h3>
        <p class="muted">Adds a detail <code>@</code> chip above the chat (not into the text box).</p>
        <div class="del-nav-list">${rows}</div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
        </div>
      </div>`);
    this.uiModal().querySelectorAll("[data-cite-detail]").forEach((btn) => {
      btn.onclick = () => {
        chat?.addCite(btn.getAttribute("data-cite-detail"));
        this.closeUiModal();
      };
    });
  },

  /** Cite draft versions while creating a milestone (before persist). */
  openCiteCreateVersionPicker(c, chat) {
    const modal = this.el?.querySelector("#del-create-modal");
    if (modal) this.captureMilestoneConfig(modal, c);
    const list = c.versions || [];
    if (!list.length) {
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">No versions</h3>
          <p class="muted">Add a version row with <strong>+ Version</strong> first, then cite it.</p>
          <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
            <button type="button" data-close-ui>OK</button>
          </div>
        </div>`);
      return;
    }
    const rows = list
      .map((v, i) => {
        const key = this.createDraftVerKey(c, i);
        return `
      <button type="button" class="del-nav-item" data-cite-ver="${this.esc(key)}">
        <strong><code>${this.esc(key)}</code></strong>
        <span class="muted">${this.esc(v.title || "Untitled")}</span>
      </button>`;
      })
      .join("");
    this.showUiModal(`
      <div class="modal-card modal-wide">
        <h3 style="margin-top:0">Cite version</h3>
        <p class="muted">Adds an <code>@M.m.p</code> chip for a draft version row.</p>
        <div class="del-nav-list">${rows}</div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
        </div>
      </div>`);
    this.uiModal().querySelectorAll("[data-cite-ver]").forEach((btn) => {
      btn.onclick = () => {
        chat?.addCite(btn.getAttribute("data-cite-ver"));
        this.closeUiModal();
      };
    });
  },

  openCiteVersionPicker(engId, major, minor, chat) {
    const eng = this.findEng(engId);
    const versions = eng ? this.milestoneVersions(eng, major, minor) : [];
    if (!versions.length) {
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">No versions</h3>
          <p class="muted">Nothing to cite under this milestone.</p>
          <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
            <button type="button" data-close-ui>OK</button>
          </div>
        </div>`);
      return;
    }
    const rows = versions
      .map(
        (v) => `
      <button type="button" class="del-nav-item" data-cite-ver="${this.esc(this.verKey(v))}">
        <strong><code>${this.esc(this.verKey(v))}</code></strong>
        <span class="muted">${this.esc(v.title)}</span>
      </button>`,
      )
      .join("");
    this.showUiModal(`
      <div class="modal-card modal-wide">
        <h3 style="margin-top:0">Cite version</h3>
        <p class="muted">Adds an <code>@M.m.p</code> chip above the chat (not into the text box).</p>
        <div class="del-nav-list">${rows}</div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
        </div>
      </div>`);
    this.uiModal().querySelectorAll("[data-cite-ver]").forEach((btn) => {
      btn.onclick = () => {
        chat?.addCite(btn.getAttribute("data-cite-ver"));
        this.closeUiModal();
      };
    });
  },

  majorIsSealed(major) {
    const m = (this.data.majors || []).find((x) => x.major === major);
    return !!(m && m.status === "sealed");
  },

  openDeleteMilestoneModal(engId, major, minor) {
    const key = `${major}.${minor}`;
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Delete milestone ${this.esc(key)}?</h3>
        <p class="muted">
          All versions under <code>${this.esc(key)}</code> will be removed.
          This is only allowed while generation ${major === 0 ? "0.x" : major + ".x"} is not Sealed.
        </p>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="del-ms-ok">Delete</button>
        </div>
      </div>`);
    this.uiModal().querySelector("#del-ms-ok").onclick = async () => {
      try {
        await api.v1.deliveryDeleteMilestone(engId, major, minor);
        this.closeUiModal();
        await this.reload();
        const eng = this.findEng(engId);
        const next = eng && this.latestMilestone(eng);
        if (next) this.goReq(engId, next.major, next.minor);
        else location.hash = "#/delivery/requirements";
      } catch (e) {
        alert(e.message || e);
      }
    };
  },

  /** Worker-bound requirements editor: generation plan + milestones. */
  viewWorkerHome(engId) {
    const eng = this.findEng(engId);
    if (!eng) return `<p class="err">Not found.</p>`;
    const w = this.findWorker(eng.worker_id);
    const filterMaj = this.selectedMajor;
    const groups = {};
    for (const v of eng.versions || []) {
      if (filterMaj != null && v.major !== filterMaj) continue;
      const k = `${v.major}.${v.minor}`;
      groups[k] = groups[k] || { major: v.major, minor: v.minor, versions: [] };
      groups[k].versions.push(v);
    }
    const milestones = Object.values(groups).sort((a, b) => {
      if (b.major !== a.major) return b.major - a.major;
      return b.minor - a.minor;
    });
    const cards = milestones
      .map((ms) => {
        const versions = ms.versions.sort((a, b) => a.patch - b.patch);
        const head = versions[0];
        const msKey = `${ms.major}.${ms.minor}`;
        const life = head ? head.status : "";
        return `
      <div class="del-ms-card" role="link" tabindex="0" data-ms-open
        data-eng="${this.esc(eng.id)}" data-major="${ms.major}" data-minor="${ms.minor}">
        <div class="del-ms-card-top">
          <div class="del-ms-card-main">
            <code class="del-ms-ver">${this.esc(msKey)}</code>
            <strong class="del-ms-card-title">${this.esc(head ? head.title : "Milestone")}</strong>
          </div>
        </div>
        <div class="muted del-ms-card-meta">
          ${life ? this.badge(life) + " · " : ""}
          generation ${ms.major === 0 ? "0.x" : ms.major + ".x"}
          ·
          <span class="del-ver-tip">
            <span class="del-ver-tip-label">${versions.length} version${versions.length === 1 ? "" : "s"}</span>
            <div class="del-ver-tip-panel" role="tooltip">${this.versionTipHtml(versions)}</div>
          </span>
        </div>
      </div>`;
      })
      .join("");
    return `
      <p class="del-crumbs">
        <a href="#/delivery/requirements">Workspace</a>
        <span class="muted">/</span>
        <span>${this.esc(w ? w.label : eng.worker_id)}</span>
      </p>
      <div class="card" style="margin-bottom:1rem">
        <div class="toolbar">
          <h3 style="margin:0;flex:1">${this.esc(eng.title || (w && w.label) || eng.worker_id)}</h3>
          ${this.badge(eng.status)}
        </div>
        <p class="muted" style="margin:0">
          Worker <strong>${this.esc(w ? w.label : eng.worker_id)}</strong>
          · project <code>${this.esc(w ? w.project_rel : "?")}</code>
          · reqs <code>${this.esc(eng.reqs_rel)}</code>
        </p>
      </div>
      ${this.viewMajorPlan()}
      <div class="del-req-section-head">
        <h3 style="margin:0">Milestones <span class="muted">major.minor</span></h3>
      </div>
      <p class="muted" style="margin:0 0 0.65rem;font-size:0.85rem">
        All requirements for this Worker. Click a milestone to open View.
      </p>
      <div class="del-ms-grid">
        ${cards}
        <button type="button" class="del-ms-card del-ms-card-add" id="del-ms-add-bound"
          data-eng="${this.esc(eng.id)}" title="Add milestone">
          <span class="del-req-plus">+</span>
          <span class="muted">New milestone</span>
        </button>
      </div>
      <div id="del-create-modal" class="modal" hidden></div>`;
  },

  viewMilestoneView(engId, major, minor) {
    const eng = this.findEng(engId);
    const versions = eng ? this.milestoneVersions(eng, major, minor) : [];
    const head = versions[0];
    if (!eng || !head) {
      return `<p class="err">Milestone not found.</p>
        <p><a href="#/delivery/requirements/${this.esc(engId)}">Back</a></p>`;
    }
    const w = this.findWorker(eng.worker_id);
    const msKey = `${major}.${minor}`;
    const editKey = `${engId}:${major}.${minor}`;
    const canEdit = this.isAwaitingStatus(head.status);
    const editing = this._msEditing === editKey;
    const ro = editing ? "" : "readonly";
    if (editing) this.ensureMsVerDraft(editKey, versions);
    const draft = editing ? this._msVerDraft : null;
    const verCards = versions
      .map((v) => {
        const d =
          (draft && draft.find((x) => x.patch === v.patch)) || {
            title: v.title,
            problem: v.problem || "",
          };
        const key = this.verKey(v);
        const inline = editing && this._verCardEdit === v.patch;
        const body = inline
          ? `<input class="del-ms-ver-edit-title" value="${this.esc(d.title)}" placeholder="Version title" />
             <textarea class="del-ms-ver-edit-note" rows="2" placeholder="Delivery note">${this.esc(d.problem)}</textarea>
             <div class="del-ms-ver-edit-actions">
               <button type="button" class="ghost" data-ver-cancel-edit>Cancel</button>
               <button type="button" class="ghost del-ms-ver-done" data-ver-done-edit>Done</button>
             </div>`
          : `<strong class="del-ms-ver-title">${this.esc(d.title || "Untitled")}</strong>
             ${d.problem ? `<span class="muted del-ms-ver-note">${this.esc(d.problem)}</span>` : ""}
             ${editing ? `<span class="muted del-ms-ver-hint">Double-click to edit · drag to swap content</span>` : ""}`;
        const canDelete = editing && versions.length > 1;
        const delX = canDelete
          ? `<button type="button" class="ghost del-ms-ver-x" data-ver-delete="${v.patch}" title="Delete version" draggable="false">×</button>`
          : "";
        const viewDrag = !editing;
        return `
      <div class="del-ms-ver-card${editing ? " editable" : ""}${canDelete ? " has-delete" : ""}${viewDrag ? " cite-drag" : ""}"
        data-ver-key="${this.esc(key)}" data-patch="${v.patch}"
        id="ms-ver-${this.esc(key.replace(/\./g, "-"))}"
        ${editing || viewDrag ? 'draggable="true"' : ""}
        ${viewDrag ? 'data-cite-drag="1"' : ""}>
        <code class="del-ms-ver-fixed">${this.esc(key)}</code>
        <div class="del-ms-ver-body">${body}</div>
        ${delX}
      </div>`;
      })
      .join("");
    const actions = [];
    if (canEdit && !editing) {
      actions.push(`<button type="button" class="ghost" id="ms-edit">Edit</button>`);
    }
    if (editing) {
      actions.push(`<button type="button" class="ghost" id="ms-cancel-edit">Cancel</button>`);
      actions.push(`<button type="button" id="ms-save">Save</button>`);
    }
    if (!editing && !this.majorIsSealed(major)) {
      actions.push(
        `<button type="button" class="ghost danger" id="ms-delete">Delete</button>`,
      );
    }
    const viewDrag = !editing;
    const fieldWrap = (key, label, control) => `
      <div class="del-cite-field${viewDrag ? " cite-drag" : ""}" data-cite-key="${this.esc(key)}"
        ${viewDrag ? 'draggable="true"' : ""}>
        <label>${label}</label>
        ${control}
      </div>`;
    const chat = this.polishChat(`ms:${editKey}`, {
      id: "ai-chat-ms",
      title: "Polish chat",
      placeholder: "Message… (Enter to send, Shift+Enter for newline)",
      tall: true,
      rows: 3,
      allowDrop: viewDrag,
      citeNav: [
        { id: "detail", label: "Detail" },
        { id: "version", label: "Version" },
      ],
      hint:
        "<strong>Detail</strong> / <strong>Version</strong> add <code>@</code> chips. " +
        (viewDrag ? "In View, drag fields or version cards onto this panel. " : "") +
        "Click a chip to jump · × to remove.",
      onCiteNav: (navId, c) => {
        if (navId === "detail") this.openCiteDetailPicker(c);
        else if (navId === "version") {
          this.openCiteVersionPicker(engId, major, minor, c);
        }
      },
      onSend: async ({ composed }, c) => {
        c.appendMessage(
          "ai",
          "(mock) Noted. Suggested polish applied to Completion criteria.",
        );
        const next =
          (head.success || "").trim() +
          (head.success ? "\n" : "") +
          "· (AI mock) " +
          composed.slice(0, 200);
        await api.v1.deliveryPatchVersion(engId, major, minor, head.patch, {
          success: next,
        });
        await this.reload();
        this.render();
      },
    });
    return `
      <p class="del-crumbs">
        <a href="#/delivery/requirements">Workspace</a>
        <span class="muted">/</span>
        <a href="#/delivery/requirements/${this.esc(eng.id)}">${this.esc(w ? w.label : eng.worker_id)}</a>
        <span class="muted">/</span>
        <code>${this.esc(msKey)}</code>
      </p>
      <div class="del-split del-split-chatwide">
        <div class="del-main">
          <div class="card">
            <div class="toolbar">
              <div style="flex:1">
                <div class="muted">Worker ${this.esc(w ? w.label : "")} · reqs <code>${this.esc(eng.reqs_rel)}</code></div>
                <h3 style="margin:0.25rem 0 0">
                  Milestone <code>${this.esc(msKey)}</code>
                </h3>
              </div>
              ${this.badge(head.status)}
              ${actions.join("")}
            </div>
            ${fieldWrap("Title", "Title", `<input id="ms-title" value="${this.esc(head.title)}" ${ro} />`)}
            ${fieldWrap("Delivery note", "Delivery note", `<textarea id="ms-problem" rows="2" ${ro}>${this.esc(head.problem)}</textarea>`)}
            ${fieldWrap("Goals", "Goals", `<textarea id="ms-goals" rows="3" ${ro}>${this.esc(head.goals)}</textarea>`)}
            ${fieldWrap("Completion criteria", "Completion criteria", `<textarea id="ms-success" rows="3" ${ro}>${this.esc(head.success)}</textarea>`)}
            <div class="toolbar del-ms-ver-head">
              <h4 style="margin:0;flex:1">Versions</h4>
              ${
                editing
                  ? `<button type="button" class="ghost" id="ms-ver-add" title="Add version">+</button>`
                  : ""
              }
            </div>
            <div class="del-ms-ver-cards" id="ms-ver-cards">${
              verCards || `<p class="muted">No versions.</p>`
            }</div>
          </div>
        </div>
        ${chat.render()}
      </div>`;
  },

  viewVersionDetail(engId, major, minor, patch) {
    const eng = this.findEng(engId);
    const ver = eng && this.findVersion(eng, major, minor, patch);
    if (!eng || !ver) {
      return `<p class="err">Version not found.</p><p><a href="#/delivery/requirements/${this.esc(engId)}">Back</a></p>`;
    }
    const w = this.findWorker(eng.worker_id);
    const key = this.verKey(ver);
    const editKey = `${engId}:${major}.${minor}.${patch}`;
    const canEdit = this.isAwaitingStatus(ver.status);
    const editing = this._msEditing === editKey;
    const ro = editing ? "" : "readonly";
    const actions = [];
    if (canEdit && !editing) {
      actions.push(`<button type="button" class="ghost" id="ver-edit">Edit</button>`);
    }
    if (editing) {
      actions.push(`<button type="button" id="ver-save">Save</button>`);
    }
    const viewDrag = !editing;
    const fieldWrap = (citeKey, label, control) => `
      <div class="del-cite-field${viewDrag ? " cite-drag" : ""}" data-cite-key="${this.esc(citeKey)}"
        ${viewDrag ? 'draggable="true"' : ""}>
        <label>${label}</label>
        ${control}
      </div>`;
    const chat = this.polishChat(`ver:${editKey}`, {
      id: "ai-chat-ver",
      title: "Polish chat",
      placeholder: "Message… (Enter to send, Shift+Enter for newline)",
      tall: true,
      rows: 3,
      allowDrop: viewDrag,
      citeNav: [
        { id: "detail", label: "Detail" },
        { id: "version", label: "Version" },
      ],
      hint:
        "<strong>Detail</strong> / <strong>Version</strong> add <code>@</code> chips. " +
        (viewDrag ? "In View, drag fields onto this panel. " : "") +
        "Click a chip to jump · × to remove.",
      onCiteNav: (navId, c) => {
        if (navId === "detail") this.openCiteDetailPicker(c);
        else if (navId === "version") {
          this.openCiteVersionPicker(engId, major, minor, c);
        }
      },
      onSend: async ({ composed }, c) => {
        c.appendMessage(
          "ai",
          "(mock) Noted. Suggested polish applied to Completion criteria.",
        );
        const next =
          (ver.success || "").trim() +
          (ver.success ? "\n" : "") +
          "· (AI mock) " +
          composed.slice(0, 200);
        await api.v1.deliveryPatchVersion(engId, major, minor, patch, {
          success: next,
        });
        await this.reload();
        this.render();
      },
    });
    return `
      <p>
        <a href="#/delivery/requirements">Workspace</a> /
        <a href="#/delivery/requirements/${this.esc(eng.id)}">${this.esc(w ? w.label : eng.worker_id)}</a> /
        <a href="#/delivery/requirements/${this.esc(eng.id)}/${major}/${minor}"><code>${major}.${minor}</code></a> /
        <code>${this.esc(key)}</code>
      </p>
      <div class="del-split del-split-chatwide">
        <div class="del-main">
          <div class="card">
            <div class="toolbar">
              <div style="flex:1">
                <div class="muted">Worker ${this.esc(w ? w.label : "")} · reqs <code>${this.esc(eng.reqs_rel)}</code></div>
                <h3 style="margin:0.25rem 0 0">
                  <code>${this.esc(key)}</code>
                  <span class="muted" style="font-weight:500;font-size:0.85rem">
                    · patch→version
                  </span>
                </h3>
              </div>
              ${this.badge(ver.status)}
              ${actions.join("")}
            </div>
            ${fieldWrap("Title", "Title", `<input id="ver-title" value="${this.esc(ver.title)}" ${ro} />`)}
            ${fieldWrap("Delivery note", "Delivery note", `<textarea id="ver-problem" rows="3" ${ro}>${this.esc(ver.problem)}</textarea>`)}
            ${fieldWrap("Goals", "Goals", `<textarea id="ver-goals" rows="2" ${ro}>${this.esc(ver.goals)}</textarea>`)}
            ${fieldWrap("Completion criteria", "Completion criteria", `<textarea id="ver-success" rows="3" ${ro} placeholder="Features and capabilities that must work…">${this.esc(ver.success)}</textarea>`)}
          </div>
        </div>
        ${chat.render()}
      </div>`;
  },

  /** Milestones that do not yet have a design (for New design picker). */
  designCandidateMilestones() {
    const have = new Set(
      (this.data.designs || []).map((d) => `${d.eng_req_id}:${d.major}.${d.minor}`),
    );
    const out = [];
    for (const eng of this.data.eng_reqs || []) {
      const seen = new Set();
      for (const v of eng.versions || []) {
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

  openCreateDesignModal() {
    const cands = this.designCandidateMilestones();
    if (!cands.length) {
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">New design</h3>
          <p class="muted">Every milestone already has a design, or there are no requirements yet.</p>
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
        <p class="muted" style="margin-top:0">Bootstrap two option cards from the bound requirement. Polish, choose, then Approve.</p>
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

  viewDesList() {
    const filter = this._desFilter || "all";
    const list = (this.data.designs || []).filter((d) => {
      if (filter === "all") return true;
      return (d.status || "") === filter;
    });
    const cards = list
      .map((d) => {
        const eng = this.findEng(d.eng_req_id);
        const w = eng ? this.findWorker(eng.worker_id) : null;
        const chosen = (d.options || []).find((o) => o.id === d.chosen_option_id);
        const nOpts = (d.options || []).length;
        return `
      <div class="del-des-card" role="link" tabindex="0" data-des-open="${this.esc(d.id)}">
        <div class="del-des-card-top">
          <strong class="del-des-card-title">${this.esc(d.title)}</strong>
          ${this.badge(d.status)}
        </div>
        <div class="muted del-des-card-meta">
          <span>${this.esc(w ? w.label || w.id : eng?.title || d.eng_req_id)}</span>
          <span>·</span>
          <code>${this.esc(`${d.major}.${d.minor}`)}</code>
          <span>·</span>
          <span>${nOpts} option${nOpts === 1 ? "" : "s"}</span>
        </div>
        <div class="del-des-card-foot muted">
          ${
            chosen
              ? `Chosen: <strong>${this.esc(chosen.title)}</strong>`
              : d.status === "approved"
                ? "Approved"
                : "No option chosen yet"
          }
        </div>
      </div>`;
      })
      .join("");
    return `
      <div class="toolbar del-des-list-bar">
        <label class="muted" style="margin:0">Status</label>
        <select id="des-filter" style="width:auto;margin:0">
          <option value="all"${filter === "all" ? " selected" : ""}>All</option>
          <option value="draft"${filter === "draft" ? " selected" : ""}>Draft</option>
          <option value="approved"${filter === "approved" ? " selected" : ""}>Approved</option>
        </select>
        <span class="muted" style="flex:1">${list.length} design${list.length === 1 ? "" : "s"}</span>
      </div>
      <div class="del-des-grid">
        ${cards || `<p class="muted">No designs yet. Use <strong>New design</strong> to bootstrap from a milestone.</p>`}
      </div>`;
  },

  viewDesDetail(id) {
    const d = this.data.designs.find((x) => x.id === id);
    if (!d) return `<p class="err">Design not found.</p>`;
    const eng = this.findEng(d.eng_req_id);
    const w = eng ? this.findWorker(eng.worker_id) : null;
    const approved = d.status === "approved";
    const ro = approved ? "readonly" : "";
    const hasChoice = !!(d.chosen_option_id || "").trim();
    const msKey = `${d.major}.${d.minor}`;
    const reqHref = `#/delivery/requirements/${this.esc(d.eng_req_id)}/${d.major}/${d.minor}`;
    const opts = (d.options || [])
      .map((o) => {
        const chosen = o.id === d.chosen_option_id;
        return `
        <div class="card del-opt${chosen ? " del-opt-chosen" : ""}" data-opt-id="${this.esc(o.id)}"
          data-cite-key="${this.esc(o.title)}">
          <div class="toolbar del-opt-head">
            <input class="del-opt-title-input" data-opt-title value="${this.esc(o.title)}" ${ro}
              placeholder="Option title" />
            ${
              chosen
                ? `<span class="badge ok">Chosen</span>`
                : approved
                  ? ""
                  : `<button type="button" class="ghost" data-choose-opt="${this.esc(o.id)}">Choose</button>`
            }
          </div>
          <label>Summary</label>
          <textarea data-opt-summary rows="2" ${ro} placeholder="What this option proposes…">${this.esc(o.summary || "")}</textarea>
          <label>Tradeoffs</label>
          <textarea data-opt-tradeoffs rows="2" ${ro} placeholder="Pros / cons…">${this.esc(o.tradeoffs || "")}</textarea>
          <label>Risks</label>
          <textarea data-opt-risks rows="2" ${ro} placeholder="What could go wrong…">${this.esc(o.risks || "")}</textarea>
          <label>Out of scope</label>
          <textarea data-opt-oos rows="2" ${ro} placeholder="Explicitly not in this option…">${this.esc(o.out_of_scope || "")}</textarea>
        </div>`;
      })
      .join("");
    const citeNav = [
      { id: "adr", label: "ADR" },
      ...(d.options || []).map((o, i) => ({
        id: "opt:" + o.id,
        label: o.title || `Option ${i + 1}`,
      })),
    ];
    const chat = this.polishChat(`des:${d.id}`, {
      id: "ai-chat-des",
      title: "Polish chat",
      placeholder: "How should this design be polished?",
      tall: true,
      rows: 3,
      allowDrop: !approved,
      citeNav,
      emptyHint: "Cite ADR / an Option, then send a polish request.",
      onCiteNav: (navId, c) => {
        if (navId === "adr") c.addCite("ADR");
        else if (navId.startsWith("opt:")) {
          const oid = navId.slice(4);
          const opt = (d.options || []).find((x) => x.id === oid);
          if (opt) c.addCite(opt.title || oid);
        }
      },
      onScrollCite: (key) => {
        const root = this.el;
        if (key === "ADR") {
          const field = root?.querySelector("#des-adr");
          if (field) {
            field.scrollIntoView({ behavior: "smooth", block: "center" });
            field.focus?.();
            window.AiChat.flash(field);
          }
          return;
        }
        const card = [...(root?.querySelectorAll("[data-opt-id]") || [])].find(
          (el) => el.getAttribute("data-cite-key") === key,
        );
        if (card) {
          card.scrollIntoView({ behavior: "smooth", block: "center" });
          window.AiChat.flash(card);
        }
      },
      onSend: async ({ composed }, c) => {
        if (approved) {
          c.appendMessage("ai", "(mock) Design is approved — ADR note only.");
        } else {
          c.appendMessage(
            "ai",
            "(mock) Noted. Suggested polish applied to ADR (and first cited option summary when present).",
          );
        }
        const line = "· (AI mock) " + composed.slice(0, 160);
        const adr = (d.adr || "").trim() ? d.adr.trim() + "\n" + line : line;
        const patch = { adr };
        if (!approved) {
          const citeOpt = (d.options || []).find((o) =>
            composed.includes("@" + (o.title || "")),
          );
          if (citeOpt) {
            patch.options = [
              {
                ...citeOpt,
                summary:
                  (citeOpt.summary || "").trim() +
                  (citeOpt.summary ? "\n" : "") +
                  line,
              },
            ];
          }
        }
        await api.v1.deliveryPatchDesign(d.id, patch);
        await this.reload();
        this.render();
      },
    });
    const actions = [];
    if (!approved) {
      actions.push(`<button type="button" class="ghost" id="des-save">Save</button>`);
      actions.push(
        `<button type="button" id="des-approve"${hasChoice ? "" : " disabled title=\"Choose an option first\""}>Approve</button>`,
      );
    }
    actions.push(
      `<button type="button" class="ghost" id="des-bootstrap"${approved ? "" : " disabled title=\"Approve design first\""}>Bootstrap roadmap</button>`,
    );
    return `
      <p class="del-crumbs">
        <a href="#/delivery/designs">Designs</a>
        <span class="muted">/</span>
        <a href="#/delivery/requirements/${this.esc(d.eng_req_id)}">${this.esc(w ? w.label || w.id : eng?.title || d.eng_req_id)}</a>
        <span class="muted">/</span>
        <a href="${reqHref}"><code>${this.esc(msKey)}</code></a>
      </p>
      <div class="del-split del-split-chatwide">
        <div class="del-main">
          <div class="card">
            <div class="toolbar">
              <div style="flex:1;min-width:0">
                <div class="muted">Bound req <code>${this.esc(d.version || msKey)}</code>
                  · ${approved ? "read-only options" : "draft — choose, polish, Approve"}</div>
                <h3 style="margin:0.25rem 0 0">${this.esc(d.title)}</h3>
              </div>
              ${this.badge(d.status)}
              ${actions.join("")}
            </div>
            <div class="err" id="des-err"></div>
            <div class="del-cite-field${approved ? "" : " cite-drag"}" data-cite-key="ADR"
              ${approved ? "" : 'draggable="true"'}>
              <label>ADR / decision notes</label>
              <textarea id="des-adr" rows="4" ${ro}
                placeholder="Why this option wins, constraints, follow-ups…">${this.esc(d.adr || "")}</textarea>
            </div>
          </div>
          <div class="toolbar del-des-opt-head">
            <h4 style="margin:0;flex:1">Options</h4>
            <span class="muted">${(d.options || []).length} card${(d.options || []).length === 1 ? "" : "s"}</span>
          </div>
          <div class="del-des-opt-list">${opts || `<p class="muted">No options.</p>`}</div>
        </div>
        ${chat.render()}
      </div>`;
  },

  viewRoadmap(selectedId) {
    const g = this.data.roadmap || { milestones: [], edges: [] };
    const milestones = g.milestones || [];
    const edgesList = g.edges || [];
    const selected =
      milestones.find((m) => m.id === selectedId) || milestones[0] || null;
    const nodes = milestones
      .map((m) => {
        const sel = selected && m.id === selected.id ? " selected" : "";
        return `<div class="del-node${sel}" data-ms="${this.esc(m.id)}"
          style="left:${m.x}px;top:${m.y}px">
          <div class="del-node-title">${this.esc(m.title)}</div>
          ${this.badge(m.status)}
        </div>`;
      })
      .join("");
    const edges = edgesList
      .map((e) => {
        const a = milestones.find((m) => m.id === e.from);
        const b = milestones.find((m) => m.id === e.to);
        if (!a || !b) return "";
        return `<line x1="${a.x + 110}" y1="${a.y + 36}" x2="${b.x}" y2="${b.y + 36}" class="del-edge" marker-end="url(#del-arrow)" />`;
      })
      .join("");
    const phases = selected
      ? (selected.phases || [])
          .map(
            (p, i) =>
              `<li><span class="muted">${i + 1}.</span> ${this.esc(p.title)} ${this.badge(p.status)}</li>`,
          )
          .join("")
      : "";
    const readyRows = [];
    const seen = new Set();
    for (const eng of this.data.eng_reqs || []) {
      for (const v of eng.versions || []) {
        if (v.status !== "awaiting_roadmap") continue;
        const key = `${eng.id}:${v.major}.${v.minor}`;
        if (seen.has(key)) continue;
        seen.add(key);
        readyRows.push({
          engId: eng.id,
          major: v.major,
          minor: v.minor,
          title: `${eng.title} · ${v.major}.${v.minor}`,
        });
      }
    }
    const readyBlock = readyRows.length
      ? `<h4>Ready for development</h4>
         <p class="muted">Milestones in <em>Awaiting roadmap</em> — mark ready to unlock the app develop stage.</p>
         ${readyRows
           .map(
             (r) => `
           <div class="toolbar" style="margin-bottom:0.4rem">
             <span style="flex:1">${this.esc(r.title)}</span>
             <button type="button" data-ms-ready data-eng="${this.esc(r.engId)}"
               data-major="${r.major}" data-minor="${r.minor}">Mark ready</button>
           </div>`,
           )
           .join("")}`
      : `<p class="muted">No eng-req milestones in Awaiting roadmap.</p>`;
    return `
      <div class="del-roadmap">
        <div class="del-canvas card">
          <svg class="del-edges" width="900" height="320">
            <defs>
              <marker id="del-arrow" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
                <path d="M0,0 L6,3 L0,6 Z" fill="var(--muted)" />
              </marker>
            </defs>
            ${edges}
          </svg>
          <div class="del-nodes">${nodes}</div>
        </div>
        <aside class="card del-inspector">
          <h3>Inspector</h3>
          ${
            selected
              ? `<p><strong>${this.esc(selected.title)}</strong> ${this.badge(selected.status)}</p>
                 <p class="muted">${this.esc(selected.acceptance)}</p>
                 <h4>Phase chain <span class="muted">(= patch under a minor)</span></h4>
                 <ol class="del-phases">${phases}</ol>
                 ${readyBlock}`
              : `<p class="muted">Select a milestone node.</p>${readyBlock}`
          }
        </aside>
      </div>`;
  },

  viewGates() {
    const pending = [];
    for (const g of this.data.gates || []) {
      pending.push({
        title: g.title,
        href: g.href || "#/delivery",
        meta: g.kind || "execution constraint",
      });
    }
    for (const eng of this.data.eng_reqs || []) {
      for (const v of eng.versions || []) {
        if (v.status === "awaiting_design") {
          const href = `#/delivery/requirements/${eng.id}/${v.major}/${v.minor}/${v.patch}`;
          if (pending.some((p) => p.href === href)) continue;
          pending.push({
            title: `${eng.title} · ${v.major}.${v.minor} ${v.title}`,
            href,
            meta: "awaiting design",
          });
        }
      }
    }
    for (const d of this.data.designs || []) {
      if (d.status !== "approved") {
        const href = `#/delivery/designs/${d.id}`;
        if (pending.some((p) => p.href === href)) continue;
        pending.push({ title: d.title, href, meta: "design approval" });
      }
    }
    const items = pending
      .map(
        (g) => `
      <div class="card">
        <div class="toolbar">
          <div style="flex:1">
            <div>${this.esc(g.title)}</div>
            <div class="muted">${this.esc(g.meta)}</div>
          </div>
          ${this.badge("pending")}
          <a href="#" data-gate-open data-href="${this.esc(g.href)}">Open</a>
        </div>
      </div>`,
      )
      .join("");
    return `
      <p class="muted">
        Execution constraints and pending human checks for the delivery pipeline.
        Full gate automation lands with execute/test (0.37+).
      </p>
      ${items || `<div class="card muted">No open constraints.</div>`}`;
  },
};
