// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Delivery core: mounting, routing, data loading.
 *
 * Part of the Delivery feature object: assigns onto `window.Features.delivery`.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  _renderToken: 0,

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

  beginAction() {
    return ++this._renderToken;
  },

  onRoute() {
    // A route change makes every in-flight action's result stale by definition:
    // bump the generation so a late response cannot paint the new route.
    this._renderToken++;
    if (!this.el || !this.data) return;
    this.render();
  },

  unmount() {
    this.el = null;
  },

  /**
   * render paints the current route.
   *
   * `token` is the value `beginAction()` handed out when the work that led here
   * started. A response that settles after the operator has navigated carries a
   * stale token and paints nothing — otherwise a slow mutation repaints whatever
   * route happens to be current, with data that belongs to the route it left.
   * Route-driven renders pass no token and always paint.
   */
  render(token) {
    if (!this.el) return;
    if (token != null && token !== this._renderToken) return;
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
        // Home is one component, not two stacked ones: the generation switcher
        // chooses the line, and Projects has its own entry
        // (#/delivery/requirements/projects).
        body = this.viewMajorIndex("requirements");
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

    const section = route.section || "requirements";
    const scopeMajor = route.routeMajor != null ? route.routeMajor : route.major;
    const scope =
      route.section === "requirements" && route.engId
        ? ""
        : this.generationScopeHtml(section, scopeMajor);

    this.el.innerHTML = `
      <div class="del-head">
        <div>
          <h2>${this.esc(metaTitle)}</h2>
          <p class="muted del-sub">${this.esc(metaSub)}</p>
        </div>
      </div>
      ${scope}
      ${this.jobBarHtml()}
      <div class="del-body">${body}</div>
      <div id="del-ui-modal" class="modal" hidden></div>`;

    this.el.querySelector("#del-req-add")?.addEventListener("click", () => {
      this.openNewMilestoneEntry();
    });
    this.el.querySelector("#del-ms-add-bound")?.addEventListener("click", () => {
      const engId = this.el.querySelector("#del-ms-add-bound")?.getAttribute("data-eng");
      if (engId) this.openCreateWizardBound(engId);
    });
    this.bindJobBar();
    this.bindGenerationScope();
    this.bindMajorPlan();
    this.bindWorkspaceCards();
    this.bindMilestoneCards();
    this.bindVerTips();
    this.bind(route);
    if (!this._activeJob) {
      const recovered = this.activeJobFromSnapshot();
      if (recovered) {
        this._activeJob = recovered;
        this.paintJobBar();
      }
    }
    if (
      isWorkspace ||
      reqHome ||
      (route.section === "requirements" && route.routeMajor != null)
    ) {
      this.loadWorkspaceInventory();
    }
  },

  /** Fixed-position version tips so .main overflow does not clip them. */
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

  _clearSurfaceState() {
    for (const map of [this._citeBySurface, this._msgsBySurface, this._chats]) {
      if (map && typeof map === "object") {
        for (const k of Object.keys(map)) delete map[k];
      }
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
          this.showNotice(e.message || e, "Error");
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
        this.showNotice("major / minor / patch must be numbers", "Invalid version");
        return;
      }
      try {
        await api.v1.deliveryAddVersion(eng.id, { major, minor, patch, title });
        await this.reload();
        this.goReq(eng.id, major, minor, patch);
      } catch (e) {
        this.showNotice(e.message || e, "Error");
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

  reqCrumbs(engId, major, minor, patch) {
    const eng = this.findEng(engId);
    const w = eng ? this.findWorker(eng.worker_id) : null;
    const root = "#/delivery/requirements";
    const items = [
      { label: "Requirements", href: root },
      { label: eng?.project_key || eng?.title || w?.label || engId, href: `${root}/${engId}` },
    ];
    if (major != null && minor != null) {
      const ms = `${major}.${minor}`;
      items.push({
        label: ms,
        href: patch != null ? `${root}/${engId}/${major}/${minor}` : null,
      });
    }
    if (patch != null) items.push({ label: `${major}.${minor}.${patch}`, href: null });
    return this.breadcrumbHtml(items);
  },
});
