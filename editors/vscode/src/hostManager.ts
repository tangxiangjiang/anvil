/**
 * The host child's lifecycle for one window.
 *
 * One sidecar per window (see the design chapter): it is spawned lazily on the
 * first prompt, restarted after a crash, and killed on deactivate. Everything
 * that talks to it goes through here, so no other module has to know whether
 * it is currently up.
 */

import * as vscode from 'vscode';
import { HostClient, LogFn } from './hostClient';
import { ChatSession, SessionConfig, SessionState } from './session';
import { SessionRouter } from './router';
import { childEnv, readApiKey, readSettings, readWebSearchKey } from './settings';
import { describeHost, hostFixHint, resolveHost, verifyHost, versionDrift } from './hostBundle';
import {
  ApprovalRequest,
  CheckpointListParams,
  CheckpointListResult,
  CheckpointRestoreParams,
  CheckpointRestoreResult,
  DoneParams,
  HostEvent,
  McpListResult,
  Method,
  ModelEntry,
  PermissionMode,
  PermissionModeSource,
  PermissionsAddParams,
  PermissionsAddResult,
  PermissionsResult,
  PermissionsSetParams,
  RuleFileInfo,
  RuleSpec,
  SessionDeleteResult,
  SessionForkResult,
  SessionInfo,
  SessionRenameResult,
  SessionResult,
  SessionTranscriptParams,
  SessionTranscriptResult,
  SkillEntry,
} from './protocol';
import { RunFailure } from './errors';

/** What the panel needs to hear about. */
export interface HostListener {
  onEvent?: (event: HostEvent) => void;
  onApproval?: (request: ApprovalRequest) => void;
  onApprovalWithdrawn?: (requestId: string) => void;
  onDone?: (done: DoneParams, failure: RunFailure) => void;
  onState?: (state: SessionState, runId?: string) => void;
  /** A session opened, with the mode the host resolved for it. */
  onSession?: (result: SessionResult) => void;
  onNotice?: (level: 'info' | 'warn' | 'error', message: string) => void;
}

/**
 * The mode a live session is actually running under, and where it came from. It
 * is not the same thing as the choice this client sent: the host resolves that
 * against the workspace's `.anvil/permissions.json`, and a declaration it will
 * not honour comes back refused rather than obeyed.
 */
export interface ResolvedMode {
  mode: PermissionMode;
  source: PermissionModeSource;
  /** What the workspace declared and the host would not honour, in words. */
  refused?: string;
}

