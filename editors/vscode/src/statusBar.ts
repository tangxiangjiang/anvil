/**
 * The window's status-bar items.
 *
 * Both are built from the hub's own view rather than from a second copy of the
 * facts: every surface that can change what a chat is doing fires
 * `onRunningChanged`, and every task start or end fires `onTasksChanged`, so
 * reading the hub at that moment is what keeps an item from disagreeing with
 * the thing it describes.
 *
 * Two items, not one per tab or one per task. A status bar is a window-wide
 * surface, and a second copy of a tab — or a row per background command — would
 * be a second place to look for the same fact. The task item appears only while
 * something is running: an idle "0" is a number nobody asked for.
 */

import * as vscode from 'vscode';
import { ChatPhase, chatStatusOf } from './chatStatus';
import type { Hub } from './hub';

/** The command the chat item runs. Registered by `extension.ts`. */
export const STATUS_BAR_COMMAND = 'anvil.focusChat';

/** The command the task item runs. Registered by `extension.ts`. */
export const STATUS_BAR_TASKS_COMMAND = 'anvil.openTasks';

export class ChatStatusBar implements vscode.Disposable {
  private readonly item: vscode.StatusBarItem;
  private readonly taskItem: vscode.StatusBarItem;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(private readonly hub: Hub) {
    // Right-hand side and low priority: it is a background status, and every
    // language service's item is more immediate than "the chat is idle".
    this.item = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 90);
    this.item.name = 'Anvil chat';
    this.item.command = STATUS_BAR_COMMAND;
    this.item.show();
    // Just left of the chat item: the count is about work running in the
    // background, which is a step more specific than the conversation's state.
    this.taskItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 91);
    this.taskItem.name = 'Anvil tasks';
    this.taskItem.command = STATUS_BAR_TASKS_COMMAND;

    this.disposables.push(
      hub.onRunningChanged(() => this.refresh()),
      // A tab opened or closed can change the phase without any run changing.
      hub.onTabsChanged(() => this.refresh()),
      // One item per task is not the shape here: the count is what the window
      // shows, and the panel is where the rows are.
      hub.onTasksChanged(() => this.refreshTasks()),
    );
    this.refresh();
    this.refreshTasks();
  }

  /** Redraws the chat item from what the hub says right now. */
  refresh(): void {
    const phase: ChatPhase = this.hub.phase;
    const { text, tooltip } = chatStatusOf(phase);
    this.item.text = text;
    this.item.tooltip = tooltip;
    this.item.accessibilityInformation = { label: tooltip };
  }

  /** Shows the running-task count, or hides the item when there is none. */
  refreshTasks(): void {
    const count = this.hub.runningTaskCount;
    if (count <= 0) {
      this.taskItem.hide();
      return;
    }
    this.taskItem.text = `$(tasklist) ${count}`;
    this.taskItem.tooltip =
      count === 1 ? '1 background task running' : `${count} background tasks running`;
    this.taskItem.accessibilityInformation = { label: this.taskItem.tooltip };
    this.taskItem.show();
  }

  dispose(): void {
    for (const disposable of this.disposables) disposable.dispose();
    this.item.dispose();
    this.taskItem.dispose();
  }
}
