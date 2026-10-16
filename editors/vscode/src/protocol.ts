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
  SkillsList: 'skills.list',
  RulesList: 'rules.list',
  PermissionsGet: 'permissions.get',
  PermissionsAdd: 'permissions.add',
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
  /**
   * Whether `.anvil/hooks.json` and `.anvil/permissions.json` are read for this
   * session. Omitted means on: both are policy the workspace wrote down, so a
   * client that wants them off says so. Only meaningful on `session.new`.
   */
  load_hooks?: boolean;
  load_permissions?: boolean;
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
  /**
   * Opt in to image attachments. Off by default: the host refuses an
   * attachment on a session that did not ask, because the SDK's
   * `Vision.Enabled` is what makes it accept one at all.
   */
  vision?: boolean;
}

export interface SessionResult {
  session_id: string;
  agent_id: string;
  cwd: string;
  provider: string;
  model: string;
  permission_mode: PermissionMode;
}

// --- permissions -------------------------------------------------------------

/**
 * One rule as the user writes it in `.anvil/permissions.json`.
 *
 * The same shape travels both ways: the host sends the rules the file holds, and
 * the client sends back the rule a "remember this" choice would append. `effect`
 * may be left empty when asking for a rule to be written, which means allow.
 */
export interface RuleSpec {
  effect?: 'allow' | 'ask' | 'deny' | '';
  tool: string;
  /**
   * What the rule matches, read according to the call's facts: a command line
   * for `shell`, a path glob for the file tools, a tool-name glob otherwise.
   * Empty matches every call to the tool.
   */
  pattern?: string;
  reason?: string;
}

/** One rule of a set, with the position and the sentence a decision quotes. */
export interface RuleInfo extends RuleSpec {
  /**
   * The rule's position as the SDK counts it — one-based, the number a denial
   * quotes. This is not an index into the `rules` array, which is zero-based.
   */
  index: number;
  /** The SDK's own sentence for this rule; show it rather than composing one. */
  describe?: string;
}

/** A rule file that will not compile, as the parser sees it. */
export interface RuleProblem {
  message: string;
  /** One-based, and zero when the problem belongs to no particular place. */
  line?: number;
  column?: number;
}

/** The workspace rule file's state. */
export interface PermissionsResult {
  /** Absolute, so the file can be opened even before it exists. */
  path: string;
  /** False with no `problem` is a workspace that has written down nothing. */
  exists: boolean;
  rules: RuleInfo[];
  /**
   * Set when the file exists but cannot be read as rules. A result rather than
   * an error: a file somebody is halfway through editing is a state of the file,
   * and it is reported with its place so it can be fixed where it is edited.
   */
  problem?: RuleProblem;
}

export interface PermissionsAddParams {
  rule: RuleSpec;
  /**
   * The pending approval this rule is being written for. The host then answers
   * that approval itself when the file decides the call, so "always allow" takes
   * effect on the call in front of the user instead of on the next one.
   */
  request_id?: string;
}

export interface PermissionsAddResult {
  path: string;
  /** The position the new rule landed at, counted as `RuleInfo.index` is. */
  index: number;
  rules: RuleInfo[];
  /**
   * The rule that decides the pending call now, which is not always the one just
   * written: a deny outranks an allow wherever in the file each one sits.
   */
  decided_by?: RuleInfo;
  /**
   * The host answered the pending approval with allow because the file now
   * decides the call. False means the card still has to be answered, and
   * `decided_by` says what stood in the way.
   */
  approved?: boolean;
}

/**
 * One "always allow" a card may offer. The host evaluated it against the very
 * call being shown, so a button built from one always writes a rule that decides
 * that call; a shape that would not is not offered.
 */
export interface RememberOption {
  /** `exact` | `prefix` | `readonly` | `path` | `tool`. The host decides it. */
  kind: string;
  rule: RuleSpec;
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
  /**
   * The target's body before and after the projected change. Present for
   * write_file / edit_file, so a review can be opened as a real diff; empty for
   * apply_patch, which may span several files and carries only `diff`.
   */
  before?: string;
  after?: string;
  /** The bodies were withheld for size; render `diff` instead. */
  truncated?: boolean;
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
  /**
   * The workspace rule that insisted on this prompt, when one did. Absent means
   * the mode gate alone is the reason, so a card never claims a rule asked when
   * none did.
   */
  rule?: RuleInfo;
  /**
   * The "always allow" choices this call can honestly be offered. Empty or
   * absent means no rule shape would decide this call — a compound shell line,
   * say — so the card offers none rather than one that would not work.
   */
  remember?: RememberOption[];
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

/**
 * What the editor knows that the prompt text alone does not say. The host folds
 * it into the message; the client never sends file contents it did not already
 * have in a selection.
 */
export interface PromptContext {
  /** Workspace-relative paths the user referenced. Files are read by the host. */
  mentions?: Mention[];
  selection?: Selection;
  diagnostics?: Diagnostic[];
}

export interface Mention {
  path: string;
}

export interface Selection {
  path: string;
  range: Range;
  text: string;
}

/** A 1-based editor range, in the shape VS Code reports it. */
export interface Range {
  start_line: number;
  start_column: number;
  end_line: number;
  end_column: number;
}

export interface Diagnostic {
  path: string;
  line: number;
  severity?: string;
  message: string;
}

/**
 * One image. Exactly one of `data` (base64, for a paste) or `path`
 * (workspace-relative, for a file the user picked) is set.
 */
export interface Attachment {
  path?: string;
  data?: string;
  mime?: string;
  /** openai detail hint: low | high | auto. */
  detail?: string;
}

export interface PromptParams {
  session_id: string;
  message: string;
  context?: PromptContext;
  attachments?: Attachment[];
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
  /** Cumulative across the stored runs, when the provider reported any. */
  usage?: Usage;
}

// --- skills.list / rules.list ------------------------------------------------

/**
 * One discoverable skill package. This is the catalogue, not the session's
 * selection: a session uses a skill only when it names the id on
 * `session.new` / `session.resume`, or when `.anvil/skills.json` marks it
 * always-on.
 */
export interface SkillEntry {
  id: string;
  name: string;
  description?: string;
  /** The SKILL.md, workspace-relative. Empty for a skill outside the root. */
  path?: string;
  /** Injected into every session without being selected. */
  always?: boolean;
}

export interface SkillsParams {
  scan_user?: boolean;
  skill_dirs?: string[];
}

export interface SkillsResult {
  skills: SkillEntry[];
}

/** One rule file, as the system prompt sees it. */
export interface RuleFileInfo {
  /** Workspace-relative, and the heading the prompt writes above the body. */
  path: string;
  bytes?: number;
  /** Whether the body reached the system prompt at all. */
  included: boolean;
  /** Whether the body was cut short by the character budget. */
  truncated?: boolean;
}

export interface RulesParams {
  load_project?: boolean;
  load_cursor_rules?: boolean;
}

export interface RulesResult {
  files: RuleFileInfo[];
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
  | 'error'
  /**
   * The model's thinking, for providers that stream it, carried in `text` like
   * `assistant`. Display-only: it is not part of the run result and has no
   * replay, so a resumed session shows none. A host that predates it simply
   * never emits the value, which is why this did not move the protocol version.
   */
  | 'reasoning';

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
  /**
   * The workspace rule that decided this tool result, when one did. It is how a
   * call that ran without a prompt says why it did not need one; absent means no
   * rule decided it, which is not the same as "some rule allowed it".
   */
  rule?: RuleInfo;
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
