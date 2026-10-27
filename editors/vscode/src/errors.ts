/**
 * Turns a finished run into something the panel can say.
 *
 * The host reports a stable `code` on failure (see `anvil.Code*`). Collapsing
 * every one of them into "something went wrong" is what makes an agent feel
 * broken: `max_steps` means the turn needs splitting, `provider` means the key
 * or the base URL is wrong, `cancelled` is not a failure at all.
 */

import { DoneParams, RunStatus } from './protocol';

export type FailureKind =
  | 'none'
  | 'cancelled'
  | 'max_steps'
  | 'provider'
  | 'empty_response'
  | 'finish_protocol'
  | 'wall_time'
  | 'deadline'
  | 'context'
  | 'unknown';

export interface RunFailure {
  kind: FailureKind;
  /** One line for the panel. */
  message: string;
  /** Whether the panel should style it as a failure. */
  isError: boolean;
}

const MESSAGES: Record<Exclude<FailureKind, 'none'>, string> = {
  cancelled: 'Stopped.',
  max_steps: 'The agent hit its step limit before finishing. Raise anvil.maxSteps or ask for a smaller change.',
  provider: 'The model provider rejected the request. Check the API key, the model id, and anvil.baseUrl.',
  empty_response: 'The model provider repeatedly returned an empty completion. Automatic recovery produced no text or tool call.',
  finish_protocol: 'The model did not send the required finish_task completion signal.',
  wall_time: 'The turn ran out of wall-clock time.',
  deadline: 'The turn ran out of time.',
  context: 'The conversation no longer fits the model context. Start a new session or shorten the request.',
  unknown: '',
};

/** Maps a terminal `done` onto a presentation. */
export function classifyDone(done: DoneParams): RunFailure {
  const status: RunStatus = done.status;
  if (status === 'finished') {
    return { kind: 'none', message: '', isError: false };
  }
  if (status === 'cancelled') {
    return { kind: 'cancelled', message: MESSAGES.cancelled, isError: false };
  }

  const kind = classifyCode(done.code);
  const detail = (done.err || '').trim();
  if (kind === 'empty_response' || kind === 'finish_protocol') {
    return { kind, message: detail || MESSAGES.empty_response, isError: true };
  }
  let message = MESSAGES[kind] ?? '';
  if (message === '') {
    message = detail || 'The run failed.';
  } else if (detail !== '' && detail !== message) {
    // Keep the host's own words visible: they name the actual model, path, or
    // limit, which the summary cannot.
    message = `${message} (${detail})`;
  }
  return { kind, message, isError: true };
}

/** Maps an `anvil.Code*` string onto a failure kind. */
export function classifyCode(code: string | undefined): Exclude<FailureKind, 'none' | 'cancelled'> {
  switch ((code ?? '').toLowerCase()) {
    case 'max_steps':
      return 'max_steps';
    case 'provider':
    case 'provider_error':
    case 'auth':
      return 'provider';
    case 'empty_response':
      return 'empty_response';
    case 'finish_protocol':
      return 'finish_protocol';
    case 'max_wall_time':
      return 'wall_time';
    case 'context_deadline':
      return 'deadline';
    case 'context':
    case 'context_overflow':
    case 'context_too_large':
      return 'context';
    default:
      return 'unknown';
  }
}

/** A one-line summary for a transport-level failure (the host died, etc.). */
export function transportFailure(err: unknown): RunFailure {
  const message = err instanceof Error ? err.message : String(err);
  return { kind: 'unknown', message, isError: true };
}
