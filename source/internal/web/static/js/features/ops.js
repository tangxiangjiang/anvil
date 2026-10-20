// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Ops — the one screen for a phone (p_0.46.12).
 *
 * Not a mobile version of the console. It is the two things an operator does away
 * from a desk: see whether anything is wrong, and stop a job that is running away.
 * Deliberately one screen, single column, no planning surfaces — those genuinely
 * need a keyboard and a wide view, and pretending otherwise would produce a
 * layout that is bad at both.
 */
window.Features = window.Features || {};
window.Features.ops = {
  el: null,
  _timer: null,

  async mount(el) {
    this.el = el;
    el.innerHTML = `
      <div class="del-head">
        <div>
          <h2>Ops</h2>
          <p class="muted del-sub">State and stop. Everything else is better on a wider screen.</p>
        </div>
        <div class="toolbar" style="margin:0">
          <button type="button" class="ghost" id="ops-refresh">Refresh</button>
        </div>
      </div>
      <div id="ops-body" class="ops-grid">
        <div class="ops-card"><h3>Health</h3><div class="muted">Loading…</div></div>
      </div>
      <div id="del-ui-modal" class="modal" hidden></div>`;

    el.querySelector("#ops-refresh").addEventListener("click", () => this.refresh());
    await this.refresh();
    // Poll only while visible: a phone left on this screen should stay current,
    // and a background tab should not keep a network loop alive.
    this._timer = setInterval(() => {
      if (!document.hidden) void this.refresh();
    }, 15000);
  },

  unmount() {
    if (this._timer) clearInterval(this._timer);
    this._timer = null;
    this.el = null;
  },

  onRoute() {
    return this.refresh();
  },

  async refresh() {
    const body = this.el?.querySelector("#ops-body");
    if (!body) return;
    const esc = window.AnvilUI.esc;
    let health = { fleets: [], summary: "Health unknown", ok: false };
    let jobs = [];
    try {
      health = await api.v1.health();
    } catch (_) {
      /* shown below */
    }
    try {
      const data = await api.v1.jobs("?status=running");
      jobs = data.jobs || [];
    } catch (_) {
      /* shown below */
    }

    const fleets = health.fleets || [];
    const fleetRows = fleets.length
      ? fleets
          .map(
            (f) => `<div class="ops-row">
              <span class="sh-dot ${f.reachable ? "ok" : "bad"}"></span>
              <span class="ops-grow">${esc(f.id)}</span>
              <span class="muted">${f.reachable ? `${f.idle} idle · ${f.busy} busy` : "unreachable"}</span>
            </div>`,
          )
          .join("")
      : `<div class="muted">No fleet configured.</div>`;

    const jobRows = jobs.length
      ? jobs
          .map(
            (j) => `<div class="ops-row">
              <span class="ops-grow">${esc(j.kind || "job")} · ${esc((j.prompt || j.job_id || "").slice(0, 40))}</span>
              <button type="button" class="danger" data-ops-stop="${esc(j.job_id)}">Stop</button>
            </div>`,
          )
          .join("")
      : `<div class="muted">Nothing running.</div>`;

    body.innerHTML = `
      <div class="ops-card">
        <h3>Health</h3>
        <div class="muted" style="margin-bottom:0.35rem">${esc(health.summary || "")}</div>
        ${fleetRows}
      </div>
      <div class="ops-card">
        <h3>Running</h3>
        ${jobRows}
      </div>`;

    body.querySelectorAll("[data-ops-stop]").forEach((btn) => {
      btn.addEventListener("click", () =>
        window.AnvilUI.withAction(
          btn,
          { key: "ops-stop:" + btn.getAttribute("data-ops-stop"), runningLabel: "Stopping…", host: this.el.querySelector("#del-ui-modal"), errorTitle: "Stop failed" },
          async () => {
            await this.stopJob(btn.getAttribute("data-ops-stop"));
            await this.refresh();
          },
        ),
      );
    });
  },

  /**
   * stopJob asks the Workbench cancel first, then falls back to the milestone
   * lever: a phase job has no direct cancel endpoint of its own, and the milestone
   * run_state is what actually stops the loop from enqueuing the next phase.
   */
  async stopJob(jobID) {
    let cancelled = false;
    try {
      await api.v1.workbenchCancel(jobID);
      cancelled = true;
    } catch (_) {
      cancelled = false;
    }
    if (cancelled) return;
    // Fall back: find the milestone bound to this job through the history.
    const data = await api.v1.jobs("");
    const rec = (data.jobs || []).find((j) => j.job_id === jobID);
    if (rec && rec.milestone_id) {
      await api.v1.deliveryPatchMilestone(rec.milestone_id, { run_state: "stopped" });
      return;
    }
    throw new Error("That job cannot be stopped from here.");
  },
};
