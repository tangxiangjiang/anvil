/*
 * The MCP manager document.
 *
 * It renders only what the extension sends: each server as the host describes it
 * — state, tools, error, the file that declared it — and a form that becomes a
 * draft. Every value is written with textContent or as an input's `value`; there
 * is no innerHTML, and there is no reason for one: a server description is data,
 * and a name a file chose is not markup.
 *
 * The document decides nothing about MCP. It does not know a valid transport
 * when it sees one, it does not know whether a sign-in succeeded, and it never
 * sees a token. A change is a message; the answer is the host's own reading of
 * the file, which is what gets rendered — so a normalised transport or a value
 * the host kept is shown the way the next session will read it.
 *
 * Two renders exist on purpose. `state` and `saved` rebuild everything, including
 * the form, because the answer to a write is a new file. `rows` — the repaint a
 * state transition causes — rebuilds only the banners and the list: a server
 * going from connecting to connected must not throw away the half-typed form
 * the person is standing in.
 */
(function () {
  const vscode = acquireVsCodeApi();
  const root = document.getElementById('root');

  /** Everything the last listing (or the note after a write) said. */
  const state = {
    runtime: false,
    servers: [],
    sources: [],
    notes: [],
    settings: {},
    problem: '',
    problemPath: '',
    listingError: '',
    note: '',
    actionError: '',
    authWaiting: '',
    saving: '',
  };

  /** The server the form is editing, or '' for "add". */
  let editing = '';
  /** The name whose Delete is armed, so a second click is the confirmation. */
  let pendingDelete = '';
  let deleteTimer = null;
  /** The nodes a `rows` message replaces without touching the form. */
  let bannersEl = null;
  let serversEl = null;
  let formEl = null;

  const FIELDS = ['name', 'transport', 'command', 'args', 'env', 'url', 'headers', 'enabled'];

  // --- small builders ---------------------------------------------------------

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  function button(label, className, onClick) {
    const node = el('button', className, label);
    node.type = 'button';
    node.addEventListener('click', onClick);
    return node;
  }

  function banner(className, text) {
    const node = el('div', 'banner ' + className);
    node.appendChild(el('span', 'banner-text', text));
    return node;
  }

  // --- what the listing means -------------------------------------------------

  function rowsOf(raw) {
    if (!Array.isArray(raw)) return [];
    const rows = [];
    for (const item of raw) {
      if (!item || typeof item !== 'object' || typeof item.name !== 'string') continue;
      rows.push({
        name: item.name,
        scope: typeof item.scope === 'string' ? item.scope : '',
        enabled: item.enabled !== false,
        transport: typeof item.transport === 'string' ? item.transport : '',
        source: typeof item.source === 'string' ? item.source : '',
        state: typeof item.state === 'string' ? item.state : undefined,
        state_label: typeof item.state_label === 'string' ? item.state_label : '',
        tool_count: typeof item.tool_count === 'number' ? item.tool_count : undefined,
        tools: Array.isArray(item.tools) ? item.tools : [],
        error: typeof item.error === 'string' ? item.error : '',
        connected_at: typeof item.connected_at === 'string' ? item.connected_at : '',
        command: typeof item.command === 'string' ? item.command : '',
        args: Array.isArray(item.args) ? item.args : [],
        url: typeof item.url === 'string' ? item.url : '',
        env_names: Array.isArray(item.env_names) ? item.env_names : [],
        header_names: Array.isArray(item.header_names) ? item.header_names : [],
      });
    }
    return rows;
  }

  function applyListing(msg) {
    state.runtime = msg.runtime === true;
    state.servers = rowsOf(msg.servers);
    state.sources = Array.isArray(msg.sources) ? msg.sources : [];
    state.notes = Array.isArray(msg.notes) ? msg.notes : [];
    state.settings = msg.settings && typeof msg.settings === 'object' ? msg.settings : {};
    state.problem = typeof msg.problem === 'string' ? msg.problem : '';
    state.problemPath = typeof msg.problemPath === 'string' ? msg.problemPath : '';
    state.listingError = typeof msg.error === 'string' ? msg.error : '';
  }

  /** Whether the form and the row actions may change anything. */
  function editable() {
    return state.runtime && state.problem === '' && state.listingError === '';
  }

  function layerName(source) {
    return source === 'project' || source === 'user' ? source : '';
  }

  // --- banners ----------------------------------------------------------------

  function bannerNodes() {
    const nodes = [];
    if (state.problem) {
      nodes.push(
        banner(
          'error',
          'This file cannot be read as MCP configuration: ' +
            state.problem +
            '. Editing is off so the file is not overwritten; fix it in the JSON.',
        ),
      );
    }
    if (state.listingError) {
      nodes.push(banner('error', 'The MCP servers could not be read: ' + state.listingError));
    }
    if (!state.runtime && !state.problem && !state.listingError) {
      nodes.push(
        banner(
          'warn',
          'This Anvil host cannot report or change MCP servers. Update Anvil to manage them here.',
        ),
      );
    }
    if (state.actionError) nodes.push(banner('error', 'Not saved: ' + state.actionError));
    if (state.authWaiting) {
      nodes.push(
        banner(
          'info',
          'Waiting for the browser: finish signing in to ' +
            state.authWaiting +
            '. The row updates when the server connects. If the editor and the host are on ' +
            'different machines, the browser may not reach the host’s callback.',
        ),
      );
    }
    for (const note of state.notes) {
      if (typeof note === 'string' && note !== '') nodes.push(banner('info', note));
    }
    for (const layer of ['project', 'user']) {
      if (state.settings[layer] !== false) continue;
      const known = state.sources.find((source) => source && source.source === layer);
      nodes.push(
        banner(
          'warn',
          'anvil.mcp.' +
            layer +
            ' is off, so sessions do not read ' +
            (known && known.path ? known.path : 'the ' + layer + ' file') +
            '. Servers added there still work here.',
        ),
      );
    }
    if (state.note) nodes.push(banner('ok', state.note));
    return nodes;
  }

  // --- the list ---------------------------------------------------------------

  function serverNode(server) {
    const row = el('div', 'server');
    row.dataset.name = server.name;

    const head = el('div', 'server-head');
    if (server.state !== undefined) {
      const dot = el('span', 'mcp-dot ' + server.state);
      dot.setAttribute('aria-hidden', 'true');
      head.appendChild(dot);
    }
    head.appendChild(el('span', 'name', server.name));
    const words = stateText(server);
    if (words) head.appendChild(el('span', 'mcp-state', words));
    head.appendChild(el('span', 'spacer'));
    if (server.transport) head.appendChild(el('span', 'transport', server.transport));
    row.appendChild(head);

    const meta = el('div', 'meta');
    const where = [layerName(server.scope), server.source].filter((part) => part !== '').join(' · ');
    if (where) meta.appendChild(el('span', 'source', where));
    const origin = originOf(server);
    if (origin) meta.appendChild(el('span', 'origin', origin));
    const keys = keysOf(server);
    if (keys) meta.appendChild(el('span', 'keys', keys));
    if (meta.childNodes.length > 0) row.appendChild(meta);

    if (server.error) row.appendChild(el('div', 'error', server.error));
    const tools = toolsNode(server);
    if (tools) row.appendChild(tools);
    if (state.runtime) row.appendChild(actionRow(server));
    return row;
  }

  /** The state word, with the tool count only when there is a connection. */
  function stateText(server) {
    if (server.state === undefined) return '';
    const count = server.tool_count;
    if (server.state === 'connected' && typeof count === 'number') {
      return server.state_label + ' · ' + count + (count === 1 ? ' tool' : ' tools');
    }
    return server.state_label;
  }

  /** What the entry actually runs: a command line, or a URL. */
  function originOf(server) {
    if (server.command) return [server.command].concat(server.args).join(' ');
    return server.url;
  }

  /** Key names only: a header or an env value may be a secret. */
  function keysOf(server) {
    const parts = [];
    if (server.env_names.length > 0) parts.push('env ' + server.env_names.join(', '));
    if (server.header_names.length > 0) parts.push('headers ' + server.header_names.join(', '));
    return parts.join(' · ');
  }

  function toolsNode(server) {
    if (server.tools.length === 0) return null;
    const box = el('div', 'tools');
    const list = el('ul', 'tool-list');
    list.hidden = true;
    for (const tool of server.tools) {
      if (!tool || typeof tool.name !== 'string') continue;
      const item = el('li', 'tool');
      item.appendChild(el('span', 'tool-name', tool.name));
      if (typeof tool.description === 'string' && tool.description !== '') {
        item.appendChild(el('span', 'tool-desc', tool.description));
      }
      list.appendChild(item);
    }
    const count = server.tools.length;
    const toggle = button(count === 1 ? 'Show 1 tool' : 'Show ' + count + ' tools', 'link', () => {
      list.hidden = !list.hidden;
      toggle.textContent = list.hidden
        ? count === 1
          ? 'Show 1 tool'
          : 'Show ' + count + ' tools'
        : 'Hide tools';
    });
    box.appendChild(toggle);
    box.appendChild(list);
    return box;
  }

  function actionRow(server) {
    const actions = el('div', 'row-actions');
    const can = editable() && server.scope !== '';
    const on = server.enabled !== false;

    const add = (label, className, disabled, onClick) => {
      const node = button(label, className, onClick);
      node.disabled = disabled;
      actions.appendChild(node);
    };

    add(on ? 'Disable' : 'Enable', 'ghost', !can, () => {
      vscode.postMessage({
        type: 'setEnabled',
        scope: server.scope,
        name: server.name,
        enabled: !on,
      });
    });
    // Reconnecting a disabled server would connect something the file turned
    // off; enabling it first is the way back.
    add('Reconnect', 'ghost', !can || !on, () => {
      vscode.postMessage({ type: 'reconnect', name: server.name });
    });
    // Signing in is a remote server's problem; a stdio server has no tokens to
    // hold, and offering the action would only produce a refusal.
    if (server.transport !== '' && server.transport !== 'stdio') {
      if (server.state === 'auth_required') {
        add('Sign in', 'primary', !can, () => {
          vscode.postMessage({ type: 'authenticate', name: server.name });
        });
      }
      if (server.state === 'connected') {
        add('Sign out', 'ghost', !can, () => {
          vscode.postMessage({ type: 'signOut', name: server.name });
        });
      }
    }
    add('Edit', 'ghost', !can, () => startEdit(server));
    actions.appendChild(deleteButton(server, can));
    return actions;
  }

  /**
   * Delete, confirmed in place.
   *
   * One click arms it and the label changes; the second click is the message.
   * The label reverts by itself, so an armed button cannot be pressed later by
   * someone who has moved on to another row.
   */
  function deleteButton(server, can) {
    const armed = pendingDelete === server.name;
    const node = button(armed ? 'Confirm delete' : 'Delete', armed ? 'delete armed' : 'delete', () => {
      if (pendingDelete !== server.name) {
        pendingDelete = server.name;
        if (deleteTimer) clearTimeout(deleteTimer);
        deleteTimer = setTimeout(() => {
          pendingDelete = '';
          deleteTimer = null;
          refreshList();
        }, 4000);
        refreshList();
        return;
      }
      pendingDelete = '';
      if (deleteTimer) clearTimeout(deleteTimer);
      deleteTimer = null;
      vscode.postMessage({ type: 'remove', scope: server.scope, name: server.name });
    });
    node.disabled = !can;
    return node;
  }

  function refreshList() {
    if (!serversEl) return;
    serversEl.textContent = '';
    if (state.servers.length === 0) {
      serversEl.appendChild(el('p', 'hint empty', 'No MCP servers are configured.'));
    } else {
      for (const server of state.servers) serversEl.appendChild(serverNode(server));
    }
  }

  function refreshBanners() {
    if (!bannersEl) return;
    bannersEl.textContent = '';
    for (const node of bannerNodes()) bannersEl.appendChild(node);
  }

  /**
   * The repaint a state transition causes: banners and rows, never the form.
   *
   * A form opened on a server that has gone away — removed from the file by hand,
   * say — is put back to "add" rather than left pointing at nothing.
   */
  function renderLive(msg) {
    applyListing(msg);
    if (editing !== '' && !state.servers.some((server) => server.name === editing)) {
      setEditing('');
    }
    refreshBanners();
    refreshList();
    refreshForm();
    refreshStatus();
  }

  // --- the form ---------------------------------------------------------------

  function formNode() {
    const form = el('form', 'form');
    form.id = 'mcp-form';
    form.addEventListener('submit', (event) => {
      event.preventDefault();
      submitForm();
    });

    const title = el('h3', 'form-title', 'Add a server');
    title.id = 'form-title';
    form.appendChild(title);
    form.appendChild(
      el(
        'p',
        'hint',
        'Adding writes the file this server’s layer names. Editing changes one entry and leaves ' +
          'fields this editor does not know about alone.',
      ),
    );

    const grid = el('div', 'grid');
    grid.appendChild(labeled('File', scopeSelect()));
    grid.appendChild(labeled('Name', textInput('name', 'filesystem')));
    grid.appendChild(labeled('Transport', transportSelect()));
    grid.appendChild(enabledCheck());
    form.appendChild(grid);
    const target = el('p', 'path');
    target.id = 'target';
    form.appendChild(target);

    form.appendChild(
      fieldset('stdio-fields', [
        labeled('Command', textInput('command', 'npx')),
        labeled('Arguments', textArea('args', 3, 'one argument per line')),
        labeled('Environment', textArea('env', 3, 'KEY=value, one per line')),
      ]),
    );
    form.appendChild(
      fieldset('remote-fields', [
        labeled('URL', textInput('url', 'https://server.example/mcp')),
        labeled('Headers', textArea('headers', 3, 'KEY=value, one per line')),
      ]),
    );
    const keepHint = el('p', 'hint');
    keepHint.id = 'keep-hint';
    form.appendChild(keepHint);

    const footer = el('div', 'form-actions');    const submit = el('button', 'primary', 'Add server');
    submit.type = 'submit';
    submit.id = 'submit';
    const cancel = button('Cancel', 'ghost', () => setEditing(''));
    cancel.id = 'cancel';
    cancel.hidden = true;
    footer.appendChild(submit);
    footer.appendChild(cancel);
    form.appendChild(footer);

    formEl = form;
    refreshTransportFields();
    refreshForm();
    return form;
  }

  function labeled(text, node) {
    const label = el('label', 'field');
    if (text !== '') label.appendChild(el('span', 'field-label', text));
    label.appendChild(node);
    return label;
  }

  function fieldset(className, children) {
    const box = el('div', 'fields ' + className);
    for (const child of children) box.appendChild(child);
    return box;
  }

  function textInput(id, placeholder) {
    const input = document.createElement('input');
    input.type = 'text';
    input.id = id;
    input.placeholder = placeholder;
    input.addEventListener('input', refreshKeepHint);
    return input;
  }

  function textArea(id, rows, placeholder) {
    const area = document.createElement('textarea');
    area.id = id;
    area.rows = rows;
    area.placeholder = placeholder;
    area.addEventListener('input', refreshKeepHint);
    return area;
  }

  function scopeSelect() {
    const select = document.createElement('select');
    select.id = 'scope';
    for (const value of ['project', 'user']) {
      const option = document.createElement('option');
      option.value = value;
      option.textContent = value;
      select.appendChild(option);
    }
    select.addEventListener('change', () => {
      vscode.postMessage({ type: 'scopeChanged', scope: select.value });
    });
    return select;
  }

  function transportSelect() {
    const select = document.createElement('select');
    select.id = 'transport';
    const options = [
      ['', 'Detect'],
      ['stdio', 'stdio'],
      ['http', 'http'],
      ['sse', 'sse'],
    ];
    for (const [value, label] of options) {
      const option = document.createElement('option');
      option.value = value;
      option.textContent = label;
      select.appendChild(option);
    }
    select.value = '';
    select.addEventListener('change', refreshTransportFields);
    return select;
  }

  /**
   * The enabled checkbox, in its own label so the grid cell is one control.
   *
   * A server is written enabled unless the person says otherwise; a disabled
   * entry stays in the file with its configuration and stops running.
   */
  function enabledCheck() {
    const label = el('label', 'field check');
    const input = document.createElement('input');
    input.type = 'checkbox';
    input.id = 'enabled';
    input.checked = true;
    label.appendChild(input);
    label.appendChild(el('span', 'field-label', 'Enabled'));
    return label;
  }

  /** Only the fields the chosen transport can use are shown. */
  function refreshTransportFields() {
    if (!formEl) return;
    const transport = formEl.querySelector('#transport').value;
    formEl.querySelector('.stdio-fields').hidden = transport !== '' && transport !== 'stdio';
    formEl.querySelector('.remote-fields').hidden = transport === 'stdio';
  }

  /** Everything the form holds, in the shape the extension validates. */
  function readForm() {
    const value = (id) => {
      const node = formEl.querySelector('#' + id);
      return node ? String(node.value) : '';
    };
    const checked = formEl.querySelector('#enabled');
    return {
      name: value('name'),
      transport: value('transport'),
      command: value('command'),
      args: value('args'),
      env: value('env'),
      url: value('url'),
      headers: value('headers'),
      enabled: checked ? checked.checked === true : true,
    };
  }

  function startEdit(server) {
    setEditing(server.name);
    if (!formEl) return;
    formEl.querySelector('#scope').value = server.scope === 'user' ? 'user' : 'project';
    formEl.querySelector('#name').value = server.name;
    formEl.querySelector('#transport').value =
      ['stdio', 'http', 'sse'].indexOf(server.transport) >= 0 ? server.transport : '';
    formEl.querySelector('#command').value = server.command;
    formEl.querySelector('#args').value = server.args.join('\n');
    formEl.querySelector('#env').value = '';
    formEl.querySelector('#url').value = server.url;
    formEl.querySelector('#headers').value = '';
    formEl.querySelector('#enabled').checked = server.enabled !== false;
    refreshTransportFields();
    refreshForm();
    formEl.querySelector('#name').focus();
    formEl.scrollIntoView({ block: 'start', behavior: 'smooth' });
    vscode.postMessage({ type: 'scopeChanged', scope: formEl.querySelector('#scope').value });
  }

  function setEditing(name) {
    editing = name;
    if (!formEl) return;
    formEl.querySelector('#scope').disabled = name !== '' || !editable();
    formEl.querySelector('#name').disabled = name !== '';
    formEl.querySelector('#form-title').textContent = name === '' ? 'Add a server' : 'Edit ' + name;
    formEl.querySelector('#submit').textContent = name === '' ? 'Add server' : 'Save';
    formEl.querySelector('#cancel').hidden = name === '';
    if (name === '') {
      for (const id of FIELDS) {
        const node = formEl.querySelector('#' + id);
        if (!node) continue;
        if (node.type === 'checkbox') node.checked = true;
        else if (node.tagName === 'SELECT') node.value = '';
        else node.value = '';
      }
      refreshTransportFields();
    }
    refreshForm();
  }

  /** Disables what may not be used and shows why, without rebuilding inputs. */
  function refreshForm() {
    if (!formEl) return;
    const open = editable();
    for (const node of formEl.querySelectorAll('input, textarea, select')) {
      if (node.id === 'name' || node.id === 'scope') continue;
      node.disabled = !open;
    }
    formEl.querySelector('#scope').disabled = !open || editing !== '';
    formEl.querySelector('#name').disabled = !open || editing !== '';
    formEl.querySelector('#submit').disabled = !open || state.saving !== '';
    refreshKeepHint();
  }

  /**
   * The line under the form about the env/header values a save would touch.
   *
   * The host takes a blank section as "keep the file's values" and a filled one
   * as the whole new map, so a person who types one variable is replacing every
   * one. Saying which names survive is the difference between an edit and a
   * silent deletion of a token they cannot retype.
   */
  function refreshKeepHint() {
    if (!formEl) return;
    const hint = formEl.querySelector('#keep-hint');
    const server = state.servers.find((row) => row.name === editing);
    if (!server) {
      hint.textContent = '';
      return;
    }
    const remote = formEl.querySelector('#transport').value !== 'stdio' &&
      (formEl.querySelector('#transport').value !== '' || formEl.querySelector('#url').value !== '');
    const existing = remote ? server.header_names : server.env_names;
    const kind = remote ? 'header' : 'environment variable';
    if (existing.length === 0) {
      hint.textContent = 'This entry has no ' + kind + 's.';
      return;
    }
    const typed = remote ? formEl.querySelector('#headers').value : formEl.querySelector('#env').value;
    if (typed.trim() === '') {
      hint.textContent =
        'Existing ' + kind + 's are kept: ' + existing.join(', ') + '.';
      return;
    }
    const kept = new Set(
      typed
        .split('\n')
        .map((line) => line.replace(/\r$/, ''))
        .filter((line) => line.indexOf('=') > 0)
        .map((line) => line.slice(0, line.indexOf('=')).trim()),
    );
    const dropped = existing.filter((name) => !kept.has(name));
    hint.textContent =
      dropped.length === 0
        ? 'Saving replaces the ' + kind + ' values with the ones typed here.'
        : 'Saving replaces every ' + kind + ' value: ' + dropped.join(', ') +
          (dropped.length === 1 ? ' is' : ' are') + ' removed because the value is not typed here.';
  }

  function submitForm() {
    if (!formEl || !editable() || state.saving !== '') return;
    const form = readForm();
    const scope = formEl.querySelector('#scope').value;
    state.saving = editing === '' ? 'Adding…' : 'Saving…';
    vscode.postMessage({ type: editing === '' ? 'add' : 'update', scope, form });
    refreshForm();
    refreshStatus();
  }

  // --- the document -----------------------------------------------------------

  function documentTitle() {
    return el('h2', 'title', 'MCP servers');
  }

  function pathsLine() {
    const box = el('div', 'paths');
    if (state.sources.length === 0) {
      box.appendChild(el('span', 'path', 'No settings layer was asked about.'));
      return box;
    }
    for (const source of state.sources) {
      if (!source || typeof source.path !== 'string') continue;
      const line = el(
        'span',
        'path',
        layerName(source.source) + ': ' + source.path + (source.exists === true ? '' : ' (not found)'),
      );
      box.appendChild(line);
    }
    return box;
  }

  function footerNode() {
    const footer = el('div', 'buttons');
    const status = el('span', 'status');
    status.id = 'status';
    footer.appendChild(status);
    footer.appendChild(el('span', 'spacer'));
    footer.appendChild(button('Reload', 'ghost', () => vscode.postMessage({ type: 'reload' })));
    footer.appendChild(button('Open mcp.json', 'ghost', openFile));
    return footer;
  }

  /**
   * Opens the file the person is looking at: the one that would not parse when
   * there is one, else the layer the form writes.
   */
  function openFile() {
    let scope = formEl ? formEl.querySelector('#scope').value : 'project';
    if (state.problemPath) {
      const broken = state.sources.find((source) => source && source.path === state.problemPath);
      if (broken) scope = broken.source;
    }
    vscode.postMessage({ type: 'openFile', scope });
  }

  function refreshStatus() {
    const status = document.getElementById('status');
    if (!status) return;
    if (state.saving !== '') status.textContent = state.saving;
    else if (!editable()) status.textContent = 'Read-only until the setting or the file is fixed.';
    else status.textContent = '';
  }

  function renderAll() {
    root.textContent = '';
    pendingDelete = '';
    if (deleteTimer) clearTimeout(deleteTimer);
    deleteTimer = null;

    root.appendChild(documentTitle());
    root.appendChild(pathsLine());
    root.appendChild(
      el(
        'p',
        'hint',
        'Each row is a server a file declares, with what the host observed. Disabling one keeps ' +
          'its configuration and stops it from running.',
      ),
    );

    bannersEl = el('div', 'banners');
    serversEl = el('div', 'servers');
    root.appendChild(bannersEl);
    root.appendChild(serversEl);
    root.appendChild(formNode());
    root.appendChild(footerNode());

    refreshBanners();
    refreshList();
    refreshStatus();
    vscode.postMessage({ type: 'scopeChanged', scope: formEl.querySelector('#scope').value });
  }

  window.addEventListener('message', (event) => {
    const msg = event.data;
    if (!msg || typeof msg !== 'object') return;
    switch (msg.type) {
      case 'state':
        state.note = '';
        state.actionError = '';
        state.authWaiting = '';
        state.saving = '';
        applyListing(msg);
        editing = '';
        renderAll();
        return;
      case 'saved':
        state.note = typeof msg.note === 'string' ? msg.note : 'Saved.';
        state.actionError = '';
        state.authWaiting = '';
        state.saving = '';
        applyListing(msg);
        editing = '';
        renderAll();
        return;
      case 'rows':
        renderLive(msg);
        return;
      case 'saving':
        state.saving = msg.saving === true ? (typeof msg.what === 'string' && msg.what !== '' ? msg.what : 'Saving…') : '';
        refreshForm();
        refreshStatus();
        return;
      case 'error':
        // An error never re-renders: the rows and the form are the user's edit,
        // and a refused save is not a reason to throw them away.
        state.saving = '';
        state.actionError = String(msg.message || 'the host refused it');
        refreshBanners();
        refreshForm();
        refreshStatus();
        return;
      case 'auth':
        state.authWaiting = typeof msg.server === 'string' ? msg.server : '';
        refreshBanners();
        return;
      case 'target': {
        const target = document.getElementById('target');
        if (!target) return;
        const path = typeof msg.path === 'string' ? msg.path : '';
        if (path === '') {
          target.textContent = 'This layer was not in the last listing.';
          return;
        }
        const parts = ['Writes ' + path];
        if (msg.exists !== true) parts.push('created on the first save');
        if (msg.read === false) parts.push('sessions do not read this file (anvil.mcp.' + msg.scope + ' is off)');
        target.textContent = parts.join(' — ');
        return;
      }
    }
  });

  vscode.postMessage({ type: 'ready' });
})();
