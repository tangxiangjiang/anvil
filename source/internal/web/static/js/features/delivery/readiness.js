// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  viewReadiness() {
    const project = this._readinessProject || "";
    const generation = this._readinessMajor == null ? "" : String(this._readinessMajor);
    const milestones = (this.data.roadmap?.milestones || []).filter((m) => {
      const b = this.parseRoadmapMsBind(m.id);
      return b && (!project || b.engId === project) &&
        (generation === "" || String(b.major) === generation);
    });
    const options = (items, selected, value, label) => items.map((x) => {
      const v = String(value(x));
      return `<option value="${this.esc(v)}"${v === String(selected) ? " selected" : ""}>${this.esc(label(x))}</option>`;
    }).join("");
    return `
      <section class="del-readiness" aria-labelledby="del-readiness-title">
        <div class="del-readiness-toolbar">
          <label>Project
            <select id="readiness-project">
              <option value="">All Projects</option>
              ${options(this.data.eng_reqs || [], project, (e) => e.id, (e) => e.title || e.project_key || e.id)}
            </select>
          </label>
          <label>Generation
            <select id="readiness-generation">
              <option value="">All Generations</option>
              ${options(this.data.majors || [], generation, (m) => m.major, (m) => this.majorLabel(m.major))}
            </select>
          </label>
          <label>Milestone
            <select id="readiness-milestone">
              <option value="">All Milestones</option>
              ${options(milestones, this._readinessMilestone || "", (m) => m.id, (m) => m.title || m.id)}
            </select>
          </label>
          <button type="button" id="readiness-refresh">Refresh</button>
          <span id="readiness-refresh-state" class="muted" role="status" aria-live="polite">Not checked</span>
        </div>
        <div id="readiness-results"><p class="muted">Refresh to ask the server for authoritative readiness.</p></div>
      </section>`;
  },

  bindReadiness(el) {
    const project = el.querySelector("#readiness-project");
    const generation = el.querySelector("#readiness-generation");
    const milestone = el.querySelector("#readiness-milestone");
    const refresh = el.querySelector("#readiness-refresh");
    const rerender = () => {
      this._readinessProject = project?.value || "";
      this._readinessMajor = generation?.value === "" ? null : Number(generation.value);
      this._readinessMilestone = "";
      this.render();
    };
    project?.addEventListener("change", rerender);
    generation?.addEventListener("change", rerender);
    milestone?.addEventListener("change", () => {
      this._readinessMilestone = milestone.value || "";
      void this.loadReadiness();
    });
    refresh?.addEventListener("click", () => this.loadReadiness());
    void this.loadReadiness();
  },

  async loadReadiness() {
    const host = this.el?.querySelector("#readiness-results");
    const state = this.el?.querySelector("#readiness-refresh-state");
    const button = this.el?.querySelector("#readiness-refresh");
    if (!host || !state) return;
    const request = (this._readinessRequest || 0) + 1;
    this._readinessRequest = request;
    state.textContent = "Refreshing…";
    if (button) button.disabled = true;
    try {
      const report = await api.v1.deliveryReadiness({
        project: this._readinessProject || "",
        generation: this._readinessMajor == null ? "" : String(this._readinessMajor),
        milestone: this._readinessMilestone || "",
      });
      if (request !== this._readinessRequest || !this.el?.querySelector("#readiness-results")) return;
      host.innerHTML = this.readinessReportHtml(report);
      state.textContent = "Refreshed";
    } catch (e) {
      if (request !== this._readinessRequest) return;
      host.innerHTML = `<p class="err">Readiness failed: ${this.esc(e.message || e)}</p>`;
      state.textContent = "Refresh failed";
    } finally {
      if (request === this._readinessRequest && button) button.disabled = false;
    }
  },

  readinessReportHtml(report) {
    const rows = report?.rows || [];
    const findings = report?.consistency?.findings || [];
    const summary = report?.consistency?.summary || {};
    const blockers = (list) => (list || []).map((b) => `
      <li class="${b.severity === "warning" ? "warning" : "blocking-error"}">
        <code>${this.esc(b.code)}</code> ${this.esc(b.message)}
        ${b.href ? `<a href="${this.esc(b.href)}">Fix</a>` : ""}
      </li>`).join("");
    const readiness = rows.length ? rows.map((r) => `
      <article class="card del-readiness-row" data-readiness-id="${this.esc(r.id)}">
        <div><span class="badge">${this.esc(r.kind)}</span> <strong>${this.esc(r.title || r.id)}</strong></div>
        <p class="muted">${this.esc(r.lifecycle)} → <code>${this.esc(r.transition)}</code></p>
        ${r.ok ? `<p class="ok">Ready</p>` : `<ul class="del-finding-list">${blockers(r.blockers)}</ul>`}
        <a href="${this.esc(r.href)}">Open record</a>
      </article>`).join("") : `<p class="muted">No non-terminal records in this scope.</p>`;
    const diagnostic = findings.map((f) => `
      <li class="${this.esc(f.severity)}" data-finding-code="${this.esc(f.code)}">
        <strong>${this.esc(f.severity.replace("_", " "))}</strong>
        <code>${this.esc(f.code)}</code> ${this.esc(f.message)}
        ${f.href ? `<a href="${this.esc(f.href)}">Fix</a>` : ""}
      </li>`).join("");
    return `
      <h3>Transition readiness</h3>
      <div class="del-readiness-grid">${readiness}</div>
      <h3>Whole-record consistency</h3>
      <p class="muted">${Number(summary.blocking_errors || 0)} blocking · ${Number(summary.warnings || 0)} warnings · ${Number(summary.informational || 0)} informational</p>
      <ul class="del-finding-list">${diagnostic}</ul>
      ${report?.consistency?.truncated ? `<p class="warning">Results were capped; narrow the scope and refresh.</p>` : ""}`;
  },
});
