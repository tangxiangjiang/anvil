/**
 * The conversation: a webview in an editor tab, fed by one `HostManager`.
 *
 * The webview is a rendering surface only. Everything with a decision in it —
 * spawning, sessions, approvals, context composition, review tabs — happens on
 * this side of the message port, so the untrusted-ish HTML never holds state
 * the host needs and never names a path the host will act on without this side
 * having decided that it should.
 *
 * The tab is a document, which sets two rules this class exists to keep. Closing
 * it is not stopping the turn: the session and the host child outlive the view,
 * and messages that arrive with no view attached are dropped rather than queued.
 * And reopening it re-announces everything, because a fresh document knows
 * nothing — the `ready` path. What it cannot re-announce is the conversation
 * itself, which happened before the document existed and left no record in this
 * process; that is replayed from the store, and re-attaching to the session
 * first is how the window finds out which conversation that was.
 */

import { randomBytes } from 'node:crypto';
import { dirname } from 'node:path';
import * as vscode from 'vscode';
import { ChangesTracker, ChangedFile } from './changes';
import { PreImage, planReview, reviewOf } from './editDiff';
import { buildPromptContext, pickMentions } from './editorContext';
import { attachmentsFromDataUrls } from './attachments';
import { HostListener, HostManager } from './hostManager';
import { PreviewDocuments } from './review';
import { SessionState } from './session';
import { dedupeMentions } from './mentions';
import { SessionAgentKey, shouldReattach } from './reattach';
import { replayOf } from './transcript';
import {
  CommandAction,
  SlashCommand,
  commandCatalogue,
  parseSlash,
  route,
} from './commands';
import {
  ApprovalRequest,
  Decision,
  DoneParams,
  HostEvent,
  PermissionMode,
  PermissionModeSource,
  PermissionModes,
  PermissionsResult,
  RuleFileInfo,
  RuleInfo,
  RuleProblem,
  RuleSpec,
  SessionInfo,
  SessionResult,
  SessionTranscriptResult,
  SkillEntry,
} from './protocol';
import { RunFailure } from './errors';
import { readSettings } from './settings';
import { describeProblem, emptyRulesFile, problemPosition } from './rulesFile';

/** A message from the webview. */
type Inbound =
  | { type: 'ready' }
  | { type: 'send'; text: string; mentions?: string[]; images?: string[] }
  | { type: 'cancel' }
  | { type: 'answer'; requestId: string; decision: Decision; scope?: string }
  /**
   * "Always allow" this shape: the rule the host offered, written into the
   * workspace rule file for the call that is waiting on the card.
   */
  | { type: 'remember'; requestId: string; rule: RuleSpec }
  /** The composer's Permissions button: show the file these rules live in. */
  | { type: 'openPermissions' }
  | { type: 'pickMention' }
  | { type: 'commands' }
  | { type: 'mode'; mode: string }
  | { type: 'review'; requestId: string }
  | { type: 'reviewChange'; path: string }
  /**
   * "Show me what that call changed", from a finished tool card. The call's
   * facts travel with the click because this is the only place they still
   * exist: the card already holds the arguments, and keeping a copy of every
   * call's body in the extension for the length of a session is not worth a
   * second reference to what the IN pane is already showing.
   */
  | { type: 'reviewCall'; toolCallId: string; runId?: string; name?: string; args?: unknown }
  /** A link the transcript rendered. The URL is re-checked before it is opened. */
  | { type: 'openLink'; url: string };

/** A message to the webview. */
type Outbound =
  | { type: 'trust'; trusted: boolean }
  | {
      type: 'state';
      state: SessionState;
      runId?: string;
      sessionId?: string;
      agentId?: string;
      mode?: PermissionMode;
    }
  | { type: 'event'; event: HostEvent; reviewable?: boolean }
  | { type: 'approval'; request: ApprovalRequest }
  | { type: 'approvalWithdrawn'; requestId: string }
  /**
   * What became of a "remember this" choice. Exactly one of the three endings:
   * the host settled the card (`approved`), the rule was written but does not
   * decide this call (`decidedBy`), or nothing was written (`error`).
   */
  | {
      type: 'rememberResult';
      requestId: string;
      rule?: RuleInfo;
      approved?: boolean;
      decidedBy?: RuleInfo;
      error?: string;
    }
  | { type: 'done'; done: DoneParams; failure: RunFailure }
  | { type: 'mentions'; paths: string[] }
  | { type: 'vision'; enabled: boolean }
  | { type: 'changes'; files: Array<Pick<ChangedFile, 'path' | 'kind'>> }
  | { type: 'commands'; commands: SlashCommand[] }
  | { type: 'help'; commands: SlashCommand[] }
  | { type: 'mode'; mode: PermissionMode | ''; resolved: PermissionMode; source: PermissionModeSource }
  | { type: 'rules'; files: RuleFileInfo[] }
  | { type: 'session'; agentId: string; started: boolean }
  /**
   * Empties the transcript. The tab is a singleton pointed at one conversation
   * at a time, so adopting another agent has to take the old one off the
   * screen — otherwise two conversations are stacked in one document, which
   * reads as corruption rather than as a switch.
   */
  | { type: 'reset' }
  /**
   * A turn in a replayed conversation. The composer is what normally puts a user
   * message on screen, and it is not involved in a replay, so the replay says it
   * itself. It is drawn by the same renderer either way.
   */
  | { type: 'user'; text: string }
  /**
   * A line in the transcript for something the user did not type and the model
   * did not say — the loop's own note to the model, replayed. Not a `notice`:
   * that is a banner about the state of things, and this is part of the
   * conversation, in its place.
   */
  | { type: 'note'; text: string }
  /**
   * Where a replayed run ended, drawn in the transcript at that point. The live
   * `done` is a different thing: it is the end of the run in flight, and it
   * lands on the status line rather than in the conversation.
   */
  | { type: 'runEnd'; done: DoneParams }
  /** The tab became the active editor: the composer takes the keyboard. */
  | { type: 'focus' }
  | { type: 'notice'; level: 'info' | 'warn' | 'error'; message: string };

/** Where the chosen permission mode is remembered between windows. */
const MODE_KEY = 'anvil.permissionMode';

/**
 * How the modes are named to the user. The wire values are the SDK's.
 *
 * `dont_ask` is the one to be careful with: it is the mirror of `bypass`, and
 * what it refuses is the asking, not the policy — anything the workspace's rules
 * allow still runs. Its label says what it is rather than what it sounds like,
 * because "don't ask" on its own reads as "run everything", which is `bypass`.
 */
