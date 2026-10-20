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

  // ------------------------------------------------------------- preflight
  //
  // A transition that would be refused is refused *before* the click: each
  // guarded button asks the server what is missing and renders the reasons
  // beside itself. The same check runs server-side as the enforcement, from one
  // implementation, so a preview cannot disagree with a refusal.

  /** _preflight asks and caches per (transition,target) for this render. */
  async preflight(transition, target) {
    if (!this._pfCache) this._pfCache = {};
    const key = transition + ":" + target;
    if (this._pfCache[key]) return this._pfCache[key];
    let res = { ok: true, blockers: [] };
    try {
      res = await api.v1.deliveryPreflight(transition, target);
    } catch (_) {
      // A failed preview must not block the action: the server will refuse it
      // with the same reason, and a broken preview endpoint is not a policy.
      res = { ok: true, blockers: [] };
    }
    this._pfCache[key] = res;
    return res;
  },

  /**
   * applyPreflight disables a button and lists why.
   *
   * `annotations` maps transition → target for every guarded button on the page.
   * Hard blockers disable; advisory ones (open_work on a seal) are shown but do
   * not disable, because sealing a generation with open work is a decision the
   * operator is allowed to make.
   */
  async applyPreflight(el, annotations) {
    if (!el || !annotations || !annotations.length) return;
    this._pfCache = {};
    for (const a of annotations) {
      const res = await this.preflight(a.transition, a.target);
      const btn = el.querySelector(a.selector);
      if (!btn) {
        // A selector that matches nothing is a bug in this file, not a policy.
        // Say so once rather than silently annotating zero buttons: a preflight
        // that quietly does nothing is indistinguishable from one that passed.
        console.warn("preflight: no button for", a.selector);
        continue;
      }
      const advisory = (res.blockers || []).filter((b) => b.code === "open_work");
      const hard = (res.blockers || []).filter((b) => b.code !== "open_work");
      if (hard.length) {
        btn.disabled = true;
        btn.setAttribute("aria-describedby", a.selector.replace(/[^a-z0-9]/gi, "") + "-why");
        btn.title = hard.map((b) => b.message).join("\n");
      }
      const show = hard.length ? hard : advisory;
      if (!show.length) continue;
      const why = document.createElement("div");
      why.className = "del-blockers" + (hard.length ? " hard" : " advisory");
      why.id = a.selector.replace(/[^a-z0-9]/gi, "") + "-why";
      // Built as nodes rather than one template: the link is conditional and
      // the message is operator-visible text, so escaping each piece at the
      // point of use is clearer than escaping a pre-joined string.
      show.forEach((b) => {
        const row = document.createElement("div");
        row.className = "del-blocker";
        row.appendChild(document.createTextNode(b.message));
        if (b.href) {
          row.appendChild(document.createTextNode(" "));
          const link = document.createElement("a");
          link.href = b.href;
          link.textContent = "open";
          row.appendChild(link);
        }
        why.appendChild(row);
      });
      btn.closest(".card, .del-ms-card, .del-test-card")?.appendChild(why);
    }
  },

  /**
   * runPreflight is the call site's entry point.
   *
   * It exists because applyPreflight used to be fire-and-forget: a throw inside
   * it left every guarded button enabled and unannotated, so the preflight
   * silently degraded to no preflight — the exact failure it exists to prevent.
   * Here a failure is visible and the buttons stay usable: the server refuses
   * with the same reason, and a broken preview is not a policy.
   */
  runPreflight(el, annotations) {
    return this.applyPreflight(el, annotations).catch((e) => {
      console.error("preflight failed:", e);
      const box = el?.querySelector(".del-preflight-warn");
      if (box) return;
      const warn = document.createElement("div");
      warn.className = "del-blockers advisory del-preflight-warn";
      warn.textContent =
        "Could not check whether these actions are ready; the server will refuse them with the reason.";
      el?.querySelector(".del-head")?.appendChild(warn);
    });
  },

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
