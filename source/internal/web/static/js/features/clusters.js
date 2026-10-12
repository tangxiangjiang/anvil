window.Features = window.Features || {};
window.Features.clusters = {
  timer: null,
  state: { openCluster: {}, openNode: {} },

  async mount(el) {
    el.innerHTML = `
      <h2>Clusters</h2>
      <p class="muted" style="margin-top:-0.5rem;margin-bottom:1rem">
        Schedulers from web config. Nodes join via agentd. Workers live under each node's sandbox directory.
      </p>
      <div class="toolbar">
        <button type="button" id="c-refresh">Refresh</button>
        <span class="muted" id="c-status">Loading...</span>
      </div>
      <div id="c-list"></div>
      <div id="c-modal" class="modal" hidden>
        <div class="modal-card modal-wide modal-tall">
          <h3>Add worker</h3>
          <p class="muted" id="c-modal-target"></p>
          <label>Name</label>
          <input id="c-name" placeholder="Demo project" />
          <label>Description</label>
          <input id="c-desc" placeholder="Optional note for operators" />
          <label>Project folder (sandbox)</label>
          <div class="fs-browser fs-browser-tall">
            <div class="fs-bar">
              <button type="button" class="ghost" id="c-fs-up" title="Up">↑</button>
              <code id="c-fs-path" class="fs-path">/</code>
              <button type="button" class="ghost" id="c-fs-use">Use current</button>
              <button type="button" class="ghost" id="c-fs-new">New folder</button>
            </div>
            <div class="fs-table-head">
              <span>Name</span><span>Size</span><span>Modified</span>
            </div>
            <div id="c-fs-list" class="fs-list fs-list-tall"></div>
            <p class="muted" style="margin:0.35rem 0 0">Selected: <code id="c-fs-sel">—</code></p>
          </div>
          <label>Git URL (required if folder is new/empty)</label>
          <input id="c-git" placeholder="https://github.com/org/repo.git" />
          <div class="err" id="c-modal-err"></div>
          <div class="toolbar" style="margin-top:1rem;justify-content:flex-end">
            <button type="button" class="ghost" id="c-modal-cancel">Cancel</button>
            <button type="button" id="c-modal-ok">Create</button>
          </div>
        </div>
      </div>`;
    el.querySelector("#c-refresh").onclick = () => this.load(el);
    el.querySelector("#c-modal-cancel").onclick = () => this.hideModal(el);
    el.querySelector("#c-modal-ok").onclick = () => this.submitWorker(el);
    el.querySelector("#c-fs-up").onclick = () => this.fsUp(el);
    el.querySelector("#c-fs-use").onclick = () => this.fsUseCurrent(el);
    el.querySelector("#c-fs-new").onclick = () => this.fsMkdir(el);
    await this.load(el);
    this.timer = setInterval(() => this.load(el), 8000);
  },

  unmount() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  },

  hideModal(el) {
    el.querySelector("#c-modal").hidden = true;
    el.querySelector("#c-modal-err").textContent = "";
    this._addCtx = null;
  },

  async showModal(el, fleetId, nodeId, label, sandboxDir) {
    this._addCtx = { fleetId, nodeId, sandboxDir: sandboxDir || "", fsPath: "", selected: "" };
    el.querySelector("#c-modal-target").textContent =
      label + (sandboxDir ? " · sandbox " + sandboxDir : " · (no sandbox_dir — configure agentd)");
    el.querySelector("#c-name").value = "";
    el.querySelector("#c-desc").value = "";
    el.querySelector("#c-git").value = "";
    el.querySelector("#c-modal-err").textContent = "";
    el.querySelector("#c-fs-sel").textContent = "—";
    el.querySelector("#c-modal").hidden = false;
    await this.fsLoad(el);
  },

  async fsLoad(el) {
    if (!this._addCtx) return;
    const list = el.querySelector("#c-fs-list");
    const pathEl = el.querySelector("#c-fs-path");
    pathEl.textContent = "/" + (this._addCtx.fsPath || "");
    list.innerHTML = `<div class="muted">Loading…</div>`;
    try {
      const data = await api.v1.listSandbox(this._addCtx.fleetId, this._addCtx.nodeId, this._addCtx.fsPath || "");
      const entries = (data.entries || []).slice().sort((a, b) => {
        if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
        return String(a.name).localeCompare(b.name);
      });
      list.innerHTML = "";
      if (!entries.length) {
        list.innerHTML = `<div class="muted">Empty folder — use New folder</div>`;
        return;
      }
      for (const e of entries) {
        const row = document.createElement("button");
        row.type = "button";
        row.className = "fs-row fs-row-grid" + (e.is_dir && this._addCtx.selected === e.path ? " selected" : "");
        const icon = e.is_dir ? "📁" : "📄";
        const size = e.is_dir ? "—" : formatBytes(e.size || 0);
        const mtime = e.mtime ? formatTime(e.mtime) : "—";
        row.innerHTML = `
          <span class="fs-name">${icon} ${esc(e.name)}${e.is_git ? ' <span class="badge ok">git</span>' : ""}</span>
          <span class="fs-meta">${esc(size)}</span>
          <span class="fs-meta">${esc(mtime)}</span>`;
        if (e.is_dir) {
          row.title = "Click to select · double-click to open";
          row.onclick = () => {
            this._addCtx.selected = e.path;
            el.querySelector("#c-fs-sel").textContent = e.path;
            list.querySelectorAll(".fs-row").forEach((r) => r.classList.remove("selected"));
            row.classList.add("selected");
            if (!el.querySelector("#c-name").value.trim()) {
              el.querySelector("#c-name").value = e.name;
            }
          };
          row.ondblclick = async () => {
            this._addCtx.fsPath = e.path;
            this._addCtx.selected = e.path;
            el.querySelector("#c-fs-sel").textContent = e.path;
            await this.fsLoad(el);
          };
        } else {
          row.title = e.path;
          row.disabled = true;
        }
        list.appendChild(row);
      }
    } catch (e) {
      list.innerHTML = `<div class="err">${esc(e.message || e)}</div>`;
    }
  },

  async fsUp(el) {
    if (!this._addCtx) return;
    const p = this._addCtx.fsPath || "";
    if (!p) return;
    const i = p.lastIndexOf("/");
    this._addCtx.fsPath = i < 0 ? "" : p.slice(0, i);
    await this.fsLoad(el);
  },

  fsUseCurrent(el) {
    if (!this._addCtx) return;
    const p = this._addCtx.fsPath || "";
    if (!p) {
      el.querySelector("#c-modal-err").textContent = "Open or create a project folder (sandbox root alone is not enough)";
      return;
    }
    this._addCtx.selected = p;
    el.querySelector("#c-fs-sel").textContent = p;
    el.querySelector("#c-modal-err").textContent = "";
    const base = p.split("/").pop();
    if (base && !el.querySelector("#c-name").value.trim()) {
      el.querySelector("#c-name").value = base;
    }
  },

  async fsMkdir(el) {
    if (!this._addCtx) return;
    const err = el.querySelector("#c-modal-err");
    err.textContent = "";
    const name = prompt("New folder name");
    if (!name) return;
    const base = (this._addCtx.fsPath || "").replace(/\/+$/, "");
    const path = base ? base + "/" + name.trim() : name.trim();
    try {
      await api.v1.mkdirSandbox(this._addCtx.fleetId, this._addCtx.nodeId, path);
      this._addCtx.selected = path;
      el.querySelector("#c-fs-sel").textContent = path;
      if (!el.querySelector("#c-name").value.trim()) {
        el.querySelector("#c-name").value = name.trim();
      }
      await this.fsLoad(el);
    } catch (e) {
      err.textContent = e.message || String(e);
    }
  },

  async submitWorker(el) {
    if (!this._addCtx) return;
    const err = el.querySelector("#c-modal-err");
    err.textContent = "";
    const name = el.querySelector("#c-name").value.trim();
    const description = el.querySelector("#c-desc").value.trim();
    const project_path = (this._addCtx.selected || "").trim();
    const git_url = el.querySelector("#c-git").value.trim();
    if (!name) {
      err.textContent = "Name is required";
      return;
    }
    if (!project_path) {
      err.textContent = "Select a folder (click) or create one with New folder";
      return;
    }
    try {
      await api.v1.addClusterWorker(this._addCtx.fleetId, this._addCtx.nodeId, {
        name,
        description: description || undefined,
        project_path,
        git_url: git_url || undefined,
      });
      this.hideModal(el);
      await this.load(el);
    } catch (e) {
      err.textContent = e.message || String(e);
    }
  },

  async load(el) {
    const status = el.querySelector("#c-status");
    const list = el.querySelector("#c-list");
    try {
      const data = await api.v1.clusters();
      const clusters = data.clusters || [];
      status.textContent = clusters.length + " scheduler(s)";
      list.innerHTML = "";
      if (!clusters.length) {
        list.innerHTML = `<div class="card muted">No fleets configured. Add fleets: in web.yaml or use scheduler_url default.</div>`;
        return;
      }
      for (const c of clusters) {
        list.appendChild(this.renderCluster(el, c));
      }
    } catch (e) {
      status.textContent = e.message || String(e);
      if (e.status === 401) location.hash = "#/login";
    }
  },

  renderCluster(el, c) {
    const open = !!this.state.openCluster[c.id];
    const wrap = document.createElement("div");
    wrap.className = "fold card";
    const ip = c.listen_app || c.scheduler_url || "";
    const badge = c.online
      ? `<span class="badge ok">online</span>`
      : `<span class="badge bad">unreachable</span>`;
    wrap.innerHTML = `
      <button type="button" class="fold-head">
        <span class="chev">${open ? "▼" : "▶"}</span>
        <span class="fold-title">
          <strong>${esc(c.name || c.id)}</strong>
          ${badge}
          <span class="muted"> · ${esc(ip)}</span>
        </span>
      </button>
      <div class="fold-body" ${open ? "" : "hidden"}></div>`;
    const body = wrap.querySelector(".fold-body");
    wrap.querySelector(".fold-head").onclick = () => {
      this.state.openCluster[c.id] = !this.state.openCluster[c.id];
      this.load(el);
    };
    if (!open) return wrap;
    if (c.description) {
      body.innerHTML += `<p class="muted">${esc(c.description)}</p>`;
    }
    if (c.error && !c.online) {
      body.innerHTML += `<p class="err">${esc(c.error)}</p>`;
    }
    const nodes = Array.isArray(c.nodes) ? c.nodes : [];
    if (!nodes.length) {
      body.innerHTML += `<p class="muted">No agentd nodes joined. Start agentd with cluster.enabled pointing at this scheduler.</p>`;
    } else {
      for (const n of nodes) {
        body.appendChild(this.renderNode(el, c, n));
      }
    }
    return wrap;
  },

  renderNode(el, cluster, n) {
    const key = cluster.id + "/" + n.id;
    const open = !!this.state.openNode[key];
    const wrap = document.createElement("div");
    wrap.className = "fold fold-nested";
    const host = n.hostname || n.id;
    const title = n.name || n.id;
    const stale = n.stale;
    const accepting = n.accepting !== false;
    const badge = stale
      ? `<span class="badge bad">stale</span>`
      : accepting
        ? `<span class="badge ok">online</span>`
        : `<span class="badge warn">draining</span>`;
    wrap.innerHTML = `
      <button type="button" class="fold-head">
        <span class="chev">${open ? "▼" : "▶"}</span>
        <span class="fold-title">
          <strong>${esc(title)}</strong>
          ${badge}
          <span class="muted"> · PC ${esc(host)}</span>
        </span>
      </button>
      <div class="fold-body" ${open ? "" : "hidden"}></div>`;
    wrap.querySelector(".fold-head").onclick = () => {
      this.state.openNode[key] = !this.state.openNode[key];
      this.load(el);
    };
    const body = wrap.querySelector(".fold-body");
    if (!open) return wrap;
    let intro = "";
    if (n.description) intro += `<p class="muted">${esc(n.description)}</p>`;
    if (n.sandbox_dir) intro += `<p class="muted">sandbox <code>${esc(n.sandbox_dir)}</code></p>`;
    else intro += `<p class="err">sandbox_dir not set on agentd — configure before adding workers</p>`;
    intro += `<p class="muted">id ${esc(n.id)} · idle ${n.idle} / busy ${n.busy} / max ${n.max_workers}</p>`;
    body.innerHTML = intro;
    const actions = document.createElement("div");
    actions.className = "toolbar";
    actions.appendChild(btn(accepting ? "Drain" : "Enable", async () => {
      await api.v1.patchClusterNode(cluster.id, n.id, { accepting: !accepting });
      await this.load(el);
    }, accepting ? "danger" : ""));
    actions.appendChild(btn("Add worker", async () => {
      await this.showModal(el, cluster.id, n.id, `${cluster.name || cluster.id} / ${title}`, n.sandbox_dir || "");
    }));
    body.appendChild(actions);

    const workers = n.workers || [];
    if (!workers.length) {
      const empty = document.createElement("p");
      empty.className = "muted";
      empty.textContent = "No project workers yet.";
      body.appendChild(empty);
    } else {
      const ul = document.createElement("div");
      ul.className = "worker-list";
      for (const w of workers) {
        const row = document.createElement("div");
        row.className = "worker-row";
        const label = w.git_branch || w.project_rel || w.name || w.id;
        const path = w.project_rel || w.project_path || "(no project)";
        const st = w.status || "";
        const stBadge = st === "stopped"
          ? `<span class="badge warn">stopped</span>`
          : st === "idle"
            ? `<span class="badge ok">idle</span>`
            : st === "busy"
              ? `<span class="badge">busy</span>`
              : `<span class="badge">${esc(st)}</span>`;
        const branchLine = w.git_branch
          ? `<div class="muted">branch <code>${esc(w.git_branch)}</code> · ${esc(path)}</div>`
          : `<div class="muted">${esc(path)}${w.git_remote ? " · " + esc(w.git_remote) : ""}</div>`;
        row.innerHTML = `
          <div>
            <strong>${esc(label)}</strong>
            ${stBadge}
            ${w.description ? `<div class="muted">${esc(w.description)}</div>` : ""}
            ${branchLine}
            <div class="muted"><span class="muted">id ${esc(w.id)}</span></div>
          </div>`;
        const actions = document.createElement("div");
        actions.className = "worker-actions";
        if (st === "stopped") {
          actions.appendChild(btn("Start", async () => {
            await api.v1.startClusterWorker(cluster.id, n.id, w.id);
            await this.load(el);
          }));
        } else if (st === "idle") {
          actions.appendChild(btn("Shutdown", async () => {
            await api.v1.shutdownClusterWorker(cluster.id, n.id, w.id);
            await this.load(el);
          }));
        }
        const del = btn("Remove", async () => {
          if (!confirm("Remove worker " + label + "? Project files on disk are kept.")) return;
          await api.v1.removeClusterWorker(cluster.id, n.id, w.id);
          await this.load(el);
        }, "danger");
        if (st !== "stopped" && st !== "idle") del.disabled = true;
        actions.appendChild(del);
        row.appendChild(actions);
        ul.appendChild(row);
      }
      body.appendChild(ul);
    }
    return wrap;
  },
};

function btn(label, onClick, cls) {
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = label;
  if (cls) b.className = cls;
  b.onclick = (e) => {
    e.stopPropagation();
    onClick().catch((err) => alert(err.message || err));
  };
  return b;
}
function esc(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function formatBytes(n) {
  n = Number(n) || 0;
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  if (n < 1024 * 1024 * 1024) return (n / (1024 * 1024)).toFixed(1) + " MB";
  return (n / (1024 * 1024 * 1024)).toFixed(1) + " GB";
}
function formatTime(sec) {
  try {
    return new Date(sec * 1000).toLocaleString();
  } catch (_) {
    return String(sec);
  }
}
