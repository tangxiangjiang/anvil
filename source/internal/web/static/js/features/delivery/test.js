// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Test surface: testing queue, campaign control card, cases and buglist.
 *
 * Part of the Delivery feature object. Each surface file assigns its own methods
 * onto the one `window.Features.delivery`, so the mounting contract in app.js
 * (`mount` / `unmount` / `onRoute`) and the router are unchanged: this is a
 * mechanical split of one 6 000-line literal, not a redesign.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  bindTestCampaignJobStreams(el) {
    el.querySelectorAll("[data-test-card]").forEach((card) => {
      const jobId = (card.getAttribute("data-campaign-job") || "").trim();
      const st = (card.getAttribute("data-campaign-job-status") || "").toLowerCase();
      if (!jobId || (st !== "running" && st !== "queued")) return;
      if (this._jobAbort?.[jobId]) return;
      if (!this._jobAbort) this._jobAbort = {};
      const ac = new AbortController();
      this._jobAbort[jobId] = ac;
      const snip = card.querySelector("[data-campaign-snip]");
      const kind = card.getAttribute("data-campaign-kind") || "campaign:run";
      const label = card.getAttribute("data-ms-title") || "";
      void this.watchDeliveryJobSSE(jobId, ac.signal, {
        onEvent: (data) => {
          const text = data.text || data.content || data.type || "";
          if (snip && text) snip.textContent = String(text).slice(0, 160);
          if (text) {
            this.setActiveJob({ id: jobId, kind, label, text: String(text).slice(0, 200) });
          }
        },
        onDone: async () => {
          delete this._jobAbort[jobId];
          if (this._activeJob && this._activeJob.id === jobId) this.setActiveJob(null);
          await this.reload();
          this.render();
        },
      });
    });
  },

  async awaitDeliveryJob(jobId, { onEvent } = {}) {
    if (!jobId) return;
    await new Promise((resolve) => {
      const ac = new AbortController();
      void this.watchDeliveryJobSSE(jobId, ac.signal, {
        onEvent,
        onDone: async () => {
          ac.abort();
          resolve();
        },
      });
    });
  },

  viewTestList(routeMajor) {
    const majLabel = this.majorLabel(routeMajor);
    const allMs = this.data.roadmap?.milestones || [];
    const rows = allMs
      .map((m) => {
        const bind = this.parseRoadmapMsBind(m.id);
        const eng = bind ? this.findEng(bind.engId) : null;
        const life = bind
          ? this.milestoneLifecycle(eng, bind.major, bind.minor)
          : m.status || "";
        const project =
          eng?.project_key ||
          eng?.title ||
          (eng ? this.findWorker(eng.worker_id)?.label : "") ||
          "";
        const worker =
          this.findWorker(m.execute_worker_id)?.label ||
          m.execute_worker_id ||
          "";
        const msKey = bind != null ? `${bind.major}.${bind.minor}` : "";
        return { m, bind, eng, life, project, worker, msKey };
      })
      .filter(
        (r) =>
          r.bind &&
          r.bind.major === routeMajor &&
          (r.life === "testing" || r.m.status === "testing"),
      )
      .sort((a, b) => {
        if (a.bind && b.bind && a.bind.minor !== b.bind.minor)
          return a.bind.minor - b.bind.minor;
        return (a.m.title || "").localeCompare(b.m.title || "");
      });

    const cards = rows.length
      ? rows.map((r) => this.testControlCardHtml(r)).join("")
      : `<div class="del-test-empty card">
           <strong>No milestones in testing</strong>
           <p class="muted">When development is done on Roadmap, use <em>提交测试</em> — then draft cases, run, fix bugs, regress, Pass.</p>
           <a class="ghost" href="#/delivery/roadmap/m/${routeMajor}">Open Roadmap · ${this.esc(majLabel)}</a>
         </div>`;

    return `
      <p class="del-crumbs">
        <a href="#/delivery/test">Generations</a>
        <span class="muted">/</span>
        <span>${this.esc(majLabel)}</span>
      </p>
      <div class="toolbar del-rm-list-bar">
        <span class="muted" style="flex:1">${rows.length} in testing · ${this.esc(majLabel)}</span>
        <a class="ghost" href="#/delivery/roadmap/m/${routeMajor}">Roadmap</a>
      </div>
      <div class="del-test-grid">${cards}</div>`;
  },

  headVersionForBind(eng, major, minor) {
    if (!eng) return null;
    const vers = (eng.versions || []).filter(
      (v) => v.major === major && v.minor === minor,
    );
    if (!vers.length) return null;
    return vers.reduce((a, b) => (a.patch <= b.patch ? a : b));
  },

  buglistForBind(engId, major, minor) {
    return (this.data.buglist_items || []).filter(
      (b) =>
        b.eng_req_id === engId && b.major === major && b.minor === minor,
    );
  },

  testCasesForBind(engId, major, minor) {
    return (this.data.test_cases || []).filter(
      (c) =>
        c.eng_req_id === engId && c.major === major && c.minor === minor,
    );
  },

  campaignPassReady(cases, bugs) {
    if (!cases.length) return false;
    if (cases.some((c) => c.status !== "passed")) return false;
    if (
      bugs.some((b) => b.status === "open" || b.status === "fixing")
    )
      return false;
    return true;
  },

  testControlCardHtml(r) {
    const m = r.m;
    const bind = r.bind;
    const des = this.findDesignForMilestone(m);
    const desHref = des ? `#/delivery/designs/${this.esc(des.id)}` : "";
    const reqHref =
      bind &&
      `#/delivery/requirements/${this.esc(bind.engId)}/${bind.major}/${bind.minor}`;
    const rmHref =
      bind &&
      `#/delivery/roadmap/m/${bind.major}/${encodeURIComponent(m.id)}`;
    const head = this.headVersionForBind(r.eng, bind.major, bind.minor);
    const success = (head?.success || m.acceptance || "").trim();
    const acceptanceBlock = success
      ? `<pre class="del-test-accept">${this.esc(success)}</pre>`
      : `<p class="muted del-test-accept-empty">No acceptance / success text on the Version yet.</p>`;
    const cases = this.testCasesForBind(bind.engId, bind.major, bind.minor);
    const caseRows = cases.length
      ? `<ul class="del-case-list">
          ${cases
            .map(
              (c) => `
            <li class="del-case-row">
              <div class="del-bug-main">
                <span class="del-case-title">${this.esc(c.title || "Case")}</span>
                ${this.badge(c.status)}
              </div>
              ${c.body ? `<p class="muted del-bug-body">${this.esc(c.body)}</p>` : ""}
            </li>`,
            )
            .join("")}
        </ul>`
      : `<p class="muted del-bug-empty">No cases yet — use AI assist to draft.</p>`;
    const bugs = this.buglistForBind(bind.engId, bind.major, bind.minor);
    const hasVerified = bugs.some((b) => b.status === "verified");
    const hasWorker = !!(m.execute_worker_id || "").trim();
    const campJob = (m.campaign_job_id || "").trim();
    const campSt = (m.campaign_job_status || "").toLowerCase();
    const campBusy = campSt === "running" || campSt === "queued";
    const openGates = this.designOpenGateCount(des);
    const canRun = hasWorker && cases.length > 0 && !campBusy && openGates === 0;
    const canRegress = hasWorker && hasVerified && !campBusy;
    const canPass = this.campaignPassReady(cases, bugs);
    const bugRows = bugs.length
      ? `<ul class="del-bug-list">
          ${bugs
            .map((b) => {
              const bugBusy =
                b.job_status === "running" || b.job_status === "queued";
              const canFix =
                (b.status === "open" || b.status === "fixing") && !bugBusy;
              return `
            <li class="del-bug-row">
              <div class="del-bug-main">
                <span class="del-bug-title">${this.esc(b.title || "Bug")}</span>
                ${this.badge(b.status)}
                ${
                  canFix
                    ? `<button type="button" class="del-icon-btn" data-bug-fix="${this.esc(b.id)}"
                         title="Worker fix" aria-label="Fix">▶</button>`
                    : bugBusy
                      ? `<span class="muted">fixing…</span>`
                      : ""
                }
              </div>
              ${b.body ? `<p class="muted del-bug-body">${this.esc(b.body)}</p>` : ""}
            </li>`;
            })
            .join("")}
        </ul>`
      : `<p class="muted del-bug-empty">No buglist items yet.</p>`;
    const campStrip = campBusy
      ? `<div class="del-job-strip" data-campaign-snip>${this.esc(m.campaign_job_kind || "job")} · ${this.esc(campSt)}${m.campaign_job_last_event ? " · " + this.esc(String(m.campaign_job_last_event).slice(0, 120)) : ""}</div>`
      : campSt === "error"
        ? `<div class="del-job-strip muted">${this.esc(m.campaign_job_last_event || "campaign job error")}</div>`
        : "";

    return `
      <div class="del-test-card" data-test-card="${this.esc(m.id)}" data-ms-card="${this.esc(m.id)}"
        data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
        data-campaign-job="${this.esc(campJob)}" data-campaign-job-status="${this.esc(campSt)}">
        <div class="del-rm-ctrl-top">
          <div class="del-rm-ctrl-id">
            <strong class="del-rm-card-title">${this.esc(m.title)}</strong>
            <div class="del-rm-card-meta muted">
              ${r.msKey ? `<code>${this.esc(r.msKey)}</code>` : ""}
              ${r.project ? `<span>· ${this.esc(r.project)}</span>` : ""}
              ${r.worker ? `<span>· Worker ${this.esc(r.worker)}</span>` : `<span class="err">· No Worker</span>`}
            </div>
          </div>
          ${this.badge("testing")}
        </div>
        ${campStrip}
        <div class="del-rm-ctrl-links">
          ${desHref ? `<a href="${desHref}">Design</a>` : `<span class="muted">No design</span>`}
          ${reqHref ? `<a href="${reqHref}">Requirements</a>` : `<span class="muted">No req</span>`}
          ${rmHref ? `<a href="${rmHref}">Roadmap</a>` : ""}
        </div>
        <div class="del-test-accept-block">
          <div class="del-polish-head muted">Acceptance / success</div>
          ${acceptanceBlock}
        </div>
        <label class="muted del-test-note-label">Test note</label>
        <textarea class="del-test-note" data-test-note="${this.esc(m.id)}" rows="2"
          placeholder="Optional note for this test pass…">${this.esc(m.test_note || "")}</textarea>
        <div class="del-case-block">
          <div class="del-campaign-head">
            <span class="del-polish-head muted">Cases</span>
            <button type="button" class="ghost" data-test-ai-cases
              data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
              data-title="${this.esc(m.title)}" data-accept="${this.esc(success.slice(0, 400))}">Draft cases</button>
          </div>
          ${caseRows}
        </div>
        <div class="del-rm-ctrl-btns del-test-actions">
          <button type="button" class="del-run-btn" data-test-run
            data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
            ${canRun ? "" : "disabled"}
            title="${!hasWorker ? "Bind Worker on Roadmap first" : openGates ? "Close Design Gate checkboxes first" : campBusy ? "Campaign job running" : "Enqueue Worker test run Job"}">▶ Run tests</button>
          <button type="button" class="del-run-btn ghost" data-test-regress
            data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
            ${canRegress ? "" : "disabled"}
            title="${!hasWorker ? "Bind Worker first" : !hasVerified ? "Verify bugs first" : "Regress"}">↻ Regress</button>
          ${
            campBusy
              ? `<button type="button" class="del-run-btn ghost" data-test-cancel="${this.esc(m.id)}"
                   title="Cancel campaign job">⏹ Cancel</button>`
              : ""
          }
          <button type="button" class="del-run-btn" data-test-pass="${this.esc(m.id)}"
            ${canPass ? "" : "disabled"} title="${canPass ? "Complete milestone" : "Need clean campaign (≥1 case, all passed, no open bugs)"}">✓ Pass</button>
          <button type="button" class="del-run-btn ghost danger" data-test-fail="${this.esc(m.id)}"
            data-title="${this.esc(m.title)}" ${campBusy ? "disabled" : ""}>✗ Fail</button>
        </div>
        <div class="del-bug-block">
          <div class="del-polish-head muted">Buglist</div>
          ${bugRows}
        </div>
      </div>`;
  },

  bindTestCardControls(el) {
    el.querySelectorAll("[data-test-note]").forEach((ta) => {
      let timer = null;
      ta.addEventListener("input", () => {
        const msId = ta.getAttribute("data-test-note");
        clearTimeout(timer);
        timer = setTimeout(async () => {
          try {
            await api.v1.deliveryPatchMilestone(msId, {
              test_note: ta.value || "",
            });
          } catch (e) {
            this.showNotice(e.message || String(e), "Could not save note");
          }
        }, 500);
      });
    });
    el.querySelectorAll("[data-test-ai-cases]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.openTestCasesModal({
          engId: btn.getAttribute("data-eng"),
          major: Number(btn.getAttribute("data-major")),
          minor: Number(btn.getAttribute("data-minor")),
          msTitle: btn.getAttribute("data-title") || "",
          acceptance: btn.getAttribute("data-accept") || "",
        });
      });
    });
    el.querySelectorAll("[data-test-run]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try {
          const body = {
            eng_req_id: btn.getAttribute("data-eng"),
            major: Number(btn.getAttribute("data-major")),
            minor: Number(btn.getAttribute("data-minor")),
          };
          if (this.deliveryForceStub()) body.force_stub = true;
          const res = await api.v1.deliveryRunTestCases(body);
          if (res?.job_id) {
            await this.awaitDeliveryJob(res.job_id);
          }
          await this.reload();
          this.render();
          this.showNotice("Test run Job finished.", "Run tests");
        } catch (e) {
          this.showNotice(e.message || String(e), "Run failed");
          await this.reload();
          this.render();
        }
      });
    });
    el.querySelectorAll("[data-test-cancel]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-test-cancel");
        btn.disabled = true;
        try {
          await api.v1.deliveryPatchMilestone(msId, { run_state: "stopped" });
          await this.reload();
          this.render();
          this.showNotice("Campaign job cancelled.", "Cancel");
        } catch (e) {
          this.showNotice(e.message || String(e), "Cancel failed");
          await this.reload();
          this.render();
        }
      });
    });
    el.querySelectorAll("[data-bug-fix]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try {
          const id = btn.getAttribute("data-bug-fix");
          const body = this.deliveryForceStub() ? { force_stub: true } : {};
          const res = await api.v1.deliveryFixBuglist(id, body);
          if (res?.job_id) {
            await this.awaitDeliveryJob(res.job_id);
          }
          await this.reload();
          this.render();
          this.showNotice("Fix Job finished → verified. Run Regress next.", "Fix");
        } catch (e) {
          this.showNotice(e.message || String(e), "Fix failed");
          await this.reload();
          this.render();
        }
      });
    });
    el.querySelectorAll("[data-test-regress]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try {
          const body = {
            eng_req_id: btn.getAttribute("data-eng"),
            major: Number(btn.getAttribute("data-major")),
            minor: Number(btn.getAttribute("data-minor")),
          };
          if (this.deliveryForceStub()) body.force_stub = true;
          const res = await api.v1.deliveryRegressBuglist(body);
          if (res?.job_id) {
            await this.awaitDeliveryJob(res.job_id);
          }
          await this.reload();
          this.render();
          this.showNotice("Regression Job finished.", "Regress");
        } catch (e) {
          this.showNotice(e.message || String(e), "Regress failed");
          await this.reload();
          this.render();
        }
      });
    });
    el.querySelectorAll("[data-test-pass]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-test-pass");
        const note = el.querySelector(
          `[data-test-note="${msId}"]`,
        )?.value;
        btn.disabled = true;
        try {
          const body = { pass_test: true };
          if (note != null) body.test_note = note;
          await api.v1.deliveryPatchMilestone(msId, body);
          await this.reload();
          this.render();
          this.showNotice("Marked complete — Accept on Roadmap when ready.", "Pass");
        } catch (e) {
          this.showNotice(e.message || String(e), "Pass failed");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-test-fail]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const msId = btn.getAttribute("data-test-fail");
        const msTitle = btn.getAttribute("data-title") || "";
        this.openFailTestModal({ msId, msTitle });
      });
    });
  },

  openTestCasesModal({ engId, major, minor, msTitle, acceptance }) {
    const key = `tcases:${engId}:${major}.${minor}`;
    const chat = this.polishChat(key, {
      id: "tc-cases-chat",
      title: "Chat",
      emptyHint: "Describe coverage to draft. Save turns AI replies into ready cases.",
      tall: false,
      rows: 2,
      allowDrop: false,
      onSend: async ({ composed }, c) => {
        const hint = String(composed || "").trim().slice(0, 200);
        const suggestion =
          `Case: ${hint || "Acceptance check"}\n` +
          `Given acceptance for ${msTitle || `${major}.${minor}`}` +
          (acceptance ? ` — ${acceptance.slice(0, 120)}` : "") +
          `\nWhen exercised\nThen expect pass criteria.`;
        c.appendMessage("ai", suggestion);
      },
    });
    this.showUiModal(`
      <div class="modal-card modal-wide del-cases-modal">
        <h3>Draft cases · ${this.esc(msTitle || `${major}.${minor}`)}</h3>
        <p class="muted del-cases-lead">Use the chat below. Each AI reply becomes one case (or split a reply with <code>---</code>).</p>
        <div id="tc-chat-host" class="del-cases-chat">${chat.render()}</div>
        <div class="err" id="tc-err"></div>
        <div class="toolbar del-cases-footer">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="tc-save">Save cases</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    chat.mount(modal);
    modal.querySelector("#tc-save").onclick = async () => {
      const err = modal.querySelector("#tc-err");
      if (err) err.textContent = "";
      const aiTexts = (chat.messages || [])
        .filter((m) => m.role === "ai" && String(m.text || "").trim())
        .map((m) => String(m.text).trim());
      const parts = [];
      for (const t of aiTexts) {
        const chunks = t.split(/\n---+\n/).map((p) => p.trim()).filter(Boolean);
        if (chunks.length) parts.push(...chunks);
      }
      const cases = parts.map((p, i) => {
        const lines = p.split("\n");
        const first = (lines[0] || "").replace(/^Case:\s*/i, "").trim();
        return {
          title: first || `Case ${i + 1}`,
          body: p,
        };
      });
      if (!cases.length) {
        if (err) err.textContent = "Chat with the assistant first, then Save.";
        return;
      }
      try {
        await api.v1.deliveryCreateTestCases({
          eng_req_id: engId,
          major,
          minor,
          cases,
        });
        this.closeUiModal();
        if (this._chats) delete this._chats[key];
        await this.reload();
        this.render();
        this.showNotice(`Saved ${cases.length} case(s).`, "Cases");
      } catch (e) {
        if (err) err.textContent = e.message || String(e);
      }
    };
  },

  openFailTestModal({ msId, msTitle }) {
    this.showUiModal(`
      <div class="modal-card del-fail-modal">
        <h3 style="margin-top:0">Fail · ${this.esc(msTitle || msId)}</h3>
        <p class="muted" style="margin-top:0">Creates a buglist stub and returns the milestone to awaiting roadmap.</p>
        <label>Title</label>
        <input id="fail-title" placeholder="Short bug title" value="Test fail: ${this.esc(msTitle || "")}" />
        <label>Details</label>
        <textarea id="fail-body" rows="4" placeholder="What failed…"></textarea>
        <div class="err" id="fail-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="fail-save">Fail &amp; return</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    modal.querySelector("#fail-save").onclick = async () => {
      const err = modal.querySelector("#fail-err");
      if (err) err.textContent = "";
      const title = (modal.querySelector("#fail-title")?.value || "").trim();
      const body = (modal.querySelector("#fail-body")?.value || "").trim();
      if (!title && !body) {
        if (err) err.textContent = "Add a title or details.";
        return;
      }
      try {
        await api.v1.deliveryPatchMilestone(msId, {
          fail_test: true,
          fail_title: title || body.slice(0, 48),
          fail_body: body,
        });
        this.closeUiModal();
        await this.reload();
        this.render();
        this.showNotice(
          "Buglist + bugfix phases seeded · awaiting roadmap — use Ready then Start.",
          "Fail",
        );
      } catch (e) {
        if (err) err.textContent = e.message || String(e);
      }
    };
  },
});
