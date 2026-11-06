(function () {
  const vscode = acquireVsCodeApi();
  const banner = document.getElementById('banner');
  const available = document.getElementById('available');
  const discovered = document.getElementById('discovered');
  const defaultModel = document.getElementById('defaultModel');
  const catalogUrl = document.getElementById('catalogUrl');
  const manualId = document.getElementById('manualId');
  const manualForm = document.getElementById('manual');
  const addManualButton = document.getElementById('addManual');
  const fetchButton = document.getElementById('fetch');
  const profiles = document.getElementById('profiles');
  const profileForm = document.getElementById('profileForm');
  const profileProblem = document.getElementById('profileProblem');
  const profileId = document.getElementById('profileId');
  const profileDescription = document.getElementById('profileDescription');
  const profileInstructions = document.getElementById('profileInstructions');
  const profileModel = document.getElementById('profileModel');
  const profileMaxSteps = document.getElementById('profileMaxSteps');
  const profileTools = document.getElementById('profileTools');
  const openProfiles = document.getElementById('openProfiles');
  const routeEditor = document.getElementById('routeEditor');
  const routeForm = document.getElementById('routeForm');
  const routeModel = document.getElementById('routeModel');
  const routeProvider = document.getElementById('routeProvider');
  const routeBaseUrl = document.getElementById('routeBaseUrl');
  const routeCredential = document.getElementById('routeCredential');
  const removeRoute = document.getElementById('removeRoute');
  let availableIds = [];
  let routes = {};
  let routeKeys = { openai: false, anthropic: false };
  let profileState = { profiles: [], tool_groups: [], default_tools: [] };

  function element(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function showBanner(kind, text) {
    banner.hidden = !text;
    banner.className = 'banner ' + (kind || '');
    banner.textContent = text || '';
  }

  function actionButton(label, title, handler) {
    const button = element('button', 'row-action', label);
    button.type = 'button';
    button.title = title;
    button.setAttribute('aria-label', title);
    button.addEventListener('click', handler);
    return button;
  }

  function modelRow(id, detail, action) {
    const row = element('div', 'model-row');
    row.setAttribute('role', 'listitem');
    const copy = element('div', 'model-copy');
    copy.appendChild(element('strong', '', id));
    if (detail) copy.appendChild(element('small', '', detail));
    row.appendChild(copy);
    row.appendChild(action);
    return row;
  }

  function renderAvailable(ids) {
    available.replaceChildren();
    if (ids.length === 0) {
      available.appendChild(element('p', 'empty', 'No models are available yet.'));
      return;
    }
    for (const id of ids) {
      const route = routes[id];
      const detail = route
        ? (route.provider === 'anthropic' ? 'Anthropic' : 'OpenAI-compatible') + ' · ' + route.baseUrl
        : 'Inherit main connection';
      const actions = element('div', 'model-actions');
      actions.appendChild(actionButton('Route', 'Configure route for ' + id, () => openRoute(id)));
      actions.appendChild(actionButton('×', 'Remove ' + id, () => vscode.postMessage({ type: 'remove', id })));
      available.appendChild(
        modelRow(
          id,
          [id === defaultModel.value ? 'Default' : '', detail].filter(Boolean).join(' · '),
          actions,
        ),
      );
    }
  }

  function openRoute(id) {
    const route = routes[id] || {};
    routeModel.textContent = id;
    routeProvider.value = route.provider === 'anthropic' ? 'anthropic' : 'openai';
    updateRoutePlaceholder();
    routeBaseUrl.value = route.baseUrl || '';
    routeCredential.value =
      route.credential === 'openai' || route.credential === 'anthropic' ? route.credential : 'default';
    removeRoute.disabled = !routes[id];
    routeEditor.hidden = false;
    routeBaseUrl.focus();
  }

  function updateRoutePlaceholder() {
    routeBaseUrl.placeholder = routeProvider.value === 'anthropic'
      ? 'https://api.anthropic.com'
      : 'https://gateway.example.com/v1';
  }

  function renderRouteKeys() {
    document.getElementById('openaiKeyState').textContent =
      'OpenAI route key: ' + (routeKeys.openai ? 'stored' : 'not stored');
    document.getElementById('anthropicKeyState').textContent =
      'Anthropic route key: ' + (routeKeys.anthropic ? 'stored' : 'not stored');
    document.getElementById('clearOpenaiKey').disabled = !routeKeys.openai;
    document.getElementById('clearAnthropicKey').disabled = !routeKeys.anthropic;
  }

  function renderDefault(selected) {
    defaultModel.replaceChildren();
    const host = element('option', '', 'Host default');
    host.value = '';
    defaultModel.appendChild(host);
    for (const id of availableIds) {
      const option = element('option', '', id);
      option.value = id;
      defaultModel.appendChild(option);
    }
    if (selected && !availableIds.includes(selected)) {
      const legacy = element('option', '', selected + ' · not available');
      legacy.value = selected;
      defaultModel.appendChild(legacy);
    }
    defaultModel.value = selected || '';
  }

  function renderDiscovered(entries) {
    discovered.replaceChildren();
    if (!Array.isArray(entries) || entries.length === 0) return;
    const enabled = new Set(availableIds);
    for (const entry of entries) {
      if (!entry || typeof entry.id !== 'string' || entry.id === '') continue;
      const facts = [entry.provider, entry.owned_by, entry.vision ? 'vision' : ''].filter(Boolean);
      const added = enabled.has(entry.id);
      const button = actionButton(added ? 'Added' : '← Add', added ? entry.id + ' is available' : 'Add ' + entry.id + ' to available models', () => {
        vscode.postMessage({ type: 'add', id: entry.id });
      });
      button.disabled = added;
      discovered.appendChild(modelRow(entry.id, facts.join(' · '), button));
    }
  }

  function renderProfileModels(selected) {
    profileModel.replaceChildren();
    const inherited = element('option', '', 'Inherit main');
    inherited.value = '';
    profileModel.appendChild(inherited);
    for (const id of availableIds) {
      const option = element('option', '', id);
      option.value = id;
      profileModel.appendChild(option);
    }
    if (selected && !availableIds.includes(selected)) {
      const missing = element('option', '', selected + ' · not available');
      missing.value = selected;
      profileModel.appendChild(missing);
    }
    profileModel.value = selected || '';
  }

  function renderProfileTools(selected) {
    profileTools.replaceChildren();
    const chosen = new Set(Array.isArray(selected) ? selected : []);
    for (const tool of profileState.tool_groups || []) {
      const label = element('label', 'tool-option');
      const input = document.createElement('input');
      input.type = 'checkbox';
      input.value = tool;
      input.checked = chosen.has(tool);
      label.append(input, document.createTextNode(tool));
      profileTools.appendChild(label);
    }
  }

  function openProfileForm(profile) {
    const current = profile || {};
    profileForm.hidden = false;
    profileId.value = current.id || '';
    profileId.disabled = Boolean(current.id);
    profileDescription.value = current.description || '';
    profileInstructions.value = current.instructions || '';
    profileMaxSteps.value = String(current.max_steps || 0);
    renderProfileModels(current.model || '');
    renderProfileTools(current.tools || []);
    profileProblem.hidden = true;
    profileId.focus();
  }

  function closeProfileForm() {
    profileForm.hidden = true;
    profileForm.reset();
    profileId.disabled = false;
  }

  function renderProfiles(state) {
    profileState = state || { profiles: [], tool_groups: [], default_tools: [] };
    profiles.replaceChildren();
    openProfiles.disabled = !profileState.exists;
    openProfiles.title = profileState.exists ? profileState.path : 'The file is created when the first profile is saved.';
    profileProblem.hidden = !profileState.problem;
    profileProblem.textContent = profileState.problem || '';
    document.getElementById('addProfile').disabled = Boolean(profileState.problem);
    if (!Array.isArray(profileState.profiles) || profileState.profiles.length === 0) {
      profiles.appendChild(element('p', 'empty', profileState.problem ? 'Fix the JSON file before editing profiles here.' : 'No subagent profiles yet.'));
      return;
    }
    for (const profile of profileState.profiles) {
      const row = element('div', 'profile-row');
      row.setAttribute('role', 'listitem');
      const copy = element('div', 'profile-copy');
      copy.appendChild(element('strong', '', profile.id));
      copy.appendChild(element('span', '', profile.description));
      const facts = [
        profile.model || 'Inherit main',
        (profile.tools && profile.tools.length ? profile.tools : profileState.default_tools || []).join(', '),
        profile.max_steps ? profile.max_steps + ' steps' : 'Inherited steps',
      ];
      copy.appendChild(element('small', '', facts.join(' · ')));
      const actions = element('div', 'profile-actions');
      actions.appendChild(actionButton('Edit', 'Edit ' + profile.id, () => openProfileForm(profile)));
      actions.appendChild(actionButton('Delete', 'Delete ' + profile.id, () => {
        vscode.postMessage({ type: 'removeProfile', id: profile.id });
      }));
      row.append(copy, actions);
      profiles.appendChild(row);
    }
  }

  document.getElementById('reload').addEventListener('click', () => {
    vscode.postMessage({ type: 'reload' });
  });
  document.getElementById('backSettings').addEventListener('click', () => {
    vscode.postMessage({ type: 'backSettings' });
  });
  addManualButton.addEventListener('click', () => {
    if (manualForm.hidden) {
      manualForm.hidden = false;
      addManualButton.setAttribute('aria-expanded', 'true');
    }
    manualId.focus();
  });
  document.getElementById('closeManual').addEventListener('click', () => {
    manualForm.hidden = true;
    addManualButton.setAttribute('aria-expanded', 'false');
    manualId.value = '';
    addManualButton.focus();
  });
  manualForm.addEventListener('submit', (event) => {
    event.preventDefault();
    const id = manualId.value.trim();
    if (!id) return;
    vscode.postMessage({ type: 'add', id });
    manualId.value = '';
    manualForm.hidden = true;
    addManualButton.setAttribute('aria-expanded', 'false');
  });
  document.getElementById('discover').addEventListener('submit', (event) => {
    event.preventDefault();
    fetchButton.disabled = true;
    fetchButton.textContent = 'Fetching…';
    showBanner('', '');
    vscode.postMessage({ type: 'discover', url: catalogUrl.value });
  });
  defaultModel.addEventListener('change', () => {
    vscode.postMessage({ type: 'setDefault', id: defaultModel.value });
  });
  document.getElementById('closeRoute').addEventListener('click', () => {
    routeEditor.hidden = true;
  });
  routeProvider.addEventListener('change', updateRoutePlaceholder);
  routeForm.addEventListener('submit', (event) => {
    event.preventDefault();
    if (
      (routeCredential.value === 'openai' && !routeKeys.openai)
      || (routeCredential.value === 'anthropic' && !routeKeys.anthropic)
    ) {
      showBanner('error', 'Set the selected route key before saving this route.');
      return;
    }
    vscode.postMessage({
      type: 'saveRoute',
      id: routeModel.textContent,
      route: {
        provider: routeProvider.value,
        baseUrl: routeBaseUrl.value.trim(),
        credential: routeCredential.value,
      },
    });
  });
  removeRoute.addEventListener('click', () => {
    vscode.postMessage({ type: 'removeRoute', id: routeModel.textContent });
  });
  document.getElementById('setOpenaiKey').addEventListener('click', () => {
    vscode.postMessage({ type: 'setRouteKey', provider: 'openai' });
  });
  document.getElementById('clearOpenaiKey').addEventListener('click', () => {
    vscode.postMessage({ type: 'clearRouteKey', provider: 'openai' });
  });
  document.getElementById('setAnthropicKey').addEventListener('click', () => {
    vscode.postMessage({ type: 'setRouteKey', provider: 'anthropic' });
  });
  document.getElementById('clearAnthropicKey').addEventListener('click', () => {
    vscode.postMessage({ type: 'clearRouteKey', provider: 'anthropic' });
  });
  document.getElementById('addProfile').addEventListener('click', () => openProfileForm());
  document.getElementById('cancelProfile').addEventListener('click', closeProfileForm);
  openProfiles.addEventListener('click', () => vscode.postMessage({ type: 'openProfiles' }));
  profileForm.addEventListener('submit', (event) => {
    event.preventDefault();
    const selectedTools = Array.from(profileTools.querySelectorAll('input:checked')).map((input) => input.value);
    vscode.postMessage({
      type: 'saveProfile',
      profile: {
        id: profileId.value.trim(),
        description: profileDescription.value.trim(),
        instructions: profileInstructions.value.trim(),
        model: profileModel.value,
        tools: selectedTools,
        max_steps: Number(profileMaxSteps.value) || 0,
      },
    });
  });

  window.addEventListener('message', (event) => {
    const message = event.data || {};
    if (message.type === 'profileError') {
      profileProblem.hidden = false;
      profileProblem.textContent = message.message || 'Could not save profile.';
      return;
    }
    if (message.type === 'error') {
      fetchButton.disabled = false;
      fetchButton.textContent = 'Fetch models';
      showBanner('error', message.message || 'Could not load models.');
      return;
    }
    if (message.type !== 'state') return;
    fetchButton.disabled = false;
    fetchButton.textContent = 'Fetch models';
    availableIds = Array.isArray(message.available) ? message.available : [];
    routes = message.routes && typeof message.routes === 'object' ? message.routes : {};
    routeKeys = message.routeKeys && typeof message.routeKeys === 'object'
      ? message.routeKeys
      : { openai: false, anthropic: false };
    catalogUrl.value = message.catalogUrl || '';
    renderDefault(message.defaultModel || '');
    renderAvailable(availableIds);
    renderDiscovered(message.discovered || []);
    renderProfiles(message.subagents);
    renderRouteKeys();
    if (!routeEditor.hidden && routeModel.textContent) {
      if (availableIds.includes(routeModel.textContent)) openRoute(routeModel.textContent);
      else routeEditor.hidden = true;
    }
    if (message.profileSaved) closeProfileForm();
    showBanner(message.note ? 'success' : '', message.note || '');
  });

  vscode.postMessage({ type: 'ready' });
})();
