// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Shared view helpers: escaping, badges, generation scope, breadcrumbs, optimistic patch.
 *
 * Part of the Delivery feature object: assigns onto `window.Features.delivery`.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  majorStatusLabel(st) {
    if (st === "sealed") return "Sealed";
    if (st === "developing") return "In development";
    return "Planning";
  },

  developingMajor() {
    const m = (this.data.majors || []).find((x) => x.status === "developing");
    return m ? m.major : null;
  },

  sectionMeta: {
    requirements: {
      title: "Requirements",
      sub: "Generation plan · Project · Milestone (major.minor.patch)",
    },
    designs: {
      title: "Designs",
      sub: "Generation plan · Design · Save → View → Approve → Bootstrap",
    },
    roadmap: {
      title: "Roadmap",
      sub: "Generation plan · Milestone · Worker · Execute · Submit for Test",
    },
    test: {
      title: "Test",
      sub: "Generation plan · Milestones in testing · Pass / Fail",
    },
    readiness: {
      title: "Readiness",
      sub: "Server-authoritative transition blockers and whole-record consistency",
    },
  },

  lifecycleLabel(st) {
    return window.AnvilUI.lifecycleLabel(st);
  },

  goMajor(section, major) {
    location.hash = `#/delivery/${section}/m/${major}`;
  },

  majorLabel(major) {
    return major === 0 ? "0.x" : `${major}.x`;
  },

  /** beginAction claims the render generation for work starting now. */
  go(section, id) {
    let h = "#/delivery/" + section;
    if (id) h += "/" + id;
    location.hash = h;
  },

  // esc / badge / lifecycleLabel are shared with Clusters and Nodes: one
  // definition each, in js/shared/. Local aliases keep the ~500 call sites
  // reading `this.esc(...)` while there is exactly one implementation.
  esc(s) {
    return window.AnvilUI.esc(s);
  },

  badge(status) {
    return window.AnvilUI.badge(status);
  },

  async mount(el) {
    this.el = el;
    if (!this._snapshotCache) {
      this._snapshotCache = window.AnvilUI.swr.createStore({ maxEntries: 2 });
    }
    const cached = this._snapshotCache.peek("delivery:all");
    if (cached) {
      this.data = this.normalizeSnapshot(cached.data);
      this._snapshotState = {
        refreshing: true,
        stale: true,
        updatedAt: cached.updatedAt,
        error: "",
      };
      // Stale content is useful immediately; the network never blocks this
      // paint. The refresh below atomically replaces it when it succeeds.
      this.render();
    }
    // A skeleton, not a line of text: the first paint should have the shape of
    // the list it is about to become, so the swap does not shift layout. This is
    // the component p_0.45.6 introduced; wiring it here is what makes that
    // phase's "a top-level list shows a skeleton rather than a blank body" true.
    if (!cached) {
      el.innerHTML = `
        <div class="del-head"><div><h2>&nbsp;</h2><p class="muted del-sub">&nbsp;</p></div></div>
        ${this.skeletonHtml(4)}`;
    }
    try {
      await this.reload();
      if (this.el !== el) return;
      this.render();
    } catch (e) {
      if (this.el !== el) return;
      if (cached) this.render();
      else {
        el.innerHTML = `<p class="err">Failed to load Delivery: ${this.esc(e.message || e)}
          <button type="button" class="ghost" data-del-initial-retry>Retry</button></p>`;
        el.querySelector("[data-del-initial-retry]")?.addEventListener("click", () => this.mount(el));
      }
    }
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

  uiModal() {
    return this.el?.querySelector("#del-ui-modal");
  },

  // The dialog behaviour (Esc, focus trap, focus restore, backdrop rules) is
  // shared with Clusters. Each feature owns only its host element.
  closeUiModal() {
    window.AnvilUI.modal.close(this.uiModal());
    this._clearSurfaceState();
  },

  /**
   * _clearSurfaceState drops per-modal chat state.
   *
   * It used to clear two hard-coded keys, so a chat opened through any other
   * surface kept its messages into the next modal that reused that surface.
   * Clearing the maps wholesale is what makes "closed" mean closed.
   */
  showUiModal(html, opts) {
    const m = this.uiModal();
    if (!m) return;
    window.AnvilUI.modal.open(m, html, opts || {});
  },

  /** In-app notice — the only path; there is no browser alert in this UI. */
  showNotice(message, title = "Notice") {
    window.AnvilUI.notice(this.uiModal(), message, title);
  },

  prevMajorSealed(major) {
    if (major <= 0) return true;
    const prev = (this.data.majors || []).find((m) => m.major === major - 1);
    return !!(prev && prev.status === "sealed");
  },

  /** Shared generation cards. mode "index" opens section/m/{M}; "filter" selects on Project home. */
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

  /** Generation plan editor + optional “open section under this major”. */  // ---------------------------------------------------------------- scope
  //
  // One generation context, rendered the same way by all four sections. The
  // reason this exists: Requirements home used to concatenate a major index and
  // a workspace picker while the other three sections showed the index alone, so
  // the same product had four different entry experiences and no persistent
  // indicator of which generation the operator was inside.

  /** generationScopeHtml renders the switcher; `section` decides where → goes. */
  generationScopeHtml(section, currentMajor) {
    const majors = this.data.majors || [];
    if (majors.length === 0) return "";
    const active =
      currentMajor != null
        ? currentMajor
        : this.developingMajor() != null
          ? this.developingMajor()
          : this.selectedMajor;
    const chips = majors
      .map((m) => {
        const on = m.major === active;
        const counts = this.majorWorkCounts(m.major);
        const label = this.majorLabel(m.major);
        const cls = "del-scope-chip" + (on ? " active" : "");
        return `<button type="button" class="${cls}"
            data-scope-major="${m.major}" data-scope-section="${this.esc(section)}"
            aria-pressed="${on ? "true" : "false"}"
            title="${this.esc(this.majorStatusLabel(m.status))}">
            <span class="del-scope-label">${this.esc(label)}</span>
            <span class="del-scope-count muted">${this.esc(counts.milestones)} milestones · ${this.esc(counts.designs)} designs</span>
          </button>`;
      })
      .join("");
    return `<div class="del-scope" role="group" aria-label="Generation">
        <span class="muted del-scope-title">Generation</span>
        ${chips}
        <button type="button" class="del-scope-chip ghost" id="del-scope-all"
          data-scope-section="${this.esc(section)}" aria-pressed="${active == null ? "true" : "false"}">All</button>
      </div>`;
  },

  /** bindGenerationScope wires the chips; selecting a major switches section scope. */
  bindGenerationScope() {
    this.el.querySelectorAll("[data-scope-major]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const maj = Number(btn.getAttribute("data-scope-major"));
        const section = btn.getAttribute("data-scope-section") || "requirements";
        this.selectedMajor = maj;
        this._renderToken++;
        this.goMajor(section, maj);
      });
    });
    this.el.querySelector("#del-scope-all")?.addEventListener("click", () => {
      const section = this.el.querySelector("#del-scope-all")?.getAttribute("data-scope-section") || "requirements";
      this.selectedMajor = null;
      this._renderToken++;
      this.go(section);
    });
  },

  /** breadcrumbHtml builds a walk-up trail; the last item is the current node. */
  breadcrumbHtml(items) {
    const parts = [];
    items.forEach((it, i) => {
      const last = i === items.length - 1;
      if (last || !it.href) {
        parts.push(`<span class="del-crumb${last ? "" : " muted"}">${this.esc(it.label)}</span>`);
      } else {
        parts.push(`<a class="del-crumb" href="${this.esc(it.href)}">${this.esc(it.label)}</a>`);
      }
    });
    return `<nav class="del-crumbs" aria-label="Breadcrumb">${parts.join(
      '<span class="muted" aria-hidden="true"> / </span>',
    )}</nav>`;
  },

  /** skeletonHtml is a placeholder sized like the real card, so layout does not shift. */
  skeletonHtml(rows = 3) {
    let out = `<div class="del-skeleton" aria-hidden="true">`;
    for (let i = 0; i < rows; i++) out += `<div class="del-skeleton-row"></div>`;
    out += `</div>`;
    return out;
  },

  /** emptyStateHtml: a list with nothing in it should say what to do next. */
  emptyStateHtml(message, actionLabel, actionTarget) {
    const action = !actionLabel
      ? ""
      : String(actionTarget || "").startsWith("#/")
        ? `<a class="ghost" href="${this.esc(actionTarget)}">${this.esc(actionLabel)}</a>`
        : `<button type="button" id="${this.esc(actionTarget)}">${this.esc(actionLabel)}</button>`;
    return `<div class="del-empty">
        <p class="muted">${this.esc(message)}</p>` + action + `</div>`;
  },

  majorIsSealed(major) {
    const m = (this.data.majors || []).find((x) => x.major === major);
    return !!(m && m.status === "sealed");
  },

  applyLocal(mutator) {
    const snapshot = JSON.parse(JSON.stringify(this.data));
    try {
      mutator(this.data);
    } catch (_) {
      return () => {};
    }
    return () => {
      this.data = snapshot;
      this.render();
    };
  },

  /**
   * patchChecklistItem flips one markdown checkbox in place.
   *
   * The checklist is stored as markdown ("- [ ] do the thing"), so the patch is
   * a splice on the matching line rather than a field write. `index` counts the
   * same way parseGateChecklist does, which is what the buttons carry.
   */
  patchChecklistItem(text, index, done) {
    const lines = String(text || "").split("\n");
    let seen = 0;
    for (let i = 0; i < lines.length; i++) {
      const m = lines[i].match(/^\s*[-*+] \[([ xX])\]/);
      if (!m) continue;
      if (seen === index) {
        lines[i] = lines[i].replace(/\[([ xX])\]/, done ? "[x]" : "[ ]");
        return lines.join("\n");
      }
      seen++;
    }
    return text;
  },
});
