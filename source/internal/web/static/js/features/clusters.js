window.Features = window.Features || {};
window.Features.clusters = {
  timer: null,
  el: null,
  _clusters: [],
  _foldNodes: {},

  parseRoute() {
    const h = location.hash || "#/clusters";
    const parts = h.replace(/^#\/?/, "").split("/").filter(Boolean);
    // clusters
    // clusters/:fleetId/workspaces
    // clusters/:fleetId/workspaces/:wsId
    // clusters/:fleetId/workspaces/:wsId/graph
    // clusters/:fleetId/workspaces/:wsId/projects/:projId
    const fleetId = parts[1] || null;
    const [reviewNodeId, reviewWorkerId] = parts[2] === "review" ? [parts[3] || null, parts[4] || null] : [null, null];
    const workspaceId = parts[2] === "workspaces" ? parts[3] || null : null;
    let view = "schedulers";
    if (parts[2] === "review" && reviewNodeId && reviewWorkerId) view = "review";
    else if (parts[2] === "workspaces" && !parts[3]) view = "workspaces";
    else if (parts[4] === "graph") view = "graph";
    else if (parts[4] === "projects" && parts[5]) view = "project";
    else if (workspaceId) view = "workspace";
    else if (parts[2] === "workspaces") view = "workspaces";
    return { fleetId, workspaceId, reviewNodeId, reviewWorkerId, view };
  },

  go(path) {
    location.hash = path;
  },

  async mount(el) {
    this.el = el;
    el.innerHTML = `<div id="c-root"></div>
      <div id="c-modal" class="modal" hidden></div>
      <div id="c-ui-modal" class="modal" hidden></div>`;
    await this.render();
    this.timer = setInterval(() => this.refreshQuiet(), 10000);
  },

  unmount() {
    window.AnvilReview?.unmount();
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    this.el = null;
  },

  onRoute() {
    this.render();
  },

  // Shared with Delivery and Nodes: one definition, in js/shared/esc.js.
  esc(s) {
    return window.AnvilUI.esc(s);
  },

  async refreshQuiet() {
    try {
      const data = await api.v1.clusters();
      this._clusters = data.clusters || [];
    } catch (_) {}
  },

  async loadClusters() {
    const data = await api.v1.clusters();
    this._clusters = data.clusters || [];
    return this._clusters;
  },

  fleet(id) {
    return (this._clusters || []).find((c) => c.id === id);
  },

  nodesForWorkspace(fleet, wsId) {
    const nodes = fleet?.nodes || [];
    const matched = nodes.filter((n) => n.workspace_id === wsId);
    if (matched.length) return matched;
    // Until nodes join with token, show all fleet nodes under every workspace.
    return nodes;
  },

  /** Prefer OS computer name (hostname), then config name, then id. */
  nodeLabel(n) {
    if (!n) return "—";
    return n.hostname || n.name || n.id || "—";
  },

  nodeSubLabel(n) {
    if (!n) return "";
    const host = n.hostname || "";
    const name = n.name || "";
    if (host && name && host !== name) return name;
    if (host) return n.id && host !== n.id ? n.id : "";
    return n.id && name !== n.id ? n.id : "";
  },

  allLiveWorkers(fleet, wsId) {
    const out = [];
    for (const n of this.nodesForWorkspace(fleet, wsId)) {
      for (const w of n.workers || []) {
        out.push({ node: n, worker: w });
      }
    }
    return out;
  },

  async render() {
    const el = this.el;
    if (!el) return;
    const root = el.querySelector("#c-root");
    const route = this.parseRoute();
    try {
      await this.loadClusters();
    } catch (e) {
      root.innerHTML = `<p class="err">${this.esc(e.message || e)}</p>`;
      return;
    }

    if (route.view === "review") {
      const fleet = this.fleet(route.fleetId);
      if (!window.AnvilReview.mountRoute(root, route, fleet)) {
        root.innerHTML = `<p class="err">The selected review worker is unavailable or outside this fleet scope.</p>`;
      }
      return;
    }

    if (route.view === "schedulers") {
      root.innerHTML = this.viewSchedulers();
      this.bindSchedulers(root);
      return;
    }
    if (route.view === "workspaces") {
      root.innerHTML = await this.viewWorkspaces(route.fleetId);
      this.bindWorkspaces(root, route.fleetId);
      return;
    }
    if (route.view === "graph") {
      root.innerHTML = await this.viewGraph(route.fleetId, route.workspaceId);
      this.bindGraph(root, route);
      return;
    }
    if (route.view === "project") {
      const projId = this.parseProjectId();
      root.innerHTML = await this.viewProject(route.fleetId, route.workspaceId, projId);
      this.bindProject(root, route.fleetId, route.workspaceId, projId);
      return;
    }
    // workspace detail
    root.innerHTML = await this.viewWorkspace(route.fleetId, route.workspaceId);
    this.bindWorkspace(root, route.fleetId, route.workspaceId);
  },

  parseProjectId() {
    const parts = (location.hash || "").replace(/^#\/?/, "").split("/").filter(Boolean);
    // clusters/:f/workspaces/:ws/projects/:p
    const i = parts.indexOf("projects");
    return i >= 0 ? parts[i + 1] : null;
  },

  viewSchedulers() {
    const cards = (this._clusters || [])
      .map((c) => {
        const n = (c.nodes || []).length;
        const online = c.online
          ? `<span class="badge ok">online</span>`
          : `<span class="badge bad">offline</span>`;
        return `
      <div class="cl-card" data-fleet="${this.esc(c.id)}" role="link" tabindex="0">
        <div class="cl-card-top">
          <strong>${this.esc(c.name || c.id)}</strong>
          ${online}
        </div>
        <div class="muted cl-card-meta">
          <code>${this.esc(c.scheduler_url || "")}</code>
        </div>
        <div class="cl-card-foot muted">${n} Node${n === 1 ? "" : "s"}
          ${c.error ? ` · <span class="err">${this.esc(c.error)}</span>` : ""}</div>
      </div>`;
      })
      .join("");
    return `
      <h2>Clusters</h2>
      <p class="muted del-sub">Fleet → Workspace → Project → Worker</p>
      <div class="toolbar"><button type="button" class="ghost" id="c-refresh">Refresh</button></div>
      <div class="cl-grid">${cards || `<p class="muted">No Fleets configured in anvil-web.</p>`}</div>`;
  },

  bindSchedulers(root) {
    root.querySelector("#c-refresh")?.addEventListener("click", () => this.render());
    root.querySelectorAll("[data-fleet]").forEach((card) => {
      card.addEventListener("click", () => {
        this.go(`#/clusters/${card.getAttribute("data-fleet")}/workspaces`);
      });
    });
  },

  async viewWorkspaces(fleetId) {
    const fleet = this.fleet(fleetId);
    let list = [];
    try {
      const data = await api.v1.listWorkspaces(fleetId);
      list = data.workspaces || [];
    } catch (e) {
      return `<p class="err">${this.esc(e.message || e)}</p>`;
    }
    const cards = list
      .map((w) => {
        const nodes = this.nodesForWorkspace(fleet, w.id);
        return `
      <div class="cl-card cl-ws-card" data-ws-open="${this.esc(w.id)}">
        <button type="button" class="ghost cl-ws-view" data-ws-view="${this.esc(w.id)}" title="Workspace graph">Graph</button>
        <div class="cl-card-top">
          <strong>${this.esc(w.name)}</strong>
        </div>
        <div class="muted cl-card-meta">Token <code class="cl-token">${this.esc(w.token)}</code></div>
        <div class="cl-card-foot muted">${nodes.length} Node${nodes.length === 1 ? "" : "s"} connected</div>
      </div>`;
      })
      .join("");
    return `
      <p class="del-crumbs">
        <a href="#/clusters">Clusters</a>
        <span class="muted">/</span>
        <span>${this.esc(fleet?.name || fleetId)}</span>
      </p>
      <div class="toolbar">
        <h2 style="margin:0;flex:1">Workspaces</h2>
        <button type="button" class="ghost" id="c-refresh">Refresh</button>
      </div>
      <div class="cl-grid">
        ${cards}
        <button type="button" class="cl-card cl-card-add" id="c-ws-add">
          <span class="del-req-plus">+</span>
          <span class="muted">New workspace</span>
        </button>
      </div>`;
  },

  bindWorkspaces(root, fleetId) {
    root.querySelector("#c-refresh")?.addEventListener("click", () => this.render());
    root.querySelector("#c-ws-add")?.addEventListener("click", () => this.openCreateWorkspace(fleetId));
    root.querySelectorAll("[data-ws-view]").forEach((btn) => {
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        this.go(`#/clusters/${fleetId}/workspaces/${btn.getAttribute("data-ws-view")}/graph`);
      });
    });
    root.querySelectorAll("[data-ws-open]").forEach((card) => {
      card.addEventListener("click", (ev) => {
        if (ev.target.closest("[data-ws-view]")) return;
        // Primary entry: Workspace View (Project + Worker topology).
        this.go(`#/clusters/${fleetId}/workspaces/${card.getAttribute("data-ws-open")}/graph`);
      });
    });
  },

  openCreateWorkspace(fleetId) {
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">New workspace</h3>
        <label>Name</label>
        <input id="c-ws-name" placeholder="e.g. IM product" />
        <label>Summary</label>
        <textarea id="c-ws-sum" rows="2" placeholder="Optional"></textarea>
        <div class="err" id="c-ws-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="c-ws-ok">Create</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    modal.querySelector("#c-ws-ok").onclick = async () => {
      const err = modal.querySelector("#c-ws-err");
      err.textContent = "";
      try {
        const ws = await api.v1.createWorkspace({
          fleet_id: fleetId,
          name: modal.querySelector("#c-ws-name").value.trim(),
          summary: modal.querySelector("#c-ws-sum").value,
        });
        this.closeUiModal();
        await this.render();
        this.showNotice("Workspace token (put in agentd cluster.workspace_token):\n\n" + ws.token, "Workspace token");
      } catch (e) {
        err.textContent = e.message || String(e);
      }
    };
  },

  async viewWorkspace(fleetId, wsId) {
    const fleet = this.fleet(fleetId);
    let ws, projects, boundIds = new Set();
    try {
      ws = await api.v1.listWorkspaces(fleetId).then((d) =>
        (d.workspaces || []).find((x) => x.id === wsId),
      );
      const pd = await api.v1.listProjects(wsId);
      projects = pd.projects || [];
      const g = await api.v1.workspaceGraph(wsId);
      boundIds = new Set((g.workers || []).map((b) => b.worker_id));
    } catch (e) {
      return `<p class="err">${this.esc(e.message || e)}</p>`;
    }
    if (!ws) return `<p class="err">Workspace not found</p>`;
    const nodes = this.nodesForWorkspace(fleet, wsId);
    const unbound = this.allLiveWorkers(fleet, wsId).filter(({ worker: w }) => !boundIds.has(w.id));
    const nodeNames = nodes
      .map((n) => this.nodeLabel(n))
      .join(", ");
    const cards = projects
      .map((p) => {
        const deps = (p.depends_on || []).length;
        return `
      <div class="cl-card" data-proj-open="${this.esc(p.id)}">
        <div class="cl-card-top">
          <strong>${this.esc(p.name)}</strong>
          <code class="muted">${this.esc(p.key)}</code>
        </div>
        <div class="muted cl-card-meta">${this.esc(p.summary || "—")}</div>
        <div class="cl-card-foot muted">${deps} dependenc${deps === 1 ? "y" : "ies"}</div>
      </div>`;
      })
      .join("");
    const orphanBanner = unbound.length
      ? `<p class="cl-orphan-banner">
          <span class="badge warn">${unbound.length} unbound</span>
          Worker${unbound.length === 1 ? "" : "s"} not on any Project
          (<code>${unbound.map((x) => x.worker.name || x.worker.id).map((s) => this.esc(s)).join("</code>, <code>")}</code>).
          Open <button type="button" class="ghost" id="c-goto-machines">Machines</button> to bind or remove.
        </p>`
      : "";
    return `
      <p class="del-crumbs">
        <a href="#/clusters">Clusters</a>
        <span class="muted">/</span>
        <a href="#/clusters/${this.esc(fleetId)}/workspaces">${this.esc(fleet?.name || fleetId)}</a>
        <span class="muted">/</span>
        <span>${this.esc(ws.name)}</span>
      </p>
      <div class="toolbar">
        <div style="flex:1">
          <h2 style="margin:0">${this.esc(ws.name)}</h2>
          <p class="muted" style="margin:0.25rem 0 0">Token <code>${this.esc(ws.token)}</code>
            · ${nodes.length} Node${nodes.length === 1 ? "" : "s"}${
              nodeNames ? ` · ${this.esc(nodeNames)}` : ""
            }</p>
        </div>
        <button type="button" class="ghost" id="c-refresh">Refresh</button>
      </div>
      ${orphanBanner}
      <div class="del-subtabs">
        <a class="del-subtab ghost" href="#/clusters/${this.esc(fleetId)}/workspaces/${this.esc(wsId)}/graph">Graph</a>
        <button type="button" class="del-subtab active" id="c-tab-projects">Projects</button>
        <button type="button" class="del-subtab ghost" id="c-tab-machines">Machines</button>
      </div>
      <div id="c-ws-main">
        <div class="cl-grid">
          ${cards}
          <button type="button" class="cl-card cl-card-add" id="c-proj-add">
            <span class="del-req-plus">+</span>
            <span class="muted">New project</span>
          </button>
        </div>
      </div>`;
  },

  bindWorkspace(root, fleetId, wsId) {
    root.querySelector("#c-refresh")?.addEventListener("click", () => this.render());
    root.querySelector("#c-proj-add")?.addEventListener("click", () =>
      this.openCreateProject(fleetId, wsId),
    );
    root.querySelectorAll("[data-proj-open]").forEach((card) => {
      card.addEventListener("click", () => {
        this.go(
          `#/clusters/${fleetId}/workspaces/${wsId}/projects/${card.getAttribute("data-proj-open")}`,
        );
      });
    });
    const showProjects = () => {
      this._openMachinesOnce = false;
      this.render();
    };
    const showMachines = async () => {
      const main = root.querySelector("#c-ws-main");
      const tabP = root.querySelector("#c-tab-projects");
      const tabM = root.querySelector("#c-tab-machines");
      tabP?.classList.remove("active");
      tabM?.classList.add("active");
      tabM?.classList.remove("ghost");
      tabP?.classList.add("ghost");
      main.innerHTML = `<p class="muted">Loading…</p>`;
      let boundIds = new Set();
      let projects = [];
      try {
        const g = await api.v1.workspaceGraph(wsId);
        boundIds = new Set((g.workers || []).map((b) => b.worker_id));
        const pd = await api.v1.listProjects(wsId);
        projects = pd.projects || [];
      } catch (e) {
        main.innerHTML = `<p class="err">${this.esc(e.message || e)}</p>`;
        return;
      }
      const fleet = this.fleet(fleetId);
      const nodes = this.nodesForWorkspace(fleet, wsId);
      main.innerHTML = this.renderMachines(fleetId, wsId, nodes, boundIds, projects);
      this.bindMachines(main, fleetId, wsId, projects);
    };
    root.querySelector("#c-tab-machines")?.addEventListener("click", showMachines);
    root.querySelector("#c-goto-machines")?.addEventListener("click", showMachines);
    root.querySelector("#c-tab-projects")?.addEventListener("click", showProjects);
    if (this._openMachinesOnce) {
      this._openMachinesOnce = false;
      showMachines();
    }
  },

  openEditProjectDeps(fleetId, wsId, projId) {
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Project depends_on</h3>
        <p class="muted">Select projects this one depends on (outgoing only).</p>
        <div id="c-proj-deps" class="cl-deps"></div>
        <div class="err" id="c-proj-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="c-proj-ok">Save</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    Promise.all([api.v1.listProjects(wsId), api.v1.getProject(wsId, projId)]).then(([d, cur]) => {
      const selected = new Set(cur.project?.depends_on || []);
      const box = modal.querySelector("#c-proj-deps");
      const list = (d.projects || []).filter((p) => p.id !== projId);
      if (!list.length) {
        box.innerHTML = `<p class="muted">No other projects yet.</p>`;
        return;
      }
      box.innerHTML = list
        .map(
          (p) =>
            `<label class="cl-dep-row"><input type="checkbox" value="${this.esc(p.id)}" ${
              selected.has(p.id) ? "checked" : ""
            } /> ${this.esc(p.name)}</label>`,
        )
        .join("");
    });
    modal.querySelector("#c-proj-ok").onclick = async () => {
      const err = modal.querySelector("#c-proj-err");
      err.textContent = "";
      const depends_on = [...modal.querySelectorAll("#c-proj-deps input:checked")].map((x) => x.value);
      try {
        await api.v1.patchProject(wsId, projId, { depends_on });
        this.closeUiModal();
        await this.render();
      } catch (e) {
        err.textContent = e.message || String(e);
      }
    };
  },

  renderMachines(fleetId, wsId, nodes, boundIds, projects) {
    if (!nodes.length) {
      return `<p class="muted">No Nodes yet. Set <code>cluster.workspace_token</code> on agentd and restart.</p>`;
    }
    const bound = boundIds || new Set();
    return nodes
      .map((n) => {
        const id = n.id;
        const open = this._foldNodes[id] !== false;
        const workers = n.workers || [];
        const unboundN = workers.filter((w) => !bound.has(w.id)).length;
        const sub = this.nodeSubLabel(n);
        const body = workers
          .map((w) => {
            const orphan = !bound.has(w.id);
            const status = w.status || "—";
            return `
        <div class="cl-machine-worker ${orphan ? "cl-machine-worker-orphan" : ""}">
          <div class="cl-machine-worker-main">
            <strong>${this.esc(w.name || w.id)}</strong>
            <code class="muted">${this.esc(w.project_rel || w.project_path || "—")}</code>
            <span class="badge">${this.esc(status)}</span>
            ${orphan ? `<span class="badge warn">no Project</span>` : ""}
          </div>
          <div class="toolbar cl-machine-worker-actions">
            ${
              orphan
                ? `<button type="button" class="ghost" data-bind-orphan="${this.esc(w.id)}" data-node="${this.esc(n.id)}">Bind to Project</button>`
                : ""
            }
            <button type="button" class="ghost danger" data-rm-orphan="${this.esc(w.id)}" data-node="${this.esc(n.id)}">Remove</button>
          </div>
        </div>`;
          })
          .join("") || `<p class="muted">No Workers on this Node.</p>`;
        return `
      <div class="cl-node-fold">
        <button type="button" class="cl-node-fold-btn" data-fold="${this.esc(id)}">
          <span>${open ? "▼" : "▶"}</span>
          <strong title="${this.esc(n.id)}">${this.esc(this.nodeLabel(n))}</strong>
          ${sub ? `<span class="muted">${this.esc(sub)}</span>` : ""}
          <span class="muted">${workers.length} worker${workers.length === 1 ? "" : "s"}${
            unboundN ? ` · ${unboundN} unbound` : ""
          }</span>
        </button>
        <div class="cl-node-fold-body" ${open ? "" : "hidden"}>${body}</div>
      </div>`;
      })
      .join("");
  },

  bindMachines(main, fleetId, wsId, projects) {
    main.querySelectorAll("[data-fold]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const id = btn.getAttribute("data-fold");
        this._foldNodes[id] = this._foldNodes[id] === false;
        const body = btn.parentElement?.querySelector(".cl-node-fold-body");
        const open = this._foldNodes[id] !== false;
        if (body) body.hidden = !open;
        const chevron = btn.querySelector("span");
        if (chevron) chevron.textContent = open ? "▼" : "▶";
      });
    });
    main.querySelectorAll("[data-bind-orphan]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.openBindOrphan(
          fleetId,
          wsId,
          btn.getAttribute("data-node"),
          btn.getAttribute("data-bind-orphan"),
          projects,
        );
      });
    });
    main.querySelectorAll("[data-rm-orphan]").forEach((btn) => {
      btn.addEventListener("click", () => {
        this.confirmRemoveWorker(
          fleetId,
          wsId,
          null,
          btn.getAttribute("data-node"),
          btn.getAttribute("data-rm-orphan"),
        );
      });
    });
  },

  openBindOrphan(fleetId, wsId, nodeId, workerId, projects) {
    const list = projects || [];
    if (!list.length) {
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">No projects</h3>
          <p class="muted">Create a Project first, then bind this worker.</p>
          <div class="toolbar" style="justify-content:flex-end"><button type="button" data-close-ui>OK</button></div>
        </div>`);
      return;
    }
    const opts = list
      .map((p) => `<option value="${this.esc(p.id)}">${this.esc(p.name)} (${this.esc(p.key)})</option>`)
      .join("");
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Bind Worker to Project</h3>
        <p class="muted">Worker <code>${this.esc(workerId)}</code> on <code>${this.esc(nodeId)}</code></p>
        <label>Project</label>
        <select id="c-bind-proj">${opts}</select>
        <div class="err" id="c-bind-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="c-bind-ok">Bind</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    modal.querySelector("#c-bind-ok").onclick = async () => {
      const err = modal.querySelector("#c-bind-err");
      err.textContent = "";
      const projId = modal.querySelector("#c-bind-proj").value;
      try {
        await api.v1.bindWorker(wsId, projId, {
          worker_id: workerId,
          fleet_id: fleetId,
          node_id: nodeId,
        });
        this.closeUiModal();
        await this.render();
      } catch (e) {
        err.textContent = e.message || String(e);
      }
    };
  },

  openCreateProject(fleetId, wsId) {
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">New project</h3>
        <label>Name</label>
        <input id="c-proj-name" placeholder="flutter-chat" />
        <label>Key (optional)</label>
        <input id="c-proj-key" placeholder="auto from name" />
        <label>Summary</label>
        <textarea id="c-proj-sum" rows="2"></textarea>
        <label>Depends on (other projects in this workspace)</label>
        <div id="c-proj-deps" class="cl-deps"></div>
        <div class="err" id="c-proj-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="c-proj-ok">Create</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    api.v1.listProjects(wsId).then((d) => {
      const box = modal.querySelector("#c-proj-deps");
      const list = d.projects || [];
      if (!list.length) {
        box.innerHTML = `<p class="muted">No other projects yet.</p>`;
        return;
      }
      box.innerHTML = list
        .map(
          (p) =>
            `<label class="cl-dep-row"><input type="checkbox" value="${this.esc(p.id)}" /> ${this.esc(p.name)}</label>`,
        )
        .join("");
    });
    modal.querySelector("#c-proj-ok").onclick = async () => {
      const err = modal.querySelector("#c-proj-err");
      err.textContent = "";
      const depends_on = [...modal.querySelectorAll("#c-proj-deps input:checked")].map(
        (x) => x.value,
      );
      try {
        const p = await api.v1.createProject(wsId, {
          name: modal.querySelector("#c-proj-name").value.trim(),
          key: modal.querySelector("#c-proj-key").value.trim(),
          summary: modal.querySelector("#c-proj-sum").value,
          depends_on,
        });
        this.closeUiModal();
        this.go(`#/clusters/${fleetId}/workspaces/${wsId}/projects/${p.id}`);
      } catch (e) {
        err.textContent = e.message || String(e);
      }
    };
  },

  async viewGraph(fleetId, wsId) {
    const fleet = this.fleet(fleetId);
    let g, ws;
    try {
      const [gd, wd] = await Promise.all([
        api.v1.workspaceGraph(wsId),
        api.v1.listWorkspaces(fleetId),
      ]);
      g = gd;
      ws = (wd.workspaces || []).find((x) => x.id === wsId);
    } catch (e) {
      return `<p class="err">${this.esc(e.message || e)}</p>`;
    }
    const projects = g.projects || [];
    const edges = g.edges || [];
    const bindings = g.workers || [];
    const workerEdges = g.worker_edges || [];
    const liveById = {};
    for (const { node, worker } of this.allLiveWorkers(fleet, wsId)) {
      liveById[worker.id] = { worker, node };
    }

    const colW = 240;
    const startX = 48;
    const projY = 48;
    const workerH = 56;
    const workerGap = 10;
    const projH = 64;
    let maxBottom = 200;
    const projPos = {};
    const workerPos = {};

    const projNodes = projects
      .map((p, i) => {
        const x = startX + (i % 4) * colW;
        const y = projY + Math.floor(i / 4) * 280;
        projPos[p.id] = { x, y, cx: x + 100, cy: y + projH / 2 };
        const bound = bindings.filter((b) => b.project_id === p.id);
        const workerHtml = bound
          .map((b, wi) => {
            const live = liveById[b.worker_id];
            const w = live?.worker;
            const n = live?.node;
            const wx = x + 12;
            const wy = y + projH + 12 + wi * (workerH + workerGap);
            workerPos[b.worker_id] = { x: wx, y: wy, cx: wx + 88, cy: wy + workerH / 2 };
            maxBottom = Math.max(maxBottom, wy + workerH + 40);
            const branch = w?.git_branch || "—";
            const status = w?.status || "offline";
            const host = n ? this.nodeLabel(n) : b.node_id || "—";
            return `
          <div class="cl-graph-worker" data-worker="${this.esc(b.worker_id)}" data-proj="${this.esc(p.id)}"
            style="left:${wx}px;top:${wy}px" title="${this.esc([w?.project_rel || "", host, b.worker_id].filter(Boolean).join("\n"))}">
            <div class="cl-graph-worker-top">
              <strong>${this.esc(w?.name || b.worker_id)}</strong>
              <span class="badge">${this.esc(status)}</span>
            </div>
            <div class="muted cl-graph-worker-meta">
              <code>${this.esc(branch)}</code> · ${this.esc(host)}
            </div>
          </div>`;
          })
          .join("");
        if (!bound.length) {
          maxBottom = Math.max(maxBottom, y + projH + 48);
        } else {
          maxBottom = Math.max(maxBottom, y + projH + 12 + bound.length * (workerH + workerGap) + 24);
        }
        return `
        <div class="cl-graph-node cl-graph-project" data-proj="${this.esc(p.id)}"
          style="left:${x}px;top:${y}px">
          <div class="del-node-title">${this.esc(p.name)}</div>
          <div class="muted"><code>${this.esc(p.key)}</code> · ${bound.length} worker${bound.length === 1 ? "" : "s"}</div>
        </div>
        ${workerHtml}`;
      })
      .join("");

    const canvasW = Math.max(900, startX + Math.min(projects.length, 4) * colW + 40);
    const canvasH = Math.max(420, maxBottom);

    const projLines = edges
      .map((e) => {
        const a = projPos[e.from];
        const b = projPos[e.to];
        if (!a || !b) return "";
        return `<line x1="${a.cx}" y1="${a.cy}" x2="${b.x}" y2="${b.cy}" class="cl-edge-project" marker-end="url(#cl-arrow)" />`;
      })
      .join("");
    const wLines = workerEdges
      .map((e) => {
        const a = workerPos[e.from];
        const b = workerPos[e.to];
        if (!a || !b) return "";
        return `<line x1="${a.cx}" y1="${a.cy}" x2="${b.cx}" y2="${b.cy}" class="cl-edge-worker" marker-end="url(#cl-arrow-w)" />`;
      })
      .join("");

    const unbound = this.allLiveWorkers(fleet, wsId).filter(
      ({ worker: w }) => !bindings.some((b) => b.worker_id === w.id),
    );
    const orphanNote = unbound.length
      ? `<p class="cl-orphan-banner"><span class="badge warn">${unbound.length} unbound</span>
          See <a href="#/clusters/${this.esc(fleetId)}/workspaces/${this.esc(wsId)}">Projects / Machines</a> to bind or remove.</p>`
      : "";

    return `
      <p class="del-crumbs">
        <a href="#/clusters">Clusters</a>
        <span class="muted">/</span>
        <a href="#/clusters/${this.esc(fleetId)}/workspaces">${this.esc(fleet?.name || fleetId)}</a>
        <span class="muted">/</span>
        <span>${this.esc(ws?.name || wsId)}</span>
      </p>
      <div class="toolbar">
        <div style="flex:1">
          <h2 style="margin:0">Workspace View</h2>
          <p class="muted" style="margin:0.25rem 0 0">Projects + Workers topology (read-only graph)</p>
        </div>
        <button type="button" class="ghost" id="c-refresh">Refresh</button>
      </div>
      ${orphanNote}
      <div class="del-subtabs">
        <span class="del-subtab active">Graph</span>
        <a class="del-subtab ghost" href="#/clusters/${this.esc(fleetId)}/workspaces/${this.esc(wsId)}">Projects</a>
        <a class="del-subtab ghost" href="#/clusters/${this.esc(fleetId)}/workspaces/${this.esc(wsId)}?tab=machines" id="c-graph-machines">Machines</a>
      </div>
      <p class="muted cl-graph-legend">
        <span class="cl-legend-item"><i class="cl-legend-solid"></i> Project depends_on</span>
        <span class="cl-legend-item"><i class="cl-legend-dash"></i> Worker depends_on</span>
      </p>
      <div class="del-canvas cl-graph-canvas" style="min-height:${canvasH}px">
        <svg class="del-edges" width="${canvasW}" height="${canvasH}">
          <defs>
            <marker id="cl-arrow" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
              <path d="M0,0 L6,3 L0,6 Z" fill="currentColor" /></marker>
            <marker id="cl-arrow-w" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
              <path d="M0,0 L6,3 L0,6 Z" fill="var(--muted)" /></marker>
          </defs>
          ${projLines}
          ${wLines}
        </svg>
        <div class="del-nodes" style="width:${canvasW}px;height:${canvasH}px">
          ${projNodes || `<p class="muted" style="padding:1rem">No projects yet — open Projects to create one.</p>`}
        </div>
      </div>`;
  },

  bindGraph(root, route) {
    root.querySelector("#c-refresh")?.addEventListener("click", () => this.render());
    root.querySelector("#c-graph-machines")?.addEventListener("click", (ev) => {
      ev.preventDefault();
      this._openMachinesOnce = true;
      this.go(`#/clusters/${route.fleetId}/workspaces/${route.workspaceId}`);
    });
    root.querySelectorAll(".cl-graph-project[data-proj]").forEach((n) => {
      n.addEventListener("click", () => {
        this.go(
          `#/clusters/${route.fleetId}/workspaces/${route.workspaceId}/projects/${n.getAttribute("data-proj")}`,
        );
      });
    });
    root.querySelectorAll(".cl-graph-worker[data-proj]").forEach((n) => {
      n.addEventListener("click", () => {
        this.go(
          `#/clusters/${route.fleetId}/workspaces/${route.workspaceId}/projects/${n.getAttribute("data-proj")}`,
        );
      });
    });
  },

  async viewProject(fleetId, wsId, projId) {
    const fleet = this.fleet(fleetId);
    let proj, bindings;
    try {
      const data = await api.v1.getProject(wsId, projId);
      proj = data.project;
      bindings = data.bindings || [];
    } catch (e) {
      return `<p class="err">${this.esc(e.message || e)}</p>`;
    }
    const bindSet = new Set(bindings.map((b) => b.worker_id));
    const nodes = this.nodesForWorkspace(fleet, wsId);
    const live = [];
    for (const n of nodes) {
      for (const w of n.workers || []) {
        if (bindSet.has(w.id)) {
          live.push({ node: n, worker: w });
        }
      }
    }
    const cards = live
      .map(({ node, worker: w }) => {
        const status = w.status || (w.running ? "running" : "stopped");
        const tip = [
          `path: ${w.project_rel || w.project_path || "—"}`,
          `branch: ${w.git_branch || "—"}`,
          `remote: ${w.git_remote || "—"}`,
          `node: ${this.nodeLabel(node)}`,
          node.hostname && node.name ? `node name: ${node.name}` : "",
          `id: ${w.id}`,
          w.description ? `note: ${w.description}` : "",
        ]
          .filter(Boolean)
          .join("\n");
        const startStop =
          status === "running" || status === "busy" || status === "idle"
            ? `<button type="button" class="ghost" data-w-shutdown="${this.esc(w.id)}" data-node="${this.esc(node.id)}">Shutdown</button>`
            : `<button type="button" class="ghost" data-w-start="${this.esc(w.id)}" data-node="${this.esc(node.id)}">Start</button>`;
        return `
      <div class="cl-card cl-worker-card">
        <button type="button" class="ghost cl-card-x" data-w-remove="${this.esc(w.id)}" data-node="${this.esc(node.id)}" title="Remove">×</button>
        <div class="cl-card-top">
          <strong>${this.esc(w.name || w.id)}</strong>
          <span class="badge">${this.esc(status)}</span>
        </div>
        <div class="muted cl-card-meta">
          <code>${this.esc(w.git_branch || "—")}</code>
          · ${this.esc(this.nodeLabel(node))}
        </div>
        <div class="toolbar cl-worker-actions">
          <button type="button" class="ghost" data-w-review="${this.esc(w.id)}" data-node="${this.esc(node.id)}">Review</button>
          ${startStop}
        </div>
        <div class="cl-worker-tip" hidden>${this.esc(tip)}</div>
      </div>`;
      })
      .join("");
    return `
      <p class="del-crumbs">
        <a href="#/clusters/${this.esc(fleetId)}/workspaces/${this.esc(wsId)}">← Projects</a>
      </p>
      <div class="toolbar">
        <div style="flex:1">
          <h2 style="margin:0">${this.esc(proj.name)}</h2>
          <p class="muted" style="margin:0.25rem 0 0"><code>${this.esc(proj.key)}</code></p>
        </div>
        <a class="ghost" id="c-open-delivery" href="#/delivery/requirements">Open in Delivery</a>
        <button type="button" class="ghost" id="c-proj-edit-deps">Edit deps</button>
        <button type="button" class="ghost" id="c-refresh">Refresh</button>
      </div>
      <div class="cl-grid">
        ${cards}
        <button type="button" class="cl-card cl-card-add" id="c-worker-add">
          <span class="del-req-plus">+</span>
          <span class="muted">New worker</span>
        </button>
      </div>`;
  },

  bindProject(root, fleetId, wsId, projId) {
    root.querySelector("#c-refresh")?.addEventListener("click", () => this.render());
    root.querySelector("#c-open-delivery")?.addEventListener("click", async (ev) => {
      ev.preventDefault();
      try {
        const data = await api.v1.getProject(wsId, projId);
        const p = data.project;
        const eng = await api.v1.deliveryEnsureProject({
          project_id: p.id,
          workspace_id: wsId,
          project_key: p.key,
          title: p.name,
        });
        location.hash = "#/delivery/requirements/" + encodeURIComponent(eng.id);
      } catch (e) {
        this.showNotice(e.message || e, "Error");
      }
    });
    root.querySelector("#c-proj-edit-deps")?.addEventListener("click", () =>
      this.openEditProjectDeps(fleetId, wsId, projId),
    );
    root.querySelector("#c-worker-add")?.addEventListener("click", () =>
      this.openAddWorker(fleetId, wsId, projId),
    );
    root.querySelectorAll("[data-w-review]").forEach((btn) => {
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        const nodeId = btn.getAttribute("data-node");
        const workerId = btn.getAttribute("data-w-review");
        this.go("#/clusters/" + encodeURIComponent(fleetId) + "/review/" +
          encodeURIComponent(nodeId) + "/" + encodeURIComponent(workerId));
      });
    });
    root.querySelectorAll(".cl-worker-card").forEach((card) => {
      const tip = card.querySelector(".cl-worker-tip");
      card.addEventListener("mouseenter", () => {
        if (!tip) return;
        this.showHoverTip(card, tip.textContent);
      });
      card.addEventListener("mouseleave", () => this.hideHoverTip());
    });
    root.querySelectorAll("[data-w-start]").forEach((btn) => {
      btn.addEventListener("click", async (ev) => {
        ev.stopPropagation();
        try {
          await api.v1.startClusterWorker(fleetId, btn.getAttribute("data-node"), btn.getAttribute("data-w-start"));
          await this.render();
        } catch (e) {
          this.showNotice(e.message || e, "Error");
        }
      });
    });
    root.querySelectorAll("[data-w-shutdown]").forEach((btn) => {
      btn.addEventListener("click", async (ev) => {
        ev.stopPropagation();
        try {
          await api.v1.shutdownClusterWorker(
            fleetId,
            btn.getAttribute("data-node"),
            btn.getAttribute("data-w-shutdown"),
          );
          await this.render();
        } catch (e) {
          this.showNotice(e.message || e, "Error");
        }
      });
    });
    root.querySelectorAll("[data-w-remove]").forEach((btn) => {
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        this.confirmRemoveWorker(
          fleetId,
          wsId,
          projId,
          btn.getAttribute("data-node"),
          btn.getAttribute("data-w-remove"),
        );
      });
    });
  },

  confirmRemoveWorker(fleetId, wsId, projId, nodeId, workerId) {
    this.showUiModal(`
      <div class="modal-card">
        <h3 style="margin-top:0">Remove worker?</h3>
        <p>Remove <code>${this.esc(workerId)}</code> from the cluster. Project files on disk are kept.</p>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" class="danger" id="c-rm-ok">Remove</button>
        </div>
      </div>`);
    this.uiModal().querySelector("#c-rm-ok").onclick = async () => {
      try {
        await api.v1.removeClusterWorker(fleetId, nodeId, workerId);
        if (projId) {
          try {
            await api.v1.unbindWorker(wsId, projId, workerId);
          } catch (_) {}
        } else if (wsId) {
          // Unbound zombie: clear any stale binding rows if present.
          try {
            const g = await api.v1.workspaceGraph(wsId);
            const hit = (g.workers || []).find((b) => b.worker_id === workerId);
            if (hit?.project_id) {
              await api.v1.unbindWorker(wsId, hit.project_id, workerId);
            }
          } catch (_) {}
        }
        this.closeUiModal();
        await this.render();
      } catch (e) {
        this.showNotice(e.message || e, "Error");
      }
    };
  },

  showHoverTip(anchor, text) {
    this.hideHoverTip();
    const tip = document.createElement("div");
    tip.className = "cl-fixed-tip";
    tip.id = "cl-hover-tip";
    tip.textContent = text;
    document.body.appendChild(tip);
    const r = anchor.getBoundingClientRect();
    tip.style.left = Math.min(r.left, window.innerWidth - tip.offsetWidth - 12) + "px";
    tip.style.top = r.bottom + 6 + "px";
  },

  hideHoverTip() {
    document.getElementById("cl-hover-tip")?.remove();
  },

  async fsUp(modal) {
    const p = this._addCtx.fsPath || "";
    if (!p) return;
    const i = p.lastIndexOf("/");
    this._addCtx.fsPath = i < 0 ? "" : p.slice(0, i);
    await this.fsLoad(modal);
  },

  fsUseCurrent(modal) {
    const p = this._addCtx.fsPath || "";
    if (!p) {
      modal.querySelector("#c-modal-err").textContent = "Open a folder under the sandbox first";
      return;
    }
    this._addCtx.selected = p;
    modal.querySelector("#c-fs-sel").textContent = p;
    modal.querySelector("#c-modal-err").textContent = "";
  },

  async fsMkdir(modal) {
    const err = modal.querySelector("#c-modal-err");
    err.textContent = "";
    const name = await this.askText("New folder name");
    if (!name) return;
    const base = (this._addCtx.fsPath || "").replace(/\/+$/, "");
    const path = base ? base + "/" + name.trim() : name.trim();
    try {
      await api.v1.mkdirSandbox(this._addCtx.fleetId, this._addCtx.nodeId, path);
      this._addCtx.selected = path;
      modal.querySelector("#c-fs-sel").textContent = path;
      if (!modal.querySelector("#c-name").value.trim()) {
        modal.querySelector("#c-name").value = name.trim();
      }
      await this.fsLoad(modal);
    } catch (e) {
      err.textContent = e.message || String(e);
    }
  },

  async submitWorker(modal) {
    const err = modal.querySelector("#c-modal-err");
    err.textContent = "";
    const name = modal.querySelector("#c-name").value.trim();
    const description = modal.querySelector("#c-desc").value.trim();
    const project_path = (this._addCtx.selected || "").trim();
    const mode = modal.querySelector("#c-mode").value;
    const git_url = modal.querySelector("#c-git")?.value.trim();
    const readme = modal.querySelector("#c-readme")?.value || "";
    if (!name) {
      err.textContent = "Name is required";
      return;
    }
    if (!project_path) {
      err.textContent = "Select or create a folder";
      return;
    }
    try {
      const body = {
        name,
        description: description || undefined,
        project_path,
      };
      if (mode === "new") {
        body.init_git = true;
        body.readme = readme;
      } else if (mode === "worktree") {
        const base = modal.querySelector("#c-wt-base")?.value.trim();
        const branch = modal.querySelector("#c-wt-branch")?.value.trim();
        if (!base) {
          err.textContent = "Base checkout path is required";
          return;
        }
        if (!branch) {
          err.textContent = "Branch name is required";
          return;
        }
        body.worktree_base = base;
        body.branch = branch;
      } else if (git_url) {
        body.git_url = git_url;
      }
      // Per-worker provider settings. Anything left blank is omitted, which the
      // daemon reads as "inherit" rather than "clear".
      const llm = {};
      const provider = modal.querySelector("#c-llm-provider")?.value || "";
      const model = modal.querySelector("#c-llm-model")?.value.trim() || "";
      const baseUrl = modal.querySelector("#c-llm-base")?.value.trim() || "";
      const keyEnv = modal.querySelector("#c-llm-keyenv")?.value.trim() || "";
      const maxSteps = Number(modal.querySelector("#c-llm-maxsteps")?.value || 0);
      if (provider) llm.provider = provider;
      if (model) llm.model = model;
      if (baseUrl) llm.base_url = baseUrl;
      if (keyEnv) llm.api_key_env = keyEnv;
      if (maxSteps > 0) llm.max_steps = maxSteps;
      if (Object.keys(llm).length) body.llm = llm;

      const depends_on = [...modal.querySelectorAll("#c-w-deps input:checked")].map((x) => x.value);
      const res = await api.v1.addClusterWorker(this._addCtx.fleetId, this._addCtx.nodeId, body);
      const workerId = res?.worker?.id || res?.id;
      if (workerId) {
        await api.v1.bindWorker(this._addCtx.wsId, this._addCtx.projId, {
          worker_id: workerId,
          fleet_id: this._addCtx.fleetId,
          node_id: this._addCtx.nodeId,
          depends_on,
        });
      }
      this.closeUiModal();
      await this.render();
    } catch (e) {
      err.textContent = e.message || String(e);
    }
  },

  uiModal() {
    return this.el?.querySelector("#c-ui-modal");
  },
  closeUiModal() {
    window.AnvilUI.modal.close(this.uiModal());
    this._addCtx = null;
  },
  showUiModal(html, opts) {
    const m = this.uiModal();
    if (!m) return;
    window.AnvilUI.modal.open(m, html, opts || {});
  },

  showNotice(message, title = "Notice") {
    window.AnvilUI.notice(this.uiModal(), message, title);
  },

  /** askText is the in-app replacement for a native prompt dialog. */
  askText(title, label = "Name") {
    return new Promise((resolve) => {
      let settled = false;
      const done = (v) => {
        if (settled) return;
        settled = true;
        resolve(v);
      };
      window.AnvilUI.modal.open(
        this.uiModal(),
        `
        <div class="modal-card">
          <h3 style="margin-top:0">${window.AnvilUI.esc(title)}</h3>
          <label for="c-ask">${window.AnvilUI.esc(label)}</label>
          <input id="c-ask" autocomplete="off" spellcheck="false" />
          <div class="toolbar" style="margin-top:0.75rem;justify-content:flex-end">
            <button type="button" class="ghost" data-close-ui>Cancel</button>
            <button type="button" id="c-ask-ok">OK</button>
          </div>
        </div>`,
        { title, onClose: () => done("") },
      );
      const m = this.uiModal();
      const input = m.querySelector("#c-ask");
      const submit = () => {
        const v = (input.value || "").trim();
        done(v);
      };
      m.querySelector("#c-ask-ok").addEventListener("click", submit);
      input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          submit();
        }
      });
    });
  },
};
