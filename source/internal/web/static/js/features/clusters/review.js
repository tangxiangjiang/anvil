// Read-only sandbox review workbench (p_0.32.2).
(function () {
  const esc = (value) => window.AnvilUI.esc(value == null ? "" : String(value));

  window.AnvilReview = {
    root: null,
    scope: null,
    generation: 0,
    selectedFile: "",
    selectedChange: null,

    mountRoute(root, route, fleet) {
      const node = (fleet?.nodes || []).find((item) => item.id === route.reviewNodeId);
      const worker = (node?.workers || []).find((item) => item.id === route.reviewWorkerId);
      if (!fleet || !node || !worker || (!worker.project_path && !worker.project_rel)) return false;
      this.mount(root, {
        fleetId: route.fleetId,
        nodeId: route.reviewNodeId,
        workerId: route.reviewWorkerId,
      });
      return true;
    },

    mount(root, scope) {
      this.root = root;
      this.scope = { ...scope };
      this.generation++;
      this.selectedFile = "";
      this.selectedChange = null;
      root.innerHTML = `
        <nav class="del-crumbs" aria-label="Breadcrumb">
          <a href="#/clusters">Clusters</a><span class="muted">/</span>
          <button type="button" class="ghost" data-review-back>Project</button>
          <span class="muted">/</span><span>Review</span>
        </nav>
        <div class="review-head">
          <div>
            <h2>Read-only review</h2>
            <p class="muted">Worker <code>${esc(scope.workerId)}</code> · content is rendered as plain text</p>
          </div>
          <button type="button" class="ghost" data-review-refresh>Refresh</button>
        </div>
        <div class="review-banner" role="note">Read-only in Anvil. File writes, terminals, and Git mutations are unavailable.</div>
        <div class="review-grid">
          <section class="review-pane review-tree-pane" aria-labelledby="review-tree-title">
            <h3 id="review-tree-title">Files</h3>
            <div data-review-tree-state class="muted" role="status">Loading files…</div>
            <div data-review-tree></div>
          </section>
          <section class="review-pane review-viewer-pane" aria-labelledby="review-viewer-title">
            <h3 id="review-viewer-title">Viewer</h3>
            <div data-review-file-state class="muted" role="status">Select a file.</div>
            <pre data-review-file tabindex="0" aria-label="Read-only file content"></pre>
          </section>
          <section class="review-pane review-changes-pane" aria-labelledby="review-changes-title">
            <h3 id="review-changes-title">Changes</h3>
            <div data-review-status-state class="muted" role="status">Loading changes…</div>
            <div data-review-changes></div>
            <h3 class="review-diff-title">Diff</h3>
            <div data-review-diff-state class="muted" role="status">Select a change.</div>
            <pre data-review-diff tabindex="0" aria-label="Read-only Git diff"></pre>
          </section>
        </div>`;
      const refresh = root.querySelector("[data-review-refresh]");
      refresh.addEventListener("click", () => window.AnvilUI.withAction(
        refresh, { key: "review-refresh", runningLabel: "Refreshing…" }, () => this.refresh(),
      ));
      root.querySelector("[data-review-back]").addEventListener("click", () => history.back());
      void this.refresh();
    },

    unmount() {
      this.generation++;
      this.root = null;
      this.scope = null;
    },

    current(generation) {
      return Boolean(this.root && this.scope && generation === this.generation);
    },

    async refresh() {
      if (!this.root || !this.scope) return;
      const generation = ++this.generation;
      this.setState("tree", "Loading files…");
      this.setState("status", "Loading changes…");
      const work = [this.loadDir("", this.root.querySelector("[data-review-tree]"), generation), this.loadStatus(generation)];
      if (this.selectedFile) work.push(this.openFile(this.selectedFile, generation));
      if (this.selectedChange) {
        work.push(this.openDiff(this.selectedChange.path, this.selectedChange.staged, generation));
      }
      await Promise.allSettled(work);
    },

    setState(kind, text, tone = "") {
      const el = this.root?.querySelector(`[data-review-${kind}-state]`);
      if (!el) return;
      el.textContent = text || "";
      el.className = (tone ? tone + " " : "") + "muted";
    },

    errorText(error) {
      if (error?.data?.code === "invalid_scope") return "This worker is no longer assigned to the selected node.";
      if (error?.data?.code === "node_unavailable" || error?.data?.code === "scheduler_unreachable") {
        return "Node is unreachable. Check the cluster connection and retry.";
      }
      return error?.message || String(error);
    },

    async loadDir(path, host, generation = this.generation) {
      if (!host) return;
      if (path === "") host.replaceChildren();
      try {
        const data = await api.v1.reviewTree(
          this.scope.fleetId, this.scope.nodeId, this.scope.workerId, path,
        );
        if (!this.current(generation)) return;
        const entries = data.entries || [];
        const list = document.createElement("ul");
        list.className = "review-tree";
        list.setAttribute("role", path ? "group" : "tree");
        for (const entry of entries) {
          const item = document.createElement("li");
          item.setAttribute("role", "treeitem");
          if (entry.is_dir) item.setAttribute("aria-expanded", "false");
          const button = document.createElement("button");
          button.type = "button";
          button.className = "review-tree-row";
          button.textContent = `${entry.is_dir ? "▸" : "·"} ${entry.name}`;
          button.title = entry.path;
          item.appendChild(button);
          if (entry.is_dir) {
            const children = document.createElement("div");
            item.appendChild(children);
            button.addEventListener("click", async () => {
              const open = item.getAttribute("aria-expanded") === "true";
              if (open) {
                children.replaceChildren();
                item.setAttribute("aria-expanded", "false");
                button.textContent = `▸ ${entry.name}`;
                return;
              }
              button.disabled = true;
              await this.loadDir(entry.path, children, generation);
              button.disabled = false;
              if (!this.current(generation)) return;
              item.setAttribute("aria-expanded", "true");
              button.textContent = `▾ ${entry.name}`;
            });
          } else {
            button.addEventListener("click", () => this.openFile(entry.path));
          }
          list.appendChild(item);
        }
        host.replaceChildren(list);
        if (path === "") {
          this.setState("tree", entries.length ? (data.truncated ? "File list truncated at the safety limit." : "") : "Repository is empty.", data.truncated ? "warn" : "");
        }
      } catch (error) {
        if (!this.current(generation)) return;
        if (path === "") this.setState("tree", this.errorText(error), "err");
        else {
          host.textContent = this.errorText(error);
          host.className = "err";
        }
      }
    },

    async openFile(path, generation = this.generation) {
      this.selectedFile = path;
      const content = this.root?.querySelector("[data-review-file]");
      if (content) content.textContent = "";
      this.setState("file", `Loading ${path}…`);
      try {
        const data = await api.v1.reviewFile(
          this.scope.fleetId, this.scope.nodeId, this.scope.workerId, path,
        );
        if (!this.current(generation)) return;
        if (data.binary) {
          this.setState("file", `${path} is binary (${data.size || 0} bytes); content was not returned.`, "warn");
          return;
        }
        this.setState("file", `${path}${data.truncated ? " · truncated at the safety limit" : ""}`, data.truncated ? "warn" : "");
        content.textContent = data.content || "";
      } catch (error) {
        if (this.current(generation)) this.setState("file", this.errorText(error), "err");
      }
    },

    async loadStatus(generation = this.generation) {
      const host = this.root?.querySelector("[data-review-changes]");
      if (!host) return;
      host.replaceChildren();
      try {
        const data = await api.v1.reviewStatus(this.scope.fleetId, this.scope.nodeId, this.scope.workerId);
        if (!this.current(generation)) return;
        const changes = [];
        for (const entry of data.status || []) {
          if (entry.index && entry.index !== " " && entry.index !== "?") {
            changes.push({ ...entry, staged: true, mark: entry.index });
          }
          if ((entry.worktree && entry.worktree !== " ") || entry.index === "?") {
            changes.push({ ...entry, staged: false, mark: entry.index === "?" ? "?" : entry.worktree });
          }
        }
        if (!changes.length) {
          this.setState("status", "Working tree is clean.");
          return;
        }
        const list = document.createElement("ul");
        list.className = "review-changes";
        for (const change of changes) {
          const item = document.createElement("li");
          const button = document.createElement("button");
          button.type = "button";
          button.className = "review-change-row";
          const scope = change.staged ? "staged" : "unstaged";
          button.textContent = `${change.mark} ${change.path} · ${scope}`;
          button.title = change.orig_path ? `${change.orig_path} → ${change.path}` : change.path;
          button.addEventListener("click", () => this.openDiff(change.path, change.staged));
          item.appendChild(button);
          list.appendChild(item);
        }
        host.replaceChildren(list);
        this.setState("status", `${data.branch || "repository"} · ${changes.length} change${changes.length === 1 ? "" : "s"}${data.truncated ? " · truncated" : ""}`, data.truncated ? "warn" : "");
      } catch (error) {
        if (this.current(generation)) this.setState("status", this.errorText(error), "err");
      }
    },

    async openDiff(path, staged, generation = this.generation) {
      this.selectedChange = { path, staged: Boolean(staged) };
      const content = this.root?.querySelector("[data-review-diff]");
      if (content) content.textContent = "";
      this.setState("diff", `Loading ${path}…`);
      try {
        const data = await api.v1.reviewDiff(
          this.scope.fleetId, this.scope.nodeId, this.scope.workerId, path, staged,
        );
        if (!this.current(generation)) return;
        content.textContent = data.diff || "";
        this.setState(
          "diff",
          `${path} · ${staged ? "staged" : "unstaged"}${data.truncated ? " · truncated at the safety limit" : data.diff ? "" : " · no textual diff"}`,
          data.truncated ? "warn" : "",
        );
      } catch (error) {
        if (this.current(generation)) this.setState("diff", this.errorText(error), "err");
      }
    },

    // Pure seams used by node/jsdom tests.
    flattenStatus(entries) {
      const out = [];
      for (const entry of entries || []) {
        if (entry.index && entry.index !== " " && entry.index !== "?") out.push({ path: entry.path, staged: true });
        if ((entry.worktree && entry.worktree !== " ") || entry.index === "?") out.push({ path: entry.path, staged: false });
      }
      return out;
    },
  };

  if (typeof module !== "undefined" && module.exports) {
    module.exports = {
      flattenStatus: (entries) => window.AnvilReview.flattenStatus(entries),
    };
  }
})();
