window.Features = window.Features || {};
window.Features.nodes = {
  timer: null,
  async mount(el) {
    el.innerHTML = `
      <h2>Nodes</h2>
      <div class="toolbar">
        <button type="button" id="n-refresh">Refresh</button>
        <span class="muted" id="n-status">Loading...</span>
      </div>
      <div class="card"><table class="table" id="n-table">
        <thead><tr>
          <th>Node</th><th>Status</th><th>Capacity</th><th>Workers</th><th>Actions</th>
        </tr></thead>
        <tbody></tbody>
      </table></div>`;
    el.querySelector("#n-refresh").onclick = () => this.load(el);
    await this.load(el);
    this.timer = setInterval(() => this.load(el), 5000);
  },
  unmount() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  },
  async load(el) {
    const status = el.querySelector("#n-status");
    const tbody = el.querySelector("#n-table tbody");
    try {
      const data = await api.v1.nodes();
      const nodes = data.nodes || [];
      status.textContent = nodes.length + " node(s)";
      tbody.innerHTML = "";
      if (!nodes.length) {
        tbody.innerHTML = `<tr><td colspan="5" class="muted">No agentd nodes joined</td></tr>`;
        return;
      }
      for (const n of nodes) {
        const tr = document.createElement("tr");
        const stale = n.stale;
        const accepting = n.accepting !== false;
        const badge = stale
          ? `<span class="badge bad">stale</span>`
          : accepting
            ? `<span class="badge ok">online</span>`
            : `<span class="badge warn">draining</span>`;
        const workers = (n.workers || [])
          .map((w) => `${w.id}:${w.status}${w.job_id ? "(" + w.job_id + ")" : ""}`)
          .join(" · ") || "—";
        tr.innerHTML = `
          <td><strong>${esc(n.id)}</strong></td>
          <td>${badge}</td>
          <td>idle ${n.idle} / busy ${n.busy} / max ${n.max_workers}</td>
          <td class="workers">${esc(workers)}</td>
          <td class="toolbar" style="margin:0"></td>`;
        const actions = tr.querySelector("td:last-child");
        actions.appendChild(btn("+", async () => {
          await api.v1.patchNode(n.id, { max_workers: (n.max_workers || 1) + 1 });
          await this.load(el);
        }));
        actions.appendChild(btn("−", async () => {
          const next = Math.max(1, (n.max_workers || 1) - 1);
          await api.v1.patchNode(n.id, { max_workers: next });
          await this.load(el);
        }));
        actions.appendChild(btn(accepting ? "Drain" : "Enable", async () => {
          await api.v1.patchNode(n.id, { accepting: !accepting });
          await this.load(el);
        }, accepting ? "danger" : ""));
        tbody.appendChild(tr);
      }
    } catch (e) {
      status.textContent = e.message || String(e);
      if (e.status === 401) location.hash = "#/login";
    }
  },
};

function btn(label, onClick, cls) {
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = label;
  if (cls) b.className = cls;
  b.onclick = () => onClick().catch((e) => alert(e.message || e));
  return b;
}
function esc(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
