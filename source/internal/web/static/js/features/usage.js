window.Features = window.Features || {};
window.Features.usage = {
  chart: null,
  async mount(el) {
    el.innerHTML = `
      <h2>Token usage</h2>
      <div class="toolbar">
        <button type="button" id="u-refresh">Refresh</button>
        <span class="muted" id="u-status"></span>
      </div>
      <div class="stats" id="u-stats"></div>
      <div class="card chart-wrap"><canvas id="u-chart" height="120"></canvas></div>
      <div class="card"><table class="table" id="u-table">
        <thead><tr><th>Node</th><th>Jobs</th><th>Input</th><th>Output</th><th>Total</th></tr></thead>
        <tbody></tbody>
      </table></div>`;
    el.querySelector("#u-refresh").onclick = () => this.load(el);
    await this.load(el);
  },
  unmount() {
    if (this.chart) { this.chart.destroy(); this.chart = null; }
  },
  async load(el) {
    const status = el.querySelector("#u-status");
    try {
      const data = await api.v1.usage();
      const g = data.global || {};
      el.querySelector("#u-stats").innerHTML = `
        <div class="stat"><div class="n">${g.total_tokens || 0}</div><div class="l">Total tokens</div></div>
        <div class="stat"><div class="n">${g.input_tokens || 0}</div><div class="l">Input</div></div>
        <div class="stat"><div class="n">${g.output_tokens || 0}</div><div class="l">Output</div></div>
        <div class="stat"><div class="n">${g.jobs || 0}</div><div class="l">Jobs</div></div>`;
      const nodes = data.nodes || {};
      const labels = Object.keys(nodes);
      const totals = labels.map((k) => (nodes[k].total_tokens || 0));
      const tbody = el.querySelector("#u-table tbody");
      tbody.innerHTML = labels.length
        ? labels.map((k) => {
            const n = nodes[k];
            return `<tr><td>${k}</td><td>${n.jobs || 0}</td><td>${n.input_tokens || 0}</td><td>${n.output_tokens || 0}</td><td>${n.total_tokens || 0}</td></tr>`;
          }).join("")
        : `<tr><td colspan="5" class="muted">No usage recorded yet</td></tr>`;
      const ctx = el.querySelector("#u-chart");
      if (this.chart) this.chart.destroy();
      this.chart = new Chart(ctx, {
        type: "bar",
        data: {
          labels: labels.length ? labels : ["(none)"],
          datasets: [{
            label: "Total tokens",
            data: labels.length ? totals : [0],
            backgroundColor: "#3d9cf088",
            borderColor: "#3d9cf0",
            borderWidth: 1,
          }],
        },
        options: {
          plugins: { legend: { labels: { color: "#8b9bb0" } } },
          scales: {
            x: { ticks: { color: "#8b9bb0" }, grid: { color: "#2c3847" } },
            y: { ticks: { color: "#8b9bb0" }, grid: { color: "#2c3847" }, beginAtZero: true },
          },
        },
      });
      status.textContent = "Updated";
    } catch (e) {
      status.textContent = e.message || String(e);
      if (e.status === 401) location.hash = "#/login";
    }
  },
};
