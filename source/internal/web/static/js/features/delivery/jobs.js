// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Job plumbing: SSE watches, the sticky job bar, milestone control cards.
 *
 * Part of the Delivery feature object: assigns onto `window.Features.delivery`.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  _activeJob: null,

  /** setActiveJob records what is running; null clears the bar. */
  setActiveJob(job) {
    const same =
      (this._activeJob && this._activeJob.id) === (job && job.id) &&
      (this._activeJob && this._activeJob.text) === (job && job.text);
    this._activeJob = job || null;
    if (!same || !job) this.paintJobBar();
  },

  /** paintJobBar replaces just the bar, never the page. */
  paintJobBar() {
    const el = this.el?.querySelector("#del-job-bar");
    if (!el) return;
    el.innerHTML = this.jobBarHtml();
    this.bindJobBar();
  },

  jobBarHtml() {
    const j = this._activeJob;
    if (!j) return `<div id="del-job-bar"></div>`;
    const kindLabel = {
      phase: "Phase job",
      "campaign:run": "Test run",
      "campaign:regress": "Regress",
      "campaign:fix": "Fix",
      polish: "Polish",
    }[j.kind] || "Job";
    return `
      <div id="del-job-bar" class="del-job-bar" role="status" aria-live="polite">
        <span class="del-job-dot" aria-hidden="true"></span>
        <strong>${this.esc(kindLabel)}</strong>
        ${j.label ? `<span class="muted">· ${this.esc(j.label)}</span>` : ""}
        <span class="del-job-text muted">${this.esc(j.text || "queued")}</span>
        ${j.id ? `<button type="button" class="ghost" data-job-stop="${this.esc(j.id)}">Stop</button>` : ""}
      </div>`;
  },

  bindJobBar() {
    const btn = this.el?.querySelector("[data-job-stop]");
    if (!btn) return;
    btn.addEventListener("click", () => {
      const jobId = btn.getAttribute("data-job-stop");
      if (!jobId) return;
      return window.AnvilUI.withAction(
        btn,
        { key: "stopjob:" + jobId, runningLabel: "Stopping…", host: this.uiModal(), errorTitle: "Stop failed" },
        async () => {
          await api.v1.deliveryCancelJob(jobId).catch(async () => {
            // Fall back to the milestone lever when the scheduler cannot cancel.
            const ms = (this.data.roadmap?.milestones || []).find(
              (m) => (m.active_job_id || m.campaign_job_id || "") === jobId,
            );
            if (ms) await api.v1.deliveryPatchMilestone(ms.id, { run_state: "stopped" });
          });
          this.setActiveJob(null);
          await this.reload();
          this.render();
        },
      );
    });
  },

  /** A running job, recovered from the snapshot after a reload or a remount. */
  activeJobFromSnapshot() {
    for (const m of this.data?.roadmap?.milestones || []) {
      const run = (m.run_state || "").toLowerCase();
      const id = (m.active_job_id || m.campaign_job_id || "").trim();
      if (run === "running" && id) {
        return {
          id,
          kind: m.campaign_job_kind || "phase",
          label: m.title || m.id,
          text: m.campaign_job_last_event || m.job_last_event || "running",
        };
      }
    }
    return null;
  },
});
