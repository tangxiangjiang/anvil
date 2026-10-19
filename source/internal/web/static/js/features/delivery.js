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
    if (!this.data.polishes) this.data.polishes = [];
    if (!this.data.buglist_items) this.data.buglist_items = [];
    if (!this.data.test_cases) this.data.test_cases = [];
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
      sub: "Generation plan · Project · milestone (major.minor.patch)",
    },
    designs: {
      title: "Designs",
      sub: "Generation plan · Design · Save → View → Approve → Bootstrap",
    },
    roadmap: {
      title: "Roadmap",
      sub: "Generation plan · milestone · Worker · Execute · 提交测试",
    },
    test: {
      title: "Test",
      sub: "Generation plan · milestones in testing · Pass / Fail",
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
    // Legacy Gates routes → Roadmap (gates live on Design Versions).
    if (
      hash === "#/delivery/gates" ||
      hash.startsWith("#/delivery/gates/") ||
      hash === "#/delivery/roadmap/gates" ||
      hash.startsWith("#/delivery/roadmap/gates/")
    ) {
      hash = "#/delivery/roadmap";
      location.hash = hash;
    }
    const parts = hash.replace(/^#\/?/, "").split("/").filter(Boolean);
    // delivery/{section}
    // delivery/{section}/m/{M}[/{msId}]
    // delivery/requirements/projects
    // delivery/requirements/{engId}[/{M}/{m}/{p}]
    // delivery/designs/{desId}
    const section = parts[1] || "requirements";
    const empty = {
      section,
      engId: null,
      major: null,
      minor: null,
      patch: null,
      desId: null,
      msId: null,
      routeMajor: null,
      projectsPicker: false,
    };

    if (parts[2] === "m" && parts[3] != null && parts[3] !== "") {
      const routeMajor = Number(parts[3]);
      if (Number.isNaN(routeMajor)) return empty;
      if (section === "roadmap") {
        return { ...empty, routeMajor, msId: parts[4] || null };
      }
      if (section === "designs") {
        return { ...empty, routeMajor };
      }
      if (section === "requirements" || section === "test") {
        return { ...empty, routeMajor };
      }
      return { ...empty, routeMajor };
    }

    if (section === "requirements" && parts[2] === "projects") {
      return { ...empty, projectsPicker: true };
    }

    if (section === "designs") {
      return { ...empty, desId: parts[2] || null };
    }

    if (section === "roadmap") {
      // Legacy #/delivery/roadmap/{msId} (no m/{M})
      return { ...empty, msId: parts[2] || null };
    }

    return {
      ...empty,
      engId: parts[2] || null,
      major: parts[3] != null ? Number(parts[3]) : null,
      minor: parts[4] != null ? Number(parts[4]) : null,
      patch: parts[5] != null ? Number(parts[5]) : null,
    };
  },

  goMajor(section, major) {
    location.hash = `#/delivery/${section}/m/${major}`;
  },

  majorLabel(major) {
    return major === 0 ? "0.x" : `${major}.x`;
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
    if (route.routeMajor != null) this.selectedMajor = route.routeMajor;
    const meta = this.sectionMeta[route.section] || {
      title: "Delivery",
      sub: "",
    };

    // Legacy roadmap/{msId} → roadmap/m/{M}/{msId}
    if (
      route.section === "roadmap" &&
      route.msId &&
      route.routeMajor == null
    ) {
      const bind = this.parseRoadmapMsBind(route.msId);
      if (bind) {
        location.hash = `#/delivery/roadmap/m/${bind.major}/${encodeURIComponent(route.msId)}`;
        return;
      }
    }

    let body = "";
    if (route.section === "requirements") {
      if (route.engId) {
        if (route.major == null) body = this.viewWorkerHome(route.engId);
        else if (route.patch == null)
          body = this.viewMilestoneView(route.engId, route.major, route.minor);
        else
          body = this.viewVersionDetail(
            route.engId,
            route.major,
            route.minor,
            route.patch,
          );
      } else if (route.routeMajor != null)
        body = this.viewRequirementsByMajor(route.routeMajor);
      else if (route.projectsPicker)
        body = this.viewWorkspace();
      else
        // Home: full Generation plan (edit/start/seal) + Project picker — major is entry, not a replacement.
        body = `${this.viewMajorIndex("requirements")}${this.viewWorkspace()}`;
    } else if (route.section === "designs") {
      if (route.desId) body = this.viewDesDetail(route.desId);
      else if (route.routeMajor != null)
        body = this.viewDesList(route.routeMajor);
      else body = this.viewMajorIndex("designs");
    } else if (route.section === "roadmap") {
      // Detail page removed: deep link → major list + focus card.
      if (route.msId && route.routeMajor != null) {
        this._rmFocusMs = route.msId;
        location.hash = `#/delivery/roadmap/m/${route.routeMajor}`;
        return;
      }
      if (route.routeMajor != null)
        body = this.viewRoadmapList(route.routeMajor);
      else body = this.viewMajorIndex("roadmap");
    } else if (route.section === "test") {
      if (route.routeMajor != null) body = this.viewTestList(route.routeMajor);
      else body = this.viewMajorIndex("test");
    } else {
      body = `<p class="muted">Unknown section. Open a Delivery item in the sidebar.</p>`;
    }

    const reqHome =
      route.section === "requirements" &&
      !route.engId &&
      route.routeMajor == null &&
      !route.projectsPicker;
    const majorIndex =
      !route.engId &&
      !route.desId &&
      !route.msId &&
      !route.projectsPicker &&
      route.routeMajor == null &&
      (route.section === "designs" ||
        route.section === "roadmap" ||
        route.section === "test");
    const isWorkspace =
      route.section === "requirements" &&
      (route.projectsPicker || reqHome);
    const majorScope =
      route.routeMajor != null &&
      !route.engId &&
      !route.desId &&
      !route.msId;
    const workerHome =
      route.section === "requirements" && route.engId && route.major == null;
    const engHome = workerHome ? this.findEng(route.engId) : null;
    const wHome = engHome ? this.findWorker(engHome.worker_id) : null;
    const headActions =
      reqHome || majorIndex || workerHome || majorScope
        ? ""
        : `<button type="button" class="ghost" id="del-reset">Reset demo data</button>`;
    let metaTitle = meta.title;
    let metaSub = meta.sub;
    if (reqHome) {
      metaTitle = "Requirements";
      metaSub =
        "Generation plan · pick a major or a Project (execute picks a Worker later)";
    } else if (majorIndex) {
      metaTitle = "Generations";
      metaSub = `Pick a major line · then ${meta.title}`;
    } else if (majorScope) {
      metaTitle = this.majorLabel(route.routeMajor);
      metaSub = `${meta.title} under this generation`;
    } else if (route.projectsPicker) {
      metaTitle = "Projects";
      metaSub = "Pick a Project by name · Delivery binds here";
    } else if (workerHome) {
      metaTitle = engHome?.title || engHome?.project_key || wHome?.label || engHome?.worker_id || "Project";
      metaSub = engHome?.project_key
        ? `Project · ${engHome.project_key}`
        : "Generation plan · milestones for this Project";
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
    if (
      isWorkspace ||
      reqHome ||
      (route.section === "requirements" && route.routeMajor != null)
    ) {
      this.loadWorkspaceInventory();
    }
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
    el.querySelector("#del-proj-filter")?.addEventListener("input", (ev) => {
      this._projFilter = ev.target.value || "";
      const q = String(this._projFilter).trim().toLowerCase();
      el.querySelectorAll("[data-proj-open]").forEach((card) => {
        const name = (card.getAttribute("data-project-name") || "").toLowerCase();
        const key = (card.getAttribute("data-project-key") || "").toLowerCase();
        card.hidden = !!(q && !name.includes(q) && !key.includes(q));
      });
      el.querySelectorAll(".del-ws-fleet").forEach((sec) => {
        const any = [...sec.querySelectorAll("[data-proj-open]")].some((c) => !c.hidden);
        sec.hidden = !!(q && !any);
      });
    });
    el.querySelectorAll("[data-proj-open]").forEach((card) => {
      const open = async () => {
        const engId = card.getAttribute("data-eng-id");
        if (engId) {
          this.goReq(engId);
          return;
        }
        const projectId = card.getAttribute("data-project-id");
        const workspaceId = card.getAttribute("data-workspace-id");
        const projectKey = card.getAttribute("data-project-key") || "";
        const title = card.getAttribute("data-project-name") || projectKey;
        try {
          const eng = await api.v1.deliveryEnsureProject({
            project_id: projectId,
            workspace_id: workspaceId,
            project_key: projectKey,
            title,
          });
          await this.reload();
          this.goReq(eng.id);
        } catch (e) {
          alert(e.message || e);
        }
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
      // Load Workspace → Project inventory for Delivery picker.
      const catalog = [];
      for (const f of this._clusters) {
        let workspaces = [];
        try {
          const wd = await api.v1.listWorkspaces(f.id);
          workspaces = wd.workspaces || [];
        } catch (_) {
          continue;
        }
        for (const ws of workspaces) {
          let projects = [];
          try {
            const pd = await api.v1.listProjects(ws.id);
            projects = pd.projects || [];
          } catch (_) {
            projects = [];
          }
          catalog.push({
            fleet: f,
            workspace: ws,
            projects,
          });
        }
      }
      this._projectCatalog = catalog;
    } catch (e) {
      this._clustersError = e.message || String(e);
      this._clusters = [];
      this._projectCatalog = [];
    }
    this._clustersLoaded = true;
    this._wsLoading = false;
    const route = this.parseRoute();
    if (
      this.el &&
      route.section === "requirements" &&
      (!route.engId || route.projectsPicker || route.routeMajor != null)
    ) {
      this.render();
    }
  },

  findEngByWorker(workerId) {
    return (this.data.eng_reqs || []).find((e) => e.worker_id === workerId) || null;
  },

  findEngByProject(projectId) {
    return (this.data.eng_reqs || []).find((e) => e.project_id === projectId) || null;
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
    el.querySelectorAll("[data-major-open]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const maj = Number(btn.getAttribute("data-major-open"));
        const sec =
          btn.getAttribute("data-section") || this.parseRoute().section;
        this.selectedMajor = maj;
        this.goMajor(sec, maj);
      });
    });
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

  /** In-app notice (no browser alert). */
  showNotice(message, title = "Notice") {
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">${this.esc(title)}</h3>
        <p class="muted" style="white-space:pre-wrap">${this.esc(message || "")}</p>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" data-close-ui>OK</button>
        </div>
      </div>`);
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
      el.querySelector("#des-new-card")?.addEventListener("click", () => {
        this.openCreateDesignModal();
      });
      el.querySelectorAll("[data-des-delete]").forEach((btn) => {
        btn.addEventListener("click", (ev) => {
          ev.preventDefault();
          ev.stopPropagation();
          this.openDeleteDesignModal(btn.getAttribute("data-des-delete"));
        });
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
      const saveDesignDetail = async () => {
        const adrPatch = this.readAdrFields(el);
        await api.v1.deliveryPatchDesign(des.id, adrPatch);
        const cards = [...el.querySelectorAll("[data-ver-id]")];
        for (const card of cards) {
          const vid = card.getAttribute("data-ver-id");
          const depends_on = [...card.querySelectorAll("[data-ver-dep]:checked")].map(
            (cb) => cb.value,
          );
          await api.v1.deliveryPatchDesignVersion(des.id, vid, {
            title: card.querySelector("[data-ver-title]")?.value ?? "",
            gate: card.querySelector('textarea[data-ver-field="gate"]')?.value ?? "",
            sub_architecture:
              card.querySelector('textarea[data-ver-field="sub_architecture"]')?.value ?? "",
            class_design:
              card.querySelector('textarea[data-ver-field="class_design"]')?.value ?? "",
            depends_on,
          });
        }
      };
      el.querySelector("#des-ver-add")?.addEventListener("click", async () => {
        try {
          await saveDesignDetail();
          await api.v1.deliveryAddDesignVersion(des.id, {});
          await this.reload();
          this.render();
        } catch (e) {
          this.showNotice(e.message || String(e), "Could not add Version");
        }
      });
      el.querySelectorAll("[data-ver-delete]").forEach((btn) => {
        btn.addEventListener("click", () => {
          this.openDeleteDesignVersionModal(des.id, btn.getAttribute("data-ver-delete"));
        });
      });
      el.querySelector("#des-save")?.addEventListener("click", async () => {
        const err = el.querySelector("#des-err");
        if (err) err.textContent = "";
        try {
          await saveDesignDetail();
          await this.reload();
          this.render();
        } catch (e) {
          if (err) err.textContent = e.message || String(e);
          else this.showNotice(e.message || String(e), "Save failed");
        }
      });
      el.querySelector("#des-view")?.addEventListener("click", () => {
        this.openDesignViewModal(des.id, {
          root: des.status === "approved" ? null : el,
          save: des.status === "approved" ? null : saveDesignDetail,
        });
      });
      if (des.status !== "approved" && window.MdPreview) {
        window.MdPreview.bindToggle(el, {
          onMode: (key, mode) => {
            const pane = [...el.querySelectorAll(".md-pane")].find(
              (p) => p.getAttribute("data-md-key") === key,
            );
            this.setAdrPaneDrag(pane, mode);
          },
        });
        el.querySelectorAll("[data-md-insert]").forEach((btn) => {
          btn.addEventListener("click", () => {
            const key = btn.getAttribute("data-md-insert");
            const pane = btn.closest(".md-pane");
            const ta = pane?.querySelector("textarea");
            if (!ta || ta.readOnly) return;
            const tpl = window.MdPreview.templates[key] || "";
            if (!ta.value.trim()) ta.value = tpl;
            else ta.value = ta.value.replace(/\s*$/, "\n\n") + tpl;
            ta.focus();
          });
        });
      }
      if (des.status === "approved") {
        this.mountDesignReadOnlyPreviews(el);
      }
      el.querySelector("#des-approve")?.addEventListener("click", async () => {
        await this.runDesignApprove(des.id, {
          root: el,
          save: saveDesignDetail,
          errEl: el.querySelector("#des-err"),
        });
      });
      el.querySelector("#des-bootstrap")?.addEventListener("click", async () => {
        try {
          await api.v1.deliveryBootstrapDesign(des.id);
          await this.reload();
          location.hash = `#/delivery/roadmap/m/${des.major}/ms_${des.eng_req_id}_${des.major}_${des.minor}`;
        } catch (e) {
          this.showNotice(e.message || String(e), "Bootstrap failed");
        }
      });
      if (des.status !== "approved") {
        const desChat = this._chats?.[`des:${des.id}`];
        if (desChat) {
          desChat.mount(el);
          desChat.bindDropSources(el);
        }
      }
    }

    if (route.section === "roadmap") {
      el.querySelector("#rm-filter")?.addEventListener("change", (ev) => {
        this._rmFilter = ev.target.value || "all";
        this.render();
      });
      if (route.routeMajor != null) {
        this.loadExecuteWorkers();
        this.bindRoadmapCanvas(el);
        this.bindMilestoneCardControls(el);
        this.bindRoadmapJobStreams(el);
        if (this._rmFocusMs) {
          const focusId = this._rmFocusMs;
          this._rmFocusMs = null;
          requestAnimationFrame(() => this.focusMilestoneCard(el, focusId));
        }
      }
    }

    if (route.section === "test" && route.routeMajor != null) {
      this.bindTestCardControls(el);
      this.bindTestCampaignJobStreams(el);
    }
  },

  bindTestCampaignJobStreams(el) {
    el.querySelectorAll("[data-test-card]").forEach((card) => {
      const jobId = (card.getAttribute("data-campaign-job") || "").trim();
      const st = (card.getAttribute("data-campaign-job-status") || "").toLowerCase();
      if (!jobId || (st !== "running" && st !== "queued")) return;
      if (this._jobAbort?.[jobId]) return;
      if (!this._jobAbort) this._jobAbort = {};
      const ac = new AbortController();
      this._jobAbort[jobId] = ac;
      const snip = card.querySelector("[data-campaign-snip]");
      void this.watchDeliveryJobSSE(jobId, ac.signal, {
        onEvent(data) {
          const text = data.text || data.content || data.type || "";
          if (snip && text) snip.textContent = String(text).slice(0, 160);
        },
        onDone: async () => {
          delete this._jobAbort[jobId];
          await this.reload();
          this.render();
        },
      });
    });
  },

  async awaitDeliveryJob(jobId, { onEvent } = {}) {
    if (!jobId) return;
    await new Promise((resolve) => {
      const ac = new AbortController();
      void this.watchDeliveryJobSSE(jobId, ac.signal, {
        onEvent,
        onDone: async () => {
          ac.abort();
          resolve();
        },
      });
    });
  },

  closeRoadmapJobStreams() {
    if (this._jobAbort) {
      for (const a of Object.values(this._jobAbort)) {
        try {
          a.abort();
        } catch (_) {}
      }
    }
    this._jobAbort = {};
  },

  bindRoadmapJobStreams(el) {
    this.closeRoadmapJobStreams();
    this._jobAbort = {};
    el.querySelectorAll("[data-ms-card]").forEach((card) => {
      const run = (card.getAttribute("data-run-state") || "").toLowerCase();
      const jobId = (card.getAttribute("data-active-job") || "").trim();
      if (run !== "running" || !jobId) return;
      if (this._jobAbort[jobId]) return;
      const ac = new AbortController();
      this._jobAbort[jobId] = ac;
      const snip = card.querySelector("[data-job-snip]");
      void this.watchDeliveryJobSSE(jobId, ac.signal, {
        onEvent(data) {
          const text = data.text || data.content || data.type || "";
          if (snip && text) snip.textContent = String(text).slice(0, 160);
        },
        onDone: async () => {
          delete this._jobAbort[jobId];
          await this.reload();
          this.render();
        },
      });
    });
  },

  async watchDeliveryJobSSE(jobId, signal, { onEvent, onDone }) {
    try {
      const tok = api.getToken?.() || localStorage.getItem("anvil_web_token") || "";
      const res = await fetch(api.v1.deliveryJobEventsURL(jobId), {
        headers: tok ? { Authorization: "Bearer " + tok } : {},
        signal,
      });
      if (!res.ok || !res.body) {
        if (onDone) await onDone();
        return;
      }
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      let eventName = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        const parts = buf.split("\n");
        buf = parts.pop() || "";
        for (const line of parts) {
          if (line.startsWith("event:")) {
            eventName = line.slice(6).trim();
            continue;
          }
          if (line.startsWith("data:")) {
            const raw = line.slice(5).trim();
            let data = {};
            try {
              data = JSON.parse(raw);
            } catch (_) {
              data = { text: raw };
            }
            if (eventName === "job.event" && onEvent) onEvent(data);
            if (eventName === "job.done" || eventName === "error") {
              if (onDone) await onDone();
              return;
            }
            eventName = "";
          }
          if (line === "") eventName = "";
        }
      }
      if (onDone) await onDone();
    } catch (e) {
      if (e?.name === "AbortError") return;
      if (onDone) await onDone();
    }
  },

  bindMilestoneCardControls(el) {
    el.querySelectorAll(".del-ms-worker").forEach((sel) => {
      sel.addEventListener("change", async () => {
        const msId = sel.getAttribute("data-ms");
        const workerId = (sel.value || "").trim();
        if (!msId) return;
        try {
          const saved = await api.v1.deliveryPatchMilestone(msId, {
            execute_worker_id: workerId,
          });
          const m = (this.data.roadmap?.milestones || []).find(
            (n) => n.id === msId,
          );
          if (m) m.execute_worker_id = saved.execute_worker_id || "";
          this.render();
        } catch (e) {
          this.showNotice(e.message || String(e), "Could not save Worker");
        }
      });
    });
    el.querySelectorAll("[data-ms-start]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-ms-start");
        const card = btn.closest("[data-ms-card]");
        const workerId = (
          card?.querySelector(".del-ms-worker")?.value || ""
        ).trim();
        btn.disabled = true;
        try {
          await api.v1.deliveryPatchMilestone(msId, {
            execute_worker_id: workerId,
            execute: true,
          });
          await this.reload();
          this.render();
        } catch (e) {
          this.showNotice(e.message || String(e), "Start failed");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-ms-pause]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-ms-pause");
        try {
          await api.v1.deliveryPatchMilestone(msId, { run_state: "paused" });
          await this.reload();
          this.render();
        } catch (e) {
          this.showNotice(e.message || String(e), "Pause failed");
        }
      });
    });
    el.querySelectorAll("[data-ms-stop]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-ms-stop");
        try {
          await api.v1.deliveryPatchMilestone(msId, { run_state: "stopped" });
          await this.reload();
          this.render();
        } catch (e) {
          this.showNotice(e.message || String(e), "Stop failed");
        }
      });
    });
    el.querySelectorAll("[data-ms-submit-test]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-ms-submit-test");
        btn.disabled = true;
        try {
          await api.v1.deliveryPatchMilestone(msId, { submit_test: true });
          await this.reload();
          this.render();
          this.showNotice("Milestone moved to Test queue.", "提交测试");
        } catch (e) {
          this.showNotice(e.message || String(e), "提交测试失败");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-ms-polish]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.openPolishModal({
          engId: btn.getAttribute("data-eng"),
          major: Number(btn.getAttribute("data-major")),
          minor: Number(btn.getAttribute("data-minor")),
          msTitle: btn.getAttribute("data-title") || "",
        });
      });
    });
    el.querySelectorAll("[data-polish-run]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const id = btn.getAttribute("data-polish-run");
        btn.disabled = true;
        try {
          const res = await api.v1.deliveryRunPolish(id);
          if (res?.job_id) {
            await this.awaitDeliveryJob(res.job_id);
          }
          await this.reload();
          this.render();
          this.showNotice("Polish run finished.", "Polish");
        } catch (e) {
          this.showNotice(e.message || String(e), "Polish run failed");
          btn.disabled = false;
        }
      });
    });
  },

  openPolishModal({ engId, major, minor, msTitle }) {
    const key = `polish:${engId}:${major}.${minor}`;
    const chat = this.polishChat(key, {
      id: key,
      title: "AI assist",
      emptyHint: "Describe what to polish; mock AI appends a draft into the note.",
      tall: false,
      rows: 2,
      allowDrop: false,
      onSend: async ({ composed }, c) => {
        const suggestion =
          "(mock polish) Clarify acceptance: " +
          String(composed || "").trim().slice(0, 280);
        c.appendMessage("ai", suggestion);
        const modal = this.uiModal();
        const bodyEl = modal?.querySelector("#pol-body");
        const titleEl = modal?.querySelector("#pol-title");
        if (bodyEl) {
          const cur = bodyEl.value.trim();
          bodyEl.value = cur ? cur + "\n" + suggestion : suggestion;
        }
        if (titleEl && !titleEl.value.trim()) {
          titleEl.value = "Polish " + (msTitle || `${major}.${minor}`);
        }
      },
    });
    this.showUiModal(`
      <div class="modal-card del-polish-modal">
        <h3 style="margin-top:0">Polish · ${this.esc(msTitle || `${major}.${minor}`)}</h3>
        <p class="muted" style="margin-top:0">Saved into the requirement Version and listed on the milestone card.</p>
        <label>Title</label>
        <input id="pol-title" placeholder="Short polish title" value="Polish ${this.esc(msTitle || `${major}.${minor}`)}" />
        <label>Polish note</label>
        <textarea id="pol-body" rows="5" placeholder="What to polish in requirements…"></textarea>
        <div id="pol-chat-host" class="del-polish-chat">${chat.render()}</div>
        <div class="err" id="pol-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="pol-save">Save polish</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    chat.mount(modal);
    modal.querySelector("#pol-save").onclick = async () => {
      const err = modal.querySelector("#pol-err");
      if (err) err.textContent = "";
      const title = (modal.querySelector("#pol-title")?.value || "").trim();
      const body = (modal.querySelector("#pol-body")?.value || "").trim();
      if (!title && !body) {
        if (err) err.textContent = "Add a title or polish note.";
        return;
      }
      try {
        await api.v1.deliveryCreatePolish({
          eng_req_id: engId,
          major,
          minor,
          title: title || body.slice(0, 48),
          body,
        });
        this.closeUiModal();
        if (this._chats) delete this._chats[key];
        await this.reload();
        this.render();
      } catch (e) {
        if (err) err.textContent = e.message || String(e);
      }
    };
  },

  /** Flatten cluster inventory workers for Roadmap Execute picker. */
  async loadExecuteWorkers() {
    if (this._execWorkersLoading || this._execWorkersLoaded) return;
    this._execWorkersLoading = true;
    try {
      const data = await api.v1.clusters();
      const out = [];
      const seen = new Set();
      for (const f of data.clusters || []) {
        for (const n of f.nodes || []) {
          for (const w of n.workers || []) {
            if (!w?.id || seen.has(w.id)) continue;
            seen.add(w.id);
            out.push({
              id: w.id,
              label: w.git_branch || w.name || w.project_rel || w.id,
              fleet: f.name || f.id || "",
              node: n.name || n.id || "",
              project_rel: (w.project_rel || w.project_path || "").replace(
                /^\/+|\/+$/g,
                "",
              ),
            });
          }
        }
      }
      this._execWorkers = out;
      this._execWorkersLoaded = true;
      if (this.el && this.parseRoute().section === "roadmap") {
        this.render();
      }
    } catch (_) {
      this._execWorkers = this._execWorkers || [];
      this._execWorkersLoaded = true;
    }
    this._execWorkersLoading = false;
  },

  /** Worker choices for Execute: inventory preferred, then snapshot workers. */
  executeWorkerChoices(eng) {
    const byId = new Map();
    const add = (w) => {
      if (!w?.id || String(w.id).startsWith("proj:")) return;
      if (byId.has(w.id)) return;
      byId.set(w.id, {
        id: w.id,
        label: w.label || w.id,
        fleet: w.fleet || "",
        node: w.node || "",
        project_rel: (w.project_rel || "").replace(/^\/+|\/+$/g, ""),
      });
    };
    for (const w of this._execWorkers || []) add(w);
    for (const w of this.data.workers || []) add(w);
    let list = [...byId.values()];
    const prefer =
      eng &&
      (eng.project_key || eng.project_rel || "").replace(/^\/+|\/+$/g, "");
    if (prefer) {
      const matched = list.filter(
        (w) =>
          w.project_rel === prefer ||
          w.project_rel.endsWith("/" + prefer) ||
          prefer.endsWith("/" + w.project_rel),
      );
      const rest = list.filter((w) => !matched.includes(w));
      list = matched.concat(rest);
    }
    return list;
  },

  milestoneLifecycle(eng, major, minor) {
    if (!eng) return "";
    const vers = (eng.versions || []).filter(
      (v) => v.major === major && v.minor === minor,
    );
    if (!vers.length) return "";
    return vers[0].status || "";
  },

  /** Pointer-drag milestone cards; persist x/y; click (no drag) selects. */
  bindRoadmapCanvas(el) {
    const canvas = el.querySelector(".del-canvas");
    const nodesLayer = el.querySelector(".del-nodes");
    const svg = el.querySelector(".del-edges");
    if (!canvas || !nodesLayer) return;

    const NODE_W = 216;
    const NODE_H = 78;
    const DRAG_THRESH = 5;

    const syncLocalPos = (id, x, y) => {
      const list = this.data.roadmap?.milestones || [];
      const m = list.find((n) => n.id === id);
      if (m) {
        m.x = x;
        m.y = y;
      }
    };

    const visibleIds = new Set(
      [...el.querySelectorAll(".del-node[data-ms]")].map((n) =>
        n.getAttribute("data-ms"),
      ),
    );
    const routeMaj = el.querySelector(".del-roadmap-graph")?.getAttribute("data-major");

    const redrawEdges = () => {
      if (!svg) return;
      const milestones = (this.data.roadmap?.milestones || []).filter((m) =>
        visibleIds.has(m.id),
      );
      const edgesList = this.data.roadmap?.edges || [];
      const paths = edgesList
        .map((e) => {
          if (!visibleIds.has(e.from) || !visibleIds.has(e.to)) return "";
          const a = milestones.find((m) => m.id === e.from);
          const b = milestones.find((m) => m.id === e.to);
          if (!a || !b) return "";
          const x1 = (a.x || 0) + NODE_W;
          const y1 = (a.y || 0) + NODE_H / 2;
          const x2 = b.x || 0;
          const y2 = (b.y || 0) + NODE_H / 2;
          const mx = (x1 + x2) / 2;
          return `<path d="M ${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}"
            class="del-edge" fill="none" marker-end="url(#del-arrow)" />`;
        })
        .join("");
      let maxX = 640;
      let maxY = 280;
      for (const m of milestones) {
        maxX = Math.max(maxX, (m.x || 0) + NODE_W + 48);
        maxY = Math.max(maxY, (m.y || 0) + NODE_H + 48);
      }
      const canvasW = Math.max(640, Math.ceil(maxX));
      const canvasH = Math.max(300, Math.ceil(maxY));
      svg.setAttribute("width", String(canvasW));
      svg.setAttribute("height", String(canvasH));
      nodesLayer.style.width = canvasW + "px";
      nodesLayer.style.height = canvasH + "px";
      const defs = svg.querySelector("defs");
      svg.innerHTML = "";
      if (defs) svg.appendChild(defs);
      else {
        svg.insertAdjacentHTML(
          "afterbegin",
          `<defs>
            <marker id="del-arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto">
              <path d="M0,0 L9,3 L0,6 Z" fill="var(--accent)" opacity="0.75" />
            </marker>
          </defs>`,
        );
      }
      svg.insertAdjacentHTML("beforeend", paths);
    };

    el.querySelectorAll(".del-node[data-ms]").forEach((node) => {
      let dragging = false;
      let moved = false;
      let startX = 0;
      let startY = 0;
      let origLeft = 0;
      let origTop = 0;
      let pointerId = null;

      const onMove = (ev) => {
        if (!dragging || ev.pointerId !== pointerId) return;
        const dx = ev.clientX - startX;
        const dy = ev.clientY - startY;
        if (!moved && Math.hypot(dx, dy) < DRAG_THRESH) return;
        moved = true;
        node.classList.add("dragging");
        const x = Math.max(0, Math.round(origLeft + dx));
        const y = Math.max(0, Math.round(origTop + dy));
        node.style.left = x + "px";
        node.style.top = y + "px";
        const id = node.getAttribute("data-ms");
        syncLocalPos(id, x, y);
        redrawEdges();
      };

      const onUp = async (ev) => {
        if (ev.pointerId !== pointerId) return;
        dragging = false;
        pointerId = null;
        node.releasePointerCapture?.(ev.pointerId);
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
        window.removeEventListener("pointercancel", onUp);
        node.classList.remove("dragging");
        const id = node.getAttribute("data-ms");
        if (!moved) {
          this.focusMilestoneCard(el, id);
          return;
        }
        const x = parseFloat(node.style.left) || 0;
        const y = parseFloat(node.style.top) || 0;
        try {
          const saved = await api.v1.deliveryPatchMilestonePos(id, x, y);
          syncLocalPos(id, saved.x, saved.y);
          node.style.left = saved.x + "px";
          node.style.top = saved.y + "px";
          redrawEdges();
        } catch (e) {
          this.showNotice(e.message || String(e), "Could not save position");
          // Revert from server snapshot
          await this.reload();
          this.render();
        }
      };

      node.addEventListener("pointerdown", (ev) => {
        if (ev.button !== 0) return;
        // Allow text selection / links inside if any later — only drag from card.
        ev.preventDefault();
        dragging = true;
        moved = false;
        pointerId = ev.pointerId;
        startX = ev.clientX;
        startY = ev.clientY;
        origLeft = parseFloat(node.style.left) || 0;
        origTop = parseFloat(node.style.top) || 0;
        try {
          node.setPointerCapture(ev.pointerId);
        } catch (_) {}
        window.addEventListener("pointermove", onMove);
        window.addEventListener("pointerup", onUp);
        window.addEventListener("pointercancel", onUp);
      });
    });
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

  /** Shared generation cards. mode "index" opens section/m/{M}; "filter" selects on Project home. */
  majorCardsHtml(mode, section) {
    const majors = this.data.majors || [];
    const lastMajor = majors.length
      ? majors.reduce((a, m) => Math.max(a, m.major), majors[0].major)
      : null;
    return majors
      .map((m) => {
        const sel =
          mode === "filter" && this.selectedMajor === m.major
            ? " del-major-card-selected"
            : "";
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
          const prevLabel = this.majorLabel(m.major - 1);
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
          actions.push(
            `<button type="button" class="ghost danger del-major-del" data-major-delete="${m.major}">Delete</button>`,
          );
        }
        const openAttr =
          mode === "index"
            ? `data-major-open="${m.major}" data-section="${this.esc(section)}"`
            : `data-major-select="${m.major}"`;
        const enterLabel =
          mode === "index"
            ? section === "designs"
              ? "Open Designs"
              : section === "roadmap"
                ? "Open Roadmap"
                : section === "test"
                  ? "Open Test"
                  : "Open Requirements"
            : "Select generation";
        const counts = this.majorWorkCounts(m.major);
        const countLine =
          mode === "index"
            ? `<div class="muted del-major-counts">${counts.milestones} milestone${counts.milestones === 1 ? "" : "s"} · ${counts.designs} design${counts.designs === 1 ? "" : "s"}</div>`
            : "";
        return `
      <div class="del-major-card${sel}">
        <div class="del-major-card-head">
          <button type="button" class="del-major-hit" ${openAttr} title="${this.esc(enterLabel)}">
            <div class="del-major-num">${this.esc(this.majorLabel(m.major))}</div>
            <div class="del-major-title">${this.esc(m.title || "Untitled")}</div>
            ${countLine}
          </button>
          <button type="button" class="del-enter-btn" ${openAttr}
            title="${this.esc(enterLabel)}" aria-label="${this.esc(enterLabel)}">→</button>
        </div>
        <div class="del-major-status-row">
          <span class="badge ${stCls}">${this.esc(this.majorStatusLabel(m.status))}</span>
        </div>
        <p class="muted del-major-sum">${this.esc(m.summary || "")}</p>
        <div class="del-major-actions">${actions.join("") || `<span class="muted del-major-final">Sealed</span>`}</div>
      </div>`;
      })
      .join("");
  },

  majorWorkCounts(major) {
    let milestones = 0;
    const seen = new Set();
    for (const e of this.data.eng_reqs || []) {
      for (const v of e.versions || []) {
        if (v.major !== major) continue;
        const k = `${e.id}:${v.major}.${v.minor}`;
        if (seen.has(k)) continue;
        seen.add(k);
        milestones++;
      }
    }
    const designs = (this.data.designs || []).filter((d) => d.major === major)
      .length;
    return { milestones, designs };
  },

  /** Generation plan editor + optional “open section under this major”. */
  viewMajorIndex(section) {
    const trashN = this.data.trash_count || 0;
    const secLabel =
      section === "designs"
        ? "Designs"
        : section === "roadmap"
          ? "Roadmap"
          : section === "test"
            ? "Test"
            : "Requirements";
    const openHint = `Edit / Start / Seal manage the line · use → to enter ${secLabel} for that generation`;
    return `
      <div class="del-major-plan">
        <div class="del-major-plan-head">
          <h3 style="margin:0">Generation plan</h3>
          <span class="muted">major line · e.g. 0.x → 1.0</span>
          <button type="button" class="ghost" id="del-trash-open" style="margin-left:auto">
            Trash${trashN ? ` (${trashN})` : ""}
          </button>
        </div>
        <p class="muted del-major-open-hint">${this.esc(openHint)}</p>
        <div class="del-major-grid">
          ${this.majorCardsHtml("index", section)}
          <button type="button" class="del-major-card del-major-card-add" id="del-major-add" title="Add major">
            <span class="del-req-plus">+</span>
            <span class="muted">Add major</span>
          </button>
        </div>
      </div>`;
  },

  /** Generation plan on Project home — select filters milestones; Edit/Start/Seal unchanged. */
  viewMajorPlan() {
    const trashN = this.data.trash_count || 0;
    return `
      <div class="del-major-plan">
        <div class="del-major-plan-head">
          <h3 style="margin:0">Generation plan</h3>
          <span class="muted">major line · e.g. 0.x → 1.0 · select to filter milestones</span>
          <button type="button" class="ghost" id="del-trash-open" style="margin-left:auto">
            Trash${trashN ? ` (${trashN})` : ""}
          </button>
        </div>
        <div class="del-major-grid">
          ${this.majorCardsHtml("filter", "requirements")}
          <button type="button" class="del-major-card del-major-card-add" id="del-major-add" title="Add major">
            <span class="del-req-plus">+</span>
            <span class="muted">Add major</span>
          </button>
        </div>
      </div>`;
  },

  /** Requirements under one major: projects/engs that have milestones in this generation. */
  viewRequirementsByMajor(major) {
    const label = this.majorLabel(major);
    const plan = (this.data.majors || []).find((m) => m.major === major);
    const rows = [];
    for (const eng of this.data.eng_reqs || []) {
      const groups = {};
      for (const v of eng.versions || []) {
        if (v.major !== major) continue;
        const k = `${v.major}.${v.minor}`;
        groups[k] = groups[k] || {
          major: v.major,
          minor: v.minor,
          versions: [],
        };
        groups[k].versions.push(v);
      }
      const milestones = Object.values(groups).sort((a, b) => b.minor - a.minor);
      if (!milestones.length) continue;
      const w = this.findWorker(eng.worker_id);
      rows.push({ eng, w, milestones });
    }
    const cards = rows.length
      ? rows
          .map(({ eng, w, milestones }) => {
            const projHref = `#/delivery/requirements/${this.esc(eng.id)}`;
            const projName = eng.title || eng.project_key || w?.label || eng.id;
            const msBits = milestones
              .map((ms) => {
                const head = ms.versions.sort((a, b) => a.patch - b.patch)[0];
                return `<a class="del-rm-major-ms" href="#/delivery/requirements/${this.esc(eng.id)}/${ms.major}/${ms.minor}">
                  <div class="del-rm-major-ms-main">
                    <code>${this.esc(`${ms.major}.${ms.minor}`)}</code>
                    <span>${this.esc(head?.title || "Milestone")}</span>
                    ${head ? this.badge(head.status) : ""}
                  </div>
                  <span class="del-enter-chevron" aria-hidden="true">→</span>
                </a>`;
              })
              .join("");
            return `
        <div class="del-maj-proj-card">
          <div class="del-maj-proj-head">
            <div class="del-maj-proj-id">
              <div class="del-inspector-kicker muted">Project</div>
              <strong class="del-maj-proj-title">${this.esc(projName)}</strong>
              <div class="muted del-req-card-meta">
                <code>${this.esc(eng.project_key || w?.project_rel || "—")}</code>
                ${this.badge(eng.status)}
              </div>
            </div>
            <a class="del-enter-btn" href="${projHref}"
              title="Open project home" aria-label="Open project ${this.esc(projName)}">→</a>
          </div>
          <div class="del-maj-proj-ms">
            <div class="del-maj-proj-ms-head muted">
              Milestones in ${this.esc(label)}
              <span>· jump straight into one</span>
            </div>
            <div class="del-rm-major-ms-list">${msBits}</div>
          </div>
        </div>`;
          })
          .join("")
      : `<div class="del-canvas-empty" style="min-height:10rem">
           <strong>No milestones in ${this.esc(label)} yet</strong>
           <p class="muted">Open a Project and add a milestone under this generation, or create Requirements from the project picker.</p>
         </div>`;

    return `
      <p class="del-crumbs">
        <a href="#/delivery/requirements">Generations</a>
        <span class="muted">/</span>
        <span>${this.esc(label)}</span>
      </p>
      ${
        plan
          ? `<p class="muted" style="margin:0 0 0.75rem">${this.esc(plan.title || "")}${plan.summary ? " — " + this.esc(plan.summary) : ""}</p>`
          : ""
      }
      <div class="toolbar del-rm-list-bar" style="margin-bottom:0.75rem">
        <a class="ghost" href="#/delivery/requirements/projects">Project picker</a>
        <span class="muted" style="flex:1">${rows.length} project${rows.length === 1 ? "" : "s"} with work in ${this.esc(label)}</span>
      </div>
      <p class="muted del-maj-proj-legend">
        Project → opens the Project home (all milestones). Rows below → open that milestone directly.
      </p>
      <div class="del-req-grid">${cards}</div>
      <div id="del-create-modal" class="modal" hidden></div>`;
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

  /** Project picker — also embedded under Requirements home below Generation plan. */
  viewWorkspace() {
    const route = this.parseRoute();
    const embedded =
      route.section === "requirements" &&
      !route.engId &&
      route.routeMajor == null &&
      !route.projectsPicker;
    const crumbs = embedded
      ? `<div class="del-req-section-head" style="margin-top:1.25rem">
           <h3 style="margin:0">Projects</h3>
           <span class="muted">open a Project to author milestones</span>
         </div>`
      : `<p class="del-crumbs">
           <a href="#/delivery/requirements">Requirements</a>
           <span class="muted">/</span>
           <span>Projects</span>
         </p>`;
    if (!this._clustersLoaded) {
      return `${crumbs}<p class="muted">Loading projects…</p>
        <div id="del-create-modal" class="modal" hidden></div>`;
    }
    if (this._clustersError) {
      return `${crumbs}<p class="err">${this.esc(this._clustersError)}</p>
        <p class="muted">Open Clusters to check connectivity, then refresh.</p>
        <div id="del-create-modal" class="modal" hidden></div>`;
    }
    const catalog = this._projectCatalog || [];
    const filter = String(this._projFilter || "").trim().toLowerCase();
    const filterBox = `
      <div class="toolbar del-proj-filter-bar">
        <input id="del-proj-filter" type="search" placeholder="Filter by project name or key…"
          value="${this.esc(this._projFilter || "")}" style="margin:0;max-width:28rem" />
      </div>`;
    if (!catalog.length) {
      return `
        ${crumbs}
        ${filterBox}
        <div class="card">
          <p class="muted">No workspaces yet. Create a Workspace and Project under Clusters, then return here.</p>
        </div>
        <div id="del-create-modal" class="modal" hidden></div>`;
    }
    const sections = catalog
      .map(({ fleet, workspace: ws, projects }) => {
        const filtered = (projects || []).filter((p) => {
          if (!filter) return true;
          const hay = `${p.name || ""} ${p.key || ""} ${p.summary || ""}`.toLowerCase();
          return hay.includes(filter);
        });
        const cards = filtered
          .map((p) => {
            const eng = this.findEngByProject(p.id);
            const vers = eng ? eng.versions || [] : [];
            const ms = eng ? this.latestMilestone(eng) : null;
            const msKey = ms ? `${ms.major}.${ms.minor}` : "—";
            const bound = eng
              ? this.badge(eng.status)
              : `<span class="badge warn">new</span>`;
            return `
      <div class="del-req-card" role="link" tabindex="0" data-proj-open
        data-eng-id="${eng ? this.esc(eng.id) : ""}"
        data-project-id="${this.esc(p.id)}"
        data-workspace-id="${this.esc(ws.id)}"
        data-project-key="${this.esc(p.key || "")}"
        data-project-name="${this.esc(p.name || "")}">
        <div class="del-req-card-top">
          <strong class="del-req-card-title">${this.esc(p.name)}</strong>
          ${bound}
        </div>
        <div class="muted del-req-card-meta">
          <code>${this.esc(p.key || "—")}</code>
          <span>·</span>
          <span>${this.esc(ws.name || ws.id)}</span>
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
        if (filter && !filtered.length) return "";
        const online = fleet.online
          ? `<span class="badge ok">online</span>`
          : `<span class="badge bad">offline</span>`;
        return `
      <section class="del-ws-fleet">
        <div class="del-ws-fleet-head">
          <h3 style="margin:0">${this.esc(ws.name || ws.id)}</h3>
          ${online}
          <span class="muted">${this.esc(fleet.name || fleet.id)}</span>
        </div>
        <div class="del-req-grid">
          ${
            cards ||
            `<p class="muted" style="grid-column:1/-1">No projects in this workspace. Create one under Clusters.</p>`
          }
        </div>
      </section>`;
      })
      .join("");
    return `${crumbs}${filterBox}${sections || `<p class="muted">No projects match “${this.esc(this._projFilter || "")}”.</p>`}
      <div id="del-create-modal" class="modal" hidden></div>`;
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
    const genCrumb =
      this.selectedMajor != null
        ? `<a href="#/delivery/requirements/m/${this.selectedMajor}">${this.esc(this.majorLabel(this.selectedMajor))}</a>
        <span class="muted">/</span>`
        : "";
    return `
      <p class="del-crumbs">
        <a href="#/delivery/requirements">Generations</a>
        <span class="muted">/</span>
        ${genCrumb}
        <span>${this.esc(eng.title || eng.project_key || (w && w.label) || eng.worker_id)}</span>
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
        Milestones for this Project. Click one to open View.
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
        <a href="#/delivery/requirements">Generations</a>
        <span class="muted">/</span>
        <a href="#/delivery/requirements/m/${major}">${this.esc(this.majorLabel(major))}</a>
        <span class="muted">/</span>
        <a href="#/delivery/requirements/${this.esc(eng.id)}">${this.esc(eng.title || (w && w.label) || eng.worker_id)}</a>
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
      <p class="del-crumbs">
        <a href="#/delivery/requirements">Generations</a>
        <span class="muted">/</span>
        <a href="#/delivery/requirements/m/${major}">${this.esc(this.majorLabel(major))}</a>
        <span class="muted">/</span>
        <a href="#/delivery/requirements/${this.esc(eng.id)}">${this.esc(eng.title || (w && w.label) || eng.worker_id)}</a>
        <span class="muted">/</span>
        <a href="#/delivery/requirements/${this.esc(eng.id)}/${major}/${minor}"><code>${major}.${minor}</code></a>
        <span class="muted">/</span>
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

  openDeleteDesignVersionModal(designId, versionId) {
    const d = (this.data.designs || []).find((x) => x.id === designId);
    const ver = (d?.versions || []).find((v) => v.id === versionId);
    if (!d || !ver) return;
    const label = (ver.title || "").trim() || `Version ${ver.sort_order}`;
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Delete Version?</h3>
        <p><strong>${this.esc(label)}</strong>
          · <span class="muted">v${ver.sort_order}</span></p>
        <p class="muted">Remaining Versions will renumber 1…n. Gate / Sub-architecture / Class design for this card are discarded.</p>
        <div class="err" id="des-del-ver-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="des-del-ver-ok">Delete</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    modal.querySelector("#des-del-ver-ok").onclick = async () => {
      const errEl = modal.querySelector("#des-del-ver-err");
      if (errEl) errEl.textContent = "";
      try {
        await api.v1.deliveryDeleteDesignVersion(designId, versionId);
        this.closeUiModal();
        await this.reload();
        this.render();
      } catch (e) {
        if (errEl) errEl.textContent = e.message || String(e);
      }
    };
  },

  openDeleteDesignModal(id) {
    const d = (this.data.designs || []).find((x) => x.id === id);
    if (!d) return;
    const note =
      d.status === "approved"
        ? "This design is approved. Deleting removes Versions and ADR; the bound milestone is not changed."
        : "Removes this design and its Version cards.";
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Delete design?</h3>
        <p><strong>${this.esc(d.title)}</strong>
          · <code>${this.esc(`${d.major}.${d.minor}`)}</code>
          ${this.badge(d.status)}</p>
        <p class="muted">${this.esc(note)}</p>
        <div class="err" id="des-del-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="des-del-ok">Delete</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    modal.querySelector("#des-del-ok").onclick = async () => {
      const errEl = modal.querySelector("#des-del-err");
      if (errEl) errEl.textContent = "";
      try {
        await api.v1.deliveryDeleteDesign(id);
        this.closeUiModal();
        await this.reload();
        this.render();
      } catch (e) {
        if (errEl) errEl.textContent = e.message || String(e);
      }
    };
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
      onSend: async ({ composed }, c) => {
        if (approved) return;
        c.appendMessage(
          "ai",
          "(mock) Noted. Suggested polish applied to ADR.Decision (and cited Version Gate when present).",
        );
        const line = "· (AI mock) " + composed.slice(0, 160);
        const decision = (this.adrFieldValue(d, "adr_decision") || "").trim();
        await api.v1.deliveryPatchDesign(d.id, {
          adr_decision: decision ? decision + "\n" + line : line,
        });
        const citeVer = vers.find(
          (v) =>
            composed.includes("@" + (v.title || "")) ||
            composed.includes(`@V${v.sort_order}.Gate`),
        );
        if (citeVer) {
          await api.v1.deliveryPatchDesignVersion(d.id, citeVer.id, {
            gate: ((citeVer.gate || "").trim() ? citeVer.gate + "\n" : "") + line,
          });
        }
        await this.reload();
        this.render();
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
    const chatHtml = approved
      ? ""
      : chat.render();
    return `
      <p class="del-crumbs">
        <a href="#/delivery/designs">Generations</a>
        <span class="muted">/</span>
        <a href="#/delivery/designs/m/${d.major}">${this.esc(this.majorLabel(d.major))}</a>
        <span class="muted">/</span>
        <span>${this.esc(d.title)}</span>
      </p>
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
  viewRoadmapList(routeMajor) {
    const NODE_W = 216;
    const NODE_H = 78;
    const majLabel = this.majorLabel(routeMajor);
    const allMs = this.data.roadmap?.milestones || [];
    const edgesList = this.data.roadmap?.edges || [];
    const filter = this._rmFilter || "all";
    const rows = allMs
      .map((m) => {
        const bind = this.parseRoadmapMsBind(m.id);
        const eng = bind ? this.findEng(bind.engId) : null;
        const life = bind
          ? this.milestoneLifecycle(eng, bind.major, bind.minor)
          : m.status || "";
        const nPh = (m.phases || []).length;
        const worker =
          this.findWorker(m.execute_worker_id)?.label ||
          m.execute_worker_id ||
          "";
        const project =
          eng?.project_key ||
          eng?.title ||
          (eng ? this.findWorker(eng.worker_id)?.label : "") ||
          "";
        const msKey = bind != null ? `${bind.major}.${bind.minor}` : "";
        return { m, bind, eng, life, nPh, worker, project, msKey };
      })
      .filter((r) => {
        if (!r.bind || r.bind.major !== routeMajor) return false;
        if (filter === "all") return true;
        if (filter === "ready")
          return r.life === "awaiting_development" || r.life === "awaiting_roadmap";
        if (filter === "running") return r.life === "in_development";
        if (filter === "planned")
          return !r.life || r.life === "planned" || r.life === "awaiting_design";
        return true;
      })
      .sort((a, b) => {
        if (a.bind && b.bind && a.bind.minor !== b.bind.minor)
          return a.bind.minor - b.bind.minor;
        const ta = (a.m.title || "").toLowerCase();
        const tb = (b.m.title || "").toLowerCase();
        return ta.localeCompare(tb);
      });

    const idSet = new Set(rows.map((r) => r.m.id));
    let maxX = 640;
    let maxY = 280;
    for (const r of rows) {
      maxX = Math.max(maxX, (r.m.x || 0) + NODE_W + 48);
      maxY = Math.max(maxY, (r.m.y || 0) + NODE_H + 48);
    }
    const canvasW = Math.max(640, Math.ceil(maxX));
    const canvasH = Math.max(300, Math.ceil(maxY));

    const nodes = rows
      .map((r) => {
        const m = r.m;
        const st = (r.life || m.status || "planned").toLowerCase();
        return `<div class="del-node del-node-${this.esc(st)}" role="button" tabindex="0"
          data-ms="${this.esc(m.id)}" style="left:${m.x || 0}px;top:${m.y || 0}px;width:${NODE_W}px"
          title="Drag to arrange · click to focus card controls">
          <span class="del-node-accent" aria-hidden="true"></span>
          <span class="del-node-grip" aria-hidden="true"></span>
          <span class="del-node-title">${this.esc(m.title)}</span>
          <span class="del-node-meta">
            ${this.badge(r.life || m.status)}
            <span class="muted">${r.nPh} phase${r.nPh === 1 ? "" : "s"}</span>
          </span>
        </div>`;
      })
      .join("");

    const edges = edgesList
      .map((e) => {
        if (!idSet.has(e.from) || !idSet.has(e.to)) return "";
        const a = allMs.find((m) => m.id === e.from);
        const b = allMs.find((m) => m.id === e.to);
        if (!a || !b) return "";
        const x1 = (a.x || 0) + NODE_W;
        const y1 = (a.y || 0) + NODE_H / 2;
        const x2 = b.x || 0;
        const y2 = (b.y || 0) + NODE_H / 2;
        const mx = (x1 + x2) / 2;
        return `<path d="M ${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}"
          class="del-edge" fill="none" marker-end="url(#del-arrow)" />`;
      })
      .join("");

    const graphBody = rows.length
      ? `<svg class="del-edges" width="${canvasW}" height="${canvasH}" aria-hidden="true">
            <defs>
              <marker id="del-arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto">
                <path d="M0,0 L9,3 L0,6 Z" fill="var(--accent)" opacity="0.75" />
              </marker>
            </defs>
            ${edges}
          </svg>
          <div class="del-nodes" style="width:${canvasW}px;height:${canvasH}px">${nodes}</div>`
      : `<div class="del-canvas-empty">
           <strong>No milestones in ${this.esc(majLabel)}</strong>
           <p class="muted">Approve a Design in this generation, then <em>Bootstrap roadmap</em> — one node per milestone.</p>
           <a class="ghost" href="#/delivery/designs/m/${routeMajor}">Open Designs · ${this.esc(majLabel)}</a>
         </div>`;

    const cards = rows.length
      ? rows.map((r) => this.milestoneControlCardHtml(r, routeMajor)).join("")
      : "";

    return `
      <p class="del-crumbs">
        <a href="#/delivery/roadmap">Generations</a>
        <span class="muted">/</span>
        <span>${this.esc(majLabel)}</span>
      </p>
      <div class="toolbar del-rm-list-bar">
        <label class="muted" style="margin:0">Status</label>
        <select id="rm-filter" style="width:auto;margin:0">
          <option value="all"${filter === "all" ? " selected" : ""}>All</option>
          <option value="ready"${filter === "ready" ? " selected" : ""}>Awaiting ready / roadmap</option>
          <option value="running"${filter === "running" ? " selected" : ""}>In development</option>
          <option value="planned"${filter === "planned" ? " selected" : ""}>Planned / design</option>
        </select>
        <span class="muted" style="flex:1">${rows.length} milestone${rows.length === 1 ? "" : "s"} in ${this.esc(majLabel)}</span>
        <span class="muted" style="font-size:0.85rem">Graph · drag · controls on cards</span>
      </div>
      <div class="del-roadmap-graph card del-canvas" data-major="${routeMajor}">${graphBody}</div>
      ${
        cards
          ? `<div class="del-req-section-head" style="margin-top:1rem">
               <h3 style="margin:0">Milestones</h3>
               <span class="muted">Worker · Start / Pause / Stop · 提交测试 · Design / Req · Polish</span>
             </div>
             <div class="del-rm-ctrl-grid">${cards}</div>`
          : ""
      }`;
  },

  /** Testing queue under one generation: lifecycle `testing` only. */
  viewTestList(routeMajor) {
    const majLabel = this.majorLabel(routeMajor);
    const allMs = this.data.roadmap?.milestones || [];
    const rows = allMs
      .map((m) => {
        const bind = this.parseRoadmapMsBind(m.id);
        const eng = bind ? this.findEng(bind.engId) : null;
        const life = bind
          ? this.milestoneLifecycle(eng, bind.major, bind.minor)
          : m.status || "";
        const project =
          eng?.project_key ||
          eng?.title ||
          (eng ? this.findWorker(eng.worker_id)?.label : "") ||
          "";
        const worker =
          this.findWorker(m.execute_worker_id)?.label ||
          m.execute_worker_id ||
          "";
        const msKey = bind != null ? `${bind.major}.${bind.minor}` : "";
        return { m, bind, eng, life, project, worker, msKey };
      })
      .filter(
        (r) =>
          r.bind &&
          r.bind.major === routeMajor &&
          (r.life === "testing" || r.m.status === "testing"),
      )
      .sort((a, b) => {
        if (a.bind && b.bind && a.bind.minor !== b.bind.minor)
          return a.bind.minor - b.bind.minor;
        return (a.m.title || "").localeCompare(b.m.title || "");
      });

    const cards = rows.length
      ? rows.map((r) => this.testControlCardHtml(r)).join("")
      : `<div class="del-test-empty card">
           <strong>No milestones in testing</strong>
           <p class="muted">When development is done on Roadmap, use <em>提交测试</em> — then draft cases, run, fix bugs, regress, Pass.</p>
           <a class="ghost" href="#/delivery/roadmap/m/${routeMajor}">Open Roadmap · ${this.esc(majLabel)}</a>
         </div>`;

    return `
      <p class="del-crumbs">
        <a href="#/delivery/test">Generations</a>
        <span class="muted">/</span>
        <span>${this.esc(majLabel)}</span>
      </p>
      <div class="toolbar del-rm-list-bar">
        <span class="muted" style="flex:1">${rows.length} in testing · ${this.esc(majLabel)}</span>
        <a class="ghost" href="#/delivery/roadmap/m/${routeMajor}">Roadmap</a>
      </div>
      <div class="del-test-grid">${cards}</div>`;
  },

  headVersionForBind(eng, major, minor) {
    if (!eng) return null;
    const vers = (eng.versions || []).filter(
      (v) => v.major === major && v.minor === minor,
    );
    if (!vers.length) return null;
    return vers.reduce((a, b) => (a.patch <= b.patch ? a : b));
  },

  buglistForBind(engId, major, minor) {
    return (this.data.buglist_items || []).filter(
      (b) =>
        b.eng_req_id === engId && b.major === major && b.minor === minor,
    );
  },

  testCasesForBind(engId, major, minor) {
    return (this.data.test_cases || []).filter(
      (c) =>
        c.eng_req_id === engId && c.major === major && c.minor === minor,
    );
  },

  campaignPassReady(cases, bugs) {
    if (!cases.length) return false;
    if (cases.some((c) => c.status !== "passed")) return false;
    if (
      bugs.some((b) => b.status === "open" || b.status === "fixing")
    )
      return false;
    return true;
  },

  testControlCardHtml(r) {
    const m = r.m;
    const bind = r.bind;
    const des = this.findDesignForMilestone(m);
    const desHref = des ? `#/delivery/designs/${this.esc(des.id)}` : "";
    const reqHref =
      bind &&
      `#/delivery/requirements/${this.esc(bind.engId)}/${bind.major}/${bind.minor}`;
    const rmHref =
      bind &&
      `#/delivery/roadmap/m/${bind.major}/${encodeURIComponent(m.id)}`;
    const head = this.headVersionForBind(r.eng, bind.major, bind.minor);
    const success = (head?.success || m.acceptance || "").trim();
    const acceptanceBlock = success
      ? `<pre class="del-test-accept">${this.esc(success)}</pre>`
      : `<p class="muted del-test-accept-empty">No acceptance / success text on the Version yet.</p>`;
    const cases = this.testCasesForBind(bind.engId, bind.major, bind.minor);
    const caseRows = cases.length
      ? `<ul class="del-case-list">
          ${cases
            .map(
              (c) => `
            <li class="del-case-row">
              <div class="del-bug-main">
                <span class="del-case-title">${this.esc(c.title || "Case")}</span>
                ${this.badge(c.status)}
              </div>
              ${c.body ? `<p class="muted del-bug-body">${this.esc(c.body)}</p>` : ""}
            </li>`,
            )
            .join("")}
        </ul>`
      : `<p class="muted del-bug-empty">No cases yet — use AI assist to draft.</p>`;
    const bugs = this.buglistForBind(bind.engId, bind.major, bind.minor);
    const hasVerified = bugs.some((b) => b.status === "verified");
    const hasWorker = !!(m.execute_worker_id || "").trim();
    const canRun = hasWorker && cases.length > 0;
    const canPass = this.campaignPassReady(cases, bugs);
    const bugRows = bugs.length
      ? `<ul class="del-bug-list">
          ${bugs
            .map((b) => {
              const canFix = b.status === "open" || b.status === "fixing";
              return `
            <li class="del-bug-row">
              <div class="del-bug-main">
                <span class="del-bug-title">${this.esc(b.title || "Bug")}</span>
                ${this.badge(b.status)}
                ${
                  canFix
                    ? `<button type="button" class="del-icon-btn" data-bug-fix="${this.esc(b.id)}"
                         title="Worker fix" aria-label="Fix">▶</button>`
                    : ""
                }
              </div>
              ${b.body ? `<p class="muted del-bug-body">${this.esc(b.body)}</p>` : ""}
            </li>`;
            })
            .join("")}
        </ul>`
      : `<p class="muted del-bug-empty">No buglist items yet.</p>`;

    const campJob = (m.campaign_job_id || "").trim();
    const campSt = (m.campaign_job_status || "").toLowerCase();
    const campBusy = campSt === "running" || campSt === "queued";
    const campStrip = campBusy
      ? `<div class="del-job-strip" data-campaign-snip>${this.esc(m.campaign_job_kind || "job")} · ${this.esc(campSt)}${m.campaign_job_last_event ? " · " + this.esc(String(m.campaign_job_last_event).slice(0, 120)) : ""}</div>`
      : campSt === "error"
        ? `<div class="del-job-strip muted">${this.esc(m.campaign_job_last_event || "campaign job error")}</div>`
        : "";

    return `
      <div class="del-test-card" data-test-card="${this.esc(m.id)}" data-ms-card="${this.esc(m.id)}"
        data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
        data-campaign-job="${this.esc(campJob)}" data-campaign-job-status="${this.esc(campSt)}">
        <div class="del-rm-ctrl-top">
          <div class="del-rm-ctrl-id">
            <strong class="del-rm-card-title">${this.esc(m.title)}</strong>
            <div class="del-rm-card-meta muted">
              ${r.msKey ? `<code>${this.esc(r.msKey)}</code>` : ""}
              ${r.project ? `<span>· ${this.esc(r.project)}</span>` : ""}
              ${r.worker ? `<span>· Worker ${this.esc(r.worker)}</span>` : `<span class="err">· No Worker</span>`}
            </div>
          </div>
          ${this.badge("testing")}
        </div>
        ${campStrip}
        <div class="del-rm-ctrl-links">
          ${desHref ? `<a href="${desHref}">Design</a>` : `<span class="muted">No design</span>`}
          ${reqHref ? `<a href="${reqHref}">Requirements</a>` : `<span class="muted">No req</span>`}
          ${rmHref ? `<a href="${rmHref}">Roadmap</a>` : ""}
        </div>
        <div class="del-test-accept-block">
          <div class="del-polish-head muted">Acceptance / success</div>
          ${acceptanceBlock}
        </div>
        <label class="muted del-test-note-label">Test note</label>
        <textarea class="del-test-note" data-test-note="${this.esc(m.id)}" rows="2"
          placeholder="Optional note for this test pass…">${this.esc(m.test_note || "")}</textarea>
        <div class="del-case-block">
          <div class="del-campaign-head">
            <span class="del-polish-head muted">Cases</span>
            <button type="button" class="ghost" data-test-ai-cases
              data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
              data-title="${this.esc(m.title)}" data-accept="${this.esc(success.slice(0, 400))}">Draft cases</button>
          </div>
          ${caseRows}
        </div>
        <div class="del-rm-ctrl-btns del-test-actions">
          <button type="button" class="del-run-btn" data-test-run
            data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
            ${canRun && !campBusy ? "" : "disabled"} title="${hasWorker ? "Enqueue Worker test run Job" : "Bind Worker on Roadmap first"}">▶ Run tests</button>
          <button type="button" class="del-run-btn ghost" data-test-regress
            data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
            ${hasVerified && !campBusy ? "" : "disabled"}>↻ Regress</button>
          <button type="button" class="del-run-btn" data-test-pass="${this.esc(m.id)}"
            ${canPass ? "" : "disabled"} title="${canPass ? "Complete milestone" : "Need clean campaign (≥1 case, all passed, no open bugs)"}">✓ Pass</button>
          <button type="button" class="del-run-btn ghost danger" data-test-fail="${this.esc(m.id)}"
            data-title="${this.esc(m.title)}">✗ Fail</button>
        </div>
        <div class="del-bug-block">
          <div class="del-polish-head muted">Buglist</div>
          ${bugRows}
        </div>
      </div>`;
  },

  bindTestCardControls(el) {
    el.querySelectorAll("[data-test-note]").forEach((ta) => {
      let timer = null;
      ta.addEventListener("input", () => {
        const msId = ta.getAttribute("data-test-note");
        clearTimeout(timer);
        timer = setTimeout(async () => {
          try {
            await api.v1.deliveryPatchMilestone(msId, {
              test_note: ta.value || "",
            });
          } catch (e) {
            this.showNotice(e.message || String(e), "Could not save note");
          }
        }, 500);
      });
    });
    el.querySelectorAll("[data-test-ai-cases]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.openTestCasesModal({
          engId: btn.getAttribute("data-eng"),
          major: Number(btn.getAttribute("data-major")),
          minor: Number(btn.getAttribute("data-minor")),
          msTitle: btn.getAttribute("data-title") || "",
          acceptance: btn.getAttribute("data-accept") || "",
        });
      });
    });
    el.querySelectorAll("[data-test-run]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try {
          const res = await api.v1.deliveryRunTestCases({
            eng_req_id: btn.getAttribute("data-eng"),
            major: Number(btn.getAttribute("data-major")),
            minor: Number(btn.getAttribute("data-minor")),
          });
          if (res?.job_id) {
            await this.awaitDeliveryJob(res.job_id);
          }
          await this.reload();
          this.render();
          this.showNotice("Test run Job finished.", "Run tests");
        } catch (e) {
          this.showNotice(e.message || String(e), "Run failed");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-bug-fix]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try {
          const res = await api.v1.deliveryFixBuglist(
            btn.getAttribute("data-bug-fix"),
          );
          if (res?.job_id) {
            await this.awaitDeliveryJob(res.job_id);
          }
          await this.reload();
          this.render();
          this.showNotice("Fix Job finished → verified. Run Regress next.", "Fix");
        } catch (e) {
          this.showNotice(e.message || String(e), "Fix failed");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-test-regress]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try {
          const res = await api.v1.deliveryRegressBuglist({
            eng_req_id: btn.getAttribute("data-eng"),
            major: Number(btn.getAttribute("data-major")),
            minor: Number(btn.getAttribute("data-minor")),
          });
          if (res?.job_id) {
            await this.awaitDeliveryJob(res.job_id);
          }
          await this.reload();
          this.render();
          this.showNotice("Regression Job finished.", "Regress");
        } catch (e) {
          this.showNotice(e.message || String(e), "Regress failed");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-test-pass]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-test-pass");
        const note = el.querySelector(
          `[data-test-note="${msId}"]`,
        )?.value;
        btn.disabled = true;
        try {
          const body = { pass_test: true };
          if (note != null) body.test_note = note;
          await api.v1.deliveryPatchMilestone(msId, body);
          await this.reload();
          this.render();
          this.showNotice("Marked complete — Polish on Roadmap when ready.", "Pass");
        } catch (e) {
          this.showNotice(e.message || String(e), "Pass failed");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-test-fail]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const msId = btn.getAttribute("data-test-fail");
        const msTitle = btn.getAttribute("data-title") || "";
        this.openFailTestModal({ msId, msTitle });
      });
    });
  },

  openTestCasesModal({ engId, major, minor, msTitle, acceptance }) {
    const key = `tcases:${engId}:${major}.${minor}`;
    const chat = this.polishChat(key, {
      id: "tc-cases-chat",
      title: "Chat",
      emptyHint: "Describe coverage to draft. Save turns AI replies into ready cases.",
      tall: false,
      rows: 2,
      allowDrop: false,
      onSend: async ({ composed }, c) => {
        const hint = String(composed || "").trim().slice(0, 200);
        const suggestion =
          `Case: ${hint || "Acceptance check"}\n` +
          `Given acceptance for ${msTitle || `${major}.${minor}`}` +
          (acceptance ? ` — ${acceptance.slice(0, 120)}` : "") +
          `\nWhen exercised\nThen expect pass criteria.`;
        c.appendMessage("ai", suggestion);
      },
    });
    this.showUiModal(`
      <div class="modal-card modal-wide del-cases-modal">
        <h3>Draft cases · ${this.esc(msTitle || `${major}.${minor}`)}</h3>
        <p class="muted del-cases-lead">Use the chat below. Each AI reply becomes one case (or split a reply with <code>---</code>).</p>
        <div id="tc-chat-host" class="del-cases-chat">${chat.render()}</div>
        <div class="err" id="tc-err"></div>
        <div class="toolbar del-cases-footer">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="tc-save">Save cases</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    chat.mount(modal);
    modal.querySelector("#tc-save").onclick = async () => {
      const err = modal.querySelector("#tc-err");
      if (err) err.textContent = "";
      const aiTexts = (chat.messages || [])
        .filter((m) => m.role === "ai" && String(m.text || "").trim())
        .map((m) => String(m.text).trim());
      const parts = [];
      for (const t of aiTexts) {
        const chunks = t.split(/\n---+\n/).map((p) => p.trim()).filter(Boolean);
        if (chunks.length) parts.push(...chunks);
      }
      const cases = parts.map((p, i) => {
        const lines = p.split("\n");
        const first = (lines[0] || "").replace(/^Case:\s*/i, "").trim();
        return {
          title: first || `Case ${i + 1}`,
          body: p,
        };
      });
      if (!cases.length) {
        if (err) err.textContent = "Chat with the assistant first, then Save.";
        return;
      }
      try {
        await api.v1.deliveryCreateTestCases({
          eng_req_id: engId,
          major,
          minor,
          cases,
        });
        this.closeUiModal();
        if (this._chats) delete this._chats[key];
        await this.reload();
        this.render();
        this.showNotice(`Saved ${cases.length} case(s).`, "Cases");
      } catch (e) {
        if (err) err.textContent = e.message || String(e);
      }
    };
  },

  openFailTestModal({ msId, msTitle }) {
    this.showUiModal(`
      <div class="modal-card del-fail-modal">
        <h3 style="margin-top:0">Fail · ${this.esc(msTitle || msId)}</h3>
        <p class="muted" style="margin-top:0">Creates a buglist stub and returns the milestone to awaiting roadmap.</p>
        <label>Title</label>
        <input id="fail-title" placeholder="Short bug title" value="Test fail: ${this.esc(msTitle || "")}" />
        <label>Details</label>
        <textarea id="fail-body" rows="4" placeholder="What failed…"></textarea>
        <div class="err" id="fail-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="fail-save">Fail &amp; return</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    modal.querySelector("#fail-save").onclick = async () => {
      const err = modal.querySelector("#fail-err");
      if (err) err.textContent = "";
      const title = (modal.querySelector("#fail-title")?.value || "").trim();
      const body = (modal.querySelector("#fail-body")?.value || "").trim();
      if (!title && !body) {
        if (err) err.textContent = "Add a title or details.";
        return;
      }
      try {
        await api.v1.deliveryPatchMilestone(msId, {
          fail_test: true,
          fail_title: title || body.slice(0, 48),
          fail_body: body,
        });
        this.closeUiModal();
        await this.reload();
        this.render();
        this.showNotice("Buglist stub created · back on Roadmap as awaiting roadmap.", "Fail");
      } catch (e) {
        if (err) err.textContent = e.message || String(e);
      }
    };
  },

  polishesForBind(engId, major, minor) {
    return (this.data.polishes || []).filter(
      (p) =>
        p.eng_req_id === engId && p.major === major && p.minor === minor,
    );
  },

  focusMilestoneCard(root, msId) {
    const el = root || this.el;
    if (!el || !msId) return;
    const card = [...el.querySelectorAll("[data-ms-card]")].find(
      (c) => c.getAttribute("data-ms-card") === msId,
    );
    if (!card) return;
    card.scrollIntoView({ behavior: "smooth", block: "nearest" });
    card.classList.add("del-rm-card-focus");
    setTimeout(() => card.classList.remove("del-rm-card-focus"), 1600);
  },

  milestoneControlCardHtml(r, routeMajor) {
    const m = r.m;
    const bind = r.bind;
    const run = (m.run_state || "idle").toLowerCase();
    const life = r.life || m.status || "";
    const des = this.findDesignForMilestone(m);
    const desHref = des ? `#/delivery/designs/${this.esc(des.id)}` : "";
    const reqHref =
      bind &&
      `#/delivery/requirements/${this.esc(bind.engId)}/${bind.major}/${bind.minor}`;
    const workerChoices = this.executeWorkerChoices(r.eng);
    const curWorker = (m.execute_worker_id || "").trim();
    const optionParts = [`<option value="">Worker…</option>`];
    if (curWorker && !workerChoices.some((w) => w.id === curWorker)) {
      optionParts.push(
        `<option value="${this.esc(curWorker)}" selected>${this.esc(curWorker)} (saved)</option>`,
      );
    }
    for (const w of workerChoices) {
      const meta = [w.node, w.project_rel].filter(Boolean).join(" · ");
      const label = meta ? `${w.label} · ${meta}` : w.label;
      const sel = w.id === curWorker ? " selected" : "";
      optionParts.push(
        `<option value="${this.esc(w.id)}"${sel}>${this.esc(label)}</option>`,
      );
    }
    const canStart =
      !!curWorker &&
      r.nPh > 0 &&
      (life === "awaiting_development" ||
        life === "in_development" ||
        run === "paused" ||
        run === "stopped" ||
        life === "");
    const canPause = run === "running";
    const canStop = run === "running" || run === "paused";
    const showPolish =
      life === "complete" || life === "testing" || life === "done";
    const canSubmitTest = life === "in_development";
    const polishes =
      bind != null ? this.polishesForBind(bind.engId, bind.major, bind.minor) : [];
    const polishRows = polishes.length
      ? `<ul class="del-polish-list">
          ${polishes
            .map(
              (p) => `
            <li class="del-polish-row">
              <div class="del-polish-main">
                <span class="del-polish-title">${this.esc(p.title || "Polish")}</span>
                ${this.badge(p.status)}
              </div>
              <button type="button" class="del-icon-btn del-polish-run" data-polish-run="${this.esc(p.id)}"
                title="Run polish" aria-label="Run polish" ${p.status === "done" ? "disabled" : ""}>▶</button>
            </li>`,
            )
            .join("")}
        </ul>`
      : showPolish
        ? `<p class="muted del-polish-empty">No polish items yet.</p>`
        : "";
    const phases = m.phases || [];
    const activePh =
      phases.find(
        (p) => p.job_status === "running" || p.job_status === "queued",
      ) || phases.find((p) => p.job_id);
    const jobStrip = activePh
      ? `<div class="del-job-strip" data-job-id="${this.esc(activePh.job_id || "")}">
           <span class="muted">Phase</span>
           <span>${this.esc(activePh.title || "")}</span>
           ${this.badge(activePh.job_status || activePh.status)}
           <span class="muted del-job-snip" data-job-snip>${this.esc(activePh.job_last_event || "")}</span>
         </div>`
      : phases.length
        ? `<div class="del-job-strip muted">${phases.filter((p) => (p.status || "") === "finished" || p.job_status === "finished").length}/${phases.length} phases finished</div>`
        : "";

    return `
      <div class="del-rm-ctrl-card" data-ms-card="${this.esc(m.id)}" id="ms-card-${this.esc(m.id)}"
        data-run-state="${this.esc(run)}" data-active-job="${this.esc(activePh?.job_id || "")}">
        <div class="del-rm-ctrl-top">
          <div class="del-rm-ctrl-id">
            <strong class="del-rm-card-title">${this.esc(m.title)}</strong>
            <div class="del-rm-card-meta muted">
              ${r.msKey ? `<code>${this.esc(r.msKey)}</code>` : ""}
              ${r.project ? `<span>· ${this.esc(r.project)}</span>` : ""}
              <span>· ${r.nPh} phase${r.nPh === 1 ? "" : "s"}</span>
              <span>· run ${this.esc(run)}</span>
            </div>
          </div>
          ${this.badge(life || m.status)}
        </div>
        ${jobStrip}
        <div class="del-rm-ctrl-links">
          ${
            desHref
              ? `<a href="${desHref}">Design</a>`
              : `<span class="muted">No design</span>`
          }
          ${
            reqHref
              ? `<a href="${reqHref}">Requirements</a>`
              : `<span class="muted">No req bind</span>`
          }
        </div>
        <div class="del-rm-ctrl-run">
          <select class="del-ms-worker" data-ms="${this.esc(m.id)}"
            ${workerChoices.length || curWorker ? "" : "disabled"}>
            ${optionParts.join("")}
          </select>
          <div class="del-rm-ctrl-btns">
            <button type="button" class="del-run-btn" data-ms-start="${this.esc(m.id)}"
              title="Start" ${canStart ? "" : "disabled"}>▶ Start</button>
            <button type="button" class="del-run-btn ghost" data-ms-pause="${this.esc(m.id)}"
              title="Pause" ${canPause ? "" : "disabled"}>⏸ Pause</button>
            <button type="button" class="del-run-btn ghost" data-ms-stop="${this.esc(m.id)}"
              title="Stop" ${canStop ? "" : "disabled"}>⏹ Stop</button>
            ${
              canSubmitTest
                ? `<button type="button" class="del-run-btn" data-ms-submit-test="${this.esc(m.id)}"
                     title="Submit to Test">提交测试</button>`
                : ""
            }
            ${
              showPolish && bind
                ? `<button type="button" class="ghost" data-ms-polish
                     data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
                     data-title="${this.esc(m.title)}">Polish</button>`
                : ""
            }
          </div>
        </div>
        ${
          showPolish || polishes.length
            ? `<div class="del-polish-block">
                 <div class="del-polish-head muted">Polish</div>
                 ${polishRows}
               </div>`
            : ""
        }
      </div>`;
  },

  /** Parse bootstrap milestone id `ms_{engId}_{major}_{minor}` when present. */
  parseRoadmapMsBind(msId) {
    const id = String(msId || "");
    if (!id.startsWith("ms_")) return null;
    const rest = id.slice(3);
    const m = rest.match(/^(.*)_(\d+)_(\d+)$/);
    if (!m) return null;
    return { engId: m[1], major: Number(m[2]), minor: Number(m[3]) };
  },

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
  viewRoadmap(selectedId, routeMajor) {
    const milestones = this.data.roadmap?.milestones || [];
    const selected = milestones.find((m) => m.id === selectedId) || null;
    const bindGuess = selected
      ? this.parseRoadmapMsBind(selected.id)
      : this.parseRoadmapMsBind(selectedId);
    const maj =
      routeMajor != null
        ? routeMajor
        : bindGuess
          ? bindGuess.major
          : this.selectedMajor;
    const listHref =
      maj != null
        ? `#/delivery/roadmap/m/${maj}`
        : "#/delivery/roadmap";
    if (!selected) {
      return `
        <div class="toolbar del-roadmap-bar">
          <a class="ghost" href="${listHref}">← Milestones</a>
        </div>
        <div class="del-canvas-empty">
          <strong>Milestone not found</strong>
          <p class="muted">This id is not on the roadmap. Pick one from the list.</p>
          <a class="ghost" href="${listHref}">Milestones</a>
        </div>`;
    }

    const phases = selected.phases || [];
    const phaseList = phases.length
      ? phases
          .map(
            (p, i) => `
        <li class="del-phase-item">
          <span class="del-phase-ord">${i + 1}</span>
          <span class="del-phase-title">${this.esc(p.title)}</span>
          ${this.badge(p.status)}
        </li>`,
          )
          .join("")
      : `<li class="del-phase-empty muted">No phases yet — Bootstrap from Design seeds Design Versions as the phase chain under this milestone.</li>`;

    const boundDes = this.findDesignForMilestone(selected);
    const bind = this.parseRoadmapMsBind(selected.id);
    const eng = bind ? this.findEng(bind.engId) : null;
    const life = bind
      ? this.milestoneLifecycle(eng, bind.major, bind.minor)
      : selected.status || "";
    const msKey = bind ? `${bind.major}.${bind.minor}` : "";
    const reqHref =
      bind &&
      `#/delivery/requirements/${this.esc(bind.engId)}/${bind.major}/${bind.minor}`;
    const desHref = boundDes ? `#/delivery/designs/${this.esc(boundDes.id)}` : "";
    const project =
      eng?.project_key ||
      eng?.title ||
      (eng ? this.findWorker(eng.worker_id)?.label : "") ||
      "";

    const workerChoices = this.executeWorkerChoices(eng);
    const curWorker = (selected.execute_worker_id || "").trim();
    const optionParts = [`<option value="">Select Worker…</option>`];
    if (curWorker && !workerChoices.some((w) => w.id === curWorker)) {
      const orphan = this.findWorker(curWorker);
      const lab = orphan?.label || curWorker;
      optionParts.push(
        `<option value="${this.esc(curWorker)}" selected>${this.esc(lab)} (saved)</option>`,
      );
    }
    for (const w of workerChoices) {
      const meta = [w.node, w.project_rel].filter(Boolean).join(" · ");
      const label = meta ? `${w.label} · ${meta}` : w.label;
      const sel = w.id === curWorker ? " selected" : "";
      optionParts.push(
        `<option value="${this.esc(w.id)}"${sel}>${this.esc(label)}</option>`,
      );
    }
    const workerSelectHtml = optionParts.join("");
    const canExecute =
      !!curWorker &&
      phases.length > 0 &&
      (life === "awaiting_development" ||
        life === "in_development" ||
        life === "");
    const execHint = !phases.length
      ? "Bootstrap phases from Designs before Execute."
      : life === "awaiting_roadmap"
        ? "Mark ready first."
        : life &&
            life !== "awaiting_development" &&
            life !== "in_development"
          ? `Lifecycle is ${this.lifecycleLabel(life)}.`
          : !curWorker
            ? "Pick an execute Worker (checkout)."
            : life === "in_development"
              ? "Already in development — re-bind Worker or Execute again to confirm."
              : "Execute advances this milestone to in development (no Job yet).";

    const markReady =
      life === "awaiting_roadmap" && bind
        ? `<button type="button" data-ms-ready data-eng="${this.esc(bind.engId)}"
             data-major="${bind.major}" data-minor="${bind.minor}">Mark ready</button>`
        : "";

    const majLabel = maj != null ? this.majorLabel(maj) : "";
    return `
      <div class="del-rm-detail">
        <p class="del-crumbs">
          <a href="#/delivery/roadmap">Generations</a>
          <span class="muted">/</span>
          ${
            maj != null
              ? `<a href="${listHref}">${this.esc(majLabel)}</a><span class="muted">/</span>`
              : ""
          }
          <span>${this.esc(selected.title)}</span>
        </p>
        <div class="toolbar del-roadmap-bar">
          <a class="ghost" href="${listHref}">← Milestones</a>
          <span class="muted" style="flex:1;text-align:right;font-size:0.85rem">
            One milestone · Design Versions land as phases
          </span>
        </div>
        <div class="del-rm-detail-grid">
          <section class="card del-rm-detail-main">
            <div class="del-inspector-kicker muted">Milestone${msKey ? ` · ${this.esc(msKey)}` : ""}</div>
            <h2 class="del-rm-detail-title">${this.esc(selected.title)}</h2>
            <div class="del-inspector-badges">${this.badge(life || selected.status)}</div>
            ${
              project
                ? `<p class="muted del-rm-detail-project">Project · ${this.esc(project)}</p>`
                : ""
            }
            ${
              selected.acceptance
                ? `<p class="del-inspector-accept">${this.esc(selected.acceptance)}</p>`
                : ""
            }
            <div class="del-inspector-links">
              ${
                desHref
                  ? `<a href="${desHref}">Open design</a>`
                  : `<span class="muted">No bound design</span>`
              }
              ${
                reqHref
                  ? `<a href="${reqHref}">Requirements ${this.esc(msKey)}</a>`
                  : ""
              }
            </div>
            ${
              markReady
                ? `<div class="del-ready-block">${markReady}
                     <p class="muted del-ready-hint">Unlocks Execute for this milestone.</p>
                   </div>`
                : ""
            }
            <div class="del-inspector-sec">
              <div class="del-inspector-sec-head">
                <h4>Phase chain</h4>
                <span class="muted">${phases.length} · from Design Versions</span>
              </div>
              <ol class="del-phases">${phaseList}</ol>
            </div>
          </section>
          <aside class="card del-inspector del-rm-detail-side">
            <div class="del-exec-block" style="border:none;margin:0;padding:0">
              <div class="del-inspector-sec-head">
                <h4>Execute</h4>
                ${life ? this.badge(life) : `<span class="muted">—</span>`}
              </div>
              <label class="del-exec-label muted" for="del-exec-worker">Worker</label>
              <select id="del-exec-worker" data-ms="${this.esc(selected.id)}"
                ${workerChoices.length || curWorker ? "" : "disabled"}>
                ${workerSelectHtml}
              </select>
              <p class="muted del-exec-hint">${this.esc(execHint)}</p>
              <button type="button" id="del-exec-run" data-ms="${this.esc(selected.id)}"
                ${canExecute ? "" : "disabled"}>Execute</button>
            </div>
          </aside>
        </div>
      </div>`;
  },
};
