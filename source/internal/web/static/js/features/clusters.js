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
    const workspaceId = parts[2] === "workspaces" ? parts[3] || null : null;
    let view = "schedulers";
    if (parts[2] === "workspaces" && !parts[3]) view = "workspaces";
    else if (parts[4] === "graph") view = "graph";
    else if (parts[4] === "projects" && parts[5]) view = "project";
    else if (workspaceId) view = "workspace";
    else if (parts[2] === "workspaces") view = "workspaces";
    return { fleetId, workspaceId, view };
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
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    this.el = null;
  },

  onRoute() {
    this.render();
  },

  esc(s) {
    return String(s ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
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
        <div class="cl-card-foot muted">${n} Dev Node${n === 1 ? "" : "s"}
          ${c.error ? ` · <span class="err">${this.esc(c.error)}</span>` : ""}</div>
      </div>`;
      })
      .join("");
    return `
      <h2>Clusters</h2>
      <p class="muted del-sub">Scheduler → Workspace → Project → Workers</p>
      <div class="toolbar"><button type="button" class="ghost" id="c-refresh">Refresh</button></div>
      <div class="cl-grid">${cards || `<p class="muted">No schedulers in web config.</p>`}</div>`;
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
        <button type="button" class="ghost cl-ws-view" data-ws-view="${this.esc(w.id)}" title="Project graph">View</button>
        <div class="cl-card-top">
          <strong>${this.esc(w.name)}</strong>
        </div>
        <div class="muted cl-card-meta">Token <code class="cl-token">${this.esc(w.token)}</code></div>
        <div class="cl-card-foot muted">${nodes.length} Dev Node${nodes.length === 1 ? "" : "s"} connected</div>
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
        this.go(`#/clusters/${fleetId}/workspaces/${card.getAttribute("data-ws-open")}`);
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
        alert("Workspace token (put in agentd cluster.workspace_token):\n\n" + ws.token);
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
            · ${nodes.length} Dev Node${nodes.length === 1 ? "" : "s"}${
              nodeNames ? ` · ${this.esc(nodeNames)}` : ""
            }</p>
        </div>
        <a class="ghost" href="#/clusters/${this.esc(fleetId)}/workspaces/${this.esc(wsId)}/graph">View graph</a>
        <button type="button" class="ghost" id="c-refresh">Refresh</button>
      </div>
      ${orphanBanner}
      <div class="del-subtabs">
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
    const showProjects = () => this.render();
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
      return `<p class="muted">No Dev Nodes yet. Set <code>cluster.workspace_token</code> on agentd and restart.</p>`;
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
          .join("") || `<p class="muted">No workers on this node.</p>`;
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
        <h3 style="margin-top:0">Bind worker to Project</h3>
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
    let g;
    try {
      g = await api.v1.workspaceGraph(wsId);
    } catch (e) {
      return `<p class="err">${this.esc(e.message || e)}</p>`;
    }
    const projects = g.projects || [];
    const edges = g.edges || [];
    const nodes = projects
      .map((p, i) => {
        const x = 80 + (i % 3) * 220;
        const y = 60 + Math.floor(i / 3) * 120;
        const nw = (g.workers || []).filter((w) => w.project_id === p.id).length;
        return `<div class="cl-graph-node" data-proj="${this.esc(p.id)}" style="left:${x}px;top:${y}px"
          data-x="${x}" data-y="${y}">
          <div class="del-node-title">${this.esc(p.name)}</div>
          <div class="muted"><code>${this.esc(p.key)}</code> · ${nw} worker${nw === 1 ? "" : "s"}</div>
        </div>`;
      })
      .join("");
    const byId = Object.fromEntries(projects.map((p, i) => {
      const x = 80 + (i % 3) * 220;
      const y = 60 + Math.floor(i / 3) * 120;
      return [p.id, { x, y }];
    }));
    const lines = edges
      .map((e) => {
        const a = byId[e.from];
        const b = byId[e.to];
        if (!a || !b) return "";
        return `<line x1="${a.x + 100}" y1="${a.y + 36}" x2="${b.x}" y2="${b.y + 36}" class="del-edge" marker-end="url(#cl-arrow)" />`;
      })
      .join("");
    return `
      <p class="del-crumbs">
        <a href="#/clusters/${this.esc(fleetId)}/workspaces/${this.esc(wsId)}">← Workspace</a>
      </p>
      <h2>Project graph</h2>
      <p class="muted">depends_on edges (read-only). Edit deps when creating/editing a project.</p>
      <div class="del-canvas cl-graph-canvas">
        <svg class="del-edges" width="900" height="400">
          <defs><marker id="cl-arrow" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
            <path d="M0,0 L6,3 L0,6 Z" fill="currentColor" /></marker></defs>
          ${lines}
        </svg>
        <div class="del-nodes" style="width:900px;height:400px">${nodes || `<p class="muted">No projects.</p>`}</div>
      </div>`;
  },

  bindGraph(root, route) {
    root.querySelectorAll("[data-proj]").forEach((n) => {
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
        <div class="toolbar cl-worker-actions">${startStop}</div>
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
          <p class="muted" style="margin:0.25rem 0 0"><code>${this.esc(proj.key)}</code>
            · Delivery bind later</p>
        </div>
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
    root.querySelector("#c-proj-edit-deps")?.addEventListener("click", () =>
      this.openEditProjectDeps(fleetId, wsId, projId),
    );
    root.querySelector("#c-worker-add")?.addEventListener("click", () =>
      this.openAddWorker(fleetId, wsId, projId),
    );
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
          alert(e.message || e);
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
          alert(e.message || e);
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
        alert(e.message || e);
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

  openAddWorker(fleetId, wsId, projId) {
    const fleet = this.fleet(fleetId);
    const nodes = this.nodesForWorkspace(fleet, wsId);
    if (!nodes.length) {
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">No Dev Nodes</h3>
          <p class="muted">Start agentd with <code>cluster.workspace_token</code> for this workspace.</p>
          <div class="toolbar" style="justify-content:flex-end"><button type="button" data-close-ui>OK</button></div>
        </div>`);
      return;
    }
    const nodeOpts = nodes
      .map((n) => {
        const sub = this.nodeSubLabel(n);
        const label = sub ? `${this.nodeLabel(n)} (${sub})` : this.nodeLabel(n);
        return `<option value="${this.esc(n.id)}">${this.esc(label)}</option>`;
      })
      .join("");
    this.showUiModal(`
      <div class="modal-card modal-wide modal-tall">
        <h3 style="margin-top:0">Add worker</h3>
        <label>Dev Node</label>
        <select id="c-node">${nodeOpts}</select>
        <label>Mode</label>
        <select id="c-mode">
          <option value="existing">Existing project folder</option>
          <option value="new">New folder (git init + README)</option>
          <option value="worktree">Git worktree (new branch)</option>
        </select>
        <label>Name</label>
        <input id="c-name" placeholder="Demo project" />
        <label>Description</label>
        <input id="c-desc" placeholder="Optional" />
        <label>Project folder (sandbox)</label>
        <div class="fs-browser fs-browser-tall">
          <div class="fs-bar">
            <button type="button" class="ghost" id="c-fs-up">↑</button>
            <code id="c-fs-path" class="fs-path">/</code>
            <button type="button" class="ghost" id="c-fs-use">Use current</button>
            <button type="button" class="ghost" id="c-fs-new">New folder</button>
          </div>
          <div id="c-fs-list" class="fs-list fs-list-tall"></div>
          <p class="muted">Selected: <code id="c-fs-sel">—</code></p>
        </div>
        <div id="c-git-wrap">
          <label>Git URL (existing empty folder)</label>
          <input id="c-git" placeholder="https://github.com/org/repo.git" />
        </div>
        <div id="c-wt-wrap" hidden>
          <label>Base checkout (existing git folder)</label>
          <input id="c-wt-base" placeholder="path of existing checkout" />
          <button type="button" class="ghost" id="c-wt-use-sel">Use selected as base</button>
          <label>New branch</label>
          <input id="c-wt-branch" placeholder="feature/x" />
          <p class="muted">Selected path above is the new worktree folder (create empty via New folder).</p>
        </div>
        <div id="c-readme-wrap" hidden>
          <label>README</label>
          <textarea id="c-readme" rows="4"># Project

Created by Anvil.
</textarea>
          <div id="c-ai-slot"></div>
        </div>
        <label>Depends on (other workers in this project)</label>
        <div id="c-w-deps" class="cl-deps"><p class="muted">Loading…</p></div>
        <div class="err" id="c-modal-err"></div>
        <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
          <button type="button" class="ghost" data-close-ui>Cancel</button>
          <button type="button" id="c-modal-ok">Create</button>
        </div>
      </div>`);
    const modal = this.uiModal();
    this._addCtx = {
      fleetId,
      wsId,
      projId,
      nodeId: nodes[0].id,
      fsPath: "",
      selected: "",
    };
    const syncMode = () => {
      const mode = modal.querySelector("#c-mode").value;
      modal.querySelector("#c-git-wrap").hidden = mode !== "existing";
      modal.querySelector("#c-readme-wrap").hidden = mode !== "new";
      modal.querySelector("#c-wt-wrap").hidden = mode !== "worktree";
    };
    modal.querySelector("#c-mode").onchange = syncMode;
    modal.querySelector("#c-node").onchange = async (ev) => {
      this._addCtx.nodeId = ev.target.value;
      await this.fsLoad(modal);
    };
    modal.querySelector("#c-fs-up").onclick = () => this.fsUp(modal);
    modal.querySelector("#c-fs-use").onclick = () => this.fsUseCurrent(modal);
    modal.querySelector("#c-fs-new").onclick = () => this.fsMkdir(modal);
    modal.querySelector("#c-wt-use-sel").onclick = () => {
      const sel = this._addCtx.selected || "";
      if (!sel) {
        modal.querySelector("#c-modal-err").textContent = "Select a base checkout folder first";
        return;
      }
      modal.querySelector("#c-wt-base").value = sel;
      modal.querySelector("#c-modal-err").textContent = "";
    };
    api.v1.getProject(wsId, projId).then((data) => {
      const box = modal.querySelector("#c-w-deps");
      const binds = data.bindings || [];
      if (!binds.length) {
        box.innerHTML = `<p class="muted">No other workers bound yet.</p>`;
        return;
      }
      box.innerHTML = binds
        .map(
          (b) =>
            `<label class="cl-dep-row"><input type="checkbox" value="${this.esc(b.worker_id)}" /> ${this.esc(b.worker_id)}</label>`,
        )
        .join("");
    });
    if (window.AiChat) {
      const chat = window.AiChat.create({
        id: "c-readme-chat",
        title: "AI assist",
        placeholder: "How should the README describe this project?",
        rows: 2,
        tall: false,
        sendLabel: "Polish README (mock)",
        emptyHint: "Ask to polish the README for this new project.",
        onSend: async ({ composed }, ch) => {
          const ta = modal.querySelector("#c-readme");
          const line = "\n\n## Notes\n\n" + composed.slice(0, 400);
          ta.value = (ta.value || "").trim() + line;
          ch.appendMessage("ai", "(mock) Appended notes to README. Review before Create.");
        },
      });
      modal.querySelector("#c-ai-slot").innerHTML = chat.render();
      chat.mount(modal);
    }
    syncMode();
    this.fsLoad(modal);
    modal.querySelector("#c-modal-ok").onclick = () => this.submitWorker(modal);
  },

  async fsLoad(modal) {
    if (!this._addCtx) return;
    const list = modal.querySelector("#c-fs-list");
    modal.querySelector("#c-fs-path").textContent = "/" + (this._addCtx.fsPath || "");
    list.innerHTML = `<div class="muted">Loading…</div>`;
    try {
      const data = await api.v1.listSandbox(
        this._addCtx.fleetId,
        this._addCtx.nodeId,
        this._addCtx.fsPath || "",
      );
      const entries = (data.entries || []).slice().sort((a, b) => {
        if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
        return String(a.name).localeCompare(b.name);
      });
      list.innerHTML = "";
      if (!entries.length) {
        list.innerHTML = `<div class="muted">Empty — use New folder</div>`;
        return;
      }
      for (const e of entries) {
        if (!e.is_dir) continue;
        const row = document.createElement("button");
        row.type = "button";
        row.className = "fs-row fs-row-grid";
        row.innerHTML = `<span class="fs-name">📁 ${this.esc(e.name)}</span>`;
        row.onclick = () => {
          this._addCtx.selected = e.path;
          modal.querySelector("#c-fs-sel").textContent = e.path;
          if (!modal.querySelector("#c-name").value.trim()) {
            modal.querySelector("#c-name").value = e.name;
          }
        };
        row.ondblclick = async () => {
          this._addCtx.fsPath = e.path;
          this._addCtx.selected = e.path;
          modal.querySelector("#c-fs-sel").textContent = e.path;
          await this.fsLoad(modal);
        };
        list.appendChild(row);
      }
    } catch (e) {
      list.innerHTML = `<div class="err">${this.esc(e.message || e)}</div>`;
    }
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
    const name = prompt("New folder name");
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
    const m = this.uiModal();
    if (m) {
      m.hidden = true;
      m.innerHTML = "";
    }
    this._addCtx = null;
  },
  showUiModal(html) {
    const m = this.uiModal();
    m.innerHTML = html;
    m.hidden = false;
    m.querySelectorAll("[data-close-ui]").forEach((b) => {
      b.onclick = () => this.closeUiModal();
    });
    m.onclick = (ev) => {
      if (ev.target === m) this.closeUiModal();
    };
  },
};
