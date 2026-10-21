/**
 * The Tasks panel: the background commands and subagents of this window.
 *
 * One document per window, revealed rather than duplicated like the permissions
 * manager — there is one task table per live session, and a second copy of it
 * would be a second place where a Cancel button could be pressed. The rows come
 * from the host every time, not from a count kept here: the tasks belong to the
 * agent, and a panel that remembered them would disagree with the host the
 * moment a session closed.
 *
 * The document is a viewer. It renders what the host reported, asks for one
 * task's output on demand, and asks the host to stop one. It never decides a
 * task's status on its own, and it never renders model or command output as
 * markup: everything a task produced is text.
 */

import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import { mediaAsset } from './webviewAsset';
import { HostManager } from './hostManager';
import { SessionInfo, TaskInfo, TasksOutputResult } from './protocol';
import { transcriptFor } from './taskList';

export const TasksViewType = 'anvil.tasks';

/** One row: the task, and the session tree it belongs to. */
export interface TaskRow {
  /** The live session whose root agent owns the table. */
  sessionId: string;
  task: TaskInfo;
}

/** What the panel needs from the window it belongs to. */
export interface TasksPanelHost {
  /** Every task of every live session in this window, in a stable order. */
  listTasks(): Promise<TaskRow[]>;
  /** One task's output, in byte order from an offset. */
  taskOutput(sessionId: string, taskId: string, offset: number, limit: number): Promise<TasksOutputResult>;
  /** Stops a running task and answers its final snapshot. */
  cancelTask(sessionId: string, taskId: string): Promise<TaskInfo>;
  /** The stored sessions, which is where a child transcript is found. */
  listSessions(): Promise<SessionInfo[]>;
  /** Opens a stored agent in a tab. */
  openSession(agentId: string): Promise<void>;
  /** Fires when a task started or ended, in any session. */
  onTasksChanged(listener: () => void): vscode.Disposable;
}

/** How many output pages to read before giving up on a stream that keeps growing. */
const MAX_OUTPUT_PAGES = 50;

export class TasksPanels implements vscode.Disposable {
  private panel: vscode.WebviewPanel | undefined;
  /** Re-lists on a lifecycle change, but only while the document is visible. */
  private readonly subscription: vscode.Disposable;

  constructor(
    private readonly extensionUri: vscode.Uri,
    private readonly host: TasksPanelHost,
  ) {
    this.subscription = host.onTasksChanged(() => {
      if (this.panel?.visible) void this.sendState();
    });
  }