const MODE_LABELS: Record<PermissionMode, string> = {
  ask: 'Ask',
  accept_edits: 'Accept edits',
  plan: 'Plan',
  dont_ask: 'No asking',
  bypass: 'Bypass',
};

function isPermissionMode(value: string): value is PermissionMode {
  return (PermissionModes as readonly string[]).includes(value);
}

/** What the composer says after the user has chosen a mode, or cleared it. */
function modeNotice(mode: PermissionMode | undefined): string {
  switch (mode) {
    case undefined:
      return 'Mode: whatever this workspace declares in .anvil/permissions.json.';
    case 'bypass':
      return 'Bypass is on: tool calls run without asking. Nothing will be shown for approval.';
    case 'dont_ask':
      return "No asking: anything this workspace's rules do not allow is refused, and nothing is shown for approval.";
    default:
      return `Mode: ${MODE_LABELS[mode]}.`;
  }
}

/**
 * The mode this workspace last chose, or undefined when it has no stored choice.
 *
 * Undefined is a real state and not `ask`: a client with no opinion sends no
 * `permission_mode` at all, which is what lets the workspace's
 * `.anvil/permissions.json` decide. Sending `ask` would silently outrank the
 * file, and every workspace would look like one that had chosen it.
 */
function readMode(context: vscode.ExtensionContext): PermissionMode | undefined {
  const stored = context.workspaceState.get<string>(MODE_KEY);
  return stored !== undefined && isPermissionMode(stored) ? stored : undefined;
}

export class ChatPanel implements HostListener {
  public static readonly viewType = 'anvil.chat';

