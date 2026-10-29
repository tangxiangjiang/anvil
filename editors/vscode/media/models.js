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
  let availableIds = [];
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
      available.appendChild(
        modelRow(
          id,
          id === defaultModel.value ? 'Default' : '',
          actionButton('×', 'Remove ' + id, () => vscode.postMessage({ type: 'remove', id })),
        ),
      );
    }
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
    const opening = manualForm.hidden;
    manualForm.hidden = !opening;
    addManualButton.setAttribute('aria-expanded', String(opening));
    if (opening) manualId.focus();
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
    catalogUrl.value = message.catalogUrl || '';
    renderDefault(message.defaultModel || '');
    renderAvailable(availableIds);
    renderDiscovered(message.discovered || []);
    renderProfiles(message.subagents);
    if (message.profileSaved) closeProfileForm();
    showBanner(message.note ? 'success' : '', message.note || '');
  });

  vscode.postMessage({ type: 'ready' });
})();
