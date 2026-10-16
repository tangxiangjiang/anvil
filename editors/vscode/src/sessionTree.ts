/**
 * The session sidebar: a native tree over this workspace's stored agents.
 *
 * Everything with a decision in it lives in `sessionList.ts`; this file only
 * maps those nodes onto `TreeItem`s and decides *when* to ask the host. The
 * `vscode` boundary is the test boundary, as everywhere else in the extension.
 *
 * The load is deliberately lazy. `session.list` needs a live sidecar, and the
 * extension is activated for a restored chat tab as well as for the sidebar —
 * so the first request is made when the view is first asked for children, which
 * only happens once it is visible, and never at activation.
 */

import * as vscode from 'vscode';
import {
  SessionNode,
  SessionNote,
  SessionRow,
  SessionViewOptions,
  buildSessionView,
  sessionNote,
} from './sessionList';
import { SessionInfo } from './protocol';

/** What the sidebar needs from the chat side. Narrow on purpose. */
export interface SessionSource {
  /** This workspace's stored agents, via the host. */
  listSessions(): Promise<SessionInfo[]>;
  /** The agent the chat tab is showing. */
  currentAgentId(): string | undefined;
  /** The agent with a turn in flight. */
  runningAgentId(): string | undefined;
  /** Whether a host child is already live, so a redraw need not start one. */
  isHostUp(): boolean;
  /** Continues a stored agent in the chat tab. */
  openSession(agentId: string): Promise<void>;
  /** Fires when the list or the markers on it could have changed. */
  onSessionsChanged(listener: () => void): vscode.Disposable;
}

/** How long to wait for a burst of state changes to settle before re-listing. */
const SETTLE_MS = 400;

export class SessionTreeProvider implements vscode.TreeDataProvider<SessionNode> {
  public static readonly viewType = 'anvil.sessions';

  private readonly changed = new vscode.EventEmitter<SessionNode | undefined>();
  readonly onDidChangeTreeData = this.changed.event;

  /** The last successful listing, so a refresh never blanks the tree. */
  private cached: SessionInfo[] | undefined;
  /** Why the last attempt failed, kept until the next one succeeds. */
  private failure: string | undefined;
  private loading = false;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private readonly source: SessionSource) {}

  getTreeItem(node: SessionNode): vscode.TreeItem {
    if (node.kind === 'note') return noteItem(node);
    if (node.kind === 'group') {
      const item = new vscode.TreeItem(node.label, vscode.TreeItemCollapsibleState.Expanded);
      // The id is what keeps a group's expansion and the selection where the
      // user left them across a refresh.
      item.id = node.id;
      item.contextValue = 'group';
      return item;
    }
    return sessionItem(node);
  }

  async getChildren(element?: SessionNode): Promise<SessionNode[]> {
    if (element) return element.kind === 'group' ? element.rows : [];

    // Checked before the call rather than after it fails: an untrusted
    // workspace cannot start the host at all, and asking would be a spawn
    // attempt whose only outcome is the same message.
    if (!vscode.workspace.isTrusted) return [sessionNote('untrusted')];

    if (this.failure !== undefined) return [sessionNote('error', this.failure)];
    if (this.cached === undefined) {
      // Nothing to show and nothing in flight: this is the one moment a host is
      // started by the sidebar alone, and it is the user who opened the view.
      void this.load();
      return [sessionNote('loading')];
    }
    return this.view();
  }

  /**
   * The explicit command. It always asks, even when that means starting a host:
   * a Refresh that quietly did nothing because the host was down would look
   * exactly like a Refresh that found no changes, and those are different.
   */
  async refresh(): Promise<void> {
    this.failure = undefined;
    await this.load();
  }

  /**
   * Redraws from what is cached, then re-lists once the burst is over: a run
   * finishing fires state, done and the changed-files report within a frame or
   * two, and three host round trips for one turn's worth of change is wasted.
   */
  refreshSoon(): void {
    this.changed.fire(undefined);
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.quietReload();
    }, SETTLE_MS);
  }

  /**
   * The unasked-for re-list. It does not start a host: a session that just
   * ended, or a folder that just changed, is not a reason to spawn a process to
   * redraw a sidebar. With no cached list there is nothing to redraw, so the
   * first load is allowed through.
   */
  private async quietReload(): Promise<void> {
    if (!this.source.isHostUp() && this.cached !== undefined) return;
    await this.load();
  }

  dispose(): void {
    if (this.timer) clearTimeout(this.timer);
    this.changed.dispose();
  }

  private async load(): Promise<void> {
    if (this.loading) return;
    this.loading = true;
    this.changed.fire(undefined);
    try {
      this.cached = await this.source.listSessions();
      this.failure = undefined;
    } catch (err) {
      this.failure = (err as Error).message;
    } finally {
      this.loading = false;
      this.changed.fire(undefined);
    }
  }

  private view(): SessionNode[] {
    const options: SessionViewOptions = {
      now: Date.now(),
      currentAgentId: this.source.currentAgentId(),
      runningAgentId: this.source.runningAgentId(),
    };
    return buildSessionView(this.cached ?? [], options);
  }
}

function sessionItem(row: SessionRow): vscode.TreeItem {
  const item = new vscode.TreeItem(row.label, vscode.TreeItemCollapsibleState.None);
  item.id = row.id;
  item.description = row.description;
  item.tooltip = row.tooltip;
  item.contextValue = row.contextValue;
  item.iconPath = new vscode.ThemeIcon(row.icon);
  item.command = {
    command: 'anvil.openSession',
    title: 'Open',
    arguments: [row.agentId],
  };
  return item;
}

function noteItem(note: SessionNote): vscode.TreeItem {
  const item = new vscode.TreeItem(note.label, vscode.TreeItemCollapsibleState.None);
  item.id = note.id;
  item.tooltip = note.tooltip;
  item.contextValue = 'note';
  item.iconPath = new vscode.ThemeIcon(note.icon);
  if (note.retry) {
    item.command = { command: 'anvil.refreshSessions', title: 'Refresh' };
  }
  return item;
}
