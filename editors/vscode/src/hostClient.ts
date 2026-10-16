/**
 * The extension's side of the `anvil-host` stdio protocol: spawn the sidecar,
 * frame lines, correlate replies, and dispatch notifications.
 *
 * This module deliberately imports nothing from `vscode`, so it can be driven
 * by a Node test against a fake or real host process. Everything editor-shaped
 * lives in extension.ts / chatPanel.ts.
 */

import { ChildProcess, spawn } from 'child_process';
import { InboundMessage, PROTOCOL_VERSION, RpcError } from './protocol';

/** Log sink. The extension passes the output channel; tests capture it. */
export type LogFn = (line: string) => void;

export interface HostClientOptions {
  /** The sidecar binary. A bare name is looked up on PATH. */
  command: string;
  args?: string[];
  /** Working directory for the child, which fixes the workspace root. */
  cwd: string;
  /** Child environment. Carries the API key; never logged. */
  env: NodeJS.ProcessEnv;
  onNotification?: (method: string, params: unknown) => void;
  /** Called exactly once, when the child ends. `unexpected` is false for a
   *  shutdown we asked for. */
  onExit?: (info: { code: number | null; signal: NodeJS.Signals | null; unexpected: boolean }) => void;
  log?: LogFn;
  /** How long a request may wait for its reply. */
  requestTimeoutMs?: number;
}

/** A reply carrying a JSON-RPC error. */
export class HostRpcError extends Error {
  readonly code: number;
  constructor(err: RpcError) {
    super(err.message || `host error ${err.code}`);
    this.name = 'HostRpcError';
    this.code = err.code;
  }
}

/** The child ended, or never started, while a request was in flight. */
export class HostDownError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'HostDownError';
  }
}

/** The host did not answer in time. The request may still run to completion. */
export class HostTimeoutError extends Error {
  constructor(method: string, ms: number) {
    super(`${method} did not answer within ${ms}ms`);
    this.name = 'HostTimeoutError';
  }
}

/**
 * Splits a byte stream into lines. Exported because it is the piece most worth
 * testing directly: a JSON line can arrive in pieces, several at a time, or
 * with CRLF endings (the Go writer emits LF, but a Windows pipe can surprise).
 */
export class LineFramer {
  private buf = '';

  /** Feeds one chunk and returns whatever complete lines it completed. */
  push(chunk: string): string[] {
    this.buf += chunk;
    const lines: string[] = [];
    for (;;) {
      const nl = this.buf.indexOf('\n');
      if (nl < 0) break;
      const line = this.buf.slice(0, nl).replace(/\r$/, '');
      this.buf = this.buf.slice(nl + 1);
      if (line.trim().length > 0) lines.push(line);
    }
    return lines;
  }
}

interface Pending {
  method: string;
  resolve: (value: unknown) => void;
  reject: (err: Error) => void;
  timer?: NodeJS.Timeout;
}

/** One running host child. */
export class HostClient {
  private child: ChildProcess | undefined;
  private readonly framer = new LineFramer();
  private readonly pending = new Map<number, Pending>();
  private nextId = 1;
  private stopping = false;
  private exited = false;

  private notificationHandler: ((method: string, params: unknown) => void) | undefined;

  constructor(private readonly options: HostClientOptions) {
    this.notificationHandler = options.onNotification;
  }

  /**
   * Installs the notification sink. The session router is the only real user:
   * it needs the client to exist before it can be handed one, so the wiring is
   * a setter rather than a constructor argument.
   */
  setNotificationHandler(handler: (method: string, params: unknown) => void): void {
    this.notificationHandler = handler;
  }

  /** True while the child is alive. */
  get running(): boolean {
    return this.child !== undefined && !this.exited;
  }

  /** The child process, for lifetime assertions in tests. */
  get process(): ChildProcess | undefined {
    return this.child;
  }

  /**
   * Spawns the sidecar. The returned promise rejects when the process cannot be
   * started at all (a missing binary is the common case), not when it exits.
   */
  start(): Promise<void> {
    if (this.running) return Promise.resolve();
    const { command, args = [], cwd, env } = this.options;
    this.stopping = false;
    this.exited = false;

    return new Promise<void>((resolve, reject) => {
      let settled = false;
      const child = spawn(command, args, { cwd, env, stdio: ['pipe', 'pipe', 'pipe'] });
      this.child = child;

      child.on('error', (err) => {
        this.exited = true;
        const wrapped = new HostDownError(`could not start ${command}: ${err.message}`);
        this.failAll(wrapped);
        if (!settled) {
          settled = true;
          reject(wrapped);
          return;
        }
        this.options.onExit?.({ code: null, signal: null, unexpected: true });
      });

      child.on('spawn', () => {
        if (!settled) {
          settled = true;
          resolve();
        }
      });

      child.stdout?.setEncoding('utf8');
      child.stdout?.on('data', (chunk: string) => this.onChunk(chunk));

      child.stderr?.setEncoding('utf8');
      child.stderr?.on('data', (chunk: string) => {
        // Diagnostics only ever arrive here; stdout is the protocol.
        for (const line of chunk.split('\n')) {
          if (line.trim()) this.options.log?.(`host: ${line.trimEnd()}`);
        }
      });

      child.on('exit', (code, signal) => {
        this.exited = true;
        const unexpected = !this.stopping;
        this.failAll(new HostDownError(`the host exited (code ${code ?? 'none'}${signal ? `, signal ${signal}` : ''})`));
        this.options.onExit?.({ code, signal, unexpected });
        if (!settled) {
          settled = true;
          reject(new HostDownError('the host exited before it started'));
        }
      });
    });
  }

