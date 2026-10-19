/**
 * What the session sidebar shows, derived from `session.list`.
 *
 * This module imports no `vscode`, so it is the half of the sidebar that can be
 * tested: the provider above it only turns these nodes into `TreeItem`s. The
 * split is the same one `commands.ts` and `hostBundle.ts` sit on.
 *
 * The rule it follows is that nothing is invented to fill a column. A session
 * is listed under the first thing the person typed — the title the store
 * derived — and a session nobody prompted falls back to its shortened id
 * rather than to a placeholder. A field the host did not report is absent
 * rather than guessed.
 */

import { SessionInfo } from './protocol';

/** A session row: one stored agent. */
export interface SessionRow {
  kind: 'session';
  /** Stable across refreshes — the agent id — so the tree keeps its selection. */
  id: string;
  label: string;
  description: string;
  tooltip: string;
  /** A `ThemeIcon` id. */
  icon: string;
  contextValue: string;
  agentId: string;
  /**
   * The stored name, when there is one. The label falls back to the id for a
   * session nobody has named, and a rename box must not start out holding that
   * fallback as if it were the name.
   */
  title?: string;
}

/** A recency heading over rows. */
export interface SessionGroup {
  kind: 'group';
  id: string;
  label: string;
  rows: SessionRow[];
}

/** A row that is the whole view: a reason it is empty, or a load in progress. */
export interface SessionNote {
  kind: 'note';
  id: string;
  label: string;
  tooltip: string;
  icon: string;
  /** Whether the row offers the refresh command. */
  retry: boolean;
}

/** Anything the sidebar can be asked to render a child of. */
export type SessionNode = SessionRow | SessionGroup | SessionNote;

export interface SessionViewOptions {
  /** The clock, injected so the recency buckets are testable. */
  now: number;
  /**
   * The agents with a chat tab open, and the agents with a turn in flight.
   *
   * Lists rather than one id: several tabs may be open, so the same agent can
   * only be marked once but any number of the rows can be marked at the same
   * time.
   */
  openAgentIds?: readonly string[];
  runningAgentIds?: readonly string[];
}

/** Longest agent id shown as a label; the tooltip always carries the whole one. */
const LABEL_LIMIT = 12;

/** How many local days back "This week" reaches, today included. */
const WEEK_DAYS = 7;

/**
 * The statuses the SDK reports, and their icons. `last_status` is typed as a
 * string on the wire, so an unrecognised one is rendered as an unknown history
 * rather than folded into the nearest known state.
 */
const STATUS_ICONS: Record<string, string> = {
  finished: 'check',
  error: 'error',
  cancelled: 'circle-slash',
};

/** Splits a session list into the sidebar's top-level entries. */
export function buildSessionView(
  sessions: SessionInfo[],
  options: SessionViewOptions,
): SessionNode[] {
  if (sessions.length === 0) return [sessionNote('empty')];

  const sorted = sortSessionsByRecency(sessions);
  const dated = sorted.some((session) => timestampOf(session) !== undefined);
  // With nothing to group by, headings would be decoration: one bucket labelled
  // "Undated" over every session says less than the sessions do.
  if (!dated) return sorted.map((session) => sessionRow(session, options));

  const bounds = boundsFor(options.now);
  const entries: SessionNode[] = [];
  for (const [id, label] of GROUP_LABELS) {
    const inBucket = sorted.filter((session) => bucketOf(timestampOf(session), bounds) === id);
    if (inBucket.length === 0) continue;
    entries.push({
      kind: 'group',
      id: `group:${id}`,
      label,
      rows: inBucket.map((session) => sessionRow(session, options)),
    });
  }
  return entries;
}

/** One of the states in which the sidebar has no sessions to list. */
export function sessionNote(
  kind: 'untrusted' | 'empty' | 'loading' | 'error',
  message?: string,
): SessionNote {
  switch (kind) {
    case 'untrusted':
      return {
        kind: 'note',
        id: 'note:untrusted',
        label: 'Trust this folder to see its sessions',
        tooltip: 'Anvil does not run in an untrusted workspace, so it cannot read this one’s history.',
        icon: 'shield',
        retry: false,
      };
    case 'loading':
      return {
        kind: 'note',
        id: 'note:loading',
        label: 'Loading sessions…',
        tooltip: 'Asking the host for this workspace’s stored agents.',
        icon: 'loading~spin',
        retry: false,
      };
    case 'error':
      return {
        kind: 'note',
        id: 'note:error',
        label: 'Could not list sessions',
        // The spawn failure's own words: "could not start the Anvil host" and a
        // bundle that failed its digest check are different problems, and the
        // fix for each is in the message that produced it.
        tooltip: message ?? 'The host did not answer.',
        icon: 'warning',
        retry: true,
      };
    default:
      return {
        kind: 'note',
        id: 'note:empty',
        label: 'No sessions in this workspace yet',
        tooltip: 'The first prompt in the chat starts one.',
        icon: 'history',
        retry: true,
      };
  }
}

/** The headings, in the order they are shown. A bucket with no rows is dropped. */
const GROUP_LABELS: ReadonlyArray<readonly [string, string]> = [
  ['today', 'Today'],
  ['week', 'This week'],
  ['earlier', 'Earlier'],
  ['undated', 'Undated'],
];

