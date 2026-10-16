/**
 * TypeScript mirror of `source/internal/editorhost/proto.go`.
 *
 * Keep this file in step with the Go one: it is the whole contract between the
 * extension and the sidecar. A field the Go side renames and this file does not
 * is a silent runtime `undefined`, so every name here is spelled exactly as it
 * appears in the Go struct tags.
 */

/** Protocol revision, matching `editorhost.ProtocolVersion`. */
export const PROTOCOL_VERSION = 1;

// --- methods -----------------------------------------------------------------

/** Extension to host requests. */
export const Method = {
  Initialize: 'initialize',
  Initialized: 'initialized',
  SessionNew: 'session.new',
  SessionResume: 'session.resume',
  SessionList: 'session.list',
  SessionClose: 'session.close',
  Prompt: 'prompt',
  Cancel: 'cancel',
  ModelsList: 'models.list',
  Shutdown: 'shutdown',
  ApprovalResponse: 'approval.response',
} as const;

/** Host to extension notifications. */
export const Notify = {
  Event: 'event',
  Done: 'done',
  Log: 'log',
  ApprovalRequest: 'approval.request',
} as const;

// --- errors ------------------------------------------------------------------

/** JSON-RPC 2.0 standard codes. */
export const CodeParse = -32700;
export const CodeInvalidRequest = -32600;
export const CodeMethodNotFound = -32601;
export const CodeInvalidParams = -32602;
export const CodeInternal = -32603;

/** Host-specific codes. */
export const CodeHostError = -32000;
export const CodeNoSession = -32001;
export const CodeNoRun = -32002;
export const CodeRunInProgress = -32003;
export const CodeNoApproval = -32004;

/** A JSON-RPC error object, as it arrives on the wire. */
export interface RpcError {
  code: number;
  message: string;
}

/** An inbound message: a reply (has `id`) or a notification (does not). */
export interface InboundMessage {
  jsonrpc: string;
  id?: number | null;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: RpcError;
}

// --- initialize --------------------------------------------------------------

export interface ClientInfo {
  name?: string;
  version?: string;
}

export interface InitializeParams {
  protocol: number;
  cwd: string;
  client?: ClientInfo;
}

export interface InitializeResult {
  protocol: number;
  host: string;
  cwd: string;
  capabilities: string[];
}

// --- sessions ----------------------------------------------------------------

export interface SessionParams {
  /** session.resume only. */
  agent_id?: string;
  provider?: string;
  base_url?: string;
  model?: string;
  system_prompt?: string;
  max_steps?: number;
  store?: string;
  load_rules?: boolean;
  load_cursor_rules?: boolean;
  setting_sources?: string[];
  builtin_tools?: string[];
  disallowed_tools?: string[];
  skills?: string[];
  plugins?: string[];
  permission_mode?: PermissionMode;
  permission_groups?: string[];
  approval_timeout_ms?: number;
  test_command?: string[];
  build_command?: string[];
}

export interface SessionResult {
  session_id: string;
  agent_id: string;
  cwd: string;
  provider: string;
  model: string;
  permission_mode: PermissionMode;
}

// --- approval ----------------------------------------------------------------

export const PermissionModes = ['ask', 'accept_edits', 'plan', 'bypass'] as const;
export type PermissionMode = (typeof PermissionModes)[number];

/** Decisions the host accepts, in the order a prompt should offer them. */
export const Decisions = ['allow', 'allow_session', 'deny'] as const;
export type Decision = (typeof Decisions)[number];

/**
 * What a gated call would do. Best-effort by contract: an absent `kind` means
 * the host could not preview it, never that the call is harmless.
 */
export interface ApprovalPreview {
  kind?: 'diff' | 'command' | 'summary' | '';
  path?: string;
  diff?: string;
  command?: string[];
  summary?: string;
}

export interface ApprovalRequest {
  request_id: string;
  session_id: string;
  run_id: string;
  tool: string;
  args?: unknown;
  preview?: ApprovalPreview;
  decisions: Decision[];
  timeout_ms?: number;
}

export interface ApprovalResponse {
  request_id: string;
  decision: Decision;
  /** Narrows an allow_session to one scope; omitted means the whole tool. */
  scope?: string;
  /** Replaces the call's arguments when the user approved an edited version. */
  args?: unknown;
  /** Shown to the model when the decision is deny. */
  message?: string;
}

// --- prompt / cancel ---------------------------------------------------------

export interface PromptParams {
  session_id: string;
  message: string;
}

export interface PromptResult {
  session_id: string;
  run_id: string;
  agent_id: string;
}

export interface CancelParams {
  session_id: string;
  run_id?: string;
}

// --- session.list ------------------------------------------------------------

export interface SessionInfo {
  agent_id: string;
  cwd: string;
  provider: string;
  model: string;
  created_at?: string;
  updated_at?: string;
  last_status?: string;
  last_run_id?: string;
  runs: number;
}

// --- models.list -------------------------------------------------------------

export interface ModelEntry {
  id: string;
  owned_by?: string;
  provider?: string;
  upstream_model?: string;
  vision?: boolean;
}

// --- event / done ------------------------------------------------------------

export type EventType =
  | 'progress'
  | 'assistant'
  | 'tool_call'
  | 'tool_result'
  | 'error';

/** One streamed unit of a run. */
export interface HostEvent {
  session_id: string;
  run_id: string;
  type: EventType;
  text?: string;
  tool_call_id?: string;
  name?: string;
  args?: unknown;
  content?: string;
  is_error?: boolean;
  step?: number;
  max_steps?: number;
  code?: string;
}

/** Run status values, mirroring `anvil.Status*`. */
export type RunStatus = 'finished' | 'error' | 'cancelled';

/** The terminal envelope, emitted exactly once per prompt after the last event. */
export interface DoneParams {
  session_id: string;
  run_id: string;
  status: RunStatus;
  result?: string;
  steps?: number;
  usage?: Usage;
  err?: string;
  code?: string;
}

export interface Usage {
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface LogParams {
  level: 'debug' | 'info' | 'warn' | 'error';
  message: string;
}

// --- helpers -----------------------------------------------------------------

/** The one-line reason a run ended, for a UI that has to say something. */
export function doneReason(done: DoneParams): string {
  switch (done.status) {
    case 'finished':
      return '';
    case 'cancelled':
      return 'Stopped.';
    default:
      return done.err || 'The run failed.';
  }
}
