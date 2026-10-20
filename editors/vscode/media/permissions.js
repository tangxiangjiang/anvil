/*
 * The permissions manager document.
 *
 * It renders only what the extension sends: two rule files, their rules, each
 * file's declaration or protected globs and — when there is one — the parser's
 * problem. Every value is written with textContent or as an input's `value`;
 * there is no innerHTML, and there is no reason for one: rules are data a person
 * is editing, not markup.
 *
 * The two layers are drawn as two sections because they are two files with two
 * jobs. The user file is the person's own preference and follows them between
 * repositories; the workspace file is the repository's policy and travels with
 * the checkout. A save names one of them — the host refuses a workspace save
 * that carries protected globs, and a user save that carries a starting mode —
 * so the document never sends both shapes in one message.
 *
 * The document never decides what a rule means. It shape-checks nothing beyond
 * what a form needs to draw (an effect is one of three words, a tool is a
 * string), and the answer to a save is the host's reading of the files. That is
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

  /** The rows being edited, one list per layer. Mutated in place by the inputs. */
  const rows = { user: [], workspace: [] };
  /** The mode select, kept because Save reads it and the state rebuild replaces it. */
  let modeSelect = null;
  /** The protected-glob input, read the same way. */
  let protectedInput = null;
  /** Per layer: true when the controls may edit. */
  const editable = { user: false, workspace: false };
  /** True between a Save and its answer. */
  let saving = false;
  /** Which layer is being saved, so one section's button waits and not both. */
  let savingSource = '';
  /** Per layer: whether a row changed since the last render. */
  const dirty = { user: false, workspace: false };
  /** Per layer: the table body and its empty note, for rebuilding rows. */
  const bodies = {};
  /** True when there is a user layer and its file has not been written yet. */
  let userMissing = false;

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
   * `note` is the one line that is not part of a file: "Saved", or nothing. A
   * save answer is rendered from the host's reading, so the form shows the files
   * rather than the edit.
   */
  function render(msg, note) {
    root.textContent = '';
    rows.user = rowsOf(msg.userRules);
    rows.workspace = rowsOf(msg.workspaceRules);
    dirty.user = false;
    dirty.workspace = false;
    saving = false;
    savingSource = '';
    modeSelect = null;
    protectedInput = null;
    bodies.user = null;
    bodies.workspace = null;

    // A file that will not parse is read-only on purpose: the old text is what
    // the user was told to fix, and saving over it would throw that away. A host
    // that could not be asked is the same state for the same reason. The two
    // files are refused independently — a broken user file leaves the workspace
    // editable.
    const problem = msg.problem;
    const userProblem = msg.userProblem;
    const failure = typeof msg.error === 'string' && msg.error !== '' ? msg.error : '';
    const userPath = typeof msg.userPath === 'string' ? msg.userPath : '';
    const userExists = msg.userExists === true;
    // An older host reports one layer and no user path; drawing an empty user
    // section over it would read as a file this build cannot see.
    const hasUser = userPath !== '';
    editable.workspace = !problem && !failure;
    editable.user = hasUser && userExists && !userProblem && !failure;
    userMissing = hasUser && !userExists && !userProblem;

    root.appendChild(el('h2', 'title', 'Permissions'));
    root.appendChild(
      el(
        'p',
        'hint',
        'These rules decide which tool calls run, which ask, and which are refused. Your rules come first; deny beats ask, and ask beats allow, whichever file a rule is in.',
      ),
    );

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
          'anvil.permissions is off, so these files are not read: their rules decide nothing.',
        ),
      );
    }
    if (note) {
      root.appendChild(banner('ok', note));
    }

    if (hasUser) userSection(msg, userPath, userExists, userProblem);
    workspaceSection(msg, problem);

    // --- the footer ----------------------------------------------------------
    const footer = el('div', 'buttons footer');
    const reload = el('button', 'ghost', 'Reload');
    reload.type = 'button';
    reload.addEventListener('click', () => vscode.postMessage({ type: 'reload' }));
    footer.appendChild(el('span', 'spacer'));
    footer.appendChild(reload);
    root.appendChild(footer);

    for (const scope of ['user', 'workspace']) {
      if (bodies[scope]) renderTable(scope);
    }
    refreshSave();
  }

  /** The person's own file: their rules, their protected globs. */
  function userSection(msg, userPath, userExists, userProblem) {
    const section = el('section', 'scope user');
    section.appendChild(scopeHead('Your rules', 'These follow you between workspaces.', userPath));

    if (!userExists && !userProblem) {
      const missing = banner(
        'info',
        'No user rules file yet. Create it to write rules that apply wherever you work.',
      );
      if (editable.workspace) {
        const create = el('button', 'ghost', 'Create user file');
        create.type = 'button';
        create.id = 'create-user';
        create.addEventListener('click', () => {
          if (saving) return;
          saving = true;
          savingSource = 'user';
          refreshSave();
          vscode.postMessage({ type: 'save', source: 'user', create: true, rules: [], protected: [] });
        });
        missing.appendChild(create);
      }
      section.appendChild(missing);
    }
    if (userProblem) {
      section.appendChild(
        banner(
          'error',
          'This file cannot be read as rules' +
            position(userProblem) +
            ': ' +
            (userProblem.message || 'the parser refused it') +
            '. Editing is off so the file is not overwritten; fix it in the JSON.',
        ),
      );
    }

    // Protected paths are a fact about a call, not a rule: an allow rule is
    // skipped when one of these globs is touched, whatever the rule says. They
    // live in the user file because a repository may not widen what it is
    // allowed to change without asking.
    const protectedRow = el('div', 'protected-row');
    protectedRow.appendChild(
      el('label', 'mode-label', 'Protected paths: globs an allow rule may never cover'),
    );
    const paths = document.createElement('textarea');
    paths.id = 'protected-paths';
    paths.rows = 2;
    paths.placeholder = 'secrets/**\n*.pem';
    paths.value = Array.isArray(msg.protected) ? msg.protected.join('\n') : '';
    paths.disabled = !editable.user;
    paths.addEventListener('input', () => touch('user'));
    protectedInput = paths;
    protectedRow.appendChild(paths);
    protectedRow.appendChild(
      el(
        'p',
        'hint',
        '.git, .anvil/checkpoints/ and the Anvil home are always protected; a workspace file cannot add to this list.',
      ),
    );
    section.appendChild(protectedRow);

    section.appendChild(tableFor('user'));
    section.appendChild(addButton('user'));
    section.appendChild(saveBar('user', 'Save your rules', 'user'));
    root.appendChild(section);
  }

  /** The repository's file: its starting mode, its rules. */
  function workspaceSection(msg, problem) {
    const section = el('section', 'scope workspace');
    section.appendChild(
      scopeHead(
        'This workspace',
        'These travel with the repository and are shared with everyone who opens it.',
        typeof msg.path === 'string' ? msg.path : 'This workspace’s .anvil/permissions.json',
      ),
    );

    if (problem) {
      section.appendChild(
        banner(
          'error',
          'This file cannot be read as rules' +
            position(problem) +
            ': ' +
            (problem.message || 'the parser refused it') +
            '. Editing is off so the file is not overwritten; fix it in the JSON.',
        ),
      );
    }

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
    select.disabled = !editable.workspace;
    select.addEventListener('change', () => touch('workspace'));
    modeSelect = select;
    modeRow.appendChild(select);
    section.appendChild(modeRow);

    section.appendChild(tableFor('workspace'));
    section.appendChild(addButton('workspace'));
    section.appendChild(saveBar('workspace', 'Save', 'workspace'));
    root.appendChild(section);
  }

  function scopeHead(title, hint, path) {
    const head = el('div', 'scope-head');
    head.appendChild(el('h3', null, title));
    if (path) head.appendChild(el('code', 'path', path));
    const node = el('div');
    node.appendChild(head);
    node.appendChild(el('p', 'hint', hint));
    return node;
  }

  /** The rows a state message names, as the form's own row objects. */
  function rowsOf(list) {
    return Array.isArray(list)
      ? list.map((rule) => ({
          effect: EFFECTS.indexOf(rule.effect) >= 0 ? rule.effect : 'allow',
          tool: typeof rule.tool === 'string' ? rule.tool : '',
          pattern: typeof rule.pattern === 'string' ? rule.pattern : '',
          reason: typeof rule.reason === 'string' ? rule.reason : '',
        }))
      : [];
  }

  function tableFor(scope) {
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
    const empty = el('p', 'hint empty', 'No rules. Every call follows the mode alone.');
    table.appendChild(empty);
    bodies[scope] = { body, empty };
    return table;
  }

  function addButton(scope) {
    const add = el('button', 'ghost', 'Add rule');
    add.type = 'button';
    add.disabled = !editable[scope];
    add.addEventListener('click', () => {
      if (!editable[scope]) return;
      rows[scope].push({ effect: 'allow', tool: '', pattern: '', reason: '' });
      dirty[scope] = true;
      renderTable(scope);
      const inputs = bodies[scope].body.querySelectorAll('input.tool');
      if (inputs.length > 0) inputs[inputs.length - 1].focus();
    });
    return add;
  }

  /** Open JSON, the section's status and its Save — one layer per save. */
  function saveBar(scope, label, openSource) {
    const bar = el('div', 'buttons');
    const status = el('span', 'status');
    status.id = 'status-' + scope;
    const openFile = el('button', 'ghost', 'Open JSON');
    const save = el('button', 'primary', label);
    openFile.type = 'button';
    save.type = 'button';
    save.id = 'save-' + scope;

    openFile.addEventListener('click', () =>
      vscode.postMessage({ type: 'openFile', source: openSource }),
    );
    save.addEventListener('click', () => saveScope(scope));

    bar.appendChild(status);
    bar.appendChild(el('span', 'spacer'));
    bar.appendChild(openFile);
    bar.appendChild(save);
    return bar;
  }

  /** Rebuilds one layer's table; numbering follows the list's order. */
  function renderTable(scope) {
    const { body, empty } = bodies[scope] || {};
    if (!body) return;
    body.textContent = '';
    rows[scope].forEach((rule, index) => body.appendChild(ruleRow(scope, rule, index)));
    empty.style.display = rows[scope].length === 0 ? '' : 'none';
    refreshSave();
  }

  function ruleRow(scope, rule, index) {
    const canEdit = editable[scope];
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
    effect.disabled = !canEdit;
    effect.addEventListener('change', () => {
      rule.effect = effect.value;
      touch(scope);
    });
    row.appendChild(effect);

    row.appendChild(field(scope, rule, 'tool', 'shell', 'tool'));
    row.appendChild(field(scope, rule, 'pattern', 'readonly', 'pattern'));
    row.appendChild(field(scope, rule, 'reason', 'why this rule exists', 'reason'));

    // Delete is a row edit like typing: Save is what makes it real, so there is
    // no confirmation to answer — the edit is still undoable with Reload until
    // it is saved.
    const remove = el('button', 'ghost delete', 'Delete');
    remove.type = 'button';
    remove.disabled = !canEdit;
    remove.addEventListener('click', () => {
      rows[scope].splice(index, 1);
      dirty[scope] = true;
      renderTable(scope);
    });
    row.appendChild(remove);
    return row;
  }

  function field(scope, rule, name, placeholder, className) {
    const input = document.createElement('input');
    input.type = 'text';
    input.className = className;
    input.placeholder = placeholder;
    input.value = rule[name] || '';
    input.disabled = !editable[scope];
    input.addEventListener('input', () => {
      rule[name] = input.value;
      touch(scope);
    });
    return input;
  }

  function touch(scope) {
    dirty[scope] = true;
    refreshSave();
  }

  function refreshSave() {
    for (const scope of ['user', 'workspace']) {
      const save = document.getElementById('save-' + scope);
      const status = document.getElementById('status-' + scope);
      if (save) {
        save.disabled = !editable[scope] || saving || !dirty[scope];
        save.textContent =
          saving && savingSource === scope
            ? 'Saving…'
            : scope === 'user'
              ? 'Save your rules'
              : 'Save';
      }
      if (status) {
        if (saving && savingSource === scope) status.textContent = 'Saving…';
        else if (!editable[scope]) {
          status.textContent =
            scope === 'user' && userMissing
              ? 'Create the user file to add rules.'
              : 'Read-only until the problem is fixed.';
        } else if (dirty[scope]) status.textContent = 'Unsaved changes.';
        else status.textContent = '';
      }
    }
  }

  function saveScope(scope) {
    if (!editable[scope] || saving) return;
    saving = true;
    savingSource = scope;
    refreshSave();
    const rules = rows[scope].map((rule) => ({
      effect: rule.effect,
      tool: (rule.tool || '').trim(),
      pattern: (rule.pattern || '').trim(),
      reason: (rule.reason || '').trim(),
    }));
    if (scope === 'user') {
      vscode.postMessage({
        type: 'save',
        source: 'user',
        rules,
        protected: protectedPaths(),
      });
      return;
    }
    vscode.postMessage({
      type: 'save',
      source: 'workspace',
      rules,
      defaultMode: modeSelect ? modeSelect.value : '',
    });
  }

  /** One glob per line, or separated by commas; blanks mean nothing. */
  function protectedPaths() {
    if (!protectedInput) return [];
    return protectedInput.value
      .split(/[\n,]/)
      .map((path) => path.trim())
      .filter((path) => path !== '');
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
        savingSource = typeof msg.source === 'string' ? msg.source : '';
        refreshSave();
        return;
      case 'error':
        // An error does not re-render: the rows are the user's edit, and a
        // refused save is not a reason to throw them away.
        saving = false;
        savingSource = '';
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
