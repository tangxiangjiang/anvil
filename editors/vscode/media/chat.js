/*
 * The chat view's DOM. Plain JavaScript on purpose: no build step, no
 * dependency, and nothing to keep in sync with the extension host bundle.
 *
 * Everything arriving from the host — assistant text, tool arguments, tool
 * results, diffs — is written with textContent, never innerHTML. Tool output is
 * arbitrary file content and command output; it is data, and it must not be
 * able to add markup or script to this document.
 *
 * That rule is why the Markdown renderer builds nodes by hand rather than
 * setting `innerHTML` to generated HTML: a renderer over *model* output is an
 * injection surface even when everything it interpolates is escaped, because a
 * URL is not text. `media/markdown.js` parses into plain objects; this file is
 * the only thing that turns them into nodes, and it refuses any link scheme it
 * will not make clickable.
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
  const modeSelect = document.getElementById('mode');
  const bypassStrip = document.getElementById('bypass');
  const dontAskStrip = document.getElementById('dontAsk');
  const rulesButton = document.getElementById('rules');
  const permissionsButton = document.getElementById('permissions');
  const palette = document.getElementById('palette');
  const dock = document.getElementById('composerDock');

  /** Run in flight, if any. */
  let running = false;
  /** True once a prompt has been sent: the empty state is gone for good. */
  let started = false;
  /**
   * The assistant round being streamed, or null. One reference for its Thinking
   * block and its answer, because they are one round: a user message, a tool
   * call, an approval, a notice, a session marker, the end of the run and a
   * transcript reset all end it. Reasoning that outlived its round would append
   * to a block sitting *above* the tool cards, so the transcript would show
   * thinking before the calls it actually followed.
   */
  let round = null;
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
  /** The slash catalogue, once the extension has sent it. */
  let catalogue = null;
  /** Rule files the last session reported, kept so the card can be reopened. */
  let rules = [];
  /** The palette rows currently shown, and which one is highlighted. */
  let paletteRows = [];
  let paletteIndex = 0;

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

  /**
   * Empties the transcript for a different conversation.
   *
   * The tab is a singleton pointed at one agent at a time, so adopting another
   * session has to take the previous one off the screen. The maps matter more
   * than the nodes: a tool result or an approval answer arriving after the
   * switch would otherwise be matched by id against a card from a conversation
   * the user is no longer looking at.
   */
  function clearTranscript() {
    endRound();
    toolCards.clear();
    approvals.clear();
    mentions = [];
    images = [];
    renderChips();
    closePalette();
    statusLine.textContent = '';
    statusLine.hidden = true;
    transcript.textContent = '';
    // The node survives the clear — only the reference to it in the document
    // does not — so putting it back is what makes a cleared conversation look
    // like a new one rather than like an empty one.
    started = false;
    if (empty) transcript.appendChild(empty);
    growInput();
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
    endRound();
  }

  /**
   * Ends the assistant round, so the next reasoning or text starts a new bubble
   * rather than appending to one the reader has already read past.
   */
  function endRound() {
    if (round && round.think && !round.thinkPinned) round.think.open = false;
    round = null;
  }

  /**
   * The bubble for the current round, created by whichever of reasoning or text
   * arrives first — so the round's label precedes its Thinking block instead of
   * floating below it.
   *
   * `text` accumulates every chunk, and `painted` is how much of it is on
   * screen: the reconciler below is fed the whole string and works out the rest,
   * because re-parsing only the new tail is exactly what it must not do.
   */
  function ensureRound() {
    if (round) return round;
    const wrap = el('div', 'msg assistant');
    wrap.appendChild(el('div', 'who', 'anvil'));
    const body = el('div', 'body md');
    wrap.appendChild(body);
    round = {
      wrap,
      body,
      text: '',
      paintQueued: false,
      think: null,
      thinkBody: null,
      thinkSealed: false,
      thinkPinned: false,
      md: { node: body, settled: [], offset: 0, rendered: 0 },
    };
    append(wrap);
    return round;
  }

  function addAssistantChunk(text) {
    const state = ensureRound();
    // The answer has started, so the thinking is done: collapse it unless the
    // reader opened it themselves, which is not something to undo for them.
    if (state.think) {
      state.thinkSealed = true;
      if (!state.thinkPinned) state.think.open = false;
    }
    state.text += text;
    queuePaint(state);
  }

  /**
   * The model's thinking, in its own collapsed block above the answer.
   *
   * Plain text in a `pre-wrap` box rather than Markdown: reasoning is not a
   * document, and typesetting it would invite the reader to treat it as one.
   */
  function addReasoning(text) {
    const state = ensureRound();
    // Reasoning that arrives after text in the same round opens a second block
    // below the answer. That is rare, and showing it there is strictly better
    // than dropping it, which is the only other option.
    if (!state.think || state.thinkSealed) {
      const think = el('details', 'think');
      think.open = false;
      think.appendChild(el('summary', null, 'Thinking'));
      think.addEventListener('toggle', () => {
        if (think.open) state.thinkPinned = true;
      });
      const body = el('div', 'body');
      think.appendChild(body);
      state.wrap.insertBefore(think, state.body);
      state.think = think;
      state.thinkBody = body;
      state.thinkSealed = false;
    }
    const pinned = atBottom();
    state.thinkBody.textContent += text;
    // Collapsed, growing the block moves nothing, so the pin only matters once
    // the reader has opened it — which is when they want to follow the bottom.
    scrollIfPinned(pinned);
  }

  // --- markdown -------------------------------------------------------------

  /**
   * The parser, read once, lazily.
   *
   * Lazy so that the order of the two `script` tags in the document is not
   * load-bearing: `markdown.js` assigns a global and this is the only place that
   * reads it, so a webview that somehow got them the other way round still
   * renders its first message.
   */
  let markdown = null;

  function parseMarkdown(text) {
    if (!markdown) markdown = self.anvilMarkdown;
    return markdown.parse(text);
  }

  /** Whether two block objects would be drawn by the same kind of node. */
  function sameBlockShape(a, b) {
    if (!a || a.type !== b.type) return false;
    if (a.type === 'heading') return a.level === b.level;
    if (a.type === 'list') return a.ordered === b.ordered;
    if (a.type === 'code') return (a.lang || '') === (b.lang || '');
    return true;
  }

  /**
   * Draws `blocks` into `container`, keeping the nodes before `from` and reusing
   * any node whose shape still matches. Returns the number of children, which is
   * where the next frame should start re-rendering.
   */
  function renderBlocks(container, blocks, from) {
    let i = from;
    for (; i < blocks.length; i++) {
      const existing = container.childNodes[i];
      if (!existing) {
        container.appendChild(blockNode(blocks[i]));
      } else if (sameBlockShape(existing.anvilBlock, blocks[i])) {
        updateBlock(existing, blocks[i]);
      } else {
        container.replaceChild(blockNode(blocks[i]), existing);
      }
    }
    while (container.childNodes.length > blocks.length) container.removeChild(container.lastChild);
    return blocks.length;
  }

  function blockNode(block) {
    const node = buildBlock(block);
    node.anvilBlock = block;
    return node;
  }

  function buildBlock(block) {
    switch (block.type) {
      case 'heading': {
        const node = el('h' + block.level, 'md-h');
        fillInline(node, block.spans);
        return node;
      }
      case 'code': {
        const node = el('pre', 'md-code');
        const code = el('code');
        if (block.lang) code.dataset.lang = block.lang;
        setText(code, block.text || '');
        node.appendChild(code);
        return node;
      }
      case 'blockquote': {
        const node = el('blockquote', 'md-quote');
        renderBlocks(node, block.blocks || [], 0);
        return node;
      }
      case 'hr':
        return el('hr', 'md-rule');
      case 'list': {
        const node = el(block.ordered ? 'ol' : 'ul', 'md-list');
        if (block.ordered && block.from && block.from !== 1) node.start = block.from;
        renderBlocks(node, (block.items || []).map(itemNode), 0);
        return node;
      }
      default: {
        const node = el('p', 'md-p');
        fillInline(node, block.spans);
        return node;
      }
    }
  }

  /**
   * A list item as a block, so the same reconciler can draw items as it draws
   * top-level blocks. An item that is one paragraph is drawn as its spans;
   * anything else — a nested list, a second paragraph — keeps its blocks.
   */
  function itemNode(item) {
    const node = el('li');
    node.anvilItem = item;
    if (item.spans) fillInline(node, item.spans);
    else renderBlocks(node, item.blocks || [], 0);
    return node;
  }

  /** A block whose shape did not change, redrawn in place. */
  function updateBlock(node, block) {
    switch (block.type) {
      case 'heading':
      case 'paragraph':
        if (block.type === 'heading') node.anvilBlock = block;
        updateInline(node, block.spans || []);
        break;
      case 'code':
        // The one place a growing leaf is large enough for how it is written to
        // matter: a 50 KB code block is the open tail of most long answers, and
        // setting `textContent` on every frame re-lays out all of it.
        setText(node.firstChild, block.text || '');
        break;
      case 'blockquote':
        renderBlocks(node, block.blocks || [], 0);
        break;
      case 'list': {
        const items = block.items || [];
        // Items are matched by index, so a streaming list grows an item rather
        // than rebuilding the list on every frame.
        for (let i = 0; i < items.length; i++) {
          const li = node.childNodes[i];
          if (li && li.anvilItem && sameItemShape(li.anvilItem, items[i])) {
            updateItem(li, items[i]);
          } else if (li) {
            node.replaceChild(itemNode(items[i]), li);
          } else {
            node.appendChild(itemNode(items[i]));
          }
        }
        while (node.childNodes.length > items.length) node.removeChild(node.lastChild);
        break;
      }
      default:
        break;
    }
    node.anvilBlock = block;
  }

  function sameItemShape(a, b) {
    return (a.spans === null) === (b.spans === null);
  }

  function updateItem(li, item) {
    if (item.spans) updateInline(li, item.spans);
    else renderBlocks(li, item.blocks || [], 0);
    li.anvilItem = item;
  }

  /**
   * Draws inline spans into `container`, keeping the nodes before the first one
   * that changed.
   *
   * Streaming appends to the end of the last span nearly every frame, so the
   * reusable prefix is almost always everything but the final node — and keeping
   * it is what stops a growing paragraph from being rebuilt, and the reader's
   * text selection from being thrown away, on every frame.
   */
  function fillInline(container, spans) {
    for (const span of spans || []) container.appendChild(inlineNode(span));
  }

  function updateInline(container, spans) {
    const next = spans || [];
    let keep = 0;
    while (keep < next.length && keep < container.childNodes.length && sameSpan(container.childNodes[keep], next[keep])) {
      keep++;
    }
    while (container.childNodes.length > keep) container.removeChild(container.lastChild);
    for (let i = keep; i < next.length; i++) container.appendChild(inlineNode(next[i]));
  }

  /** Whether an existing node already renders this span exactly. */
  function sameSpan(node, span) {
    const shape = node.anvilSpan;
    if (!shape || shape.type !== span.type) return false;
    if (span.type === 'link') return shape.href === span.href;
    if (span.type === 'text' || span.type === 'code') return shape.value === span.value;
    return false;
  }

  function inlineNode(span) {
    let node;
    switch (span.type) {
      case 'code':
        node = el('code', 'md-inline-code', span.value);
        break;
      case 'strong':
        node = el('strong');
        fillInline(node, span.spans);
        break;
      case 'em':
        node = el('em');
        fillInline(node, span.spans);
        break;
      case 'del':
        node = el('del');
        fillInline(node, span.spans);
        break;
      case 'link':
        node = linkNode(span);
        break;
      default:
        node = document.createTextNode(span.value);
        break;
    }
    node.anvilSpan = span;
    return node;
  }

  /**
   * A link, but only if its scheme is one this will open.
   *
   * It is a `span` wearing a link's role, not an `<a href>`: a real anchor can
   * be middle-clicked or activated from the context menu, which navigates the
   * webview away and takes the conversation with it. Nothing here puts a URL in
   * the document, so there is nothing for the browser to follow even by
   * accident. A target this will not open stays the literal text it was written
   * as, which is honest about what happened to it.
   *
   * The extension checks the scheme again before handing it to
   * `vscode.env.openExternal`. This check is presentation; that one is the
   * boundary, because a webview is not where a decision about what to open
   * belongs.
   */
  function linkNode(span) {
    const href = safeHref(span.href);
    if (!href) return el('span', 'md-literal', '[' + spansText(span.spans) + '](' + span.href + ')');

    const node = el('span', 'md-link');
    node.setAttribute('role', 'link');
    node.tabIndex = 0;
    node.title = href;
    fillInline(node, span.spans);
    const open = () => vscode.postMessage({ type: 'openLink', url: href });
    node.addEventListener('click', open);
    node.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        open();
      }
    });
    return node;
  }

  /** The URL if it is one to open, otherwise '' — http and https, nothing else. */
  function safeHref(href) {
    if (typeof href !== 'string') return '';
    const trimmed = href.trim();
    return /^https?:\/\/\S+$/i.test(trimmed) ? trimmed : '';
  }

  function spansText(spans) {
    return (spans || []).map((span) => (span.value !== undefined ? span.value : spansText(span.spans))).join('');
  }

  /**
   * Sets a node's text to exactly `text`, appending when the new text merely
   * extends the old one.
   *
   * `textContent = text` is the obvious version and it is O(n) in the whole
   * string every frame; a code block that is still streaming is the largest
   * thing on screen, so extending its text node is what keeps a long answer from
   * being quadratic in its own length.
   */
  function setText(node, text) {
    if (!node) return;
    const only = node.childNodes.length === 1 ? node.firstChild : null;
    if (only && only.nodeType === 3) {
      if (only.data === text) return;
      if (text.startsWith(only.data)) {
        only.appendData(text.slice(only.data.length));
        return;
      }
      only.data = text;
      return;
    }
    node.textContent = text;
  }

  /**
   * Coalesces the chunks that land in one animation frame into a single paint.
   *
   * It is not only about doing less work: the reconciler re-parses from the open
   * block, and a fenced code block *is* the open block, so without this a
   * fast stream would re-parse the whole block once per token. The closure holds
   * the round's own state rather than reading `round`, so a round that ends
   * between the chunk and the frame still paints everything it accumulated.
   */
  function queuePaint(state) {
    if (state.paintQueued) return;
    state.paintQueued = true;
    requestAnimationFrame(() => {
      state.paintQueued = false;
      const pinned = atBottom();
      paintMarkdown(state.md, state.text);
      scrollIfPinned(pinned);
    });
  }

  /**
   * Renders the accumulated answer, re-parsing only the block it is still
   * growing into.
   *
   * `md.offset` is where that block starts and `md.settled` is every block
   * before it, which are final: text is only ever appended after a block, so
   * nothing before the last one can change again. Sealing on a blank line
   * instead — the obvious shortcut — is wrong in four ways at once: an indented
   * code block continues across a blank line, a blank line inside an open fence
   * would split the code in two, `- a` / blank / `- b` is a single loose list,
   * and a list item's second paragraph has a blank line *inside* the item.
   * `markdown.test.ts` walks whole documents one character at a time to keep
   * this rule honest, because the failure it prevents is a duplicated or lost
   * line rather than a crash.
   */
  function paintMarkdown(state, text) {
    const tail = parseMarkdown(text.slice(state.offset)).blocks;
    const blocks = state.settled.concat(tail);
    const last = tail[tail.length - 1];
    state.offset = last ? state.offset + last.start : text.length;
    state.settled = blocks.slice(0, -1);
    renderBlocks(state.node, blocks, state.rendered);
    // Everything from the tail onwards may change again next frame, so the next
    // paint starts there.
    state.rendered = state.settled.length;
  }

  function addNotice(text) {
    const wrap = el('div', 'msg error');
    wrap.appendChild(el('div', 'body', text));
    append(wrap);
    endRound();
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

  /**
   * One labelled half of a tool card. The label is most of what makes the card
   * readable: two unlabelled blocks leave the reader to work out which one the
   * model asked for and which one it got back, and for a call whose arguments
   * look like its output that is a guess.
   */
  function section(name, label, text) {
    const wrap = el('div', 'section ' + name);
    wrap.appendChild(el('div', 'label', label));
    const pre = el('pre');
    pre.textContent = text;
    wrap.appendChild(pre);
    return wrap;
  }

  function addToolCall(event) {
    endRound();
    const card = el('details', 'tool');
    const summary = el('summary');
    summary.appendChild(el('span', 'name', event.name || 'tool'));
    summary.appendChild(el('span', 'hint', callHint(event.name, event.args)));
    summary.appendChild(el('span', 'state', 'running…'));
    card.appendChild(summary);

    const pane = el('div', 'pane');
    // Omitted rather than shown empty: `undefined` would otherwise be rendered
    // as the string "undefined" and read as arguments the model never sent.
    if (event.args !== undefined) {
      pane.appendChild(section('in', 'IN', safeJson(event.args)));
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

    // Why this call ran without stopping, when a rule is the reason — or why it
    // did not run at all, which is the same field on a denial. The host sets it
    // only for a call a rule really decided, so its absence means no rule did,
    // not that some rule allowed it. It sits under the summary, which is the
    // half of the card that stays visible while it is collapsed.
    if (event.rule) {
      const line = el('div', 'rule-ref');
      line.appendChild(el('span', null, failed ? 'refused by ' : 'allowed by '));
      line.appendChild(el('code', null, ruleText(event.rule)));
      card.insertBefore(line, card.querySelector('.pane'));
    }

    const content = event.content || '';
    const limit = 4000;
    const truncated = content.length > limit;
    // The result goes in OUT, and the "show all" button has to name *that* pane:
    // the card has two now, so a button that reached for the last `pre` in the
    // card would expand the arguments instead of the output.
    const out = section('out', 'OUT', truncated ? content.slice(0, limit) : content);
    const pre = out.querySelector('pre');
    card.querySelector('.pane').appendChild(out);
    if (truncated) {
      const more = el('button', 'more', `show all (${content.length} chars)`);
      more.type = 'button';
      more.addEventListener('click', () => {
        pre.textContent = content;
        more.remove();
      });
      out.appendChild(more);
    }
  }

  function safeJson(value) {
    try {
      return typeof value === 'string' ? value : JSON.stringify(value, null, 2);
    } catch {
      return String(value);
    }
  }

  // --- rules ----------------------------------------------------------------

  /**
   * Names a deciding rule the way the SDK does.
   *
   * The host sends its own sentence with the rule (`describe`), which is the
   * wording a denial uses in the model's tool error — so the card and the error
   * cannot disagree about which rule did it. The fallback is for a rule that
   * arrived without one, which is a shape, not a decision: it says what the
   * rule is made of rather than inventing a sentence.
   */
  function ruleText(rule) {
    if (!rule) return '';
    return rule.describe || ruleSentence(rule);
  }

  /** The rule as the file spells it, for a label that has no sentence. */
  function ruleSentence(rule) {
    if (!rule) return '';
    const pattern = rule.pattern ? ' "' + rule.pattern + '"' : '';
    return (rule.effect || 'allow') + ' ' + (rule.tool || 'tool') + pattern;
  }

  /**
   * What each "always allow" choice is a claim about, by the kind the host
   * labelled it with. The labels live here and the pattern does not: what a
   * button writes is shown beside the label as the file spells it, so the user
   * reads the rule they are about to add instead of a paraphrase of it.
   */
  const REMEMBER_LABELS = {
    exact: 'Always allow this command',
    prefix: 'Always allow this prefix',
    readonly: 'Always allow read-only commands',
    path: 'Always allow this file',
    tool: 'Always allow this tool',
  };

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
    endRound();
    const card = el('div', 'approval');

    const head = el('div', 'head');
    head.appendChild(el('span', null, 'Allow '));
    head.appendChild(el('span', 'tool-name', request.tool));
    head.appendChild(el('span', null, '?'));
    card.appendChild(head);
    // A rule that insists on asking is part of why the card is here at all, and
    // "why am I being asked about a command I allowed" has exactly this answer.
    if (request.rule) {
      const asked = el('div', 'why rule-ref');
      asked.appendChild(el('span', null, 'Asked for by '));
      asked.appendChild(el('code', null, ruleText(request.rule)));
      card.appendChild(asked);
    }
    card.appendChild(describePreview(request));

    const buttons = el('div', 'buttons');
    const scope = request.preview && request.preview.path ? request.preview.path : undefined;
    buttons.appendChild(
      decisionButton(request, 'allow', 'Allow once', 'primary', scope),
    );
    for (const button of rememberButtons(request)) buttons.appendChild(button);
    buttons.appendChild(decisionButton(request, 'deny', 'Deny', 'secondary', scope));
    card.appendChild(buttons);

    if (request.timeout_ms) {
      card.appendChild(el('div', 'why', `Denied automatically in ${Math.round(request.timeout_ms / 1000)}s.`));
    }

    approvals.set(request.request_id, card);
    transcript.appendChild(card);
    transcript.scrollTop = transcript.scrollHeight;
  }

  function decisionButton(request, decision, label, cls, scope) {
    const button = el('button', cls, label);
    button.type = 'button';
    button.addEventListener('click', () => {
      resolveApproval(request.request_id, decision, scope);
    });
    return button;
  }

  /**
   * The "always allow" choices, one per rule the host proved would decide this
   * very call. They are what the card remembers with now: each writes a rule
   * into .anvil/permissions.json, which outlives the session and the process —
   * unlike the session's own allowlist, which is memory and nothing else.
   *
   * A call no rule can describe — a compound shell line, say — is offered none
   * of them, and then the session-scoped answer stands in, because it is the
   * only memory such a call has. It is deliberately the fallback rather than a
   * peer: "allow every shell call for this session" is the all-or-nothing
   * choice the rule file exists to replace.
   */
  function rememberButtons(request) {
    const options = Array.isArray(request.remember) ? request.remember : [];
    if (options.length === 0) {
      const scope = request.preview && request.preview.path ? request.preview.path : undefined;
      const label = scope ? `Always allow for ${scope}` : `Always allow ${request.tool} this session`;
      const button = decisionButton(request, 'allow_session', label, 'secondary', scope);
      button.title = 'For this session only: nothing is written to .anvil/permissions.json.';
      return [button];
    }
    return options.map((option) => {
      const button = el('button', 'secondary remember');
      button.type = 'button';
      button.appendChild(el('span', 'label', REMEMBER_LABELS[option.kind] || 'Always allow this'));
      // The rule as the file spells it, so what is remembered is read rather
      // than inferred from the label — and "readonly" is exactly what lands in
      // the file for that choice.
      if (option.rule && option.rule.pattern) {
        button.appendChild(el('code', 'pattern', option.rule.pattern));
      }
      button.title = 'Writes to .anvil/permissions.json: ' + ruleSentence(option.rule);
      button.addEventListener('click', () => {
        askToRemember(request, button, option);
      });
      return button;
    });
  }

  /**
   * Puts a "remember this" choice to the host, which appends the rule and — when
   * the file now decides the call — answers the card itself.
   *
   * The buttons wait rather than claiming a verdict: whether the call runs is
   * the file's decision, not the button's, and a card that said "allowed" for a
   * rule an earlier deny outranks would be lying about the thing the user was
   * just shown.
   */
  function askToRemember(request, button, option) {
    const card = approvals.get(request.request_id);
    if (!card) return;
    card.classList.add('pending');
    for (const other of card.querySelectorAll('button')) other.disabled = true;
    button.disabled = true;
    vscode.postMessage({
      type: 'remember',
      requestId: request.request_id,
      rule: option.rule,
    });
  }

  /**
   * What became of a "remember this" choice. Three endings, and the card shows
   * which one it got rather than one message for all of them:
   *
   *   approved          the file decides this call now, and the host answered
   *                     it — the card is done, and nothing is left to send
   *   written, unasked  the rule is in the file but does not decide this call
   *                     (an earlier deny or ask outranks it), so the card is
   *                     still open and the user is told what stood in the way
   *   failed            nothing was written, and the error says why
   */
  function resolveRemember(msg) {
    const card = approvals.get(msg.requestId);
    if (!card) return;
    card.classList.remove('pending');
    if (msg.error) {
      for (const button of card.querySelectorAll('button')) button.disabled = false;
      card.appendChild(el('div', 'why', 'Nothing was written: ' + msg.error));
      return;
    }
    if (msg.approved === true) {
      // The host answered the approval itself. A second answer from here would
      // name a request that is no longer pending.
      approvals.delete(msg.requestId);
      card.classList.add('resolved');
      for (const button of card.querySelectorAll('button')) button.disabled = true;
      const verdict = el('div', 'why', 'Allowed, and remembered: ');
      verdict.appendChild(el('code', null, ruleText(msg.rule)));
      card.appendChild(verdict);
      return;
    }
    // Written, but not deciding. Another rule cannot help — precedence is by
    // effect and then by file order, so no later rule outranks what did — and
    // the choices are taken away rather than left to add a second copy.
    for (const button of card.querySelectorAll('button.remember')) button.remove();
    for (const button of card.querySelectorAll('button')) button.disabled = false;
    const why = el('div', 'why');
    if (msg.decidedBy) {
      why.appendChild(el('span', null, 'The rule was written, but this call is still decided by '));
      why.appendChild(el('code', null, ruleText(msg.decidedBy)));
      why.appendChild(el('span', null, '. Answer the card to let it run.'));
    } else {
      why.appendChild(el('span', null, 'The rule was written, but it does not decide this call. Answer the card to let it run.'));
    }
    card.appendChild(why);
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
    endRound();
  }

  // --- mode -----------------------------------------------------------------

  /**
   * Reflects the permission mode the extension is actually using. The select is
   * driven from there, never from what was clicked: the extension can refuse a
   * change (a turn is running, the host said no), and a select that kept the
   * refused value would be lying about what the next tool call will do.
   *
   * Two values travel, and they are not the same one. `mode` is the choice this
   * workspace has stored — empty when it has none, which the select shows as
   * "Workspace default" — and `resolved` is what the session runs under, which
   * the workspace's permissions.json may have supplied. The select shows the
   * first, because that is what clicking it would change; the label says the
   * second, because that is what the next tool call will actually do.
   */
  function showMode(msg) {
    if (!modeSelect) return;
    const chosen = msg.mode || '';
    const resolved = msg.resolved || chosen || 'ask';
    modeSelect.value = chosen;

    const fallback = modeSelect.querySelector('option[value=""]');
    if (fallback) {
      const label = modeLabels[resolved] || resolved;
      fallback.textContent = chosen === '' ? 'Workspace default (' + label + ')' : 'Workspace default';
    }

    const bypass = resolved === 'bypass';
    const dontAsk = resolved === 'dont_ask';
    if (bypassStrip) bypassStrip.hidden = !bypass;
    if (dontAskStrip) dontAskStrip.hidden = !dontAsk;
    document.body.classList.toggle('bypass-mode', bypass);
    document.body.classList.toggle('dont-ask-mode', dontAsk);
  }

  /**
   * How the modes are named here. It repeats src/chatPanel.ts's MODE_LABELS,
   * which is unavoidable — media/ is never imported by the extension side — and
   * test/unit/permissionModes.test.ts is what keeps the two lists equal.
   */
  const modeLabels = {
    ask: 'Ask',
    accept_edits: 'Accept edits',
    plan: 'Plan',
    dont_ask: 'No asking',
    bypass: 'Bypass',
  };

  // --- slash palette --------------------------------------------------------

  /**
   * The query the palette is open for: text starting with `/` and nothing else.
   * Once there is whitespace the command is settled and what follows is its
   * argument, so the palette gets out of the way.
   */
  function paletteQuery() {
    const trimmed = input.value.trim();
    if (trimmed.charAt(0) !== '/') return null;
    const body = trimmed.slice(1);
    if (body === '' || /\s/.test(body)) return null;
    return body;
  }

  /** Prefix match first, then anything whose description mentions the query. */
  function paletteMatches(commands, query) {
    const needle = query.toLowerCase();
    const byName = commands.filter((command) => command.name.toLowerCase().indexOf(needle) === 0);
    if (byName.length) return byName.slice(0, 8);
    return commands
      .filter((command) => String(command.description || '').toLowerCase().indexOf(needle) !== -1)
      .slice(0, 8);
  }

  function closePalette() {
    paletteRows = [];
    paletteIndex = 0;
    palette.hidden = true;
    palette.textContent = '';
  }

  function paintPalette() {
    palette.textContent = '';
    for (let index = 0; index < paletteRows.length; index++) {
      const command = paletteRows[index];
      const row = el('button', 'palette-row' + (index === paletteIndex ? ' current' : ''));
      row.type = 'button';
      row.appendChild(el('span', 'palette-name', '/' + command.name));
      row.appendChild(el('span', 'palette-desc', command.description || ''));
      if (command.source === 'skill') row.appendChild(el('span', 'palette-tag', 'skill'));
      row.addEventListener('mousedown', (event) => {
        // mousedown, not click: the textarea would blur first and the palette
        // would be gone before the click ever landed.
        event.preventDefault();
        acceptPalette(index);
      });
      palette.appendChild(row);
    }
    palette.hidden = paletteRows.length === 0;
  }

  function updatePalette() {
    const query = paletteQuery();
    if (query === null) {
      closePalette();
      return;
    }
    // Asking for the catalogue is what a `/` means: it is fetched once and then
    // served from memory, so typing after the first keystroke costs nothing.
    if (catalogue === null) {
      vscode.postMessage({ type: 'commands' });
      palette.hidden = true;
      return;
    }
    paletteRows = paletteMatches(catalogue, query);
    if (paletteIndex >= paletteRows.length) paletteIndex = 0;
    paintPalette();
  }

  /** Puts the command name in the composer; what follows is its arguments. */
  function acceptPalette(index) {
    const command = paletteRows[index];
    closePalette();
    if (!command) return;
    input.value = '/' + command.name + ' ';
    growInput();
    input.focus();
  }

  function movePalette(delta) {
    if (paletteRows.length === 0) return;
    paletteIndex = (paletteIndex + delta + paletteRows.length) % paletteRows.length;
    paintPalette();
  }

  // --- command cards --------------------------------------------------------

  function commandCard(title, commands) {
    const card = el('div', 'commands');
    card.appendChild(el('div', 'head', title));
    for (const command of commands) {
      const row = el('div', 'command');
      row.appendChild(el('span', 'cmd', '/' + command.name));
      row.appendChild(el('span', 'desc', command.description || ''));
      if (command.source === 'skill') row.appendChild(el('span', 'tag', 'skill'));
      card.appendChild(row);
    }
    return card;
  }

  function showHelp(commands) {
    const builtins = commands.filter((command) => command.source === 'builtin');
    const skills = commands.filter((command) => command.source !== 'builtin');
    append(commandCard('Commands', builtins));
    if (skills.length) append(commandCard('Skills in this workspace', skills));
    endRound();
  }

  /**
   * Lists the rule files the session loaded, and says plainly which of them
   * reached the prompt: "why is the model ignoring my AGENTS.md" is otherwise
   * unanswerable from the outside.
   */
  function showRules(files) {
    const card = el('div', 'commands rules');
    card.appendChild(el('div', 'head', files.length ? 'Rule files loaded' : 'No rule files found'));
    for (const file of files) {
      const row = el('div', 'command');
      row.appendChild(el('span', 'cmd', file.path));
      const skipped = file.included !== true;
      const state = skipped ? 'past the budget, skipped' : file.truncated ? 'truncated' : 'in the prompt';
      row.appendChild(el('span', 'desc', file.bytes ? state + ' · ' + file.bytes + ' bytes' : state));
      if (skipped) row.classList.add('muted');
      card.appendChild(row);
    }
    if (files.length === 0) {
      card.appendChild(el('div', 'command muted', 'AGENTS.md and .anvil/rules/*.md were not found in this workspace.'));
    }
    append(card);
    endRound();
  }

  function showSession(msg) {
    const line = el('div', 'msg session');
    const label = msg.started !== true
      ? 'New conversation. The next message starts a fresh session.'
      : 'Continuing session ' + msg.agentId + '.';
    line.appendChild(el('div', 'body', label));
    append(line);
    endRound();
  }


  function setRunning(next) {
    running = next;
    stopButton.hidden = !running;
    sendButton.disabled = running;
    input.disabled = running;
    attachButton.disabled = running;
    // The mode is a session option, so the extension refuses a change mid-turn
    // rather than applying it half way. Saying so by disabling is honest about
    // that; the extension's own check is what enforces it.
    if (modeSelect) modeSelect.disabled = running;
    if (!running) input.focus();
  }

  function count(value) {
    if (typeof value !== 'number' || !isFinite(value) || value <= 0) return '';
    return value >= 1000 ? (value / 1000).toFixed(value >= 10000 ? 0 : 1) + 'k' : String(value);
  }

  function finishRun(done, failure) {
    endRound();
    setRunning(false);
    const parts = [];
    const steps = done && typeof done.steps === 'number' ? `${done.steps} step${done.steps === 1 ? '' : 's'}` : '';
    if (steps) parts.push(steps);
    const usage = (done && done.usage) || null;
    if (usage) {
      const total = count(usage.total_tokens);
      if (total) {
        const split = count(usage.input_tokens) && count(usage.output_tokens)
          ? ` (${count(usage.input_tokens)} in / ${count(usage.output_tokens)} out)`
          : '';
        parts.push(`${total} tokens${split}`);
      }
    }
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

  // --- composer height ------------------------------------------------------

  /**
   * Grows the textarea to fit what is in it, up to the cap the stylesheet
   * declares — one source of truth for the cap, rather than a second copy of it
   * here in pixels.
   */
  function growInput() {
    input.style.height = 'auto';
    input.style.height = input.scrollHeight + 'px';
  }

  /**
   * Publishes the composer's height, which is what the transcript reserves at
   * its bottom so the last message is never hidden behind the card. Both the
   * palette opening and the textarea growing change this box, so measuring the
   * box covers them without either of them knowing about this.
   *
   * The variable goes on the document element, not on the dock: custom
   * properties inherit downward, and the transcript is the dock's *sibling*, so
   * a variable set on the dock would never reach the rule that reads it. That
   * failure is silent — the reserve just falls back to its declared default and
   * the last message slides under the card — so a test asserts the two files
   * agree on the name.
   */
  function syncComposerHeight() {
    if (!dock) return;
    // Read before writing: the reserve is about to change the transcript's
    // scroll height, and whether the reader was at the bottom has to be decided
    // against the layout they are looking at, not the one being computed.
    const pinned = atBottom();
    document.documentElement.style.setProperty('--anvil-composer-h', dock.offsetHeight + 'px');
    scrollIfPinned(pinned);
  }

  if (dock && typeof ResizeObserver === 'function') {
    // Fires once on observe, which is also how the first paint's placeholder
    // value is replaced with the real measurement.
    new ResizeObserver(() => syncComposerHeight()).observe(dock);
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
    growInput();
    mentions = [];
    images = [];
    closePalette();
    renderChips();
    addUser(text, sentMentions, sentImages);
    // Optimistic: the host's `state` notification confirms it a moment later,
    // but the composer must lock immediately or a double Enter sends twice.
    setRunning(true);
    statusLine.hidden = true;
    vscode.postMessage({ type: 'send', text, mentions: sentMentions, images: sentImages });
  });

  input.addEventListener('keydown', (event) => {
    // The palette owns the arrow keys and Enter while it is open: without this
    // Enter would submit the half-typed command instead of completing it.
    if (!palette.hidden && paletteRows.length > 0) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        movePalette(1);
        return;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        movePalette(-1);
        return;
      }
      if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault();
        acceptPalette(paletteIndex);
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        closePalette();
        return;
      }
    }
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
    growInput();
    if (input.value.endsWith('@')) {
      input.value = input.value.slice(0, -1);
      closePalette();
      askForMentions();
      return;
    }
    updatePalette();
  });

  input.addEventListener('blur', () => closePalette());

  if (modeSelect) {
    modeSelect.addEventListener('change', () => {
      // The echo is optimistic only in that it is immediate: the extension
      // answers with a `mode` message, and that is what the select ends up
      // showing, so a refusal corrects this instead of sticking.
      vscode.postMessage({ type: 'mode', mode: modeSelect.value });
    });
  }

  if (rulesButton) {
    rulesButton.addEventListener('click', () => showRules(rules));
  }

  if (permissionsButton) {
    // Always offered, even before the file exists: creating it is one of the two
    // things this button is for, and an entry point that only appears once
    // somebody has already found the file is not an entry point.
    permissionsButton.addEventListener('click', () => vscode.postMessage({ type: 'openPermissions' }));
  }

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
        else if (e.type === 'reasoning') addReasoning(e.text || '');
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
      case 'rememberResult':
        resolveRemember(msg);
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
      case 'commands':
        catalogue = Array.isArray(msg.commands) ? msg.commands : [];
        // The palette was opened for a `/` that arrived before the catalogue
        // did, so what was typed has to be matched again now that it is here.
        updatePalette();
        return;
      case 'help':
        showHelp(Array.isArray(msg.commands) ? msg.commands : []);
        return;
      case 'mode':
        showMode(msg);
        return;
      case 'rules': {
        rules = Array.isArray(msg.files) ? msg.files : [];
        if (rulesButton) {
          rulesButton.hidden = rules.length === 0;
          rulesButton.textContent = 'Rules (' + rules.length + ')';
        }
        // Only what the user cannot see for themselves is shown unbidden. A
        // rule file that was skipped or cut short is a silent failure of
        // something they wrote, which is exactly when to speak up; the rest
        // waits behind the button.
        if (rules.some((file) => file.included !== true || file.truncated === true)) showRules(rules);
        return;
      }
      case 'session':
        showSession(msg);
        return;
      case 'reset':
        clearTranscript();
        return;
      case 'focus':
        // The tab became the active editor, so the keyboard belongs here —
        // unless a turn is running, in which case the textarea is disabled.
        if (!running) input.focus();
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
