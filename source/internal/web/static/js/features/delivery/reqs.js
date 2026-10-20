// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Requirements surface: project home, milestone list, workspace picker.
 *
 * Part of the Delivery feature object: assigns onto `window.Features.delivery`.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
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

  /**
   * The active job, as the sticky bar shows it.
   *
   * Kept on the feature rather than read from the DOM because the bar has to
   * survive a re-render: the SSE handler updates this object and repaints only
   * the bar, so a job stays visible while the operator scrolls a roadmap of any
   * length.
   */
  goReq(engId, major, minor, patch) {
    let h = "#/delivery/requirements";
    if (engId) h += "/" + engId;
    if (major != null && minor != null) {
      h += `/${major}/${minor}`;
      if (patch != null) h += `/${patch}`;
    }
    location.hash = h;
  },

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
          this.showNotice(e.message || e, "Error");
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
        this.showNotice(e.message || e, "Error");
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
        this.showNotice(e.message || e, "Error");
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

  openDeleteMilestoneModal(engId, major, minor) {
    const key = `${major}.${minor}`;
    const eng = this.findEng(engId);
    const des = (this.data.designs || []).find(
      (d) => d.eng_req_id === engId && d.major === major && d.minor === minor,
    );
    const phaseCount = (des && des.phases ? des.phases.length : 0);
    const versionCount = (eng && eng.versions
      ? eng.versions.filter((v) => v.major === major && v.minor === minor).length
      : 0);
    const lines = [
      `Milestone ${key} of ${eng ? eng.title || eng.project_key || eng.worker_id : engId}.`,
      "",
      `This removes ${versionCount} version${versionCount === 1 ? "" : "s"}`
        + (phaseCount ? ` and ${phaseCount} roadmap phase${phaseCount === 1 ? "" : "s"}` : "")
        + ". It cannot be undone.",
      "",
      `Only allowed while generation ${major === 0 ? "0.x" : major + ".x"} is not Sealed.`,
    ];
    window.AnvilUI.modal.requireTyped(this.uiModal(), {
      title: `Delete milestone ${key}?`,
      body: lines.join("\n"),
      word: `DELETE ${key}`,
      confirmLabel: "Delete milestone",
      onConfirm: async () => {
        try {
          await api.v1.deliveryDeleteMilestone(engId, major, minor);
          await this.reload();
          const e2 = this.findEng(engId);
          const next = e2 && this.latestMilestone(e2);
          if (next) this.goReq(engId, next.major, next.minor);
          else location.hash = "#/delivery/requirements";
        } catch (e) {
          this.showNotice(e.message || e, "Delete failed");
        }
      },
    });
  },

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

  /** The requirement walk, as a breadcrumb trail. */
  openDeleteDesignVersionModal(designId, versionId) {
    const d = (this.data.designs || []).find((x) => x.id === designId);
    const ver = (d?.versions || []).find((v) => v.id === versionId);
    if (!d || !ver) return;
    const label = (ver.title || "").trim() || `Version ${ver.sort_order}`;
    const remaining = (d.versions || []).length - 1;
    const lines = [
      `${label}  ·  v${ver.sort_order}`,
      "",
      "Gate, Sub-architecture and Class design for this card are discarded.",
      remaining
        ? `The remaining ${remaining} version${remaining === 1 ? "" : "s"} renumber 1…n.`
        : "This is the design's last version; the design itself stays.",
      "It cannot be undone.",
    ];
    window.AnvilUI.modal.requireTyped(this.uiModal(), {
      title: "Delete design version?",
      body: lines.join("\n"),
      word: "DELETE",
      confirmLabel: "Delete version",
      onConfirm: async () => {
        try {
          await api.v1.deliveryDeleteDesignVersion(designId, versionId);
          await this.reload();
          this.render();
        } catch (e) {
          this.showNotice(e.message || String(e), "Delete failed");
        }
      },
    });
  },

});