  private panel: vscode.WebviewPanel | undefined;
  /** Whether the document in `panel` has asked for its state yet. */
  private listening = false;
  /** Messages held until it does. See `post`. */
  private pending: Outbound[] = [];
  private manager: HostManager | undefined;
  /** Fires when the host comes up or goes away, for the title-bar stop button. */
  private readonly runningChanged = new vscode.EventEmitter<void>();
  /** Fires when the session list, or the markers on it, could have changed. */
  private readonly sessionsChanged = new vscode.EventEmitter<void>();
  /** Pre-images for the current turn, so the changed-files list can show diffs. */
  private readonly changes = new ChangesTracker();
  /**
   * The changed files of the last finished turn, by path — and which turn that
   * was. The run id is not decoration: a pre-image belongs to one turn, so a
   * card from an older run must not be diffed against a later turn's pre-image,
   * which would attribute one turn's edit to another call.
   */
  private lastChanges: { run: string; files: Map<string, ChangedFile> } = {
    run: '',
    files: new Map(),
  };
  /** Review tabs opened for an approval, closed when it is answered. */
  private readonly reviews = new Map<string, vscode.Uri[]>();
  /**
   * The slash catalogue: built-ins plus the workspace's skills. Fetched once per
   * host, because it costs a round trip and only changes when the process does.
   */
  private catalogue: SlashCommand[] | undefined;
  /** The rule files the last session reported, re-announced to a reloaded view. */
  private rules: RuleFileInfo[] = [];
  /**
   * The permission mode this workspace last chose, remembered per workspace, or
   * undefined when it has chosen none. Undefined is not `ask`: it is the state
   * in which nothing is sent and the workspace's `.anvil/permissions.json`
   * decides, which is what makes a team's choice something it can commit.
   */
  private mode: PermissionMode | undefined;
  /**
   * The last resolved mode a notice was posted for, so a session re-opened for
   * an unrelated reason (a model change) does not repeat what the user already
   * read. Keyed by the whole picture, because "the file decided" and "the file
   * was refused" are different things to say.
   */
  private announcedMode = '';
  /**
   * A rule file that will not compile, reported against the file itself rather
   * than only as a failed session. Created on first use: a window that never
   * looks at a rules file never makes a diagnostics collection.
   */
  private diagnostics: vscode.DiagnosticCollection | undefined;

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly log: (line: string) => void,
    private readonly previews: PreviewDocuments,
  ) {
    this.mode = readMode(context);
  }

  /**
   * Opens the conversation tab, or reveals the one that is already open.
   *
   * `ViewColumn.Active` puts it where the user is looking, which is the least
   * surprising answer for a command they ran from wherever they were. A reveal
   * names the panel's own column rather than the active one so that a tab the
   * user dragged somewhere stays there.
   */
  open(): void {
    if (this.panel) {
      this.panel.reveal(this.panel.viewColumn, false);
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      ChatPanel.viewType,
      'Anvil',
      vscode.ViewColumn.Active,
      {
        enableScripts: true,
        // Clicking another editor would otherwise destroy the document and the
        // transcript with it. It is an optimisation, not a guarantee: a document
        // that did go away is rebuilt by the replay, and VS Code creates the
        // panel a serializer is handed, which may not retain at all — which is
        // why `ready` is the source of truth either way.
        retainContextWhenHidden: true,
        localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
      },
    );
    panel.iconPath = vscode.Uri.joinPath(this.context.extensionUri, 'media', 'anvil.svg');
    this.adopt(panel);
  }

  /**
   * Attaches to a panel that already exists: one this side just created, or one
   * VS Code restored from a previous window. Both need the same three things —
   * a document, a message port, and a way to notice the tab going away.
   */
  adopt(panel: vscode.WebviewPanel): void {
    this.panel = panel;
    // Each document announces itself once; until it does, nothing is listening.
    this.listening = false;
    this.pending = [];
    panel.webview.options = {
      enableScripts: true,
      localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
    };
    panel.webview.html = renderHtml(panel.webview, this.context.extensionUri);
    panel.webview.onDidReceiveMessage((msg: Inbound) => void this.onMessage(msg));
    panel.onDidChangeViewState((event) => {
      if (event.webviewPanel.active) this.post({ type: 'focus' });
    });
    panel.onDidDispose(() => {
      // Guarded: `deactivate` disposes the panel too, and a stale disposal must
      // not clear a panel that has since replaced it.
      if (this.panel !== panel) return;
      this.panel = undefined;
      this.listening = false;
      this.pending = [];
    });
    this.updateTitle();
  }

  /**
   * Creates the manager lazily: no host work happens at activation, and the two
   * things that can ask for a host are both user actions — a prompt in the tab,
   * or the session view being opened.
   */
  private ensureManager(): HostManager {
    if (!this.manager) {
      this.manager = new HostManager(this.context, this.log, this);
      // The mode is remembered per workspace, so it has to be in force before the
      // session that will carry it is created. This only records the choice; a
      // session is not opened to hold it.
      //
      // Nothing is recorded when the workspace has no stored choice, and that is
      // the point of the guard: sending `permission_mode: undefined` would travel
      // as no key at all, but recording an override for it would still make this
      // client look like it had an opinion — and it is the client having none that
      // lets the workspace's file decide.
      if (this.mode !== undefined) {
        void this.manager.applyOptions({ permission_mode: this.mode });
      }
    }
    return this.manager;
  }

  private async onMessage(msg: Inbound): Promise<void> {
    if (msg.type === 'ready') {
      // Whatever was said before this document started listening, in the order
      // it was said — which for a tab opened by a session click is the reset,
      // the session marker and the state, in that order.
      const queued = this.pending;
      this.pending = [];
      this.listening = true;
      for (const message of queued) this.post(message);

      // A fresh document has lost whatever transcript it might have had, so
      // re-announce everything it cannot infer: trust, the run in flight, and
      // any open prompt.
      this.post({ type: 'trust', trusted: vscode.workspace.isTrusted });
      this.post({ type: 'vision', enabled: readSettings().session.vision === true });
      this.postMode();
      if (this.rules.length > 0) this.post({ type: 'rules', files: this.rules });
      if (this.manager) this.post({ type: 'state', ...this.manager.state() });
      for (const request of this.pendingApprovals()) {
        this.post({ type: 'approval', request });
      }

      // The document has no transcript, and after a window reload nothing else
      // does either. Coming back to the session is the last thing done here,
      // because it is the only part that can take a moment — everything above
      // is already on the wire by the time the host is asked anything.
      void this.reattach();
      return;
    }

    try {
      HostManager.assertTrusted();
    } catch (err) {
      this.post({ type: 'notice', level: 'error', message: (err as Error).message });
      return;
    }

    switch (msg.type) {
      case 'send':
        await this.sendTurn(msg.text, msg.mentions ?? [], msg.images ?? []);
        return;
      case 'cancel':
        await this.ensureManager().cancel();
        return;
      case 'answer':
        await this.answer(msg.requestId, msg.decision, msg.scope);
        return;
      case 'remember':
        await this.remember(msg.requestId, msg.rule);
        return;
      case 'openPermissions':
        await this.openPermissionRules();
        return;
      case 'pickMention':
        // The picker is the editor's, not the webview's: it searches the real
        // workspace and returns paths, and a path is all the protocol carries.
        this.post({ type: 'mentions', paths: dedupeMentions(await pickMentions()) });
        return;
      case 'commands':
        await this.sendCatalogue();
        return;
      case 'mode':
        await this.setMode(msg.mode);
        return;
      case 'review':
        await this.openReview(msg.requestId);
        return;
      case 'reviewChange':
        await this.openChangeReview(msg.path);
        return;
      case 'reviewCall':
        await this.openCallReview(msg);
        return;
      case 'openLink':
        await this.openLink(msg.url);
        return;
      default:
        return;
    }
  }

  /**
   * Opens a link out of the transcript, after checking its scheme again.
   *
   * The webview already refuses anything that is not http or https, and that
   * check is presentation rather than the boundary: this message comes from a
   * document, and that document is written by a renderer over *model* output.
   * `vscode.env.openExternal` hands a URL to the operating system, so the
   * decision about what may reach it belongs on this side. The duplication of
   * the pattern is the point.
   *
   * A refusal is silent. The webview only posts a URL it already agreed to open,
   * so a rejected message means the document is not the one this extension
   * wrote, and there is nothing useful to tell the user about that.
   */
  private async openLink(url: unknown): Promise<void> {
    if (typeof url !== 'string') return;
    const trimmed = url.trim();
    if (!/^https?:\/\/\S+$/i.test(trimmed)) return;
    await vscode.env.openExternal(vscode.Uri.parse(trimmed));
  }

  // --- commands ---------------------------------------------------------------

  /**
   * The catalogue, fetched once. Built-ins need no host, so they are always
   * available; the skills are the reason it is a round trip at all.
   */
  private async commandCatalogue(): Promise<SlashCommand[]> {
    if (this.catalogue) return this.catalogue;
    let skills: SkillEntry[] = [];
    try {
      skills = await this.ensureManager().skills();
    } catch (err) {
      // A missing or broken host must not cost the user the built-ins.
      this.log(`could not list skills: ${(err as Error).message}`);
      this.post({
        type: 'notice',
        level: 'warn',
        message: `Skill commands are unavailable: ${(err as Error).message}`,
      });
    }
    this.catalogue = commandCatalogue(skills);
    return this.catalogue;
  }

  private async sendCatalogue(): Promise<void> {
    this.post({ type: 'commands', commands: await this.commandCatalogue() });
  }

  /**
   * Applies a permission mode to the conversation. The mode is a session
   * option, so the session is re-opened under it — same agent, so the history
   * and the transcript both stay.
   *
   * The empty string is not a mode and not a mistake: it is the composer's
   * "Workspace default" entry, which clears the stored choice so that the
   * workspace's `.anvil/permissions.json` decides again. That is the only way
   * back to letting the file speak, and without it a client that had once sent
   * `ask` could never stop outranking the file it was reading.
   */
  private async setMode(mode: string): Promise<void> {
    if (mode !== '' && !isPermissionMode(mode)) {
      this.post({ type: 'notice', level: 'warn', message: `Unknown mode: ${mode}` });
      this.postMode();
      return;
    }
    const chosen: PermissionMode | undefined = mode === '' ? undefined : mode;
    if (chosen === this.mode) return;
    const manager = this.ensureManager();
    // Checked here rather than left to the re-open, so the refusal is reported
    // as a refusal. `reopen` would throw the same thing, but by then the chosen
    // mode is already recorded as an override and would silently take effect on
    // the *next* session — a mode the user was told had been refused.
    if (manager.state().state === 'running') {
      this.post({ type: 'notice', level: 'warn', message: 'Stop the turn before changing the mode.' });
      this.postMode();
      return;
    }
    // false means either "nothing was open to re-open" (the ordinary case
    // before the first prompt: the choice is simply recorded) or "it refused".
    // A live session that did not take the change is the second one.
    const applied = await manager.applyOptions({ permission_mode: chosen });
    if (!applied && manager.running) {
      this.postMode();
      return;
    }
    this.mode = chosen;
    // Clearing the key rather than storing the empty string: `readMode` treats
    // both the same, and an absent key is what a workspace that never chose
    // anything has.
    await this.context.workspaceState.update(MODE_KEY, chosen);
    this.postMode();
    this.postState();
    this.post({ type: 'notice', level: chosen === 'bypass' ? 'warn' : 'info', message: modeNotice(chosen) });
  }

  // --- session history --------------------------------------------------------

  /** Opens a past agent from this workspace as the current conversation. */
  private async pickResume(): Promise<void> {
    const manager = this.ensureManager();
    let sessions: Awaited<ReturnType<HostManager['listSessions']>> = [];
    try {
      sessions = await manager.listSessions();
    } catch (err) {
      this.post({ type: 'notice', level: 'error', message: `Could not list sessions: ${(err as Error).message}` });
      return;
    }
    if (sessions.length === 0) {
      this.post({ type: 'notice', level: 'info', message: 'No earlier sessions in this workspace yet.' });
      return;
    }
    const items = sessions.map((session) => ({
      label: session.agent_id,
      description: `${session.runs} run${session.runs === 1 ? '' : 's'} · ${session.last_status || 'unknown'}`,
      detail: [session.model, session.updated_at ? `updated ${session.updated_at}` : ''].filter(Boolean).join(' · '),
      session,
    }));
    const picked = await vscode.window.showQuickPick(items, {
      title: 'Resume a session',
      placeHolder: 'A past agent in this workspace',
      matchOnDescription: true,
      matchOnDetail: true,
    });
    if (!picked) return;
    await this.resumeAgent(picked.session.agent_id);
  }

  /** Ends the conversation. The host stays up; the next prompt starts fresh. */
  private async clearConversation(): Promise<void> {
    await this.ensureManager().clearConversation();
    await this.forgetAgent();
    this.changes.begin();
    this.forgetChanges();
    this.post({ type: 'reset' });
    this.post({ type: 'session', agentId: '', started: false });
    this.postState();
    this.updateTitle();
    this.sessionsChanged.fire();
  }

  // --- the session sidebar's view of this -------------------------------------

  /** The agents this workspace has stored. Starts a host if none is live. */
  async listSessions(): Promise<SessionInfo[]> {
    return this.ensureManager().listSessions();
  }

  /** The agent the tab is showing, whether or not a turn is in flight. */
  currentAgentId(): string | undefined {
    return this.manager?.state().agentId;
  }

  /** The agent with a turn in flight. */
  runningAgentId(): string | undefined {
    const state = this.manager?.state();
    return state?.state === 'running' ? state.agentId : undefined;
  }

  /** Whether a host child is live, so a redraw need not start one. */
  isHostUp(): boolean {
    return this.manager?.running ?? false;
  }

  /**
   * Continues a stored agent, from the sidebar. The tab is opened first: the
   * resume reports its outcome as a message to the panel, and a message with no
   * document behind it is dropped — the failure would be silent.
   */
  async openSession(agentId: string): Promise<void> {
    this.open();
    await this.resumeAgent(agentId);
  }

  /** Starts a fresh conversation, from the sidebar. */
  async newSession(): Promise<void> {
    this.open();
    await this.clearConversation();
  }

  /** Subscribes to changes in the session list, or in the markers on it. */
  onSessionsChanged(listener: () => void): vscode.Disposable {
    return this.sessionsChanged.event(listener);
  }

  /** Composes and sends one turn, with whatever context the editor has. */
  private async sendTurn(text: string, mentions: string[], images: string[]): Promise<void> {
    // The catalogue is only consulted for a line that could be a command, so an
    // ordinary prompt never pays for the round trip (or for the host process
    // the round trip would start).
    const action = parseSlash(text)
      ? route(text, await this.commandCatalogue())
      : { kind: 'prompt' as const, text };
    await this.runAction(action, text, mentions, images);
  }

  /**
   * Carries out what the composer line meant. Everything here maps onto
   * something the session can already do — a mode, a model, a skill, a stored
   * agent — so no command is a prompt trick.
   */
  private async runAction(
    action: CommandAction,
    text: string,
    mentions: string[],
    images: string[],
  ): Promise<void> {
    switch (action.kind) {
      case 'prompt':
        await this.sendQuestion(action.text, mentions, images);
        return;
      case 'mode':
        await this.setMode(action.mode);
        if (action.args !== '') {
          await this.sendQuestion(action.args, mentions, images);
          return;
        }
        this.postState();
        return;
      case 'model':
        await this.pickModel(action.model);
        // Re-announced even when nothing ran: the composer locked itself the
        // moment the command was submitted, and only a state message unlocks
        // it. A cancelled picker has no other ending.
        this.postState();
        return;
      case 'resume':
        if (action.agentId !== '') {
          await this.resumeAgent(action.agentId);
          return;
        }
        await this.pickResume();
        this.postState();
        return;
      case 'clear':
        await this.clearConversation();
        return;
      case 'help':
        this.post({ type: 'help', commands: await this.commandCatalogue() });
        this.postState();
        return;
      case 'skill': {
        // The manager says so itself when the skill is already on; a second
        // notice here would be the same sentence twice.
        const added = await this.ensureManager().useSkill(action.skillId);
        if (added) {
          this.post({
            type: 'notice',
            level: 'info',
            message: `The ${action.skillId} skill is on for this conversation.`,
          });
        }
        if (action.args !== '') {
          await this.sendQuestion(action.args, mentions, images);
          return;
        }
        this.postState();
        return;
      }
      default:
        // Unreachable while every action has a case. Sending what was typed is
        // the safe ending: a new command cannot swallow the user's words.
        await this.sendQuestion(text, mentions, images);
        return;
    }
  }

  /** Adopts a named agent, for `/resume <id>` typed rather than picked. */
  private async resumeAgent(agentId: string): Promise<void> {
    try {
      await this.ensureManager().resumeSession(agentId);
    } catch (err) {
      this.post({
        type: 'notice',
        level: 'error',
        message: `Could not resume ${agentId}: ${(err as Error).message}`,
      });
      this.postState();
      return;
    }
    // Cleared before the marker, never after: the transcript that was on screen
    // belonged to the agent that was current a moment ago, and the marker is
    // the first line of the conversation that replaced it.
    this.post({ type: 'reset' });
    this.post({ type: 'session', agentId, started: true });
    this.postState();
    this.updateTitle();
    this.sessionsChanged.fire();
    await this.reportRules();
    // Last, so the two never stack: the reset has taken the old conversation off
    // the screen, and the marker is the first line of the new one.
    await this.replay(agentId);
  }

  /**
   * Fills the tab with a conversation it did not watch.
   *
   * Every step goes through the same paths a live turn does — a tool call through
   * `postEvent`, so a replayed card's review is decided by the same rule as a
   * live one's — except that nothing here touches the change tracker. A replayed
   * call's pre-image is not on disk any more; snapshotting now would file the
   * *current* contents under a historical call's id and, worse, list files the
   * present turn never touched under the present turn's changed files.
   *
   * Best-effort on purpose. A host that had never heard of `session.transcript`
   * answers `-32601`, and an old host beside a new extension is a pair that has
   * to keep working: the tab stays usable, with no replay and no error.
   */
  private async replay(agentId: string): Promise<void> {
    if (!agentId || !this.panel) return;
    let result: SessionTranscriptResult;
    try {
      result = await this.ensureManager().transcript(agentId);
    } catch (err) {
      this.log(`no replay for ${agentId}: ${(err as Error).message}`);
      return;
    }

    const sessionId = this.manager?.state().sessionId ?? '';
    const steps = replayOf(result.entries ?? [], sessionId);
    if (steps.length === 0) return;
    for (const step of steps) {
      if (step.kind === 'user') this.post({ type: 'user', text: step.text });
      else if (step.kind === 'notice') this.post({ type: 'note', text: step.text });
      else if (step.kind === 'runEnd') this.post({ type: 'runEnd', done: step.done });
      else this.postEvent(step.event);
    }
    if (this.dropped(result) > 0) {
      this.post({
        type: 'note',
        text: `Earlier turns are not shown: the store keeps the newest ${steps.length} records of this conversation.`,
      });
    }
  }

  /**
   * How much of the conversation the host left out. The reader reports a count
   * rather than a flag so the client can say how much, and a host that predates
   * the field reports nothing, which is read as nothing dropped.
   */
  private dropped(result: SessionTranscriptResult): number {
    const dropped = result.dropped;
    return typeof dropped === 'number' && dropped > 0 ? dropped : 0;
  }

  /**
   * Comes back to the session this workspace was talking to, if it still has one.
   *
   * Three endings, and the middle one is why the agent list is consulted rather
   * than the memory being trusted: an id the workspace no longer stores has to be
   * forgotten, or every reload would try to resume an agent that is gone and say
   * so. A listing that could not be read is not evidence of that — it is no
   * evidence at all, so the memory stays and nothing happens.
   */
  private async reattach(): Promise<void> {
    const stored = this.context.workspaceState.get<string>(SessionAgentKey);
    if (!stored) return;

    // A session that is already live needs no resuming — the manager survived
    // because this document, not the window, is what went away. The transcript
    // is still what the document lost, and that is what it gets.
    if (this.manager?.state().agentId === stored) {
      await this.replay(stored);
      return;
    }

    let listed: SessionInfo[] | undefined;
    try {
      listed = await this.ensureManager().listSessions();
    } catch (err) {
      this.log(`could not check whether ${stored} is still stored: ${(err as Error).message}`);
    }

    switch (shouldReattach(stored, listed)) {
      case 'resume':
        await this.resumeAgent(stored);
        return;
      case 'clear':
        await this.forgetAgent();
        return;
      default:
        return;
    }
  }

  /**
   * Remembers which agent the tab is showing, so a reloaded window can find it.
   *
   * Written here rather than at each place a session opens: this side hears
   * about every one of them — new, resumed, re-opened for a model change — and
   * the id is the host's answer, not something composed from what was asked.
   */
  private rememberAgent(agentId: string): void {
    if (!agentId) return;
    void this.context.workspaceState.update(SessionAgentKey, agentId);
  }

  /** Forgets it. Called when the conversation is ended, and when it is gone. */
  private async forgetAgent(): Promise<void> {
    await this.context.workspaceState.update(SessionAgentKey, undefined);
  }

  /** Names the tab after the conversation in it, so a tab strip is readable. */
  private updateTitle(): void {
    const agentId = this.currentAgentId();
    const title = agentId ? `Anvil — ${agentId.slice(0, 8)}` : 'Anvil';
    if (this.panel && this.panel.title !== title) this.panel.title = title;
  }

  /** Applies a model to the conversation, from an argument or a picker. */
  private async pickModel(given: string): Promise<void> {
    const manager = this.ensureManager();
    let model = given;
    if (model === '') {
      let entries: Awaited<ReturnType<HostManager['listModels']>> = [];
      try {
        entries = await manager.listModels();
      } catch (err) {
        this.post({
          type: 'notice',
          level: 'error',
          message: `Could not list models: ${(err as Error).message} (check anvil.baseUrl)`,
        });
        return;
      }
      if (entries.length === 0) {
        this.post({ type: 'notice', level: 'info', message: 'The provider reported no models.' });
        return;
      }
      const picked = await vscode.window.showQuickPick(
        entries.map((entry) => ({
          label: entry.id,
          description: [entry.provider, entry.vision ? 'vision' : ''].filter(Boolean).join(' · '),
          detail: entry.owned_by,
          id: entry.id,
        })),
        { title: 'Model', placeHolder: 'The model this conversation uses' },
      );
      if (!picked) return;
      model = picked.id;
    }
    await manager.applyOptions({ model });
    this.post({ type: 'notice', level: 'info', message: `Model: ${model}. The conversation continues.` });
    this.postState();
  }

  /** One ordinary turn, with the context the editor has. */
  private async sendQuestion(text: string, mentions: string[], images: string[]): Promise<void> {
    const context = buildPromptContext(dedupeMentions(mentions));
    const { attachments, errors } = attachmentsFromDataUrls(images);
    for (const error of errors) {
      this.post({ type: 'notice', level: 'warn', message: `Dropped an attachment: ${error}.` });
    }
    // The pre-images this turn will need are gone the moment it starts writing,
    // so the tracker is reset here rather than when the first write arrives.
    this.changes.begin();
    this.forgetChanges();
    await this.ensureManager()
      .send(text, { context, attachments })
      .catch(() => undefined);
    // The session exists now, so what it loaded can be reported. This is the
    // first moment the rules are a fact about a session rather than a guess.
    await this.reportRules();
  }

  /**
   * Reports which rule files apply, once per change. It is the answer to "why
   * is the model ignoring my AGENTS.md", and it is invisible from here without
   * asking the host.
   */
  private async reportRules(): Promise<void> {
    const manager = this.manager;
    if (!manager) return;
    try {
      const files = await manager.rules();
      const key = JSON.stringify(files);
      if (key === JSON.stringify(this.rules)) return;
      this.rules = files;
      this.post({ type: 'rules', files });
    } catch (err) {
      this.log(`could not list rule files: ${(err as Error).message}`);
    }
  }

  /** Answers an approval, and closes the review it was answered from. */
  private async answer(requestId: string, decision: Decision, scope?: string): Promise<void> {
    await this.ensureManager().respond(requestId, decision, { scope });
    await this.closeReview(requestId);
  }

  /**
   * Writes an "always allow" into the workspace rule file, and reports what
   * that did to the card it was chosen on.
   *
   * The host owns both halves: it appends the rule, and when the file then
   * decides the approval, it answers that approval itself. What is decided here
   * is only what the card is told — the call runs, or the card is still open and
   * something else decides it — because those two endings look identical from
   * the button and could not be more different to the user.
   */
  private async remember(requestId: string, rule: RuleSpec): Promise<void> {
    const manager = this.ensureManager();
    try {
      const result = await manager.addPermissionRule(rule, requestId);
      await this.closeReview(requestId);
      this.log(
        `wrote ${result.path} rule ${result.index}` +
          (result.decided_by ? ` (decided by ${result.decided_by.describe || 'a rule'})` : ''),
      );
      this.post({
        type: 'rememberResult',
        requestId,
        rule: result.rules.find((candidate) => candidate.index === result.index),
        approved: result.approved === true,
        decidedBy: result.decided_by,
      });
    } catch (err) {
      // Nothing was written — the file is malformed, .anvil is not writable, or
      // the session does not read the file at all. The card stays open, and the
      // message is the host's, which names the file and the line.
      this.post({ type: 'rememberResult', requestId, error: (err as Error).message });
    }
  }

  /**
   * Shows the workspace rule file where it lives, creating it when this
   * workspace has written no rules yet.
   *
   * A file that will not compile is opened at the place the parser objected to,
   * reported against the file in the Problems panel, and said in the panel: it
   * is the one failure of this feature the user cannot see coming, so "the
   * session would not start" would leave them looking for which bracket. The
   * session itself reports the same problem as a failure to open, and this is
   * the way to it.
   */
  async openPermissionRules(): Promise<void> {
    let state: PermissionsResult;
    try {
      state = await this.ensureManager().permissionRules();
    } catch (err) {
      this.post({
        type: 'notice',
        level: 'error',
        message: `Could not read the permission rules: ${(err as Error).message}`,
      });
      return;
    }
    const uri = vscode.Uri.file(state.path);
    if (!state.exists) {
      const create = 'Create it';
      const answer = await vscode.window.showInformationMessage(
        'This workspace has no permission rules. Nothing is allowed or refused by rule yet.',
        { modal: false, detail: state.path },
        create,
      );
      if (answer !== create) return;
      try {
        await vscode.workspace.fs.createDirectory(vscode.Uri.file(dirname(state.path)));
        await vscode.workspace.fs.writeFile(uri, Buffer.from(emptyRulesFile(), 'utf8'));
      } catch (err) {
        this.post({
          type: 'notice',
          level: 'error',
          message: `Could not create ${state.path}: ${(err as Error).message}`,
        });
        return;
      }
    }
    const document = await vscode.workspace.openTextDocument(uri);
    const editor = await vscode.window.showTextDocument(document);
    this.reportProblem(state.path, state.problem);
    // A file the session does not read is a file whose rules do nothing. The
    // setting is what turns it off, so saying which setting is the whole help
    // a user needs — the alternative is editing a rule and watching it be
    // ignored, which reads as the feature being broken.
    if (readSettings().session.load_permissions === false) {
      this.post({
        type: 'notice',
        level: 'warn',
        message: `anvil.permissions is off, so ${state.path} is not read: its rules decide nothing.`,
      });
    }
    if (!state.problem) return;
    const position = problemPosition(state.problem);
    const at = new vscode.Position(position.line, position.column);
    editor.selection = new vscode.Selection(at, at);
    editor.revealRange(new vscode.Range(at, at), vscode.TextEditorRevealType.InCenter);
    this.post({ type: 'notice', level: 'warn', message: describeProblem(state.path, state.problem) });
  }

  /** Reports a rule file that will not compile against the file itself. */
  private reportProblem(path: string, problem: RuleProblem | undefined): void {
    if (!problem) {
      this.diagnostics?.delete(vscode.Uri.file(path));
      return;
    }
    if (!this.diagnostics) this.diagnostics = vscode.languages.createDiagnosticCollection('anvil');
    const position = problemPosition(problem);
    const at = new vscode.Position(position.line, position.column);
    // A zero-width range at the exact place the parser stopped: the file is
    // being edited by hand, so pointing at a character beats underlining a line.
    const diagnostic = new vscode.Diagnostic(
      new vscode.Range(at, at),
      problem.message,
      vscode.DiagnosticSeverity.Error,
    );
    diagnostic.source = 'anvil';
    this.diagnostics.set(vscode.Uri.file(path), [diagnostic]);
  }

  private pendingApprovals(): ApprovalRequest[] {
    return this.manager?.pendingApprovals() ?? [];
  }

  // --- reviews ---------------------------------------------------------------

  /**
   * Opens the preview in the editor's own diff view. Only write_file and
   * edit_file carry both bodies; apply_patch spans files and so is left to the
   * diff text the card already shows.
   */
  private async openReview(requestId: string): Promise<void> {
    const request = this.pendingApprovals().find((candidate) => candidate.request_id === requestId);
    const preview = request?.preview;
    if (!request || !preview || typeof preview.diff !== 'string') {
      this.post({ type: 'notice', level: 'warn', message: 'There is no preview to review for this call.' });
      return;
    }
    if (preview.truncated || typeof preview.before !== 'string' || typeof preview.after !== 'string') {
      // The host dropped the bodies rather than send a clipped post-image, so
      // the honest thing is to say the diff text is all there is.
      this.post({
        type: 'notice',
        level: 'warn',
        message: 'This change is too large to open as a diff. Review the diff shown in the card.',
      });
      return;
    }
    const label = preview.path ?? request.tool;
    const uris = await this.previews.openPreview(
      preview.before,
      preview.after,
      label,
      `${label} (proposed)`,
    );
    const existing = this.reviews.get(requestId) ?? [];
    this.reviews.set(requestId, [...existing, ...uris]);
  }

  private async openChangeReview(path: string): Promise<void> {
    const change = this.lastChanges.files.get(path);
    if (!change) return;
    if (change.kind === 'deleted') {
      await this.previews.openDeleted(change.before, path);
      return;
    }
    const uri = this.fileUri(path);
    if (!uri) return;
    await this.previews.openAgainstDisk(change.before, uri, path);
  }

  /**
   * Opens what a finished tool call changed, from the card's own button.
   *
   * This is a record, not a request: it answers nothing, it is not tracked as a
   * pending decision, and nothing closes it when a run ends. The approval
   * card's review is the opposite — it belongs to a question that is still
   * open, and it is closed when the question is answered.
   */
  private async openCallReview(call: {
    toolCallId: string;
    runId?: string;
    name?: string;
    args?: unknown;
  }): Promise<void> {
    const review = reviewOf(call.name, call.args);
    if (!review) {
      // A card that offered no button, or a host that sent arguments this
      // client cannot read. Either way there is nothing to open, and inventing
      // a diff would be worse than saying so.
      this.log(`no change to review for tool call ${call.toolCallId}`);
      this.post({
        type: 'notice',
        level: 'warn',
        message: 'This call did not change a file, so there is nothing to review.',
      });
      return;
    }

    const preImage = review.kind === 'patch' ? undefined : this.preImageFor(call.runId, review.path);
    const plan = planReview(review, preImage);

    if (plan.kind === 'text') {
      await this.previews.openText(plan.body, plan.label);
      return;
    }
    if (plan.kind === 'memory') {
      await this.previews.openPreview(plan.before, plan.after, plan.label, plan.title);
      return;
    }
    if (plan.deleted) {
      await this.previews.openDeleted(plan.before, plan.path);
      return;
    }
    const uri = this.fileUri(plan.path);
    if (!uri) {
      this.post({
        type: 'notice',
        level: 'warn',
        message: `There is no workspace folder to open ${plan.path} from.`,
      });
      return;
    }
    await this.previews.openAgainstDisk(plan.before, uri, plan.path);
  }

  /**
   * The pre-image of a path, but only when the call belongs to the turn that
   * pre-image came from. Any other answer would be a diff of a different
   * change: the same file written again in a later turn has a later pre-image,
   * and showing it under an older card's button reads as that call's edit.
   */
  private preImageFor(runId: string | undefined, path: string): PreImage | undefined {
    if (!runId || runId !== this.lastChanges.run) return undefined;
    const change = this.lastChanges.files.get(path);
    if (!change) return undefined;
    return { before: change.before, deleted: change.kind === 'deleted' };
  }

  /**
   * A workspace file as a URI, refusing anything that would climb out of the
   * folder. The path came from the model's tool arguments, so the SDK's sandbox
   * governs the write and this governs which file a diff describes.
   */
  private fileUri(path: string): vscode.Uri | undefined {
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) return undefined;
    const segments = path.split('/').filter((segment) => segment !== '' && segment !== '..' && segment !== '.');
    if (segments.length === 0) return undefined;
    return vscode.Uri.joinPath(folder.uri, ...segments);
  }

  /** Forgets the last turn's changes, when there is no last turn any more. */
  private forgetChanges(): void {
    this.lastChanges = { run: '', files: new Map() };
  }

  private async closeReview(requestId: string): Promise<void> {
    const uris = this.reviews.get(requestId);
    if (!uris) return;
    this.reviews.delete(requestId);
    await this.previews.close(uris);
  }

  // --- HostListener ----------------------------------------------------------

  onEvent = (event: HostEvent): void => {
    // The tool call is the last moment the file it names still holds its old
    // contents, and it arrives whether or not the call is gated — which is what
    // makes the changed-files list work in accept_edits and bypass too.
    if (event.type === 'tool_call') {
      void this.changes.snapshotToolCall(event.name, event.args);
    }
    this.postEvent(event);
  };

  /**
   * Sends one event to the card, with the one thing the card cannot work out
   * for itself: whether this call has a change to review.
   *
   * It is decided here rather than in the renderer because the renderer cannot
   * read a module, and a second "which tools change a file" list in `chat.js`
   * would go stale the first time one moved. Everything that puts a tool call
   * in front of the user — a live event, and a replayed one — comes through
   * here, so the answer cannot be missing from one path and present on another.
   */
  private postEvent(event: HostEvent): void {
    const reviewable = event.type === 'tool_call' && reviewOf(event.name, event.args) !== undefined;
    this.post({ type: 'event', event, reviewable });
  }

  onApproval = (request: ApprovalRequest): void => {
    // Also snapshotted here, where nothing can be racing the write: a gated
    // call is blocked until the user answers.
    void this.changes.snapshotToolCall(request.tool, request.args);
    this.post({ type: 'approval', request });
  };

  onApprovalWithdrawn = (requestId: string): void => {
    void this.closeReview(requestId);
    this.post({ type: 'approvalWithdrawn', requestId });
  };

  onDone = (done: DoneParams, failure: RunFailure): void => {
    this.post({ type: 'done', done, failure });
    void this.reportChanges(done.run_id);
    // The turn changed the stored record — its run count, its status, its
    // updated_at — so the sidebar's copy of it is stale from here on.
    this.sessionsChanged.fire();
  };

  onState = (): void => {
    if (this.manager) this.post({ type: 'state', ...this.manager.state() });
    this.runningChanged.fire();
    this.sessionsChanged.fire();
  };

  /**
   * A session opened. The mode the host resolved is the panel's to show, and the
   * select is re-sent here rather than only when the user changes it: a session
   * opened under the workspace's declaration is a select that was showing the
   * wrong thing until now.
   */
  onSession = (result: SessionResult): void => {
    this.rememberAgent(result.agent_id);
    this.postMode();
    this.announceSessionMode(result);
  };

  onNotice = (level: 'info' | 'warn' | 'error', message: string): void =>
    this.post({ type: 'notice', level, message });

  /** Reports what the turn actually changed, once the writes have landed. */
  private async reportChanges(runId: string): Promise<void> {
    try {
      const files = await this.changes.finish();
      // Kept with the run that produced them, so a card's review can tell
      // whether these pre-images are its own turn's.
      this.lastChanges = { run: runId, files: new Map(files.map((file) => [file.path, file])) };
      this.post({
        type: 'changes',
        files: files.map((file) => ({ path: file.path, kind: file.kind })),
      });
    } catch (err) {
      this.log(`could not compute the changed files: ${(err as Error).message}`);
    }
  }

  /**
   * Sends to the tab, if there is one. Closing the tab is now routine — it is
   * how the user puts the conversation away — so a message that arrives with no
   * document behind it is expected rather than exceptional, and the rejection
   * `postMessage` returns for a disposed webview is swallowed rather than left
   * to surface as an unhandled one.
   */
  private post(message: Outbound): void {
    const panel = this.panel;
    // Closed tabs drop events rather than queueing them for a document that may
    // never exist: the session carries on, and what was missed is the live
    // rendering of it. Nothing is lost by that — the turns are on disk, and a
    // document that comes back gets them replayed from there rather than from a
    // queue that would have to hold a whole conversation to be any use.
    if (!panel) return;
    if (!this.listening) {
      // A document that has not run its script yet has no listener, and a
      // message posted into it is delivered to nobody. This is not a corner
      // case: opening a tab and resuming a session in it is one command, so the
      // marker for that session would otherwise be the first thing lost.
      this.pending.push(message);
      return;
    }
    void panel.webview.postMessage(message).then(undefined, () => undefined);
  }

  /** Re-announces where the session stands. The mode travels with `postMode`. */
  private postState(): void {
    const manager = this.manager;
    this.post(manager ? { type: 'state', ...manager.state() } : { type: 'state', state: 'idle' });
  }

  /** Stops the active run. Safe when nothing is running. */
  async stop(): Promise<void> {
    await this.manager?.cancel();
  }

  /**
   * Tells the view which mode is in force.
   *
   * Two things travel, because they are two things: `mode` is the choice this
   * workspace has stored — empty when it has none — and `resolved` is what the
   * session actually runs under. They differ whenever the workspace's
   * `.anvil/permissions.json` supplied the mode, which is exactly the case a
   * person cannot see from the select alone.
   */
  private postMode(): void {
    const state = this.manager?.state();
    this.post({
      type: 'mode',
      mode: this.mode ?? '',
      resolved: state?.mode ?? this.mode ?? 'ask',
      source: state?.modeSource ?? 'default',
    });
  }

  /**
   * A session opened, and the host has said which mode it resolved for it.
   *
   * The mode is announced once per distinct answer rather than once per session:
   * a mode change or a model change re-opens the session, and repeating the same
   * sentence on every one of those would teach the user to ignore it.
   */
  private announceSessionMode(result: SessionResult): void {
    const key = `${result.permission_mode}\x00${result.permission_mode_source}\x00${result.permission_mode_refused ?? ''}`;
    if (key === this.announcedMode) return;
    this.announcedMode = key;
    if (result.permission_mode_refused) {
      this.post({
        type: 'notice',
        level: 'warn',
        message: `${result.permission_mode_refused}. This session started in ask.`,
      });
      return;
    }
    if (result.permission_mode_source === 'file') {
      this.post({
        type: 'notice',
        level: 'info',
        message: `Mode ${MODE_LABELS[result.permission_mode]} comes from this workspace's .anvil/permissions.json.`,
      });
    }
  }

  /** Replaces the host child and tells the view to start over. */
  async restart(): Promise<void> {
    await this.ensureManager().restart();
    this.catalogue = undefined;
    this.postState();
    this.runningChanged.fire();
    this.sessionsChanged.fire();
  }

  /** Whether a host child is live right now. */
  get running(): boolean {
    return this.manager?.running ?? false;
  }

  /**
   * Whether a turn is in flight. Distinct from `running`, which only means a
   * session is open: the Stop button hangs off this one, because stopping an
   * idle conversation is not a thing the user needs a button for.
   */
  get busy(): boolean {
    return this.manager?.state().state === 'running';
  }

  /** Subscribes to host up/down transitions. */
  onRunningChanged(listener: () => void): vscode.Disposable {
    return this.runningChanged.event(listener);
  }

  async dispose(): Promise<void> {
    const manager = this.manager;
    this.manager = undefined;
    this.runningChanged.dispose();
    this.sessionsChanged.dispose();
    this.diagnostics?.dispose();
    this.diagnostics = undefined;
    for (const uris of this.reviews.values()) {
      await this.previews.close(uris).catch(() => undefined);
    }
    this.reviews.clear();
    if (manager) await manager.dispose();
  }
}

