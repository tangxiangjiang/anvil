// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Roadmap canvas: node layout, drag, edge drawing.
 *
 * Part of the Delivery feature object: assigns onto `window.Features.delivery`.
 */
window.Features = window.Features || {};
window.Features.delivery = window.Features.delivery || {};

Object.assign(window.Features.delivery, {
  bindRoadmapCanvas(el) {
    const canvas = el.querySelector(".del-canvas");
    const nodesLayer = el.querySelector(".del-nodes");
    const svg = el.querySelector(".del-edges");
    if (!canvas || !nodesLayer) return;

    const NODE_W = 216;
    const NODE_H = 78;
    const DRAG_THRESH = 5;

    const syncLocalPos = (id, x, y) => {
      const list = this.data.roadmap?.milestones || [];
      const m = list.find((n) => n.id === id);
      if (m) {
        m.x = x;
        m.y = y;
      }
    };

    const visibleIds = new Set(
      [...el.querySelectorAll(".del-node[data-ms]")].map((n) =>
        n.getAttribute("data-ms"),
      ),
    );
    const routeMaj = el.querySelector(".del-roadmap-graph")?.getAttribute("data-major");

    const redrawEdges = () => {
      if (!svg) return;
      const milestones = (this.data.roadmap?.milestones || []).filter((m) =>
        visibleIds.has(m.id),
      );
      const edgesList = this.data.roadmap?.edges || [];
      const paths = edgesList
        .map((e) => {
          if (!visibleIds.has(e.from) || !visibleIds.has(e.to)) return "";
          const a = milestones.find((m) => m.id === e.from);
          const b = milestones.find((m) => m.id === e.to);
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
      let maxX = 640;
      let maxY = 280;
      for (const m of milestones) {
        maxX = Math.max(maxX, (m.x || 0) + NODE_W + 48);
        maxY = Math.max(maxY, (m.y || 0) + NODE_H + 48);
      }
      const canvasW = Math.max(640, Math.ceil(maxX));
      const canvasH = Math.max(300, Math.ceil(maxY));
      svg.setAttribute("width", String(canvasW));
      svg.setAttribute("height", String(canvasH));
      nodesLayer.style.width = canvasW + "px";
      nodesLayer.style.height = canvasH + "px";
      const defs = svg.querySelector("defs");
      svg.innerHTML = "";
      if (defs) svg.appendChild(defs);
      else {
        svg.insertAdjacentHTML(
          "afterbegin",
          `<defs>
            <marker id="del-arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto">
              <path d="M0,0 L9,3 L0,6 Z" fill="var(--accent)" opacity="0.75" />
            </marker>
          </defs>`,
        );
      }
      svg.insertAdjacentHTML("beforeend", paths);
    };

    el.querySelectorAll(".del-node[data-ms]").forEach((node) => {
      let dragging = false;
      let moved = false;
      let startX = 0;
      let startY = 0;
      let origLeft = 0;
      let origTop = 0;
      let pointerId = null;

      const onMove = (ev) => {
        if (!dragging || ev.pointerId !== pointerId) return;
        const dx = ev.clientX - startX;
        const dy = ev.clientY - startY;
        if (!moved && Math.hypot(dx, dy) < DRAG_THRESH) return;
        moved = true;
        node.classList.add("dragging");
        const x = Math.max(0, Math.round(origLeft + dx));
        const y = Math.max(0, Math.round(origTop + dy));
        node.style.left = x + "px";
        node.style.top = y + "px";
        const id = node.getAttribute("data-ms");
        syncLocalPos(id, x, y);
        redrawEdges();
      };

      const onUp = async (ev) => {
        if (ev.pointerId !== pointerId) return;
        dragging = false;
        pointerId = null;
        node.releasePointerCapture?.(ev.pointerId);
        window.removeEventListener("pointermove", onMove);
        window.removeEventListener("pointerup", onUp);
        window.removeEventListener("pointercancel", onUp);
        node.classList.remove("dragging");
        const id = node.getAttribute("data-ms");
        if (!moved) {
          this.focusMilestoneCard(el, id);
          return;
        }
        const x = parseFloat(node.style.left) || 0;
        const y = parseFloat(node.style.top) || 0;
        try {
          const saved = await api.v1.deliveryPatchMilestonePos(id, x, y);
          syncLocalPos(id, saved.x, saved.y);
          node.style.left = saved.x + "px";
          node.style.top = saved.y + "px";
          redrawEdges();
        } catch (e) {
          this.showNotice(e.message || String(e), "Could not save position");
          // Revert from server snapshot
          await this.reload();
          this.render();
        }
      };

      node.addEventListener("pointerdown", (ev) => {
        if (ev.button !== 0) return;
        // Allow text selection / links inside if any later — only drag from card.
        ev.preventDefault();
        dragging = true;
        moved = false;
        pointerId = ev.pointerId;
        startX = ev.clientX;
        startY = ev.clientY;
        origLeft = parseFloat(node.style.left) || 0;
        origTop = parseFloat(node.style.top) || 0;
        try {
          node.setPointerCapture(ev.pointerId);
        } catch (_) {}
        window.addEventListener("pointermove", onMove);
        window.addEventListener("pointerup", onUp);
        window.addEventListener("pointercancel", onUp);
      });
    });
  },

  focusMilestoneCard(root, msId) {
    const el = root || this.el;
    if (!el || !msId) return;
    const card = [...el.querySelectorAll("[data-ms-card]")].find(
      (c) => c.getAttribute("data-ms-card") === msId,
    );
    if (!card) return;
    card.scrollIntoView({ behavior: "smooth", block: "nearest" });
    card.classList.add("del-rm-card-focus");
    setTimeout(() => card.classList.remove("del-rm-card-focus"), 1600);
  },

  roadmapGateStripHtml(des) {
    if (!des) return "";
    const vers = des.versions || [];
    const rows = [];
    for (const v of vers) {
      const items = this.parseGateChecklist(v.gate || "");
      for (const it of items) {
        if (it.done) continue;
        rows.push(`
          <li class="del-gate-row">
            <button type="button" class="del-gate-check" data-gate-check="${it.index}"
              data-design="${this.esc(des.id)}" data-version="${this.esc(v.id)}" data-done="0"
              title="Mark done">☐</button>
            <span class="muted">${this.esc(v.title || "Version")}:</span>
            <span>${this.esc(it.text || "#" + it.index)}</span>
          </li>`);
      }
    }
    if (!rows.length) return "";
    return `<div class="del-gate-strip">
      <div class="del-polish-head muted">Open gates</div>
      <ul class="del-gate-list">${rows.join("")}</ul>
    </div>`;
  },

  roadmapAcceptStripHtml(m) {
    if (!m || (m.status || "") !== "complete") return "";
    if ((m.accept_status || "") === "accepted") return "";
    const items = this.parseGateChecklist(m.acceptance || "");
    const open = items.filter((it) => !it.done);
    if (!items.length) {
      return `<div class="del-gate-strip muted">Acceptance pending — no checklist (Accept when ready).</div>`;
    }
    if (!open.length) {
      return `<div class="del-gate-strip muted">Acceptance checklist complete — Accept when ready.</div>`;
    }
    const rows = open
      .map(
        (it) => `
      <li class="del-gate-row">
        <button type="button" class="del-gate-check" data-accept-check="${it.index}"
          data-ms="${this.esc(m.id)}" data-done="0" title="Mark done">☐</button>
        <span>${this.esc(it.text || "#" + it.index)}</span>
      </li>`,
      )
      .join("");
    return `<div class="del-gate-strip">
      <div class="del-polish-head muted">Acceptance</div>
      <ul class="del-gate-list">${rows}</ul>
    </div>`;
  },

  /** Parse bootstrap milestone id `ms_{engId}_{major}_{minor}` when present. */
  viewRoadmap(selectedId, routeMajor) {
    const milestones = this.data.roadmap?.milestones || [];
    const selected = milestones.find((m) => m.id === selectedId) || null;
    const bindGuess = selected
      ? this.parseRoadmapMsBind(selected.id)
      : this.parseRoadmapMsBind(selectedId);
    const maj =
      routeMajor != null
        ? routeMajor
        : bindGuess
          ? bindGuess.major
          : this.selectedMajor;
    const listHref =
      maj != null
        ? `#/delivery/roadmap/m/${maj}`
        : "#/delivery/roadmap";
    if (!selected) {
      return `
        <div class="toolbar del-roadmap-bar">
          <a class="ghost" href="${listHref}">← Milestones</a>
        </div>
        <div class="del-canvas-empty">
          <strong>Milestone not found</strong>
          <p class="muted">This id is not on the roadmap. Pick one from the list.</p>
          <a class="ghost" href="${listHref}">Milestones</a>
        </div>`;
    }

    const phases = selected.phases || [];
    const phaseList = phases.length
      ? phases
          .map(
            (p, i) => `
        <li class="del-phase-item">
          <span class="del-phase-ord">${i + 1}</span>
          <span class="del-phase-title">${this.esc(p.title)}</span>
          ${this.badge(p.status)}
        </li>`,
          )
          .join("")
      : `<li class="del-phase-empty muted">No phases yet — Bootstrap from Design seeds Design Versions as the phase chain under this milestone.</li>`;

    const boundDes = this.findDesignForMilestone(selected);
    const bind = this.parseRoadmapMsBind(selected.id);
    const eng = bind ? this.findEng(bind.engId) : null;
    const life = bind
      ? this.milestoneLifecycle(eng, bind.major, bind.minor)
      : selected.status || "";
    const msKey = bind ? `${bind.major}.${bind.minor}` : "";
    const reqHref =
      bind &&
      `#/delivery/requirements/${this.esc(bind.engId)}/${bind.major}/${bind.minor}`;
    const desHref = boundDes ? `#/delivery/designs/${this.esc(boundDes.id)}` : "";
    const project =
      eng?.project_key ||
      eng?.title ||
      (eng ? this.findWorker(eng.worker_id)?.label : "") ||
      "";

    const workerChoices = this.executeWorkerChoices(eng);
    const curWorker = (selected.execute_worker_id || "").trim();
    const optionParts = [`<option value="">Select Worker…</option>`];
    if (curWorker && !workerChoices.some((w) => w.id === curWorker)) {
      const orphan = this.findWorker(curWorker);
      const lab = orphan?.label || curWorker;
      optionParts.push(
        `<option value="${this.esc(curWorker)}" selected>${this.esc(lab)} (saved)</option>`,
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
    const workerSelectHtml = optionParts.join("");
    const canExecute =
      !!curWorker &&
      phases.length > 0 &&
      (life === "awaiting_development" ||
        life === "in_development" ||
        life === "");
    const execHint = !phases.length
      ? "Bootstrap phases from Designs before Execute."
      : life === "awaiting_roadmap"
        ? "Mark ready first."
        : life &&
            life !== "awaiting_development" &&
            life !== "in_development"
          ? `Lifecycle is ${this.lifecycleLabel(life)}.`
          : !curWorker
            ? "Pick an execute Worker (checkout)."
            : life === "in_development"
              ? "Already in development — re-bind Worker or Execute again to confirm."
              : "Execute advances this milestone to in development (no Job yet).";

    const markReady =
      life === "awaiting_roadmap" && bind
        ? `<button type="button" data-ms-ready data-eng="${this.esc(bind.engId)}"
             data-major="${bind.major}" data-minor="${bind.minor}">Mark ready</button>`
        : "";

    const majLabel = maj != null ? this.majorLabel(maj) : "";
    return `
      <div class="del-rm-detail">
        <p class="del-crumbs">
          <a href="#/delivery/roadmap">Generations</a>
          <span class="muted">/</span>
          ${
            maj != null
              ? `<a href="${listHref}">${this.esc(majLabel)}</a><span class="muted">/</span>`
              : ""
          }
          <span>${this.esc(selected.title)}</span>
        </p>
        <div class="toolbar del-roadmap-bar">
          <a class="ghost" href="${listHref}">← Milestones</a>
          <span class="muted" style="flex:1;text-align:right;font-size:0.85rem">
            One milestone · Design Versions land as phases
          </span>
        </div>
        <div class="del-rm-detail-grid">
          <section class="card del-rm-detail-main">
            <div class="del-inspector-kicker muted">Milestone${msKey ? ` · ${this.esc(msKey)}` : ""}</div>
            <h2 class="del-rm-detail-title">${this.esc(selected.title)}</h2>
            <div class="del-inspector-badges">${this.badge(life || selected.status)}</div>
            ${
              project
                ? `<p class="muted del-rm-detail-project">Project · ${this.esc(project)}</p>`
                : ""
            }
            ${
              selected.acceptance
                ? `<p class="del-inspector-accept">${this.esc(selected.acceptance)}</p>`
                : ""
            }
            <div class="del-inspector-links">
              ${
                desHref
                  ? `<a href="${desHref}">Open design</a>`
                  : `<span class="muted">No bound design</span>`
              }
              ${
                reqHref
                  ? `<a href="${reqHref}">Requirements ${this.esc(msKey)}</a>`
                  : ""
              }
            </div>
            ${
              markReady
                ? `<div class="del-ready-block">${markReady}
                     <p class="muted del-ready-hint">Unlocks Execute for this milestone.</p>
                   </div>`
                : ""
            }
            <div class="del-inspector-sec">
              <div class="del-inspector-sec-head">
                <h4>Phase chain</h4>
                <span class="muted">${phases.length} · from Design Versions</span>
              </div>
              <ol class="del-phases">${phaseList}</ol>
            </div>
          </section>
          <aside class="card del-inspector del-rm-detail-side">
            <div class="del-exec-block" style="border:none;margin:0;padding:0">
              <div class="del-inspector-sec-head">
                <h4>Execute</h4>
                ${life ? this.badge(life) : `<span class="muted">—</span>`}
              </div>
              <label class="del-exec-label muted" for="del-exec-worker">Worker</label>
              <select id="del-exec-worker" data-ms="${this.esc(selected.id)}"
                ${workerChoices.length || curWorker ? "" : "disabled"}>
                ${workerSelectHtml}
              </select>
              <p class="muted del-exec-hint">${this.esc(execHint)}</p>
              <button type="button" id="del-exec-run" data-ms="${this.esc(selected.id)}"
                ${canExecute ? "" : "disabled"}>Execute</button>
            </div>
          </aside>
        </div>
      </div>`;
  },
});
