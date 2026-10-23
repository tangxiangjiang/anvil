// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Cluster worker creation and sandbox picker.
 *
 * Loaded after clusters.js and assigned onto the same feature object so the
 * route/mount contract remains window.Features.clusters.
 */
Object.assign(window.Features.clusters, {
  openAddWorker(fleetId, wsId, projId) {
    const fleet = this.fleet(fleetId);
    const nodes = this.nodesForWorkspace(fleet, wsId);
    if (!nodes.length) {
      this.showUiModal(`
        <div class="modal-card">
          <h3 style="margin-top:0">No Nodes</h3>
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
        <label>Node</label>
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
        <label>AI provider (this worker)</label>
        <p class="muted" style="margin:0 0 0.35rem;font-size:0.82rem">
          Optional. Empty fields inherit agentd's llm block, so a worker that says
          nothing behaves exactly as before. The key is read from the environment
          variable you name here — it is never stored in the config or the request.
        </p>
        <div class="cl-llm-grid">
          <div>
            <label for="c-llm-provider">Provider</label>
            <select id="c-llm-provider">
              <option value="">(inherit)</option>
              <option value="openai">openai</option>
              <option value="anthropic">anthropic</option>
            </select>
          </div>
          <div>
            <label for="c-llm-model">Model</label>
            <input id="c-llm-model" placeholder="(inherit)" />
          </div>
        </div>
        <label for="c-llm-base">Base URL</label>
        <input id="c-llm-base" placeholder="(inherit) http://127.0.0.1:8080/v1" />
        <div class="cl-llm-grid">
          <div>
            <label for="c-llm-keyenv">API key env var</label>
            <input id="c-llm-keyenv" placeholder="(inherit) ANVIL_API_KEY" />
          </div>
          <div>
            <label for="c-llm-maxsteps">Max steps</label>
            <input id="c-llm-maxsteps" type="number" min="0" placeholder="(inherit)" />
          </div>
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
        emptyHint: "Ask for a README for this new project.",
        // Real drafting: the proposal fills the README field, and Create is still
        // what writes it. Nothing here persists anything.
        onSend: async ({ text }, ch) => {
          const ta = modal.querySelector("#c-readme");
          await window.AnvilUI.assist.ask({
            surface: "project_readme",
            field: "readme",
            instruction: text,
            host: modal.querySelector("#c-readme-assist"),
            chat: ch,
            onApply: async (_field, value) => {
              if (ta) ta.value = value;
            },
          });
        },
      });
      modal.querySelector("#c-ai-slot").innerHTML =
        chat.render() + `<div id="c-readme-assist" class="assist-host"></div>`;
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

});