/** Builds the webview document. Nothing here is fetched at runtime. */
function renderHtml(webview: vscode.Webview, extensionUri: vscode.Uri): string {
  const nonce = makeNonce();
  const asset = (name: string): vscode.Uri =>
    webview.asWebviewUri(vscode.Uri.joinPath(extensionUri, 'media', name));
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource}; font-src ${webview.cspSource}; img-src data: ${webview.cspSource}; script-src 'nonce-${nonce}';">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<link rel="stylesheet" href="${asset('chat.css')}">
<title>Anvil</title>
</head>
<body>
  <div id="banner" class="banner" hidden></div>
  <div id="transcript" class="transcript" role="log" aria-live="polite">
    <div id="empty" class="empty">
      <p>Ask for a change. Anvil reads, edits, and runs commands in this workspace.</p>
      <p class="hint">Mutating calls stop for your approval. <kbd>Stop</kbd> interrupts a turn.</p>
      <p class="hint"><kbd>/</kbd> lists commands, <kbd>@</kbd> references a file.</p>
      <p class="hint">Past conversations are in the Anvil view in the activity bar.</p>
    </div>
  </div>
  <!--
    The composer is out of the flow, so the transcript scrolls underneath it and
    the reading column stays full height. chat.js measures this box into
    --anvil-composer-h, which is what the transcript reserves at its bottom.
  -->
  <div id="composerDock" class="dock">
    <div id="status" class="status" hidden></div>
    <div id="bypass" class="bypass" hidden>Bypass is on. Tool calls run without asking.</div>
    <div id="dontAsk" class="dont-ask" hidden>No asking. Only calls this workspace's rules allow will run.</div>
    <form id="composer" class="composer">
      <div id="palette" class="palette" hidden></div>
      <div id="chips" class="chips" hidden></div>
      <textarea id="input" rows="1" placeholder="Describe a change…  (@ references a file, / lists commands)" aria-label="Prompt"></textarea>
      <div class="actions">
        <!--
          The empty value is a real choice: "let the workspace decide". It is
          first because it is what a workspace with no stored choice is in, and
          the label is filled in by chat.js with the mode it resolved to, since
          which mode that is depends on a file this side has to read.
        -->
        <select id="mode" aria-label="Permission mode" title="What the agent may do without asking">
          <option value="">Workspace default</option>
          <option value="ask">Ask before changes</option>
          <option value="accept_edits">Accept edits</option>
          <option value="plan">Plan only</option>
          <option value="dont_ask">Only what the rules allow</option>
          <option value="bypass">Bypass approvals</option>
        </select>
        <button id="rules" type="button" class="ghost" title="Which rule files this workspace loaded" hidden>Rules</button>
        <button id="permissions" type="button" class="ghost" title="Open .anvil/permissions.json: what runs without asking, and what is refused">Permissions</button>
        <span class="spacer"></span>
        <button id="attach" type="button" class="secondary" title="Reference a file or folder">@</button>
        <button id="stop" type="button" class="secondary" hidden>Stop</button>
        <button id="send" type="submit" class="primary">Send</button>
      </div>
    </form>
  </div>
  <!--
    The Markdown parser first, because it is a plain script assigning a global
    rather than a module -- media/ is linted as browser code and tsc never
    copies it into out/, so there is nothing to import. chat.js reads the global
    lazily on its first render, so the order is convention rather than a
    requirement; it is written this way so a reader can see the dependency.
  -->
  <script nonce="${nonce}" src="${asset('markdown.js')}"></script>
  <script nonce="${nonce}" src="${asset('chat.js')}"></script>
</body>
</html>`;
}

/** A fresh CSP nonce per render, from the platform's random source. */
function makeNonce(): string {
  return randomBytes(16).toString('base64');
}
