/**
 * One conversation: the state machine the panel renders.
 *
 * It owns a `HostClient`'s traffic for a single session id and nothing else —
 * no `vscode` import, so the whole thing runs under a Node test against the
 * real sidecar.
 */

import { LogFn, HostRpcError } from './hostClient';
import { RunFailure, classifyDone, transportFailure } from './errors';
import {
  ApprovalRequest,
  Attachment,
  CodeNoApproval,
  Decision,
  DoneParams,
  HostEvent,
  Method,
  Notify,
  PromptContext,
  PromptResult,
  SessionParams,
  SessionResult,
} from './protocol';

export type SessionState = 'idle' | 'running';

/**
 * The slice of `HostClient` a session actually uses. Typing it this way keeps
 * the state machine testable without a process: the unit tests hand in a stub
 * that answers from a table, while the extension hands in the real client.
 */
export interface Transport {
  request<T>(method: string, params?: unknown, timeoutMs?: number): Promise<T>;
  notify(method: string, params?: unknown): void;
}

export interface ChatSessionHandlers {
  onEvent?: (event: HostEvent) => void;
  onApproval?: (request: ApprovalRequest) => void;
  /** A pending prompt went away without an answer (superseded, cancelled, …). */
  onApprovalWithdrawn?: (requestId: string) => void;
  onDone?: (done: DoneParams, failure: RunFailure) => void;
  onState?: (state: SessionState, runId?: string) => void;
  /**
   * A session was opened, with the identity the host gave it. It carries the mode
   * the session actually runs in — which is not always the one this client asked
   * for: the host resolves it against the workspace's `.anvil/permissions.json`,
   * and reports what that file declared and was refused. This is the only place
   * that answer exists, so it is announced here rather than waited for.
   */
  onSession?: (result: SessionResult) => void;
  onLog?: LogFn;
}

/** Options for one session, mapped onto `session.new`. */
export type SessionConfig = Omit<SessionParams, 'agent_id'>;

/**
 * What travels with one turn beyond its text. Both are optional and both are
 * omitted from the wire when empty, so a plain question is sent exactly as it
 * was before either existed.
 */
export interface SendOptions {
  context?: PromptContext;
  attachments?: Attachment[];
}

export class ChatSession {
  private result: SessionResult | undefined;
  private runId: string | undefined;
  private state: SessionState = 'idle';
  private readonly approvals = new Map<string, ApprovalRequest>();
  /** Serialises session.new so two prompts at once cannot both create one. */
  private opening: Promise<SessionResult> | undefined;
  /**
   * The agent this conversation belongs to. It outlives the live session on
   * purpose: closing releases the process-side session, and the next open
   * *resumes* this agent rather than starting a new one, which is what makes a
   * settings change (a mode, a model, a skill) keep the conversation.
   */
  private conversation: string | undefined;

  constructor(
    private readonly client: Transport,
    private readonly cwd: string,
    private config: SessionConfig,
    private readonly handlers: ChatSessionHandlers = {},
  ) {}

  get id(): string | undefined {
    return this.result?.session_id;
  }

  get agent(): string | undefined {
    return this.result?.agent_id ?? this.conversation;
  }

  /** The options this session would open with next, for a caller that extends them. */
  get settings(): SessionConfig {
    return { ...this.config };
  }

  get activeRun(): string | undefined {
    return this.runId;
  }

  get currentState(): SessionState {
    return this.state;
  }

  /** Notifications from the host, routed by method. */
  handle(method: string, params: unknown): void {
    switch (method) {
      case Notify.Event: {
        const event = params as HostEvent;
        if (!this.isMine(event.session_id)) return;
        this.handlers.onEvent?.(event);
        return;
      }
      case Notify.ApprovalRequest: {
        const request = params as ApprovalRequest;
        if (!this.isMine(request.session_id)) return;
        this.approvals.set(request.request_id, request);
        this.handlers.onApproval?.(request);
        return;
      }
      case Notify.Done: {
        const done = params as DoneParams;
        if (!this.isMine(done.session_id)) return;
        this.setState('idle');
        this.runId = undefined;
        // A run cannot end while its approvals are still open; the host denies
        // them itself and tells the model why. Drop them so a stale prompt is
        // not left on screen.
        for (const id of [...this.approvals.keys()]) {
          this.approvals.delete(id);
          this.handlers.onApprovalWithdrawn?.(id);
        }
        this.handlers.onDone?.(done, classifyDone(done));
        return;
      }
      case Notify.Log: {
        const log = params as { level: string; message: string };
        this.handlers.onLog?.(`host ${log.level}: ${log.message}`);
        return;
      }
      default:
        return;
    }
  }

  /** A message belongs to this session when it names ours, or none at all. */
  private isMine(sessionId: string | undefined): boolean {
    if (!this.result) return false;
    return !sessionId || sessionId === this.result.session_id;
  }

  /**
   * Opens the conversation, or returns the one already open.
   *
   * Which method it uses is the difference the whole resume story turns on:
   * with an agent already behind this session, the same conversation is
   * re-opened under the current options (`session.resume`), so the history and
   * the agent id survive a mode, model, or skill change.
   */
  async open(): Promise<SessionResult> {
    if (this.result) return this.result;
    if (this.opening) return this.opening;
    const params: Record<string, unknown> = this.conversation
      ? { agent_id: this.conversation, ...this.config }
      : { ...this.config };
    const method = this.conversation ? Method.SessionResume : Method.SessionNew;
    this.opening = this.client
      .request<SessionResult>(method, params)
      .then((res) => {
        this.result = res;
        this.conversation = res.agent_id;
        this.opening = undefined;
        this.handlers.onLog?.(
          `session ${res.session_id} in ${this.cwd} (${res.provider}/${res.model}, ${res.permission_mode} from ${res.permission_mode_source})`,
        );
        this.handlers.onSession?.(res);
        return res;
      })
      .catch((err: unknown) => {
        this.opening = undefined;
        throw err;
      });
    return this.opening;
  }

