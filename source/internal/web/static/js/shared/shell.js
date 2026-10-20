// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Shell chrome (p_0.46.9): the health strip, the help sheet, and the actionable
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
        el.innerHTML = `<span class="sh-dot bad"></span><span class="muted">No fleet configured</span>`;
        el.title = "Add a scheduler fleet in Clusters to start work.";
        return;
      }
      const bad = fleets.filter((f) => !f.reachable);
      const dot = bad.length === 0 ? "ok" : bad.length === fleets.length ? "bad" : "warn";
      const idle = fleets.reduce((n, f) => n + (f.idle || 0), 0);
      const busy = fleets.reduce((n, f) => n + (f.busy || 0), 0);
      el.innerHTML =
        `<span class="sh-dot ${dot}"></span>` +
        `<span class="muted">${window.AnvilUI.esc(h.summary || "")}</span>` +
        `<span class="muted sh-cap">${idle} idle · ${busy} busy</span>`;
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
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" data-close-ui>Close</button>
        </div>
      </div>`,
      { title: sheet.title },
    );
  },

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
        { code: "in_development", means: "A phase job is running.", next: "Let it finish, or submit for test." },
        { code: "testing", means: "In the test queue.", next: "Run the campaign; pass or fail." },
        { code: "complete", means: "Passed; waiting for acceptance.", next: "Accept, then merge to main." },
      ],
    },
    designs: {
      title: "Designs",
      what: "The ADR plus the ordered Design Versions that become roadmap phases. Each version carries a Gate — its checkboxes block phase jobs — and Depends on, which must stay acyclic.",
      states: [
        { code: "draft", means: "Editable.", next: "Save, View, then Approve." },
        { code: "approved", means: "Read-only; the text is what the agent follows.", next: "Bootstrap the roadmap." },
      ],
    },
    roadmap: {
      title: "Roadmap",
      what: "The milestone graph and the controls that run work. Start enqueues the next phase as a job on the bound Worker; the reason a Start is disabled is shown next to the card.",
      keys: [
        { k: "drag", d: "Move a milestone on the canvas." },
        { k: "Enter", d: "Open a milestone card." },
      ],
    },
    test: {
      title: "Test",
      what: "The campaign loop for milestones in testing: draft cases, run them on the Worker, fix failures, regress, then pass. Pass requires every case passed and no open bug.",
    },
    workbench: {
      title: "Workbench",
      what: "Start a session on a Worker and watch it work. One session per worker. Approvals are decided by the worker's own options, not here — the note above the stream says so.",
    },
    jobs: {
      title: "Job history",
      what: "Every job, including phase jobs started from the Roadmap. Open one to read its transcript — this is where you find out why a run failed.",
    },
    clusters: {
      title: "Clusters",
      what: "Fleets, workspaces, projects and the workers bound to them. A worker must be bound to a project before Delivery or the Workbench can use it.",
    },
    default: {
      title: "Anvil console",
      what: "Delivery plans work and the Roadmap runs it. Use the generation switcher at the top of a Delivery section to change which major line you are looking at.",
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
  async showError(message, status) {
    const host = this._helpHost();
    if (!host) return;
    let info = { title: "That did not work", what: message, next: "", raw: message, href: "" };
    try {
      info = await api.v1.classifyError({ message: String(message || ""), status: status || 0 });
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