interface Bounds {
  /** Local midnight this morning. */
  today: number;
  /** Local midnight six days ago, so "This week" is seven whole local days. */
  week: number;
}

/**
 * The bucket edges, computed from the local calendar rather than by subtracting
 * 24-hour days: a day is 23 or 25 hours long twice a year, and "within 168
 * hours" would cut a day in half. "Today" is the reader's today, not UTC's.
 */
function boundsFor(now: number): Bounds {
  const today = new Date(now);
  today.setHours(0, 0, 0, 0);
  const week = new Date(today);
  week.setDate(week.getDate() - (WEEK_DAYS - 1));
  return { today: today.getTime(), week: week.getTime() };
}

function bucketOf(at: number | undefined, bounds: Bounds): string {
  if (at === undefined) return 'undated';
  if (at >= bounds.today) return 'today';
  if (at >= bounds.week) return 'week';
  return 'earlier';
}

/**
 * The instant a session sorts and groups by. `updated_at` is the one that
 * moves; `created_at` is the fallback for a store that reports only that.
 */
function timestampOf(session: SessionInfo): number | undefined {
  for (const stamp of [session.updated_at, session.created_at]) {
    if (typeof stamp !== 'string' || stamp === '') continue;
    const at = Date.parse(stamp);
    if (!isNaN(at)) return at;
  }
  return undefined;
}

/**
 * Newest first, and the undated last rather than at the top.
 *
 * Exported because the search picker lists rows with no headings, and the order
 * a person expects there is the same one the sidebar uses.
 */
export function sortSessionsByRecency(sessions: readonly SessionInfo[]): SessionInfo[] {
  return [...sessions].sort(byRecency);
}

function byRecency(left: SessionInfo, right: SessionInfo): number {
  const a = timestampOf(left);
  const b = timestampOf(right);
  if (a === undefined && b === undefined) return 0;
  if (a === undefined) return 1;
  if (b === undefined) return -1;
  return b - a;
}

function shortId(agentId: string): string {
  return agentId.length > LABEL_LIMIT ? agentId.slice(0, LABEL_LIMIT) : agentId;
}

/**
 * The title is what a person recognises; the id is what a command takes, and
 * it stays in the tooltip either way. A title the store could not derive — a
 * session nobody prompted, or one whose first message was only whitespace —
 * falls back to the id rather than to a placeholder.
 */
export function sessionLabel(session: SessionInfo): string {
  const title = session.title?.trim();
  return title ? title : shortId(session.agent_id);
}

function runs(count: number): string {
  return `${count} run${count === 1 ? '' : 's'}`;
}

function sessionRow(session: SessionInfo, options: SessionViewOptions): SessionRow {
  const agentId = session.agent_id;
  const running = options.runningAgentIds?.includes(agentId) === true;
  const current = options.openAgentIds?.includes(agentId) === true;

  // The icon carries the last status, so the description does not repeat it;
  // it carries what the row cannot show as a glyph — the model and the size of
  // the conversation.
  const parts: string[] = [];
  if (session.model) parts.push(session.model);
  parts.push(runs(session.runs));
  // Running wins over "open": a session being resumed and then prompted is both,
  // and the one that is changing under the user is the running one.
  if (running) parts.push('running');
  else if (current) parts.push('open');

  const title = session.title?.trim();
  return {
    kind: 'session',
    id: agentId,
    label: sessionLabel(session),
    title: title ? title : undefined,
    description: parts.join(' · '),
    tooltip: tooltipFor(session, { running, current }),
    icon: running ? 'sync~spin' : STATUS_ICONS[session.last_status ?? ''] ?? 'history',
    contextValue: running ? 'session.running' : current ? 'session.open' : 'session',
    agentId,
  };
}

/** Plain text, not Markdown: the lines include paths, which are not markup. */
function tooltipFor(session: SessionInfo, flags: { running: boolean; current: boolean }): string {
  const lines = [`Agent ${session.agent_id}`];
  if (flags.running) lines.push('A turn is running.');
  else if (flags.current) lines.push('Open in a chat tab.');
  if (session.cwd) lines.push(`Workspace ${session.cwd}`);
  if (session.provider || session.model) {
    lines.push(`Model ${[session.provider, session.model].filter(Boolean).join('/')}`);
  }
  const created = localStamp(session.created_at);
  if (created) lines.push(`Started ${created}`);
  const updated = localStamp(session.updated_at);
  if (updated) lines.push(`Last run ${updated}`);
  lines.push(`Runs ${session.runs}`);
  lines.push(`Last status ${session.last_status ? session.last_status : 'unknown'}`);
  const tokens = totalTokens(session);
  if (tokens) lines.push(`Tokens ${tokens}`);
  return lines.join('\n');
}

function localStamp(stamp: string | undefined): string | undefined {
  if (typeof stamp !== 'string' || stamp === '') return undefined;
  const at = Date.parse(stamp);
  return isNaN(at) ? undefined : new Date(at).toLocaleString();
}

function totalTokens(session: SessionInfo): string | undefined {
  const total = session.usage?.total_tokens;
  return typeof total === 'number' && total > 0 ? String(total) : undefined;
}
