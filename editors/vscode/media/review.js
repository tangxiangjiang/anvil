/*
 * The per-hunk review document.
 *
 * It renders only what the extension sends: the file path and the hunks of a
 * proposed change. Everything is written with textContent — hunk lines are file
 * content, and file content is data. There is no innerHTML here, and there is no
 * reason for one: the document is a list of checkboxes and a diff.
 *
 * It answers exactly once. Each button disables itself and its neighbours on
 * the way out, because the extension closes this document when the answer
 * lands, and a second click in the gap would name an approval that is no longer
 * pending.
 */
(function () {
  const vscode = acquireVsCodeApi();
  const root = document.getElementById('root');
  /** Set by the first button press. A document answers once. */
  let answered = false;
  /** The Escape key's way to the Cancel button. */
  let cancelButton = null;

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  function render(msg) {
    root.textContent = '';
    answered = false;
    cancelButton = null;

    root.appendChild(el('h2', 'title', 'Review changes'));
    root.appendChild(el('div', 'path', msg.path || 'This change'));
    root.appendChild(
      el('p', 'hint', 'Choose the parts of the change to keep. An unchecked part is not applied.'),
    );

    const hunks = Array.isArray(msg.hunks) ? msg.hunks : [];
    const boxes = [];

    if (hunks.length === 0) {
      root.appendChild(el('p', 'hint', 'There is nothing to choose here.'));
    } else {
      const list = el('div', 'hunks');
      hunks.forEach((hunk, index) => {
        const section = el('section', 'hunk');
        const head = el('label', 'hunk-head');
        const box = document.createElement('input');
        box.type = 'checkbox';
        box.className = 'hunk-box';
        box.checked = true;
        // The index is the only thing that travels back: the extension applies
        // it to its own copy of the hunks, which is the one that was proven to
        // match the file.
        box.setAttribute('data-index', String(index));
        head.appendChild(box);
        head.appendChild(el('code', 'hunk-range', hunk.header || 'part ' + (index + 1)));
        section.appendChild(head);

        const body = el('div', 'hunk-body');
        const lines = Array.isArray(hunk.lines) ? hunk.lines : [];
        for (const line of lines) {
          const kind = line.kind === 'add' || line.kind === 'del' ? line.kind : 'context';
          const marker = kind === 'add' ? '+' : kind === 'del' ? '-' : ' ';
          body.appendChild(el('div', 'line ' + kind, marker + (line.text || '')));
        }
        if (lines.length === 0) body.appendChild(el('div', 'line context', ' '));
        section.appendChild(body);
        list.appendChild(section);
        boxes.push(box);
      });
      root.appendChild(list);
    }

    const footer = el('div', 'buttons');
    const count = el('span', 'count');
    const cancel = el('button', 'ghost', 'Cancel');
    const reject = el('button', 'secondary', 'Reject all');
    const accept = el('button', 'primary', 'Accept selected');
    cancel.type = 'button';
    reject.type = 'button';
    accept.type = 'button';
    cancelButton = cancel;

    function refresh() {
      const checked = boxes.filter((box) => box.checked).length;
      count.textContent =
        checked === hunks.length
          ? hunks.length === 1
            ? 'Applying the whole change'
            : `Applying all ${hunks.length} parts`
          : `Applying ${checked} of ${hunks.length} parts`;
      accept.disabled = answered || checked === 0;
      reject.disabled = answered;
      cancel.disabled = answered;
    }

    function settle() {
      answered = true;
      for (const button of [cancel, reject, accept]) button.disabled = true;
    }

    for (const box of boxes) box.addEventListener('change', refresh);

    accept.addEventListener('click', () => {
      if (answered) return;
      const accepted = boxes
        .filter((box) => box.checked)
        .map((box) => Number(box.getAttribute('data-index')));
      if (accepted.length === 0) return;
      settle();
      vscode.postMessage({ type: 'accept', accepted });
    });
    reject.addEventListener('click', () => {
      if (answered) return;
      settle();
      vscode.postMessage({ type: 'reject' });
    });
    cancel.addEventListener('click', () => {
      if (answered) return;
      settle();
      vscode.postMessage({ type: 'cancel' });
    });

    footer.appendChild(count);
    footer.appendChild(el('span', 'spacer'));
    footer.appendChild(cancel);
    footer.appendChild(reject);
    footer.appendChild(accept);
    root.appendChild(footer);
    refresh();
  }

  window.addEventListener('message', (event) => {
    const msg = event.data;
    if (!msg || msg.type !== 'review') return;
    render(msg);
  });

  // Escape closes without answering, which is what Cancel does. It is safe to
  // offer: the approval stays pending, and the card in the chat tab still holds
  // its Allow and Deny.
  window.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape' || answered || !cancelButton) return;
    cancelButton.click();
  });

  vscode.postMessage({ type: 'ready' });
})();
