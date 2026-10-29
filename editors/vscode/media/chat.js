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
  const queueCount = document.getElementById('queueCount');
  const sendModeButton = document.getElementById('sendMode');
  const sendModeMenu = document.getElementById('sendModeMenu');
  const sendModeRows = sendModeMenu ? Array.from(sendModeMenu.querySelectorAll('.send-mode-row')) : [];
  const stopButton = document.getElementById('stop');
  const statusLine = document.getElementById('status');
  /**
   * The entry inside the transcript that holds the run's line and the root's.
   * In the flow rather than the dock, so it scrolls with the conversation and
   * cannot be painted over (p_0.46.24).
   */
  const footprint = document.getElementById('footprint');
  const banner = document.getElementById('banner');
  const replayLoading = document.getElementById('replayLoading');
  const chipsBar = document.getElementById('chips');
  const attachButton = document.getElementById('attach');
  const actionMenu = document.getElementById('actionMenu');
  const mentionButton = document.getElementById('mention');
  const quoteButton = document.getElementById('quote');
  const imageButton = document.getElementById('image');
  const slashButton = document.getElementById('slash');
  const modeButton = document.getElementById('mode');
  const modeMenu = document.getElementById('modeMenu');
  const modeLabel = document.getElementById('modeLabel');
  const modeRows = modeMenu ? Array.from(modeMenu.querySelectorAll('.mode-row')) : [];
  const collaborationButton = document.getElementById('collaboration');
  const collaborationMenu = document.getElementById('collaborationMenu');
  const collaborationLabel = document.getElementById('collaborationLabel');
  const collaborationDot = collaborationButton
    ? collaborationButton.querySelector('.collaboration-dot')
    : null;
  const collaborationRows = collaborationMenu
    ? Array.from(collaborationMenu.querySelectorAll('.collaboration-row:not(:disabled)'))
    : [];
  const agentMapButton = document.getElementById('agentMap');
  const modelPicker = document.getElementById('modelPicker');
  const modelPickerLabel = document.getElementById('modelPickerLabel');
  const modelMenu = document.getElementById('modelMenu');
  const modelMenuList = document.getElementById('modelMenuList');
  const effortMenuLabel = document.getElementById('effortMenuLabel');
  const effortButtons = Array.from(document.querySelectorAll('[data-effort]'));
  const bypassStrip = document.getElementById('bypass');
  const dontAskStrip = document.getElementById('dontAsk');
  const rootLine = document.getElementById('root');
  const rulesButton = document.getElementById('rules');
  const permissionsButton = document.getElementById('permissions');
  const focusButton = document.getElementById('focus');
  const palette = document.getElementById('palette');
  const dock = document.getElementById('composerDock');
  const planDock = document.getElementById('planDock');
  const autoScrollButton = document.getElementById('autoScroll');
  const scrollBottomButton = document.getElementById('scrollBottom');
  const meter = document.getElementById('meter');
  /**
   * The conversation total, a node per fact rather than one string: each count can
   * land on its own, and a landing count pulses (p_0.46.26). The clock tick is
   * a node inside the cache count so its per-second write cannot pulse the
   * number beside it, and the window's share is a node of its own because
   * `meterMath.label` reports it beside the counts, not instead of them.
   */
  const meterIn = el('span', 'meter-in');
  const meterOut = el('span', 'meter-out');
  const meterShare = el('span', 'meter-share');
  const meterCache = el('span', 'meter-cache');
  const meterClock = el('span', 'meter-clock');
  const meterPrefix = el('span', 'meter-prefix', '\u00a0· ');
  const meterDot1 = el('span', 'meter-dot', ' · ');
  const meterDot2 = el('span', 'meter-dot', ' · ');
  const meterDot3 = el('span', 'meter-dot', ' · ');
  /** The counts in the order the line reads them; the dots sit between. */
  const meterCounts = [meterIn, meterOut, meterShare, meterCache];
  if (meter) {
    meter.appendChild(meterPrefix);
    meter.appendChild(meterIn);
    meter.appendChild(meterDot1);
    meter.appendChild(meterOut);
    meter.appendChild(meterDot2);
    meter.appendChild(meterShare);
    meter.appendChild(meterDot3);
    meter.appendChild(meterCache);
  }
  const todoBlock = document.getElementById('todos');
  const todosToggle = document.getElementById('todosToggle');
  const todosBody = document.getElementById('todosBody');
  const todosCount = document.getElementById('todosCount');
  const todoList = document.getElementById('todoList');
  const todoCurrentLabel = document.getElementById('todoCurrentLabel');
  const todoEmpty = document.getElementById('todoEmpty');
  const todoHistory = document.getElementById('todoHistory');
  const todoHistorySummary = document.getElementById('todoHistorySummary');
  const todoHistoryList = document.getElementById('todoHistoryList');
  /**
   * The page's two live regions. `srStatus` is polite — a finished answer and
   * the end of a turn; `srAlert` is assertive and reserved for the two things
   * that cannot wait: an approval request, and an error.
   */
  const srStatus = document.getElementById('srStatus');
  const srAlert = document.getElementById('srAlert');

  /**
   * The meter's arithmetic, from `media/meter.js`. It is a plain script that
   * assigns a global rather than a module, like the Markdown parser, and it is
   * included before this file for that reason.
   */
  const meterMath = self.anvilMeter;

  /**
   * The same shorthand the meter uses for a token count, so a run's own summary
   * and the session meter can never give one number two spellings.
   */
  const count = meterMath.short;

  /**
   * The same shorthand, except that zero is spelled out. `short(0)` is the empty
   * string, which is right where a count is optional and wrong where it is the
   * subject — "0 of 200k" is a fact, " of 200k" is a riddle.
   */
  function amount(value) {
    const whole = meterMath.count(value);
    return whole > 0 ? count(whole) : '0';
  }

  /** Prompt recall, from `media/history.js` — another plain global script. */
  const historyMath = self.anvilHistory;
  const history = historyMath.create();
  /**
   * The value the history last put in the box, or null. It is how the input
   * handler tells the user editing a recalled prompt from the walk itself
   * writing one, which is the difference between starting over at the newest
   * entry and continuing down the list.
   */
  let placed = null;

  /** Run in flight, if any. */
  let running = false;
  /** What clicking Send does while a run is active. */
  let runningSendMode = 'enqueue';
  /**
   * A stored conversation is being drawn. Everything said to a screen reader is
   * held back while it is: a replay is history the reader did not just ask for,
   * and announcing it block by block would read the whole session aloud the
   * moment it is opened.
   */
  let replaying = false;
  /** Keep a restored conversation at its newest turn until async blocks settle. */
  let replayPinsBottom = false;
  let replayAsyncBlocks = 0;
  let replayGeneration = 0;
  let collaborationMode = 'multi';
  let agentMapDialog = null;
  let agentMapState = null;
  let replayLoadingAnimations = [];
  /** True once a prompt has been sent: the empty state is gone for good. */
  let started = false;
  /**
   * A render target: the window's transcript, or one spawn call's body.
   *
   * A child's events are drawn inside the card its parent's `spawn_agent` call
   * made, not into the parent's transcript. Giving each target its own round
   * and container is what lets the same primitives — text, reasoning, tool
   * cards — draw both, rather than a second renderer that would drift from the
   * first. `active` is the target the next block lands in; event dispatch
   * switches it for a child and back.
   */
  function makeScope(container) {
    return { container, round: null };
  }
  const mainScope = makeScope(transcript);
  let active = mainScope;
  /**
   * The target of each spawn call, by the `tool_call_id` of the call. A child's
   * event names its spawn call, which is how it finds the card — at any depth,
   * because a grandchild's spawn call is registered by the same code.
   */
  const spawnScopes = new Map();
  /** Background task id to the spawn card that handed it off. */
  const backgroundCards = new Map();
  /** Last lifecycle event, including one that arrived before the handoff result. */
  const backgroundTaskStates = new Map();
  /** Stable foreground spawn-call and background task ids that are still working. */
  const activeSubagents = new Set();
  /** Spawn calls whose parent result arrived; late child chunks cannot reactivate them. */
  const completedSubagentSpawns = new Set();

  function syncCollaborationActivity() {
    const working = collaborationMode === 'multi' && activeSubagents.size > 0;
    if (collaborationDot) collaborationDot.classList.toggle('active', working);
    if (collaborationButton) {
      collaborationButton.title = working
        ? activeSubagents.size + (activeSubagents.size === 1 ? ' subagent working' : ' subagents working')
        : 'No subagents working';
    }
  }
  /** Runs `fn` with `scope` as the target, and restores the previous one. */
  function inScope(scope, fn) {
    const previous = active;
    active = scope;
    try {
      fn();
    } finally {
      active = previous;
    }
  }
  /** The assistant round being streamed in the active target, or null. */
  function currentRound() {
    return active.round;
  }
  function setCurrentRound(state) {
    active.round = state;
  }
  /** Tool cards by tool_call_id, so a result can find its call. */
  const toolCards = new Map();
  /**
   * The facts a card's review button hands back, by tool_call_id: the call's
   * name, its arguments, and the run it belonged to.
   *
   * They are kept here because the review is derived from the call's own
   * arguments and the card is the only place they still exist — the extension
   * keeps no copy of every call's body. What is stored is the same object the
   * IN pane is already showing as text, so it costs a reference, not a copy.
   */
  const callFacts = new Map();
  /** Approval cards by request_id. */
  const approvals = new Map();
  /**
   * Undo cards by run id: the card and the button that was pressed, so the
   * restore's answer can settle the one it belongs to and no other.
   */
  const checkpointBlocks = new Map();
  /**
   * Fork buttons by run id. A run's row is drawn once, but the answer can arrive
   * after the transcript has been replaced, so the map is cleared with it.
   */
  const forkBlocks = new Map();
  /**
   * Plan surfaces by run id, so an approval or a refusal can settle the review
   * it belongs to. Only the newest surface is docked; older entries remain
   * addressable until the conversation resets.
   */
  const planCards = new Map();
  /**
   * One anchor per user message, in transcript order: the run that followed it
   * once that run has ended, and the control it owns.
   *
   * The anchor is the message because the undo is a turn. A checkpoint is taken
   * before a run, so the only honest place to say "go back to before this" is
   * the prompt the run answered.
   */
  const rewindTurns = [];
  /**
   * The paths each finished run captured, by run id, as the extension read
   * them. A missing key is "not answered yet", which is different from an empty
   * list: the first waits, the second says the turn changed nothing.
   */
  const rewindStates = new Map();
  /** The visible conclusion for each completed run, used to place its file list. */
  const runSummaries = new Map();
  /** Referenced workspace paths, in the order they were picked. */
  let mentions = [];
  /** Earlier messages selected as context for the next prompt. */
  let quotes = [];
  /** The current browser selection, split into narrative regions by speaker. */
  let selectedQuoteParts = [];
  let selectedCopyText = '';
  let selectionQuoteButton = null;
  let selectionContextMenu = null;
  let selectionPaintQueued = false;
  /** Quoteable messages in transcript order. */
  let quoteCandidates = [];
  let nextQuoteId = 1;
  /** Pasted images, as data URLs. */
  let images = [];
  /** Whether the session was created with vision on. Images need it. */
  let vision = false;
  /**
   * The context window from settings, or 0 when nobody configured one. There is
   * no per-model table here: a guessed window would turn the meter into a
   * number about Anvil rather than about the model.
   */
  let contextWindow = 0;
  /** Where the session compacts, as a fraction of the window. 0 means unset. */
  let autoCompactAt = 0;
  /** The prompt cache's TTL the client knows, or 0 when none is known. */
  let cacheTtlSeconds = 0;
  /**
   * The host's last estimate of the conversation, or null before one arrives.
   * It is an estimate of the next request, not the provider's count — the field
   * names on the wire say so, and so does the meter's wording.
   */
  let contextInfo = null;
  /** When the last run reached the provider, in ms since epoch, or null. */
  let lastRequestAt = null;
  /** The one-second cache countdown, or null while nothing needs counting. */
  let cacheClock = null;
  /**
   * What this session has spent, and what its last run spent. Reset with the
   * transcript, because the two describe the same conversation: a meter outliving
   * the conversation it counted would be a number about nobody.
   */
  let usageTotals = meterMath.empty();
  /** The current run's cumulative estimate, replaced by terminal provider usage. */
  let liveUsage = null;
  /**
   * The span on the root line that carries this conversation's share of its
   * context window, or null until a root exists. `renderMeter` owns both
   * scopes; this is only where the conversation's half is written.
   */
  let rootFacts = null;
  /** The slash catalogue, once the extension has sent it. */
  let catalogue = null;
  /** Rule files the last session reported, kept so the card can be reopened. */
  let rules = [];
  /** The palette rows currently shown, and which one is highlighted. */
  let paletteRows = [];
  let paletteIndex = 0;
  let quotePickerForced = false;

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  // --- the working line -----------------------------------------------------

  const SVG_NS = 'http://www.w3.org/2000/svg';

  /** An SVG node with its attributes. The working mark is the only SVG here. */
  function svgNode(tag, attrs) {
    const node = document.createElementNS(SVG_NS, tag);
    for (const name of Object.keys(attrs)) node.setAttribute(name, attrs[name]);
    return node;
  }

  /**
   * The working mark: an anvil, a hammer whose head pivots at the anvil's edge,
   * and a spark where the head lands. Built as nodes rather than markup: a
   * character has no hammer that keeps its shape in every font, and one small
   * picture is not worth an image file of its own.
   */
  function hammerGlyph() {
    const svg = svgNode('svg', {
      class: 'working-glyph',
      viewBox: '0 0 16 16',
      'aria-hidden': 'true',
      focusable: 'false',
    });
    // Drawn to the box, not inside it: face, horn, waist and base span x 0.2–11.4
    // and y 9.8–15, so the mark's ink reaches the edges a 16-unit box gives it.
    svg.appendChild(
      svgNode('path', {
        class: 'anvil',
        fill: 'currentColor',
        d:
          'M1.2 9.8h10.2v2H1.2z' +
          'M1.2 9.8 0.2 10.8l1 1z' +
          'M6.4 11.8h2.2v1.6H6.4z' +
          'M3.8 13.4h7.2v1.6H3.8z',
      }),
    );
    const spark = svgNode('g', { class: 'spark' });
    // Three rays where the head lands. 1.4 units is about 1.6px at 18px; at 0.8
    // the browser antialiased the strike into a smudge.
    spark.appendChild(
      svgNode('path', {
        d: 'M5.2 6.0v2.4M3.6 6.8 5.2 8.4M6.8 6.8 5.2 8.4',
        fill: 'none',
        stroke: 'currentColor',
        'stroke-width': '1.4',
        'stroke-linecap': 'round',
      }),
    );
    svg.appendChild(spark);
    // The handle meets the face at (11.4, 9.8) — the pivot — and the head rests
    // above it; both are wider than they were so the hammer has weight at 18px.
    const hammer = svgNode('g', { class: 'hammer' });
    hammer.appendChild(
      svgNode('rect', {
        x: '10.6',
        y: '2.6',
        width: '1.6',
        height: '7.2',
        rx: '.8',
        fill: 'currentColor',
      }),
    );
    hammer.appendChild(
      svgNode('rect', {
        x: '8.2',
        y: '0.8',
        width: '6.2',
        height: '2.8',
        rx: '.8',
        fill: 'currentColor',
      }),
    );
    svg.appendChild(hammer);
    return svg;
  }

  /** The neutral activity before a stream or tool event says what is happening. */
  const DEFAULT_WORKING_WORD = 'Thinking…';

  /*
   * The status line is two halves that never show at once: the working mark
   * while a turn is in flight, and the run's summary after it. They are built
   * once so a second's re-render cannot restart the animation or the clock.
   */
  const statusText = el('span', 'status-text');
  const workingLine = el('span', 'working');
  const workingWord = el('span', 'working-word', DEFAULT_WORKING_WORD);
  const workingMeta = el('span', 'working-meta');
  /**
   * The step counter, in its own node so a landing step can pulse without
   * moving the clock beside it. It is an inline-block, not an inline span:
   * a transform on an inline box does not apply (p_0.46.26).
   */
  const workingStepNode = el('span', 'working-step');
  statusText.hidden = true;
  workingLine.hidden = true;
  workingLine.appendChild(hammerGlyph());
  workingLine.appendChild(workingWord);
  workingLine.appendChild(workingMeta);
  statusLine.appendChild(workingLine);
  statusLine.appendChild(statusText);

  /** The swing is motion, so a reader who asked for less gets a still mark. */
  const reduceMotion =
    typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;

  let workingTimer = null;
  let workingSince = 0;
  let workingStep = 0;
  let workingMax = 0;
  let workingAnimations = [];

  /**
   * The head falls onto the anvil and the spark answers. The Web Animations API
   * rather than a keyframe block: `chat.css` may not carry an at-rule, and the
   * only geometry the stylesheet supplies is the pivot.
   */
  function animateHammer() {
    const glyph = workingLine.querySelector('.working-glyph');
    if (!glyph || typeof Element.prototype.animate !== 'function') return [];
    const head = glyph.querySelector('.hammer');
    const spark = glyph.querySelector('.spark');
    const animations = [];
    if (head) {
      animations.push(
        head.animate(
          [
            { transform: 'rotate(0deg)', offset: 0 },
            { transform: 'rotate(0deg)', offset: 0.55 },
            { transform: 'rotate(-72deg)', offset: 0.66 },
            { transform: 'rotate(-70deg)', offset: 0.73 },
            { transform: 'rotate(0deg)', offset: 1 },
          ],
          { duration: 900, iterations: Infinity, easing: 'cubic-bezier(.3, 0, .6, 1)' },
        ),
      );
    }
    if (spark) {
      animations.push(
        spark.animate(
          [
            { opacity: 0, offset: 0 },
            { opacity: 0, offset: 0.62 },
            { opacity: 1, offset: 0.67 },
            { opacity: 0, offset: 0.82 },
            { opacity: 0, offset: 1 },
          ],
          { duration: 900, iterations: Infinity },
        ),
      );
    }
    return animations;
  }

  /** The line a turn shows while it runs: the mark, the word and the facts. */
  function startWorking() {
    if (workingTimer !== null) return; // the clock is already running
    // The line lives in the scroll content now, so a write to it moves the
    // reader's position unless it is put back.
    const pinned = atBottom();
    workingSince = Date.now();
    workingStep = 0;
    workingMax = 0;
    workingWord.textContent = DEFAULT_WORKING_WORD;
    delete workingWord.dataset.flowText;
    statusText.hidden = true;
    workingLine.hidden = false;
    renderWorkingMeta();
    syncFootprint();
    scrollIfPinned(pinned);
    workingTimer = setInterval(renderWorkingMeta, 1000);
    if (!reduceMotion) workingAnimations = animateHammer();
  }

  function stopWorking() {
    if (workingTimer !== null) {
      clearInterval(workingTimer);
      workingTimer = null;
    }
    for (const animation of workingAnimations) animation.cancel();
    workingAnimations = [];
    const pinned = atBottom();
    workingLine.hidden = true;
    syncFootprint();
    scrollIfPinned(pinned);
  }

  /**
   * The parenthetical, in the order the CLI puts it: what stops the turn, how
   * long it has run, which step it is on, and the run's live token estimate.
   */
  function renderWorkingMeta() {
    if (document.hidden) return; // nobody is reading a hidden tab
    const seconds = Math.max(0, Math.floor((Date.now() - workingSince) / 1000));
    const spent =
      seconds < 60 ? seconds + 's' : Math.floor(seconds / 60) + 'm ' + (seconds % 60) + 's';
    // The escape and the clock stay one run of text; the step is appended
    // as a node, because a node is what the pulse has to move.
    workingMeta.textContent = '(' + ['esc to interrupt', spent].join(' · ');
    if (workingStep > 0) {
      // The separator stays outside the pulsing node: the pulse moves the
      // counter, not the punctuation in front of it.
      workingMeta.appendChild(document.createTextNode('\u00a0· '));
      workingStepNode.textContent = stepLabel();
      workingMeta.appendChild(workingStepNode);
    }
    // The meter owns its leading separator and stays hidden until the runtime
    // has a current-run estimate. Keeping it inside this parenthetical avoids a
    // second footprint row while preserving its per-character animation.
    workingMeta.appendChild(meter);
    workingMeta.appendChild(document.createTextNode(')'));
  }

  /** Changes activity only when an event says the work itself changed. */
  function setWorkingActivity(text) {
    if (workingLine.hidden || typeof text !== 'string' || text === '') return;
    if (workingWord.textContent !== text) flowText(workingWord, text, 620);
  }

  /** The step as the line spells it, with the ceiling only when one was set. */
  function stepLabel() {
    if (workingStep <= 0) return '';
    return workingMax > 0 ? 'step ' + workingStep + '/' + workingMax : 'step ' + workingStep;
  }

  /**
   * A number that has just become a new fact, saying so once.
   *
   * The Web Animations API rather than a keyframe block, for the same reason
   * the hammer swings from script: `chat.css` may not carry an at-rule, and a
   * test parses it as text. A short scale and an opacity dip — a change of
   * state, not a celebration — and it leaves nothing behind, so a number
   * rewritten with the same value cannot look stuck.
   */
  function pulseNumber(node) {
    if (!node || reduceMotion) return;
    if (typeof Element.prototype.animate !== 'function') return;
    for (const animation of node.getAnimations()) animation.cancel();
    node.animate(
      [
        { transform: 'scale(1)', opacity: 1, offset: 0 },
        { transform: 'scale(1.18)', opacity: 1, offset: 0.35 },
        { transform: 'scale(1)', opacity: 0.6, offset: 0.7 },
        { transform: 'scale(1)', opacity: 1, offset: 1 },
      ],
      { duration: 520, easing: 'cubic-bezier(.2, 0, .3, 1)' },
    );
  }

  /**
   * What each number node was last told, by node.
   *
   * The node's own `textContent` cannot answer that: the cache count owns the
   * clock node, so its text changes every second while the number does not,
   * and comparing the assembled text would pulse the count once a second.
   */
  const numberText = new WeakMap();
  /** Animation frame and currently painted numeric value for live counters. */
  const rollingFrame = new WeakMap();
  const rollingValue = new WeakMap();
  const rollingTarget = new WeakMap();
  const flowFrame = new WeakMap();

  function paintTextFlow(node, text, progress) {
    if (node.dataset.flowText !== text) {
      node.textContent = '';
      node.dataset.flowText = text;
      for (const character of text) {
        const className = /\s/.test(character) ? 'flow-char flow-gap' : 'flow-char';
        node.appendChild(el('span', className, character));
      }
    }
    const chars = Array.from(node.children);
    const head = progress * (chars.length + 2) - 1;
    chars.forEach((char, index) => {
      const strength = Math.max(0, 1 - Math.abs(index - head) / 1.6);
      char.style.transform = `translateY(${-0.06 * strength}em) scale(${1 + 0.2 * strength})`;
      char.style.filter = `brightness(${1 + 0.65 * strength})`;
      char.style.textShadow =
        strength > 0.05 ? `0 0 ${6 * strength}px var(--vscode-focusBorder)` : 'none';
    });
  }

  /** Replaces text while one bright, enlarged character front travels over it. */
  function flowText(node, text, duration) {
    if (!node) return;
    const previous = flowFrame.get(node);
    if (previous !== undefined) cancelAnimationFrame(previous);
    flowFrame.delete(node);
    if (reduceMotion || typeof requestAnimationFrame !== 'function') {
      node.textContent = text;
      delete node.dataset.flowText;
      return;
    }
    const started = performance.now();
    const length = duration || 560;
    const tick = (now) => {
      const progress = Math.min(1, (now - started) / length);
      paintTextFlow(node, text, progress);
      if (progress < 1) flowFrame.set(node, requestAnimationFrame(tick));
      else flowFrame.delete(node);
    };
    flowFrame.set(node, requestAnimationFrame(tick));
  }

  function stopNumberRoll(node) {
    const frame = rollingFrame.get(node);
    if (frame !== undefined) cancelAnimationFrame(frame);
    rollingFrame.delete(node);
  }

  /**
   * Rolls one token count toward a new estimate. A new stream chunk retargets
   * the in-flight roll from the value currently on screen, so fast chunks move
   * one counter continuously instead of queueing animations.
   */
  function rollNumber(node, value, suffix, label, exact) {
    if (!node) return;
    const target = Math.max(0, Math.floor(Number(value) || 0));
    const textOf = (n) => (label ? label + ' ' : '') + (exact ? String(n) : amount(n)) + suffix;
    const next = textOf(target);
    if ((numberText.get(node) || '') === next && rollingTarget.get(node) === target) return;
    numberText.set(node, next);
    rollingTarget.set(node, target);
    stopNumberRoll(node);
    const from = rollingValue.has(node) ? rollingValue.get(node) : 0;
    if (reduceMotion || from === target || typeof requestAnimationFrame !== 'function') {
      rollingValue.set(node, target);
      node.textContent = next;
      delete node.dataset.flowText;
      return;
    }
    const started = performance.now();
    const duration = 320;
    const tick = (now) => {
      const progress = Math.min(1, (now - started) / duration);
      const eased = 1 - Math.pow(1 - progress, 3);
      const current = Math.round(from + (target - from) * eased);
      rollingValue.set(node, current);
      paintTextFlow(node, textOf(current), progress);
      if (progress < 1) rollingFrame.set(node, requestAnimationFrame(tick));
      else rollingFrame.delete(node);
    };
    rollingFrame.set(node, requestAnimationFrame(tick));
  }

  /**
   * Gives a node the number it now holds, and pulses it only when that number is
   * a new one.
   *
   * An unchanged value is not written at all, so the pulse always means "this
   * changed" rather than "this was redrawn" — `renderMeter` runs on every cache
   * tick and every root update, and most of those do not move a count. A count
   * that appears for the first time, or disappears with a reset, is drawn
   * without a pulse: the pulse marks a number arriving, not the line opening.
   */
  function setNumber(node, text) {
    if (!node) return;
    stopNumberRoll(node);
    rollingValue.delete(node);
    rollingTarget.delete(node);
    const next = String(text);
    const before = numberText.get(node) || '';
    if (before === next) return;
    numberText.set(node, next);
    node.textContent = next;
    delete node.dataset.flowText;
    if (before !== '' && next !== '') pulseNumber(node);
  }

  /** The other half: the run's summary, or a note the host sent. */
  function setStatus(text) {
    const pinned = atBottom();
    statusText.textContent = text;
    statusText.hidden = text === '';
    syncFootprint();
    scrollIfPinned(pinned);
  }

  /**
   * Shows the footprint once any of its lines has something to say, and hides
   * it only while all of them are empty.
   *
   * The block is hidden as a whole rather than by its children: the status
   * line keeps its reserved box (see `.footprint .status`), so a turn starting
   * or ending changes what the block says without changing its height.
   */
  function syncFootprint() {
    if (!footprint) return;
    footprint.hidden = rootLine.hidden && meter.hidden && workingLine.hidden && statusText.hidden;
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

  function syncScrollBottomButton() {
    if (!scrollBottomButton) return;
    scrollBottomButton.hidden = atBottom();
  }

  function syncAutoScrollButton() {
    if (!autoScrollButton) return;
    autoScrollButton.setAttribute('aria-pressed', autoScroll ? 'true' : 'false');
    autoScrollButton.setAttribute('aria-label', autoScroll ? 'Disable auto-scroll' : 'Enable auto-scroll');
    autoScrollButton.title = autoScroll
      ? 'Auto-scroll is on'
      : 'Auto-scroll is off — review position is preserved';
  }

  function scrollConversationToBottom() {
    transcript.scrollTop = transcript.scrollHeight;
    syncScrollBottomButton();
  }

  function setAutoScroll(on) {
    autoScroll = Boolean(on);
    syncAutoScrollButton();
    remember({ autoScroll });
    if (autoScroll) scrollConversationToBottom();
  }

  function scrollIfPinned(wasPinned) {
    if (autoScroll || wasPinned) transcript.scrollTop = transcript.scrollHeight;
    syncScrollBottomButton();
  }

  /**
   * Finishes a restored conversation at the newest turn. Two frames are
   * intentional: the first lets queued Markdown paints run, and the second
   * wins over VS Code restoring the webview's previous DOM scroll anchor.
   * Mermaid blocks keep the pin alive until their asynchronous render ends.
   */
  function settleReplayAtBottom() {
    if (!replayPinsBottom || replaying || replayAsyncBlocks > 0) return;
    transcript.scrollTop = transcript.scrollHeight;
    requestAnimationFrame(() => {
      transcript.scrollTop = transcript.scrollHeight;
      requestAnimationFrame(() => {
        transcript.scrollTop = transcript.scrollHeight;
        replayPinsBottom = false;
        hideReplayLoading();
      });
    });
  }

  function showReplayLoading() {
    if (!replayLoading) return;
    replayLoading.hidden = false;
    for (const animation of replayLoadingAnimations) animation.cancel();
    replayLoadingAnimations = [];
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    const dots = replayLoading.querySelectorAll('.replay-loading-mark i');
    dots.forEach((dot, index) => {
      replayLoadingAnimations.push(
        dot.animate(
          [
            { transform: 'translateY(0)', opacity: 0.45 },
            { transform: 'translateY(-6px)', opacity: 1, offset: 0.33 },
            { transform: 'translateY(0)', opacity: 0.45 },
          ],
          { duration: 900, delay: index * 120, iterations: Infinity, easing: 'ease-in-out' },
        ),
      );
    });
  }

  function hideReplayLoading() {
    for (const animation of replayLoadingAnimations) animation.cancel();
    replayLoadingAnimations = [];
    if (replayLoading) replayLoading.hidden = true;
  }

  /**
   * The document's saved state — which conversation it is showing, and whether
   * it is in the focus view.
   *
   * Merged rather than replaced. The two are written at different moments (the
   * agent when the session is announced, the view when it is toggled), and a
   * whole-object write from one would silently erase what the other said. That
   * is also how the extension learns the view: VS Code hands this object back
   * when it restores the tab.
   */
  function remember(patch) {
    const state = vscode.getState();
    vscode.setState(Object.assign({}, state && typeof state === 'object' ? state : {}, patch));
  }

  /**
   * Whether tool cards and thinking are hidden.
   *
   * The document owns this rather than the extension: it is a reading
   * preference with nowhere else to live, it must survive a reload, and the
   * extension has no API to set a webview's state — only to ask the document
   * to flip it. The body class is the initial value because the host rendered
   * the page with it; a reload therefore comes back in the same view.
   */
  let focusView = document.body.classList.contains('focus-view');
  const restoredDocumentState = vscode.getState();
  let autoScroll = !(
    restoredDocumentState &&
    typeof restoredDocumentState === 'object' &&
    restoredDocumentState.autoScroll === false
  );

  /** The first block at or below the top of the visible transcript. */
  function firstVisibleBlock(skipHidden) {
    const top = transcript.getBoundingClientRect().top;
    const children = Array.from(transcript.children);
    let index = children.findIndex((child) => child.getBoundingClientRect().bottom > top);
    if (index < 0) return null;
    // The anchor has to be a block that survives the toggle, or its measured
    // position afterwards is zero — a hidden element has no box. Turning focus
    // on therefore walks back to the nearest prose block above the tool card it
    // was looking at.
    if (skipHidden) {
      while (index > 0 && hiddenInFocus(children[index])) index--;
      if (hiddenInFocus(children[index])) return null;
    }
    return children[index];
  }

  /** Whether the focus view hides this block. Kept beside the stylesheet's rule. */
  function hiddenInFocus(node) {
    return (
      node.classList.contains('tool') ||
      node.classList.contains('think') ||
      node.classList.contains('output-link')
    );
  }

  /**
   * Shows or hides the agent's side of the work, keeping the reader's place.
   *
   * Hiding a few hundred tool cards shrinks the scroll height in one step, and
   * the browser keeps `scrollTop` as a number — which would land the reader
   * somewhere else in the conversation. So the block at the top of the visible
   * area is measured before and realigned after; a reader pinned to the bottom
   * stays at the bottom, which is where new text arrives.
   */
  function setFocus(on) {
    if (focusView === on) return;
    const pinned = autoScroll || atBottom();
    const anchor = pinned ? null : firstVisibleBlock(on);
    const top = anchor ? anchor.getBoundingClientRect().top : 0;
    focusView = on;
    document.body.classList.toggle('focus-view', on);
    if (focusButton) focusButton.setAttribute('aria-pressed', on ? 'true' : 'false');
    if (pinned) transcript.scrollTop = transcript.scrollHeight;
    else if (anchor) transcript.scrollTop += anchor.getBoundingClientRect().top - top;
    remember({ focus: on });
  }

  /**
   * Adds a block at the end of the conversation.
   *
   * The footprint is the transcript's last entry — the conversation's foot,
   * under every turn — so a block goes in front of it rather than after it.
   * A plain append would leave the root line and the run's line reading out
   * above the conversation in a copy or a screen reader, even though the flex
   * `order` draws them last (p_0.46.26).
   */
  function addToEnd(target, node) {
    if (target === transcript && footprint && footprint.parentNode === transcript) {
      transcript.insertBefore(node, footprint);
    } else {
      target.appendChild(node);
    }
  }

  function append(node) {
    const pinned = atBottom();
    clearEmpty();
    const target = targetFor(node);
    addToEnd(target, node);
    syncRail(target);
    scrollIfPinned(pinned);
  }

  /**
   * The turn being built, or null between turns.
   *
   * A reply is one turn of the agent — the prose, the Thinking, the tool rows,
   * the approvals and the ending — folded under one header. It is a `<details>`
   * rather than a div and a handler so the chevron, the keyboard and the
   * screen reader are the browser's, and so a closed turn costs no layout.
   */
  let reply = null;

  /**
   * Whether this block begins an agent turn.
   *
   * The host's own cards — help, rules, MCP, session lines, notices — are
   * conversation furniture: a `/help` is not a reply, and an error that arrives
   * between turns belongs where it can be read without opening something.
   */
  function inReply(node) {
    if (node.classList.contains('tool') || node.classList.contains('approval')) return true;
    return (
      node.classList.contains('msg') &&
      (node.classList.contains('assistant') || node.classList.contains('result-note'))
    );
  }

  /**
   * Where a block goes: into the open reply, into a new one, or to the
   * transcript.
   *
   * Once a reply is open everything goes inside it until a user message closes
   * it — including the cards the host sends after a run ends. Routing by block
   * type alone would put such a card after the `<details>` while the turn's
   * next block went inside, and the transcript would read out of order.
   */
  function targetFor(node) {
    if (active !== mainScope) return active.container;
    if (reply) return reply.body;
    if (!inReply(node)) return active.container;
    return ensureReply().body;
  }

  /**
   * Opens a reply with its start marker.
   *
   * The dot is the rail's beginning — hollow in the colour the ending fills —
   * and the header carries the speaker's name for the whole turn, which is why
   * the rounds inside it no longer repeat it.
   */
  function ensureReply() {
    const node = el('details', 'reply');
    node.open = true;
    const head = el('summary', 'reply-head');
    head.appendChild(el('span', 'start-dot'));
    head.appendChild(el('span', 'reply-name', 'anvil'));
    head.appendChild(el('span', 'reply-hint'));
    node.appendChild(head);
    const body = el('div', 'reply-body');
    node.appendChild(body);
    // A reply the reader opened is theirs. The next turn folds the one before
    // it, and that must not undo a decision the reader just made — the same
    // rule Thinking follows.
    head.addEventListener('click', () => {
      node.dataset.pinned = '1';
    });
    addToEnd(transcript, node);
    reply = { node: node, body: body, head: head, texts: [], quote: null };
    return reply;
  }

  /**
   * Ends the turn on screen: the next user message closes it.
   *
   * A reader-pinned reply stays open; only a reply nobody touched is folded.
   */
  function closeReply() {
    if (!reply) return;
    if (reply.node.dataset.pinned !== '1') reply.node.open = false;
    reply = null;
  }

  /** Closes the newest turn at the stronger boundary of run completion. */
  function finishReply() {
    if (!reply) return;
    syncReplyQuote(reply);
    reply.node.open = false;
  }

  /**
   * The vertical line through one assistant reply.
   *
   * A reply is everything the agent puts on screen between two hands of the
   * conversation: its prose, its Thinking and every tool row. The rail belongs
   * to that whole turn, not to a run of adjacent calls, because the turn is the
   * first-level grouping a reader recognises. A user message, a notice or an
   * approval ends it. Each block draws its own full-height segment, so the
   * markup stays flat and the replay, the spawn scopes and the focus view need
   * to learn nothing.
   */
  function syncRail(container) {
    if (!container) return;
    let run = [];
    const flush = () => {
      for (let i = 0; i < run.length; i += 1) {
        const block = run[i];
        block.classList.add('rail');
        block.classList.toggle('rail-up', i > 0);
        block.classList.toggle('rail-down', i < run.length - 1);
      }
      run = [];
    };
    for (const child of container.children) {
      if (child.classList.contains('tool')) {
        run.push(child);
        continue;
      }
      // The output line is part of the reply even though it is not a step: it
      // sits between a row and whatever comes next, and the rail crosses it.
      if (child.classList.contains('output-link')) {
        run.push(child);
        continue;
      }
      if (child.classList.contains('msg') && child.classList.contains('assistant')) {
        run.push(child);
        continue;
      }
      flush();
    }
    flush();
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
    // A card from the vanished conversation cannot receive a child's events any
    // more, and its container is about to be detached with the transcript.
    spawnScopes.clear();
    backgroundCards.clear();
    backgroundTaskStates.clear();
    activeSubagents.clear();
    completedSubagentSpawns.clear();
    syncCollaborationActivity();
    active = mainScope;
    // The block about to be cleared is not this conversation's answer any more,
    // so it is ended without being announced.
    endRound(true);
    toolCards.clear();
    callFacts.clear();
    approvals.clear();
    checkpointBlocks.clear();
    forkBlocks.clear();
    if (planDock) {
      planDock.replaceChildren();
      planDock.hidden = true;
    }
    planCards.clear();
    rewindTurns.length = 0;
    rewindStates.clear();
    runSummaries.clear();
    // The turn being built belonged to the conversation that just left.
    reply = null;
    mentions = [];
    quotes = [];
    quoteCandidates = [];
    nextQuoteId = 1;
    images = [];
    renderChips();
    closePalette();
    closeActionMenu(false);
    closeModeMenu(false);
    // The history belongs to the conversation the transcript showed; a replaced
    // transcript gets its prompts back from the replay, not from memory.
    historyMath.clear(history);
    placed = null;
    // The clock and the animation described the conversation that just left.
    stopWorking();
    setStatus('');
    resetUsage();
    clearTodoHistory();
    transcript.textContent = '';
    // The node survives the clear — only the reference to it in the document
    // does not — so putting it back is what makes a cleared conversation look
    // like a new one rather than like an empty one.
    started = false;
    if (empty) transcript.appendChild(empty);
    // Emptying the transcript detached every child, this block included; it is
    // part of the conversation's own chrome rather than a turn, so it goes back
    // (p_0.46.24).
    if (footprint) transcript.appendChild(footprint);
    syncFootprint();
    growInput();
  }

  // --- messages -------------------------------------------------------------

  function quotePreview(text, limit) {
    const oneLine = String(text || '').replace(/\s+/g, ' ').trim();
    const cap = typeof limit === 'number' ? limit : 72;
    return oneLine.length > cap ? oneLine.slice(0, cap - 1) + '…' : oneLine;
  }

  function messageQuoteIcon() {
    const svg = svgNode('svg', {
      viewBox: '0 0 20 20',
      fill: 'none',
      stroke: 'currentColor',
      'stroke-width': '1.5',
      'stroke-linecap': 'round',
      'stroke-linejoin': 'round',
      'aria-hidden': 'true',
    });
    svg.appendChild(svgNode('path', { d: 'M6.8 6.2H4.9a2 2 0 0 0-2 2v2.2h3.9v3.4H3.5' }));
    svg.appendChild(svgNode('path', { d: 'M16.9 6.2H15a2 2 0 0 0-2 2v2.2h3.9v3.4h-3.3' }));
    return svg;
  }

  function registerQuote(role, text) {
    const candidate = { id: nextQuoteId++, role: role === 'user' ? 'user' : 'assistant', text: String(text || '') };
    quoteCandidates.push(candidate);
    return candidate;
  }

  function addQuote(candidate) {
    if (!candidate || candidate.text.trim() === '') return;
    if (!quotes.some((quote) => quote.id === candidate.id)) quotes.push(candidate);
    renderChips();
    closePalette();
    input.focus();
  }

  /** The part of one narrative region covered by a browser selection. */
  function selectedTextWithin(range, node) {
    if (!range.intersectsNode(node)) return '';
    const bounds = document.createRange();
    bounds.selectNodeContents(node);
    const clipped = range.cloneRange();
    if (clipped.compareBoundaryPoints(Range.START_TO_START, bounds) < 0) {
      clipped.setStart(bounds.startContainer, bounds.startOffset);
    }
    if (clipped.compareBoundaryPoints(Range.END_TO_END, bounds) > 0) {
      clipped.setEnd(bounds.endContainer, bounds.endOffset);
    }
    return clipped.toString().replace(/\u00a0/g, ' ').trim();
  }

  /**
   * Splits a selection at message boundaries. UI labels, tool rows and the
   * composer have no `data-quote-role`, so selecting across the whole window
   * still quotes only conversation prose.
   */
  function transcriptSelection() {
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0 || selection.isCollapsed) {
      return { parts: [], text: '', range: null };
    }
    const range = selection.getRangeAt(0);
    const parts = [];
    for (const node of transcript.querySelectorAll('[data-quote-role]')) {
      const text = selectedTextWithin(range, node);
      if (text === '') continue;
      parts.push({
        role: node.dataset.quoteRole === 'user' ? 'user' : 'assistant',
        text,
      });
    }
    return { parts, text: selection.toString(), range };
  }

  function closeSelectionContextMenu() {
    if (selectionContextMenu) selectionContextMenu.hidden = true;
  }

  function clearTranscriptSelection() {
    const selection = window.getSelection();
    if (selection) selection.removeAllRanges();
    selectedQuoteParts = [];
    selectedCopyText = '';
    if (selectionQuoteButton) selectionQuoteButton.hidden = true;
    closeSelectionContextMenu();
  }

  /** Adds one chip per selected message, preserving each message's speaker. */
  function quoteTranscriptSelection(parts) {
    const selected = Array.isArray(parts) ? parts : [];
    for (const part of selected) {
      if (!part || typeof part.text !== 'string' || part.text.trim() === '') continue;
      quotes.push({
        id: nextQuoteId++,
        role: part.role === 'user' ? 'user' : 'assistant',
        text: part.text.trim(),
      });
    }
    if (selected.length === 0) return;
    renderChips();
    closePalette();
    clearTranscriptSelection();
    input.focus();
  }

  function ensureSelectionQuoteButton() {
    if (selectionQuoteButton && selectionQuoteButton.isConnected) return selectionQuoteButton;
    const button = el('button', 'selection-quote');
    button.type = 'button';
    button.title = 'Quote selection (Ctrl+Shift+Q)';
    button.setAttribute('aria-label', 'Quote selected conversation text');
    button.appendChild(messageQuoteIcon());
    button.appendChild(el('span', '', 'Quote'));
    // Keep the browser selection alive until click reads the cached pieces.
    button.addEventListener('pointerdown', (event) => event.preventDefault());
    button.addEventListener('click', () => quoteTranscriptSelection(selectedQuoteParts));
    document.body.appendChild(button);
    selectionQuoteButton = button;
    return button;
  }

  function ensureSelectionContextMenu() {
    if (selectionContextMenu && selectionContextMenu.isConnected) return selectionContextMenu;
    const menu = el('div', 'selection-context-menu');
    menu.setAttribute('role', 'menu');
    menu.hidden = true;
    const quote = el('button', '', 'Quote selection');
    quote.type = 'button';
    quote.setAttribute('role', 'menuitem');
    quote.addEventListener('click', () => quoteTranscriptSelection(selectedQuoteParts));
    const copy = el('button', '', 'Copy');
    copy.type = 'button';
    copy.setAttribute('role', 'menuitem');
    copy.addEventListener('click', () => {
      if (selectedCopyText !== '') vscode.postMessage({ type: 'copy', text: selectedCopyText });
      closeSelectionContextMenu();
    });
    menu.appendChild(quote);
    menu.appendChild(copy);
    menu.addEventListener('keydown', (event) => {
      const rows = Array.from(menu.querySelectorAll('button'));
      const current = rows.indexOf(document.activeElement);
      if (event.key === 'Escape') {
        event.preventDefault();
        closeSelectionContextMenu();
        input.focus();
      } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        const delta = event.key === 'ArrowDown' ? 1 : -1;
        rows[(current + delta + rows.length) % rows.length].focus();
      }
    });
    document.body.appendChild(menu);
    selectionContextMenu = menu;
    return menu;
  }

  function paintSelectionQuoteAction() {
    selectionPaintQueued = false;
    // Focusing the custom context menu may collapse the browser range. Its
    // actions use the pieces captured when the menu opened.
    if (selectionContextMenu && !selectionContextMenu.hidden) return;
    const selected = transcriptSelection();
    selectedQuoteParts = selected.parts;
    selectedCopyText = selected.text;
    const button = ensureSelectionQuoteButton();
    if (selected.parts.length === 0 || !selected.range) {
      button.hidden = true;
      return;
    }
    const rect = selected.range.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) {
      button.hidden = true;
      return;
    }
    button.hidden = false;
    const width = button.offsetWidth || 76;
    button.style.left = Math.max(8, Math.min(window.innerWidth - width - 8, rect.left + rect.width / 2 - width / 2)) + 'px';
    button.style.top = Math.max(8, rect.top - 34) + 'px';
  }

  function scheduleSelectionQuoteAction() {
    if (selectionPaintQueued) return;
    selectionPaintQueued = true;
    requestAnimationFrame(paintSelectionQuoteAction);
  }

  function quoteAction(candidate, label) {
    const button = el('button', 'message-quote');
    button.type = 'button';
    button.title = label;
    button.setAttribute('aria-label', label);
    button.appendChild(messageQuoteIcon());
    button.addEventListener('click', (event) => {
      event.preventDefault();
      event.stopPropagation();
      addQuote(candidate);
    });
    return button;
  }

  function syncReplyQuote(state) {
    if (!state || !state.head) return;
    const text = state.texts.join('\n\n').trim();
    if (text === '') return;
    if (!state.quote) state.quote = registerQuote('assistant', text);
    else state.quote.text = text;
    if (!state.head.querySelector('.message-quote')) {
      state.head.appendChild(quoteAction(state.quote, 'Quote this reply'));
    }
  }

  function addUser(text, sentMentions, sentImages, sentQuotes) {
    // This message opens a new turn: the reply above it folds, and the message
    // itself is never part of a reply.
    if (active === mainScope) closeReply();
    const wrap = el('div', 'msg user');
    const candidate = registerQuote('user', text);
    const who = el('div', 'who');
    who.appendChild(el('span', null, 'you'));
    who.appendChild(quoteAction(candidate, 'Quote this message'));
    wrap.appendChild(who);
    const body = el('div', 'body', text);
    body.dataset.quoteRole = 'user';
    wrap.appendChild(body);
    // What travelled with the prompt is echoed under it: a file read on the
    // user's behalf is part of the turn, and a transcript that omits it would
    // misreport what the model was asked.
    if (sentMentions && sentMentions.length) {
      const refs = el('div', 'refs');
      for (const mention of sentMentions) refs.appendChild(el('span', 'ref', '@' + mention));
      wrap.appendChild(refs);
    }
    if (sentQuotes && sentQuotes.length) {
      const refs = wrap.querySelector('.refs') || el('div', 'refs');
      for (const quote of sentQuotes) {
        refs.appendChild(
          el('span', 'ref quote-ref', '↳ ' + (quote.role === 'user' ? 'you' : 'anvil') + ': ' + quotePreview(quote.text, 54)),
        );
      }
      if (!refs.parentNode) wrap.appendChild(refs);
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
    // Only the window's transcript gets a rewind: a spawned child's prompt is
    // not a turn of this conversation, and its runs are not boundary candidates.
    if (active === mainScope) rewindTurns.push({ wrap: wrap, runId: '', menu: null });
    endRound();
  }

  // --- announcements --------------------------------------------------------

  /**
   * How often a streaming update may reach the polite region.
   *
   * A token arrives every few milliseconds. A screen reader asked to read each
   * one is unusable, and one that re-reads the whole region on every change is
   * worse. Five hundred milliseconds is long enough to be a phrase rather than
   * a syllable, and short enough that progress still reads as progress.
   */
  const ANNOUNCE_INTERVAL_MS = 500;
  /** When the polite region last changed, for the throttle above. */
  let announcedAt = 0;

  /**
   * Says something politely, once, when the thing happened.
   *
   * Each call appends its own node rather than replacing the region's text: a
   * live region announces the content that changed, so replacing `textContent`
   * would ask the reader to hear everything already said a second time. Old
   * nodes are dropped from the front so the region does not grow for the life
   * of the session.
   */
  function announce(text) {
    if (!srStatus || !text || replaying) return;
    srStatus.appendChild(el('div', null, text));
    while (srStatus.childNodes.length > 4) srStatus.removeChild(srStatus.firstChild);
  }

  /** The throttled form, for text that arrives while a turn is still running. */
  function announceProgress(text) {
    const now = Date.now();
    if (now - announcedAt < ANNOUNCE_INTERVAL_MS) return;
    announcedAt = now;
    announce(text);
  }

  /**
   * Says something urgently: an approval request, or an error. Deliberately not
   * throttled — by the time one of these arrives the reader needs to stop doing
   * what they were doing.
   */
  function announceAlert(text) {
    if (!srAlert || !text || replaying) return;
    srAlert.textContent = text;
  }

  /**
   * Ends the assistant round, so the next reasoning or text starts a new bubble
   * rather than appending to one the reader has already read past.
   *
   * `silent` ends it without announcing the block, for the one caller whose
   * block is about to be wiped from the screen.
   */
  function endRound(silent) {
    const round = currentRound();
    if (round && round.think && !round.thinkPinned) round.think.open = false;
    // Every ending gets the action — a text block, a tool call, the run's end —
    // because the bubble is complete the moment it stops growing, and a Copy
    // that only appeared after the whole turn would refuse to copy the answer
    // the reader is looking at while the agent works on the next step.
    if (round && round.text.trim() !== '') addCopy(round.wrap, round.text);
    if (round && round.text.trim() !== '' && active === mainScope && reply && round.quoted !== true) {
      reply.texts.push(round.text);
      round.quoted = true;
    }
    // The block is finished, so this is the first moment it can be said as a
    // whole. A child agent's text is not this conversation's answer and is not
    // announced; its card in the transcript is where it is read.
    if (!silent && round && round.body && active === mainScope) {
      const said = round.body.textContent.trim();
      if (said !== '') announce(said);
    }
    setCurrentRound(null);
  }

  /**
   * The copy action on a finished answer.
   *
   * The text is the raw Markdown that arrived, not the rendered prose: what a
   * reader wants to paste elsewhere is the source, and a copy that lost its
   * fences and links would be a second rendering rather than a copy.
   *
   * It goes through the host because the clipboard is the editor's: a webview's
   * `navigator.clipboard` access depends on permissions this document does not
   * control, and a Copy button that sometimes silently does nothing is worse
   * than one that takes a round trip.
   */
  function addCopy(wrap, text) {
    const button = el('button', 'copy', 'Copy');
    button.type = 'button';
    button.title = 'Copy this answer';
    button.addEventListener('click', () => {
      vscode.postMessage({ type: 'copy', text });
      button.textContent = 'Copied';
      button.disabled = true;
      setTimeout(() => {
        button.textContent = 'Copy';
        button.disabled = false;
      }, 1200);
    });
    wrap.appendChild(button);
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
    if (currentRound()) return currentRound();
    const wrap = el('div', 'msg assistant');
    wrap.appendChild(el('div', 'who', 'anvil'));
    const body = el('div', 'body md');
    body.dataset.quoteRole = 'assistant';
    wrap.appendChild(body);
    const state = {
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
    setCurrentRound(state);
    append(wrap);
    return state;
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
  let mermaidConfigured = false;
  let mermaidSequence = 0;

  function parseMarkdown(text) {
    if (!markdown) markdown = self.anvilMarkdown;
    return markdown.parse(text);
  }

  /** Whether two block objects would be drawn by the same kind of node. */
  function sameBlockShape(a, b) {
    if (!a || a.type !== b.type) return false;
    if (a.type === 'heading') return a.level === b.level;
    if (a.type === 'list') return a.ordered === b.ordered;
    if (a.type === 'code') {
      if (isMermaidBlock(a) || isMermaidBlock(b)) {
        return isMermaidBlock(a) && isMermaidBlock(b) && a.closed === b.closed && a.text === b.text;
      }
      return (a.lang || '') === (b.lang || '');
    }
    // A formula is redrawn when its source changes rather than reconciled: its
    // inner nodes belong to the renderer, not to this file.
    if (a.type === 'math') return a.text === b.text;
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
        if (isMermaidBlock(block) && block.closed === true) return mermaidNode(block.text || '');
        const node = el('pre', 'md-code');
        node.tabIndex = 0;
        node.setAttribute('aria-label', block.lang ? block.lang + ' code block' : 'Code block');
        autoHideScrollbar(node);
        const code = el('code');
        if (block.lang) code.dataset.lang = block.lang;
        fillCode(code, block.text || '', block.lang || '', block.closed === true);
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
      case 'math': {
        const node = mathNode(block.text || '', true);
        node.classList.add('md-math-block');
        return node;
      }
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
        if (isMermaidBlock(block)) break;
        // The one place a growing leaf is large enough for how it is written to
        // matter: a 50 KB code block is the open tail of most long answers, and
        // setting `textContent` on every frame re-lays out all of it.
        fillCode(node.firstChild, block.text || '', block.lang || '', block.closed === true);
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

  const shikiAliases = {
    c: 'c',
    h: 'c',
    cc: 'cpp',
    cpp: 'cpp',
    cxx: 'cpp',
    'c++': 'cpp',
    hpp: 'cpp',
    cs: 'csharp',
    csharp: 'csharp',
    js: 'javascript',
    javascript: 'javascript',
    jsx: 'jsx',
    ts: 'typescript',
    typescript: 'typescript',
    tsx: 'tsx',
    py: 'python',
    python: 'python',
    rb: 'ruby',
    ruby: 'ruby',
    rs: 'rust',
    rust: 'rust',
    go: 'go',
    java: 'java',
    kotlin: 'kotlin',
    swift: 'swift',
    php: 'php',
    sh: 'shellscript',
    shell: 'shellscript',
    bash: 'shellscript',
    shellscript: 'shellscript',
    ps1: 'powershell',
    powershell: 'powershell',
    json: 'json',
    jsonc: 'jsonc',
    yaml: 'yaml',
    yml: 'yaml',
    toml: 'toml',
    xml: 'xml',
    html: 'html',
    css: 'css',
    scss: 'scss',
    less: 'less',
    sql: 'sql',
    md: 'markdown',
    markdown: 'markdown',
    mdx: 'mdx',
    diff: 'diff',
    dockerfile: 'dockerfile',
    docker: 'dockerfile',
    cmake: 'cmake',
    objc: 'objective-c',
    'objective-c': 'objective-c',
    lua: 'lua',
    perl: 'perl',
    r: 'r',
    zig: 'zig',
    proto: 'protobuf',
    protobuf: 'protobuf',
    graphql: 'graphql',
  };
  let shikiHighlighter = null;
  let shikiHighlighterPromise = null;
  let codeHighlightSequence = 0;
  const shikiLanguageLoads = new Map();

  function inferShikiLanguage(text) {
    const source = String(text || '');
    if (
      /\benum\s+class\b|\bvirtual\b|(?:^|\n)\s*(?:public|private|protected)\s*:|\bnamespace\s+\w+|::|template\s*</.test(
        source,
      )
    ) {
      return 'cpp';
    }
    if (/^\s*#\s*include\b/m.test(source)) return /\b(?:class|constexpr|nullptr|std::)\b/.test(source) ? 'cpp' : 'c';
    if (/^\s*(?:package\s+\w+|func\s+\w+\s*\()/m.test(source)) return 'go';
    if (/^\s*(?:def|class)\s+\w+.*:\s*$/m.test(source)) return 'python';
    if (/\bfn\s+\w+\s*\(|\blet\s+mut\b|\bimpl(?:<[^>]+>)?\s+\w+/m.test(source)) return 'rust';
    if (/^\s*(?:interface|type)\s+\w+|:\s*(?:string|number|boolean)\b/m.test(source)) return 'typescript';
    if (/^\s*(?:const|let|var)\s+\w+\s*=|=>|function\s+\w+\s*\(/m.test(source)) return 'javascript';
    if (/^\s*(?:\{|\[)/.test(source)) {
      try {
        JSON.parse(source);
        return 'json';
      } catch {
        // A brace alone is not enough evidence to colour arbitrary output.
      }
    }
    return '';
  }

  function resolveShikiLanguage(language, text) {
    const requested = String(language || '').trim().toLowerCase();
    if (shikiAliases[requested]) return shikiAliases[requested];
    if (requested === '' || requested === 'text' || requested === 'plaintext' || requested === 'plain') {
      return inferShikiLanguage(text);
    }
    return '';
  }

  function getShikiHighlighter() {
    if (shikiHighlighter) return Promise.resolve(shikiHighlighter);
    if (shikiHighlighterPromise) return shikiHighlighterPromise;
    const renderer = self.anvilShiki;
    if (!renderer || typeof renderer.createHighlighter !== 'function') return Promise.resolve(null);
    shikiHighlighterPromise = renderer
      .createHighlighter({ themes: ['dark-plus', 'light-plus'], langs: [] })
      .then((highlighter) => {
        shikiHighlighter = highlighter;
        return highlighter;
      })
      .catch(() => null);
    return shikiHighlighterPromise;
  }

  function loadShikiLanguage(highlighter, language) {
    if (highlighter.getLoadedLanguages().includes(language)) return Promise.resolve();
    if (shikiLanguageLoads.has(language)) return shikiLanguageLoads.get(language);
    const loading = Promise.resolve(highlighter.loadLanguage(language)).catch((error) => {
      shikiLanguageLoads.delete(language);
      throw error;
    });
    shikiLanguageLoads.set(language, loading);
    return loading;
  }

  function paintShikiTokens(code, result) {
    const fragment = document.createDocumentFragment();
    const lines = Array.isArray(result && result.tokens) ? result.tokens : [];
    for (let lineIndex = 0; lineIndex < lines.length; lineIndex++) {
      for (const token of lines[lineIndex]) {
        const span = el('span', 'shiki-token', token.content);
        if (token.color) span.style.color = token.color;
        const style = Number(token.fontStyle) || 0;
        if ((style & 1) !== 0) span.style.fontStyle = 'italic';
        if ((style & 2) !== 0) span.style.fontWeight = 'bold';
        if ((style & 4) !== 0) span.style.textDecoration = 'underline';
        fragment.appendChild(span);
      }
      if (lineIndex < lines.length - 1) fragment.appendChild(document.createTextNode('\n'));
    }
    code.replaceChildren(fragment);
  }

  async function highlightClosedCode(code, text, language, generation) {
    const lang = resolveShikiLanguage(language, text);
    if (!lang) return;
    const highlighter = await getShikiHighlighter();
    if (!highlighter) return;
    await loadShikiLanguage(highlighter, lang);
    const theme = document.body.classList.contains('vscode-light') ||
      document.body.classList.contains('vscode-high-contrast-light')
      ? 'light-plus'
      : 'dark-plus';
    const result = highlighter.codeToTokens(text, { lang, theme });
    if (!code.isConnected || code.dataset.highlightGeneration !== generation) return;
    paintShikiTokens(code, result);
  }

  /** Keeps streaming cheap, then applies offline TextMate tokens once closed. */
  function fillCode(code, text, language, closed) {
    const generation = String(++codeHighlightSequence);
    code.dataset.highlightGeneration = generation;
    setText(code, text);
    if (!closed) return;
    highlightClosedCode(code, text, language, generation).catch(() => undefined);
  }

  function isMermaidBlock(block) {
    if (!block || block.type !== 'code') return false;
    const lang = String(block.lang || '').trim().toLowerCase();
    if (lang === 'mermaid') return true;
    if (lang !== '' && lang !== 'text' && lang !== 'plaintext') return false;
    return /^(?:flowchart|graph|sequenceDiagram|classDiagram|stateDiagram(?:-v2)?|erDiagram|journey|gantt|pie|quadrantChart|requirementDiagram|gitGraph|C4(?:Context|Container|Component|Dynamic|Deployment)|mindmap|timeline|sankey-beta|xychart-beta|block-beta|architecture-beta|packet-beta|kanban|radar-beta|treemap-beta)\b/.test(
      String(block.text || '').trimStart(),
    );
  }

  function classMemberKind(text) {
    const value = String(text || '').trim();
    if (value === '' || value.startsWith('<<') || value.startsWith('%%')) return '';
    return value.includes('(') && value.includes(')') ? 'method' : 'member';
  }

  function compactClassBody(lines) {
    const kept = { member: 0, method: 0 };
    const omitted = { member: false, method: false };
    const out = [];
    for (const line of lines) {
      const kind = classMemberKind(line);
      if (!kind) {
        out.push(line);
        continue;
      }
      if (kept[kind] < 3) {
        kept[kind]++;
        out.push(line);
        continue;
      }
      if (omitted[kind]) continue;
      omitted[kind] = true;
      const indent = /^\s*/.exec(line)?.[0] || '  ';
      out.push(indent + (kind === 'method' ? '+...()' : '...'));
    }
    return out;
  }

  function compactClassDiagramSource(source) {
    if (!/^\s*classDiagram\b/m.test(source)) return source;
    const lines = String(source || '').split(/\r?\n/);
    const out = [];
    let body = null;
    const colonCounts = new Map();
    const colonOmitted = new Set();
    for (const line of lines) {
      if (body) {
        if (/^\s*}\s*$/.test(line)) {
          out.push(...compactClassBody(body), line);
          body = null;
        } else {
          body.push(line);
        }
        continue;
      }
      if (/^\s*class\s+.+\{\s*$/.test(line)) {
        out.push(line);
        body = [];
        continue;
      }
      const colon = /^(\s*)([A-Za-z_][\w.-]*)\s*:\s*(.+)$/.exec(line);
      const kind = colon ? classMemberKind(colon[3]) : '';
      if (!colon || !kind) {
        out.push(line);
        continue;
      }
      const key = colon[2] + ':' + kind;
      const count = colonCounts.get(key) || 0;
      colonCounts.set(key, count + 1);
      if (count < 3) {
        out.push(line);
      } else if (!colonOmitted.has(key)) {
        colonOmitted.add(key);
        out.push(`${colon[1]}${colon[2]} : ${kind === 'method' ? '+...()' : '...'}`);
      }
    }
    if (body) out.push(...compactClassBody(body));
    return out.join('\n');
  }

  /**
   * Keeps native scrollbars out of the reading surface until they are useful.
   * Scrolling, keyboard navigation, or approaching the right/bottom edge shows
   * the bar briefly; leaving it idle removes the native scrollbar dimensions.
   */
  function autoHideScrollbar(node) {
    let timer = null;
    const hide = () => {
      timer = null;
      node.classList.remove('scroll-active');
    };
    const show = () => {
      node.classList.add('scroll-active');
      if (timer !== null) clearTimeout(timer);
      timer = setTimeout(hide, 850);
    };
    node.addEventListener('scroll', show, { passive: true });
    node.addEventListener(
      'pointermove',
      (event) => {
        const box = node.getBoundingClientRect();
        if (box.right - event.clientX <= 18 || box.bottom - event.clientY <= 18) show();
      },
      { passive: true },
    );
    node.addEventListener('keydown', show);
    node.addEventListener('blur', hide);
  }

  /**
   * Draws a closed Mermaid fence. The generated SVG is parsed as inert XML and
   * sanitized before insertion; model-authored markup never enters the page.
   */
  function mermaidNode(source) {
    const wrap = el('div', 'md-mermaid-wrap');
    const figure = el('figure', 'md-mermaid');
    figure.tabIndex = 0;
    figure.setAttribute('aria-label', 'Diagram preview');
    autoHideScrollbar(figure);
    wrap.appendChild(figure);
    const fallback = el('pre', 'md-code md-mermaid-source');
    const code = el('code');
    code.dataset.lang = 'mermaid';
    setText(code, source);
    fallback.appendChild(code);
    figure.appendChild(fallback);

    const renderer = self.mermaid;
    if (!renderer || typeof renderer.render !== 'function') return wrap;
    if (!mermaidConfigured) {
      const light = document.body.classList.contains('vscode-light');
      renderer.initialize({
        startOnLoad: false,
        securityLevel: 'strict',
        suppressErrorRendering: true,
        // Match VS Code Markdown's standard Mermaid themes instead of
        // overriding a partial set of variables. A partial custom theme leaves
        // diagram-specific colours (notably sequence actors and notes) wrong.
        theme: light ? 'default' : 'dark',
        flowchart: {
          htmlLabels: true,
          wrappingWidth: 260,
          nodeSpacing: 48,
          rankSpacing: 64,
        },
      });
      mermaidConfigured = true;
    }

    const id = 'anvil-mermaid-' + ++mermaidSequence;
    const previewSource = compactClassDiagramSource(source);
    const replayBlock = replayPinsBottom ? replayGeneration : 0;
    if (replayBlock !== 0) replayAsyncBlocks++;
    Promise.resolve(renderer.render(id, previewSource))
      .then((result) => {
        const svg = result && typeof result.svg === 'string' ? result.svg : '';
        if (svg === '' || !figure.isConnected) return;
        const diagram = safeMermaidSvg(svg);
        if (!diagram) throw new Error('Mermaid returned invalid SVG');
        const pinned = replayPinsBottom || atBottom();
        const surface = el('div', 'md-mermaid-surface');
        const view = el('button', 'md-mermaid-view', 'View');
        view.type = 'button';
        view.setAttribute('aria-label', 'View Mermaid diagram in large view');
        view.title = 'View diagram';
        let detailPromise = null;
        view.addEventListener('click', async () => {
          if (previewSource === source) {
            openMermaidDiagram(diagram, source);
            return;
          }
          if (!detailPromise) {
            detailPromise = Promise.resolve(renderer.render(id + '-detail', source)).then((detailResult) => {
              const detailSvg = detailResult && typeof detailResult.svg === 'string' ? detailResult.svg : '';
              const detail = safeMermaidSvg(detailSvg);
              if (!detail) throw new Error('Mermaid returned invalid detail SVG');
              normalizeMermaidContrast(detail);
              return detail;
            });
          }
          view.disabled = true;
          try {
            openMermaidDiagram(await detailPromise, source);
          } catch {
            openMermaidDiagram(diagram, source);
          } finally {
            view.disabled = false;
          }
        });
        surface.appendChild(diagram);
        figure.replaceChildren(surface);
        wrap.appendChild(view);
        normalizeMermaidContrast(diagram);
        scrollIfPinned(pinned);
      })
      .catch(() => {
        figure.classList.add('failed');
        figure.appendChild(el('div', 'md-mermaid-error', 'Diagram could not be rendered.'));
      })
      .finally(() => {
        if (replayBlock !== 0 && replayBlock === replayGeneration) {
          replayAsyncBlocks = Math.max(0, replayAsyncBlocks - 1);
        }
        settleReplayAtBottom();
      });
    return wrap;
  }

  /**
   * Parses Mermaid's output as inert XML and removes every active or external
   * surface before the SVG enters the document. The model supplies diagram
   * text, not markup; Mermaid supplies the markup and strict mode escapes the
   * labels, but this boundary remains defensive in case that contract changes.
   */
  function safeMermaidSvg(source) {
    // VS Code's Markdown renderer inserts Mermaid's string as HTML. Parsing it
    // as XML is stricter in the wrong place: flowchart HTML labels may contain
    // named HTML entities that are valid in VS Code but reject the whole SVG
    // as XML. HTML parsing preserves those labels; the walk below is the
    // security boundary before the SVG enters this document.
    const parsed = new DOMParser().parseFromString(source, 'text/html');
    const root = parsed.querySelector('svg');
    if (!root) return null;
    for (const node of root.querySelectorAll('script, iframe, object, embed, image')) node.remove();
    for (const node of [root, ...root.querySelectorAll('*')]) {
      for (const attribute of Array.from(node.attributes)) {
        const name = attribute.name.toLowerCase();
        const value = attribute.value.trim();
        if (
          name.startsWith('on') ||
          (/^(?:href|xlink:href)$/.test(name) && !value.startsWith('#')) ||
          /javascript:/i.test(value) ||
          (name === 'style' && /(?:url\s*\(|expression\s*\()/i.test(value))
        ) {
          node.removeAttribute(attribute.name);
        }
      }
    }
    const diagram = document.importNode(root, true);
    diagram.classList.add('md-mermaid-svg');
    diagram.setAttribute('role', 'img');
    diagram.setAttribute('aria-label', 'Mermaid diagram');
    return diagram;
  }

  /**
   * Applies the VS Code palette as inline-important styles. Mermaid emits a
   * style element inside each SVG, so document-level CSS can lose depending on
   * the diagram and bundle version; writing the final presentation here keeps
   * actor labels and messages consistently readable.
   *
   * Some sequence renderers emit only the number text. When there is no circle
   * for it, draw one from the text's measured box after the SVG is connected.
   */
  function setMermaidNaturalSize(diagram) {
    const values = String(diagram.getAttribute('viewBox') || '')
      .trim()
      .split(/[\s,]+/)
      .map(Number);
    const width = values.length === 4 ? values[2] : 0;
    if (Number.isFinite(width) && width > 0) {
      diagram.style.setProperty('--anvil-mermaid-natural-width', Math.ceil(width) + 'px');
    }
  }

  function normalizeMermaidContrast(diagram) {
    setMermaidNaturalSize(diagram);
    const foreground = document.body.classList.contains('vscode-light')
      ? '#303030'
      : '#d0d0d0';
    const nodeFill = 'var(--vscode-editorWidget-background, var(--vscode-editor-background))';
    const nodeStroke = 'var(--vscode-focusBorder)';
    for (const shape of diagram.querySelectorAll(
      '.node rect, .node polygon, .node path, .node circle, .node ellipse',
    )) {
      shape.style.setProperty('fill', nodeFill, 'important');
      shape.style.setProperty('stroke', nodeStroke, 'important');
    }
    for (const shape of diagram.querySelectorAll('.cluster rect, .cluster polygon, .cluster path')) {
      shape.style.setProperty('fill', nodeFill, 'important');
      shape.style.setProperty(
        'stroke',
        'var(--vscode-panel-border, var(--vscode-focusBorder))',
        'important',
      );
    }
    const relationColor = 'var(--vscode-descriptionForeground)';
    for (const path of diagram.querySelectorAll(
      '.relationshipLine, .relation, .edgePath path, path.flowchart-link',
    )) {
      path.style.setProperty('fill', 'none', 'important');
      path.style.setProperty('stroke', relationColor, 'important');
    }
    for (const marker of diagram.querySelectorAll('marker path, marker polygon, marker circle')) {
      marker.style.setProperty('fill', relationColor, 'important');
      marker.style.setProperty('stroke', relationColor, 'important');
    }
    diagram.style.setProperty('color', foreground, 'important');
    diagram.style.setProperty('-webkit-text-fill-color', foreground, 'important');
    for (const node of diagram.querySelectorAll('*')) {
      node.style.setProperty('color', foreground, 'important');
      node.style.setProperty('-webkit-text-fill-color', foreground, 'important');
    }
    for (const node of diagram.querySelectorAll('text, tspan')) {
      node.style.setProperty('fill', foreground, 'important');
      node.style.setProperty('stroke', 'none', 'important');
      node.style.setProperty('opacity', '1', 'important');
    }

    const numbers = Array.from(diagram.querySelectorAll('.sequenceNumber'));
    const dots = Array.from(diagram.querySelectorAll('[id$="-sequencenumber" i]'));
    const styleDot = (dot) => {
      dot.style.setProperty('fill', '#d8b7b2', 'important');
      dot.style.setProperty('stroke', '#f0d8d4', 'important');
      dot.style.setProperty('stroke-width', '1.5px', 'important');
    };
    dots.forEach(styleDot);
    for (const number of numbers) {
      number.style.setProperty('color', '#292929', 'important');
      number.style.setProperty('fill', '#292929', 'important');
      number.style.setProperty('-webkit-text-fill-color', '#292929', 'important');
      number.style.setProperty('opacity', '1', 'important');
    }
    for (let index = dots.length; index < numbers.length; index++) {
      const number = numbers[index];
      if (!(number instanceof SVGGraphicsElement) || !number.parentNode) continue;
      let box;
      try {
        box = number.getBBox();
      } catch {
        continue;
      }
      const dot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
      dot.classList.add('anvil-sequence-number-dot');
      dot.setAttribute('cx', String(box.x + box.width / 2));
      dot.setAttribute('cy', String(box.y + box.height / 2));
      dot.setAttribute('r', String(Math.max(8, Math.max(box.width, box.height) / 2 + 4)));
      styleDot(dot);
      number.parentNode.insertBefore(dot, number);
    }
  }

  function mermaidToolbarButton(label, title, paths) {
    const button = el('button', 'ghost md-mermaid-toolbar-button');
    button.type = 'button';
    button.setAttribute('aria-label', label);
    button.title = title;
    const icon = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    icon.classList.add('md-mermaid-toolbar-icon');
    icon.setAttribute('viewBox', '0 0 20 20');
    icon.setAttribute('aria-hidden', 'true');
    for (const pathData of paths) {
      const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
      path.setAttribute('d', pathData);
      icon.appendChild(path);
    }
    button.appendChild(icon);
    return button;
  }

  /** Opens a diagram in a scrollable modal with explicit zoom controls. */
  function openMermaidDiagram(diagram, source) {
    const dialog = el('dialog', 'md-mermaid-dialog');
    const panel = el('div', 'md-mermaid-dialog-panel');
    const toolbar = el('div', 'md-mermaid-toolbar');
    toolbar.appendChild(el('span', 'title', 'Diagram'));
    const pan = mermaidToolbarButton('Pan diagram', 'Pan diagram', [
      'M10 2.5v15M2.5 10h15M10 2.5 7.5 5M10 2.5 12.5 5M17.5 10 15 7.5M17.5 10 15 12.5M10 17.5 7.5 15M10 17.5 12.5 15M2.5 10 5 7.5M2.5 10 5 12.5',
    ]);
    const zoomOut = mermaidToolbarButton('Zoom out', 'Zoom out', [
      'M8.5 3.5a5 5 0 1 0 0 10 5 5 0 0 0 0-10M12.2 12.2 16.5 16.5M6 8.5h5',
    ]);
    const zoomIn = mermaidToolbarButton('Zoom in', 'Zoom in', [
      'M8.5 3.5a5 5 0 1 0 0 10 5 5 0 0 0 0-10M12.2 12.2 16.5 16.5M6 8.5h5M8.5 6v5',
    ]);
    const fit = mermaidToolbarButton('Fit diagram', 'Fit diagram', [
      'M7 3.5H3.5V7M13 3.5h3.5V7M7 16.5H3.5V13M13 16.5h3.5V13',
    ]);
    const close = mermaidToolbarButton('Close diagram', 'Close diagram', ['M5 5l10 10M15 5 5 15']);
    close.classList.add('close');
    pan.setAttribute('aria-pressed', 'false');
    const controls = el('div', 'md-mermaid-toolbar-controls');
    controls.appendChild(pan);
    controls.appendChild(zoomOut);
    controls.appendChild(zoomIn);
    controls.appendChild(fit);
    controls.appendChild(close);
    toolbar.appendChild(controls);

    const viewport = el('div', 'md-mermaid-viewport');
    const large = diagram.cloneNode(true);
    large.classList.add('md-mermaid-large');
    large.setAttribute('aria-label', 'Mermaid diagram');
    large.dataset.source = source;
    viewport.appendChild(large);
    panel.appendChild(toolbar);
    panel.appendChild(viewport);
    dialog.appendChild(panel);
    document.body.appendChild(dialog);

    let zoom = 1.5;
    let panEnabled = false;
    let drag = null;
    const applyZoom = () => {
      large.style.width = Math.round(zoom * 100) + '%';
      zoomOut.disabled = zoom <= 0.5;
      zoomIn.disabled = zoom >= 3;
    };
    pan.addEventListener('click', () => {
      panEnabled = !panEnabled;
      pan.setAttribute('aria-pressed', panEnabled ? 'true' : 'false');
      viewport.classList.toggle('panning', panEnabled);
    });
    viewport.addEventListener('pointerdown', (event) => {
      if (!panEnabled || event.button !== 0) return;
      drag = {
        id: event.pointerId,
        x: event.clientX,
        y: event.clientY,
        left: viewport.scrollLeft,
        top: viewport.scrollTop,
      };
      viewport.setPointerCapture(event.pointerId);
      viewport.classList.add('dragging');
      event.preventDefault();
    });
    viewport.addEventListener('pointermove', (event) => {
      if (!drag || event.pointerId !== drag.id) return;
      viewport.scrollLeft = drag.left - (event.clientX - drag.x);
      viewport.scrollTop = drag.top - (event.clientY - drag.y);
    });
    const stopDragging = (event) => {
      if (!drag || event.pointerId !== drag.id) return;
      drag = null;
      viewport.classList.remove('dragging');
      if (viewport.hasPointerCapture(event.pointerId)) viewport.releasePointerCapture(event.pointerId);
    };
    viewport.addEventListener('pointerup', stopDragging);
    viewport.addEventListener('pointercancel', stopDragging);
    zoomOut.addEventListener('click', () => {
      zoom = Math.max(0.5, zoom - 0.25);
      applyZoom();
    });
    fit.addEventListener('click', () => {
      zoom = 1;
      applyZoom();
      viewport.scrollTo(0, 0);
    });
    zoomIn.addEventListener('click', () => {
      zoom = Math.min(3, zoom + 0.25);
      applyZoom();
    });
    const dismiss = () => {
      if (dialog.open && typeof dialog.close === 'function') dialog.close();
      else dialog.remove();
    };
    close.addEventListener('click', dismiss);
    dialog.addEventListener('close', () => dialog.remove());
    dialog.addEventListener('click', (event) => {
      if (event.target === dialog) dismiss();
    });
    applyZoom();
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
    close.focus();
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
    if (span.type === 'math') return shape.value === span.value && shape.display === span.display;
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
      case 'math':
        node = mathNode(span.value, span.display === true);
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
   * A formula, drawn as MathML by the vendored renderer.
   *
   * `render` builds nodes instead of returning markup, which is the property
   * the transcript depends on: a formula is model output, and nothing here
   * assigns HTML from it. `trust: false` removes the commands that could write
   * a link or a class (`\href`, `\htmlClass`), and `throwOnError` hands a
   * malformed formula back to this file so it can be shown as the characters
   * it is rather than as the renderer's own red error box.
   *
   * MathML is what makes the page's CSP stay as it is. KaTeX's HTML output
   * writes inline style attributes on nearly every span, and a CSP without
   * `'unsafe-inline'` blocks those; MathML writes none, needs no stylesheet,
   * and needs no font files — the browser already draws mathematics. The price
   * is a look that follows the platform's math font rather than KaTeX's.
   *
   * The renderer is a page script, not an import: `media/` is plain scripts.
   * If it is missing, the formula still appears — as its source.
   */
  function mathNode(value, display) {
    // A span either way: a display formula can sit inside a list item or a
    // sentence, where a `div` would end the line's element early. The class
    // that makes it a block does the layout.
    const node = el('span', display ? 'md-math md-math-display' : 'md-math');
    const renderer = self.katex;
    if (renderer && typeof renderer.render === 'function') {
      // Built in a detached element: `render` appends as it goes, and a formula
      // it refuses halfway would leave a partial drawing behind.
      const drawing = el('span', 'md-math-drawing');
      try {
        renderer.render(value, drawing, {
          displayMode: display,
          output: 'mathml',
          throwOnError: true,
          trust: false,
          strict: false,
        });
        node.appendChild(drawing);
        return node;
      } catch {
        // Falls through to the source below, which is what the model wrote and
        // what a reader can still use.
      }
    }
    node.classList.add('md-math-source');
    node.textContent = display ? '$$' + value + '$$' : '$' + value + '$';
    return node;
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
    // An error is one of the two things that interrupts rather than waits.
    announceAlert(text);
    endRound();
  }

  /**
   * A line that is neither the user's nor the model's: something the loop put
   * into the conversation on its own account, replayed.
   *
   * It exists because the store keeps those records under the user's role, and a
   * transcript that showed them as "you" would report words the user never
   * typed. The title says where it came from, since the text alone reads like a
   * prompt.
   */
  function addNote(text) {
    const wrap = el('div', 'msg note');
    wrap.title = 'The loop put this into the conversation; you did not type it.';
    wrap.appendChild(el('div', 'body', text));
    append(wrap);
    endRound();
  }

  /**
   * The boundary a compaction leaves in the conversation.
   *
   * A live compaction knows what it did — how many messages it summarized and
   * what the estimate fell from and to — and says so. A replayed one comes from
   * a stored summary record that keeps none of those numbers, so it says only
   * that the boundary is there. Both are the same row in the same place, which
   * is what makes the replay readable as the conversation it is.
   */
  function addCompact(info) {
    const parts = ['context compacted'];
    const summarized = info ? count(info.summarized) : '';
    if (summarized) {
      parts.push(summarized + (summarized === '1' ? ' message summarized' : ' messages summarized'));
    }
    const before = info ? count(info.before) : '';
    const after = info ? count(info.after) : '';
    if (before && after) parts.push(before + ' → ' + after);
    const wrap = el('div', 'msg compact');
    wrap.title = 'Earlier turns were summarized so the conversation fits its window.';
    wrap.appendChild(el('div', 'body', parts.join(' · ')));
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
   * The name a reader knows the call by. The SDK names a tool for its
   * implementation (`read_file`); the transcript names it for the act. The map
   * is display only: the host, the rules and the wire keep the SDK's names.
   */
  const TOOL_TITLES = {
    read_file: 'Read',
    read_image: 'Read image',
    write_file: 'Write',
    edit_file: 'Edit',
    apply_patch: 'Patch',
    grep: 'Grep',
    glob: 'Find',
    semantic_search: 'Search',
    shell: 'Bash',
    web_search: 'Web search',
    fetch_url: 'Fetch',
    list_skills: 'Skills',
    read_skill: 'Skill',
    task_list: 'Tasks',
    task_output: 'Task output',
    task_cancel: 'Stop task',
    spawn_agent: 'Agent',
    build: 'Build',
    diagnostics: 'Diagnostics',
    definition: 'Definition',
    references: 'References',
    hover: 'Hover',
    rename_symbol: 'Rename',
    git_status: 'Git status',
    git_diff: 'Git diff',
    git_log: 'Git log',
    memory_list: 'Memory',
    memory_read: 'Recall',
    memory_search: 'Recall',
    memory_write: 'Remember',
    memory_delete: 'Forget',
    run_workflow: 'Workflow',
    plugin_ping: 'Plugin',
  };

  function toolTitle(name) {
    if (typeof name !== 'string' || name === '') return 'Tool';
    if (TOOL_TITLES[name]) return TOOL_TITLES[name];
    // An unknown name is still words rather than an identifier: a new tool
    // reads as "Some tool", not as `some_new_tool`.
    const words = name.replace(/_/g, ' ');
    return words.charAt(0).toUpperCase() + words.slice(1);
  }

  /** A compact activity for the hammer line, derived from the call being made. */
  function toolActivity(name) {
    if (['read_file', 'read_image', 'read_skill', 'memory_read'].includes(name)) return 'Reading…';
    if (['grep', 'glob', 'semantic_search', 'memory_search'].includes(name)) return 'Searching…';
    if (['write_file', 'edit_file', 'apply_patch', 'rename_symbol', 'memory_write', 'memory_delete'].includes(name)) {
      return 'Editing…';
    }
    if (name === 'shell') return 'Running Bash…';
    if (name === 'web_search') return 'Searching web…';
    if (name === 'fetch_url') return 'Fetching…';
    if (name === 'build') return 'Building…';
    if (['diagnostics', 'definition', 'references', 'hover'].includes(name)) return 'Inspecting…';
    if (['git_status', 'git_diff', 'git_log'].includes(name)) return 'Inspecting Git…';
    if (['spawn_agent', 'task_list', 'task_output', 'task_cancel'].includes(name)) return 'Delegating…';
    return toolTitle(name) + '…';
  }

  /** A trimmed string argument, or ''. */
  function textArg(args, key) {
    const value = args && typeof args === 'object' ? args[key] : undefined;
    return typeof value === 'string' ? value.trim() : '';
  }

  /** A finite numeric argument, or 0. */
  function numberArg(args, key) {
    const value = args && typeof args === 'object' ? args[key] : undefined;
    return typeof value === 'number' && Number.isFinite(value) ? value : 0;
  }

  /**
   * A path in a row, as a link.
   *
   * Only the argument the row knows is a file becomes one. The click is stopped
   * and its default prevented, because the link lives inside `<summary>` and
   * following the path should not also open the row. Enter and Space do the same
   * thing for a reader who reaches it from the keyboard.
   */
  function fileLink(path, line) {
    const link = el('span', 'file', path);
    link.setAttribute('role', 'link');
    link.tabIndex = 0;
    link.title = 'Open ' + path;
    const open = (event) => {
      event.preventDefault();
      event.stopPropagation();
      if (line > 0) vscode.postMessage({ type: 'openPath', path, line });
      else vscode.postMessage({ type: 'openPath', path });
    };
    link.addEventListener('click', open);
    link.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' || event.key === ' ') open(event);
    });
    return link;
  }

  /**
   * The second half of a row: what the call touched, as the parts that read as
   * one sentence. `file` is the only part that is a link, `meta` is the muted
   * range that rides beside it, and `text` is a command, pattern or title.
   * A call whose arguments say nothing useful falls back to the old one-line
   * hint rather than to an empty row.
   */
  function toolDetail(name, args) {
    const parts = [];
    const file = textArg(args, 'path') || textArg(args, 'file_path');
    const addFile = (line) => {
      if (file !== '') parts.push({ kind: 'file', text: file, line });
    };
    switch (name) {
      case 'read_file': {
        // offset is 1-based and limit counts lines, the same reading the host
        // uses. No range means no parentheses, not a made-up one.
        const offset = numberArg(args, 'offset');
        const limit = numberArg(args, 'limit');
        addFile(offset > 0 ? offset : undefined);
        if (offset > 0 && limit > 0) {
          parts.push({ kind: 'meta', text: `(lines ${offset}-${offset + limit - 1})` });
        } else if (offset > 0) {
          parts.push({ kind: 'meta', text: `(from line ${offset})` });
        } else if (limit > 0) {
          parts.push({ kind: 'meta', text: `(first ${limit} lines)` });
        }
        break;
      }
      case 'write_file':
      case 'edit_file':
      case 'read_image':
        addFile(undefined);
        break;
      case 'grep': {
        const pattern = textArg(args, 'pattern');
        if (pattern !== '') parts.push({ kind: 'text', text: pattern });
        addFile(undefined);
        break;
      }
      case 'glob': {
        const pattern = textArg(args, 'pattern');
        if (pattern !== '') parts.push({ kind: 'text', text: pattern });
        break;
      }
      case 'semantic_search': {
        const query = textArg(args, 'query');
        if (query !== '') parts.push({ kind: 'text', text: query });
        break;
      }
      case 'shell': {
        const command = textArg(args, 'command');
        if (command !== '') parts.push({ kind: 'text', text: command });
        break;
      }
      case 'web_search': {
        const query = textArg(args, 'query');
        if (query !== '') parts.push({ kind: 'text', text: query });
        break;
      }
      case 'fetch_url': {
        const url = textArg(args, 'url');
        if (url !== '') parts.push({ kind: 'text', text: url });
        break;
      }
      case 'spawn_agent': {
        const description = textArg(args, 'description');
        if (description !== '') parts.push({ kind: 'text', text: description });
        break;
      }
      case 'apply_patch': {
        // The patch names its files in its own text; the first one is the best
        // one-line answer to "what did this touch?".
        const patch = textArg(args, 'patch');
        const match = patch.match(/\*\*\* (?:Update|Add|Delete) File: (.+)/);
        if (match) parts.push({ kind: 'file', text: match[1].trim() });
        break;
      }
      case 'task_output':
      case 'task_cancel': {
        const id = textArg(args, 'task_id');
        if (id !== '') parts.push({ kind: 'text', text: id });
        break;
      }
      case 'read_skill': {
        const id = textArg(args, 'id');
        if (id !== '') parts.push({ kind: 'text', text: id });
        break;
      }
      case 'rename_symbol': {
        const line = numberArg(args, 'line');
        addFile(line > 0 ? line : undefined);
        const next = textArg(args, 'new_name');
        if (next !== '') parts.push({ kind: 'text', text: `→ ${next}` });
        break;
      }
      default:
        // Any other tool that names a path is still about that file.
        addFile(undefined);
        break;
    }
    if (parts.length === 0) {
      const fallback = callHint(name, args);
      if (fallback !== '') parts.push({ kind: 'text', text: fallback });
    }
    return parts;
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

  /**
   * The brief a spawn call carries: the description the person saw, then the
   * whole prompt the child gets. The prompt is shown because the child sees
   * nothing else from this conversation — a card that hid it would leave the
   * reader guessing what was delegated.
   */
  function spawnBrief(args) {
    if (!args || typeof args !== 'object') return '';
    const lines = [];
    if (typeof args.description === 'string' && args.description.trim() !== '') {
      lines.push(args.description.trim());
    }
    if (typeof args.prompt === 'string' && args.prompt.trim() !== '') {
      if (lines.length > 0) lines.push('');
      lines.push(args.prompt.trim());
    }
    return lines.join('\n');
  }

  function addToolCall(event, reviewable, anchor) {
    endRound();
    const spawn = event.name === 'spawn_agent';
    const card = el('details', 'tool' + (spawn ? ' spawn' : ''));
    const summary = el('summary');
    summary.appendChild(el('span', 'name', toolTitle(event.name)));
    const hint = el('span', 'hint');
    for (const part of toolDetail(event.name, event.args)) {
      if (part.kind === 'file') hint.appendChild(fileLink(part.text, part.line));
      else hint.appendChild(el('span', part.kind, part.text));
    }
    summary.appendChild(hint);
    summary.appendChild(el('span', 'state', spawn ? 'starting…' : 'running…'));
    card.appendChild(summary);

    const pane = el('div', 'pane');
    /** Where a spawn call's child events go; undefined for every other tool. */
    let body;
    // A spawn card starts open so the child's work is visible while it runs. A
    // replayed spawn has no steps to show and is collapsed by its result.
    if (spawn) {
      card.open = true;
      const brief = spawnBrief(event.args);
      if (brief !== '') pane.appendChild(section('task', 'TASK', brief));
      body = el('div', 'spawn-body');
      pane.appendChild(body);
    } else if (event.name === 'edit_file') {
      // An edit already carries both sides of the change, so the card shows that
      // comparison instead of the raw arguments, and opens onto it.
      const edit = editPair(event.args);
      if (edit) {
        card.open = true;
        pane.appendChild(renderEditDiff(edit, anchor));
      } else if (event.args !== undefined) {
        pane.appendChild(section('in', 'IN', safeJson(event.args)));
      }
    } else if (event.args !== undefined) {
      // Every other call keeps the labelled IN block. Omitted rather than shown
      // empty: `undefined` would otherwise be rendered as the string "undefined".
      pane.appendChild(section('in', 'IN', safeJson(event.args)));
    }
    card.appendChild(pane);

    if (event.tool_call_id) {
      toolCards.set(event.tool_call_id, card);
      // A child's events name this call, so this is the card they land in — and
      // a grandchild's spawn call is registered from inside this same body,
      // which is what makes the nesting work at any depth.
      if (spawn) spawnScopes.set(event.tool_call_id, makeScope(body));
      // Only for a call the extension said has a change to show. The button
      // itself waits for the result: a change that failed to apply is not a
      // change, and a diff of one would describe something that never happened.
      if (reviewable) {
        callFacts.set(event.tool_call_id, {
          runId: event.run_id,
          name: event.name,
          args: event.args,
        });
      }
    }
    append(card);
  }

  /**
   * The review affordance, added below the summary and never inside it.
   *
   * `<details>` toggles from its summary alone, so a button within the summary
   * would have to swallow its own click to stop the card collapsing under it.
   * Outside, the button is a sibling of the summary, which also means it stays
   * reachable while the card is closed — and a reader who wants the change
   * rather than the arguments is exactly the reader who has it closed.
   */
  function addReviewButton(card, toolCallId) {
    const facts = toolCallId ? callFacts.get(toolCallId) : undefined;
    if (!facts || card.querySelector('button.review')) return;
    const open = el('button', 'review', 'Review changes');
    open.type = 'button';
    open.title = 'Open what this call changed in the diff editor';
    open.addEventListener('click', () =>
      vscode.postMessage({
        type: 'reviewCall',
        toolCallId,
        runId: facts.runId,
        name: facts.name,
        args: facts.args,
      }),
    );
    // An edit card is already open on its diff, so the button sits under that
    // comparison. Every other card keeps it above the pane, where it stays
    // reachable while the card is collapsed.
    if (card.querySelector('.edit-diff')) card.appendChild(open);
    else card.insertBefore(open, card.querySelector('.pane'));
  }

  /** The two sides of an edit_file call, or undefined when the arguments are not one. */
  function editPair(args) {
    if (!args || typeof args !== 'object') return undefined;
    if (typeof args.old_string !== 'string' || typeof args.new_string !== 'string') return undefined;
    return {
      path: typeof args.path === 'string' ? args.path : '',
      before: args.old_string,
      after: args.new_string,
    };
  }

  /**
   * How to draw each comparison again in the other shape, keyed by its element.
   * A WeakMap so a card removed from the transcript is not kept alive by it.
   */
  const diffPainters = new WeakMap();

  /**
   * A short comparison of the two sides, capped so a large replacement does not
   * become the whole transcript. The lines are text nodes: the strings are
   * model output.
   */
  function renderEditDiff(change, anchor) {
    const box = el('div', 'edit-diff');
    const numbered = anchor && typeof anchor.startLine === 'number';
    const raw = [];
    if (numbered && Array.isArray(anchor.before)) {
      const start = anchor.startLine - anchor.before.length;
      for (let i = 0; i < anchor.before.length; i += 1) {
        raw.push({
          kind: 'ctx',
          text: anchor.before[i],
          sign: ' ',
          oldLine: start + i,
          newLine: start + i,
        });
      }
    }
    let oldNo = numbered ? anchor.startLine : 0;
    let newNo = numbered ? anchor.startLine : 0;
    for (const row of lineDiffRows(change.before, change.after)) {
      const text = row.text.slice(1);
      if (row.kind === 'add') {
        raw.push({ kind: 'add', text, sign: '+', newLine: numbered ? newNo : undefined });
        newNo += 1;
      } else if (row.kind === 'del') {
        raw.push({ kind: 'del', text, sign: '-', oldLine: numbered ? oldNo : undefined });
        oldNo += 1;
      } else {
        raw.push({
          kind: 'ctx',
          text,
          sign: ' ',
          oldLine: numbered ? oldNo : undefined,
          newLine: numbered ? newNo : undefined,
        });
        oldNo += 1;
        newNo += 1;
      }
    }
    if (numbered && Array.isArray(anchor.after)) {
      for (let i = 0; i < anchor.after.length; i += 1) {
        raw.push({
          kind: 'ctx',
          text: anchor.after[i],
          sign: ' ',
          oldLine: oldNo + i,
          newLine: newNo + i,
        });
      }
    }
    const cap = 24;
    const rows = pairDiffRows(raw);
    // The path is in the row above this pane; the comparison says how much
    // changed, which is the one question the rows below do not answer at a
    // glance. On a one-line edit this is the whole summary.
    const adds = raw.filter((row) => row.kind === 'add').length;
    const dels = raw.filter((row) => row.kind === 'del').length;
    if (adds > 0 || dels > 0) {
      box.appendChild(el('div', 'note', changeNote(adds, dels)));
    }
    const pairs = rows.slice(0, cap);
    const pre = el('pre', numbered ? 'diff numbered side' : 'diff side');

    // One side of a pair: a line number when the comparison has them, then the
    // sign and the text. The cell stays the box that carries the row's colour
    // and the high-contrast edge, so only the clipping element changed.
    function rowNode(side, row) {
      const node = el('div', 'row ' + row.kind);
      const cell = el('span', 'cell ' + side + ' ' + row.kind);
      if (numbered) cell.appendChild(el('span', 'ln', String(row.line)));
      cell.appendChild(el('span', 'code', row.sign + ' ' + row.text));
      node.appendChild(cell);
      return node;
    }

    /**
     * The comparison has two shapes. Wide, each side is one pane, so a long
     * line scrolls its side rather than drawing a bar under itself; narrow, the
     * sides are interleaved into one unified column. A pane is a whole side and
     * cannot be hidden into a column, so the pairs are kept and painted again
     * when the width class flips (`repaintDiffs`).
     */
    function paint() {
      const narrow = document.body.classList.contains('diff-narrow');
      pre.textContent = '';
      pre.className = 'diff side' + (numbered ? ' numbered' : '') + (narrow ? ' narrow' : '');
      if (narrow) {
        // A context line is the same on both sides and is said once; the two
        // sides of a change stack, old above new.
        for (const pair of pairs) {
          for (const side of ['old', 'new']) {
            const row = pair[side];
            if (row && !(side === 'new' && pair.kind === 'ctx')) {
              pre.appendChild(rowNode(side, row));
            }
          }
        }
        return;
      }
      for (const side of ['old', 'new']) {
        const pane = el('div', 'pane ' + side);
        for (const pair of pairs) {
          const row = pair[side];
          // The side that has nothing keeps its row: the panes must have the
          // same number of lines, or the next line would be compared with the
          // wrong one.
          pane.appendChild(row ? rowNode(side, row) : el('div', 'row empty'));
        }
        pre.appendChild(pane);
      }
      syncDiffScroll(pre);
    }

    paint();
    diffPainters.set(pre, paint);
    box.appendChild(pre);
    if (rows.length > cap) {
      box.appendChild(el('div', 'more-note', 'Showing the first ' + cap + ' rows of this change.'));
    }
    return box;
  }

  /**
   * The two sides are one picture. A drag on either bar, or a wheel over either
   * pane, puts the other pane at the same offset — the same line and the same
   * character column — so the halves of a change never drift apart. The write is
   * compared first, so the copy does not come back as another scroll event.
   */
  function syncDiffScroll(pre) {
    const panes = pre.querySelectorAll('.pane');
    if (panes.length !== 2) return;
    const follow = (from, to) => {
      from.addEventListener('scroll', () => {
        if (to.scrollTop !== from.scrollTop) to.scrollTop = from.scrollTop;
        if (to.scrollLeft !== from.scrollLeft) to.scrollLeft = from.scrollLeft;
      });
    };
    follow(panes[0], panes[1]);
    follow(panes[1], panes[0]);
  }

  /** "Added 1 line", "Removed 2 lines", or both, from the counts above. */
  function changeNote(adds, dels) {
    const lines = (count) => (count === 1 ? '1 line' : count + ' lines');
    const parts = [];
    if (adds > 0) parts.push('Added ' + lines(adds));
    if (dels > 0) {
      parts.push((parts.length > 0 ? 'removed ' : 'Removed ') + lines(dels));
    }
    return parts.join(', ');
  }

  /** How many lines a result holds, not counting a trailing newline. */
  function lineCount(text) {
    const trimmed = text.replace(/\r?\n+$/, '');
    return trimmed === '' ? 0 : trimmed.split(/\r?\n/).length;
  }

  /** Line comparison of the two sides. A huge pair is clipped rather than diffed. */
  function lineDiffRows(before, after) {
    const a = splitLines(before);
    const b = splitLines(after);
    if (a.length * b.length > 4000) {
      const rows = [];
      for (const line of a.slice(0, 12)) rows.push({ kind: 'del', text: '-' + line });
      for (const line of b.slice(0, 12)) rows.push({ kind: 'add', text: '+' + line });
      return rows;
    }
    const n = a.length;
    const m = b.length;
    const dp = Array.from({ length: n + 1 }, () => new Uint16Array(m + 1));
    for (let i = n - 1; i >= 0; i -= 1) {
      for (let j = m - 1; j >= 0; j -= 1) {
        dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
      }
    }
    const rows = [];
    let i = 0;
    let j = 0;
    while (i < n && j < m) {
      if (a[i] === b[j]) {
        rows.push({ kind: 'ctx', text: ' ' + a[i] });
        i += 1;
        j += 1;
      } else if (dp[i + 1][j] >= dp[i][j + 1]) {
        rows.push({ kind: 'del', text: '-' + a[i] });
        i += 1;
      } else {
        rows.push({ kind: 'add', text: '+' + b[j] });
        j += 1;
      }
    }
    while (i < n) {
      rows.push({ kind: 'del', text: '-' + a[i] });
      i += 1;
    }
    while (j < m) {
      rows.push({ kind: 'add', text: '+' + b[j] });
      j += 1;
    }
    return rows;
  }

  function splitLines(text) {
    const lines = text.split('\n');
    if (lines.length > 0 && lines[lines.length - 1] === '' && text.endsWith('\n')) lines.pop();
    return lines;
  }

  /**
   * Zips a change block's deletions with its insertions, old side beside new.
   *
   * The LCS decides which lines are gone and which arrived but not that a block
   * is a replacement; pairing by position inside the block is what makes a
   * two-column row read as one change. A block with no counterpart on one side
   * becomes a half row, and context keeps both numbers so each side can show
   * its own.
   */
  function pairDiffRows(raw) {
    const rows = [];
    for (let i = 0; i < raw.length; ) {
      if (raw[i].kind === 'ctx') {
        const row = raw[i];
        rows.push({
          kind: 'ctx',
          old: { kind: 'ctx', line: row.oldLine, text: row.text, sign: ' ' },
          new: { kind: 'ctx', line: row.newLine, text: row.text, sign: ' ' },
        });
        i += 1;
        continue;
      }
      const dels = [];
      const adds = [];
      while (i < raw.length && raw[i].kind !== 'ctx') {
        if (raw[i].kind === 'del') dels.push(raw[i]);
        else adds.push(raw[i]);
        i += 1;
      }
      const n = Math.max(dels.length, adds.length);
      for (let k = 0; k < n; k += 1) {
        const d = dels[k];
        const a = adds[k];
        rows.push({
          kind: d && a ? 'change' : d ? 'del' : 'add',
          old: d ? { kind: 'del', line: d.oldLine, text: d.text, sign: '-' } : null,
          new: a ? { kind: 'add', line: a.newLine, text: a.text, sign: '+' } : null,
        });
      }
    }
    return rows;
  }

  function addToolResult(event) {
    let card = event.tool_call_id ? toolCards.get(event.tool_call_id) : undefined;
    if (!card) {
      if (!event.tool_call_id) {
        // A result that names no call cannot be attached to one. It is still
        // something the model saw, so it is said in one muted line with the
        // output behind a caption — never printed as an error.
        addOrphanResult(event);
        return;
      }
      // A result whose call is not on screen (a window that opened mid-run, a
      // reloaded webview): draw the row it should have had, then settle it
      // through the normal path. The result is information, not a failure.
      addToolCall({ ...event, type: 'tool_call', args: undefined }, false);
      card = toolCards.get(event.tool_call_id);
      if (!card) return;
    }
    if (event.tool_call_id && spawnScopes.has(event.tool_call_id)) {
      addSpawnResult(card, event);
      return;
    }
    const state = card.querySelector('.state');
    const failed = event.is_error === true;
    card.classList.add(failed ? 'failed' : 'done');
    state.textContent = failed ? 'failed' : 'done';

    // A call that failed changed nothing, so its card offers no review; a call
    // that succeeded offers one when its arguments describe a change at all.
    if (!failed) addReviewButton(card, event.tool_call_id);

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
    // The paths no allow rule could cover, in the form the call spelled them.
    // Present even when the mode approved the call, which is how a result that
    // ran without a prompt says no remembered rule decided it.
    if (Array.isArray(event.protected) && event.protected.length > 0) {
      const line = el('div', 'rule-ref protected');
      line.appendChild(el('span', null, 'protected path: '));
      line.appendChild(el('code', null, event.protected.join(', ')));
      card.insertBefore(line, card.querySelector('.pane'));
    }

    const content = event.content || '';
    addOutputLink(card, event, content);
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

  /**
   * The caption a result offers: small grey text that opens the whole output in
   * a tab. Null when there is nothing to show.
   *
   * It is a button rather than a link so following it never toggles a folded
   * row, and the text is the count rather than the output so a card stays one
   * line. One builder for the two places a result can land.
   */
  function outputNote(name, event, content) {
    const count = lineCount(content);
    if (count === 0) return null;
    const note = el(
      'button',
      'output-link',
      count === 1 ? '1 line of output' : count + ' lines of output',
    );
    note.type = 'button';
    note.title = 'Open the full output in a new tab';
    note.addEventListener('click', () =>
      vscode.postMessage({
        type: 'openOutput',
        title: name,
        id: event.tool_call_id || '',
        text: content,
      }),
    );
    return note;
  }

  /**
   * The caption under a settled row.
   *
   * It is a sibling of the `<details>`, not a child: a folded details hides
   * everything but its summary, and this line has to be readable while the row
   * is closed. The rail pass treats it as part of the reply, so the line does
   * not stop around it.
   */
  function addOutputLink(card, event, content) {
    const label = card.querySelector('.name');
    const note = outputNote(label ? label.textContent.trim() : 'Tool', event, content);
    if (!note) return;
    card.parentNode.insertBefore(note, card.nextSibling);
    syncRail(card.parentElement);
  }

  /**
   * A result that names no call at all.
   *
   * The host always sends an id, so this is defensive: without one there is no
   * row to draw, and the old fallback printed the whole result in the error
   * colour. The block says what came back and leaves the text behind the usual
   * caption.
   */
  function addOrphanResult(event) {
    const name = toolTitle(event.name);
    const wrap = el('div', 'msg result-note');
    wrap.appendChild(el('div', 'body', `${name} returned output, but its call is not in this transcript.`));
    const note = outputNote(name, event, event.content || '');
    if (note) wrap.appendChild(note);
    append(wrap);
    endRound();
  }

  /** A button in a spawn card's action row. */
  function spawnAction(label, onClick) {
    const button = el('button', 'spawn-action', label);
    button.type = 'button';
    button.addEventListener('click', onClick);
    return button;
  }

  /**
   * Offers the child's stored conversation.
   *
   * The child is a session like any other — the store keeps it, the sidebar
   * nests it — so this is the same open a sidebar click does. It is offered
   * only when an id is known: a foreground spawn's result carries it, and a
   * background spawn's child announced it on its own events.
   */
  function addSpawnTranscriptButton(pane, agentId) {
    if (!agentId) return;
    const row = el('div', 'spawn-actions');
    row.appendChild(
      spawnAction('Open subagent transcript', () =>
        vscode.postMessage({ type: 'openAgentTranscript', agentId }),
      ),
    );
    pane.appendChild(row);
  }

  /** Three staggered dots: enough motion to read as work, without flashing the row. */
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

  /** Applies a real task notification to the card that started that background job. */
  function updateBackgroundTask(task) {
    if (!task || typeof task.id !== 'string' || task.id === '') return;
    backgroundTaskStates.set(task.id, task);
    const activityId = 'task:' + task.id;
    if (task.kind === 'agent' && task.status === 'running') activeSubagents.add(activityId);
    else activeSubagents.delete(activityId);
    syncCollaborationActivity();
    const card = backgroundCards.get(task.id);
    if (!card) return;
    const state = card.querySelector('.state');
    const note = card.querySelector('.spawn-note');
    if (!state) return;

    card.classList.remove('background-running', 'done', 'failed', 'stopped');
    if (task.status === 'running') {
      card.classList.add('background-running');
      state.textContent = 'background';
      state.appendChild(backgroundActivity());
      if (note) note.textContent = 'Working in the background.';
      return;
    }

    const failed = task.status === 'failed';
    const stopped = task.status === 'cancelled';
    card.classList.add(failed ? 'failed' : stopped ? 'stopped' : 'done');
    state.textContent = failed ? 'background failed' : stopped ? 'background stopped' : 'background done';
    if (note) {
      note.textContent = failed
        ? 'Background task failed.'
        : stopped
          ? 'Background task stopped.'
          : 'Background task finished.';
    }
    backgroundCards.delete(task.id);
    backgroundTaskStates.delete(task.id);
  }

  /**
   * Settles a spawn card.
   *
   * The result is the parent's half of the delegation: the child's id, its step
   * count, and its final message. The child's own steps arrived as events and
   * are already in the body. A replayed tab has no child records — the child's
   * conversation is a separate stored agent — so the result's text is all there
   * is, and the card says what happened rather than pretending the steps exist.
   */
  function addSpawnResult(card, event) {
    completedSubagentSpawns.add(event.tool_call_id);
    activeSubagents.delete('spawn:' + event.tool_call_id);
    syncCollaborationActivity();
    const content = event.content || '';
    const failed = event.is_error === true;
    const state = card.querySelector('.state');
    const pane = card.querySelector('.pane');
    const body = card.querySelector('.spawn-body');
    const scope = spawnScopes.get(event.tool_call_id);
    const live = body !== null && body.childNodes.length > 0;

    const lines = content.split('\n');
    const first = lines[0] || '';
    const rest = lines.slice(1).join('\n').trim();
    const finished = /^(\S+) finished in (\d+) steps?\./.exec(first);
    const background = /^(\S+) started in the background/.exec(first);

    if (failed) {
      card.classList.add('failed');
      state.textContent = 'failed';
      pane.appendChild(section('out', 'OUT', content));
      if (!live) card.open = false;
      return;
    }
    if (background) {
      const row = el('div', 'spawn-actions');
      row.appendChild(el('span', 'spawn-note', 'Working in the background.'));
      row.appendChild(spawnAction('Open Tasks', () => vscode.postMessage({ type: 'openTasks' })));
      pane.appendChild(row);
      addSpawnTranscriptButton(pane, scope && scope.agentId);
      backgroundCards.set(background[1], card);
      updateBackgroundTask(
        backgroundTaskStates.get(background[1]) || { id: background[1], kind: 'agent', status: 'running' },
      );
      return;
    }
    card.classList.add('done');
    state.textContent = finished ? `done · ${finished[2]} steps` : 'done';
    addSpawnTranscriptButton(pane, (scope && scope.agentId) || (finished ? finished[1] : ''));
    // A live spawn already has the child's message in the body, streamed as the
    // child's own events. A replayed one does not: its result is the only copy.
    if (!live && rest !== '') pane.appendChild(section('result', 'RESULT', rest));
    if (!live) card.open = false;
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
        // A change that is one file's text can be kept in parts. The extension
        // proves it before opening anything; this is only the offer, and it is
        // made for the two tools whose arguments carry exactly those bodies.
        if (request.tool === 'write_file' || request.tool === 'edit_file') {
          const partial = el('button', 'review', 'Review changes…');
          partial.type = 'button';
          partial.title = 'Keep only some of this change';
          partial.addEventListener('click', () =>
            vscode.postMessage({ type: 'reviewPartial', requestId: request.request_id }),
          );
          box.appendChild(partial);
        }
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

  /** Counts approval cards so each can label its own group of buttons. */
  let approvalSeq = 0;

  function showApproval(request) {
    endRound();
    const card = el('div', 'approval');
    card.dataset.tool = typeof request.tool === 'string' ? request.tool : '';
    setWorkingActivity('Waiting for ' + toolTitle(request.tool) + ' approval…');

    const head = el('div', 'head');
    // The buttons below are a group named by this question, so a reader that
    // reaches "Allow once" is also told what it is allowing.
    const headId = 'approval-head-' + ++approvalSeq;
    head.id = headId;
    head.appendChild(el('span', null, 'Allow '));
    head.appendChild(el('span', 'tool-name', toolTitle(request.tool)));
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
    // A protected path is why no "always allow" can settle this card: the rule
    // file may say allow, and it is skipped. Saying so is the difference between
    // a card that looks like it ignored its own rules and one that explains why.
    if (Array.isArray(request.protected) && request.protected.length > 0) {
      const line = el('div', 'why protected');
      line.appendChild(el('span', null, 'Protected path — no always-allow rule can cover it: '));
      line.appendChild(el('code', null, request.protected.join(', ')));
      card.appendChild(line);
    }
    card.appendChild(describePreview(request));

    const buttons = el('div', 'buttons');
    buttons.setAttribute('role', 'group');
    buttons.setAttribute('aria-labelledby', headId);
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
    // An approval is part of the turn: it belongs inside the reply like the
    // calls above it, and inside one is where a reader who expanded the turn
    // will look for it.
    const target = active === mainScope && reply ? reply.body : transcript;
    addToEnd(target, card);
    // An approval is not a step, so a run ends at it; the rows above may still
    // carry a `rail-down` from before the card existed.
    syncRail(target);
    transcript.scrollTop = transcript.scrollHeight;
    // A card that only appears is a turn waiting behind a reader's back. The
    // turn is running, so the composer is disabled and nothing else owns the
    // keyboard: saying it, and putting focus on the first answer, is the whole
    // keyboard path to deciding it.
    const preview = request.preview || {};
    announceAlert('Approval needed: ' + toolTitle(request.tool) + (preview.summary ? '. ' + preview.summary : ''));
    const first = buttons.querySelector('button');
    if (first) first.focus();
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
      const label = scope ? `Always allow for ${scope}` : `Always allow ${toolTitle(request.tool)} this session`;
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
      announceAlert('Nothing was written: ' + msg.error);
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
      setWorkingActivity(toolActivity(card.dataset.tool));
      announce('Allowed, and remembered.');
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

  /**
   * Where focus goes after a decision. The button just pressed is disabled by
   * the answer, which takes it out of the document; the turn is still running,
   * so the stop control is the one control that still means something.
   *
   * `hadFocus` is read before the buttons are disabled, because that is the
   * moment the browser moves focus to the body.
   */
  function refocusAfter(hadFocus) {
    if (hadFocus) sendButton.focus();
  }

  function resolveApproval(requestId, decision, scope) {
    const card = approvals.get(requestId);
    if (!card) return;
    const hadFocus = card.contains(document.activeElement);
    approvals.delete(requestId);
    card.classList.add('resolved');
    for (const button of card.querySelectorAll('button')) button.disabled = true;
    const verdict = el('div', 'why', decision === 'deny' ? 'Denied.' : 'Allowed.');
    card.appendChild(verdict);
    setWorkingActivity(decision === 'deny' ? DEFAULT_WORKING_WORD : toolActivity(card.dataset.tool));
    announce(verdict.textContent);
    refocusAfter(hadFocus);
    vscode.postMessage({ type: 'answer', requestId, decision, scope });
  }

  function withdrawApproval(requestId) {
    const card = approvals.get(requestId);
    if (!card) return;
    const hadFocus = card.contains(document.activeElement);
    approvals.delete(requestId);
    card.classList.add('resolved');
    for (const button of card.querySelectorAll('button')) button.disabled = true;
    card.appendChild(el('div', 'why', 'This request is no longer pending.'));
    setWorkingActivity(DEFAULT_WORKING_WORD);
    announce('This approval request is no longer pending.');
    refocusAfter(hadFocus);
  }

  /**
   * An approval the review panel answered. The card settles on this rather than
   * on a click of its own, and a partial acceptance says how much was kept:
   * "Allowed." over a change that was half applied would misstate the thing the
   * reviewer just chose.
   */
  function settleFromReview(msg) {
    const card = approvals.get(msg.requestId);
    if (!card) return;
    approvals.delete(msg.requestId);
    card.classList.add('resolved');
    for (const button of card.querySelectorAll('button')) button.disabled = true;
    if (msg.decision === 'deny') {
      card.appendChild(el('div', 'why', 'Denied.'));
      setWorkingActivity(DEFAULT_WORKING_WORD);
      announce('Denied.');
      return;
    }
    const kept = typeof msg.kept === 'number' ? msg.kept : NaN;
    const total = typeof msg.total === 'number' ? msg.total : NaN;
    const text =
      kept >= 0 && total > 0 && kept < total
        ? 'Allowed, keeping ' + kept + ' of ' + total + ' parts.'
        : 'Allowed.';
    card.appendChild(el('div', 'why', text));
    setWorkingActivity(toolActivity(card.dataset.tool));
    announce(text);
  }

  // --- composer chips -------------------------------------------------------

  /** Redraws the row of chips above the textarea. */
  function renderChips() {
    chipsBar.textContent = '';
    for (const mention of mentions) chipsBar.appendChild(mentionChip(mention));
    for (const quote of quotes) chipsBar.appendChild(quoteChip(quote));
    for (let index = 0; index < images.length; index++) chipsBar.appendChild(imageChip(index));
    chipsBar.hidden = mentions.length === 0 && quotes.length === 0 && images.length === 0;
  }

  function removeChip(title, onRemove) {
    const button = el('button', 'chip-x', '×');
    button.type = 'button';
    button.title = title;
    // "×" is a picture of a cross, not a name; the title already reads as the
    // action ("Stop referencing src/x.ts"), so it is also what is announced.
    button.setAttribute('aria-label', title);
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

  function quoteChip(quote) {
    const speaker = quote.role === 'user' ? 'you' : 'anvil';
    const chip = el('span', 'chip quote');
    chip.title = speaker + ': ' + quote.text;
    chip.appendChild(el('span', 'chip-quote-mark', '“'));
    chip.appendChild(el('span', 'chip-label', speaker + ': ' + quotePreview(quote.text, 48)));
    chip.appendChild(
      removeChip('Stop quoting this message', () => {
        quotes = quotes.filter((candidate) => candidate.id !== quote.id);
        renderChips();
      }),
    );
    return chip;
  }

  function imageChip(index) {
    const chip = el('span', 'chip image');
    const thumb = el('img');
    thumb.src = images[index];
    thumb.alt = 'attached image';
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
   * Reads an image into a data URL, for a paste or a drop. The bytes never
   * touch the disk: the host takes base64 straight from the prompt and hands it
   * to the model, so an attachment leaves nothing behind in the workspace.
   * (The picker reads its files on the host side and posts the same URL.)
   */
  function readImage(file) {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result !== 'string') return;
      addImage(reader.result);
    };
    reader.onerror = () => showBanner('error', 'That image could not be read.');
    reader.readAsDataURL(file);
  }

  /**
   * Adds one image, as a data URL, to what the composer will send.
   *
   * A paste and the picker both arrive here, and the host answers the picker
   * with data URLs for exactly that reason: one representation means one chip,
   * one send, and one conversion on the extension side.
   */
  function addImage(dataUrl) {
    if (typeof dataUrl !== 'string' || dataUrl.indexOf('data:image/') !== 0) return;
    images.push(dataUrl);
    renderChips();
    if (!vision) {
      showBanner('warn', 'Images are off for this session. Turn on "anvil.vision" and reload the host to use them.');
    }
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
  function showChanges(files, runId) {
    if (!Array.isArray(files) || files.length === 0) return;
    const card = el('div', 'changes');
    card.appendChild(el('div', 'head', `Changed files · ${files.length}`));
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
    const summary = runSummaries.get(typeof runId === 'string' ? runId : '');
    if (summary && summary.isConnected) summary.insertAdjacentElement('afterend', card);
    else append(card);
    endRound();
  }

  // --- undo -----------------------------------------------------------------

  /**
   * Offers the undo of a finished run.
   *
   * The button is the only route to a write: it asks the extension, which asks
   * the user in a modal before anything on disk changes. The paths are shown
   * because "restore the workspace" is too broad a promise for a card to make —
   * these files, no others.
   */
  function showCheckpoint(msg) {
    const runId = typeof msg.runId === 'string' ? msg.runId : '';
    if (!runId || checkpointBlocks.has(runId)) return;
    const files = Array.isArray(msg.files) ? msg.files.filter((path) => typeof path === 'string') : [];
    if (files.length === 0) return;
    const card = el('div', 'checkpoint');
    card.appendChild(el('div', 'head', 'Undo this run'));
    card.appendChild(
      el(
        'div',
        'hint',
        'Put ' + files.length + ' file' + (files.length === 1 ? '' : 's') +
          ' back as they were before this run. Files a shell command wrote are not covered.',
      ),
    );
    const shown = files.slice(0, 8).join(', ') + (files.length > 8 ? ', …' : '');
    card.appendChild(el('div', 'paths', shown));

    const button = el('button', 'restore', 'Restore files…');
    button.type = 'button';
    button.title = 'Put these files back to their content before this run';
    button.addEventListener('click', () => {
      if (button.disabled) return;
      button.disabled = true;
      button.textContent = 'Waiting for confirmation…';
      vscode.postMessage({ type: 'restoreCheckpoint', runId: runId });
    });
    card.appendChild(button);

    checkpointBlocks.set(runId, { card: card, button: button });
    append(card);
    endRound();
  }

  /** Settles the card whose restore was answered: cancelled, failed, or done. */
  function settleCheckpoint(msg) {
    const entry = checkpointBlocks.get(msg.runId);
    if (!entry) return;
    const button = entry.button;
    if (msg.cancelled === true) {
      button.disabled = false;
      button.textContent = 'Restore files…';
      return;
    }
    if (typeof msg.error === 'string' && msg.error !== '') {
      button.disabled = false;
      button.textContent = 'Try again';
      entry.card.appendChild(el('div', 'outcome failed', 'Not restored: ' + msg.error));
      return;
    }
    button.disabled = true;
    button.textContent = 'Restored';
    const parts = [];
    if (typeof msg.restored === 'number' && msg.restored > 0) {
      parts.push('put back ' + msg.restored + ' file' + (msg.restored === 1 ? '' : 's'));
    }
    if (typeof msg.deleted === 'number' && msg.deleted > 0) {
      parts.push('deleted ' + msg.deleted + ' file' + (msg.deleted === 1 ? '' : 's') + ' the run created');
    }
    entry.card.appendChild(
      el('div', 'outcome done', parts.length > 0 ? 'Restored: ' + parts.join(', ') + '.' : 'Nothing to restore.'),
    );
  }

  // --- fork -----------------------------------------------------------------

  /**
   * The one action a finished run offers on its own.
   *
   * A fork copies the conversation through a `run_end`, so the row is drawn at
   * the boundary itself rather than at the bottom of the panel: "from here" has
   * to mean the place it is written. The extension creates the new tab; this
   * side only asks, and the button waits for the answer because a fork that
   * failed has to be possible to try again.
   */
  function showRunActions(runId) {
    if (typeof runId !== 'string' || runId === '' || forkBlocks.has(runId)) return;
    const row = el('div', 'run-actions');
    const button = el('button', 'ghost', 'Fork from here');
    button.type = 'button';
    button.title = 'Copy this conversation up to this run into a new tab';
    button.addEventListener('click', () => {
      if (button.disabled) return;
      button.disabled = true;
      button.textContent = 'Forking…';
      vscode.postMessage({ type: 'forkSession', runId: runId });
    });
    row.appendChild(button);
    forkBlocks.set(runId, { row: row, button: button });
    append(row);
  }

  /** Settles the run's fork button: the new tab opened, or it did not. */
  function settleFork(msg) {
    const entry = forkBlocks.get(msg.runId);
    if (!entry) return;
    if (typeof msg.error === 'string' && msg.error !== '') {
      entry.button.disabled = false;
      entry.button.textContent = 'Fork from here';
      entry.row.appendChild(el('span', 'outcome failed', 'Not forked: ' + msg.error));
      return;
    }
    entry.button.disabled = true;
    entry.button.textContent = 'Forked';
  }

  // --- rewind ---------------------------------------------------------------

  /**
   * Gives a finished run to the turn it answered.
   *
   * Only the last turn can be unassigned. A run is one turn, so a run that ends
   * with no prompt waiting — a transcript that started mid-turn, a replay with
   * records left out — must not be pinned onto a message it did not answer.
   */
  function noteRun(runId) {
    if (typeof runId !== 'string' || runId === '') return;
    const turn = rewindTurns[rewindTurns.length - 1];
    if (!turn || turn.runId !== '') return;
    turn.runId = runId;
    drawRewind(turn);
  }

  /** The last run before this turn; empty when this turn is the first one. */
  function previousRunId(turn) {
    for (let i = rewindTurns.indexOf(turn) - 1; i >= 0; i--) {
      if (rewindTurns[i].runId !== '') return rewindTurns[i].runId;
    }
    return '';
  }

  /**
   * The rewind control on one user message.
   *
   * It is quiet until opened: the transcript is the content, and an undo wants
   * a label before it wants a toolbar. The three choices are drawn together so
   * the difference between them — the conversation, the files, or both — is
   * visible before either is chosen.
   */
  function drawRewind(turn) {
    if (turn.menu) {
      renderRewind(turn);
      return;
    }
    const box = el('div', 'rewind');
    const button = el('button', 'ghost', 'Rewind to here');
    button.type = 'button';
    button.title = 'Go back to before this turn';
    const menu = el('div', 'rewind-menu');
    menu.hidden = true;
    const hint = el(
      'div',
      'rewind-hint',
      'A checkpoint is taken before the turn, not before each tool call.',
    );
    hint.hidden = true;
    button.addEventListener('click', () => {
      menu.hidden = !menu.hidden;
      hint.hidden = menu.hidden;
      if (!menu.hidden) {
        renderRewind(turn);
        const first = menu.querySelector('button');
        if (first && !first.disabled) first.focus();
      }
    });
    box.appendChild(button);
    box.appendChild(menu);
    box.appendChild(hint);
    turn.wrap.appendChild(box);
    turn.menu = { box: box, button: button, menu: menu, hint: hint };
    renderRewind(turn);
  }

  /**
   * Draws the choices the extension's answer allows.
   *
   * Nothing here assumes a checkpoint exists: until `rewindState` says a run
   * captured something, the file action is not offered, and the combined action
   * has nothing to combine with. A turn the store recorded no files for says so
   * rather than showing a button that would fail.
   */
  function renderRewind(turn) {
    const menu = turn.menu.menu;
    menu.textContent = '';
    const prevRunId = previousRunId(turn);
    const files = rewindStates.get(turn.runId);
    const restore = Array.isArray(files) && files.length > 0;
    const waiting = !rewindStates.has(turn.runId);

    menu.appendChild(
      rewindRow(
        turn,
        prevRunId === '' ? 'Start a new conversation' : 'Fork from before this turn',
        prevRunId === ''
          ? 'This is the first turn, so rewinding past it starts an empty conversation'
          : 'Copy the conversation up to the turn before this one into a new tab',
        'fork',
        prevRunId,
      ),
    );

    if (restore) {
      menu.appendChild(
        rewindRow(
          turn,
          'Restore this turn’s files',
          'Put ' + files.length + ' file' + (files.length === 1 ? '' : 's') +
            ' back as they were before this turn; the conversation is left as it is',
          'restore',
          prevRunId,
        ),
      );
      menu.appendChild(
        rewindRow(
          turn,
          'Fork and restore',
          'Branch before this turn and put its files back; both halves report what happened',
          'both',
          prevRunId,
        ),
      );
    } else if (waiting) {
      menu.appendChild(el('div', 'rewind-note', 'Checking this turn’s files…'));
    } else {
      menu.appendChild(el('div', 'rewind-note', 'This turn changed no files, so there is nothing to restore.'));
    }
  }

  /** One choice in the menu. It posts, then waits, so a second click is nothing. */
  function rewindRow(turn, label, title, action, prevRunId) {
    const row = el('div', 'rewind-row');
    const button = el('button', 'ghost', label);
    button.type = 'button';
    button.title = title;
    button.addEventListener('click', () => {
      if (button.disabled) return;
      for (const control of turn.menu.menu.querySelectorAll('button')) control.disabled = true;
      turn.menu.menu.appendChild(el('div', 'rewind-note', 'Rewinding…'));
      vscode.postMessage({ type: 'rewind', action: action, runId: turn.runId, prevRunId: prevRunId });
    });
    row.appendChild(button);
    return row;
  }

  /** The extension's answer about one run: the paths it captured, or none. */
  function showRewindState(msg) {
    const runId = typeof msg.runId === 'string' ? msg.runId : '';
    if (runId === '') return;
    const files = Array.isArray(msg.files) ? msg.files.filter((path) => typeof path === 'string') : [];
    rewindStates.set(runId, files);
    const turn = rewindTurns.find((candidate) => candidate.runId === runId);
    if (turn && turn.menu) renderRewind(turn);
  }

  /**
   * Settles a rewind, half by half.
   *
   * A fork that worked and a restore that failed is not one outcome, and a
   * single line saying "done" would erase the half the user has to deal with.
   * Both endings are drawn, and the menu is offered again so a failed half can
   * be retried.
   */
  function settleRewind(msg) {
    const turn = rewindTurns.find((candidate) => candidate.runId === msg.runId);
    if (!turn || !turn.menu) return;
    turn.menu.menu.hidden = false;
    turn.menu.hint.hidden = false;
    renderRewind(turn);
    if (msg.cancelled === true) {
      turn.menu.menu.appendChild(el('div', 'rewind-note', 'Cancelled. Nothing changed.'));
      return;
    }
    const outcomes = [];
    if (typeof msg.forked === 'string' && msg.forked !== '') {
      outcomes.push({ ok: true, text: 'Forked into a new tab.' });
    }
    if (typeof msg.forkError === 'string' && msg.forkError !== '') {
      outcomes.push({ ok: false, text: 'Not forked: ' + msg.forkError + '.' });
    }
    if (typeof msg.restoreError === 'string' && msg.restoreError !== '') {
      outcomes.push({ ok: false, text: 'Not restored: ' + msg.restoreError + '.' });
    } else if (typeof msg.restored === 'number') {
      outcomes.push({ ok: true, text: restoredText(msg.restored, msg.deleted) });
    }
    if (outcomes.length === 0) outcomes.push({ ok: true, text: 'Done.' });
    for (const outcome of outcomes) {
      turn.menu.box.appendChild(el('div', 'outcome ' + (outcome.ok ? 'done' : 'failed'), outcome.text));
    }
  }

  /** What a restore actually did, in the words the card already uses. */
  function restoredText(restored, deleted) {
    const gone = typeof deleted === 'number' ? deleted : 0;
    if (restored === 0 && gone === 0) return 'No files needed restoring.';
    const put = 'put back ' + restored + ' file' + (restored === 1 ? '' : 's');
    if (gone === 0) return 'Restored: ' + put + '.';
    return 'Restored: ' + put + ', deleted ' + gone + ' file' + (gone === 1 ? '' : 's') + ' the turn created.';
  }

  /** `/rewind`: opens the control on the last turn that finished. */
  function openLastRewind() {
    for (let i = rewindTurns.length - 1; i >= 0; i--) {
      const turn = rewindTurns[i];
      if (turn.runId !== '' && turn.menu) {
        turn.menu.menu.hidden = false;
        turn.menu.hint.hidden = false;
        renderRewind(turn);
        turn.menu.button.focus();
        return;
      }
    }
    addNotice('There is no finished turn to rewind to.');
  }

  // --- plan review ----------------------------------------------------------

  /** The current plan-mode result, docked immediately above the composer. */
  function showPlan(msg) {
    const runId = msg.runId;
    if (
      typeof runId !== 'string' ||
      runId === '' ||
      planCards.has(runId) ||
      !planDock
    ) return;
    const card = el('section', 'plan');
    card.setAttribute('aria-label', 'Current plan');
    const toggle = el('button', 'plan-toggle');
    toggle.type = 'button';
    toggle.setAttribute('aria-expanded', 'true');
    toggle.title = 'Collapse plan';
    const chevron = el('span', 'plan-chevron', '⌄');
    chevron.setAttribute('aria-hidden', 'true');
    toggle.appendChild(chevron);
    toggle.appendChild(el('span', 'plan-title', 'Plan'));
    card.appendChild(toggle);

    const content = el('div', 'plan-content');
    const body = el('div', 'plan-body body md');
    renderBlocks(body, parseMarkdown(typeof msg.text === 'string' ? msg.text : ''), 0);
    content.appendChild(body);
    const actions = el('div', 'plan-actions');
    const open = el('button', 'ghost', 'Open plan');
    open.type = 'button';
    open.title = 'Open this plan in a read-only document beside the conversation';
    const approve = el('button', 'primary', 'Approve and run');
    approve.type = 'button';
    approve.title = 'Leave plan mode and carry the plan out';
    const reject = el('button', 'ghost', 'Keep planning');
    reject.type = 'button';
    reject.title = 'Stay in plan mode and revise the request';
    open.addEventListener('click', () => {
      vscode.postMessage({ type: 'openPlan', runId: runId });
    });
    approve.addEventListener('click', () => {
      if (approve.disabled) return;
      approve.disabled = true;
      approve.textContent = 'Approving…';
      vscode.postMessage({ type: 'approvePlan', runId: runId });
    });
    reject.addEventListener('click', () => {
      if (reject.disabled) return;
      vscode.postMessage({ type: 'rejectPlan', runId: runId });
    });
    actions.appendChild(open);
    actions.appendChild(approve);
    actions.appendChild(reject);
    content.appendChild(actions);
    // Says what approval costs before the button is pressed rather than after:
    // it is the mode that withheld the write tools that is being left.
    const note = el('div', 'plan-note', 'Approving leaves plan mode; keeping the plan sends nothing.');
    content.appendChild(note);
    card.appendChild(content);
    toggle.addEventListener('click', () => {
      const expanded = card.classList.toggle('collapsed') === false;
      toggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
      toggle.title = expanded ? 'Collapse plan' : 'Expand plan';
      requestAnimationFrame(syncComposerHeight);
    });
    planCards.set(runId, {
      card: card,
      toggle: toggle,
      approve: approve,
      reject: reject,
      note: note,
    });
    const pinned = atBottom();
    planDock.replaceChildren(card);
    planDock.hidden = false;
    scrollIfPinned(pinned);
  }

  /** The answer to one of the card's two decisions. */
  function settlePlan(msg) {
    const entry = planCards.get(msg.runId);
    if (!entry) return;
    if (typeof msg.error === 'string' && msg.error !== '') {
      entry.approve.disabled = false;
      entry.approve.textContent = 'Approve and run';
      entry.note.className = 'plan-note failed';
      entry.note.textContent = 'Not approved: ' + msg.error;
      return;
    }
    entry.note.className = 'plan-note done';
    if (msg.action === 'approve') {
      entry.approve.disabled = true;
      entry.approve.textContent = 'Approved';
      entry.reject.disabled = true;
      const mode = typeof msg.mode === 'string' ? msg.mode : '';
      entry.note.textContent =
        mode === ''
          ? 'Approved. Carrying the plan out.'
          : 'Approved in ' + (modeLabels[mode] || mode) + ' mode. Carrying the plan out.';
      return;
    }
    entry.approve.disabled = true;
    entry.approve.textContent = 'Not approved';
    entry.note.textContent = 'Kept in plan mode. Revise the request and send it again.';
  }

  // --- mode -----------------------------------------------------------------

  /**
   * Reflects the permission mode the extension is actually using. The menu is
   * driven from there, never from what was clicked: the extension can refuse a
   * change (a turn is running, the host said no), and a menu that kept the
   * refused value would be lying about what the next tool call will do.
   *
   * Two values travel, and they are not the same one. `mode` is the choice this
   * workspace has stored — empty when it has none, which the trigger shows as
   * "Workspace default" — and `resolved` is what the session runs under, which
   * the workspace's permissions.json may have supplied. The select shows the
   * first, because that is what clicking it would change; the label says the
   * second, because that is what the next tool call will actually do.
   */
  function showMode(msg) {
    if (!modeButton) return;
    const chosen = msg.mode || '';
    const resolved = msg.resolved || chosen || 'ask';
    modeButton.dataset.mode = chosen;
    if (modeLabel) {
      modeLabel.textContent = chosen === '' ? 'Workspace default' : modeLabels[chosen] || chosen;
    }
    for (const row of modeRows) {
      const selected = row.dataset.mode === chosen;
      row.classList.toggle('selected', selected);
      row.setAttribute('aria-checked', selected ? 'true' : 'false');
    }
    const resolvedLabel = modeLabels[resolved] || resolved;
    modeButton.title =
      chosen === '' ? 'Workspace default · currently ' + resolvedLabel : 'Agent mode · ' + resolvedLabel;

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
    auto: 'Auto',
    plan: 'Plan',
    dont_ask: 'No asking',
    bypass: 'Bypass',
  };

  function closeModeMenu(focusTrigger) {
    if (!modeMenu || modeMenu.hidden) return;
    modeMenu.hidden = true;
    modeButton.setAttribute('aria-expanded', 'false');
    if (focusTrigger) modeButton.focus();
  }

  function openModeMenu(fromKeyboard) {
    if (!modeMenu || !modeButton || modeButton.disabled) return;
    closeActionMenu(false);
    closeModelMenu(false);
    closeCollaborationMenu(false);
    modeMenu.hidden = false;
    modeButton.setAttribute('aria-expanded', 'true');
    if (fromKeyboard) {
      const selected = modeRows.find((row) => row.classList.contains('selected'));
      (selected || modeRows[0])?.focus();
    }
  }

  function toggleModeMenu(event) {
    if (!modeMenu || modeMenu.hidden) openModeMenu(Boolean(event && event.detail === 0));
    else closeModeMenu(true);
  }

  function closeModelMenu(focusTrigger) {
    if (!modelMenu || modelMenu.hidden) return;
    modelMenu.hidden = true;
    modelPicker?.setAttribute('aria-expanded', 'false');
    if (focusTrigger) modelPicker?.focus();
  }

  function positionModelMenu() {
    if (!modelMenu || !modelPicker || modelMenu.hidden) return;
    const trigger = modelPicker.getBoundingClientRect();
    const left = Math.max(8, Math.min(trigger.left, window.innerWidth - modelMenu.offsetWidth - 8));
    modelMenu.style.left = left + 'px';
    modelMenu.style.right = 'auto';
    modelMenu.style.bottom = Math.max(8, window.innerHeight - trigger.top + 6) + 'px';
  }

  function openModelMenu(fromKeyboard) {
    if (!modelMenu || !modelPicker || modelPicker.disabled) return;
    closeActionMenu(false);
    closeModeMenu(false);
    closeCollaborationMenu(false);
    closeSendModeMenu(false);
    modelMenu.hidden = false;
    modelPicker.setAttribute('aria-expanded', 'true');
    positionModelMenu();
    if (fromKeyboard) {
      (modelMenuList?.querySelector('[aria-checked="true"]') || modelMenuList?.querySelector('button'))?.focus();
    }
  }

  // --- collaboration and Agent Map -----------------------------------------

  function showCollaboration(msg) {
    collaborationMode = msg.mode === 'single' ? 'single' : 'multi';
    if (collaborationLabel) {
      collaborationLabel.textContent = collaborationMode === 'single' ? 'Single' : 'Multi';
    }
    for (const row of collaborationRows) {
      const selected = row.dataset.collaboration === collaborationMode;
      row.classList.toggle('selected', selected);
      row.setAttribute('aria-checked', selected ? 'true' : 'false');
    }
    if (agentMapButton) agentMapButton.hidden = collaborationMode !== 'multi';
    syncCollaborationActivity();
  }

  function closeCollaborationMenu(focusTrigger) {
    if (!collaborationMenu || collaborationMenu.hidden) return;
    collaborationMenu.hidden = true;
    collaborationButton.setAttribute('aria-expanded', 'false');
    if (focusTrigger) collaborationButton.focus();
  }

  function positionCollaborationMenu() {
    if (!collaborationMenu || !collaborationButton || collaborationMenu.hidden) return;
    const trigger = collaborationButton.getBoundingClientRect();
    const left = Math.max(8, Math.min(trigger.left, window.innerWidth - collaborationMenu.offsetWidth - 8));
    collaborationMenu.style.left = left + 'px';
    collaborationMenu.style.right = 'auto';
    collaborationMenu.style.bottom = Math.max(8, window.innerHeight - trigger.top + 6) + 'px';
  }

  function openCollaborationMenu(fromKeyboard) {
    if (!collaborationMenu || !collaborationButton || collaborationButton.disabled) return;
    closeActionMenu(false);
    closeModeMenu(false);
    closeModelMenu(false);
    collaborationMenu.hidden = false;
    collaborationButton.setAttribute('aria-expanded', 'true');
    positionCollaborationMenu();
    if (fromKeyboard) {
      const selected = collaborationRows.find((row) => row.classList.contains('selected'));
      (selected || collaborationRows[0])?.focus();
    }
  }

  function shortTokens(usage) {
    const total = usage && Number(usage.total_tokens || usage.totalTokens || 0);
    return total > 0 ? amount(total) + ' tokens' : 'no token data';
  }

  function elapsed(node) {
    const start = Date.parse(node.createdAt || '');
    if (!Number.isFinite(start)) return '';
    const end = Date.parse(node.endedAt || '') || Date.now();
    const seconds = Math.max(0, Math.floor((end - start) / 1000));
    if (seconds < 60) return seconds + 's';
    return Math.floor(seconds / 60) + 'm ' + (seconds % 60) + 's';
  }

  function agentCard(node, selected) {
    const card = el('button', 'agent-map-card status-' + node.status);
    card.type = 'button';
    card.dataset.agentId = node.agentId;
    card.setAttribute('aria-pressed', selected ? 'true' : 'false');
    const title = el('span', 'agent-map-card-title');
    title.appendChild(el('i', 'agent-map-status'));
    title.appendChild(el('strong', '', node.title));
    card.appendChild(title);
    card.appendChild(
      el(
        'small',
        '',
        [node.model, elapsed(node), shortTokens(node.usage)].filter((part) => part !== '').join(' · '),
      ),
    );
    card.addEventListener('click', () => {
      if (node.depth === 0) showAgentRootSummary(node);
      else showAgentDetailLoading(node);
    });
    return card;
  }

  function ensureAgentMapDialog() {
    if (agentMapDialog && agentMapDialog.isConnected) return agentMapDialog;
    const dialog = el('dialog', 'agent-map-dialog');
    dialog.setAttribute('aria-labelledby', 'agentMapTitle');
    const panel = el('div', 'agent-map-panel');
    const head = el('div', 'agent-map-head');
    head.appendChild(el('strong', '', 'Agent map')).id = 'agentMapTitle';
    const close = el('button', 'agent-map-close', '×');
    close.type = 'button';
    close.setAttribute('aria-label', 'Close Agent Map');
    close.addEventListener('click', () => dialog.close());
    head.appendChild(close);
    panel.appendChild(head);
    panel.appendChild(el('div', 'agent-map-content', 'Loading agents…'));
    dialog.appendChild(panel);
    dialog.addEventListener('click', (event) => {
      if (event.target === dialog) dialog.close();
    });
    dialog.addEventListener('close', () => {
      dialog.remove();
      agentMapDialog = null;
      agentMapState = null;
      if (agentMapButton) agentMapButton.focus();
    });
    document.body.appendChild(dialog);
    agentMapDialog = dialog;
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
    close.focus();
    return dialog;
  }

  function renderAgentMap(state) {
    agentMapState = state;
    const dialog = ensureAgentMapDialog();
    const content = dialog.querySelector('.agent-map-content');
    if (!content) return;
    content.textContent = '';
    const nodes = Array.isArray(state.nodes) ? state.nodes : [];
    const root = nodes.find((node) => node.agentId === state.rootId) || nodes[0];
    if (!root) {
      content.appendChild(el('p', 'agent-map-empty', 'No agents in this conversation yet.'));
      return;
    }
    const subtitle = el(
      'div',
      'agent-map-subtitle',
      state.counts.total + (state.counts.total === 1 ? ' agent' : ' agents') + ' · click an agent for details',
    );
    content.appendChild(subtitle);
    const topology = el('div', 'agent-map-topology');
    const rootColumn = el('div', 'agent-map-root');
    rootColumn.appendChild(agentCard(root, false));
    topology.appendChild(rootColumn);
    const children = el('div', 'agent-map-children');
    for (const node of nodes.filter((item) => item.depth > 0)) {
      const row = el('div', 'agent-map-child depth-' + Math.min(4, node.depth));
      row.appendChild(agentCard(node, false));
      children.appendChild(row);
    }
    if (children.childElementCount === 0) {
      children.appendChild(el('p', 'agent-map-empty', 'No subagents have been started.'));
    }
    topology.appendChild(children);
    content.appendChild(topology);
  }

  function showAgentRootSummary(root) {
    if (!agentMapDialog || !agentMapState) return;
    const content = agentMapDialog.querySelector('.agent-map-content');
    if (!content) return;
    content.textContent = '';
    const back = el('button', 'agent-map-back', '←  ' + root.title);
    back.type = 'button';
    back.addEventListener('click', () => renderAgentMap(agentMapState));
    content.appendChild(back);
    content.appendChild(el('p', 'agent-map-meta', root.status + ' · ' + root.model + ' · ' + shortTokens(root.usage)));
    const stats = el('div', 'agent-map-stats');
    for (const [label, value] of [
      ['Agents', agentMapState.counts.total],
      ['Working', agentMapState.counts.working],
      ['Finished', agentMapState.counts.finished],
      ['Failed', agentMapState.counts.failed],
    ]) {
      const row = el('div', 'agent-map-stat');
      row.appendChild(el('span', '', label));
      row.appendChild(el('strong', '', String(value)));
      stats.appendChild(row);
    }
    content.appendChild(stats);
  }

  function showAgentDetailLoading(node) {
    if (!agentMapDialog || !agentMapState) return;
    const content = agentMapDialog.querySelector('.agent-map-content');
    if (!content) return;
    content.textContent = '';
    const back = el('button', 'agent-map-back', '←  ' + node.title);
    back.type = 'button';
    back.addEventListener('click', () => renderAgentMap(agentMapState));
    content.appendChild(back);
    content.appendChild(
      el('p', 'agent-map-meta', [node.status, node.model, node.background ? 'background' : 'foreground'].join(' · ')),
    );
    content.appendChild(el('div', 'agent-map-detail-loading', 'Loading transcript…'));
    vscode.postMessage({ type: 'agentDetail', agentId: node.agentId });
  }

  function showAgentDetail(msg) {
    if (!agentMapDialog || !agentMapState) return;
    const content = agentMapDialog.querySelector('.agent-map-content');
    const node = agentMapState.nodes.find((item) => item.agentId === msg.agentId);
    if (!content || !node) return;
    content.textContent = '';
    const back = el('button', 'agent-map-back', '←  ' + node.title);
    back.type = 'button';
    back.addEventListener('click', () => renderAgentMap(agentMapState));
    content.appendChild(back);
    content.appendChild(
      el(
        'p',
        'agent-map-meta',
        [msg.status || node.status, node.model, msg.steps ? msg.steps + ' steps' : '', shortTokens(msg.usage || node.usage)]
          .filter((part) => part !== '')
          .join(' · '),
      ),
    );
    for (const [label, text] of [
      ['Prompt', msg.prompt],
      ['Result', msg.result],
    ]) {
      const sectionNode = el('section', 'agent-map-detail-section');
      sectionNode.appendChild(el('h3', '', label));
      sectionNode.appendChild(el('pre', '', text || '(none)'));
      content.appendChild(sectionNode);
    }
    const open = el('button', 'agent-map-open', 'Open transcript');
    open.type = 'button';
    open.addEventListener('click', () => vscode.postMessage({ type: 'openAgent', agentId: node.agentId }));
    content.appendChild(open);
  }

  // --- slash palette --------------------------------------------------------

  function closeActionMenu(focusTrigger) {
    if (!actionMenu || actionMenu.hidden) return;
    actionMenu.hidden = true;
    attachButton.setAttribute('aria-expanded', 'false');
    if (focusTrigger) attachButton.focus();
  }

  function openActionMenu(fromKeyboard) {
    if (!actionMenu) return;
    closeModeMenu(false);
    closeCollaborationMenu(false);
    closePalette();
    actionMenu.hidden = false;
    attachButton.setAttribute('aria-expanded', 'true');
    if (fromKeyboard) {
      const first = actionMenu.querySelector('button:not(:disabled)');
      if (first) first.focus();
    }
  }

  function toggleActionMenu(event) {
    if (!actionMenu || actionMenu.hidden) openActionMenu(Boolean(event && event.detail === 0));
    else closeActionMenu(true);
  }

  /**
   * The query the palette is open for: text starting with `/` and nothing else.
   * Once there is whitespace the command is settled and what follows is its
   * argument, so the palette gets out of the way.
   */
  function paletteQuery() {
    const trimmed = input.value.trim();
    if (trimmed.charAt(0) !== '/') return null;
    const body = trimmed.slice(1);
    if (/\s/.test(body)) return null;
    return body;
  }

  function quotePaletteQuery() {
    const match = /^\s*\/ref[ \t]+([^\r\n]*)$/i.exec(input.value);
    return match ? match[1].trim().toLowerCase() : null;
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
    quotePickerForced = false;
    palette.hidden = true;
    palette.textContent = '';
  }

  function paintPalette() {
    palette.textContent = '';
    for (let index = 0; index < paletteRows.length; index++) {
      const command = paletteRows[index];
      const row = el('button', 'palette-row' + (index === paletteIndex ? ' current' : ''));
      row.type = 'button';
      if (command.kind === 'quote') {
        row.appendChild(el('span', 'palette-name', command.role === 'user' ? 'you' : 'anvil'));
        row.appendChild(el('span', 'palette-desc', quotePreview(command.text, 110)));
        if (quotes.some((quote) => quote.id === command.id)) row.appendChild(el('span', 'palette-tag', 'added'));
      } else {
        row.appendChild(el('span', 'palette-name', '/' + command.name));
        row.appendChild(el('span', 'palette-desc', command.description || ''));
        if (command.source === 'skill') row.appendChild(el('span', 'palette-tag', 'skill'));
      }
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
    const quoteQuery = quotePickerForced ? '' : quotePaletteQuery();
    if (quoteQuery !== null) {
      const needle = quoteQuery.toLowerCase();
      paletteRows = quoteCandidates
        .slice()
        .reverse()
        .filter((quote) => {
          const speaker = quote.role === 'user' ? 'you' : 'anvil';
          return needle === '' || speaker.indexOf(needle) !== -1 || quote.text.toLowerCase().indexOf(needle) !== -1;
        })
        .slice(0, 10)
        .map((quote) => ({ ...quote, kind: 'quote' }));
      if (paletteIndex >= paletteRows.length) paletteIndex = 0;
      paintPalette();
      return;
    }
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
    const commands = catalogue.some((command) => command.name === 'ref')
      ? catalogue
      : [
          { name: 'ref', description: 'Quote an earlier message', source: 'builtin', local: true },
          ...catalogue,
        ];
    paletteRows = paletteMatches(commands, query);
    if (paletteIndex >= paletteRows.length) paletteIndex = 0;
    paintPalette();
  }

  /** Puts the command name in the composer; what follows is its arguments. */
  function acceptPalette(index) {
    const command = paletteRows[index];
    closePalette();
    if (!command) return;
    if (command.kind === 'quote') {
      if (/^\s*\/ref(?:[ \t].*)?$/i.test(input.value)) input.value = '';
      addQuote(command);
      growInput();
      return;
    }
    input.value = '/' + command.name + ' ';
    growInput();
    input.focus();
    if (command.name === 'ref') updatePalette();
  }

  function openQuotePicker() {
    closeActionMenu(false);
    closeModeMenu(false);
    closePalette();
    quotePickerForced = true;
    paletteIndex = 0;
    updatePalette();
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

  // --- mcp ------------------------------------------------------------------

  /** The card the last `mcp` message drew, or null before one arrived. */
  let mcpCard = null;

  /**
   * What the enabled MCP layers declare, and — from a host with the runtime —
   * what became of each.
   *
   * The state word and the tool count are the host's; this only draws them. A
   * host from before 0.44 sends no `runtime` and no state, and the card stays the
   * configuration listing it always was: a dot there would mean "the file
   * mentioned it", which is not the thing a person cares about.
   */
  function showMcp(msg) {
    const card = el('div', 'commands mcp');
    card.appendChild(el('div', 'head', 'MCP servers'));
    const error = typeof msg.error === 'string' ? msg.error : '';
    if (error !== '') {
      card.appendChild(el('div', 'command failed', 'Could not read the MCP configuration: ' + error));
      append(card);
      endRound();
      return;
    }
    const runtime = msg.runtime === true;
    card.dataset.runtime = runtime ? '1' : '0';
    const servers = Array.isArray(msg.servers) ? msg.servers : [];
    for (const server of servers) {
      const row = mcpRow(server, runtime);
      card.appendChild(row);
      const reason = mcpErrorText(server);
      if (reason !== '') card.appendChild(mcpErrorRow(reason));
    }
    if (servers.length === 0) {
      card.appendChild(el('div', 'command muted', 'No MCP servers are configured.'));
    }
    // A file that will not parse is the one thing here the user can fix, and the
    // host still answered with whatever it last observed.
    const problem = typeof msg.problem === 'string' ? msg.problem : '';
    if (problem !== '') {
      card.appendChild(el('div', 'command failed', 'Could not read the MCP configuration: ' + problem));
    }
    // Layers that are enabled but have no file. The path is the one the host
    // resolved, so it can be opened or created as printed.
    const notes = Array.isArray(msg.notes) ? msg.notes : [];
    for (const note of notes) {
      if (typeof note === 'string' && note !== '') card.appendChild(el('div', 'command muted', note));
    }
    // The card a later status is drawn onto. One per document: a second card
    // would be a second conversation, and each document has its own here.
    mcpCard = card;
    append(card);
    endRound();
  }

  /**
   * One server's row: dot, name, file, transport, and the state in words.
   *
   * A legacy host gets the row it always got, with no dot and no word — not an
   * empty span, which would take a gap in the row and shift everything after it.
   */
  function mcpRow(server, runtime) {
    const row = el('div', 'command');
    const name = typeof server.name === 'string' ? server.name : '';
    row.dataset.name = name;
    const state = typeof server.state === 'string' ? server.state : '';
    if (runtime && state !== '') {
      row.appendChild(el('span', 'mcp-dot ' + state, '●'));
      row.classList.add('mcp-' + state);
    }
    row.appendChild(el('span', 'cmd', name));
    row.appendChild(el('span', 'desc', typeof server.source === 'string' ? server.source : ''));
    if (typeof server.transport === 'string' && server.transport !== '') {
      row.appendChild(el('span', 'tag', server.transport));
    }
    if (runtime) {
      const text = mcpStateText(server);
      if (text !== '') row.appendChild(el('span', 'desc mcp-state', text));
    }
    return row;
  }

  /**
   * The state in words, with the count only while it is a fact.
   *
   * A failed server's last tool count would read as though it still had them, so
   * the number appears with `connected` and with nothing else.
   */
  function mcpStateText(server) {
    const label = typeof server.state_label === 'string' ? server.state_label : '';
    if (label === '') return '';
    if (server.state !== 'connected') return label;
    const tools = typeof server.tool_count === 'number' ? server.tool_count : 0;
    return label + ' · ' + tools + (tools === 1 ? ' tool' : ' tools');
  }

  /** The line under a row that says why it is not working, if it is not. */
  function mcpErrorText(server) {
    const text = typeof server.error === 'string' ? server.error : '';
    // Only a state that is not working has a reason worth the extra line; a
    // connected server carries none, and a disabled one is disabled on purpose.
    if (!(server.state === 'failed' || server.state === 'auth_required')) return '';
    return text;
  }

  /**
   * The reason as its own line.
   *
   * It is text, never markup: the host builds it from a child process's stderr.
   */
  function mcpErrorRow(text) {
    const node = el('div', 'command muted mcp-error', text);
    return node;
  }

  /**
   * A state change lands on the row that already names the server.
   *
   * A server the card has no row for is one added after it was drawn — from the
   * manager, in another tab — and the card grows a row rather than ignoring the
   * news. A document with no card at all has nothing to update: its own listing
   * will say the same thing when it arrives.
   */
  function updateMcp(server) {
    if (!mcpCard) return;
    const name = typeof server.name === 'string' ? server.name : '';
    if (name === '' || mcpCard.dataset.runtime !== '1') return;
    let row = null;
    for (const child of mcpCard.children) {
      if (child.dataset && child.dataset.name === name && child.classList.contains('command')) {
        row = child;
        break;
      }
    }
    const reason = mcpErrorText(server);
    const errorRow = row && row.nextElementSibling && row.nextElementSibling.classList.contains('mcp-error')
      ? row.nextElementSibling
      : null;
    if (errorRow) mcpCard.removeChild(errorRow);
    const fresh = mcpRow(server, true);
    if (row) {
      mcpCard.replaceChild(fresh, row);
    } else {
      mcpCard.appendChild(fresh);
    }
    if (reason !== '') mcpCard.insertBefore(mcpErrorRow(reason), fresh.nextSibling);
  }

  // --- web search -----------------------------------------------------------

  /**
   * The web tools this session was given, and whether a key is stored for them.
   *
   * Posted only when the setting is on, so the row's absence is the "off"
   * answer. The mcp provider is drawn as what it is: no built-in tools, and
   * whatever search a workspace MCP server supplies.
   */
  function showWebSearch(msg) {
    const card = el('div', 'commands websearch');
    card.appendChild(el('div', 'head', 'Web tools'));
    const provider = msg.provider === 'mcp' ? 'mcp' : 'http';
    if (provider === 'mcp') {
      card.appendChild(
        el(
          'div',
          'command muted',
          'No built-in web tools: search is expected from an MCP server this workspace declares.',
        ),
      );
      append(card);
      endRound();
      return;
    }
    const row = el('div', 'command');
    row.appendChild(el('span', 'cmd', 'web_search, fetch_url'));
    const hasKey = msg.key === true;
    row.appendChild(el('span', 'desc', hasKey ? 'API key is set' : 'no API key is set'));
    if (!hasKey) row.classList.add('failed');
    card.appendChild(row);
    if (!hasKey) {
      card.appendChild(
        el('div', 'command muted', 'Run "Anvil: Set Web Search Key" to store one for the search endpoint.'),
      );
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


  function closeSendModeMenu(restoreFocus) {
    if (!sendModeMenu || !sendModeButton) return;
    sendModeMenu.hidden = true;
    composer.classList.remove('send-menu-open');
    sendModeButton.setAttribute('aria-expanded', 'false');
    if (restoreFocus) sendModeButton.focus();
  }

  function updateSendMode() {
    for (const row of sendModeRows) {
      row.setAttribute('aria-checked', String(row.dataset.sendMode === runningSendMode));
    }
    if (!sendModeButton) return;
    const label = runningSendMode === 'steer' ? 'Steer' : 'Enqueue';
    sendModeButton.title = 'Send behavior: ' + label;
    sendModeButton.setAttribute('aria-label', 'Send behavior: ' + label);
  }

  function toggleSendModeMenu() {
    if (!sendModeMenu || !sendModeButton) return;
    const opening = sendModeMenu.hidden;
    sendModeMenu.hidden = !opening;
    composer.classList.toggle('send-menu-open', opening);
    sendModeButton.setAttribute('aria-expanded', String(opening));
    if (opening) {
      closeActionMenu(false);
      closeModeMenu(false);
      closeCollaborationMenu(false);
      sendModeRows.find((row) => row.dataset.sendMode === runningSendMode)?.focus();
    }
  }

  function setRunning(next) {
    running = next;
    // The line says what is happening for exactly as long as something is:
    // starting it here rather than on the first event also covers a turn that a
    // reopened tab found already in flight.
    if (running) startWorking();
    else stopWorking();
    sendButton.classList.toggle('running', running);
    const action = runningSendMode === 'steer' ? 'Interrupt and steer the current task' : 'Enqueue after current response';
    sendButton.setAttribute('aria-label', running ? action : 'Send');
    sendButton.title = running ? action : 'Send';
    if (stopButton) {
      stopButton.hidden = !running;
      stopButton.disabled = false;
    }
    // Model and permission mode describe the next composer turn. During a run
    // they remain available so an Enqueue or Steer message can capture them;
    // the extension applies the snapshot only after this run is terminal.
    if (modeButton) modeButton.disabled = false;
    if (collaborationButton) collaborationButton.disabled = running;
    if (modelPicker) modelPicker.disabled = false;
    if (running) closeCollaborationMenu(false);
    if (!running) closeSendModeMenu(false);
    input.focus();
  }

  function showQueue(msg) {
    const count = Number.isFinite(msg.count) ? Math.max(0, Math.floor(msg.count)) : 0;
    if (queueCount) {
      queueCount.textContent = String(count);
      queueCount.hidden = count === 0;
    }
    const suffix = count > 0 ? ' · ' + count + ' queued' : '';
    const action = runningSendMode === 'steer' ? 'Interrupt and steer the current task' : 'Enqueue after current response';
    sendButton.title = running ? action + suffix : 'Send';
  }

  function showModels(msg) {
    if (!modelMenuList || !modelPickerLabel) return;
    const available = Array.isArray(msg.available) ? msg.available : [];
    const current = typeof msg.current === 'string' ? msg.current : '';
    const defaultModel = typeof msg.defaultModel === 'string' ? msg.defaultModel : '';
    const selected = current === defaultModel ? '' : current;
    modelMenuList.replaceChildren();
    const addModel = (value, title, detail) => {
      const row = el('button', 'model-menu-row');
      row.type = 'button';
      row.setAttribute('role', 'radio');
      row.setAttribute('aria-checked', value === selected ? 'true' : 'false');
      row.dataset.model = value;
      const copy = el('span', 'model-menu-copy');
      copy.appendChild(el('strong', '', title));
      copy.appendChild(el('small', '', detail));
      row.appendChild(copy);
      row.appendChild(el('span', 'model-menu-check', value === selected ? '✓' : ''));
      row.addEventListener('click', () => {
        vscode.postMessage({ type: 'selectModel', model: value });
        closeModelMenu(true);
      });
      modelMenuList.appendChild(row);
    };
    addModel(
      '',
      'Default (recommended)',
      defaultModel ? 'Use the workspace default · ' + defaultModel : 'Use the host default model',
    );
    for (const id of available) {
      if (typeof id !== 'string' || id === '') continue;
      addModel(id, id, id === defaultModel ? 'Workspace default model' : 'Available model');
    }
    modelPickerLabel.textContent =
      selected === ''
        ? defaultModel ? 'Default · ' + defaultModel : 'Default'
        : selected;
    modelPicker.title = current ? 'Model: ' + current : 'Model: host default';
  }

  function showEffort(msg) {
    const effort = msg.effort === 'low' || msg.effort === 'high' ? msg.effort : 'medium';
    const label = effort[0].toUpperCase() + effort.slice(1);
    if (effortMenuLabel) {
      effortMenuLabel.replaceChildren(document.createTextNode('Effort '), el('span', '', '(' + label + ')'));
    }
    for (const button of effortButtons) {
      button.setAttribute('aria-checked', button.dataset.effort === effort ? 'true' : 'false');
    }
  }

  function dispatchQueued(msg) {
    const sentMentions = Array.isArray(msg.mentions) ? msg.mentions : [];
    const sentImages = Array.isArray(msg.images) ? msg.images : [];
    const sentQuotes = Array.isArray(msg.quotes) ? msg.quotes : [];
    addUser(msg.text || '', sentMentions, sentImages, sentQuotes);
    setStatus('');
    todoEpoch += 1;
    setRunning(true);
  }

  /**
   * Adds one finished run to the session's totals and redraws the meter.
   *
   * The run's own usage is the only honest input: an `error` event may carry
   * usage, but the `done` that follows it is authoritative and arrives once,
   * so counting both would count the same run twice.
   */
  function recordUsage(done) {
    liveUsage = null;
    meterMath.add(usageTotals, done);
    recordContext(done);
    renderMeter();
  }

  /** Replaces the current run's estimate; it is never added to stored totals. */
  function recordLiveUsage(event) {
    if (!event || event.usage_estimated !== true || !event.usage) return;
    liveUsage = event.usage;
    renderMeter();
  }

  /**
   * Keeps the two per-run facts the meter needs beyond the counts: the host's
   * estimate of the conversation, and when the run last reached the provider.
   *
   * An absent estimate leaves the previous one alone — the conversation has not
   * changed — but an absent anchor is left alone too, on purpose: a run that
   * never reached the provider did not refresh the cache, so the previous
   * request is still the most recent one and its countdown goes on aging.
   */
  function recordContext(done) {
    if (!done) return;
    if (done.context && typeof done.context === 'object') contextInfo = done.context;
    if (typeof done.last_request_at === 'string' && done.last_request_at !== '') {
      const at = Date.parse(done.last_request_at);
      if (isFinite(at)) lastRequestAt = at;
    }
  }

  /** Forgets the session's totals, for a session that is replacing this one. */
  function resetUsage() {
    usageTotals = meterMath.empty();
    liveUsage = null;
    contextInfo = null;
    lastRequestAt = null;
    renderMeter();
  }

  /**
   * Draws the current run's live counts and the conversation's context facts.
   *
   * The estimated input and output for the active run sit immediately below
   * the hammer/status line in the transcript footprint (p_0.46.28). The
   * conversation's context share remains on the root line below them; provider
   * cache accounting waits for the settled run summary.
   *
   * Context comes from the host's estimate against the declared window; before
   * the host has answered, the last run's total stands in, because it is
   * measured rather than guessed. Cache comes from the last run's reported
   * cached input and the TTL the client was told, counted from the run's own
   * last request — so the line says "the cache is warm for this long", not
   * "something is happening".
   */
  function renderMeter() {
    if (!meter) return;
    const pinned = atBottom();
    const countsBefore = meterCounts.map((node) => numberText.get(node) || '').join('\u0000');
    // This conversation: its estimated use against the window it declared.
    const facts = [];
    const context = meterMath.remaining(usageTotals, contextInfo, autoCompactAt);
    if (context) {
      facts.push(
        amount(context.used) +
          ' of ' +
          amount(context.window) +
          ' (' +
          meterMath.percent(context.used, context.window) +
          '%)',
      );
      facts.push(amount(context.left) + ' left');
      if (context.compactAt > 0) facts.push('compact at ' + amount(context.compactAt));
    }
    // The root line is inside the transcript, so a change to it has to keep a
    // reader who was at the bottom at the bottom. Only a change: this function
    // runs once a second while a cache is live, and pinning on every tick would
    // drag a reader who had scrolled up within the 60px tolerance back down,
    // once a second, for a countdown that did not move the conversation.
    const factsText = facts.length > 0 ? ' · ' + facts.join(' · ') : '';
    if (rootFacts && rootFacts.textContent !== factsText) {
      const pinned = atBottom();
      rootFacts.textContent = factsText;
      scrollIfPinned(pinned);
    }
    // The footprint and the settled status describe one run. The runtime's
    // estimate is already cumulative across that run's model rounds, so adding
    // completed conversation totals here mixes scopes and makes the live number
    // jump above the provider total it must converge on.
    const hasLive = liveUsage !== null;
    const input = meterMath.count(liveUsage && liveUsage.input_tokens);
    const output = meterMath.count(liveUsage && liveUsage.output_tokens);
    // All three counts are drawn whenever the provider reported them — in, out
    // and cache are one sentence about the conversation, and
    // dropping in/out because a context window happened to be declared left
    // the line saying only 'cache' (p_0.46.26).
    const cache = meterMath.cache(usageTotals, cacheTtlSeconds, lastRequestAt, Date.now());
    // A count is only known once a run has ended, so during a conversation's
    // first turn all three are unknown. The line keeps its place rather than
    // vanishing, because its absence read as "this build has no meter" rather
    // than "nothing has been counted yet" (p_0.46.26). An em dash is the one
    // spelling that cannot be mistaken for a count — and it keeps the count's
    // name, so the line says which fact is not known yet.
    const pending = input <= 0 && output <= 0;
    const countText = (value, suffix, label) => {
      const name = label ? label + ' ' : '';
      if (value > 0) return name + amount(value) + suffix;
      return pending ? name + '\u2014' : '';
    };
    if (input > 0 || hasLive) rollNumber(meterIn, input, '\u00a0in', '', false);
    else setNumber(meterIn, countText(input, ' in', ''));
    // A live request starts at zero output and then moves; keeping the zero
    // visible makes the first streamed increment a roll rather than an appear.
    if (output > 0 || hasLive) rollNumber(meterOut, output, '\u00a0out', '', false);
    else setNumber(meterOut, countText(output, ' out', ''));
    // Cache is provider accounting, not a live estimate. It appears in the
    // terminal run summary once the provider has settled the request.
    setNumber(meterCache, hasLive ? '' : countText(cache.cached, '', 'cache'));
    // The host's context estimate against the window. Provider input totals
    // may accumulate across model rounds and can be much larger than the
    // context actually sent, so they are only the older-host fallback inside
    // `meterMath.label`.
    const spent = meterMath.label(usageTotals, contextWindow, contextInfo);
    const share = spent
      ? spent
          .split(' · ')
          .filter((part) => !/ (in|out)$/.test(part))
          .join(' · ')
      : '';
    setNumber(meterShare, share);
    // Visibility follows the value each node holds, so a count hidden here
    // cannot reappear in the line through another path.
    for (const node of meterCounts) node.hidden = (numberText.get(node) || '') === '';
    // A dot belongs to the count in front of it, and is shown only while that
    // count is visible and another count is still visible after it — so each
    // visible count but the last is followed by exactly one dot, and hiding a
    // count in the middle cannot leave two dots in a row or none at all.
    const dots = [meterDot1, meterDot2, meterDot3];
    const lastShown = meterCounts.map((node) => !node.hidden).lastIndexOf(true);
    for (let i = 0; i < dots.length; i += 1) {
      dots[i].hidden = meterCounts[i].hidden || i >= lastShown;
    }
    // The cache clock is written into a node appended to the cache count
    // rather than to the line, so its per-second tick does not pulse the count
    // it sits beside.
    meterClock.textContent = '';
    if (!hasLive && cache.cached > 0 && cache.remaining !== null) {
      meterClock.textContent =
        ' · ' + (cache.remaining > 0 ? meterMath.clock(cache.remaining) : 'cold');
      meterCache.appendChild(meterClock);
    }
    // The line is assembled from the nodes, not recomputed: a node that was
    // hidden for lack of a count is absent here for the same reason, and the
    // placeholder that stands in during a first turn is a count's absence, not
    // a separate branch (p_0.46.26).
    const parts = [];
    for (const node of meterCounts) if (!node.hidden) parts.push(node.textContent);
    const text = parts.join(' · ');
    // The meter is the live face. Once the run settles, runSummary owns these
    // facts in one line and this duplicate disappears.
    meter.hidden = text === '' || !hasLive;
    // The row may have truncating width: the whole line stays reachable.
    meter.title = text;
    meter.setAttribute('aria-label', text);
    syncFootprint();
    const countsAfter = meterCounts.map((node) => numberText.get(node) || '').join('\u0000');
    // A changed count grows the footprint, so keep a reader already at the end
    // at the end. The cache clock is deliberately excluded: its one-second
    // tick must never pull a reader who scrolled up.
    if (countsBefore !== countsAfter) scrollIfPinned(pinned);
    syncCacheClock(cache, hasLive);
  }

  /**
   * Runs the one-second clock only while there is something to count: a known
   * TTL, a run to count from, a cache worth watching, life left in it, and a tab
   * the person can actually see. Everything else on the line moves on an event,
   * so anything more than this would be a timer redrawing static text.
   */
  function syncCacheClock(cache, hasLive) {
    const wanted =
      cacheTtlSeconds > 0 &&
      lastRequestAt !== null &&
      cache.cached > 0 &&
      cache.remaining !== null &&
      cache.remaining > 0 &&
      !hasLive &&
      !meter.hidden &&
      !document.hidden;
    if (wanted && cacheClock === null) {
      cacheClock = setInterval(renderMeter, 1000);
    } else if (!wanted && cacheClock !== null) {
      clearInterval(cacheClock);
      cacheClock = null;
    }
  }

  // A hidden tab stops counting; coming back draws the truth at once, and
  // `renderMeter` starts the clock again only if the TTL has time left.
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) renderMeter();
  });

  /** The mark a status draws. An unknown status gets a plain one, not a guess. */
  const todoMarks = { pending: '○', in_progress: '◐', done: '✓' };

  /** At most this many distinct revisions from the retained conversation. */
  const TODO_HISTORY_LIMIT = 20;
  /** Oldest first. A status-only update replaces one entry in place. */
  const todoHistoryEntries = [];
  let currentTodoKey = '';
  let todoEpoch = 0;

  function setTodosCollapsed(collapsed) {
    if (!todoBlock || !todosBody || !todosToggle) return;
    todoBlock.classList.toggle('collapsed', collapsed);
    todosBody.hidden = collapsed;
    todosToggle.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
    todosToggle.setAttribute('aria-label', collapsed ? 'Expand plan' : 'Collapse plan');
    todosToggle.title = collapsed ? 'Expand plan' : 'Collapse plan';
    requestAnimationFrame(syncComposerHeight);
  }

  function drawTodoRows(container, items) {
    container.replaceChildren();
    for (const item of items) {
      const row = el('div', 'todo-row');
      row.setAttribute('role', 'listitem');
      if (todoMarks[item.status]) row.dataset.status = item.status;
      row.appendChild(el('span', 'todo-mark', todoMarks[item.status] || '·'));
      row.appendChild(el('span', 'todo-text', item.text));
      container.appendChild(row);
    }
  }

  /** Draws every saved plan except the one already visible as Current. */
  function renderTodoHistory() {
    if (!todoHistory || !todoHistoryList || !todoHistorySummary) return;
    todoHistoryList.replaceChildren();
    const past = todoHistoryEntries
      .map((entry, index) => ({ entry, number: index + 1 }))
      .filter(({ entry }) => entry.key !== currentTodoKey)
      .reverse();
    todoHistory.hidden = past.length === 0;
    todoHistorySummary.textContent = `History (${past.length})`;
    for (const { entry, number } of past) {
      const details = el('details', 'todo-history-entry');
      const summary = el('summary', 'todo-history-entry-summary');
      summary.appendChild(el('span', 'todo-history-number', `Plan ${number}`));
      summary.appendChild(el('span', 'todo-history-title', entry.items[0].text));
      const done = entry.items.filter((item) => item.status === 'done').length;
      summary.appendChild(el('span', 'todo-history-progress', `${done}/${entry.items.length}`));
      details.appendChild(summary);
      const rows = el('div', 'todo-history-rows');
      rows.setAttribute('role', 'list');
      rows.setAttribute('aria-label', `Plan ${number}`);
      drawTodoRows(rows, entry.items);
      details.appendChild(rows);
      todoHistoryList.appendChild(details);
    }
    if (todosCount) {
      todosCount.textContent = todoHistoryEntries.length > 1
        ? `${todoHistoryEntries.length} saved`
        : '';
    }
  }

  /**
   * Replaces Current and records distinct revisions. The run id makes replay
   * reconstruct the same boundaries as the live stream; the epoch is a
   * fallback for an older host that omitted it.
   */
  function setTodos(items, runId) {
    if (!todoBlock || !todoList) return;
    const list = (Array.isArray(items) ? items : [])
      .map((item) => ({
        text: item && typeof item.text === 'string' ? item.text.trim() : '',
        status: item && typeof item.status === 'string' ? item.status : '',
      }))
      .filter((item) => item.text !== '');
    if (list.length === 0) {
      currentTodoKey = '';
      drawTodoRows(todoList, []);
      todoList.hidden = true;
      if (todoCurrentLabel) todoCurrentLabel.hidden = true;
      if (todoEmpty) todoEmpty.hidden = false;
      renderTodoHistory();
      todoBlock.hidden = todoHistoryEntries.length === 0;
      return;
    }
    const signature = list.map((item) => item.text).join('\n');
    const owner = typeof runId === 'string' && runId !== '' ? runId : `turn:${todoEpoch}`;
    const key = `${owner}\n${signature}`;
    const existing = todoHistoryEntries.find((entry) => entry.key === key);
    if (existing) existing.items = list;
    else {
      todoHistoryEntries.push({ key, items: list });
      if (todoHistoryEntries.length > TODO_HISTORY_LIMIT) todoHistoryEntries.shift();
      if (!replaying) setTodosCollapsed(false);
    }
    currentTodoKey = key;
    todoList.hidden = false;
    if (todoCurrentLabel) todoCurrentLabel.hidden = false;
    if (todoEmpty) todoEmpty.hidden = true;
    drawTodoRows(todoList, list);
    renderTodoHistory();
    todoBlock.hidden = false;
  }

  /** A switched conversation must not inherit the old one's plan archive. */
  function clearTodoHistory() {
    todoHistoryEntries.length = 0;
    currentTodoKey = '';
    if (todoList) drawTodoRows(todoList, []);
    if (todoHistoryList) todoHistoryList.replaceChildren();
    if (todoHistory) {
      todoHistory.hidden = true;
      todoHistory.open = false;
    }
    if (todoEmpty) todoEmpty.hidden = true;
    if (todoCurrentLabel) todoCurrentLabel.hidden = false;
    if (todosCount) todosCount.textContent = '';
    setTodosCollapsed(true);
    if (todoBlock) todoBlock.hidden = true;
  }

  /**
   * What a finished run is worth saying: how many steps, and what it cost. One
   * function because two places say it — the status line for the run in flight,
   * and the marker a replayed one leaves in the transcript — and a run's steps
   * counted one way in one of them and another way in the other would be two
   * answers to one question.
   */
  function runSummary(done) {
    const parts = [];
    const steps = done && typeof done.steps === 'number' && done.steps > 0
      ? `${done.steps} step${done.steps === 1 ? '' : 's'}`
      : '';
    if (steps) parts.push(steps);
    const usage = (done && done.usage) || null;
    if (usage) {
      const inputTokens = meterMath.count(usage.input_tokens);
      const outputTokens = meterMath.count(usage.output_tokens);
      const totalTokens = meterMath.count(usage.total_tokens) || inputTokens + outputTokens;
      const total = count(totalTokens);
      if (total) {
        const split = inputTokens || outputTokens
          ? ` (${count(inputTokens) || '0'} in / ${count(outputTokens) || '0'} out)`
          : '';
        parts.push(`${total} tokens${split}`);
      }
      const cachedTokens = meterMath.count(usage.cached_tokens);
      if (cachedTokens > 0 && inputTokens > 0) {
        parts.push(`${Math.min(100, Math.round((cachedTokens / inputTokens) * 100))}% cache`);
      }
    }
    return parts;
  }

  function finishRun(done, failure) {
    const round = currentRound();
    endRound();
    setRunning(false);
    // The run is over, so nothing in it can still be in flight. A card left
    // reading "running…" is a call whose result never arrived, and leaving it
    // in flight forever reads as a stuck turn rather than as a lost event.
    stopRunningCards();
    // The answer that ended the turn is its summary. A run that ended on a tool
    // call has no concluding text, and a failed or stopped run's last words are
    // not a conclusion, so both keep the ending they had. `failure` is always
    // present — a clean finish is `{kind: 'none'}` — so the kind is what says
    // the run finished, not the object's absence.
    if (!failure || failure.kind === 'none') markSummary(round, done && done.run_id);
    finishReply();
    const parts = runSummary(done);
    recordUsage(done);
    setStatus(parts.join(' · '));
    // "Stopped." is not a failure, but it is still something the user asked
    // for and should see once: a run that ends silently reads as a hang.
    if (failure && failure.message) addNotice(failure.message);
    // The end of the turn is the one fact a reader should not have to discover
    // by tabbing around. It is said after the last block, so the two arrive in
    // the order they happened.
    if (failure && failure.isError) announce('Turn ended with an error.');
    else announce(failure && failure.kind === 'cancelled' ? 'Turn stopped.' : 'Answer finished.');
    noteRun(done && done.run_id);
    showRunActions(done && done.run_id);
  }

  /**
   * Where a replayed run ended, drawn in the transcript rather than on the
   * status line: a replay shows several runs at once, and a line at the bottom
   * could only report the last of them.
   *
   * The status is a word, not a colour. Newer stores also keep a terminal
   * failure reason; showing it here makes a reopened failed run as actionable
   * as the same run was while it was live.
   */
  function addRunEnd(done) {
    const round = currentRound();
    endRound();
    stopRunningCards();
    recordUsage(done);
    const status = done && done.status;
    const word = status === 'error' ? 'Run failed' : status === 'cancelled' ? 'Run stopped' : 'Run finished';
    // Before the run-end line is appended: the summary has to still be the last
    // block the run drew, and the line is the replay's ending, not its content.
    if (status !== 'error' && status !== 'cancelled') markSummary(round, done && done.run_id);
    finishReply();
    if (status === 'error' && done && done.err) addNotice(done.err);
    const line = el('div', 'msg run-end');
    line.appendChild(el('div', 'body', [word].concat(runSummary(done)).join(' · ')));
    append(line);
    noteRun(done && done.run_id);
    showRunActions(done && done.run_id);
  }

  /**
   * The first line of a final answer, as the summary's title.
   *
   * The model has already written its conclusion, so the title is that line
   * with its Markdown taken off rather than a second sentence invented from the
   * prompt. A line that is syntax — a fence, a rule, a table — has no title, and
   * the answer keeps its ordinary ending.
   */
  function summaryTitle(line) {
    const text = String(line || '').trim();
    if (text === '') return '';
    if (/^(`{3,}|~{3,})/.test(text)) return '';
    if (/^[-*_]{3,}$/.test(text)) return '';
    if (text.startsWith('|')) return '';
    const title = text
      .replace(/^#{1,6}\s*/, '')
      .replace(/^>\s*/, '')
      .replace(/^[-*+]\s+/, '')
      .replace(/^\d+[.)]\s+/, '')
      .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/[*_`~]/g, '')
      .replace(/[：:]\s*$/, '')
      .trim();
    if (title === '') return '';
    return title.length > 80 ? title.slice(0, 79) + '…' : title;
  }

  /** A final answer cut into the title its header carries and the body below it. */
  function summaryParts(text) {
    const lines = String(text || '').split(/\r?\n/);
    let i = 0;
    while (i < lines.length && lines[i].trim() === '') i += 1;
    if (i >= lines.length) return null;
    const title = summaryTitle(lines[i]);
    if (title === '') return null;
    // A clipped title is only a preview of a prose first line, not a
    // replacement for it. Keep that complete line in the rendered body so the
    // visual transcript contains the same answer as Copy.
    const bodyFrom = title.endsWith('…') ? i : i + 1;
    const body = lines.slice(bodyFrom).join('\n').replace(/^(?:[ \t]*\r?\n)+/, '');
    return { title, body };
  }

  /** Whether this block is the last thing its run put on screen. */
  function isLastBlock(node) {
    let next = node.nextElementSibling;
    while (next && next.classList.contains('output-link')) next = next.nextElementSibling;
    return next === null;
  }

  /**
   * Marks the answer that ended a finished turn as its summary.
   *
   * Only a round that is still the last block qualifies: a run that ended on a
   * tool call has no concluding text, and the prose before that call is not one.
   * The header replaces the speaker's name with the end dot, the word and the
   * answer's first line; the body is re-rendered without that line, so the
   * title is said once. The Copy action already holds the whole answer, because
   * `endRound` copied it before this ran.
   */
  function markSummary(round, runId) {
    if (!round || !round.wrap || !round.body) return false;
    if (round.text.trim() === '') return false;
    if (!isLastBlock(round.wrap)) return false;
    const parts = summaryParts(round.text);
    if (!parts) return false;
    const who = round.wrap.querySelector('.who');
    if (!who) return false;
    who.textContent = '';
    who.appendChild(el('span', 'end-dot'));
    who.appendChild(el('span', 'summary-label', 'Summary'));
    who.appendChild(el('span', 'summary-title', parts.title));
    round.wrap.classList.add('summary');
    round.text = parts.body;
    round.md = { node: round.body, settled: [], offset: 0, rendered: 0 };
    round.body.textContent = '';
    paintMarkdown(round.md, round.text);
    // The folded face of the turn is the line that now titles its ending, so a
    // column of closed replies reads as the conclusions that closed them. A
    // turn whose run did not conclude keeps an empty hint — no title is
    // invented for it.
    const turn = round.wrap.closest('.reply');
    const hint = turn ? turn.querySelector('.reply-hint') : null;
    if (hint) hint.textContent = parts.title;
    const body = round.wrap.parentElement;
    if (turn && turn.parentElement && body) {
      // Keep the conclusion next to its turn, but outside the fold containing
      // the work that led to it.
      turn.parentElement.insertBefore(round.wrap, turn.nextSibling);
      round.wrap.classList.remove('rail', 'rail-up', 'rail-down');
      syncRail(body);
    } else {
      syncRail(body);
    }
    if (typeof runId === 'string' && runId !== '') runSummaries.set(runId, round.wrap);
    return true;
  }

  /**
   * Marks every card still waiting for a result as stopped.
   *
   * Structural rather than textual: a card that has an ending has one of the two
   * classes the result handler adds, and a card with neither was never told how
   * its call went. Its reviewer is not offered, because the button's rule is
   * that a change which did not land is not a change — and whether this one
   * landed is exactly what is unknown.
   */
  function stopRunningCards() {
    for (const card of toolCards.values()) {
      if (card.classList.contains('done') || card.classList.contains('failed')) continue;
      const state = card.querySelector('.state');
      if (state) state.textContent = 'stopped';
      card.classList.add('stopped');
    }
  }

  function showBanner(level, message) {
    banner.textContent = '';
    banner.dataset.level = level;
    banner.appendChild(el('div', 'text', message));
    const dismiss = el('button', 'dismiss', '✕');
    dismiss.type = 'button';
    dismiss.title = 'Dismiss';
    // The glyph is the whole visible label; `title` is a tooltip, not a name.
    dismiss.setAttribute('aria-label', 'Dismiss');
    dismiss.addEventListener('click', () => {
      banner.hidden = true;
    });
    banner.appendChild(dismiss);
    banner.hidden = false;
    // The banner is in the reading flow but not in a live region, so it is said
    // here: an error interrupts, a warning waits its turn.
    if (level === 'error') announceAlert(message);
    else announce(message);
  }

  /**
   * The window's root, from the extension.
   *
   * A line rather than a control (see `workspaceRoot.ts`): a conversation
   * belongs to one folder for its whole life, so there is nothing to switch to.
   * The name is what the eye reads and the path is the tooltip, which is the
   * split the Explorer makes between where a folder is and what it is called.
   *
   * Since p_0.46.24 the line also carries this conversation's share of its
   * context window: it describes the conversation, so it lives on the line that
   * names it rather than on a meter floating under the composer.
   */
  function renderRoot(msg) {
    if (!rootLine) return;
    const pinned = atBottom();
    rootLine.textContent = '';
    rootFacts = null;
    const name = typeof msg.name === 'string' ? msg.name : '';
    if (name === '') {
      // A conversation with no root has nothing to say its context belongs to.
      rootLine.hidden = true;
      renderMeter();
      syncFootprint();
      return;
    }
    const path = typeof msg.path === 'string' ? msg.path : '';
    rootLine.appendChild(el('span', 'root-label', 'Root'));
    const folder = el('code', 'root-name', name);
    folder.title = path;
    rootLine.appendChild(folder);
    if (msg.multi === true) {
      rootLine.appendChild(el('span', 'root-note', 'one folder per window'));
    }
    // Where `renderMeter` writes the conversation's half; the span is created
    // here so the facts always follow the folder.
    rootFacts = el('span', 'root-facts');
    rootLine.appendChild(rootFacts);
    rootLine.title =
      msg.multi === true
        ? `This conversation, its checkpoints and its host all belong to ${path}. The window's other folders are not part of it; open another window to work in them.`
        : `This conversation reads and writes in ${path}.`;
    rootLine.hidden = false;
    renderMeter();
    syncFootprint();
    scrollIfPinned(pinned);
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

  // --- diff width -----------------------------------------------------------

  /** Below this content width, an edit diff is one column rather than two. */
  const DIFF_SIDE_MIN = 620;
  /**
   * The base size the threshold was chosen at. Two columns of a diff are only
   * worth showing while each has a readable number of characters in it, and a
   * character is wider at 20px than at 15 — so the threshold grows with the
   * reader's size instead of switching to the narrow shape too late.
   */
  const DIFF_SIDE_BASE = 15;

  /**
   * Chooses the diff's shape from the width the transcript actually gives it.
   *
   * `chat.css` must stay free of at-rules for `chatLayout.test.ts` to parse it,
   * so the narrow fallback is a class on the body. Unlike a cell, a pane cannot
   * be hidden into a single column, so a flip repaints the comparisons on
   * screen; one rendered afterwards reads the class when it paints. The padding
   * is subtracted because the reading column is capped at `--anvil-read-width`,
   * so a wide window and a 52em column are not the same thing.
   */
  function syncDiffWidth() {
    const pinned = atBottom();
    const style = getComputedStyle(transcript);
    const inset = (parseFloat(style.paddingLeft) || 0) + (parseFloat(style.paddingRight) || 0);
    const width = transcript.clientWidth - inset;
    if (width <= 0) return; // the tab is not visible yet
    const font = parseFloat(style.fontSize) || DIFF_SIDE_BASE;
    const min = Math.round(DIFF_SIDE_MIN * (font / DIFF_SIDE_BASE));
    const narrow = width < min;
    if (document.body.classList.contains('diff-narrow') !== narrow) {
      document.body.classList.toggle('diff-narrow', narrow);
      repaintDiffs();
    }
    scrollIfPinned(pinned);
  }

  /** Draws every comparison on screen in the shape the body class now asks for. */
  function repaintDiffs() {
    for (const pre of document.querySelectorAll('.tool .edit-diff .diff')) {
      const paint = diffPainters.get(pre);
      if (paint) paint();
    }
  }

  let diffWidthQueued = false;
  function queueDiffWidth() {
    if (diffWidthQueued) return;
    diffWidthQueued = true;
    requestAnimationFrame(() => {
      diffWidthQueued = false;
      syncDiffWidth();
    });
  }

  window.addEventListener('resize', queueDiffWidth);
  syncDiffWidth(); // first paint; the listener is the throttled path

  // --- wiring ---------------------------------------------------------------

  composer.addEventListener('submit', (event) => {
    event.preventDefault();
    const text = input.value.trim();
    // `/tasks` is this document's own command: it opens the window's task table
    // and sends nothing. Checked before the running guard, because the table is
    // most useful while a turn is running. The text is consumed; anything else
    // in the composer was not sent, so it stays for the next prompt.
    if (text === '/tasks') {
      input.value = '';
      growInput();
      closePalette();
      vscode.postMessage({ type: 'openTasks' });
      return;
    }
    if (/^\/ref(?:\s.*)?$/i.test(text)) {
      openQuotePicker();
      return;
    }
    if (text === '') return;
    // The composer is emptied here but the turn keeps what was sent, so the
    // optimistic echo below is the record of it either way.
    const sentMentions = mentions.slice();
    const sentImages = images.slice();
    const sentQuotes = quotes.slice();
    // Recorded before the box is emptied: the prompt is what the user typed,
    // and the walk below must not depend on the composer still holding it.
    historyMath.record(history, text);
    placed = null;
    input.value = '';
    growInput();
    mentions = [];
    quotes = [];
    images = [];
    closePalette();
    renderChips();
    const type = running ? (runningSendMode === 'steer' ? 'sendPriority' : 'enqueue') : 'send';
    if (!running) {
      addUser(text, sentMentions, sentImages, sentQuotes);
      // Optimistic: the host's `state` notification confirms it a moment later.
      setRunning(true);
      setStatus('');
      // A new turn: a plan the reader closed last time is allowed to appear
      // again, even if the model repeats it word for word.
      todoEpoch += 1;
    }
    vscode.postMessage({
      type,
      text,
      mentions: sentMentions,
      images: sentImages,
      quotes: sentQuotes.map((quote) => ({ role: quote.role, text: quote.text })),
    });
  });

  // True while an IME composition is open, and for the Enter that closes it.
  // That Enter commits the candidate into the box. It is not a send, even when
  // the keydown arrives after compositionend with isComposing already false.
  let composing = false;
  let confirmEnter = false;
  input.addEventListener('compositionstart', () => {
    composing = true;
  });
  input.addEventListener('compositionend', () => {
    composing = false;
    confirmEnter = true;
    setTimeout(() => {
      confirmEnter = false;
    }, 0);
  });

  input.addEventListener('keydown', (event) => {
    if (event.isComposing || event.keyCode === 229 || composing) return;
    if (confirmEnter) {
      confirmEnter = false;
      return;
    }
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
    if (event.key === 'ArrowUp' || event.key === 'ArrowDown') {
      if (recall(event.key === 'ArrowUp' ? -1 : 1)) {
        event.preventDefault();
        return;
      }
    }
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      composer.dispatchEvent(new Event('submit', { cancelable: true }));
    }
  });

  /**
   * Walks the prompts sent in this conversation, when the key is the history's
   * rather than the textarea's.
   *
   * Up only recalls from the first line and Down only from the last, so moving
   * the caret through a multi-line draft stays the textarea's business. Once a
   * recalled prompt is on screen the caret is put at its end, and Up keeps
   * walking from there rather than requiring the caret to be at the top.
   */
  function recall(delta) {
    if (delta < 0 && history.cursor === null && !(input.selectionStart === 0 && input.selectionEnd === 0)) {
      return false;
    }
    if (delta > 0 && !(input.selectionStart === input.value.length && input.selectionEnd === input.value.length)) {
      return false;
    }
    const value = historyMath.walk(history, delta, input.value);
    if (value === null) return false;
    input.value = value;
    placed = value;
    growInput();
    input.setSelectionRange(value.length, value.length);
    return true;
  }

  /*
   * "@" opens the reference picker. It is watched on `input` rather than
   * `keydown` because which character a key produces is a keyboard-layout
   * question, and `key` is "Digit2" on the layouts where "@" needs Shift.
   */
  input.addEventListener('input', () => {
    quotePickerForced = false;
    closeActionMenu(false);
    // An edit to a recalled prompt ends the walk: `placed` is the value the
    // history put there, so anything else came from the keyboard, and the next
    // Up should start again from the newest prompt rather than overwrite this.
    if (placed !== null && input.value !== placed) {
      historyMath.edited(history);
      placed = null;
    }
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
  input.addEventListener('focus', () => {
    closeActionMenu(false);
    closeModeMenu(false);
    closeCollaborationMenu(false);
    closeSendModeMenu(false);
  });

  if (modeButton && modeMenu) {
    modeButton.addEventListener('click', toggleModeMenu);
    for (const row of modeRows) {
      row.addEventListener('click', () => {
        closeModeMenu(true);
        vscode.postMessage({ type: 'mode', mode: row.dataset.mode || '' });
      });
    }
    modeMenu.addEventListener('keydown', (event) => {
      const current = modeRows.indexOf(document.activeElement);
      if (event.key === 'Escape') {
        event.preventDefault();
        closeModeMenu(true);
      } else if (event.key === 'ArrowDown') {
        event.preventDefault();
        modeRows[(current + 1 + modeRows.length) % modeRows.length]?.focus();
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        modeRows[(current - 1 + modeRows.length) % modeRows.length]?.focus();
      }
    });
    document.addEventListener('mousedown', (event) => {
      if (modeMenu.hidden || modeMenu.contains(event.target) || modeButton.contains(event.target)) return;
      closeModeMenu(false);
    });
    window.addEventListener('blur', () => closeModeMenu(false));
  }

  if (modelPicker && modelMenu) {
    modelPicker.addEventListener('click', (event) => {
      if (modelMenu.hidden) openModelMenu(event.detail === 0);
      else closeModelMenu(true);
    });
    for (const button of effortButtons) {
      button.addEventListener('click', () => {
        vscode.postMessage({ type: 'effort', effort: button.dataset.effort });
      });
    }
    modelMenu.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        closeModelMenu(true);
      }
    });
    document.addEventListener('mousedown', (event) => {
      if (modelMenu.hidden || modelMenu.contains(event.target) || modelPicker.contains(event.target)) return;
      closeModelMenu(false);
    });
    window.addEventListener('blur', () => closeModelMenu(false));
  }

  if (collaborationButton && collaborationMenu) {
    collaborationButton.addEventListener('click', (event) => {
      if (collaborationMenu.hidden) openCollaborationMenu(event.detail === 0);
      else closeCollaborationMenu(true);
    });
    for (const row of collaborationRows) {
      row.addEventListener('click', () => {
        closeCollaborationMenu(true);
        vscode.postMessage({ type: 'collaboration', mode: row.dataset.collaboration });
      });
    }
    collaborationMenu.addEventListener('keydown', (event) => {
      const current = collaborationRows.indexOf(document.activeElement);
      if (event.key === 'Escape') {
        event.preventDefault();
        closeCollaborationMenu(true);
      } else if (event.key === 'ArrowDown' || event.key === 'ArrowRight') {
        event.preventDefault();
        collaborationRows[(current + 1 + collaborationRows.length) % collaborationRows.length]?.focus();
      } else if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') {
        event.preventDefault();
        collaborationRows[(current - 1 + collaborationRows.length) % collaborationRows.length]?.focus();
      }
    });
    document.addEventListener('mousedown', (event) => {
      if (
        collaborationMenu.hidden ||
        collaborationMenu.contains(event.target) ||
        collaborationButton.contains(event.target)
      ) {
        return;
      }
      closeCollaborationMenu(false);
    });
    window.addEventListener('resize', positionCollaborationMenu);
  }
  if (agentMapButton) {
    agentMapButton.addEventListener('click', () => {
      ensureAgentMapDialog();
      vscode.postMessage({ type: 'agentMap' });
    });
  }

  if (rulesButton) {
    rulesButton.addEventListener('click', () => {
      closeActionMenu(false);
      showRules(rules);
      input.focus();
    });
  }

  if (permissionsButton) {
    // Always offered, even before the file exists: creating it is one of the two
    // things this button is for, and an entry point that only appears once
    // somebody has already found the file is not an entry point.
    permissionsButton.addEventListener('click', () => {
      closeActionMenu(false);
      vscode.postMessage({ type: 'openPermissions' });
    });
  }

  if (slashButton) {
    slashButton.addEventListener('click', () => {
      closeActionMenu(false);
      input.value = '/';
      growInput();
      input.focus();
      updatePalette();
    });
  }

  if (mentionButton) {
    mentionButton.addEventListener('click', () => {
      closeActionMenu(false);
      askForMentions();
    });
  }

  if (quoteButton) {
    quoteButton.addEventListener('click', openQuotePicker);
  }

  if (actionMenu) {
    actionMenu.addEventListener('keydown', (event) => {
      const rows = Array.from(actionMenu.querySelectorAll('button:not(:disabled)'));
      const current = rows.indexOf(document.activeElement);
      if (event.key === 'Escape') {
        event.preventDefault();
        closeActionMenu(true);
      } else if (event.key === 'ArrowDown' || event.key === 'ArrowRight') {
        event.preventDefault();
        rows[(current + 1 + rows.length) % rows.length]?.focus();
      } else if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') {
        event.preventDefault();
        rows[(current - 1 + rows.length) % rows.length]?.focus();
      }
    });
    document.addEventListener('mousedown', (event) => {
      if (actionMenu.hidden || actionMenu.contains(event.target) || attachButton.contains(event.target)) return;
      closeActionMenu(false);
    });
    window.addEventListener('blur', () => closeActionMenu(false));
    document.addEventListener('visibilitychange', () => {
      if (document.hidden) closeActionMenu(false);
    });
  }

  transcript.addEventListener('scroll', syncScrollBottomButton, { passive: true });
  if (autoScrollButton) {
    autoScrollButton.addEventListener('click', () => setAutoScroll(!autoScroll));
    syncAutoScrollButton();
  }
  if (scrollBottomButton) {
    scrollBottomButton.addEventListener('click', scrollConversationToBottom);
  }

  if (focusButton) {
    // Local, and then remembered: the button is the document asking itself, so
    // there is no round trip to wait for. The palette command and the key
    // binding take the other path — the extension asks the document to flip.
    focusButton.addEventListener('click', () => setFocus(!focusView));
  }

  if (todosToggle && todosBody && todoBlock) {
    todosToggle.addEventListener('click', () => {
      setTodosCollapsed(!todoBlock.classList.contains('collapsed'));
    });
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

  attachButton.addEventListener('click', toggleActionMenu);

  if (imageButton) {
    // The picker is the editor's: the webview can read a `File` it was given,
    // but it cannot open a dialog, and a picked path is then read by the same
    // side that reads an attachment. The host answers with data URLs, so the
    // composer ends up with exactly what a paste puts there.
    imageButton.addEventListener('click', () => {
      closeActionMenu(false);
      vscode.postMessage({ type: 'pickImages' });
    });
  }

  /**
   * Asks the host to stop the turn. Shared by the send button — which is the
   * stop button while a turn runs — and Escape, so the two cannot drift.
   */
  function requestStop() {
    if (stopButton) stopButton.disabled = true;
    vscode.postMessage({ type: 'cancel' });
  }

  document.addEventListener('selectionchange', scheduleSelectionQuoteAction);
  transcript.addEventListener('scroll', scheduleSelectionQuoteAction, { passive: true });
  window.addEventListener('resize', scheduleSelectionQuoteAction);

  transcript.addEventListener('contextmenu', (event) => {
    const selected = transcriptSelection();
    if (selected.parts.length === 0) return;
    event.preventDefault();
    selectedQuoteParts = selected.parts;
    selectedCopyText = selected.text;
    if (selectionQuoteButton) selectionQuoteButton.hidden = true;
    const menu = ensureSelectionContextMenu();
    menu.hidden = false;
    const width = 176;
    const height = 70;
    const rect = selected.range ? selected.range.getBoundingClientRect() : null;
    const x = event.clientX || (rect ? rect.left : 8);
    const y = event.clientY || (rect ? rect.bottom : 8);
    menu.style.left = Math.max(8, Math.min(window.innerWidth - width - 8, x)) + 'px';
    menu.style.top = Math.max(8, Math.min(window.innerHeight - height - 8, y)) + 'px';
    const first = menu.querySelector('button');
    if (first) first.focus();
  });

  document.addEventListener('pointerdown', (event) => {
    if (!selectionContextMenu || selectionContextMenu.hidden) return;
    if (!selectionContextMenu.contains(event.target)) closeSelectionContextMenu();
  });

  /*
   * Escape interrupts a turn, from anywhere in the panel.
   *
   * On the window rather than on the textarea because the textarea is disabled
   * while a turn runs — which is exactly when Escape matters. An idle Escape is
   * left alone entirely: there is nothing to interrupt, and the composer's own
   * handler still uses it to close the palette, while the draft stays put.
   */
  window.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape' || !running) return;
    event.preventDefault();
    requestStop();
  });

  window.addEventListener('keydown', (event) => {
    if (!event.ctrlKey || !event.shiftKey || event.altKey || event.key.toLowerCase() !== 'q') return;
    const selected = transcriptSelection();
    if (selected.parts.length === 0) return;
    event.preventDefault();
    quoteTranscriptSelection(selected.parts);
  });

  // --- dropping files onto the panel ---------------------------------------

  /** Whether a drag is carrying files at all, rather than a text selection. */
  function carriesFiles(event) {
    const types = (event.dataTransfer && event.dataTransfer.types) || [];
    return (
      Array.prototype.indexOf.call(types, 'Files') !== -1 ||
      Array.prototype.indexOf.call(types, 'text/uri-list') !== -1 ||
      Array.prototype.indexOf.call(types, 'application/vnd.code.uri-list') !== -1
    );
  }

  document.addEventListener('dragover', (event) => {
    // Only a file drag is ours. Preventing the default for everything would
    // swallow a text selection dropped on the transcript, which has no meaning
    // here but should still not look accepted.
    if (!carriesFiles(event)) return;
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    document.body.classList.add('drop-target');
  });

  document.addEventListener('dragleave', (event) => {
    // `dragleave` also fires when the pointer crosses into a child, so only a
    // departure from the document itself ends the highlight.
    if (event.relatedTarget === null) document.body.classList.remove('drop-target');
  });

  document.addEventListener('drop', (event) => {
    document.body.classList.remove('drop-target');
    if (!carriesFiles(event)) return;
    event.preventDefault();
    const transfer = event.dataTransfer;
    if (!transfer) return;
    const files = Array.prototype.slice.call(transfer.files || []);
    const paths = [];
    const uris = [];
    for (const file of files) {
      if (file.type && file.type.indexOf('image/') === 0) {
        // An image is an attachment, not a reference: the mention path reads
        // files as text, and an image is not text.
        readImage(file);
        continue;
      }
      // Electron used to expose the dropped file's path on the `File`; newer
      // versions do not, so the URI list is the other way to learn it.
      if (typeof file.path === 'string' && file.path !== '') paths.push(file.path);
    }
    // A drag from the editor's own explorer carries the real paths here; an OS
    // drag carries them on the `File` when it can.
    const uriList = (transfer.getData('text/uri-list') || transfer.getData('application/vnd.code.uri-list') || '').split(/\r?\n/);
    for (const uri of uriList) {
      const trimmed = uri.trim();
      if (trimmed !== '' && trimmed.charAt(0) !== '#') uris.push(trimmed);
    }
    if (paths.length > 0 || uris.length > 0) {
      // Resolved on the host: the workspace root is the extension's to know,
      // and a path outside it is not a mention.
      vscode.postMessage({ type: 'dropFiles', paths, uris });
    } else if (files.length > 0) {
      showBanner('warn', 'That file could not be referenced. Use @ to pick it instead.');
    }
  });

  sendButton.addEventListener('click', () => {
    composer.dispatchEvent(new Event('submit', { cancelable: true }));
  });
  if (stopButton) stopButton.addEventListener('click', requestStop);
  if (sendModeButton && sendModeMenu) {
    sendModeButton.addEventListener('click', toggleSendModeMenu);
    for (const row of sendModeRows) {
      row.addEventListener('click', () => {
        runningSendMode = row.dataset.sendMode === 'steer' ? 'steer' : 'enqueue';
        updateSendMode();
        closeSendModeMenu(true);
        if (running) setRunning(true);
      });
    }
    sendModeMenu.addEventListener('keydown', (event) => {
      const current = sendModeRows.indexOf(document.activeElement);
      if (event.key === 'Escape') {
        event.preventDefault();
        closeSendModeMenu(true);
      } else if (event.key === 'ArrowDown') {
        event.preventDefault();
        sendModeRows[(current + 1 + sendModeRows.length) % sendModeRows.length]?.focus();
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        sendModeRows[(current - 1 + sendModeRows.length) % sendModeRows.length]?.focus();
      }
    });
    document.addEventListener('mousedown', (event) => {
      if (sendModeMenu.hidden || sendModeMenu.contains(event.target) || sendModeButton.contains(event.target)) return;
      closeSendModeMenu(false);
    });
    updateSendMode();
  }

  /**
   * Draws one event on whichever scope is active.
   *
   * Split out from the listener so a child's events can be drawn inside the
   * spawn card that started it: the listener picks the scope, this decides what
   * the event means. Everything that appends goes through `active`.
   */
  function renderEvent(e, msg) {
    if (e.usage_estimated === true && active === mainScope) recordLiveUsage(e);
    if (e.type === 'assistant') {
      if (active === mainScope) setWorkingActivity('Responding…');
      addAssistantChunk(e.text || '');
    } else if (e.type === 'reasoning') {
      if (active === mainScope) setWorkingActivity(DEFAULT_WORKING_WORD);
      addReasoning(e.text || '');
    } else if (e.type === 'tool_call') {
      if (active === mainScope) setWorkingActivity(toolActivity(e.name));
      addToolCall(e, msg.reviewable === true, msg.edit);
    } else if (e.type === 'tool_result') {
      addToolResult(e);
      if (active === mainScope) setWorkingActivity(DEFAULT_WORKING_WORD);
    }
    else if (e.type === 'todo') setTodos(e.items, e.run_id);
    else if (e.type === 'compact') addCompact(e.compact);
    // A root run's terminal error is repeated authoritatively by `done`; draw
    // it there once. Child errors have no root `done` in this webview, so they
    // still belong inside the expanded subagent transcript.
    else if (e.type === 'error' && active !== mainScope) addNotice(e.text || 'The run reported an error.');
    else if (e.type === 'progress') {
      // A step belongs to the working line while a turn runs. The same event in
      // a replay has no run of its own, so its text lands on the line instead.
      if (typeof e.step === 'number' && e.step > 0) {
        // A repeat of the step already on the line is not a new fact, so it
        // must not pulse: the host may restate the current step on any event
        // inside it (p_0.46.26).
        const advanced = e.step !== workingStep;
        workingStep = e.step;
        workingMax = typeof e.maxSteps === 'number' && e.maxSteps > 0 ? e.maxSteps : 0;
        if (!workingLine.hidden) {
          if (advanced) setWorkingActivity(e.text || DEFAULT_WORKING_WORD);
          renderWorkingMeta();
          if (advanced) pulseNumber(workingStepNode);
        }
      }
      if (e.text) {
        // A host-supplied activity is another event-driven state.
        if (!workingLine.hidden) {
          setWorkingActivity(e.text);
        } else setStatus(e.text);
        // The line is what the eye reads; the polite region is what the ear
        // hears, and the throttle is what keeps a run of them from becoming
        // noise.
        if (active === mainScope) announceProgress(e.text);
      }
    }
  }

  /**
   * The reader's text size, from `anvil.chatFontSize` or from the value the
   * extension rendered onto the body. It is a variable rather than a body rule
   * so the stylesheet keeps its fallback to the editor's UI font, and so a
   * change repaints without rebuilding the transcript.
   *
   * The range is clamped here as well as in the settings reader: this function
   * is the last place before a number becomes pixels.
   */
  function applyFontSize(size) {
    const px = Number(size);
    if (!Number.isFinite(px) || px < 10 || px > 24) return;
    document.documentElement.style.setProperty('--anvil-font-size', px + 'px');
    // The diff's two-column threshold is in pixels and scales with the text, so
    // a resize of the type is also a resize of the document's shape.
    queueDiffWidth();
  }

  window.addEventListener('message', (event) => {
    const msg = event.data;
    switch (msg.type) {
      case 'trust':
        if (!msg.trusted) {
          showBanner('warn', 'This workspace is not trusted, so Anvil will not start. Trust the folder and reload the window.');
          input.disabled = true;
          sendButton.disabled = true;
          attachButton.disabled = true;
          if (imageButton) imageButton.disabled = true;
        }
        return;
      case 'state':
        sendButton.disabled = false;
        setRunning(msg.state === 'running');
        return;
      case 'queue':
        showQueue(msg);
        return;
      case 'dispatchQueued':
        dispatchQueued(msg);
        return;
      case 'root':
        renderRoot(msg);
        return;
      case 'event': {
        const e = msg.event;
        // A child's events name the spawn call that started them, so they are
        // drawn in that card's body rather than in the parent's flow. The id is
        // also how the card learns which stored session to offer afterwards.
        const target = e.parent_tool_call_id ? spawnScopes.get(e.parent_tool_call_id) : undefined;
        if (target) {
          if (!completedSubagentSpawns.has(e.parent_tool_call_id)) {
            activeSubagents.add('spawn:' + e.parent_tool_call_id);
          }
          syncCollaborationActivity();
          if (e.agent_id) target.agentId = e.agent_id;
          inScope(target, () => renderEvent(e, msg));
          return;
        }
        renderEvent(e, msg);
        return;
      }
      case 'approval':
        showApproval(msg.request);
        return;
      case 'approvalWithdrawn':
        withdrawApproval(msg.requestId);
        return;
      case 'approvalSettled':
        settleFromReview(msg);
        return;
      case 'rememberResult':
        resolveRemember(msg);
        return;
      case 'mentions':
        addMentions(msg.paths || []);
        return;
      case 'images':
        // The picker's answer: data URLs in exactly the shape a paste leaves in
        // `images`, so everything downstream — the chips, the send, the host's
        // conversion — has one kind of input rather than two.
        for (const dataUrl of Array.isArray(msg.images) ? msg.images : []) addImage(dataUrl);
        return;
      case 'vision':
        vision = msg.enabled === true;
        return;
      case 'fontSize':
        applyFontSize(msg.size);
        return;
      case 'backgroundTask':
        updateBackgroundTask(msg.task);
        if (agentMapDialog) vscode.postMessage({ type: 'agentMap' });
        return;
      case 'changes':
        showChanges(msg.files, msg.runId);
        return;
      case 'checkpoint':
        showCheckpoint(msg);
        return;
      case 'checkpointRestored':
        settleCheckpoint(msg);
        return;
      case 'forked':
        settleFork(msg);
        return;
      case 'rewindState':
        showRewindState(msg);
        return;
      case 'rewound':
        settleRewind(msg);
        return;
      case 'rewindOpen':
        openLastRewind();
        return;
      case 'plan':
        showPlan(msg);
        return;
      case 'planSettled':
        settlePlan(msg);
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
      case 'models':
        showModels(msg);
        return;
      case 'effort':
        showEffort(msg);
        return;
      case 'collaboration':
        showCollaboration(msg);
        return;
      case 'agentMap':
        renderAgentMap(msg.state || { rootId: '', nodes: [], counts: { total: 0, working: 0, finished: 0, failed: 0 } });
        return;
      case 'agentDetail':
        showAgentDetail(msg);
        return;
      case 'rules': {
        rules = Array.isArray(msg.files) ? msg.files : [];
        if (rulesButton) {
          const label = 'Loaded rule files (' + rules.length + ')';
          rulesButton.setAttribute('aria-label', label);
          rulesButton.title = label;
        }
        // Only what the user cannot see for themselves is shown unbidden. A
        // rule file that was skipped or cut short is a silent failure of
        // something they wrote, which is exactly when to speak up; the rest
        // waits behind the button.
        if (rules.some((file) => file.included !== true || file.truncated === true)) showRules(rules);
        return;
      }
      case 'mcp':
        showMcp(msg);
        return;
      case 'mcpStatus':
        updateMcp(msg.server);
        return;
      case 'webSearch':
        showWebSearch(msg);
        return;
      case 'session':
        showSession(msg);
        return;
      case 'agent':
        // The one thing this document must remember for a reload: the
        // serializer hands it back and the tab comes up on the same
        // conversation. It is not drawn — a new session is not news.
        remember({ agentId: msg.agentId || undefined });
        return;
      case 'toggleFocus':
        // The palette command and its keybinding arrive here. The document
        // flips and stores; the button shows the result.
        setFocus(!focusView);
        return;
      case 'context':
        // 0 is "unknown", the same value the setting's absence means, so a
        // missing or malformed field falls back to counts only.
        contextWindow = typeof msg.window === 'number' ? msg.window : 0;
        autoCompactAt = typeof msg.auto_compact_at === 'number' ? msg.auto_compact_at : 0;
        cacheTtlSeconds = typeof msg.cache_ttl_seconds === 'number' ? msg.cache_ttl_seconds : 0;
        // The estimate is the host's answer to `session.context`, and its
        // absence means the host did not answer — not that the conversation is
        // empty. Falling back to the last run's total is more honest than
        // keeping a figure from a session that may have been replaced.
        contextInfo = msg.info && typeof msg.info === 'object' ? msg.info : null;
        renderMeter();
        return;
      case 'replayStart':
        replaying = true;
        replayPinsBottom = true;
        replayAsyncBlocks = 0;
        replayGeneration++;
        showReplayLoading();
        return;
      case 'replayEnd':
        replaying = false;
        settleReplayAtBottom();
        return;
      case 'reset':
        clearTranscript();
        return;
      case 'user':
        // A replayed turn. The composer does this for a live one, and it is not
        // involved here, so the replay asks for it directly.
        addUser(msg.text || '');
        // Also separate fallback plan identities when an older stored event
        // has no run id.
        todoEpoch += 1;
        // It is also a prompt this conversation has shown, which is what the
        // recall list holds.
        historyMath.record(history, msg.text || '');
        return;
      case 'note':
        addNote(msg.text || '');
        return;
      case 'runEnd':
        addRunEnd(msg.done);
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

  // Before the first paint the extension writes the size onto the body; the
  // `ready` answer repeats it, which covers a document that waited while the
  // setting changed.
  applyFontSize(document.body.dataset.fontSize);
  setRunning(false);
  renderChips();
  vscode.postMessage({ type: 'ready' });
})();
