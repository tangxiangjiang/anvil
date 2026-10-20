// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Workbench — the console's interactive surface (p_0.46.7).
 *
 * A person types a request, an agent runs it on a worker, and this renders what
 * happens: assistant text, a card per tool call, step progress, and a terminal
 * state. It is the one place in anvil-web where work is *started* rather than
 * planned.
 *
 * Two deliberate limits, both stated in the UI instead of hidden:
 *
 *   - **No approvals.** The daemon's job protocol carries no approval event and
 *     no channel to answer one, so a gated tool call inside a Worker job is
 *     decided by the worker's own options, not by a person here. Showing an
 *     approval card that could never be delivered would be worse than saying so.
 *   - **One session per worker.** Two agents on one checkout damage it the same
 *     way two writers damage a transcript; the server refuses and this renders
 *     the refusal.
 *
 * The event renderer here is the same one the job history (p_0.46.10) uses, so a
 * phase Job started from the Roadmap will read identically.
 */
window.Features = window.Features || {};
window.Features.workbench = {
  el: null,
  _sessionId: null,
  _abort: null,
  _cards: null,
  _steps: 0,

  async mount(el) {
    this.el = el;
    this._sessionId = null;
    this._cards = new Map();
    el.innerHTML = `
      <div class="del-head">
        <div>
          <h2>Workbench</h2>
          <p class="muted del-sub">Pick a worker, describe the task, watch it run.</p>
        </div>
        <div class="toolbar" style="margin:0">
          <button type="button" class="ghost" id="wb-refresh">Refresh</button>
        </div>
      </div>
      <div class="wb-grid">
        <aside class="wb-side">
          <h3 style="margin-top:0">Sessions</h3>
          <div id="wb-sessions" class="wb-sessions">${this.skeleton(3)}</div>
          <h3>Start</h3>
          <form id="wb-form">
            <label for="wb-worker">Worker</label>
            <select id="wb-worker"><option value="">Loading…</option></select>
            <label for="wb-prompt">What should the agent do?</label>
            <textarea id="wb-prompt" rows="4" placeholder="Read the failing test and fix it." required></textarea>
            <div class="err" id="wb-err"></div>
            <button type="submit" id="wb-start" style="width:100%">Start session</button>
          </form>
        </aside>
        <section class="wb-main">
          <div id="wb-session-head" class="wb-session-head muted">No session selected.</div>
          <div id="wb-approval-note" class="wb-note" hidden></div>
          <div id="wb-conn" class="wb-conn" hidden></div>
          <div id="wb-stream" class="wb-stream" role="log" aria-live="polite"></div>
          <div class="wb-composer">
            <button type="button" id="wb-stop" class="danger" disabled>Stop</button>
            <span class="muted" id="wb-status"></span>
          </div>
        </section>
      </div>`;

    el.querySelector("#wb-refresh").addEventListener("click", () => this.refresh());
    el.querySelector("#wb-form").addEventListener("submit", (e) => {
      e.preventDefault();
      return this.start();
    });
    el.querySelector("#wb-stop").addEventListener("click", () => this.stop());

    await this.loadWorkers();
    await this.refresh();
  },

  unmount() {
    this.closeStream();
    this.el = null;
  },

  onRoute() {
    this.closeStream();
  },

  skeleton(rows) {
    let out = `<div class="del-skeleton" aria-hidden="true">`;
    for (let i = 0; i < rows; i++) out += `<div class="del-skeleton-row"></div>`;
    return `${out}</div>`;
  },

  /** loadWorkers fills the picker from the cluster inventory Delivery already reads. */
  async loadWorkers() {
    const sel = this.el?.querySelector("#wb-worker");
    if (!sel) return;
    const opts = [];
    try {
      const data = await api.v1.clusters();
      for (const f of data.clusters || []) {
        let ws = [];
        try {
          const wd = await api.v1.listWorkspaces(f.id);
          ws = wd.workspaces || [];
        } catch (_) {
          ws = [];
        }
        for (const w of ws) {
          for (const proj of w.projects || []) {
            for (const worker of proj.workers || []) {
              const id = worker.id || worker.worker_id;
              if (!id) continue;
              const label = [f.name || f.id, proj.name || proj.id, worker.name || id]
                .filter(Boolean)
                .join(" · ");
              opts.push({ id, label });
            }
          }
        }
      }
    } catch (e) {
      sel.innerHTML = `<option value="">Could not load workers</option>`;
      return;
    }
    if (opts.length === 0) {
      sel.innerHTML = `<option value="">No workers bound — bind one in Clusters</option>`;
      return;
    }
    sel.innerHTML = opts
      .map((o) => `<option value="${window.AnvilUI.esc(o.id)}">${window.AnvilUI.esc(o.label)}</option>`)
      .join("");
  },

  async refresh() {
    const box = this.el?.querySelector("#wb-sessions");
    if (!box) return;
    try {
      const data = await api.v1.workbenchSessions();
      const rows = data.sessions || [];
      if (rows.length === 0) {
        box.innerHTML = `<div class="del-empty"><p class="muted">No sessions yet. Start one on the right.</p></div>`;
        return;
      }
      box.innerHTML = rows
        .map(
          (s) => `
        <button type="button" class="wb-sess${s.id === this._sessionId ? " active" : ""}" data-wb-open="${window.AnvilUI.esc(s.id)}">
          <span class="wb-sess-state ${window.AnvilUI.badgeClass(s.state)}"></span>
          <span class="wb-sess-body">
            <span class="wb-sess-prompt">${window.AnvilUI.esc((s.prompt || "").slice(0, 80))}</span>
            <span class="muted wb-sess-meta">${window.AnvilUI.esc(s.worker_id)} · ${window.AnvilUI.esc(s.state)}</span>
          </span>
        </button>`,
        )
        .join("");
      box.querySelectorAll("[data-wb-open]").forEach((btn) => {
        btn.addEventListener("click", () => this.attach(btn.getAttribute("data-wb-open")));
      });
    } catch (e) {
      box.innerHTML = `<div class="err">${window.AnvilUI.esc(e.message || String(e))}</div>`;
    }
  },

  async start() {
    const el = this.el;
    const errEl = el.querySelector("#wb-err");
    const btn = el.querySelector("#wb-start");
    const workerId = el.querySelector("#wb-worker").value;
    const prompt = el.querySelector("#wb-prompt").value.trim();
    if (errEl) errEl.textContent = "";
    if (!workerId) {
      if (errEl) errEl.textContent = "Pick a worker first.";
      return;
    }
    if (!prompt) {
      if (errEl) errEl.textContent = "Describe the task.";
      return;
    }
    return window.AnvilUI.withAction(
      btn,
      { key: "wb-start", runningLabel: "Starting…", host: el.querySelector("#del-ui-modal"), errorTitle: "Start failed" },
      async () => {
        try {
          const sess = await api.v1.workbenchStart({ worker_id: workerId, prompt });
          el.querySelector("#wb-prompt").value = "";
          await this.refresh();
          await this.attach(sess.id);
        } catch (e) {
          if (errEl) errEl.textContent = e.message || String(e);
        }
      },
    );
  },

  async stop() {
    const id = this._sessionId;
    if (!id) return;
    const btn = this.el?.querySelector("#wb-stop");
    return window.AnvilUI.withAction(
      btn,
      { key: "wb-stop", runningLabel: "Stopping…", host: this.el.querySelector("#del-ui-modal"), errorTitle: "Stop failed" },
      async () => {
        await api.v1.workbenchCancel(id);
        this.setStatus("Stopping…");
      },
    );
  },

  setStatus(text) {
    const el = this.el?.querySelector("#wb-status");
    if (el) el.textContent = text || "";
  },

  /**
   * showApprovalLimit renders the capability note for a session.
   *
   * It reads approval_supported from the session payload rather than assuming:
   * if the job protocol ever gains an approval channel, the backend flips this
   * flag and the note disappears without a client change.
   */
  async showApprovalLimit(sessionId) {
    const note = this.el?.querySelector("#wb-approval-note");
    if (!note) return;
    note.hidden = true;
    note.textContent = "";
    try {
      const sess = await api.v1.workbenchSession(sessionId);
      if (sess.approval_supported) return;
      note.hidden = false;
      note.textContent =
        "Approvals are not delivered for this run: a gated tool call is decided by the worker's own options, not here.";
    } catch (_) {
      // A session that cannot be described must not produce a claim about it.
    }
  },

  /** attach opens a session: replay first, then follow. */
  async attach(sessionId) {
    this.closeStream();
    this._sessionId = sessionId;
    this._cards = new Map();
    this._steps = 0;
    // A fresh resumer per session: seq numbers belong to one job, so carrying a
    // seen-set across sessions would drop the first events of the next one.
    this._resumer = window.AnvilUI.stream.createResumer();
    this._ended = false;
    const stream = this.el?.querySelector("#wb-stream");
    const head = this.el?.querySelector("#wb-session-head");
    const stop = this.el?.querySelector("#wb-stop");
    if (!stream) return;
    stream.innerHTML = "";
    this.setStatus("connecting…");
    if (head) head.textContent = sessionId;

    // State the limit the backend reports, once, per attached session. The job
    // protocol carries no approval event and no channel to answer one, so a
    // gated tool call inside this run is decided by the worker's options - not
    // by a person here. Saying so is the difference between a known limit and a
    // missing feature someone waits for.
    this.showApprovalLimit(sessionId);

    // Follow, and keep following: the stream replays then goes live, so a drop
    // resumes without losing or duplicating an event. Without the dedupe a
    // reconnect would redraw the whole conversation.
    for (;;) {
      const ac = new AbortController();
      this._abort = ac;
      const outcome = await this.consume(api.v1.workbenchEventsURL(sessionId), ac.signal, {
        onEvent: (ev) => {
          if (!this._resumer.firstSeen(ev)) return;
          this.renderEvent(ev);
        },
        onDone: (done) => {
          this._ended = true;
          if (stop) stop.disabled = true;
          this.renderDone(done);
          this.setStatus(done.status === "cancelled" ? "stopped" : done.status);
          this.setConn("ended");
          this.refresh();
        },
        onOpen: () => {
          this._resumer.opened();
          if (stop) stop.disabled = false;
          this.setStatus("running");
          this.setConn("live");
        },
      });
      if (this._ended || outcome === "aborted" || this._sessionId !== sessionId) return;
      const next = this._resumer.dropped();
      if (!next.retry) {
        this.setConn("offline");
        this.setStatus("disconnected — press Refresh to retry");
        return;
      }
      this.setConn("reconnecting");
      await new Promise((r) => setTimeout(r, next.delayMs));
      if (this._sessionId !== sessionId) return;
    }
  },

  /** setConn reflects the connection state in the banner. */
  setConn(state) {
    const el = this.el?.querySelector("#wb-conn");
    if (!el) return;
    const label = window.AnvilUI.stream.connectionLabel(state);
    el.hidden = state === "live" || state === "";
    el.textContent = label;
    el.className = "wb-conn" + (state === "offline" ? " bad" : state === "reconnecting" ? " warn" : "");
  },

  closeStream() {
    if (this._abort) {
      try {
        this._abort.abort();
      } catch (_) {}
      this._abort = null;
    }
  },

  /**
   * consume reads an SSE stream.
   *
   * Written here rather than shared with the Delivery job stream because the two
   * differ in what they do with a frame, and a shared parser with a callback per
   * frame would be the only thing left in it. The parsing rules are the same:
   * `event:` and `data:` lines, a blank line ends a frame, and a partial frame
   * at a read boundary is buffered rather than dropped.
   */
  async consume(url, signal, { onEvent, onDone, onOpen }) {
    let res;
    try {
      const tok = api.getToken?.() || "";
      res = await fetch(url, {
        headers: tok ? { Authorization: "Bearer " + tok } : {},
        signal,
      });
    } catch (e) {
      if (e?.name === "AbortError") return "aborted";
      return "dropped";
    }
    if (!res.ok || !res.body) return "dropped";
    if (onOpen) onOpen();
    const reader = res.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    let name = "";
    for (;;) {
      let chunk;
      try {
        chunk = await reader.read();
      } catch (e) {
        if (e?.name === "AbortError") return "aborted";
        return "dropped";
      }
      if (chunk.done) return this._ended ? "ended" : "dropped";
      buf += dec.decode(chunk.value, { stream: true });
      const lines = buf.split("\n");
      buf = lines.pop() || "";
      for (const line of lines) {
        if (line.startsWith(":")) continue; // heartbeat comment
        if (line.startsWith("event:")) {
          name = line.slice(6).trim();
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
          if (name === "job.event" && onEvent) onEvent(data);
          else if (name === "job.done" && onDone) onDone(data);
          continue;
        }
        if (line === "") name = "";
      }
    }
    return this._ended ? "ended" : "dropped";
  },

  /** renderEvent appends one frame: assistant text, a tool card, or progress. */
  renderEvent(ev) {
    const stream = this.el?.querySelector("#wb-stream");
    if (!stream || !ev) return;
    const type = ev.type || "";
    if (type === "assistant" && ev.text) {
      const last = stream.lastElementChild;
      if (last && last.classList.contains("wb-say")) {
        last.textContent += ev.text; // append, not per-token cards
      } else {
        const div = document.createElement("div");
        div.className = "wb-say";
        div.textContent = ev.text;
        stream.appendChild(div);
      }
      this.scroll(stream);
      return;
    }
    if (type === "tool_call") {
      const card = document.createElement("details");
      card.className = "wb-tool";
      card.open = false;
      card.dataset.call = ev.tool_call_id || "";
      const summary = document.createElement("summary");
      summary.textContent = `⚙ ${ev.name || "tool"}`;
      card.appendChild(summary);
      const args = document.createElement("pre");
      args.className = "wb-tool-args";
      args.textContent = this.pretty(ev.args);
      card.appendChild(args);
      stream.appendChild(card);
      this._cards.set(ev.tool_call_id || ev.name, card);
      this.scroll(stream);
      return;
    }
    if (type === "tool_result") {
      const card = this._cards.get(ev.tool_call_id) || stream.lastElementChild;
      if (card && card.classList.contains("wb-tool")) {
        if (ev.is_error) card.classList.add("wb-tool-error");
        const out = document.createElement("pre");
        out.className = "wb-tool-out";
        out.textContent = String(ev.content || "").slice(0, 4000);
        card.appendChild(out);
      }
      this.scroll(stream);
      return;
    }
    if (type === "progress") {
      this._steps = ev.step || this._steps;
      const max = ev.max_steps ? `/${ev.max_steps}` : "";
      this.setStatus(`step ${this._steps}${max}`);
      return;
    }
    if (type === "error") {
      const div = document.createElement("div");
      div.className = "wb-error";
      div.textContent = ev.text || "error";
      stream.appendChild(div);
      this.scroll(stream);
      return;
    }
    if (type === "todo" && Array.isArray(ev.items)) {
      const box = document.createElement("ul");
      box.className = "wb-todos";
      for (const it of ev.items) {
        const li = document.createElement("li");
        li.textContent = `${it.status === "done" ? "✔" : "○"} ${it.text}`;
        box.appendChild(li);
      }
      stream.appendChild(box);
      this.scroll(stream);
      return;
    }
  },

  renderDone(done) {
    const stream = this.el?.querySelector("#wb-stream");
    if (!stream || !done) return;
    const div = document.createElement("div");
    const cancelled = done.status === "cancelled";
    div.className = "wb-done" + (cancelled ? " wb-done-cancelled" : done.error ? " wb-done-error" : "");
    div.textContent = cancelled
      ? "Stopped."
      : done.error
        ? `Failed: ${done.error}`
        : done.result || "Finished.";
    stream.appendChild(div);
    this.scroll(stream);
  },

  pretty(v) {
    if (v == null) return "";
    if (typeof v === "string") return v;
    try {
      return JSON.stringify(v, null, 2).slice(0, 2000);
    } catch (_) {
      return String(v);
    }
  },

  scroll(stream) {
    stream.scrollTop = stream.scrollHeight;
  },

  // ------------------------------------------------------------------ test seam
  // The SSE frame splitter is the part worth testing without a DOM: a partial
  // frame across a read boundary is the bug that makes a stream look truncated.
  splitFrames(text) {
    const out = [];
    let name = "";
    for (const line of String(text).split("\n")) {
      if (line.startsWith(":")) continue;
      if (line.startsWith("event:")) {
        name = line.slice(6).trim();
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
        out.push({ event: name, data });
        continue;
      }
      if (line === "") name = "";
    }
    return out;
  },
};

if (typeof module !== "undefined" && module.exports) {
  module.exports = { splitFrames: (...a) => window.Features.workbench.splitFrames(...a) };
}
