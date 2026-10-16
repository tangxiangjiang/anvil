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
  const chipsBar = document.getElementById('chips');
  const attachButton = document.getElementById('attach');

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
  /** Referenced workspace paths, in the order they were picked. */
  let mentions = [];
  /** Pasted images, as data URLs. */
  let images = [];
  /** Whether the session was created with vision on. Images need it. */
  let vision = false;

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

  function addUser(text, sentMentions, sentImages) {
    const wrap = el('div', 'msg user');
    wrap.appendChild(el('div', 'who', 'you'));
    wrap.appendChild(el('div', 'body', text));
    // What travelled with the prompt is echoed under it: a file read on the
    // user's behalf is part of the turn, and a transcript that omits it would
    // misreport what the model was asked.
    if (sentMentions && sentMentions.length) {
      const refs = el('div', 'refs');
      for (const mention of sentMentions) refs.appendChild(el('span', 'ref', '@' + mention));
      wrap.appendChild(refs);
    }
    if (sentImages && sentImages.length) {
      const shots = el('div', 'shots');
      for (const dataUrl of sentImages) {
        const thumb = el('img');
        thumb.src = dataUrl;
        thumb.alt = 'attached image';
        shots.appendChild(thumb);
      }
      wrap.appendChild(shots);
    }
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
      // The host sends both bodies for a single-file change, which is what the
      // editor's own diff view needs. When it dropped them (too large) or never
      // had them (a patch spanning files), the diff text below is all there is,
      // and saying so is better than an empty diff editor.
      const hasBodies =
        preview.truncated !== true &&
        typeof preview.before === 'string' &&
        typeof preview.after === 'string';
      if (hasBodies) {
        const open = el('button', 'review', 'Review in editor');
        open.type = 'button';
        open.title = 'Open this change as a diff before deciding';
        open.addEventListener('click', () => vscode.postMessage({ type: 'review', requestId: request.request_id }));
        box.appendChild(open);
      } else if (preview.truncated === true) {
        box.appendChild(el('div', 'why', 'This change is too large to open in a diff editor. The diff below is the whole change.'));
      }
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

  // --- composer chips -------------------------------------------------------

  /** Redraws the row of chips above the textarea. */
  function renderChips() {
    chipsBar.textContent = '';
    for (const mention of mentions) chipsBar.appendChild(mentionChip(mention));
    for (let index = 0; index < images.length; index++) chipsBar.appendChild(imageChip(index));
    chipsBar.hidden = mentions.length === 0 && images.length === 0;
  }

  function removeChip(title, onRemove) {
    const button = el('button', 'chip-x', '×');
    button.type = 'button';
    button.title = title;
    button.addEventListener('click', onRemove);
    return button;
  }

  function mentionChip(path) {
    const chip = el('span', 'chip mention');
    chip.appendChild(el('span', 'chip-label', path));
    chip.appendChild(
      removeChip(`Stop referencing ${path}`, () => {
        mentions = mentions.filter((candidate) => candidate !== path);
        renderChips();
      }),
    );
    return chip;
  }

  function imageChip(index) {
    const chip = el('span', 'chip image');
    const thumb = el('img');
    thumb.src = images[index];
    thumb.alt = 'pasted image';
    chip.appendChild(thumb);
    chip.appendChild(
      removeChip('Remove this image', () => {
        images.splice(index, 1);
        renderChips();
      }),
    );
    return chip;
  }

  /** Adds picked paths, keeping the order they were picked in. */
  function addMentions(paths) {
    for (const path of paths) {
      if (typeof path === 'string' && path !== '' && mentions.indexOf(path) === -1) mentions.push(path);
    }
    renderChips();
    input.focus();
  }

  /**
   * Reads a pasted image into a data URL. The bytes never touch the disk: the
   * host takes base64 straight from the prompt and hands it to the model, so a
   * paste leaves nothing behind in the workspace.
   */
  function readImage(file) {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result !== 'string') return;
      images.push(reader.result);
      renderChips();
      if (!vision) {
        showBanner('warn', 'Images are off for this session. Turn on "anvil.vision" and reload the host to use them.');
      }
    };
    reader.onerror = () => showBanner('error', 'That image could not be read.');
    reader.readAsDataURL(file);
  }

  function askForMentions() {
    vscode.postMessage({ type: 'pickMention' });
  }

  // --- changed files --------------------------------------------------------

  function kindLabel(kind) {
    if (kind === 'created') return 'added';
    if (kind === 'deleted') return 'deleted';
    return 'changed';
  }

  /**
   * Lists what the turn changed. Each row opens a diff:
   *   - a modification: the pre-image in memory against the file on disk;
   *   - a deletion: against an empty side;
   *   - an addition: against an empty side too, which is what it was.
   */
  function showChanges(files) {
    if (!Array.isArray(files) || files.length === 0) return;
    const card = el('div', 'changes');
    card.appendChild(el('div', 'head', `Changed ${files.length} file${files.length === 1 ? '' : 's'}`));
    for (const file of files) {
      if (!file || typeof file.path !== 'string') continue;
      const row = el('button', 'change');
      row.type = 'button';
      row.title = 'Open a diff of this change';
      row.appendChild(el('span', 'kind ' + (file.kind || 'modified'), kindLabel(file.kind)));
      row.appendChild(el('span', 'path', file.path));
      row.addEventListener('click', () => vscode.postMessage({ type: 'reviewChange', path: file.path }));
      card.appendChild(row);
    }
    append(card);
    currentText = null;
  }

  // --- run state ------------------------------------------------------------

  function setRunning(next) {
    running = next;
    stopButton.hidden = !running;
    sendButton.disabled = running;
    input.disabled = running;
    attachButton.disabled = running;
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
    // The composer is emptied here but the turn keeps what was sent, so the
    // optimistic echo below is the record of it either way.
    const sentMentions = mentions.slice();
    const sentImages = images.slice();
    input.value = '';
    mentions = [];
    images = [];
    renderChips();
    addUser(text, sentMentions, sentImages);
    // Optimistic: the host's `state` notification confirms it a moment later,
    // but the composer must lock immediately or a double Enter sends twice.
    setRunning(true);
    statusLine.hidden = true;
    vscode.postMessage({ type: 'send', text, mentions: sentMentions, images: sentImages });
  });

  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      composer.dispatchEvent(new Event('submit', { cancelable: true }));
    }
  });

  /*
   * "@" opens the reference picker. It is watched on `input` rather than
   * `keydown` because which character a key produces is a keyboard-layout
   * question, and `key` is "Digit2" on the layouts where "@" needs Shift.
   */
  input.addEventListener('input', () => {
    if (!input.value.endsWith('@')) return;
    input.value = input.value.slice(0, -1);
    askForMentions();
  });

  // A paste is only ours when it carries an image; text pastes stay the
  // textarea's, which is why the default is left alone unless one is found.
  input.addEventListener('paste', (event) => {
    const items = (event.clipboardData && event.clipboardData.items) || [];
    const files = [];
    for (const item of items) {
      if (item.kind !== 'file' || !item.type.startsWith('image/')) continue;
      const file = item.getAsFile();
      if (file) files.push(file);
    }
    if (files.length === 0) return;
    event.preventDefault();
    for (const file of files) readImage(file);
  });

  attachButton.addEventListener('click', askForMentions);

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
          attachButton.disabled = true;
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
      case 'mentions':
        addMentions(msg.paths || []);
        return;
      case 'vision':
        vision = msg.enabled === true;
        return;
      case 'changes':
        showChanges(msg.files);
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
  renderChips();
  vscode.postMessage({ type: 'ready' });
})();
