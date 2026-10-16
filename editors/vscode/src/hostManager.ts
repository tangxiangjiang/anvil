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
import { ChatSession, SendOptions, SessionConfig, SessionState } from './session';
import { childEnv, readApiKey, readSettings } from './settings';
import { describeHost, hostFixHint, resolveHost, verifyHost, versionDrift } from './hostBundle';
import {
  ApprovalRequest,
  Decision,
  DoneParams,
  HostEvent,
  Method,
  ModelEntry,
  PermissionMode,
  RuleFileInfo,
  SessionInfo,
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
  onNotice?: (level: 'info' | 'warn' | 'error', message: string) => void;
}

export class HostManager {
  private client: HostClient | undefined;
  private session: ChatSession | undefined;
  private starting: Promise<ChatSession> | undefined;
  private startingClient: Promise<HostClient> | undefined;
  private cwd: string | undefined;
  private disposed = false;
  /** Set once a crash has been reported, so the panel is told only once. */
  private crashed = false;
  /**
   * Session options the user has changed since the host came up — the mode, the
   * model, the skills. They are layered over the settings at every session this
   * manager opens, so they survive a host restart as well as a re-open.
   */
  private overrides: Partial<SessionConfig> = {};

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly log: LogFn,
    private readonly listener: HostListener,
  ) {}

  /** Starts the host and opens a session if it is not already up. */
  async ensure(): Promise<ChatSession> {
    if (this.session) return this.session;
    if (this.starting) return this.starting;
    this.starting = this.start().finally(() => {
      this.starting = undefined;
    });
    return this.starting;
  }

  private async start(): Promise<ChatSession> {
    const client = await this.startClient();
    if (this.session) return this.session;
    const settings = readSettings();
    // The session needs the client to exist, and the client needs the session's
    // router: the handler goes on after both are built.
    const session = new ChatSession(
      client,
      this.cwd ?? '',
      { ...settings.session, ...this.overrides },
      {
        onEvent: this.listener.onEvent,
        onApproval: this.listener.onApproval,
        onApprovalWithdrawn: this.listener.onApprovalWithdrawn,
        onDone: this.listener.onDone,
        onState: this.listener.onState,
        onLog: this.log,
      },
    );
    client.setNotificationHandler((method, params) => session.handle(method, params));
    this.session = session;
    this.crashed = false;
    return session;
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
    const apiKey = await readApiKey(this.context);
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
      env: childEnv(apiKey),
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
    return client;
  }

  private onHostExit(info: { code: number | null; signal: NodeJS.Signals | null; unexpected: boolean }): void {
    if (this.crashed || this.disposed) return;
    this.crashed = true;
    const session = this.session;
    this.client = undefined;
    this.session = undefined;
    this.log(`host exited (code ${info.code ?? 'none'}${info.signal ? `, signal ${info.signal}` : ''})`);

    // A run that was in flight will never reach its `done`: the child that owed
    // it is gone. Reporting it here is what unlocks the panel — without it the
    // composer stays disabled and the turn looks hung forever.
    session?.failIfRunning('The Anvil host stopped while this turn was running.');
    if (info.unexpected) {
      this.listener.onNotice?.('error', 'The Anvil host stopped. Send another message to restart it.');
    }
  }

  /** Sends a turn, starting the host if needed. */
  async send(text: string, options: SendOptions = {}): Promise<void> {
    try {
      const session = await this.ensure();
      await session.send(text, options);
    } catch (err) {
      this.listener.onNotice?.('error', (err as Error).message);
      throw err;
    }
  }

  /** Stops the active run. */
  async cancel(): Promise<void> {
    const session = this.session;
    if (!session) return;
    try {
      await session.cancel();
    } catch (err) {
      this.listener.onNotice?.('error', (err as Error).message);
    }
  }

  /** Answers an approval. */
  async respond(
    requestId: string,
    decision: Decision,
    options: { scope?: string } = {},
  ): Promise<void> {
    const session = this.session;
    if (!session) return;
    try {
      await session.respond(requestId, decision, options);
    } catch (err) {
      this.listener.onNotice?.('error', (err as Error).message);
    }
  }

  /** Replaces the child: settings may have changed, or it may have wedged. */
  async restart(): Promise<void> {
    const session = this.session;
    const client = this.client;
    this.session = undefined;
    this.client = undefined;
    if (session) await session.close().catch(() => undefined);
    if (client) await client.stop();
    this.crashed = false;
    this.listener.onNotice?.('info', 'The Anvil host was restarted.');
  }

  /** True when a child is live. */
  get running(): boolean {
    return this.session !== undefined;
  }

  // --- catalogue --------------------------------------------------------------

  /**
   * Applies session options the user chose. When a session is already live the
   * conversation is re-opened under them — same agent, so the history stays.
   * When none is, the change waits for the first prompt rather than creating a
   * session to hold it.
   */
  async applyOptions(changes: Partial<SessionConfig>): Promise<boolean> {
    const previous = this.overrides;
    this.overrides = { ...this.overrides, ...changes };
    const session = this.session;
    if (!session) return false;
    try {
      await session.reopen(changes);
      return true;
    } catch (err) {
      // Put the change back: a refusal has to be a refusal everywhere, or the
      // next session would quietly open with an option the user was told had
      // not been applied.
      this.overrides = previous;
      this.listener.onNotice?.('error', (err as Error).message);
      return false;
    }
  }

  /** Adds a skill to the conversation, keeping the ones already selected. */
  async useSkill(id: string): Promise<boolean> {
    const session = this.session;
    const existing = session?.settings.skills ?? this.overrides.skills ?? [];
    if (existing.includes(id)) {
      this.listener.onNotice?.('info', `The ${id} skill is already on for this conversation.`);
      return false;
    }
    return this.applyOptions({ skills: [...existing, id] });
  }

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

  /** Past agents stored under the workspace root. */
  async listSessions(): Promise<SessionInfo[]> {
    const client = await this.startClient();
    const result = await client.request<{ sessions: SessionInfo[] }>(Method.SessionList, {});
    return result.sessions ?? [];
  }

  /** The provider's model catalogue, for the /model picker. */
  async listModels(): Promise<ModelEntry[]> {
    const client = await this.startClient();
    const settings = readSettings();
    const result = await client.request<{ models: ModelEntry[] }>(Method.ModelsList, {
      provider: this.overrides.provider ?? settings.session.provider,
      base_url: this.overrides.base_url ?? settings.session.base_url,
    });
    return result.models ?? [];
  }

  /** Continues a stored agent as the current conversation. */
  async resumeSession(agentId: string): Promise<void> {
    const session = await this.ensure();
    await session.continueWith(agentId);
  }

  /** Ends the current conversation; the next prompt starts a new agent. */
  async clearConversation(): Promise<void> {
    const session = this.session;
    if (session) await session.reset();
  }

  /** The panel's whole view of where things stand, without reaching inside. */
  state(): {
    state: SessionState;
    runId?: string;
    sessionId?: string;
    agentId?: string;
    mode: PermissionMode;
  } {
    const session = this.session;
    const mode = this.overrides.permission_mode ?? 'ask';
    if (!session) return { state: 'idle', mode };
    return {
      state: session.currentState,
      runId: session.activeRun,
      sessionId: session.id,
      agentId: session.agent,
      mode,
    };
  }

  /** Prompts still waiting for an answer. */
  pendingApprovals(): ApprovalRequest[] {
    return this.session?.pendingApprovals() ?? [];
  }

  async dispose(): Promise<void> {
    this.disposed = true;
    const session = this.session;
    const client = this.client;
    this.session = undefined;
    this.client = undefined;
    // Close the session first so the host can stop its run cleanly, then ask
    // the process to end. The child is killed either way.
    if (session) await session.close().catch(() => undefined);
    if (client) await client.stop();
  }

  /** The trusted-workspace gate, in one place. */
  static assertTrusted(): void {
    if (!vscode.workspace.isTrusted) {
      throw new Error('Anvil refuses to run in an untrusted workspace. Trust this folder and try again.');
    }
  }
}
