/*
 * The Tasks document.
 *
 * It draws one row per background task the extension listed, and one task's
 * output when asked. Every value it writes came from the host — a command line,
 * a child's answer, a description a person typed — so all of it is text: there
 * is no innerHTML anywhere, and a `<pre>` is a text node by another name.
 *
 * The document never invents a status. A row says "running" because the host
 * said so; Cancel only asks, and the next state message is what moves the row.
 * Elapsed time is the one thing computed here, and it is arithmetic on two
 * timestamps rather than a claim about the task.
 */
(function () {
  const vscode = acquireVsCodeApi();
  const rowsEl = document.getElementById('rows');
  const statusEl = document.getElementById('status');
  const refreshEl = document.getElementById('refresh');
  const outputEl = document.getElementById('output');
  const outputTitleEl = document.getElementById('outputTitle');
  const outputTextEl = document.getElementById('outputText');
  const droppedEl = document.getElementById('dropped');
  const outputRefreshEl = document.getElementById('outputRefresh');
  const outputCloseEl = document.getElementById('outputClose');
  const reduceMotion =
    typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;

  /** The rows from the last state message, kept for elapsed updates. */
  let rows = [];
  /** The (session, task) the output pane is showing, or null when closed. */
  let selected = null;

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  /** The same quiet three-dot pulse used by a background spawn in the chat. */
  function backgroundActivity() {
    const activity = el('span', 'background-activity');
    activity.setAttribute('aria-hidden', 'true');
    for (let i = 0; i < 3; i++) {
      const dot = el('span', 'background-dot');
      activity.appendChild(dot);
      if (!reduceMotion && typeof dot.animate === 'function') {
        dot.animate(
          [
            { transform: 'translateY(0) scale(0.75)', opacity: 0.32 },
            { transform: 'translateY(-2px) scale(1)', opacity: 1 },
            { transform: 'translateY(0) scale(0.75)', opacity: 0.32 },
          ],
          { duration: 960, delay: i * 160, iterations: Infinity, easing: 'ease-in-out' },
        );
      }
    }
    return activity;
  }

  function setStatus(text) {
    statusEl.textContent = text || '';
    statusEl.hidden = !text;
  }

  /** Seconds between two ISO timestamps, floored, and never negative. */
  function secondsBetween(startedAt, endedAt) {
    const start = Date.parse(startedAt || '');
    if (isNaN(start)) return 0;
    const end = endedAt ? Date.parse(endedAt) : Date.now();
    if (isNaN(end)) return 0;
    return Math.max(0, Math.floor((end - start) / 1000));
  }

  function formatElapsed(seconds) {
    const s = seconds % 60;
    const m = Math.floor(seconds / 60) % 60;
    const h = Math.floor(seconds / 3600);
    const mm = String(m).padStart(2, '0');
    const ss = String(s).padStart(2, '0');
    return h > 0 ? h + ':' + mm + ':' + ss : m + ':' + ss;
  }

  function iso(value) {
    return typeof value === 'string' ? value : '';
  }

  /** The elapsed cell of one row, updated in place each second. */
  function elapsedOf(row) {
    // A task that ended without an end stamp is not still ticking: an elapsed
    // time that kept counting would be a claim the row cannot support.
    if (row.status !== 'running' && !row.endedAt) return '—';
    if (isNaN(Date.parse(iso(row.startedAt)))) return '—';
    return formatElapsed(secondsBetween(iso(row.startedAt), iso(row.endedAt)));
  }

  function tickElapsed() {
    const cells = rowsEl.querySelectorAll('.elapsed[data-task-id]');
    for (let i = 0; i < cells.length; i++) {
      const cell = cells[i];
      const row = rows.find((candidate) => candidate.id === cell.getAttribute('data-task-id'));
      if (row) cell.textContent = elapsedOf(row);
    }
  }
  setInterval(tickElapsed, 1000);

  function action(label, onClick) {
    const button = el('button', 'action', label);
    button.type = 'button';
    button.addEventListener('click', onClick);
    return button;
  }

  function drawRow(row) {
    const node = el('div', 'row');
    node.setAttribute('data-task-id', row.id);

    node.appendChild(el('span', 'kind', row.kind === 'agent' ? 'AGENT' : 'SHELL'));
    node.appendChild(el('span', 'title', row.title));

    const status = el('span', 'status status-' + row.status, row.status);
    if (row.status === 'running') status.appendChild(backgroundActivity());
    node.appendChild(status);

    const elapsed = el('span', 'elapsed', elapsedOf(row));
    elapsed.setAttribute('data-task-id', row.id);
    node.appendChild(elapsed);

    // The owning agent is useful when a child's own work is in the list: the
    // same command started by two agents is two rows that look alike otherwise.
    if (row.agentId) node.appendChild(el('span', 'agent', row.agentId));

    const actions = el('span', 'actions');
    if (row.transcriptAgentId) {
      actions.appendChild(
        action('Open transcript', () => {
          vscode.postMessage({ type: 'openTranscript', agentId: row.transcriptAgentId });
        }),
      );
    }
    if (row.kind === 'shell') {
      actions.appendChild(
        action('View output', () => {
          selected = { sessionId: row.sessionId, taskId: row.id, title: row.title };
          openOutput(row.title);
          vscode.postMessage({ type: 'output', sessionId: row.sessionId, taskId: row.id });
        }),
      );
    }
    if (row.status === 'running') {
      actions.appendChild(
        action('Cancel', (event) => {
          const button = event.currentTarget;
          button.disabled = true;
          button.textContent = 'Cancelling…';
          vscode.postMessage({ type: 'cancel', sessionId: row.sessionId, taskId: row.id });
        }),
      );
    }
    node.appendChild(actions);
    return node;
  }

  function renderState(msg) {
    rows = Array.isArray(msg.rows) ? msg.rows : [];
    rowsEl.textContent = '';
    if (msg.error) {
      setStatus(msg.error);
    } else if (rows.length === 0) {
      setStatus('No background tasks in this window.');
    } else {
      setStatus('');
    }
    // Running first, then most recent first: the list is about what is happening
    // now, and a finished task is history that stays below it.
    const ordered = rows.slice().sort((a, b) => {
      const runningA = a.status === 'running' ? 0 : 1;
      const runningB = b.status === 'running' ? 0 : 1;
      if (runningA !== runningB) return runningA - runningB;
      return Date.parse(b.startedAt || '') - Date.parse(a.startedAt || '');
    });
    for (const row of ordered) rowsEl.appendChild(drawRow(row));

    // A task whose session closed is no longer in the table; the pane over it
    // would be showing a row that has gone.
    if (selected && !rows.some((row) => row.id === selected.taskId)) closeOutput();
  }

  function openOutput(title) {
    outputTitleEl.textContent = title;
    outputEl.hidden = false;
    outputTextEl.textContent = 'Reading…';
    droppedEl.hidden = true;
  }

  function closeOutput() {
    selected = null;
    outputEl.hidden = true;
    outputTextEl.textContent = '';
    droppedEl.hidden = true;
  }

  function renderOutput(msg) {
    if (msg.error) {
      outputTextEl.textContent = msg.error;
      setStatus('');
      return;
    }
    outputTextEl.textContent = msg.output || '';
    droppedEl.hidden = msg.truncated !== true;
    const exit =
      typeof msg.exitCode === 'number' && msg.exitCode >= 0 ? ' (exit ' + msg.exitCode + ')' : '';
    setStatus(msg.status ? 'Status: ' + msg.status + exit : '');
  }

  refreshEl.addEventListener('click', () => vscode.postMessage({ type: 'refresh' }));

  outputRefreshEl.addEventListener('click', () => {
    if (!selected) return;
    outputTextEl.textContent = 'Reading…';
    vscode.postMessage({
      type: 'output',
      sessionId: selected.sessionId,
      taskId: selected.taskId,
    });
  });

  outputCloseEl.addEventListener('click', closeOutput);

  window.addEventListener('message', (event) => {
    const msg = event.data;
    if (!msg || typeof msg !== 'object') return;
    switch (msg.type) {
      case 'state':
        renderState(msg);
        return;
      case 'output':
        if (!selected || selected.taskId !== msg.taskId) return;
        renderOutput(msg);
        return;
      case 'cancelled':
        if (msg.error) setStatus(msg.error);
        return;
      case 'error':
        setStatus(msg.message);
        return;
    }
  });

  vscode.postMessage({ type: 'ready' });
})();
