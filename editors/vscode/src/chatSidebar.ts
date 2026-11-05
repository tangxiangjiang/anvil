import * as vscode from 'vscode';
import { Hub } from './hub';

interface StoredChatViewState {
  agentId?: unknown;
  focus?: unknown;
}

/**
 * The Anvil surface contributed to VS Code's Secondary Side Bar.
 *
 * It creates an ordinary ChatPanel document on a WebviewView surface, so the
 * protocol, replay, approvals, composer, and source attachments stay identical
 * to editor-tab chats.
 */
export class ChatSidebarProvider implements vscode.WebviewViewProvider {
  static readonly viewType = 'anvil.chatView';

  constructor(private readonly hub: Hub) {}

  resolveWebviewView(
    view: vscode.WebviewView,
    context: vscode.WebviewViewResolveContext<StoredChatViewState>,
    _token: vscode.CancellationToken,
  ): void {
    const storedAgent =
      typeof context.state?.agentId === 'string' && context.state.agentId !== ''
        ? context.state.agentId
        : undefined;
    const agentId = storedAgent && !this.hub.findByAgent(storedAgent) ? storedAgent : undefined;
    const panel = this.hub.createPanel(agentId, context.state?.focus === true);
    this.hub.registerSidebarPanel(panel);
    panel.adoptView(view);
  }
}
