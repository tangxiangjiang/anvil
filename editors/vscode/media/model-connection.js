(function () {
  const vscode = acquireVsCodeApi();
  const form = document.getElementById('connectionForm');
  const provider = document.getElementById('provider');
  const baseUrl = document.getElementById('baseUrl');
  const effort = document.getElementById('effort');
  const modeSelect = document.getElementById('mode');
  const singleModelId = document.getElementById('singleModelId');
  const singleFields = document.getElementById('singleFields');
  const gatewayFields = document.getElementById('gatewayFields');
  const banner = document.getElementById('banner');

  function mode() {
    return modeSelect.value === 'single' ? 'single' : 'gateway';
  }

  function syncMode() {
    const single = mode() === 'single';
    singleFields.hidden = !single;
    gatewayFields.hidden = single;
    singleModelId.required = single;
  }

  function payload(type) {
    return {
      type,
      provider: provider.value,
      baseUrl: baseUrl.value.trim(),
      mode: mode(),
      singleModelId: singleModelId.value.trim(),
      effort: effort.value,
    };
  }

  function showBanner(kind, text) {
    banner.hidden = !text;
    banner.className = 'banner ' + (kind || '');
    banner.textContent = text || '';
  }

  modeSelect.addEventListener('change', syncMode);
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    showBanner('', '');
    vscode.postMessage(payload('save'));
  });
  document.getElementById('openManager').addEventListener('click', () => {
    showBanner('', '');
    vscode.postMessage(payload('openManager'));
  });
  document.getElementById('setApiKey').addEventListener('click', () => {
    showBanner('', '');
    vscode.postMessage({ type: 'setApiKey' });
  });

  window.addEventListener('message', (event) => {
    const message = event.data || {};
    if (message.type === 'error') {
      showBanner('error', message.message || 'Could not save the model settings.');
      return;
    }
    if (message.type !== 'state') return;
    provider.value = message.provider === 'anthropic' ? 'anthropic' : 'openai';
    baseUrl.value = message.baseUrl || '';
    singleModelId.value = message.singleModelId || '';
    effort.value = ['low', 'high'].includes(message.effort) ? message.effort : 'medium';
    modeSelect.value = message.mode === 'single' ? 'single' : 'gateway';
    syncMode();
    showBanner(message.problem ? 'error' : message.note ? 'success' : '', message.problem || message.note || '');
  });

  vscode.postMessage({ type: 'ready' });
})();
