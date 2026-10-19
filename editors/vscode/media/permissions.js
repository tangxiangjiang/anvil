/*
 * The permissions manager document.
 *
 * It renders only what the extension sends: the rule file's path, its rules, its
 * declaration and — when there is one — the parser's problem. Every value is
 * written with textContent or as an input's `value`; there is no innerHTML, and
 * there is no reason for one: rules are data a person is editing, not markup.
 *
 * The document never decides what a rule means. It shape-checks nothing beyond
 * what a form needs to draw (an effect is one of three words, a tool is a
 * string), and the answer to a save is the host's reading of the file. That is
 * why a save re-renders the whole form from the reply: what comes back is what a
 * session will read, which is not always byte-for-byte what was sent.
 */
(function () {
  const vscode = acquireVsCodeApi();
  const root = document.getElementById('root');

  const EFFECTS = ['allow', 'ask', 'deny'];
  const MODE_LABELS = {
    ask: 'Ask (the ladder’s floor)',
    accept_edits: 'Accept edits',
    auto: 'Auto: safe actions',
    plan: 'Plan',
    dont_ask: "Don't ask",
  };

  /** The rows being edited. Mutated in place by the inputs, so focus survives. */
  let rows = [];
  /** The mode select, kept because Save reads it and the state rebuild replaces it. */
  let modeSelect = null;
  /** True when the controls may edit; false for a parse problem or no host. */
  let editable = false;
  /** True between Save and its answer. */
  let saving = false;
  /** Whether a row changed since the last render. */
  let dirty = false;

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  function banner(className, text) {
    const node = el('div', 'banner ' + className);
    node.appendChild(el('span', 'banner-text', text));
    return node;
  }

  function position(problem) {
    if (!problem || !problem.line) return '';
    if (!problem.column) return ' (line ' + problem.line + ')';
    return ' (line ' + problem.line + ', column ' + problem.column + ')';
  }

  /**
   * Draws the whole document from a state message.
   *
   * `note` is the one line that is not part of the file: "Saved", or nothing. A
   * save answer is rendered from the host's reading, so the form shows the file
   * rather than the edit.
   */
  function render(msg, note) {
    root.textContent = '';
    rows = Array.isArray(msg.rules)
      ? msg.rules.map((rule) => ({
          effect: EFFECTS.indexOf(rule.effect) >= 0 ? rule.effect : 'allow',
          tool: typeof rule.tool === 'string' ? rule.tool : '',
          pattern: typeof rule.pattern === 'string' ? rule.pattern : '',
          reason: typeof rule.reason === 'string' ? rule.reason : '',
        }))
      : [];
    dirty = false;
    saving = false;
    modeSelect = null;

    // A file that will not parse is read-only on purpose: the old text is what
    // the user was told to fix, and saving over it would throw that away. A host
    // that could not be asked is the same state for the same reason.
    const problem = msg.problem;
    const failure = typeof msg.error === 'string' && msg.error !== '' ? msg.error : '';
    editable = !problem && !failure;

    root.appendChild(el('h2', 'title', 'Permissions'));
    const where = el('div', 'path', msg.path || 'This workspace’s .anvil/permissions.json');
    root.appendChild(where);
    root.appendChild(
      el(
        'p',
        'hint',
        'These rules decide which tool calls run, which ask, and which are refused. Saving replaces the whole file.',
      ),
    );

    if (problem) {
      const text =
        'This file cannot be read as rules' +
        position(problem) +
        ': ' +
        (problem.message || 'the parser refused it') +
        '. Editing is off so the file is not overwritten; fix it in the JSON.';
      root.appendChild(banner('error', text));
    }
    if (failure) {
      root.appendChild(banner('error', 'The rules could not be read: ' + failure));
    }
    if (msg.defaultModeRefused) {
      root.appendChild(banner('warn', msg.defaultModeRefused));
    }
    if (msg.loadPermissions === false) {
      root.appendChild(
        banner(
          'warn',
          'anvil.permissions is off, so this file is not read: its rules decide nothing.',
        ),
      );
    }
    if (note) {
      root.appendChild(banner('ok', note));
    }

    // --- the declaration -----------------------------------------------------
    const modeRow = el('div', 'mode-row');
    modeRow.appendChild(el('label', 'mode-label', 'New sessions start in'));
    const select = document.createElement('select');
    select.id = 'default-mode';
    const none = document.createElement('option');
    none.value = '';
    none.textContent = 'No declaration (Ask)';
    select.appendChild(none);
    const modes = Array.isArray(msg.modes) ? msg.modes : [];
    for (const mode of modes) {
      if (typeof mode !== 'string' || mode === 'bypass') continue;
      const option = document.createElement('option');
      option.value = mode;
      option.textContent = MODE_LABELS[mode] || mode;
      select.appendChild(option);
    }
    select.value = typeof msg.defaultMode === 'string' ? msg.defaultMode : '';
    // A declaration the host refuses is not among the options; leaving the
    // select empty is the honest rendering, and the banner above says what the
    // file actually declared.
    if (select.selectedIndex < 0) select.value = '';
    select.disabled = !editable;
    select.addEventListener('change', touch);
    modeSelect = select;
    modeRow.appendChild(select);
    root.appendChild(modeRow);

    // --- the rules -----------------------------------------------------------
    const table = el('div', 'rules');
    const head = el('div', 'rule rule-head');
    head.appendChild(el('span', 'num', '#'));
    head.appendChild(el('span', 'col', 'Effect'));
    head.appendChild(el('span', 'col', 'Tool'));
    head.appendChild(el('span', 'col', 'Pattern'));
    head.appendChild(el('span', 'col', 'Reason'));
    head.appendChild(el('span', 'col', ''));
    table.appendChild(head);

    const body = el('div', 'rule-body');
    table.appendChild(body);
    root.appendChild(table);

    const empty = el('p', 'hint empty', 'No rules. Every call follows the mode alone.');
    root.appendChild(empty);

    bodyFor(body, empty);

    const add = el('button', 'ghost', 'Add rule');
    add.type = 'button';
    add.id = 'add-rule';
    add.disabled = !editable;
    add.addEventListener('click', () => {
      rows.push({ effect: 'allow', tool: '', pattern: '', reason: '' });
      dirty = true;
      bodyFor(body, empty);
      const inputs = body.querySelectorAll('.rule:not(.rule-head) input.tool');
      if (inputs.length > 0) inputs[inputs.length - 1].focus();
      refreshSave();
    });
    root.appendChild(add);

    // --- the footer ----------------------------------------------------------
    const footer = el('div', 'buttons');
    const status = el('span', 'status');
    status.id = 'status';
    const reload = el('button', 'ghost', 'Reload');
    const openFile = el('button', 'ghost', 'Open JSON');
    const save = el('button', 'primary', 'Save');
    reload.type = 'button';
    openFile.type = 'button';
    save.type = 'button';
    save.id = 'save';

    reload.addEventListener('click', () => vscode.postMessage({ type: 'reload' }));
    openFile.addEventListener('click', () => vscode.postMessage({ type: 'openFile' }));
    save.addEventListener('click', saveRows);

    footer.appendChild(status);
    footer.appendChild(el('span', 'spacer'));
    footer.appendChild(reload);
    footer.appendChild(openFile);
    footer.appendChild(save);
    root.appendChild(footer);

    refreshSave();
  }

  /** Rebuilds the table's rows; numbering follows the list's order. */
  function bodyFor(body, empty) {
    body.textContent = '';
    rows.forEach((rule, index) => body.appendChild(ruleRow(rule, index)));
    empty.style.display = rows.length === 0 ? '' : 'none';
  }

  function ruleRow(rule, index) {
    const row = el('div', 'rule');
    row.appendChild(el('span', 'num', String(index + 1)));

    const effect = document.createElement('select');
    effect.className = 'effect';
    for (const value of EFFECTS) {
      const option = document.createElement('option');
      option.value = value;
      option.textContent = value;
      effect.appendChild(option);
    }
    effect.value = rule.effect;
    effect.disabled = !editable;
    effect.addEventListener('change', () => {
      rule.effect = effect.value;
      touch();
    });
    row.appendChild(effect);

    row.appendChild(field(rule, 'tool', 'shell', 'tool'));
    row.appendChild(field(rule, 'pattern', 'readonly', 'pattern'));
    row.appendChild(field(rule, 'reason', 'why this rule exists', 'reason'));

    // Delete is a row edit like typing: Save is what makes it real, so there is
    // no confirmation to answer — the edit is still undoable with Reload until
    // it is saved.
    const remove = el('button', 'ghost delete', 'Delete');
    remove.type = 'button';
    remove.disabled = !editable;
    remove.addEventListener('click', () => {
      rows.splice(index, 1);
      dirty = true;
      const body = row.parentElement;
      const empty = document.getElementById('root').querySelector('.empty');
      if (body && empty) bodyFor(body, empty);
      refreshSave();
    });
    row.appendChild(remove);
    return row;
  }

  function field(rule, name, placeholder, className) {
    const input = document.createElement('input');
    input.type = 'text';
    input.className = className;
    input.placeholder = placeholder;
    input.value = rule[name] || '';
    input.disabled = !editable;
    input.addEventListener('input', () => {
      rule[name] = input.value;
      touch();
    });
    return input;
  }

  function touch() {
    dirty = true;
    refreshSave();
  }

  function refreshSave() {
    const save = document.getElementById('save');
    const status = document.getElementById('status');
    if (save) {
      save.disabled = !editable || saving || !dirty;
      save.textContent = saving ? 'Saving…' : 'Save';
    }
    if (status) {
      if (saving) status.textContent = 'Saving…';
      else if (!editable) status.textContent = 'Read-only until the problem is fixed.';
      else if (dirty) status.textContent = 'Unsaved changes.';
    }
  }

  function saveRows() {
    if (!editable || saving) return;
    saving = true;
    refreshSave();
    const rules = rows.map((rule) => ({
      effect: rule.effect,
      tool: (rule.tool || '').trim(),
      pattern: (rule.pattern || '').trim(),
      reason: (rule.reason || '').trim(),
    }));
    const defaultMode = modeSelect ? modeSelect.value : '';
    vscode.postMessage({ type: 'save', rules, defaultMode });
  }

  window.addEventListener('message', (event) => {
    const msg = event.data;
    if (!msg || typeof msg !== 'object') return;
    switch (msg.type) {
      case 'state':
        render(msg, '');
        return;
      case 'saved':
        render(msg, 'Saved. The file now says this, rule for rule.');
        return;
      case 'saving':
        saving = msg.saving === true;
        refreshSave();
        return;
      case 'error':
        // An error does not re-render: the rows are the user's edit, and a
        // refused save is not a reason to throw them away.
        saving = false;
        refreshSave();
        {
          const old = root.querySelector('.banner.error.runtime');
          if (old) old.remove();
          const node = banner('error', 'Not saved: ' + (msg.message || 'the host refused it'));
          node.classList.add('runtime');
          root.insertBefore(node, root.firstChild);
        }
        return;
    }
  });

  vscode.postMessage({ type: 'ready' });
})();
