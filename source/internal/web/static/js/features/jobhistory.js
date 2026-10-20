// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Job history (p_0.46.10).
 *
 * The console could not answer "why did this fail": events lived in a 200-frame
 * memory ring and the database kept one string, so a finished job's transcript was
 * gone. This reads the persisted log instead.
 *
 * The renderer is the Workbench's — an event looks the same whether it is live or
 * a week old, and an operator should not learn two vocabularies for "a tool was
 * called".
 */
window.Features = window.Features || {};
window.Features.jobhistory = {
  el: null,
  _filter: { status: "", limit: 100 },

  async mount(el) {
    this.el = el;
    el.innerHTML = `
      <div class="del-head">
        <div>
          <h2>Job history</h2>
          <p class="muted del-sub">Every job, what it did, and why it stopped.</p>
        </div>
        <div class="toolbar" style="margin:0">
          <select id="jh-status" style="width:auto;margin:0">
            <option value="">All statuses</option>
            <option value="running">Running</option>
            <option value="finished">Finished</option>
            <option value="error">Failed</option>
            <option value="cancelled">Cancelled</option>
          </select>
          <button type="button" class="ghost" id="jh-refresh">Refresh</button>
        </div>
      </div>
      <div id="jh-list" class="jh-list">${this.skeleton(4)}</div>
      <div id="del-ui-modal" class="modal" hidden></div>`;

    el.querySelector("#jh-refresh").addEventListener("click", () => this.refresh());
    el.querySelector("#jh-status").addEventListener("change", (e) => {
      this._filter.status = e.target.value;
      return this.refresh();
    });
    await this.refresh();
  },

  unmount() {
    this.el = null;
  },

  onRoute() {
    return this.refresh();
  },

  skeleton(rows) {
    let out = `<div class="del-skeleton" aria-hidden="true">`;
    for (let i = 0; i < rows; i++) out += `<div class="del-skeleton-row"></div>`;
    return `${out}</div>`;
  },

  async refresh() {
    const box = this.el?.querySelector("#jh-list");
    if (!box) return;
    const q = new URLSearchParams();
    if (this._filter.status) q.set("status", this._filter.status);
    const suffix = q.toString() ? `?${q}` : "";
    try {
      const data = await api.v1.jobs(suffix);
      const jobs = data.jobs || [];
      if (jobs.length === 0) {
        box.innerHTML = `<div class="del-empty"><p class="muted">No jobs recorded yet. Start a session in the Workbench, or run a phase from the Roadmap.</p></div>`;
        return;
      }
      box.innerHTML = jobs
        .map((j) => {
          const dur = j.duration_secs ? `${j.duration_secs}s` : "—";
          const when = window.AnvilUI.esc((j.started_at || "").replace("T", " ").slice(0, 19));
          const running = j.status === "running" || j.status === "queued";
          return `
          <div class="jh-row">
            <span class="jh-state ${window.AnvilUI.badgeClass(j.status)}"></span>
            <button type="button" class="jh-main jh-open" data-jh-open="${window.AnvilUI.esc(j.job_id)}">
              <span class="jh-kind">${window.AnvilUI.esc(j.kind || "job")}</span>
              <span class="muted jh-id">${window.AnvilUI.esc(j.job_id)}</span>
            </button>
            <span class="jh-meta muted">${when}</span>
            <span class="jh-meta muted">${dur}</span>
            <span class="jh-meta muted">${j.event_count || 0} events${j.truncated ? " (trimmed)" : ""}</span>
            <span class="badge ${window.AnvilUI.badgeClass(j.status)}">${window.AnvilUI.esc(j.status)}</span>
            ${running ? `<button type="button" class="ghost jh-stop" data-jh-stop="${window.AnvilUI.esc(j.job_id)}">Stop</button>` : `<span></span>`}
          </div>`;
        })
        .join("");
      box.querySelectorAll("[data-jh-open]").forEach((btn) => {
        btn.addEventListener("click", () => this.openJob(btn.getAttribute("data-jh-open")));
      });
      // Stop from here rather than only from #/ops: a desktop operator opens this
      // list first when a job is running away, and sending them to a phone-sized
      // screen to press one button is the wrong shape.
      box.querySelectorAll("[data-jh-stop]").forEach((btn) => {
        btn.addEventListener("click", () =>
          window.AnvilUI.withAction(
            btn,
            {
              key: "jh-stop:" + btn.getAttribute("data-jh-stop"),
              runningLabel: "Stopping…",
              host: this.el.querySelector("#del-ui-modal"),
              errorTitle: "Stop failed",
            },
            async () => {
              await api.v1.jobCancel(btn.getAttribute("data-jh-stop"));
              await this.refresh();
            },
          ),
        );
      });
    } catch (e) {
      box.innerHTML = `<div class="err">${window.AnvilUI.esc(e.message || String(e))}</div>`;
    }
  },

  /** openJob renders one job's persisted transcript, newest-first by seq. */
  async openJob(jobId) {
    let events = [];
    try {
      const data = await api.v1.jobEvents(jobId);
      events = data.events || [];
    } catch (e) {
      window.AnvilUI.notice(this.el.querySelector("#del-ui-modal"), e.message || String(e), "Could not open the job");
      return;
    }
    const rows = events.length
      ? events.map((ev) => this.eventHtml(ev)).join("")
      : `<p class="muted">No events were recorded for this job.</p>`;
    window.AnvilUI.modal.open(
      this.el.querySelector("#del-ui-modal"),
      `
      <div class="modal-card modal-doc">
        <div class="modal-doc-head">
          <h3 style="margin:0;flex:1">${window.AnvilUI.esc(jobId)}</h3>
          <div class="modal-doc-actions">
            <span class="muted">${events.length} events</span>
            <button type="button" class="ghost" data-close-ui>Close</button>
          </div>
        </div>
        <div class="modal-doc-body wb-stream">${rows}</div>
      </div>`,
      { title: "Job " + jobId },
    );
  },

  /**
   * eventHtml renders one persisted event with the same vocabulary as the live
   * Workbench stream. Values go through textContent-equivalent escaping rather
   * than being interpolated raw: tool output is arbitrary file content.
   */
  eventHtml(ev) {
    const esc = window.AnvilUI.esc;
    const t = ev.type || "";
    if (t === "assistant" && ev.text) {
      return `<div class="wb-say">${esc(ev.text)}</div>`;
    }
    if (t === "tool_call") {
      return `<details class="wb-tool"${""}>
        <summary>⚙ ${esc(ev.name || "tool")}</summary>
        <pre class="wb-tool-args">${esc(ev.args || "")}</pre>
      </details>`;
    }
    if (t === "tool_result") {
      return `<details class="wb-tool${ev.is_error ? " wb-tool-error" : ""}">
        <summary>${ev.is_error ? "✗" : "✓"} ${esc(ev.name || "result")}</summary>
        <pre class="wb-tool-out">${esc(String(ev.content || "").slice(0, 4000))}</pre>
      </details>`;
    }
    if (t === "error") {
      return `<div class="wb-error">${esc(ev.text || "error")}</div>`;
    }
    if (t === "progress") {
      return `<div class="muted jh-progress">step ${esc(String(ev.step || ""))}</div>`;
    }
    const text = ev.text || ev.name || t;
    if (!text) return "";
    return `<div class="muted jh-other">${esc(t)}: ${esc(text)}</div>`;
  },
};