  /** Opens the panel, or brings the one that is open to the front. */
  open(): void {
    if (this.panel) {
      this.panel.reveal();
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      TasksViewType,
      'Tasks',
      vscode.ViewColumn.Active,
      {
        enableScripts: true,
        // A reopened document lists afresh, so nothing in it is worth keeping
        // alive while hidden.
        localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, 'media')],
      },
    );
    this.panel = panel;
    panel.webview.html = renderHtml(panel.webview, this.extensionUri);
    panel.webview.onDidReceiveMessage((raw: unknown) => {
      void this.handle(raw);
    });
    panel.onDidDispose(() => {
      if (this.panel === panel) this.panel = undefined;
    });
  }

  dispose(): void {
    this.subscription.dispose();
    this.panel?.dispose();
    this.panel = undefined;
  }

  private async handle(raw: unknown): Promise<void> {
    if (!raw || typeof raw !== 'object') return;
    const message = raw as Record<string, unknown>;

    // The gate is here as well as in the manager: this handler is a second entry
    // point into the host, and an untrusted workspace must not get one through
    // it just because the document asked.
    try {
      HostManager.assertTrusted();
    } catch (err) {
      await this.post({ type: 'error', message: (err as Error).message });
      return;
    }

    switch (message.type) {
      case 'ready':
      case 'refresh':
        await this.sendState();
        return;
      case 'output':
        await this.sendOutput(message);
        return;
      case 'cancel':
        await this.cancel(message);
        return;
      case 'openTranscript':
        if (typeof message.agentId === 'string' && message.agentId !== '') {
          await this.host.openSession(message.agentId);
        }
        return;
    }
  }

  /** Lists the tasks and the sessions a row's transcript can be matched to. */
  private async sendState(): Promise<void> {
    let rows: TaskRow[];
    try {
      rows = await this.host.listTasks();
    } catch (err) {
      await this.post({ type: 'state', rows: [], error: (err as Error).message });
      return;
    }
    // The stored sessions are only consulted to match an agent task to its
    // child's transcript, and only when there is a row to match. Asking for them
    // with nothing to show would start a host to answer a question nobody asked.
    const sessions: SessionInfo[] =
      rows.length > 0 ? await this.host.listSessions().catch(() => [] as SessionInfo[]) : [];
    await this.post({
      type: 'state',
      rows: rows.map((row) => ({
        sessionId: row.sessionId,
        id: row.task.id,
        kind: row.task.kind,
        title: row.task.title,
        status: row.task.status,
        agentId: row.task.agent_id ?? '',
        startedAt: row.task.started_at,
        endedAt: row.task.ended_at ?? '',
        exitCode: row.task.exit_code,
        transcriptAgentId: transcriptFor(row.task, sessions) ?? '',
      })),
    });
  }

  /**
   * Reads one task's output and posts it whole.
   *
   * The ring is read page by page rather than assumed to fit one answer. A page
   * that says `truncated` means the start had already been dropped; the loop
   * continues from `next_offset`, so what is shown is the output that still
   * exists, with a note that older bytes are gone rather than a silent gap.
   */
  private async sendOutput(message: Record<string, unknown>): Promise<void> {
    const sessionId = message.sessionId;
    const taskId = message.taskId;
    if (typeof sessionId !== 'string' || typeof taskId !== 'string') return;
    let offset = 0;
    let text = '';
    let truncated = false;
    let status = '';
    let exitCode = 0;
    try {
      for (let page = 0; page < MAX_OUTPUT_PAGES; page++) {
        const result = await this.host.taskOutput(sessionId, taskId, offset, 0);
        status = result.status;
        exitCode = result.exit_code;
        if (result.truncated === true) truncated = true;
        text += result.output;
        // A finished task answers an empty chunk at the end, which is where a
        // page with nothing new stops the loop.
        if (result.next_offset <= offset) break;
        offset = result.next_offset;
      }
    } catch (err) {
      await this.post({ type: 'output', taskId, error: (err as Error).message });
      return;
    }
    await this.post({ type: 'output', taskId, status, exitCode, output: text, truncated });
  }

  /** Stops one task, then lists again so every surface shows the truth. */
  private async cancel(message: Record<string, unknown>): Promise<void> {
    const sessionId = message.sessionId;
    const taskId = message.taskId;
    if (typeof sessionId !== 'string' || typeof taskId !== 'string') return;
    try {
      const task = await this.host.cancelTask(sessionId, taskId);
      await this.post({ type: 'cancelled', taskId, status: task.status });
    } catch (err) {
      await this.post({ type: 'cancelled', taskId, error: (err as Error).message });
    }
    await this.sendState();
  }

  private async post(message: Record<string, unknown>): Promise<void> {
    if (!this.panel) return;
    await this.panel.webview.postMessage(message);
  }
}

function makeNonce(): string {
  return randomBytes(16).toString('base64');
}

function renderHtml(webview: vscode.Webview, extensionUri: vscode.Uri): string {
  const nonce = makeNonce();
  const asset = mediaAsset(webview, extensionUri);
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource}; font-src ${webview.cspSource}; script-src 'nonce-${nonce}';">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<link rel="stylesheet" href="${asset('tokens.css')}">
<link rel="stylesheet" href="${asset('panel.css')}">
<link rel="stylesheet" href="${asset('tasks.css')}">
<title>Tasks</title>
</head>
<body>
<main id="root">
  <div class="toolbar">
    <h1>Background tasks</h1>
    <button id="refresh" type="button">Refresh</button>
  </div>
  <p id="status" class="note" hidden></p>
  <div id="rows" class="rows"></div>
  <section id="output" class="output" hidden>
    <div class="output-head">
      <span id="outputTitle"></span>
      <button id="outputRefresh" type="button">Refresh</button>
      <button id="outputClose" type="button">Close</button>
    </div>
    <p id="dropped" class="dropped" hidden>Older output was dropped before the ring kept the rest.</p>
    <pre id="outputText"></pre>
  </section>
</main>
<script nonce="${nonce}" src="${asset('tasks.js')}"></script>
</body>
</html>`;
}
