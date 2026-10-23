// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Shell chrome (p_0.45.11): the health strip, the help sheet, and the actionable
 * error dialog.
 *
 * Three things an operator needs that are not about any one surface:
 *
 *   - "is anything wrong right now" (health).
 *   - "which button now, and what does this state mean" (help, per surface).
 *   - "what do I do about this failure" (the actionable error, with the raw
 *     message kept underneath).
 *
 * They live in the shell because they answer questions that cross surfaces.
 */
window.AnvilShell = {
  _helpOpen: false,
  _firstRunSeq: 0,
  firstRunStorageKey: "anvil_web_first_run_v1",

  // ------------------------------------------------------------- first run
  /**
   * mountFirstRun installs a persistent entry point and refreshes completion.
   *
   * Only the collapsed preference is local. Step completion comes from the
   * authenticated server on every route, so another Project or tab cannot make
   * this browser display stale progress.
   */
  mountFirstRun() {
    const foot = document.querySelector(".side-foot");
    if (!foot) return;
    let button = document.getElementById("shell-first-run-open");
    if (!button) {
      button = document.createElement("button");
      button.type = "button";
      button.id = "shell-first-run-open";
      button.className = "ghost shell-first-run-open";
      button.addEventListener("click", () => this.openFirstRun());
      foot.insertBefore(button, foot.firstChild);
    }
    let panel = document.getElementById("shell-first-run");
    if (!panel) {
      panel = document.createElement("section");
      panel.id = "shell-first-run";
      panel.className = "shell-first-run";
      panel.setAttribute("aria-label", "First-run checklist");
      panel.hidden = true;
      foot.parentElement.insertBefore(panel, foot);
    }
    void this.refreshFirstRun();
  },

  firstRunSteps(report) {
    return [
      { key: "admin_created", label: "Create the admin account", href: "#/clusters" },
      { key: "fleet_configured", label: "Configure a Fleet", href: "#/clusters" },
      { key: "project_bound", label: "Bind a Project", href: "#/delivery/requirements/projects" },
      { key: "requirement_created", label: "Create a Requirement", href: "#/delivery/requirements" },
    ].map((step) => ({ ...step, done: !!report?.[step.key] }));
  },

  firstRunDismissed() {
    try {
      return JSON.parse(localStorage.getItem(this.firstRunStorageKey) || "{}").dismissed === true;
    } catch (_) {
      return false;
    }
  },

  setFirstRunDismissed(dismissed) {
    try {
      // No ids, names, paths, credentials or server response are persisted.
      localStorage.setItem(this.firstRunStorageKey, JSON.stringify({ version: 1, dismissed: !!dismissed }));
    } catch (_) {
      // Storage may be disabled; the checklist still works for this page load.
    }
  },

  async refreshFirstRun({ forceOpen = false } = {}) {
    const seq = ++this._firstRunSeq;
    const panel = document.getElementById("shell-first-run");
    const button = document.getElementById("shell-first-run-open");
    if (!panel || !button) return;
    try {
      const report = await api.v1.firstRun();
      if (seq !== this._firstRunSeq) return;
      const steps = this.firstRunSteps(report);
      const complete = steps.filter((step) => step.done).length;
      button.textContent = `Setup checklist ${window.AnvilUI.esc(complete)}/${window.AnvilUI.esc(steps.length)}`;
      button.setAttribute("aria-label", `Open first-run checklist, ${window.AnvilUI.esc(complete)} of ${window.AnvilUI.esc(steps.length)} complete`);
      panel.innerHTML = `
        <div class="shell-first-run-head">
          <strong>First-run checklist</strong>
          <button type="button" class="ghost" data-first-run-close aria-label="Close first-run checklist">×</button>
        </div>
        <p class="muted">Installation progress across all Projects.</p>
        <ol>${steps
          .map(
            (step) => `<li class="${step.done ? "done" : ""}">
              <span aria-hidden="true">${step.done ? "✓" : "○"}</span>
              <a href="${step.href}">${window.AnvilUI.esc(step.label)}</a>
            </li>`,
          )
          .join("")}</ol>
        <div class="sr-only" role="status" aria-live="polite">${window.AnvilUI.esc(complete)} of ${window.AnvilUI.esc(steps.length)} setup steps complete.</div>`;
      panel.querySelector("[data-first-run-close]")?.addEventListener("click", () => this.closeFirstRun());
      panel.querySelectorAll("a").forEach((link) => link.addEventListener("click", () => this.closeFirstRun()));
      panel.hidden = !forceOpen && (steps.every((step) => step.done) || this.firstRunDismissed());
    } catch (_) {
      if (seq !== this._firstRunSeq) return;
      button.textContent = "Setup checklist unavailable";
      button.setAttribute("aria-label", "First-run checklist is temporarily unavailable");
      panel.hidden = true;
    }
  },

  openFirstRun() {
    this.setFirstRunDismissed(false);
    void this.refreshFirstRun({ forceOpen: true });
  },

  closeFirstRun() {
    this.setFirstRunDismissed(true);
    const panel = document.getElementById("shell-first-run");
    if (panel) panel.hidden = true;
    document.getElementById("shell-first-run-open")?.focus();
  },

  // ---------------------------------------------------------------- health
  /**
   * mountHealth renders the strip into the sidebar foot and refreshes it.
   *
   * Deliberately cheap: it asks one endpoint that probes each fleet with its own
   * timeout, and it only repaints the strip. A health check that re-renders the
   * page would be a health check that gets removed.
   */
  mountHealth() {
    let el = document.getElementById("shell-health");
    if (!el) {
      const foot = document.querySelector(".side-foot");
      if (!foot) return;
      el = document.createElement("div");
      el.id = "shell-health";
      el.className = "shell-health";
      el.setAttribute("role", "status");
      el.setAttribute("aria-live", "polite");
      foot.insertBefore(el, foot.firstChild);
    }
    this.refreshHealth();
  },

  async refreshHealth() {
    const el = document.getElementById("shell-health");
    if (!el) return;
    try {
      const h = await api.v1.health();
      const fleets = h.fleets || [];
      if (fleets.length === 0) {
        el.innerHTML = `<span class="sh-dot bad"></span><span class="muted">No Fleet configured</span>` +
          `<span class="muted sh-cap">Jobs: ${window.AnvilUI.esc(Number(h.active_jobs || 0))} active</span>`;
        el.title = "Configure a Fleet in anvil-web, then open Clusters.";
        return;
      }
      const bad = fleets.filter((f) => !f.reachable);
      const dot = bad.length === 0 ? "ok" : bad.length === fleets.length ? "bad" : "warn";
      const idle = fleets.reduce((n, f) => n + (f.idle || 0), 0);
      const busy = fleets.reduce((n, f) => n + (f.busy || 0), 0);
      const activeJobs = Number(h.active_jobs || 0);
      el.innerHTML =
        `<span class="sh-dot ${dot}"></span>` +
        `<span class="muted">${window.AnvilUI.esc(h.summary || "")}</span>` +
        `<span class="muted sh-cap">Workers: ${window.AnvilUI.esc(idle)} idle · ${window.AnvilUI.esc(busy)} busy · Jobs: ${window.AnvilUI.esc(activeJobs)} active</span>`;
      el.title = fleets
        .map((f) => `${f.id}: ${f.reachable ? "reachable" : "unreachable" + (f.error ? " — " + f.error : "")}`)
        .join("\n");
    } catch (_) {
      el.innerHTML = `<span class="sh-dot warn"></span><span class="muted">Health unknown</span>`;
    }
  },

  // ------------------------------------------------------------------ help
  /**
   * openHelp shows what a surface is for, the lifecycle legend, and the actions
   * available in each state.
   *
   * The text is short and lives here rather than in a doc site: the question
   * "which button now" is asked while looking at the buttons.
   */
  openHelp(surface) {
    const host = this._helpHost();
    if (!host) return;
    const sheet = this.helpSheets[surface] || this.helpSheets.default;
    window.AnvilUI.modal.open(
      host,
      `
      <div class="modal-card modal-wide">
        <h3 style="margin-top:0">${window.AnvilUI.esc(sheet.title)}</h3>
        <p class="muted" style="margin-top:0">${window.AnvilUI.esc(sheet.what)}</p>
        ${sheet.states
          ? `<h4 style="margin-bottom:0.3rem">States</h4>
             <table class="help-states">
               <thead><tr><th>State</th><th>Means</th><th>What moves it on</th></tr></thead>
               <tbody>${sheet.states
                 .map(
                   (s) =>
                     `<tr><td>${window.AnvilUI.badge(s.code)}</td><td>${window.AnvilUI.esc(s.means)}</td><td>${window.AnvilUI.esc(s.next)}</td></tr>`,
                 )
                 .join("")}</tbody>
             </table>`
          : ""}
        ${sheet.keys
          ? `<h4 style="margin-bottom:0.3rem">Keyboard</h4>
             <ul class="help-keys">${sheet.keys.map((k) => `<li><code>${window.AnvilUI.esc(k.k)}</code> ${window.AnvilUI.esc(k.d)}</li>`).join("")}</ul>`
          : ""}
        <h4 style="margin-bottom:0.3rem">Vocabulary</h4>
        <dl class="help-vocabulary">${this.vocabulary
          .map(
            (term) => `<div><dt>${window.AnvilUI.esc(term.name)}</dt><dd>${window.AnvilUI.esc(term.meaning)}</dd></div>`,
          )
          .join("")}</dl>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" data-close-ui>Close</button>
        </div>
      </div>`,
      { title: sheet.title },
    );
  },

  vocabulary: [
    { name: "Fleet", meaning: "One configured scheduler and the Nodes it reports." },
    { name: "Node", meaning: "A machine running agentd and hosting Workers." },
    { name: "Worker", meaning: "One Project checkout and branch on a Node." },
    { name: "Project", meaning: "The engineering unit to which Delivery binds." },
    { name: "Generation", meaning: "A product major line, such as 0.x, containing Milestones." },
    { name: "Milestone", meaning: "A major.minor delivery unit in a Generation." },
    { name: "Version", meaning: "A major.minor.patch Requirement row; Design Versions map to Phases." },
    { name: "Phase", meaning: "An ordered execution unit under a Milestone." },
    { name: "Job", meaning: "One queued or running unit of Worker execution." },
    { name: "Session", meaning: "One interactive Workbench conversation on a Worker." },
  ],

  _helpHost() {
    let host = document.getElementById("shell-modal");
    if (!host) {
      host = document.createElement("div");
      host.id = "shell-modal";
      host.className = "modal";
      host.hidden = true;
      document.body.appendChild(host);
    }
    return host;
  },

  /**
   * The lifecycle legend is the same vocabulary the badges use, so the help cannot
   * drift from the UI without someone noticing here.
   */
  helpSheets: {
    requirements: {
      title: "Requirements",
      what: "A Project's milestones and their versions. Each version states what to build (Delivery note), why (Goals) and what done means (Completion criteria). Completion criteria are what the agent is told, so they are the most valuable text on the page.",
      states: [
        { code: "awaiting_design", means: "Created, no design yet.", next: "Write a design in Designs." },
        { code: "awaiting_roadmap", means: "Design approved, phases not published.", next: "Bootstrap the roadmap from the design." },
        { code: "awaiting_development", means: "Roadmap exists, not started.", next: "Start from the Roadmap." },
        { code: "in_development", means: "A Phase Job is running.", next: "Let it finish, or submit for Test." },
        { code: "testing", means: "In the test queue.", next: "Run the campaign; pass or fail." },
        { code: "complete", means: "Passed; waiting for acceptance.", next: "Accept, then merge to main." },
      ],
    },
    designs: {
      title: "Designs",
      what: "The ADR plus ordered Design Versions that become Roadmap Phases. Each Design Version carries a Gate whose checkboxes block Phase Jobs, plus acyclic dependencies.",
      keys: [
        { k: "↑ / ↓", d: "Move focus between Design Versions." },
        { k: "Space / X", d: "Toggle one Version; Shift+Space extends the range." },
        { k: "Ctrl/Cmd+A", d: "Select every visible Version." },
        { k: "Enter / Esc", d: "Open the Version editor / clear selection." },
        { k: "/", d: "Focus plan search." },
      ],
      states: [
        { code: "draft", means: "Editable.", next: "Save, View, then Approve." },
        { code: "approved", means: "Read-only; the text is what the agent follows.", next: "Bootstrap the roadmap." },
      ],
    },
    roadmap: {
      title: "Roadmap",
      what: "The Milestone graph and the controls that run work. Start enqueues the next Phase as a Job on the bound Worker; a disabled Start explains why on its card.",
      keys: [
        { k: "drag", d: "Move a milestone on the canvas." },
        { k: "↑ / ↓", d: "Move focus between milestones." },
        { k: "Space / X", d: "Toggle one milestone; Shift+Space extends the range." },
        { k: "Ctrl/Cmd+A", d: "Select every visible milestone." },
        { k: "Enter / Esc", d: "Open the milestone card / clear selection." },
        { k: "/", d: "Focus plan search." },
      ],
    },
    test: {
      title: "Test",
      what: "The campaign loop for Milestones in testing: draft cases, run them on the Worker, fix failures, regress, then pass. Pass requires every case passed and no open bug.",
      keys: [
        { k: "↑ / ↓", d: "Move focus between test cases." },
        { k: "Space / X", d: "Toggle one case; Shift+Space extends the range." },
        { k: "Ctrl/Cmd+A", d: "Select every visible case." },
        { k: "Enter / Esc", d: "Open case details / clear selection." },
        { k: "/", d: "Focus plan search." },
      ],
    },
    workbench: {
      title: "Workbench",
      what: "Start a Session on a Worker and watch it work. One Session runs per Worker. Approvals follow the Worker's configured policy.",
    },
    jobs: {
      title: "Job history",
      what: "Every Job, including Phase Jobs started from the Roadmap. Open one to read its transcript and diagnose a failed run.",
    },
    clusters: {
      title: "Clusters",
      what: "Fleets, Workspaces, Projects, Nodes, and their Workers. Bind a Worker to a Project before Delivery or Workbench uses it.",
    },
    default: {
      title: "Anvil console",
      what: "Delivery plans work and the Roadmap runs it. Use the Generation switcher at the top of each Delivery section.",
    },
  },

  // ----------------------------------------------------------------- errors
  /**
   * showError renders a classified failure.
   *
   * The raw message stays visible: a dialog that hides the original makes an
   * unexpected failure look like a known one, and an operator who cannot see the
   * server's words cannot report them either.
   */
  async showError(message, status, requestId) {
    const host = this._helpHost();
    if (!host) return;
    let info = { title: "That did not work", what: message, next: "", raw: message, href: "" };
    try {
      info = await api.v1.classifyError({
        message: String(message || ""),
        status: status || 0,
        request_id: String(requestId || ""),
      });
    } catch (_) {
      // Classification is a convenience; failing to classify must still show the
      // message rather than nothing.
    }
    window.AnvilUI.modal.open(
      host,
      `
      <div class="modal-card">
        <h3 style="margin-top:0">${window.AnvilUI.esc(info.title)}</h3>
        <p>${window.AnvilUI.esc(info.what)}</p>
        ${info.next ? `<p class="muted">${window.AnvilUI.esc(info.next)}</p>` : ""}
        ${info.request_id ? `<p class="muted">Request ID <code>${window.AnvilUI.esc(info.request_id)}</code></p>` : ""}
        ${info.href ? `<p><a href="${window.AnvilUI.esc(info.href)}">Go there</a></p>` : ""}
        ${info.raw && info.raw !== info.what ? `<details class="err-detail"><summary>Server message</summary><pre>${window.AnvilUI.esc(info.raw)}</pre></details>` : ""}
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" data-close-ui>Close</button>
        </div>
      </div>`,
      { title: info.title },
    );
  },

  /** surfaceForRoute maps a hash to a help sheet key. */
  surfaceForRoute(hash) {
    const h = String(hash || "");
    if (h.startsWith("#/delivery/requirements")) return "requirements";
    if (h.startsWith("#/delivery/designs")) return "designs";
    if (h.startsWith("#/delivery/roadmap")) return "roadmap";
    if (h.startsWith("#/delivery/test")) return "test";
    if (h.startsWith("#/workbench")) return "workbench";
    if (h.startsWith("#/jobs")) return "jobs";
    if (h.startsWith("#/clusters")) return "clusters";
    return "default";
  },
};