  private onChunk(chunk: string): void {
    for (const line of this.framer.push(chunk)) {
      let msg: InboundMessage;
      try {
        msg = JSON.parse(line) as InboundMessage;
      } catch {
        this.options.log?.(`host: unparseable line (${line.length} chars)`);
        continue;
      }
      this.dispatch(msg);
    }
  }

  private dispatch(msg: InboundMessage): void {
    if (typeof msg.id === 'number') {
      const entry = this.pending.get(msg.id);
      if (!entry) return;
      this.pending.delete(msg.id);
      if (entry.timer) clearTimeout(entry.timer);
      if (msg.error) entry.reject(new HostRpcError(msg.error));
      else entry.resolve(msg.result);
      return;
    }
    if (typeof msg.method === 'string') {
      this.notificationHandler?.(msg.method, msg.params);
    }
  }

  /** Sends a request and resolves with its result. */
  request<T>(method: string, params?: unknown, timeoutMs?: number): Promise<T> {
    const child = this.child;
    if (!child || this.exited) {
      return Promise.reject(new HostDownError(`${method}: the host is not running`));
    }
    const id = this.nextId++;
    const ms = timeoutMs ?? this.options.requestTimeoutMs ?? 30_000;
    return new Promise<T>((resolve, reject) => {
      const entry: Pending = {
        method,
        resolve: resolve as (value: unknown) => void,
        reject,
      };
      if (ms > 0) {
        entry.timer = setTimeout(() => {
          if (this.pending.delete(id)) reject(new HostTimeoutError(method, ms));
        }, ms);
      }
      this.pending.set(id, entry);
      this.write({ jsonrpc: '2.0', id, method, params });
    });
  }

  /** Sends a notification: no reply, no id. */
  notify(method: string, params?: unknown): void {
    if (!this.child || this.exited) return;
    this.write({ jsonrpc: '2.0', method, params });
  }

  /** Runs the handshake and remembers what the host can do. */
  async initialize(cwd: string, client: { name: string; version: string }): Promise<{ host: string; capabilities: string[]; cwd: string }> {
    const res = await this.request<{ protocol: number; host: string; cwd: string; capabilities: string[] }>(
      'initialize',
      { protocol: PROTOCOL_VERSION, cwd, client },
    );
    if (res.protocol !== PROTOCOL_VERSION) {
      // A mismatch is not a hang and must not look like one: the two sides
      // speak different revisions, and no amount of retrying fixes it. Name
      // which binary answered and what to do about it.
      throw new HostDownError(
        `${res.host} speaks editor-host protocol ${res.protocol}; this extension speaks ` +
          `${PROTOCOL_VERSION}. The two were built from different revisions: reinstall the ` +
          'extension, or point anvil.hostPath at an anvil-host built with it.',
      );
    }
    this.notify('initialized');
    return { host: res.host, capabilities: res.capabilities ?? [], cwd: res.cwd };
  }

  /**
   * Asks the host to shut down and waits for the child to go. Falls back to a
   * kill, so a wedged host cannot keep the editor's window from closing.
   */
  async stop(graceMs = 2000): Promise<void> {
    const child = this.child;
    if (!child || this.exited) return;
    this.stopping = true;
    const ended = new Promise<void>((resolve) => {
      if (this.exited) return resolve();
      child.once('exit', () => resolve());
    });
    this.notify('shutdown');
    try {
      child.stdin?.end();
    } catch {
      // The pipe is already gone; the exit race below still covers us.
    }
    const timer = setTimeout(() => {
      if (!this.exited) child.kill();
    }, graceMs);
    await ended;
    clearTimeout(timer);
  }

  private write(payload: unknown): void {
    try {
      this.child?.stdin?.write(`${JSON.stringify(payload)}\n`);
    } catch (err) {
      this.options.log?.(`host: write failed (${(err as Error).message})`);
    }
  }

  private failAll(err: Error): void {
    for (const [, entry] of this.pending) {
      if (entry.timer) clearTimeout(entry.timer);
      entry.reject(err);
    }
    this.pending.clear();
  }
}
