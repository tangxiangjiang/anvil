// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Roadmap surface: milestone list and control cards.
 *
 * Part of the Delivery feature object: assigns onto `window.Features.delivery`.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  closeRoadmapJobStreams() {
    if (this._jobAbort) {
      for (const a of Object.values(this._jobAbort)) {
        try {
          a.abort();
        } catch (_) {}
      }
    }
    this._jobAbort = {};
  },

  bindRoadmapJobStreams(el) {
    this.closeRoadmapJobStreams();
    this._jobAbort = {};
    el.querySelectorAll("[data-ms-card]").forEach((card) => {
      const run = (card.getAttribute("data-run-state") || "").toLowerCase();
      const jobId = (card.getAttribute("data-active-job") || "").trim();
      if (run !== "running" || !jobId) return;
      if (this._jobAbort[jobId]) return;
      const ac = new AbortController();
      this._jobAbort[jobId] = ac;
      const snip = card.querySelector("[data-job-snip]");
      const label = card.getAttribute("data-ms-title") || "";
      void this.watchDeliveryJobSSE(jobId, ac.signal, {
        onEvent: (data) => {
          const text = data.text || data.content || data.type || "";
          if (snip && text) snip.textContent = String(text).slice(0, 160);
          if (text) {
            this.setActiveJob({ id: jobId, kind: "phase", label, text: String(text).slice(0, 200) });
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

  async watchDeliveryJobSSE(jobId, signal, { onEvent, onDone }) {
    try {
      const tok = api.getToken?.() || localStorage.getItem("anvil_web_token") || "";
      const res = await fetch(api.v1.deliveryJobEventsURL(jobId), {
        headers: tok ? { Authorization: "Bearer " + tok } : {},
        signal,
      });
      if (!res.ok || !res.body) {
        if (onDone) await onDone();
        return;
      }
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      let eventName = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        const parts = buf.split("\n");
        buf = parts.pop() || "";
        for (const line of parts) {
          if (line.startsWith("event:")) {
            eventName = line.slice(6).trim();
            continue;
          }
          if (line.startsWith("data:")) {
            const raw = line.slice(5).trim();
            let data = {};
            try {
              data = JSON.parse(raw);
            } catch (_) {
              data = { text: raw };
            }
            if (eventName === "job.event" && onEvent) onEvent(data);
            if (eventName === "job.done" || eventName === "error") {
              if (onDone) await onDone();
              return;
            }
            eventName = "";
          }
          if (line === "") eventName = "";
        }
      }
      if (onDone) await onDone();
    } catch (e) {
      if (e?.name === "AbortError") return;
      if (onDone) await onDone();
    }
  },

  bindMilestoneCardControls(el) {
    // Ask before offering: a Start that would be refused is disabled with its
    // reasons listed, rather than failing after the click.
    const annotations = [];
    el.querySelectorAll("[data-ms-start]").forEach((btn) => {
      const t = btn.getAttribute("data-ms-start");
      annotations.push({
        transition: "start_phase",
        target: t,
        selector: '[data-ms-start="' + t + '"]',
      });
    });
    el.querySelectorAll("[data-ms-submit-test]").forEach((btn) => {
      const t = btn.getAttribute("data-ms-submit-test");
      annotations.push({
        transition: "submit_test",
        target: t,
        selector: '[data-ms-submit-test="' + t + '"]',
      });
    });
    el.querySelectorAll("[data-ms-accept]").forEach((btn) => {
      const t = btn.getAttribute("data-ms-accept");
      annotations.push({
        transition: "accept_milestone",
        target: t,
        selector: '[data-ms-accept="' + t + '"]',
      });
    });
    this.runPreflight(el, annotations);

    el.querySelectorAll(".del-ms-worker").forEach((sel) => {
      sel.addEventListener("change", async () => {
        const msId = sel.getAttribute("data-ms");
        const workerId = (sel.value || "").trim();
        if (!msId) return;
        try {
          const saved = await api.v1.deliveryPatchMilestone(msId, {
            execute_worker_id: workerId,
          });
          const m = (this.data.roadmap?.milestones || []).find(
            (n) => n.id === msId,
          );
          if (m) m.execute_worker_id = saved.execute_worker_id || "";
          this.render();
        } catch (e) {
          this.showNotice(e.message || String(e), "Could not save Worker");
        }
      });
    });
    // Start enqueues a phase job. The claim key is the milestone, not the
    // button: the same Start exists in the card and in the toolbar, and a
    // double fire costs a duplicate job rather than a duplicate repaint.
    el.querySelectorAll("[data-ms-start]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const msId = btn.getAttribute("data-ms-start");
        const card = btn.closest("[data-ms-card]");
        const workerId = (card?.querySelector(".del-ms-worker")?.value || "").trim();
        const token = this.beginAction();
        return window.AnvilUI.withAction(
          btn,
          { key: "start:" + msId, runningLabel: "Starting…", host: this.uiModal(), errorTitle: "Start failed" },
          async () => {
            const body = { execute_worker_id: workerId, execute: true };
            if (this.deliveryForceStub()) body.force_stub = true;
            await api.v1.deliveryPatchMilestone(msId, body);
            await this.reload();
            this.render(token);
          },
        );
      });
    });
    // A gate check flips one checkbox. It patches the snapshot and repaints the
    // strip instead of re-fetching everything: this is the interaction an
    // operator repeats most, and it used to rebuild the whole page each time.
    el.querySelectorAll("[data-gate-check]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const designId = btn.getAttribute("data-design");
        const versionId = btn.getAttribute("data-version");
        const index = Number(btn.getAttribute("data-gate-check"));
        const done = btn.getAttribute("data-done") !== "1";
        const undo = this.applyLocal((data) => {
          const d = (data.designs || []).find((x) => x.id === designId);
          const v = (d?.versions || []).find((x) => x.id === versionId);
          if (v) v.gate = this.patchChecklistItem(v.gate || "", index, done);
        });
        this.render();
        return window.AnvilUI.withAction(
          btn,
          { key: "gate:" + designId + ":" + versionId + ":" + index, host: this.uiModal(), errorTitle: "Gate check failed" },
          async () => {
            try {
              await api.v1.deliveryToggleGateCheck(designId, versionId, { index, done });
            } catch (e) {
              undo();
              throw e;
            }
          },
        );
      });
    });
    el.querySelectorAll("[data-accept-check]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const msId = btn.getAttribute("data-ms");
        const index = Number(btn.getAttribute("data-accept-check"));
        const done = btn.getAttribute("data-done") !== "1";
        const undo = this.applyLocal((data) => {
          const m = (data.roadmap?.milestones || []).find((x) => x.id === msId);
          if (m) m.acceptance = this.patchChecklistItem(m.acceptance || "", index, done);
        });
        this.render();
        return window.AnvilUI.withAction(
          btn,
          { key: "accept:" + msId + ":" + index, host: this.uiModal(), errorTitle: "Acceptance check failed" },
          async () => {
            try {
              await api.v1.deliveryToggleAcceptanceCheck(msId, { index, done });
            } catch (e) {
              undo();
              throw e;
            }
          },
        );
      });
    });
    el.querySelectorAll("[data-ms-ready]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-ms-ready");
        btn.disabled = true;
        try {
          await api.v1.deliveryPatchMilestone(msId, { ready_dev: true });
          await this.reload();
          this.render();
          this.showNotice("Marked awaiting development — Start when ready.", "Ready");
        } catch (e) {
          this.showNotice(e.message || String(e), "Ready failed");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-ms-accept]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-ms-accept");
        btn.disabled = true;
        try {
          await api.v1.deliveryPatchMilestone(msId, { accept_milestone: true });
          await this.reload();
          this.render();
          this.showNotice("Milestone accepted — Merge to main when ready.", "Accept");
        } catch (e) {
          this.showNotice(e.message || String(e), "Accept failed");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-ms-merge]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const msId = btn.getAttribute("data-ms-merge");
        btn.disabled = true;
        try {
          const m = await api.v1.deliveryMergeMilestone(msId, {});
          await this.reload();
          this.render();
          this.showNotice(
            m.merge_note || "Merged to main.",
            m.merge_status === "merged" ? "Merge" : "Merge status",
          );
        } catch (e) {
          this.showNotice(e.message || String(e), "Merge failed");
          btn.disabled = false;
        }
      });
    });
    el.querySelectorAll("[data-ms-pause]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const msId = btn.getAttribute("data-ms-pause");
        const token = this.beginAction();
        return window.AnvilUI.withAction(
          btn,
          { key: "pause:" + msId, runningLabel: "Pausing…", host: this.uiModal(), errorTitle: "Pause failed" },
          async () => {
            await api.v1.deliveryPatchMilestone(msId, { run_state: "paused" });
            await this.reload();
            this.render(token);
          },
        );
      });
    });
    el.querySelectorAll("[data-ms-stop]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const msId = btn.getAttribute("data-ms-stop");
        const token = this.beginAction();
        return window.AnvilUI.withAction(
          btn,
          { key: "stop:" + msId, runningLabel: "Stopping…", host: this.uiModal(), errorTitle: "Stop failed" },
          async () => {
            await api.v1.deliveryPatchMilestone(msId, { run_state: "stopped" });
            await this.reload();
            this.render(token);
          },
        );
      });
    });
    el.querySelectorAll("[data-ms-submit-test]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const msId = btn.getAttribute("data-ms-submit-test");
        const token = this.beginAction();
        return window.AnvilUI.withAction(
          btn,
          { key: "submittest:" + msId, runningLabel: "Submitting…", host: this.uiModal(), errorTitle: "提交测试失败" },
          async () => {
            await api.v1.deliveryPatchMilestone(msId, { submit_test: true });
            await this.reload();
            this.render(token);
            this.showNotice("Milestone moved to Test queue.", "提交测试");
          },
        );
      });
    });
    el.querySelectorAll("[data-ms-polish]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.openPolishModal({
          engId: btn.getAttribute("data-eng"),
          major: Number(btn.getAttribute("data-major")),
          minor: Number(btn.getAttribute("data-minor")),
          msTitle: btn.getAttribute("data-title") || "",
        });
      });
    });
    el.querySelectorAll("[data-polish-run]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const id = btn.getAttribute("data-polish-run");
        btn.disabled = true;
        try {
          const polBody = this.deliveryForceStub() ? { force_stub: true } : {};
          const res = await api.v1.deliveryRunPolish(id, polBody);
          if (res?.job_id) {
            await this.awaitDeliveryJob(res.job_id);
          }
          await this.reload();
          this.render();
          this.showNotice("Polish run finished.", "Polish");
        } catch (e) {
          this.showNotice(e.message || String(e), "Polish run failed");
          btn.disabled = false;
        }
      });
    });
  },

  openPolishModal({ engId, major, minor, msTitle }) {
    const key = `polish:${engId}:${major}.${minor}`;
    const chat = this.polishChat(key, {
      id: key,
      title: "AI assist",
      emptyHint: "Describe what to polish; mock AI appends a draft into the note.",
      tall: false,
      rows: 2,
      allowDrop: false,
      onSend: async ({ composed }, c) => {
        const suggestion =
          "(mock polish) Clarify acceptance: " +
          String(composed || "").trim().slice(0, 280);
        c.appendMessage("ai", suggestion);
        const modal = this.uiModal();
        const bodyEl = modal?.querySelector("#pol-body");
        const titleEl = modal?.querySelector("#pol-title");
        if (bodyEl) {
          const cur = bodyEl.value.trim();
          bodyEl.value = cur ? cur + "\n" + suggestion : suggestion;
        }
        if (titleEl && !titleEl.value.trim()) {
          titleEl.value = "Polish " + (msTitle || `${major}.${minor}`);
        }
      },
    });
    this.showUiModal(`
      <div class="modal-card del-polish-modal">
        <h3 style="margin-top:0">Polish · ${this.esc(msTitle || `${major}.${minor}`)}</h3>
        <p class="muted" style="margin-top:0">Saved into the requirement Version and listed on the milestone card.</p>
        <label>Title</label>
        <input id="pol-title" placeholder="Short polish title" value="Polish ${this.esc(msTitle || `${major}.${minor}`)}" />
        <label>Polish note</label>
        <textarea id="pol-body" rows="5" placeholder="What to polish in requirements…"></textarea>
        <div id="pol-chat-host" class="del-polish-chat">${chat.render()}</div>
        <div class="err" id="pol-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="pol-save">Save polish</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    chat.mount(modal);
    modal.querySelector("#pol-save").onclick = async () => {
      const err = modal.querySelector("#pol-err");
      if (err) err.textContent = "";
      const title = (modal.querySelector("#pol-title")?.value || "").trim();
      const body = (modal.querySelector("#pol-body")?.value || "").trim();
      if (!title && !body) {
        if (err) err.textContent = "Add a title or polish note.";
        return;
      }
      try {
        await api.v1.deliveryCreatePolish({
          eng_req_id: engId,
          major,
          minor,
          title: title || body.slice(0, 48),
          body,
        });
        this.closeUiModal();
        if (this._chats) delete this._chats[key];
        await this.reload();
        this.render();
      } catch (e) {
        if (err) err.textContent = e.message || String(e);
      }
    };
  },

  /** Flatten cluster inventory workers for Roadmap Execute picker. */
  async loadExecuteWorkers() {
    if (this._execWorkersLoading || this._execWorkersLoaded) return;
    this._execWorkersLoading = true;
    try {
      const data = await api.v1.clusters();
      const out = [];
      const seen = new Set();
      for (const f of data.clusters || []) {
        for (const n of f.nodes || []) {
          for (const w of n.workers || []) {
            if (!w?.id || seen.has(w.id)) continue;
            seen.add(w.id);
            out.push({
              id: w.id,
              label: w.git_branch || w.name || w.project_rel || w.id,
              fleet: f.name || f.id || "",
              node: n.name || n.id || "",
              project_rel: (w.project_rel || w.project_path || "").replace(
                /^\/+|\/+$/g,
                "",
              ),
            });
          }
        }
      }
      this._execWorkers = out;
      this._execWorkersLoaded = true;
      if (this.el && this.parseRoute().section === "roadmap") {
        this.render();
      }
    } catch (_) {
      this._execWorkers = this._execWorkers || [];
      this._execWorkersLoaded = true;
    }
    this._execWorkersLoading = false;
  },

  /** Worker choices for Execute: inventory preferred, then snapshot workers. */
  executeWorkerChoices(eng) {
    const byId = new Map();
    const add = (w) => {
      if (!w?.id || String(w.id).startsWith("proj:")) return;
      if (byId.has(w.id)) return;
      byId.set(w.id, {
        id: w.id,
        label: w.label || w.id,
        fleet: w.fleet || "",
        node: w.node || "",
        project_rel: (w.project_rel || "").replace(/^\/+|\/+$/g, ""),
      });
    };
    for (const w of this._execWorkers || []) add(w);
    for (const w of this.data.workers || []) add(w);
    let list = [...byId.values()];
    const prefer =
      eng &&
      (eng.project_key || eng.project_rel || "").replace(/^\/+|\/+$/g, "");
    if (prefer) {
      const matched = list.filter(
        (w) =>
          w.project_rel === prefer ||
          w.project_rel.endsWith("/" + prefer) ||
          prefer.endsWith("/" + w.project_rel),
      );
      const rest = list.filter((w) => !matched.includes(w));
      list = matched.concat(rest);
    }
    return list;
  },

  milestoneLifecycle(eng, major, minor) {
    if (!eng) return "";
    const vers = (eng.versions || []).filter(
      (v) => v.major === major && v.minor === minor,
    );
    if (!vers.length) return "";
    return vers[0].status || "";
  },

  /** Pointer-drag milestone cards; persist x/y; click (no drag) selects. */
  viewRoadmapList(routeMajor) {
    const NODE_W = 216;
    const NODE_H = 78;
    const majLabel = this.majorLabel(routeMajor);
    const allMs = this.data.roadmap?.milestones || [];
    const edgesList = this.data.roadmap?.edges || [];
    const filter = this._rmFilter || "all";
    const rows = allMs
      .map((m) => {
        const bind = this.parseRoadmapMsBind(m.id);
        const eng = bind ? this.findEng(bind.engId) : null;
        const life = bind
          ? this.milestoneLifecycle(eng, bind.major, bind.minor)
          : m.status || "";
        const nPh = (m.phases || []).length;
        const worker =
          this.findWorker(m.execute_worker_id)?.label ||
          m.execute_worker_id ||
          "";
        const project =
          eng?.project_key ||
          eng?.title ||
          (eng ? this.findWorker(eng.worker_id)?.label : "") ||
          "";
        const msKey = bind != null ? `${bind.major}.${bind.minor}` : "";
        return { m, bind, eng, life, nPh, worker, project, msKey };
      })
      .filter((r) => {
        if (!r.bind || r.bind.major !== routeMajor) return false;
        if (filter === "all") return true;
        if (filter === "ready")
          return r.life === "awaiting_development" || r.life === "awaiting_roadmap";
        if (filter === "running") return r.life === "in_development";
        if (filter === "planned")
          return !r.life || r.life === "planned" || r.life === "awaiting_design";
        return true;
      })
      .sort((a, b) => {
        if (a.bind && b.bind && a.bind.minor !== b.bind.minor)
          return a.bind.minor - b.bind.minor;
        const ta = (a.m.title || "").toLowerCase();
        const tb = (b.m.title || "").toLowerCase();
        return ta.localeCompare(tb);
      });

    const idSet = new Set(rows.map((r) => r.m.id));
    let maxX = 640;
    let maxY = 280;
    for (const r of rows) {
      maxX = Math.max(maxX, (r.m.x || 0) + NODE_W + 48);
      maxY = Math.max(maxY, (r.m.y || 0) + NODE_H + 48);
    }
    const canvasW = Math.max(640, Math.ceil(maxX));
    const canvasH = Math.max(300, Math.ceil(maxY));

    const nodes = rows
      .map((r) => {
        const m = r.m;
        const st = (r.life || m.status || "planned").toLowerCase();
        return `<div class="del-node del-node-${this.esc(st)}" role="button" tabindex="0"
          data-ms="${this.esc(m.id)}" style="left:${m.x || 0}px;top:${m.y || 0}px;width:${NODE_W}px"
          title="Drag to arrange · click to focus card controls">
          <span class="del-node-accent" aria-hidden="true"></span>
          <span class="del-node-grip" aria-hidden="true"></span>
          <span class="del-node-title">${this.esc(m.title)}</span>
          <span class="del-node-meta">
            ${this.badge(r.life || m.status)}
            <span class="muted">${r.nPh} phase${r.nPh === 1 ? "" : "s"}</span>
          </span>
        </div>`;
      })
      .join("");

    const edges = edgesList
      .map((e) => {
        if (!idSet.has(e.from) || !idSet.has(e.to)) return "";
        const a = allMs.find((m) => m.id === e.from);
        const b = allMs.find((m) => m.id === e.to);
        if (!a || !b) return "";
        const x1 = (a.x || 0) + NODE_W;
        const y1 = (a.y || 0) + NODE_H / 2;
        const x2 = b.x || 0;
        const y2 = (b.y || 0) + NODE_H / 2;
        const mx = (x1 + x2) / 2;
        return `<path d="M ${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}"
          class="del-edge" fill="none" marker-end="url(#del-arrow)" />`;
      })
      .join("");

    const graphBody = rows.length
      ? `<svg class="del-edges" width="${canvasW}" height="${canvasH}" aria-hidden="true">
            <defs>
              <marker id="del-arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto">
                <path d="M0,0 L9,3 L0,6 Z" fill="var(--accent)" opacity="0.75" />
              </marker>
            </defs>
            ${edges}
          </svg>
          <div class="del-nodes" style="width:${canvasW}px;height:${canvasH}px">${nodes}</div>`
      : `<div class="del-canvas-empty">
           <strong>No milestones in ${this.esc(majLabel)}</strong>
           <p class="muted">Approve a Design in this generation, then <em>Bootstrap roadmap</em> — one node per milestone.</p>
           <a class="ghost" href="#/delivery/designs/m/${routeMajor}">Open Designs · ${this.esc(majLabel)}</a>
         </div>`;

    const cards = rows.length
      ? rows.map((r) => this.milestoneControlCardHtml(r, routeMajor)).join("")
      : "";

    return `
      <p class="del-crumbs">
        <a href="#/delivery/roadmap">Generations</a>
        <span class="muted">/</span>
        <span>${this.esc(majLabel)}</span>
      </p>
      <div class="toolbar del-rm-list-bar">
        <label class="muted" style="margin:0">Status</label>
        <select id="rm-filter" style="width:auto;margin:0">
          <option value="all"${filter === "all" ? " selected" : ""}>All</option>
          <option value="ready"${filter === "ready" ? " selected" : ""}>Awaiting ready / roadmap</option>
          <option value="running"${filter === "running" ? " selected" : ""}>In development</option>
          <option value="planned"${filter === "planned" ? " selected" : ""}>Planned / design</option>
        </select>
        <span class="muted" style="flex:1">${rows.length} milestone${rows.length === 1 ? "" : "s"} in ${this.esc(majLabel)}</span>
        <span class="muted" style="font-size:0.85rem">Graph · drag · controls on cards</span>
      </div>
      <div class="del-roadmap-graph card del-canvas" data-major="${routeMajor}">${graphBody}</div>
      ${
        cards
          ? `<div class="del-req-section-head" style="margin-top:1rem">
               <h3 style="margin:0">Milestones</h3>
               <span class="muted">Worker · Start / Pause / Stop · 提交测试 · Design / Req · Polish</span>
             </div>
             <div class="del-rm-ctrl-grid">${cards}</div>`
          : ""
      }`;
  },

  /** Testing queue under one generation: lifecycle `testing` only. */
  polishesForBind(engId, major, minor) {
    return (this.data.polishes || []).filter(
      (p) =>
        p.eng_req_id === engId && p.major === major && p.minor === minor,
    );
  },

  milestoneControlCardHtml(r, routeMajor) {
    const m = r.m;
    const bind = r.bind;
    const run = (m.run_state || "idle").toLowerCase();
    const life = r.life || m.status || "";
    const des = this.findDesignForMilestone(m);
    const desHref = des ? `#/delivery/designs/${this.esc(des.id)}` : "";
    const reqHref =
      bind &&
      `#/delivery/requirements/${this.esc(bind.engId)}/${bind.major}/${bind.minor}`;
    const workerChoices = this.executeWorkerChoices(r.eng);
    const curWorker = (m.execute_worker_id || "").trim();
    const optionParts = [`<option value="">Worker…</option>`];
    if (curWorker && !workerChoices.some((w) => w.id === curWorker)) {
      optionParts.push(
        `<option value="${this.esc(curWorker)}" selected>${this.esc(curWorker)} (saved)</option>`,
      );
    }
    for (const w of workerChoices) {
      const meta = [w.node, w.project_rel].filter(Boolean).join(" · ");
      const label = meta ? `${w.label} · ${meta}` : w.label;
      const sel = w.id === curWorker ? " selected" : "";
      optionParts.push(
        `<option value="${this.esc(w.id)}"${sel}>${this.esc(label)}</option>`,
      );
    }
    const phasesForStart = m.phases || [];
    const hasRunnablePhase = phasesForStart.some((p) => {
      const st = (p.status || "").toLowerCase();
      return st !== "finished" && st !== "done";
    });
    const phaseJobBusy = phasesForStart.some(
      (p) => p.job_status === "running" || p.job_status === "queued",
    );
    const nextPhaseOpenGates = this.nextPhaseOpenGateCount(m, des);
    const canStart =
      !!curWorker &&
      r.nPh > 0 &&
      hasRunnablePhase &&
      !phaseJobBusy &&
      run !== "running" &&
      nextPhaseOpenGates === 0 &&
      (life === "awaiting_development" ||
        life === "in_development" ||
        run === "paused" ||
        run === "stopped" ||
        life === "");
    const canPause = run === "running";
    const canStop = run === "running" || run === "paused";
    const showPolish =
      life === "complete" || life === "testing" || life === "done";
    const canSubmitTest = life === "in_development" && !phaseJobBusy;
    const canReady =
      life === "awaiting_roadmap" &&
      phasesForStart.some((p) => {
        const st = (p.status || "").toLowerCase();
        return st !== "finished" && st !== "done";
      });
    const acceptSt = (m.accept_status || "").toLowerCase();
    const acceptOpen = this.parseGateChecklist(m.acceptance || "").filter(
      (it) => !it.done,
    ).length;
    const canAccept =
      life === "complete" && acceptSt !== "accepted" && acceptOpen === 0;
    const mergeSt = (m.merge_status || "").toLowerCase();
    const canMerge = acceptSt === "accepted" && mergeSt !== "merged";
    const acceptStrip = this.roadmapAcceptStripHtml(m);
    const polishes =
      bind != null ? this.polishesForBind(bind.engId, bind.major, bind.minor) : [];
    const polishRows = polishes.length
      ? `<ul class="del-polish-list">
          ${polishes
            .map(
              (p) => `
            <li class="del-polish-row">
              <div class="del-polish-main">
                <span class="del-polish-title">${this.esc(p.title || "Polish")}</span>
                ${this.badge(p.status)}
              </div>
              <button type="button" class="del-icon-btn del-polish-run" data-polish-run="${this.esc(p.id)}"
                title="Run polish" aria-label="Run polish" ${p.status === "done" ? "disabled" : ""}>▶</button>
            </li>`,
            )
            .join("")}
        </ul>`
      : showPolish
        ? `<p class="muted del-polish-empty">No polish items yet.</p>`
        : "";
    const phases = m.phases || [];
    const activePh =
      phases.find(
        (p) => p.job_status === "running" || p.job_status === "queued",
      ) || phases.find((p) => p.job_id);
    const jobStrip = activePh
      ? `<div class="del-job-strip" data-job-id="${this.esc(activePh.job_id || "")}">
           <span class="muted">Phase</span>
           <span>${this.esc(activePh.title || "")}</span>
           ${this.badge(activePh.job_status || activePh.status)}
           <span class="muted del-job-snip" data-job-snip>${this.esc(activePh.job_last_event || "")}</span>
         </div>`
      : phases.length
        ? `<div class="del-job-strip muted">${phases.filter((p) => (p.status || "") === "finished" || p.job_status === "finished").length}/${phases.length} phases finished</div>`
        : "";
    const gateStrip = this.roadmapGateStripHtml(des);

    return `
      <div class="del-rm-ctrl-card" data-ms-card="${this.esc(m.id)}" id="ms-card-${this.esc(m.id)}"
        data-run-state="${this.esc(run)}" data-active-job="${this.esc(activePh?.job_id || "")}">
        <div class="del-rm-ctrl-top">
          <div class="del-rm-ctrl-id">
            <strong class="del-rm-card-title">${this.esc(m.title)}</strong>
            <div class="del-rm-card-meta muted">
              ${r.msKey ? `<code>${this.esc(r.msKey)}</code>` : ""}
              ${r.project ? `<span>· ${this.esc(r.project)}</span>` : ""}
              <span>· ${r.nPh} phase${r.nPh === 1 ? "" : "s"}</span>
              <span>· run ${this.esc(run)}</span>
            </div>
          </div>
          ${this.badge(life || m.status)}
        </div>
        ${jobStrip}
        ${gateStrip}
        ${acceptStrip}
        <div class="del-rm-ctrl-links">
          ${
            desHref
              ? `<a href="${desHref}">Design</a>`
              : `<span class="muted">No design</span>`
          }
          ${
            reqHref
              ? `<a href="${reqHref}">Requirements</a>`
              : `<span class="muted">No req bind</span>`
          }
        </div>
        <div class="del-rm-ctrl-run">
          <select class="del-ms-worker" data-ms="${this.esc(m.id)}"
            ${workerChoices.length || curWorker ? "" : "disabled"}>
            ${optionParts.join("")}
          </select>
          <div class="del-rm-ctrl-btns">
            <button type="button" class="del-run-btn" data-ms-start="${this.esc(m.id)}"
              title="${canStart ? "Start" : nextPhaseOpenGates ? "Close open gates first" : phaseJobBusy || run === "running" ? "Job already running" : !hasRunnablePhase ? "No runnable phase" : "Start"}"
              ${canStart ? "" : "disabled"}>▶ Start</button>
            <button type="button" class="del-run-btn ghost" data-ms-pause="${this.esc(m.id)}"
              title="Pause" ${canPause ? "" : "disabled"}>⏸ Pause</button>
            <button type="button" class="del-run-btn ghost" data-ms-stop="${this.esc(m.id)}"
              title="Stop" ${canStop ? "" : "disabled"}>⏹ Stop</button>
            ${
              canReady
                ? `<button type="button" class="del-run-btn" data-ms-ready="${this.esc(m.id)}"
                     title="Ready for development">Ready</button>`
                : ""
            }
            ${
              canSubmitTest
                ? `<button type="button" class="del-run-btn" data-ms-submit-test="${this.esc(m.id)}"
                     title="Submit to Test">提交测试</button>`
                : ""
            }
            ${
              canAccept
                ? `<button type="button" class="del-run-btn" data-ms-accept="${this.esc(m.id)}"
                     title="Accept milestone">✓ Accept</button>`
                : life === "complete" && acceptSt !== "accepted" && acceptOpen > 0
                  ? `<span class="muted" title="Check acceptance items first">Accept blocked</span>`
                  : ""
            }
            ${
              canMerge
                ? `<button type="button" class="del-run-btn" data-ms-merge="${this.esc(m.id)}"
                     title="Merge milestone branch into main">Merge main</button>`
                : ""
            }
            ${
              acceptSt === "accepted"
                ? `<span class="muted">Accepted${m.accepted_at ? " · " + this.esc(m.accepted_at.slice(0, 10)) : ""}${mergeSt === "merged" ? " · merged" : ""}</span>`
                : ""
            }
            ${
              showPolish && bind
                ? `<button type="button" class="ghost" data-ms-polish
                     data-eng="${this.esc(bind.engId)}" data-major="${bind.major}" data-minor="${bind.minor}"
                     data-title="${this.esc(m.title)}">Polish</button>`
                : ""
            }
          </div>
        </div>
        ${
          showPolish || polishes.length
            ? `<div class="del-polish-block">
                 <div class="del-polish-head muted">Polish</div>
                 ${polishRows}
               </div>`
            : ""
        }
      </div>`;
  },

  /** Dev: localStorage anvil_delivery_force_stub=1 → pass force_stub on Jobs. */
  deliveryForceStub() {
    try {
      return localStorage.getItem("anvil_delivery_force_stub") === "1";
    } catch (_) {
      return false;
    }
  },

  nextPhaseOpenGateCount(m, des) {
    if (!des || !m) return 0;
    const phases = m.phases || [];
    let idx = phases.findIndex((p) => {
      const st = (p.status || "").toLowerCase();
      return st !== "finished" && st !== "done";
    });
    if (idx < 0) return 0;
    const vers = des.versions || [];
    if (!vers.length) return 0;
    const v = idx < vers.length ? vers[idx] : vers[0];
    return this.parseGateChecklist(v.gate || "").filter((it) => !it.done).length;
  },

  designOpenGateCount(des) {
    if (!des) return 0;
    let n = 0;
    for (const v of des.versions || []) {
      n += this.parseGateChecklist(v.gate || "").filter((it) => !it.done).length;
    }
    return n;
  },

  /**
   * applyLocal mutates the loaded snapshot without a round trip.
   *
   * For the checkbox-shaped toggles only: a gate check changes one boolean, and
   * re-fetching the whole snapshot to learn that the boolean flipped is what made
   * the console feel heavy. Everything else keeps reconciling through reload(),
   * because the server is allowed to disagree.
   *
   * Returns an undo closure, so a failed request can put the old value back
   * before the reconciliation lands.
   */
  parseGateChecklist(gate) {
    const lines = String(gate || "").split("\n");
    const out = [];
    let idx = 0;
    for (const line of lines) {
      const m = line.match(/^\s*(?:[-*]|\d+\.)\s+\[([ xX])\]\s+(.*)$/);
      if (!m) continue;
      out.push({
        index: idx,
        done: m[1].toLowerCase() === "x",
        text: (m[2] || "").trim(),
      });
      idx += 1;
    }
    return out;
  },

  parseRoadmapMsBind(msId) {
    const id = String(msId || "");
    if (!id.startsWith("ms_")) return null;
    const rest = id.slice(3);
    const m = rest.match(/^(.*)_(\d+)_(\d+)$/);
    if (!m) return null;
    return { engId: m[1], major: Number(m[2]), minor: Number(m[3]) };
  },
});