  /**
   * Applies new options to this conversation.
   *
   * A mode, a model, or a skill is fixed when the SDK builds its options, so
   * changing one means re-opening the session — under the same agent, which is
   * why the history comes with it. Nothing is opened when nothing is live yet:
   * a change made before the first prompt is simply the config the first
   * `open` will use, and creating a session to apply it would spawn work the
   * user has not asked for.
   */
  async reopen(changes: Partial<SessionConfig>): Promise<SessionResult | undefined> {
    if (this.state === 'running') {
      throw new Error('the session cannot change while a turn is running');
    }
    this.config = { ...this.config, ...changes };
    if (!this.result) return undefined;
    const conversation = this.conversation;
    await this.close();
    this.conversation = conversation;
    return this.open();
  }

  /**
   * Adopts a stored agent as this conversation. The previous session is closed
   * first, so nothing is left running on the host for a conversation that has
   * moved on.
   */
  async continueWith(agentId: string): Promise<SessionResult> {
    if (this.state === 'running') {
      throw new Error('a turn is running; stop it before opening another session');
    }
    await this.close();
    this.conversation = agentId;
    return this.open();
  }

  /** Drops the conversation, so the next open starts a new agent. */
  async reset(): Promise<void> {
    await this.close();
    this.conversation = undefined;
  }

  /** Opens a session if needed, then sends one turn. */
  async send(text: string, options: SendOptions = {}): Promise<PromptResult> {
    const message = text.trim();
    if (message === '') throw new Error('prompt is empty');
    if (this.state === 'running') {
      throw new Error(`a turn is already running (${this.runId ?? 'unknown run'})`);
    }
    const session = await this.open();
    const params: Record<string, unknown> = {
      session_id: session.session_id,
      message,
    };
    if (options.context && Object.keys(options.context).length > 0) {
      params.context = options.context;
    }
    if (options.attachments && options.attachments.length > 0) {
      params.attachments = options.attachments;
    }
    const result = await this.client.request<PromptResult>(Method.Prompt, params);
    this.runId = result.run_id;
    this.setState('running', result.run_id);
    return result;
  }

  /** Stops the active run. A no-op when nothing is running. */
  async cancel(): Promise<void> {
    if (!this.result || !this.runId) return;
    await this.client.request(Method.Cancel, { session_id: this.result.session_id, run_id: this.runId });
  }

  /**
   * Answers a pending approval. Returns false when the host no longer has it
   * pending — the user answered a prompt that a cancel already released.
   */
  async respond(
    requestId: string,
    decision: Decision,
    options: { scope?: string; args?: unknown; message?: string } = {},
  ): Promise<boolean> {
    this.approvals.delete(requestId);
    try {
      await this.client.request(Method.ApprovalResponse, {
        request_id: requestId,
        decision,
        ...options,
      });
      return true;
    } catch (err) {
      if (err instanceof HostRpcError && err.code === CodeNoApproval) {
        this.handlers.onLog?.(`approval ${requestId} was no longer pending`);
        return false;
      }
      throw err;
    }
  }

  pendingApprovals(): ApprovalRequest[] {
    return [...this.approvals.values()];
  }

  /**
   * Closes the live session on the host. The client stays up for the next one,
   * and `conversation` is deliberately kept: the next open resumes this agent,
   * so closing to change an option does not throw the conversation away.
   */
  async close(): Promise<void> {
    const id = this.result?.session_id;
    this.result = undefined;
    this.runId = undefined;
    for (const requestId of [...this.approvals.keys()]) {
      this.approvals.delete(requestId);
      this.handlers.onApprovalWithdrawn?.(requestId);
    }
    this.setState('idle');
    if (!id) return;
    try {
      await this.client.request(Method.SessionClose, { session_id: id });
    } catch (err) {
      this.handlers.onLog?.(`session.close failed: ${(err as Error).message}`);
    }
  }

  /**
   * Reports a lost transport as a finished run, but only when a run was
   * actually in flight — otherwise there is nothing to end, and the panel would
   * show a failure the user never asked for. Returns whether it reported one.
   */
  failIfRunning(reason: string): boolean {
    if (this.state !== 'running') return false;
    this.fail(new Error(reason));
    return true;
  }

  /** Reports a transport failure as a finished run, so the UI has an ending. */
  fail(err: unknown): void {
    const failure = transportFailure(err);
    this.setState('idle');
    this.runId = undefined;
    for (const id of [...this.approvals.keys()]) {
      this.approvals.delete(id);
      this.handlers.onApprovalWithdrawn?.(id);
    }
    this.handlers.onDone?.(
      {
        session_id: this.result?.session_id ?? '',
        run_id: '',
        status: 'error',
        err: failure.message,
      },
      failure,
    );
  }

  private setState(state: SessionState, runId?: string): void {
    if (this.state === state && runId === undefined) return;
    this.state = state;
    this.handlers.onState?.(state, runId);
  }
}