export class HostManager {
  private client: HostClient | undefined;
  /**
   * Every live conversation, by the host's session id.
   *
   * One process serves them all: a notification names the session it belongs
   * to, so a second tab costs a session and not a child. The router is the
   * rule; this class only hands it the notifications.
   */
  private readonly router = new SessionRouter<ChatSession>();
  private startingClient: Promise<HostClient> | undefined;
  private cwd: string | undefined;
  private disposed = false;
  /** Set once a crash has been reported, so panels are told only once. */
  private crashed = false;
  /**
   * Fires after a crash or a restart. Every panel holds a session handle that
   * belonged to the dead child, and a handle is not reborn with the process:
   * the panel drops it and opens a fresh one on its next prompt.
   */
  private readonly resetEmitter = new vscode.EventEmitter<void>();
  readonly onReset = this.resetEmitter.event;

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly log: LogFn,
    private readonly listener: HostListener,
  ) {}

  /**
   * Opens one conversation on the shared child.
   *
   * The listener is the panel's, not the window's: every event, approval and
   * done notification is routed to exactly one caller. The session is
   * registered under the id the host answers with, which is why constructing it
   * here does not open it — `ChatSession.open` is what reaches the host, and a
   * tab that is restored before its first prompt costs nothing.
   */
  async open(config: SessionConfig, listener: HostListener): Promise<ChatSession> {
    const client = await this.startClient();
    const session = new ChatSession(client, this.cwd ?? '', config, {
      onEvent: listener.onEvent,
      onApproval: listener.onApproval,
      onApprovalWithdrawn: listener.onApprovalWithdrawn,
      onDone: listener.onDone,
      onState: listener.onState,
      onSession: (result) => {
        this.router.register(result.session_id, session);
        listener.onSession?.(result);
      },
      onLog: this.log,
    });
    return session;
  }

  /** Sends one notification to the session it names. */
  private route(method: string, params: unknown): void {
    this.router.route(method, params, this.log);
  }

  /**
   * Closes a session and takes it out of the router.
   *
   * Both halves are one operation: a session the host has closed cannot answer
   * anything, and a router entry for it would only log a drop for every late
   * notification that arrived in the gap.
   */
  async close(session: ChatSession): Promise<void> {
    this.router.unregister(session);
    await session.close();
  }

  /** The live sessions, for a caller that has to tell all of them something. */
  liveSessions(): ChatSession[] {
    return this.router.sessions();
  }

  /**
   * Spawns the child and completes the handshake, without opening a session.
   *
   * The catalogue calls — `session.list`, `skills.list`, `rules.list`,
   * `models.list` — need a live host but not a conversation, and asking for a
   * picker's contents should not open an agent.
   */
  private async startClient(): Promise<HostClient> {
    if (this.client) return this.client;
    if (this.startingClient) return this.startingClient;
    this.startingClient = this.spawn().finally(() => {
      this.startingClient = undefined;
    });
    return this.startingClient;
  }

  private async spawn(): Promise<HostClient> {
    if (this.disposed) throw new Error('the Anvil host manager is disposed');
    // The gate lives here as well as in the panel's message handler: this is
    // the only path that spawns a process, and a UI check is not an enforcement
    // point. A workspace trust decision is exactly the kind of thing that
    // should hold even if a caller forgets to ask.
    HostManager.assertTrusted();
    const settings = readSettings();
    // Both credentials are read before the spawn and only ever travel through
    // the child environment.
    const [apiKey, webSearchKey] = await Promise.all([
      readApiKey(this.context),
      readWebSearchKey(this.context),
    ]);
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) throw new Error('open a folder to use Anvil');
    const cwd = folder.uri.fsPath;
    this.cwd = cwd;

    const host = resolveHost({
      extensionPath: this.context.extensionUri.fsPath,
      configured: settings.hostPath,
      workspaceRoot: cwd,
    });
    // The binary is checked against what the build recorded before it is run,
    // and only when it is the one we shipped: a user's own build has no
    // manifest to compare with, and pretending otherwise would only break their
    // loop. Refusing to start is the point — a host that is not the one we
    // packaged is not a configuration to work around silently.
    const refusal = verifyHost(host);
    if (refusal) throw new Error(refusal);
    this.log(`host ${describeHost(host)}`);

    const client = new HostClient({
      command: host.command,
      cwd,
      env: childEnv({ apiKey, webSearchKey }),
      log: this.log,
      onExit: (info) => this.onHostExit(info),
    });

    try {
      await client.start();
    } catch (err) {
      // The caller reports this: the panel owns the user-facing message. What
      // is added here is the fix, which the spawn error alone does not name —
      // and the fix depends on where we looked.
      throw new Error(
        `could not start the Anvil host: ${(err as Error).message} (${hostFixHint(host)})`,
      );
    }
    try {
      const version = this.context.extension.packageJSON.version as string;
      const handshake = await client.initialize(cwd, { name: 'anvil-vscode', version });
      this.log(`host ${handshake.host} (capabilities: ${handshake.capabilities.join(', ') || 'none'})`);
      const drift = versionDrift(handshake.host, version);
      if (drift) this.listener.onNotice?.('warn', drift);
    } catch (err) {
      await client.stop();
      this.listener.onNotice?.('error', `the host did not answer initialize: ${(err as Error).message}`);
      throw err;
    }

    this.client = client;
    // One router for the whole window. It is installed here rather than per
    // session because sessions come and go while the connection stays.
    client.setNotificationHandler((method, params) => this.route(method, params));
    this.crashed = false;
    return client;
  }

  private onHostExit(info: { code: number | null; signal: NodeJS.Signals | null; unexpected: boolean }): void {
    if (this.crashed || this.disposed) return;
    this.crashed = true;
    const sessions = this.router.clear();
    this.client = undefined;
    this.log(`host exited (code ${info.code ?? 'none'}${info.signal ? `, signal ${info.signal}` : ''})`);

    // A run that was in flight will never reach its `done`: the child that owed
    // it is gone. Reporting it here is what unlocks each panel — without it the
    // composer stays disabled and the turn looks hung forever.
    for (const session of sessions) {
      session.failIfRunning('The Anvil host stopped while this turn was running.');
    }
    this.resetEmitter.fire();
    if (info.unexpected) {
      this.listener.onNotice?.('error', 'The Anvil host stopped. Send another message to restart it.');
    }
  }

  /**
   * Replaces the child: settings may have changed, or it may have wedged.
   *
   * Every session belonged to the process that is going away, so all of them are
   * closed first and every panel is told to drop its handle. The conversations
   * themselves are on disk; the next prompt resumes them.
   */
  async restart(): Promise<void> {
    const sessions = this.router.clear();
    const client = this.client;
    this.client = undefined;
    for (const session of sessions) await session.close().catch(() => undefined);
    if (client) await client.stop();
    this.crashed = false;
    this.resetEmitter.fire();
    this.listener.onNotice?.('info', 'The Anvil host was restarted.');
  }

  /** True when a child is live. */
  get running(): boolean {
    return this.client !== undefined;
  }

  // --- catalogue --------------------------------------------------------------

  /** The skill packages this workspace offers. */
  async skills(): Promise<SkillEntry[]> {
    const client = await this.startClient();
    const result = await client.request<{ skills: SkillEntry[] }>(Method.SkillsList, {});
    return result.skills ?? [];
  }

  /**
   * The rule files that apply to this workspace, reported for the layers the
   * session was configured with. "Why is the model ignoring my AGENTS.md" is
   * otherwise invisible from the client side.
   */
  async rules(): Promise<RuleFileInfo[]> {
    const client = await this.startClient();
    const result = await client.request<{ files: RuleFileInfo[] }>(Method.RulesList, readSettings().rules);
    return result.files ?? [];
  }

  /**
   * The MCP servers the enabled file layers declare.
   *
   * Configuration, not connection: the host reports which files were read and
   * what they name, and carries no state for a server that failed to start.
   * The sources are the caller's — the panel passes the same list it put on the
   * session, so the card cannot describe a layer the conversation does not load.
   */
  async mcpServers(settingSources: string[]): Promise<McpListResult> {
    const client = await this.startClient();
    return client.request<McpListResult>(Method.McpList, { setting_sources: settingSources });
  }

  /** Past agents stored under the workspace root. */
  async listSessions(): Promise<SessionInfo[]> {
    const client = await this.startClient();
    const result = await client.request<{ sessions: SessionInfo[] }>(Method.SessionList, {});
    return result.sessions ?? [];
  }

  /**
   * The stored conversation for one agent, oldest first.
   *
   * Keyed by agent id, so it works with no session open — which is what a
   * reloaded window needs. A host that predates the method rejects with
   * `-32601`; the caller treats that as "no replay available" rather than as a
   * failure, because an old host beside a new extension is a supported pair.
   */
  async transcript(agentId: string, limit?: number): Promise<SessionTranscriptResult> {
    const client = await this.startClient();
    const params: SessionTranscriptParams = { agent_id: agentId };
    if (limit !== undefined) params.limit = limit;
    return client.request<SessionTranscriptResult>(Method.SessionTranscript, params);
  }

  /**
   * Gives a stored session a name of its own.
   *
   * It is an append, not an edit: nothing already in the file is rewritten, so a
   * resume reads the same history it did before. The title in the listing is the
   * last one written, which is what makes a second rename replace the first.
   */
  async renameSession(agentId: string, title: string): Promise<SessionRenameResult> {
    const client = await this.startClient();
    return client.request<SessionRenameResult>(Method.SessionRename, { agent_id: agentId, title });
  }

  /**
   * Forgets a stored session.
   *
   * A live session holding the same agent is not closed here — the caller does
   * that, because it is the tab that would write the file back on its next
   * prompt. Deleting an agent that is not there rejects with a host error rather
   * than succeeding, so a stale sidebar row reports that it was already gone.
   */
  async deleteSession(agentId: string): Promise<SessionDeleteResult> {
    const client = await this.startClient();
    return client.request<SessionDeleteResult>(Method.SessionDelete, { agent_id: agentId });
  }

  /**
   * Branches a stored conversation at a finished run.
   *
   * The host copies the records and answers the new agent id; it does not open
   * it, because the client is what turns an id into a tab. A run id that never
   * ended is rejected rather than approximated — the server has no boundary to
   * copy to, and a half-run fork would resume from a turn that never finished.
   */
  async forkSession(agentId: string, runId: string): Promise<SessionForkResult> {
    const client = await this.startClient();
    return client.request<SessionForkResult>(Method.SessionFork, { agent_id: agentId, run_id: runId });
  }

  /**
   * The runs of a session that captured a pre-image, newest first.
   *
   * An empty list is the normal answer for a session whose runs changed nothing
   * and for one that ran before checkpoints existed. It is keyed by agent id, so
   * it answers for a session this host has not opened — a tab that has just come
   * back can offer the undo of the run that broke something.
   */
  async listCheckpoints(agentId: string): Promise<CheckpointListResult> {
    const client = await this.startClient();
    const params: CheckpointListParams = { agent_id: agentId };
    return client.request<CheckpointListResult>(Method.CheckpointList, params);
  }

  /**
   * Puts one run's captured files back, and deletes the files that run created.
   *
   * The result is what was written or deleted, not what was asked for: a path
   * the host refused is not in it. The SDK runs every path through the workspace
   * sandbox, so this cannot be used to reach outside the workspace.
   */
  async restoreCheckpoint(agentId: string, runId: string): Promise<CheckpointRestoreResult> {
    const client = await this.startClient();
    const params: CheckpointRestoreParams = { agent_id: agentId, run_id: runId };
    return client.request<CheckpointRestoreResult>(Method.CheckpointRestore, params);
  }

  /**
   * The workspace rule file's state: where it is, what it says, and whether it
   * can be read at all.
   *
   * A file that will not compile comes back as a result with its `problem`, not
   * as a rejection — a file somebody is halfway through editing is a state the
   * rules are in, and the client asked about the file, not about a session
   * built from it.
   */
  async permissionRules(): Promise<PermissionsResult> {
    const client = await this.startClient();
    return client.request<PermissionsResult>(Method.PermissionsGet, {});
  }

  /**
   * Appends one rule to the workspace rule file.
   *
   * `requestId`, when given, names the pending approval the rule is being
   * written for: the host then answers that approval itself when the file
   * decides the call, so "always allow" takes effect on the call in front of
   * the user rather than on the next one. A file that will not compile is
   * refused here, and the message names the file and the line.
   */
  async addPermissionRule(rule: RuleSpec, requestId?: string): Promise<PermissionsAddResult> {
    const client = await this.startClient();
    const params: PermissionsAddParams = { rule };
    if (requestId !== undefined && requestId !== '') params.request_id = requestId;
    return client.request<PermissionsAddResult>(Method.PermissionsAdd, params);
  }

  /**
   * Replaces the workspace rule file with the list a manager showed a person.
   *
   * The answer is `permissions.get`'s result, not an echo: the panel renders what
   * a session will read, including a declaration the host re-read differently. A
   * file this build cannot parse is refused and left untouched, and the message
   * names the line — the manager shows it rather than writing over the one thing
   * the user was told to go and fix.
   */
  async savePermissionRules(params: PermissionsSetParams): Promise<PermissionsResult> {
    const client = await this.startClient();
    return client.request<PermissionsResult>(Method.PermissionsSet, params);
  }

  /**
   * The provider's model catalogue, for the /model picker.
   *
   * The provider can be overridden per tab, so the caller passes the tab's
   * choice: a conversation pointed at one endpoint should not list another
   * endpoint's models just because this window happens to share one child.
   */
  async listModels(options: { provider?: string; base_url?: string } = {}): Promise<ModelEntry[]> {
    const client = await this.startClient();
    const settings = readSettings();
    const result = await client.request<{ models: ModelEntry[] }>(Method.ModelsList, {
      provider: options.provider ?? settings.session.provider,
      base_url: options.base_url ?? settings.session.base_url,
    });
    return result.models ?? [];
  }

  async dispose(): Promise<void> {
    this.disposed = true;
    const sessions = this.router.clear();
    const client = this.client;
    this.client = undefined;
    // Close the sessions first so the host can stop their runs cleanly, then
    // ask the process to end. The child is killed either way.
    for (const session of sessions) await session.close().catch(() => undefined);
    if (client) await client.stop();
    this.resetEmitter.dispose();
  }

  /** The trusted-workspace gate, in one place. */
  static assertTrusted(): void {
    if (!vscode.workspace.isTrusted) {
      throw new Error('Anvil refuses to run in an untrusted workspace. Trust this folder and try again.');
    }
  }
}
