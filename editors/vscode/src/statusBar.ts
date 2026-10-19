/**
 * The window's one chat status-bar item.
 *
 * It is built from the hub's own view of the tabs rather than from a second
 * copy of "is anything running": every surface that can change what a chat is
 * doing already fires `onRunningChanged`, and reading the panels at that moment
 * is what keeps the item from disagreeing with the tabs it describes.
 *
 * One item, not one per tab. A status bar is a window-wide surface, and a
 * second copy of a tab would be a second place to look for the same fact.
 */

import * as vscode from 'vscode';
import { ChatPhase, chatStatusOf } from './chatStatus';
import type { Hub } from './hub';

/** The command the item runs. Registered by `extension.ts`. */
export const STATUS_BAR_COMMAND = 'anvil.focusChat';

export class ChatStatusBar implements vscode.Disposable {
  private readonly item: vscode.StatusBarItem;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(private readonly hub: Hub) {
    // Right-hand side and low priority: it is a background status, and every
    // language service's item is more immediate than "the chat is idle".
    this.item = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 90);
    this.item.name = 'Anvil chat';
    this.item.command = STATUS_BAR_COMMAND;
    this.item.show();
    this.disposables.push(
      hub.onRunningChanged(() => this.refresh()),
      // A tab opened or closed can change the phase without any run changing.
      hub.onTabsChanged(() => this.refresh()),
    );
    this.refresh();
  }

  /** Redraws the item from what the hub says right now. */
  refresh(): void {
    const phase: ChatPhase = this.hub.phase;
    const { text, tooltip } = chatStatusOf(phase);
    this.item.text = text;
    this.item.tooltip = tooltip;
    this.item.accessibilityInformation = { label: tooltip };
  }

  dispose(): void {
    for (const disposable of this.disposables) disposable.dispose();
    this.item.dispose();
  }
}
