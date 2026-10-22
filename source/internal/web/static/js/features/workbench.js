// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Workbench — the console's interactive surface (p_0.45.9).
 *
 * A person types a request, an agent runs it on a worker, and this renders what
 * happens: assistant text, a card per tool call, step progress, and a terminal
 * state. It is the one place in anvil-web where work is *started* rather than
 * planned.
 *
 * One session per worker: two agents on one checkout damage it the same
 *     way two writers damage a transcript; the server refuses and this renders
 *     the refusal.
 *
 * The event renderer here is the same one the job history (p_0.45.12) uses, so a
 * phase Job started from the Roadmap will read identically.
 */
window.Features = window.Features || {};
window.Features.workbench = {
  el: null,
  _sessionId: null,
  _abort: null,
  _cards: null,
  _steps: 0,
  _approvals: null,

  async mount(el) {
    this.el = el;
    this._sessionId = null;
    this._cards = new Map();
    this._approvals = new Map();
    if (!this._snapshotCache) {
      this._snapshotCache = window.AnvilUI.swr.createStore({ maxEntries: 2, maxAgeMs: 5 * 60 * 1000 });
    }
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
          <div id="wb-snapshot-state"></div>
          <div id="wb-sessions" class="wb-sessions">${this.skeleton(3)}</div>
          <h3>Start</h3>
          <form id="wb-form">
            <label for="wb-worker">Worker</label>
            <select id="wb-worker"><option value="">Loading…</option></select>
            <label for="wb-prompt">What should the agent do?</label>
            <textarea id="wb-prompt" rows="4" placeholder="Read the failing test and fix it." required></textarea>
            <div class="err" id="wb-err"></div>
            <button type="submit" id="wb-start" style="width:100%">Start Session</button>
          </form>
        </aside>
        <section class="wb-main">
          <div id="wb-session-head" class="wb-session-head muted">No Session selected.</div>
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

    const cached = this._snapshotCache.peek("workbench:sessions");
    if (cached) {
      this._sessions = cached.data.sessions || [];
      this._snapshotState = { stale: true, refreshing: true, updatedAt: cached.updatedAt, error: "" };
      this.paintSessions();
    }
    await Promise.all([this.loadWorkers(), this.refresh()]);
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

  /** loadWorkers fills the picker from the Fleet inventory Delivery already reads. */
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
      sel.innerHTML = `<option value="">No Workers bound — bind one in Clusters</option>`;
      return;
    }
    sel.innerHTML = opts
      .map((o) => `<option value="${window.AnvilUI.esc(o.id)}">${window.AnvilUI.esc(o.label)}</option>`)
      .join("");
  },

  async refresh() {
    const box = this.el?.querySelector("#wb-sessions");
    if (!box) return;
    this._snapshotState = { ...(this._snapshotState || {}), refreshing: true, error: "" };
    if (this._sessions) this.paintSessions();
    try {
      const result = await this._snapshotCache.revalidate(
        "workbench:sessions",
        () => api.v1.workbenchSessions(),
        {
          // Prompts and event content are intentionally excluded. The cached
          // list is only enough to identify and reopen a Session.
          project: (data) => ({
            sessions: (data.sessions || []).map((session) => ({
              id: session.id,
              worker_id: session.worker_id,
              state: session.state,
            })),
          }),
        },
      );
      if (!result.accepted || !this.el) return;
      this._sessions = result.snapshot.sessions || [];
      this._snapshotState = {
        stale: false,
        refreshing: false,
        updatedAt: result.updatedAt,
        error: "",
      };
    } catch (error) {
      this._snapshotState = {
        ...(this._snapshotState || {}),
        stale: Boolean(this._sessions),
        refreshing: false,
        error: error?.message || String(error),
      };
    }
    this.paintSessions();
  },

  paintSessions() {
    const box = this.el?.querySelector("#wb-sessions");
    const stateBox = this.el?.querySelector("#wb-snapshot-state");
    if (!box || !stateBox) return;
    const rows = this._sessions || [];
    if (rows.length === 0) {
      box.innerHTML = `<div class="del-empty"><p class="muted">No Sessions yet. Start one on the right.</p></div>`;
    } else {
      box.innerHTML = rows.map(
        (s) => `
        <button type="button" class="wb-sess${s.id === this._sessionId ? " active" : ""}" data-wb-open="${window.AnvilUI.esc(s.id)}">
          <span class="wb-sess-state ${window.AnvilUI.badgeClass(s.state)}"></span>
          <span class="wb-sess-body">
            <span class="wb-sess-prompt">${window.AnvilUI.esc(s.id || "Session")}</span>
            <span class="muted wb-sess-meta">${window.AnvilUI.esc(s.worker_id)} · ${window.AnvilUI.esc(s.state)}</span>
          </span>
        </button>`,
      ).join("");
    }
    const state = this._snapshotState || {};
    const age = window.AnvilUI.swr.ageLabel(state.updatedAt);
    stateBox.innerHTML = state.error
      ? `<div class="snapshot-state bad" role="status">Showing last successful snapshot · updated ${window.AnvilUI.esc(age)} · disconnected <button type="button" class="ghost" data-wb-retry>Retry</button></div>`
      : state.refreshing || state.stale
        ? `<div class="snapshot-state warn" role="status">Showing last successful snapshot · updated ${window.AnvilUI.esc(age)} · refreshing…</div>`
        : state.updatedAt
          ? `<div class="snapshot-state" role="status">Updated ${window.AnvilUI.esc(age)}</div>`
          : "";
    stateBox.querySelector("[data-wb-retry]")?.addEventListener("click", () => this.refresh());
    box.querySelectorAll("[data-wb-open]").forEach((btn) => {
      btn.addEventListener("click", () => this.attach(btn.getAttribute("data-wb-open")));
    });
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
   * showApprovalLimit renders a capability note only for bridged read-only jobs.
   *
   * It reads approval_supported from the session payload rather than assuming:
   * Interactive Workbench sessions support approvals; old/read-only records do
   * not grow controls they cannot route.
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
      note.textContent = sess.read_only
        ? "This Phase Job is read-only here. Interactive approvals are available only on Workbench-started Sessions."
        : "Interactive approvals are unavailable for this stored Session.";
    } catch (_) {
      // A session that cannot be described must not produce a claim about it.
    }
  },

  /** attach opens a session: replay first, then follow. */
  async attach(sessionId) {
    this.closeStream();
    this._sessionId = sessionId;
    this._cards = new Map();
    this._approvals = new Map();
    this._steps = 0;
    // A fresh resumer per session: seq numbers belong to one job, so carrying a
    // seen-set across sessions would drop the first events of the next one.
    this._resumer = window.AnvilUI.stream.createResumer();
    this._ended = false;
    this._lastEventAt = 0;
    const stream = this.el?.querySelector("#wb-stream");
    const head = this.el?.querySelector("#wb-session-head");
    const stop = this.el?.querySelector("#wb-stop");
    if (!stream) return;
    stream.innerHTML = "";
    this.setStatus("connecting…");
    if (head) head.textContent = sessionId;
    let readOnly = false;
    try {
      const info = await api.v1.workbenchSession(sessionId);
      readOnly = Boolean(info.read_only);
    } catch (_) {}

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
          this._lastEventAt = Date.now();
          this.setConn(this._connState || "live");
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
          if (stop) stop.disabled = readOnly;
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
    this._connState = state;
    const label = window.AnvilUI.stream.connectionLabel(state);
    const last = this._lastEventAt
      ? ` · last event ${window.AnvilUI.swr.ageLabel(this._lastEventAt)}`
      : "";
    el.hidden = state === "";
    el.textContent = label + last;
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
    if (type === "approval_request" && ev.approval) {
      this.renderApproval(ev.approval);
      return;
    }
    if (type === "approval_result" && ev.approval_result) {
      this.settleApproval(ev.approval_result);
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

  renderApproval(req) {
    const stream = this.el?.querySelector("#wb-stream");
    if (!stream || !req?.request_id || this._approvals.has(req.request_id)) return;
    const card = document.createElement("section");
    card.className = "wb-approval";
    card.dataset.approval = req.request_id;

    const title = document.createElement("strong");
    title.textContent = "Approval required: " + (req.tool || "tool");
    card.appendChild(title);
    const meta = document.createElement("div");
    meta.className = "muted";
    meta.textContent = req.expires_at ? "Expires " + req.expires_at : "Waiting for your decision";
    card.appendChild(meta);
    if (req.preview?.summary || req.preview?.path || req.preview?.command) {
      const preview = document.createElement("pre");
      preview.textContent = req.preview.summary || req.preview.path || (req.preview.command || []).join(" ");
      card.appendChild(preview);
    }
    const label = document.createElement("label");
    label.textContent = "Arguments (editable JSON)";
    const args = document.createElement("textarea");
    args.rows = 5;
    args.value = this.pretty(req.args || {});
    label.appendChild(args);
    card.appendChild(label);

    const actions = document.createElement("div");
    actions.className = "toolbar";
    const add = (decision, text, danger = false) => {
      if (!Array.isArray(req.decisions) || !req.decisions.includes(decision)) return;
      const btn = document.createElement("button");
      btn.type = "button";
      if (danger) btn.className = "danger";
      btn.textContent = text;
      btn.addEventListener("click", () => this.answerApproval(req, decision, args, card));
      actions.appendChild(btn);
    };
    add("allow", "Allow");
    add("allow_session", "Allow for session");
    add("deny", "Deny", true);
    card.appendChild(actions);
    const status = document.createElement("div");
    status.className = "wb-approval-status muted";
    card.appendChild(status);
    stream.appendChild(card);
    this._approvals.set(req.request_id, { card, state: "pending" });
    this.scroll(stream);
  },

  async answerApproval(req, decision, argsEl, card) {
    const item = this._approvals.get(req.request_id);
    if (!item || item.state !== "pending") return;
    let args;
    try {
      args = JSON.parse(argsEl.value || "{}");
    } catch (_) {
      card.querySelector(".wb-approval-status").textContent = "Arguments must be valid JSON.";
      return;
    }
    item.state = this.approvalNextState(item.state, "respond");
    card.querySelectorAll("button").forEach((b) => (b.disabled = true));
    card.querySelector(".wb-approval-status").textContent = "Sending decision…";
    try {
      await api.v1.workbenchApproval(this._sessionId, req.request_id, {
        decision,
        scope: decision === "allow_session" ? req.preview?.path || "" : "",
        args: decision === "deny" ? undefined : args,
      });
      item.state = this.approvalNextState(item.state, "answered");
      card.querySelector(".wb-approval-status").textContent =
        decision === "deny" ? "Denied." : decision === "allow_session" ? "Allowed for this session." : "Allowed.";
    } catch (e) {
      item.state = this.approvalNextState(item.state, "expired");
      card.classList.add("wb-approval-expired");
      card.querySelector(".wb-approval-status").textContent = e.message || "Approval is no longer pending.";
    }
  },

  settleApproval(outcome) {
    const item = this._approvals.get(outcome?.request_id);
    if (!item) return;
    item.state = this.approvalNextState(item.state, outcome.status || "answered");
    item.card.querySelectorAll("button").forEach((b) => (b.disabled = true));
    if (item.state === "expired" || item.state === "cancelled") item.card.classList.add("wb-approval-expired");
    const status = item.card.querySelector(".wb-approval-status");
    if (status) status.textContent = outcome.message || item.state;
  },

  approvalNextState(current, event) {
    const terminal = ["answered", "expired", "cancelled"];
    if (terminal.includes(current)) return current;
    if (current === "pending" && event === "respond") return "responding";
    if ((current === "pending" || current === "responding") && terminal.includes(event)) return event;
    return current;
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
  module.exports = {
    splitFrames: (...a) => window.Features.workbench.splitFrames(...a),
    approvalNextState: (...a) => window.Features.workbench.approvalNextState(...a),
  };
}
