/*
 * The chat view's DOM. Plain JavaScript on purpose: no build step, no
 * dependency, and nothing to keep in sync with the extension host bundle.
 *
 * Everything arriving from the host — assistant text, tool arguments, tool
 * results, diffs — is written with textContent, never innerHTML. Tool output is
 * arbitrary file content and command output; it is data, and it must not be
 * able to add markup or script to this document.
 */
(function () {
  const vscode = acquireVsCodeApi();

  const transcript = document.getElementById('transcript');
  const empty = document.getElementById('empty');
  const composer = document.getElementById('composer');
  const input = document.getElementById('input');
  const sendButton = document.getElementById('send');
  const stopButton = document.getElementById('stop');
  const statusLine = document.getElementById('status');
  const banner = document.getElementById('banner');

  /** Run in flight, if any. */
  let running = false;
  /** True once a prompt has been sent: the empty state is gone for good. */
  let started = false;
  /** The assistant bubble new text is appended to. */
  let currentText = null;
  /** Tool cards by tool_call_id, so a result can find its call. */
  const toolCards = new Map();
  /** Approval cards by request_id. */
  const approvals = new Map();

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function clearEmpty() {
    if (!started) {
      started = true;
      if (empty && empty.parentNode) empty.remove();
    }
  }

  function atBottom() {
    return transcript.scrollHeight - transcript.scrollTop - transcript.clientHeight < 60;
  }

  function scrollIfPinned(wasPinned) {
    if (wasPinned) transcript.scrollTop = transcript.scrollHeight;
  }

  function append(node) {
    const pinned = atBottom();
    clearEmpty();
    transcript.appendChild(node);
    scrollIfPinned(pinned);
  }

  // --- messages -------------------------------------------------------------

  function addUser(text) {
    const wrap = el('div', 'msg user');
    wrap.appendChild(el('div', 'who', 'you'));
    wrap.appendChild(el('div', 'body', text));
    append(wrap);
    currentText = null;
  }

  function addAssistantChunk(text) {
    if (!currentText) {
      const wrap = el('div', 'msg assistant');
      wrap.appendChild(el('div', 'who', 'anvil'));
      currentText = el('div', 'body');
      wrap.appendChild(currentText);
      append(wrap);
    }
    const pinned = atBottom();
    currentText.textContent += text;
    scrollIfPinned(pinned);
  }

  function addNotice(text) {
    const wrap = el('div', 'msg error');
    wrap.appendChild(el('div', 'body', text));
    append(wrap);
    currentText = null;
  }

  // --- tool cards -----------------------------------------------------------

  /** A short one-line hint about what the call touches. */
  function callHint(name, args) {
    if (!args || typeof args !== 'object') return '';
    const order = ['path', 'file_path', 'command', 'pattern', 'query', 'url', 'name'];
    for (const key of order) {
      const value = args[key];
      if (typeof value === 'string' && value !== '') return value;
      if (Array.isArray(value)) return value.join(' ');
    }
    try {
      return JSON.stringify(args);
    } catch {
      // A cyclic or otherwise unserialisable argument list is not worth a
      // broken card: the tool name alone is still useful.
      return '';
    }
  }

  function addToolCall(event) {
    currentText = null;
    const card = el('details', 'tool');
    const summary = el('summary');
    summary.appendChild(el('span', 'name', event.name || 'tool'));
    summary.appendChild(el('span', 'hint', callHint(event.name, event.args)));
    summary.appendChild(el('span', 'state', 'running…'));
    card.appendChild(summary);

    const pane = el('div', 'pane');
    if (event.args !== undefined) {
      const pre = el('pre');
      pre.textContent = safeJson(event.args);
      pane.appendChild(pre);
    }
    card.appendChild(pane);

    if (event.tool_call_id) toolCards.set(event.tool_call_id, card);
    append(card);
  }

  function addToolResult(event) {
    const card = event.tool_call_id ? toolCards.get(event.tool_call_id) : undefined;
    if (!card) {
      // A result without a card (a reloaded webview, a dropped call): show it
      // rather than dropping what the model just saw.
      addNotice(`tool result: ${event.content || ''}`);
      return;
    }
    const state = card.querySelector('.state');
    const failed = event.is_error === true;
    card.classList.add(failed ? 'failed' : 'done');
    state.textContent = failed ? 'failed' : 'done';

    const content = event.content || '';
    const pane = card.querySelector('.pane');
    const pre = el('pre');
    const limit = 4000;
    const truncated = content.length > limit;
    pre.textContent = truncated ? content.slice(0, limit) : content;
    pane.appendChild(pre);
    if (truncated) {
      const more = el('button', 'more', `show all (${content.length} chars)`);
      more.type = 'button';
      more.addEventListener('click', () => {
        pre.textContent = content;
        more.remove();
      });
      pane.appendChild(more);
    }
  }

  function safeJson(value) {
    try {
      return typeof value === 'string' ? value : JSON.stringify(value, null, 2);
    } catch {
      return String(value);
    }
  }

  // --- approvals ------------------------------------------------------------

  function describePreview(request) {
    const preview = request.preview || {};
    const box = el('div', 'preview');

    if (preview.kind === 'command' && Array.isArray(preview.command)) {
      const row = el('div', 'argv');
      row.appendChild(el('span', 'prompt', '$'));
      row.appendChild(el('code', null, preview.command.join(' ')));
      box.appendChild(row);
      return box;
    }

    if (preview.kind === 'diff' && typeof preview.diff === 'string') {
      if (preview.path) box.appendChild(el('div', 'path', preview.path));
      const pre = el('pre', 'diff');
      for (const line of preview.diff.split('\n')) {
        let cls = null;
        if (line.startsWith('+++') || line.startsWith('---')) cls = 'hunk';
        else if (line.startsWith('@@')) cls = 'hunk';
        else if (line.startsWith('+')) cls = 'add';
        else if (line.startsWith('-')) cls = 'del';
        pre.appendChild(el('div', cls, line));
      }
      box.appendChild(pre);
      return box;
    }

    if (preview.path) box.appendChild(el('div', 'path', preview.path));
    if (preview.summary) box.appendChild(el('div', 'summary', preview.summary));
    if (!preview.path && !preview.summary) {
      // An absent preview is not a claim that the call is harmless, so it is
      // shown as an explicit unknown rather than as nothing at all.
      box.appendChild(el('div', 'why', 'The host could not preview this call. Review the arguments below.'));
      const pre = el('pre');
      pre.textContent = safeJson(request.args);
      box.appendChild(pre);
    }
    return box;
  }

  function showApproval(request) {
    currentText = null;
    const card = el('div', 'approval');

    const head = el('div', 'head');
    head.appendChild(el('span', null, 'Allow '));
    head.appendChild(el('span', 'tool-name', request.tool));
    head.appendChild(el('span', null, '?'));
    card.appendChild(head);
    card.appendChild(describePreview(request));

    const buttons = el('div', 'buttons');
    const scope = request.preview && request.preview.path ? request.preview.path : undefined;
    const choices = [
      { decision: 'allow', label: 'Allow once', cls: 'primary' },
      {
        decision: 'allow_session',
        label: scope ? `Always allow for ${scope}` : `Always allow ${request.tool}`,
        cls: 'secondary',
      },
      { decision: 'deny', label: 'Deny', cls: 'secondary' },
    ];
    for (const choice of choices) {
      const button = el('button', choice.cls, choice.label);
      button.type = 'button';
      button.addEventListener('click', () => {
        resolveApproval(request.request_id, choice.decision, scope);
      });
      buttons.appendChild(button);
    }
    card.appendChild(buttons);

    if (request.timeout_ms) {
      card.appendChild(el('div', 'why', `Denied automatically in ${Math.round(request.timeout_ms / 1000)}s.`));
    }

    approvals.set(request.request_id, card);
    transcript.appendChild(card);
    transcript.scrollTop = transcript.scrollHeight;
  }

  function resolveApproval(requestId, decision, scope) {
    const card = approvals.get(requestId);
    if (!card) return;
    approvals.delete(requestId);
    card.classList.add('resolved');
    for (const button of card.querySelectorAll('button')) button.disabled = true;
    const verdict = el('div', 'why', decision === 'deny' ? 'Denied.' : 'Allowed.');
    card.appendChild(verdict);
    vscode.postMessage({ type: 'answer', requestId, decision, scope });
  }

  function withdrawApproval(requestId) {
    const card = approvals.get(requestId);
    if (!card) return;
    approvals.delete(requestId);
    card.classList.add('resolved');
    for (const button of card.querySelectorAll('button')) button.disabled = true;
    card.appendChild(el('div', 'why', 'This request is no longer pending.'));
  }

  // --- run state ------------------------------------------------------------

  function setRunning(next) {
    running = next;
    stopButton.hidden = !running;
    sendButton.disabled = running;
    input.disabled = running;
    if (!running) input.focus();
  }

  function finishRun(done, failure) {
    currentText = null;
    setRunning(false);
    const parts = [];
    const steps = done && typeof done.steps === 'number' ? `${done.steps} step${done.steps === 1 ? '' : 's'}` : '';
    if (steps) parts.push(steps);
    if (done && done.usage && done.usage.total_tokens) parts.push(`${done.usage.total_tokens} tokens`);
    statusLine.textContent = parts.join(' · ');
    statusLine.hidden = parts.length === 0;
    // "Stopped." is not a failure, but it is still something the user asked
    // for and should see once: a run that ends silently reads as a hang.
    if (failure && failure.message) addNotice(failure.message);
  }

  function showBanner(level, message) {
    banner.textContent = '';
    banner.dataset.level = level;
    banner.appendChild(el('div', 'text', message));
    const dismiss = el('button', 'dismiss', '✕');
    dismiss.type = 'button';
    dismiss.title = 'Dismiss';
    dismiss.addEventListener('click', () => {
      banner.hidden = true;
    });
    banner.appendChild(dismiss);
    banner.hidden = false;
  }

  // --- wiring ---------------------------------------------------------------

  composer.addEventListener('submit', (event) => {
    event.preventDefault();
    const text = input.value.trim();
    if (text === '' || running) return;
    input.value = '';
    addUser(text);
    // Optimistic: the host's `state` notification confirms it a moment later,
    // but the composer must lock immediately or a double Enter sends twice.
    setRunning(true);
    statusLine.hidden = true;
    vscode.postMessage({ type: 'send', text });
  });

  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      composer.dispatchEvent(new Event('submit', { cancelable: true }));
    }
  });

  stopButton.addEventListener('click', () => {
    stopButton.disabled = true;
    vscode.postMessage({ type: 'cancel' });
  });

  window.addEventListener('message', (event) => {
    const msg = event.data;
    switch (msg.type) {
      case 'trust':
        if (!msg.trusted) {
          showBanner('warn', 'This workspace is not trusted, so Anvil will not start. Trust the folder and reload the window.');
          input.disabled = true;
          sendButton.disabled = true;
        }
        return;
      case 'state':
        stopButton.disabled = false;
        setRunning(msg.state === 'running');
        return;
      case 'event': {
        const e = msg.event;
        if (e.type === 'assistant') addAssistantChunk(e.text || '');
        else if (e.type === 'tool_call') addToolCall(e);
        else if (e.type === 'tool_result') addToolResult(e);
        else if (e.type === 'error') addNotice(e.text || 'The run reported an error.');
        else if (e.type === 'progress' && e.text) {
          statusLine.textContent = e.text;
          statusLine.hidden = false;
        }
        return;
      }
      case 'approval':
        showApproval(msg.request);
        return;
      case 'approvalWithdrawn':
        withdrawApproval(msg.requestId);
        return;
      case 'done':
        finishRun(msg.done, msg.failure);
        return;
      case 'notice':
        showBanner(msg.level, msg.message);
        if (msg.level === 'error') setRunning(false);
        return;
      default:
        return;
    }
  });

  setRunning(false);
  vscode.postMessage({ type: 'ready' });
})();
