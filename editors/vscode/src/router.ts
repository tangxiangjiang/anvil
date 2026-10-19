/**
 * Which conversation a notification belongs to.
 *
 * One host child serves every tab in the window: two conversations cost two
 * sessions, not two processes. The price of that is demultiplexing — every
 * notification names the session it came from, and this map is the whole of the
 * decision about where it goes. It is kept out of `hostManager.ts` because it
 * imports no `vscode`, so the rule that matters most here — one tab's event,
 * approval or run end never reaches another tab — can be tested against two
 * real sessions and a real child.
 */

import { LogFn } from './hostClient';
import { Notify } from './protocol';

/** The slice of a session a router needs. */
export interface RoutedSession {
  handle(method: string, params: unknown): void;
}

export class SessionRouter<T extends RoutedSession = RoutedSession> {
  private readonly byId = new Map<string, T>();

  /**
   * Registers a session under the id the host answered with.
   *
   * A session that re-opened — a mode or model change closes one and opens the
   * same conversation again — arrives with a new id. The old one is the same
   * conversation and must not stay reachable, or the router would hold two keys
   * for one tab for as long as the window lives.
   */
  register(sessionId: string, session: T): void {
    for (const [id, candidate] of this.byId) {
      if (candidate === session && id !== sessionId) this.byId.delete(id);
    }
    this.byId.set(sessionId, session);
  }

  /** Removes a closed session, under whichever id it was registered. */
  unregister(session: T): void {
    for (const [id, candidate] of this.byId) {
      if (candidate === session) this.byId.delete(id);
    }
  }

  /** Forgets every key, after the process that owned them went away. */
  clear(): T[] {
    const sessions = [...this.byId.values()];
    this.byId.clear();
    return sessions;
  }

  /** The live sessions, in no particular order. */
  sessions(): T[] {
    return [...this.byId.values()];
  }

  /** How many sessions are registered. */
  get size(): number {
    return this.byId.size;
  }

  /**
   * Sends one notification to the session it names.
   *
   * A notification with no `session_id` is a host-level log line. Anything else
   * unkeyed is dropped rather than broadcast: `ChatSession` treats an absent id
   * as "mine", so fanning it out would put one conversation's event into every
   * other tab's transcript.
   */
  route(method: string, params: unknown, log: LogFn): void {
    const id = (params as { session_id?: string } | undefined)?.session_id;
    if (!id) {
      if (method === Notify.Log) {
        const line = params as { level?: string; message?: string };
        log(`host ${line.level ?? 'info'}: ${line.message ?? ''}`);
        return;
      }
      log(`dropped an unkeyed ${method} notification`);
      return;
    }
    const session = this.byId.get(id);
    if (!session) {
      log(`dropped a ${method} notification for closed session ${id}`);
      return;
    }
    session.handle(method, params);
  }
}
