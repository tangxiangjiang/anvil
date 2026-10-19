/**
 * One conversation: a webview in an editor tab, and the session behind it.
 *
 * The webview is a rendering surface only. Everything with a decision in it —
 * sessions, approvals, context composition, review tabs — happens on this side
 * of the message port, so the untrusted-ish HTML never holds state the host
 * needs and never names a path the host will act on without this side having
 * decided that it should.
 *
 * A panel is a tab is a conversation. Several may be open at once; they share
 * one host child (`Hub`) and nothing else. Nothing a tab says or is told may
 * reach another tab, which is what the router in `router.ts` enforces on the
 * wire and what this class keeps true in its own fields.
 *
 * The tab is a document, which sets two rules this class exists to keep. A
 * closed tab takes its session with it — a run nobody can see is a run nobody
 * can stop — and messages that arrive with no view attached are dropped rather
 * than queued. And reopening it re-announces everything, because a fresh
 * document knows nothing — the `ready` path. What it cannot re-announce is the
 * conversation itself, which happened before the document existed and left no
 * record in this process; that is replayed from the store, and re-attaching to
 * the session first is how the window finds out which conversation that was.
 */

import { randomBytes } from 'node:crypto';
import * as vscode from 'vscode';
import { ChangesTracker, ChangedFile } from './changes';
import { EditAnchor, PreImage, locateEdit, planReview, reviewOf } from './editDiff';
import { PartialDiff, applyAccepted, partialDiff, rewriteArgs } from './hunks';
import { ReviewPanels } from './reviewPanel';
import { buildPromptContext, pickMentions } from './editorContext';
import { attachmentsFromDataUrls, dataUrlFromBytes, mimeForImagePath } from './attachments';
import type { Hub } from './hub';
import { HostListener, HostManager, ResolvedMode } from './hostManager';
import { ChatSession, SessionConfig, SessionState } from './session';
import { PreviewDocuments } from './review';
import { dedupeMentions, relativeMention } from './mentions';
import { mcpEnabled, missingMcpSources, settingSourcesFor } from './mcp';
import { shouldReattach } from './reattach';
import { replayOf } from './transcript';
import { CommandAction, SlashCommand, parseSlash, route } from './commands';
import {
  ApprovalRequest,
  Decision,
  DoneParams,
  HostEvent,
  McpServerInfo,
  PermissionMode,
  PermissionModeSource,
  PermissionModes,
  RuleFileInfo,
  RuleInfo,
  RuleSpec,
  SessionInfo,
  SessionResult,
  SessionTranscriptResult,
} from './protocol';
import { RunFailure } from './errors';
import { readSettings, readWebSearchKey } from './settings';
import { webSearchNeedsKey } from './webSearch';

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
  /** "Let me keep only some of this", from an approval card's diff preview. */
  | { type: 'reviewPartial'; requestId: string }
  | { type: 'reviewChange'; path: string }
  /**
   * "Put the files back the way they were before that run". The extension asks
   * for confirmation before touching anything: the restore happens on disk, and
   * there is no second undo for it.
   */
  | { type: 'restoreCheckpoint'; runId: string }
  /**
   * "Branch the conversation from this run", from a finished run's row. The new
   * conversation opens in its own tab; this one stays exactly as it is.
   */
  | { type: 'forkSession'; runId: string }
  /**
   * "Show me what that call changed", from a finished tool card. The call's
   * facts travel with the click because this is the only place they still
   * exist: the card already holds the arguments, and keeping a copy of every
   * call's body in the extension for the length of a session is not worth a
   * second reference to what the IN pane is already showing.
   */
  | { type: 'reviewCall'; toolCallId: string; runId?: string; name?: string; args?: unknown }
  /** A link the transcript rendered. The URL is re-checked before it is opened. */
  | { type: 'openLink'; url: string }
  /**
   * "Put this answer on the clipboard." The text is the raw Markdown the bubble
   * holds; the host writes it, because the clipboard belongs to the editor.
   */
  | { type: 'copy'; text: string }
  /** The composer's image button: the extension's own open dialog. */
  | { type: 'pickImages' }
  /**
   * Files dropped on the panel. `paths` are plain absolute paths (Electron's
   * old `File.path`), `uris` are `file:` URIs (the editor's drag payload); both
   * are resolved against the workspace here, because only this side knows the
   * root.
   */
  | { type: 'dropFiles'; paths?: string[]; uris?: string[] };

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
  | { type: 'event'; event: HostEvent; reviewable?: boolean; edit?: EditAnchor }
  | { type: 'approval'; request: ApprovalRequest }
  | { type: 'approvalWithdrawn'; requestId: string }
  /**
   * An approval the review panel answered. The card settles on this rather than
   * on its own button, and says how much was kept when it was not all of it —
   * "Allowed." over a change that was half applied would be a lie of degree.
   */
  | {
      type: 'approvalSettled';
      requestId: string;
      decision: Decision;
      kept?: number;
      total?: number;
    }
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
  /**
   * Images the picker read, as data URLs — the same shape a paste leaves in
   * the composer, so the webview has one representation to draw and one path to
   * send.
   */
  | { type: 'images'; images: string[] }
  | { type: 'vision'; enabled: boolean }
  /**
   * The context window the token meter measures the last run against, or 0
   * when it is unknown. The window is display arithmetic only and never travels
   * to the host.
   */
  | { type: 'context'; window: number }
  | { type: 'changes'; files: Array<Pick<ChangedFile, 'path' | 'kind'>> }
  /**
   * A finished run that captured pre-images, so it can be undone. Posted only
   * when the run changed something: an undo that would do nothing is not a
   * button worth showing.
   */
  | { type: 'checkpoint'; runId: string; files: string[] }
  /** What the restore did, or that it was cancelled or failed. */
  | {
      type: 'checkpointRestored';
      runId: string;
      cancelled?: boolean;
      restored?: number;
      deleted?: number;
      error?: string;
    }
  /**
   * What became of a fork the run's button asked for. Exactly one of the two
   * endings: the new agent's id, or why the copy could not be made.
   */
  | { type: 'forked'; runId: string; agentId?: string; error?: string }
  | { type: 'commands'; commands: SlashCommand[] }
  | { type: 'help'; commands: SlashCommand[] }
  | { type: 'mode'; mode: PermissionMode | ''; resolved: PermissionMode; source: PermissionModeSource }
  | { type: 'rules'; files: RuleFileInfo[] }
  /**
   * What the enabled MCP layers declare, and which of their files are missing.
   * Configuration only: there is no connected field, because the host cannot
   * observe one.
   */
  | { type: 'mcp'; servers: McpServerInfo[]; notes: string[]; error?: string }
  /**
   * The web tools this session was given. Posted only when enabled — the row's
   * absence is the "off" answer, not a row that says so. `key` is whether a
   * credential is stored for the one provider that needs one.
   */
  | { type: 'webSearch'; provider: string; key: boolean }
  | { type: 'session'; agentId: string; started: boolean }
  /**
   * The agent this tab is showing, for the document to remember across a
   * reload. Empty means a conversation that has not started yet.
   */
  | { type: 'agent'; agentId: string }
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
  auto: 'Auto',
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
    case 'auto':
      return 'Auto: read-only commands and edits inside this workspace run without asking. Everything else still asks.';
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
  /**
   * This tab's conversation, and nothing else's.
   *
   * It is created on the first thing that needs a host and kept across
   * re-opens (a mode or model change closes the session and resumes the same
   * agent). A crash drops it; the agent id below is what brings it back.
   */
  private session: ChatSession | undefined;
  /** A session being built, so two messages sent at once cannot build two. */
  private opening: Promise<ChatSession> | undefined;
  /**
   * The agent this tab is showing.
   *
   * It outlives the session handle and is what a reload restores: the document
   * remembers it with `vscode.setState`, the serializer hands it back, and the
   * panel resumes it. Empty means the tab is showing a conversation that has
   * not started yet.
   */
  private agentId: string | undefined;
  /**
   * This tab's session options, until a session exists to carry them.
   *
   * Per tab rather than per window: chapter 37 makes the mode a session option,
   * so two tabs may run different ones, and a change here must not reach the
   * conversation next door.
   */
  private overrides: Partial<SessionConfig> = {};
  /** The mode the live session resolved, which is not always the one asked for. */
  private resolvedMode: ResolvedMode | undefined;
  /** Whether this is the tab in front, for the one command that means "this one". */
  private active = false;
  private disposed = false;
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
  /** The per-hunk review documents, one per pending approval that can be split. */
  private readonly partialReviews: ReviewPanels;
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

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly log: (line: string) => void,
    private readonly previews: PreviewDocuments,
    private readonly hub: Hub,
    agentId?: string,
  ) {
    this.mode = readMode(context);
    this.agentId = agentId === '' ? undefined : agentId;
    this.partialReviews = new ReviewPanels(context.extensionUri);
  }

  /**
   * Opens this tab, or reveals it when its document is already up.
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
    // Brand mark on the editor tab (marketplace icon is package.json `icon`).
    panel.iconPath = vscode.Uri.joinPath(this.context.extensionUri, 'media', 'icon.png');
    panel.webview.options = {
      enableScripts: true,
      localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
    };
    panel.webview.html = renderHtml(panel.webview, this.context.extensionUri);
    panel.webview.onDidReceiveMessage((msg: Inbound) => void this.onMessage(msg));
    this.active = panel.active;
    panel.onDidChangeViewState((event) => {
      this.active = event.webviewPanel.active;
      if (this.active) this.post({ type: 'focus' });
      // The front tab changed, and Stop means the tab in front of the user.
      this.hub.notifyRunning();
    });
    panel.onDidDispose(() => {
      // Guarded: `deactivate` disposes the panel too, and a stale disposal must
      // not clear a panel that has since replaced it.
      if (this.panel !== panel) return;
      this.panel = undefined;
      this.listening = false;
      this.pending = [];
      this.active = false;
      // A closed tab is a closed conversation. The agent stays on disk, so the
      // sidebar can open it again — but a session behind a document nobody can
      // see would be a run nobody can stop or watch.
      void this.dispose();
    });
    this.updateTitle();
  }

  /**
   * Closes this tab's document, ending the session behind it.
   *
   * It awaits the teardown because the caller may be about to delete the
   * conversation on disk: a session still holding the agent could write the file
   * back on its next prompt. Disposing the document fires the same handler the
   * user's close button does, and `dispose` is idempotent, so this is the one
   * way that settles both paths.
   */
  async close(): Promise<void> {
    this.panel?.dispose();
    await this.dispose();
  }

  /** Brings this tab to the front, opening it first when it has no document. */
  reveal(): void {
    if (!this.panel) {
      this.open();
      return;
    }
    this.panel.reveal(this.panel.viewColumn, false);
  }

  /** Whether this is the tab the user is looking at. */
  isActive(): boolean {
    return this.active;
  }

  /** Posts a banner line to this tab, from the window-level half. */
  notice(level: 'info' | 'warn' | 'error', message: string): void {
    this.post({ type: 'notice', level, message });
  }

  /**
   * This tab's session, built on the first thing that needs one.
   *
   * No host work happens at activation, and the things that can ask for a host
   * are user actions — a prompt in the tab, or the session view being opened.
   * A restored tab is not one of them: it is built here and opened by the
   * resume the reattach path performs.
   *
   * The mode the workspace last chose travels with it. Nothing is recorded when
   * the workspace has no stored choice, and that is the point of the guard:
   * sending `permission_mode: undefined` would travel as no key at all, but
   * recording an override for it would still make this tab look like it had an
   * opinion — and it is the client having none that lets the workspace's file
   * decide.
   */
  private ensureSession(): Promise<ChatSession> {
    if (this.session) return Promise.resolve(this.session);
    if (this.opening) return this.opening;
    const config: SessionConfig = { ...readSettings().session, ...this.overrides };
    if (this.mode !== undefined) config.permission_mode = this.mode;
    this.opening = this.hub.manager.open(config, this).then(
      async (session) => {
        this.opening = undefined;
        if (this.disposed) {
          // The tab closed while the host was being started. Nothing may
          // outlive the document that asked for it.
          await session.close().catch(() => undefined);
          throw new Error('this chat tab was closed');
        }
        this.session = session;
        return session;
      },
      (err: unknown) => {
        this.opening = undefined;
        throw err;
      },
    );
    return this.opening;
  }

  /**
   * The session to talk through, pointed at the agent this tab shows.
   *
   * After a crash the handle is gone but the agent is not: the next prompt
   * resumes it, or the conversation would silently restart from nothing.
   */
  private async liveSession(): Promise<ChatSession> {
    const session = await this.ensureSession();
    if (this.agentId && session.agent !== this.agentId) {
      await session.continueWith(this.agentId);
    }
    return session;
  }

  /**
   * Applies options to this tab's conversation, and records them for the next
   * session it opens.
   *
   * Mode, model and skills are fixed when the SDK builds its options, so a live
   * session is re-opened under them — same agent, so the history and the
   * transcript both stay. Returns false when there was nothing live to change:
   * a choice made before the first prompt is simply the config that prompt will
   * use, and opening a session to hold it would be work nobody asked for.
   */
  private async applyOptions(changes: Partial<SessionConfig>): Promise<boolean> {
    this.overrides = { ...this.overrides, ...changes };
    const session = this.session;
    if (!session) return false;
    const result = await session.reopen(changes);
    if (result) this.noteResolvedMode(result);
    return true;
  }

  /** Turns a skill on for this conversation. False when it was already on. */
  private async useSkill(skillId: string): Promise<boolean> {
    const current = this.session?.settings.skills ?? this.overrides.skills ?? [];
    if (current.includes(skillId)) return false;
    await this.applyOptions({ skills: [...current, skillId] });
    return true;
  }

  /** Keeps what the host resolved for the live session, and tells the view. */
  private noteResolvedMode(result: SessionResult): void {
    this.resolvedMode = {
      mode: result.permission_mode,
      source: result.permission_mode_source,
      refused: result.permission_mode_refused,
    };
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
      this.reportContext();
      this.postAgentId();
      this.postMode();
      if (this.rules.length > 0) this.post({ type: 'rules', files: this.rules });
      this.postState();
      for (const request of this.pendingApprovals()) {
        this.post({ type: 'approval', request });
      }

      // The document has no transcript, and after a window reload nothing else
      // does either. Coming back to the session is the last thing done here,
      // because it is the only part that can take a moment — everything above
      // is already on the wire by the time the host is asked anything.
      //
      // The MCP card and the web-tools row follow it rather than racing it: a
      // restored tab should show its conversation first, and both describe the
      // session's configuration rather than any one turn in it.
      void this.reattach()
        .catch(() => undefined)
        .then(() => this.reportMcp())
        .then(() => this.reportWebSearch());
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
        // Nothing running is nothing to cancel: a Stop on an idle tab is not a
        // reason to start a host.
        await this.session?.cancel();
        return;
      case 'answer':
        await this.answer(msg.requestId, msg.decision, msg.scope);
        return;
      case 'remember':
        await this.remember(msg.requestId, msg.rule);
        return;
      case 'openPermissions':
        await this.hub.openPermissionRules();
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
      case 'reviewPartial':
        await this.openPartialReview(msg.requestId);
        return;
      case 'reviewChange':
        await this.openChangeReview(msg.path);
        return;
      case 'restoreCheckpoint':
        await this.restoreCheckpoint(msg.runId);
        return;
      case 'forkSession':
        await this.forkSession(msg.runId);
        return;
      case 'reviewCall':
        await this.openCallReview(msg);
        return;
      case 'openLink':
        await this.openLink(msg.url);
        return;
      case 'copy':
        await this.copy(msg.text);
        return;
      case 'pickImages':
        await this.pickImages();
        return;
      case 'dropFiles':
        this.dropFiles(msg.paths, msg.uris);
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

  /**
   * Puts an answer on the clipboard.
   *
   * The webview could try `navigator.clipboard`, but a webview's access to it
   * depends on permissions this document does not control, and a Copy that
   * silently does nothing is worse than a round trip. `vscode.env.clipboard` is
   * the editor's own, and it needs no permission. Only a string is written: the
   * text of a bubble the user asked to copy, not anything derived from it.
   */
  private async copy(text: unknown): Promise<void> {
    if (typeof text !== 'string' || text === '') return;
    await vscode.env.clipboard.writeText(text);
  }

  /**
   * Opens the editor's image picker and posts what was chosen.
   *
   * A webview cannot open a file dialog, so the composer's button asks here.
   * The files are read on this side and answered as `data:` URLs — the same
   * shape a paste leaves in the composer — so the webview has one kind of
   * attachment to draw and one path to send, whatever produced it.
   */
  private async pickImages(): Promise<void> {
    const picked = await vscode.window.showOpenDialog({
      canSelectMany: true,
      title: 'Attach images',
      openLabel: 'Attach',
      filters: { Images: ['png', 'jpg', 'jpeg', 'gif', 'webp'] },
    });
    if (!picked || picked.length === 0) return;
    const images: string[] = [];
    const errors: string[] = [];
    for (const uri of picked) {
      try {
        const bytes = await vscode.workspace.fs.readFile(uri);
        const result = dataUrlFromBytes(bytes, mimeForImagePath(uri.path));
        if (result.dataUrl) images.push(result.dataUrl);
        else if (result.error) errors.push(`${uri.path}: ${result.error}`);
      } catch (err) {
        errors.push(`${uri.path}: ${(err as Error).message}`);
      }
    }
    if (images.length > 0) this.post({ type: 'images', images });
    if (errors.length > 0) {
      this.post({
        type: 'notice',
        level: 'warn',
        message: `Some images were not attached: ${errors.join('; ')}`,
      });
    }
  }

  /**
   * Turns dropped files into mentions of the ones inside the workspace.
   *
   * The webview posts whatever the drag carried — plain paths and/or `file:`
   * URIs, because which of the two exists depends on how the drag started —
   * and this side answers the one question that matters: is the file inside the
   * workspace? A path outside it is not a mention, and saying so now is better
   * than letting the host refuse the prompt later.
   */
  private dropFiles(paths: unknown, uris: unknown): void {
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) {
      this.post({
        type: 'notice',
        level: 'warn',
        message: 'No workspace folder is open, so a dropped file has nothing to be relative to.',
      });
      return;
    }
    const candidates: string[] = [];
    for (const value of Array.isArray(paths) ? paths : []) {
      if (typeof value === 'string' && value !== '') candidates.push(value);
    }
    for (const value of Array.isArray(uris) ? uris : []) {
      if (typeof value !== 'string' || value === '') continue;
      // A drag payload can carry comment lines and blank ones; `Uri.parse`
      // accepts almost anything, so the scheme is what decides.
      let uri: vscode.Uri;
      try {
        uri = vscode.Uri.parse(value);
      } catch {
        continue;
      }
      if (uri.scheme !== 'file') continue;
      candidates.push(uri.fsPath);
    }
    const mentions: string[] = [];
    let outside = 0;
    for (const candidate of candidates) {
      const relative = relativeMention(folder.uri.fsPath, candidate);
      if (relative === undefined) outside++;
      else mentions.push(relative);
    }
    const unique = dedupeMentions(mentions);
    if (unique.length > 0) this.post({ type: 'mentions', paths: unique });
    if (outside > 0) {
      this.post({
        type: 'notice',
        level: 'warn',
        message:
          outside === 1
            ? 'A dropped file is outside this workspace, so it was not added.'
            : `${outside} dropped files are outside this workspace, so they were not added.`,
      });
    }
  }

  // --- commands ---------------------------------------------------------------

  /** The catalogue, fetched once per host by the hub. */
  private commandCatalogue(): Promise<SlashCommand[]> {
    return this.hub.commandCatalogue();
  }

  private async sendCatalogue(): Promise<void> {
    this.post({ type: 'commands', commands: await this.commandCatalogue() });
  }

  /**
   * Applies a permission mode to this tab's conversation. The mode is a session
   * option, so the session is re-opened under it — same agent, so the history
   * and the transcript both stay. Another tab's session is not touched: the
   * mode belongs to the conversation, not to the window.
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
    // Checked here rather than left to the re-open, so the refusal is reported
    // as a refusal. `reopen` would throw the same thing, but by then the chosen
    // mode is already recorded as an override and would silently take effect on
    // the *next* session — a mode the user was told had been refused.
    if (this.session?.currentState === 'running') {
      this.post({ type: 'notice', level: 'warn', message: 'Stop the turn before changing the mode.' });
      this.postMode();
      return;
    }
    try {
      await this.applyOptions({ permission_mode: chosen });
    } catch (err) {
      this.post({ type: 'notice', level: 'error', message: `Could not change the mode: ${(err as Error).message}` });
      this.postMode();
      return;
    }
    this.mode = chosen;
    // Clearing the key rather than storing the empty string: `readMode` treats
    // both the same, and an absent key is what a workspace that never chose
    // anything has. It is the *stored* choice a new tab starts from; live tabs
    // keep the mode their own session resolved.
    await this.context.workspaceState.update(MODE_KEY, chosen);
    this.postMode();
    this.postState();
    this.post({ type: 'notice', level: chosen === 'bypass' ? 'warn' : 'info', message: modeNotice(chosen) });
  }

  // --- session history --------------------------------------------------------

  /** Opens a past agent from this workspace as the current conversation. */
  private async pickResume(): Promise<void> {
    let sessions: SessionInfo[] = [];
    try {
      sessions = await this.hub.listSessions();
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

  /** Ends this tab's conversation. The host stays up; the next prompt starts fresh. */
  private async clearConversation(): Promise<void> {
    const session = this.session;
    if (session) await session.reset();
    await this.forgetAgent();
    this.changes.begin();
    this.forgetChanges();
    this.post({ type: 'reset' });
    this.post({ type: 'session', agentId: '', started: false });
    this.postState();
    this.updateTitle();
    this.hub.notifySessions();
  }

  // --- what the hub and the sidebar ask about this tab -------------------------

  /** The agent the tab is showing, whether or not a turn is in flight. */
  currentAgentId(): string | undefined {
    return this.session?.agent ?? this.agentId;
  }

  /** The agent with a turn in flight. */
  runningAgentId(): string | undefined {
    return this.session?.currentState === 'running' ? this.currentAgentId() : undefined;
  }

  /** Whether a turn is in flight in this tab. */
  get busy(): boolean {
    return this.session?.currentState === 'running';
  }

  /**
   * Whether this tab is holding a question the user has to answer.
   *
   * It is the front tab that is asked, and the tab behind keeps its own cards:
   * the window's status bar says "waiting" when any tab is, because a run
   * blocked on an approval in a tab nobody is looking at is exactly the state a
   * window-level indicator exists to surface.
   */
  awaitingApproval(): boolean {
    return this.pendingApprovals().length > 0;
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
        // The session says so itself when the skill is already on; a second
        // notice here would be the same sentence twice.
        const added = await this.useSkill(action.skillId);
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
      const session = await this.ensureSession();
      await session.continueWith(agentId);
    } catch (err) {
      this.post({
        type: 'notice',
        level: 'error',
        message: `Could not resume ${agentId}: ${(err as Error).message}`,
      });
      this.postState();
      return;
    }
    this.agentId = agentId;
    this.postAgentId();
    // Cleared before the marker, never after: the transcript that was on screen
    // belonged to the agent that was current a moment ago, and the marker is
    // the first line of the conversation that replaced it.
    this.post({ type: 'reset' });
    this.post({ type: 'session', agentId, started: true });
    this.postState();
    this.updateTitle();
    this.hub.notifyTabs();
    this.hub.notifySessions();
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
      result = await this.hub.transcript(agentId);
    } catch (err) {
      this.log(`no replay for ${agentId}: ${(err as Error).message}`);
      return;
    }

    const sessionId = this.session?.id ?? '';
    const steps = replayOf(result.entries ?? [], sessionId);
    if (steps.length === 0) return;
    for (const step of steps) {
      if (step.kind === 'user') this.post({ type: 'user', text: step.text });
      else if (step.kind === 'notice') this.post({ type: 'note', text: step.text });
      else if (step.kind === 'runEnd') this.post({ type: 'runEnd', done: step.done });
      else await this.postEvent(step.event);
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
   * Comes back to the conversation this tab was showing, if it still has one.
   *
   * The id comes from the document, which is where VS Code keeps a tab's state
   * across a reload — the extension host does not survive one, so there is
   * nowhere else it could have been. Three endings, and the middle one is why
   * the agent list is consulted rather than the memory being trusted: an id the
   * workspace no longer stores has to be forgotten, or every reload would try
   * to resume an agent that is gone and say so. A listing that could not be
   * read is not evidence of that — it is no evidence at all, so the memory
   * stays and nothing happens.
   */
  private async reattach(): Promise<void> {
    const stored = this.agentId;
    if (!stored) return;

    // A session that is already live needs no resuming — it survived because
    // this document, not the window, is what went away. The transcript is still
    // what the document lost, and that is what it gets.
    if (this.session?.agent === stored) {
      await this.replay(stored);
      return;
    }

    let listed: SessionInfo[] | undefined;
    try {
      listed = await this.hub.listSessions();
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
   * Remembers which agent the tab is showing.
   *
   * Written here rather than at each place a session opens: this side hears
   * about every one of them — new, resumed, re-opened for a model change — and
   * the id is the host's answer, not something composed from what was asked.
   * The document is told separately (`vscode.setState`), because it is the one
   * that survives a reload.
   */
  private rememberAgent(agentId: string): void {
    if (!agentId || agentId === this.agentId) return;
    this.agentId = agentId;
    this.postAgentId();
    this.updateTitle();
    this.hub.notifyTabs();
  }

  /** Forgets it. Called when the conversation is ended, and when it is gone. */
  private async forgetAgent(): Promise<void> {
    if (this.agentId === undefined) return;
    this.agentId = undefined;
    this.postAgentId();
    this.updateTitle();
    this.hub.notifyTabs();
  }

  /**
   * Tells the document which agent to remember, so a reload can restore this
   * tab onto the same conversation.
   *
   * It is its own message rather than a field on `state`: this is the one thing
   * the document must persist, and it changes at moments that are not state
   * changes. `vscode.setState` is the document's only durable memory.
   */
  private postAgentId(): void {
    this.post({ type: 'agent', agentId: this.agentId ?? '' });
  }

  /** Names the tab after the conversation in it, so a tab strip is readable. */
  private updateTitle(): void {
    const agentId = this.currentAgentId();
    const title = agentId ? `Anvil — ${agentId.slice(0, 8)}` : 'Anvil';
    if (this.panel && this.panel.title !== title) this.panel.title = title;
  }

  /** Applies a model to the conversation, from an argument or a picker. */
  private async pickModel(given: string): Promise<void> {
    let model = given;
    if (model === '') {
      let entries: Awaited<ReturnType<Hub['listModels']>> = [];
      try {
        // This tab's provider, not the window's: a conversation pointed at one
        // endpoint should list that endpoint's models.
        entries = await this.hub.listModels({
          provider: this.overrides.provider,
          base_url: this.overrides.base_url,
        });
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
    await this.applyOptions({ model });
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
    await this.liveSession()
      .then((session) => session.send(text, { context, attachments }))
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
    try {
      const files = await this.hub.rules();
      const key = JSON.stringify(files);
      if (key === JSON.stringify(this.rules)) return;
      this.rules = files;
      this.post({ type: 'rules', files });
    } catch (err) {
      this.log(`could not list rule files: ${(err as Error).message}`);
    }
  }

  /**
   * Reports the MCP servers this workspace's enabled files declare.
   *
   * Asked once per document: the layers are session config, so a tab in this
   * workspace loads the same set until the window reloads, and the settings are
   * where a different set is chosen. Nothing is posted when both layers are
   * off — a card on every conversation saying MCP is off is noise, and the
   * setting is already the answer.
   */
  private async reportMcp(): Promise<void> {
    const { mcp } = readSettings();
    if (!mcpEnabled(mcp)) return;
    try {
      const listing = await this.hub.mcpServers(settingSourcesFor(mcp));
      this.post({
        type: 'mcp',
        servers: listing.servers ?? [],
        notes: missingMcpSources(listing.sources ?? []),
      });
    } catch (err) {
      // A malformed file is the interesting failure, and an empty card would
      // hide the one thing the user can fix. The hosts that predate the method
      // fail too, and saying so once is honest: there is no listing to show.
      this.post({ type: 'mcp', servers: [], notes: [], error: (err as Error).message });
    }
  }

  /**
   * Reports the web tools this session was given.
   *
   * Only when enabled: a row on every conversation saying the web tools are off
   * is noise, and the setting is already the answer. The key is reported as
   * present or absent and in no other form — its value never leaves
   * SecretStorage, so the webview could not render it even if it wanted to.
   */
  private async reportWebSearch(): Promise<void> {
    const { webSearch } = readSettings();
    if (!webSearch.enabled) return;
    const provider = webSearch.provider.trim().toLowerCase() === 'mcp' ? 'mcp' : 'http';
    const stored = webSearchNeedsKey(webSearch)
      ? await readWebSearchKey(this.context)
      : undefined;
    this.post({
      type: 'webSearch',
      provider,
      key: stored !== undefined && stored.trim() !== '',
    });
  }

  /**
   * Tells the document the window the token meter measures against.
   *
   * Public because a settings change restarts the host without reloading any
   * tab, and `Hub.restart` calls this on each one so the number on screen is
   * the one the setting now says. 0 is "unknown" and means counts only.
   */
  reportContext(): void {
    this.post({ type: 'context', window: readSettings().contextWindow });
  }

  /** Answers an approval, and closes the review it was answered from. */
  private async answer(requestId: string, decision: Decision, scope?: string): Promise<void> {
    await this.session?.respond(requestId, decision, { scope });
    await this.closeReview(requestId);
    // `respond` drops the request from the session's map itself, so no
    // withdrawal notification follows it — the hub has to be told here or the
    // status bar would keep saying "waiting" for a question already answered.
    this.hub.notifyRunning();
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
    try {
      const result = await this.hub.addPermissionRule(rule, requestId);
      await this.closeReview(requestId);
      // A rule that decided this call answered it on the host, and the client
      // heard that only as this result — without this the tab would keep
      // claiming a question that is already answered.
      if (result.approved === true) this.session?.forgetApproval(requestId);
      this.hub.notifyRunning();
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

  private pendingApprovals(): ApprovalRequest[] {
    return this.session?.pendingApprovals() ?? [];
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

  /**
   * Opens the per-hunk review for an approval that can be taken in parts.
   *
   * The card's `Review changes…` button is a hint about shape — a diff with both
   * bodies. Whether the change really can be split is decided by `partialDiff`,
   * which proves every hunk applies. A change that fails the proof is answered
   * with Allow or Deny, and the notice says so rather than leaving a button
   * that would produce a guessed file.
   */
  private async openPartialReview(requestId: string): Promise<void> {
    const request = this.pendingApprovals().find((candidate) => candidate.request_id === requestId);
    const partial = request ? partialDiff(request.tool, request.preview) : undefined;
    if (!request || !partial) {
      this.post({
        type: 'notice',
        level: 'warn',
        message: 'This change cannot be applied in parts. Use Allow or Deny.',
      });
      return;
    }
    this.partialReviews.open(requestId, {
      path: request.preview?.path ?? request.tool,
      hunks: partial.hunks,
      onAccept: (accepted) => this.answerPartial(request, partial, accepted),
      onReject: () => this.rejectPartial(request, partial.hunks.length),
    });
  }

  /**
   * Answers "keep these parts" with an allow whose arguments carry the accepted
   * text.
   *
   * The text is computed against the preview's whole `before` and handed to the
   * host as rewritten arguments, so the call executes exactly what the reviewer
   * chose — there is no second diff, and nothing is re-applied from the patch.
   * When the computation or the rewrite refuses, nothing is sent and the card
   * keeps its ordinary choice.
   */
  private async answerPartial(
    request: ApprovalRequest,
    partial: PartialDiff,
    accepted: readonly number[],
  ): Promise<void> {
    if (accepted.length === 0) {
      await this.rejectPartial(request, partial.hunks.length);
      return;
    }
    const text = applyAccepted(partial.before, partial.hunks, accepted);
    const args =
      text === undefined
        ? undefined
        : rewriteArgs(request.tool, request.args, partial.before, partial.after, text);
    if (text === undefined || args === undefined) {
      this.post({
        type: 'notice',
        level: 'warn',
        message:
          'This change no longer matches the file it was previewed from, so nothing was sent. Decide with Allow or Deny.',
      });
      return;
    }
    const session = this.session;
    if (!session) return;
    try {
      await session.respond(request.request_id, 'allow', { args });
    } catch (err) {
      this.post({
        type: 'notice',
        level: 'error',
        message: `Could not answer the approval: ${(err as Error).message}`,
      });
      return;
    }
    await this.closeReview(request.request_id);
    this.post({
      type: 'approvalSettled',
      requestId: request.request_id,
      decision: 'allow',
      kept: accepted.length,
      total: partial.hunks.length,
    });
  }

  /** Answers "keep none of it" as a denial, in the same words the card's Deny uses. */
  private async rejectPartial(request: ApprovalRequest, total: number): Promise<void> {
    const session = this.session;
    if (!session) return;
    try {
      await session.respond(request.request_id, 'deny', {
        message: 'The reviewer rejected this change.',
      });
    } catch (err) {
      this.post({
        type: 'notice',
        level: 'error',
        message: `Could not answer the approval: ${(err as Error).message}`,
      });
      return;
    }
    await this.closeReview(request.request_id);
    this.post({
      type: 'approvalSettled',
      requestId: request.request_id,
      decision: 'deny',
      kept: 0,
      total,
    });
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
    // Both surfaces belong to one question: closing the diff tabs while the
    // per-hunk document stayed up would leave a live set of buttons over a
    // request that has already been answered.
    this.partialReviews.close(requestId);
    const uris = this.reviews.get(requestId);
    if (!uris) return;
    this.reviews.delete(requestId);
    await this.previews.close(uris);
  }

  // --- HostListener ----------------------------------------------------------

  onEvent = (event: HostEvent): void => {
    // The tool call is the last moment the file it names still holds its old
    // contents, and it arrives whether or not the call is gated — which is what
    // makes the changed-files list work in accept_edits and bypass too. The
    // read is awaited before the card is drawn, so the line number comes from
    // the file the call found rather than from the file after it wrote.
    void (async () => {
      if (event.type === 'tool_call') {
        await this.changes.snapshotToolCall(event.name, event.args);
      }
      await this.postEvent(event, 'snapshot');
    })();
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
  private async postEvent(event: HostEvent, source: 'snapshot' | 'disk' = 'disk'): Promise<void> {
    const reviewable = event.type === 'tool_call' && reviewOf(event.name, event.args) !== undefined;
    const edit = await this.editAnchor(event, source);
    this.post({ type: 'event', event, reviewable, edit });
  }

  /**
   * Where an edit sits, so the card can number the lines around it.
   *
   * A live call uses the pre-image just taken: that is the file the `old_string`
   * was found in. A replay has no pre-image — taking one now would record the
   * file as it is today under this turn — so it reads the file as it stands and
   * looks for the text the call wrote.
   */
  private async editAnchor(event: HostEvent, source: 'snapshot' | 'disk'): Promise<EditAnchor | undefined> {
    if (event.type !== 'tool_call') return undefined;
    const review = reviewOf(event.name, event.args);
    if (!review || review.kind !== 'edit') return undefined;
    const text = source === 'snapshot' ? this.changes.preImage(review.path) : await this.changes.read(review.path);
    if (text === undefined) return undefined;
    const first = source === 'snapshot' ? review.before : review.after;
    const second = source === 'snapshot' ? review.after : review.before;
    return locateEdit(text, first) ?? locateEdit(text, second);
  }

  onApproval = (request: ApprovalRequest): void => {
    // Also snapshotted here, where nothing can be racing the write: a gated
    // call is blocked until the user answers.
    void this.changes.snapshotToolCall(request.tool, request.args);
    this.post({ type: 'approval', request });
    // A tab that now needs an answer is a different window state, and the
    // status bar is the surface that has to show it even when this tab is not
    // the one in front.
    this.hub.notifyRunning();
  };

  onApprovalWithdrawn = (requestId: string): void => {
    void this.closeReview(requestId);
    this.post({ type: 'approvalWithdrawn', requestId });
    this.hub.notifyRunning();
  };

  onDone = (done: DoneParams, failure: RunFailure): void => {
    this.post({ type: 'done', done, failure });
    void this.reportChanges(done.run_id);
    void this.reportCheckpoint(done.run_id);
    // The turn changed the stored record — its run count, its status, its
    // updated_at — so the sidebar's copy of it is stale from here on.
    this.hub.notifySessions();
    this.hub.notifyRunning();
  };

  onState = (): void => {
    this.postState();
    this.hub.notifyRunning();
    this.hub.notifySessions();
  };

  /**
   * A session opened. The mode the host resolved is this tab's to show, and the
   * select is re-sent here rather than only when the user changes it: a session
   * opened under the workspace's declaration is a select that was showing the
   * wrong thing until now.
   */
  onSession = (result: SessionResult): void => {
    this.rememberAgent(result.agent_id);
    this.noteResolvedMode(result);
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
   * Offers the undo of a run, once the run has ended.
   *
   * The checkpoint is looked up by run id rather than assumed: a run that changed
   * nothing captured nothing, and a host that predates checkpoints has no list at
   * all. Either way there is no button, rather than a button that would fail.
   */
  private async reportCheckpoint(runId: string): Promise<void> {
    const agentId = this.currentAgentId();
    if (!agentId || !runId) return;
    try {
      const runs = await this.hub.listCheckpoints(agentId);
      const run = runs.find((candidate) => candidate.run_id === runId);
      if (!run || run.files.length === 0) return;
      this.post({ type: 'checkpoint', runId, files: run.files.map((file) => file.path) });
    } catch (err) {
      // A host without the method answers -32601, which is "no checkpoints here"
      // rather than a failure worth a banner.
      this.log(`could not read the checkpoint for ${runId}: ${(err as Error).message}`);
    }
  }

  /**
   * Puts a run's files back, after a modal confirmation.
   *
   * The confirmation is the point: this writes to disk, and the extension has no
   * record of the restored content to put back in turn. The card disabled itself
   * when the button was pressed, so every outcome — cancelled, counts, error —
   * is reported back to it.
   */
  private async restoreCheckpoint(runId: string): Promise<void> {
    const agentId = this.currentAgentId();
    if (!agentId) return;
    const choice = 'Restore';
    const answer = await vscode.window.showWarningMessage(
      'Restore the files this run changed?',
      {
        modal: true,
        detail:
          'Each file goes back to its content before this run, and files the run created are deleted. ' +
          'This cannot be undone from here.',
      },
      choice,
    );
    if (answer !== choice) {
      this.post({ type: 'checkpointRestored', runId, cancelled: true });
      return;
    }
    try {
      const result = await this.hub.restoreCheckpoint(agentId, runId);
      const deleted = result.files.filter((file) => file.deleted).length;
      const restored = result.files.length - deleted;
      // The next turn's changed-files card is computed against the restored
      // workspace, not against the one the last card described.
      this.changes.begin();
      this.forgetChanges();
      this.post({ type: 'checkpointRestored', runId, restored, deleted });
    } catch (err) {
      this.post({ type: 'checkpointRestored', runId, error: (err as Error).message });
    }
  }

  /**
   * Branches this conversation at a finished run, in a new tab.
   *
   * The hub opens the tab; this tab is not touched, because the point of a fork
   * is that the conversation continues in two directions from the same history.
   * The button disabled itself when pressed, so both endings are reported: a
   * branch that could not be made has to be possible to ask for again.
   */
  private async forkSession(runId: string): Promise<void> {
    const agentId = this.currentAgentId();
    if (!agentId || !runId) return;
    try {
      const newId = await this.hub.forkSession(agentId, runId);
      this.post({ type: 'forked', runId, agentId: newId });
    } catch (err) {
      this.post({ type: 'forked', runId, error: (err as Error).message });
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

  /** Re-announces where this tab's session stands. The mode travels with `postMode`. */
  private postState(): void {
    const session = this.session;
    if (!session) {
      this.post({ type: 'state', state: 'idle' });
      return;
    }
    this.post({
      type: 'state',
      state: session.currentState,
      runId: session.activeRun,
      sessionId: session.id,
      agentId: this.currentAgentId(),
      mode: this.resolvedMode?.mode,
    });
  }

  /** Stops this tab's active run. Safe when nothing is running. */
  async stop(): Promise<void> {
    await this.session?.cancel();
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
    this.post({
      type: 'mode',
      mode: this.mode ?? '',
      resolved: this.resolvedMode?.mode ?? this.mode ?? 'ask',
      source: this.resolvedMode?.source ?? 'default',
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
    await this.hub.restart();
    this.postState();
  }

  /** Whether a host child is live right now. */
  get running(): boolean {
    return this.hub.manager.running;
  }

  /**
   * The session handle was closed by a crash or a restart. Everything the dead
   * child said is still the conversation — the agent is on disk — but the
   * handle must not be used again, and the mode it resolved is no longer the
   * mode anything is running under.
   */
  hostReset(): void {
    this.session = undefined;
    this.opening = undefined;
    this.resolvedMode = undefined;
    this.postState();
    this.postMode();
  }

  async dispose(): Promise<void> {
    if (this.disposed) return;
    this.disposed = true;
    // The hub owns whether this tab is counted; it drops it from its set and
    // forgets it before the handle is closed, so a late notification finds
    // nothing rather than a half-disposed tab.
    this.hub.unregister(this);
    this.panel = undefined;
    this.listening = false;
    this.pending = [];
    this.partialReviews.dispose();
    const session = this.session;
    this.session = undefined;
    this.opening = undefined;
    for (const uris of this.reviews.values()) {
      await this.previews.close(uris).catch(() => undefined);
    }
    this.reviews.clear();
    if (session) await this.hub.manager.close(session).catch(() => undefined);
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
      <p class="hint">Mutating calls stop for your approval. The button sends, and stops a turn while one is running.</p>
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
    <div id="meter" class="meter" hidden></div>
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
          <option value="auto">Auto: safe actions</option>
          <option value="plan">Plan only</option>
          <option value="dont_ask">Only what the rules allow</option>
          <option value="bypass">Bypass approvals</option>
        </select>
        <button id="rules" type="button" class="ghost" title="Which rule files this workspace loaded" hidden>Rules</button>
        <button id="permissions" type="button" class="ghost" title="Manage permission rules: what runs without asking, what asks, and what is refused">Permissions</button>
        <span class="spacer"></span>
        <button id="attach" type="button" class="secondary" title="Reference a file or folder">@</button>
        <!--
          The picker the paste path already accepts: a button because a paste
          needs an image in the clipboard first, and a file on disk is the
          common case.
        -->
        <button id="image" type="button" class="secondary" title="Attach an image">Image</button>
        <!--
          One control for both ends of a turn: a paper plane sends, and the same
          button becomes a stop square while a turn runs. A separate Stop button
          would appear only at the moment Send stops being useful.
        -->
        <button id="send" type="button" class="primary icon" aria-label="Send" title="Send">
          <svg class="icon-send" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M22 2 11 13"/>
            <path d="m22 2-7 20-4-9-9-4 20-7z"/>
          </svg>
          <svg class="icon-stop" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" aria-hidden="true">
            <rect x="7" y="7" width="10" height="10" rx="1.5" fill="currentColor"/>
          </svg>
        </button>
      </div>
    </form>
    <!--
      The model's own plan, below the composer: it is about the work in flight,
      not about the conversation, so it does not belong in the transcript. Every
      todo event carries the whole list and replaces this block; an empty list
      hides it.
    -->
    <div id="todos" class="todos" hidden aria-label="Task list"></div>
  </div>
  <!--
    The Markdown parser first, because it is a plain script assigning a global
    rather than a module -- media/ is linted as browser code and tsc never
    copies it into out/, so there is nothing to import. chat.js reads the global
    lazily on its first render, so the order is convention rather than a
    requirement; it is written this way so a reader can see the dependency.
  -->
  <script nonce="${nonce}" src="${asset('markdown.js')}"></script>
  <script nonce="${nonce}" src="${asset('meter.js')}"></script>
  <script nonce="${nonce}" src="${asset('history.js')}"></script>
  <script nonce="${nonce}" src="${asset('chat.js')}"></script>
</body>
</html>`;
}

/** A fresh CSP nonce per render, from the platform's random source. */
function makeNonce(): string {
  return randomBytes(16).toString('base64');
}
