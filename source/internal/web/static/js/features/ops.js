// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Ops — the one screen for a phone (p_0.45.14).
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
    if (!this._snapshotCache) {
      this._snapshotCache = window.AnvilUI.swr.createStore({ maxEntries: 2, maxAgeMs: 5 * 60 * 1000 });
    }
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
    const cached = this._snapshotCache.peek("ops:all");
    if (cached) {
      this._snapshot = cached.data;
      this._snapshotState = { stale: true, refreshing: true, updatedAt: cached.updatedAt, error: "" };
      this.paint();
    }
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
    this._snapshotState = { ...(this._snapshotState || {}), refreshing: true, error: "" };
    if (this._snapshot) this.paint();
    try {
      const result = await this._snapshotCache.revalidate(
        "ops:all",
        async () => {
          const [health, data, readiness] = await Promise.all([
            api.v1.health(),
            api.v1.jobs("?status=running"),
            api.v1.deliveryReadiness({}),
          ]);
          return { health, jobs: data.jobs || [], readiness };
        },
        {
          // Prompts can contain source or incident details. Ops only needs a
          // stable job identity to stop it, so sensitive prompt text never
          // enters this cache.
          project: ({ health, jobs, readiness }) => ({
            health,
            jobs: jobs.map((job) => ({
              job_id: job.job_id,
              kind: job.kind,
              status: job.status,
            })),
            readiness: {
              rows: (readiness?.rows || []).map((row) => ({
                id: row.id,
                title: row.title,
                lifecycle: row.lifecycle,
                transition: row.transition,
                ok: row.ok,
                href: row.href,
                blocker_count: (row.blockers || []).length,
              })),
            },
          }),
        },
      );
      if (!result.accepted || !this.el) return;
      this._snapshot = result.snapshot;
      this._snapshotState = {
        stale: false,
        refreshing: false,
        updatedAt: result.updatedAt,
        error: "",
      };
    } catch (error) {
      this._snapshotState = {
        ...(this._snapshotState || {}),
        stale: Boolean(this._snapshot),
        refreshing: false,
        error: error?.message || String(error),
      };
    }
    this.paint();
  },

  paint() {
    const body = this.el?.querySelector("#ops-body");
    if (!body) return;
    const esc = window.AnvilUI.esc;
    const health = this._snapshot?.health || { fleets: [], summary: "Health unknown", ok: false };
    const jobs = this._snapshot?.jobs || [];
    const readiness = this._snapshot?.readiness?.rows || [];
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
              <span class="ops-grow">${esc(j.kind || "job")} · ${esc(j.job_id || "")}</span>
              <button type="button" class="danger" data-ops-stop="${esc(j.job_id)}">Stop</button>
            </div>`,
          )
          .join("")
      : `<div class="muted">Nothing running.</div>`;
    const readinessRows = readiness.length
      ? readiness
          .map(
            (row) => `<div class="ops-row">
              <span class="sh-dot ${row.ok ? "ok" : "warn"}"></span>
              <span class="ops-grow">
                <strong>${esc(row.title || row.id)}</strong>
                <span class="muted">${esc(row.lifecycle || "")} → ${esc(row.transition || "")}</span>
              </span>
              <span class="${row.ok ? "ok" : "warning"}">${row.ok ? "Ready" : `${Number(row.blocker_count || 0)} blocked`}</span>
              ${row.href ? `<a class="ghost ops-open" href="${esc(row.href)}">Open</a>` : ""}
            </div>`,
          )
          .join("")
      : `<div class="muted">No non-terminal records.</div>`;

    const state = this._snapshotState || {};
    const age = window.AnvilUI.swr.ageLabel(state.updatedAt);
    const connection = state.error
      ? `<div class="snapshot-state bad" role="status">Showing last successful snapshot · updated ${esc(age)} · disconnected <button type="button" class="ghost" data-ops-retry>Retry</button></div>`
      : state.refreshing || state.stale
        ? `<div class="snapshot-state warn" role="status">Showing last successful snapshot · updated ${esc(age)} · refreshing…</div>`
        : state.updatedAt
          ? `<div class="snapshot-state" role="status">Updated ${esc(age)}</div>`
          : `<div class="snapshot-state bad" role="status">No snapshot available · disconnected <button type="button" class="ghost" data-ops-retry>Retry</button></div>`;
    body.innerHTML = `${connection}
      <div class="ops-card">
        <h3>Health</h3>
        <div class="muted" style="margin-bottom:0.35rem">${esc(health.summary || "")}</div>
        ${fleetRows}
      </div>
      <div class="ops-card">
        <h3>Running</h3>
        ${jobRows}
      </div>
      <div class="ops-card">
        <h3>Readiness</h3>
        ${readinessRows}
      </div>`;

    body.querySelector("[data-ops-retry]")?.addEventListener("click", () => this.refresh());
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
   * stopJob stops a job by id.
   *
   * The server decides *how*: it knows the job's kind from the record, so a
   * Workbench session is cancelled and a phase job stops its milestone. This used
   * to try the Workbench cancel first and fall back, which meant every phase job
   * made a call that could only fail - the Workbench only knows its own sessions.
   */
  async stopJob(jobID) {
    await api.v1.jobCancel(jobID);
  },
};
