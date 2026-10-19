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
  /**
   * Replays a stored conversation. Keyed by agent id rather than by session, so
   * it answers for a session this host has not opened — which is the case it
   * exists for: a window that has just reloaded and knows which agent it was
   * talking to. A host that predates it answers `-32601`, and the panel then has
   * no replay rather than an error.
   */
  SessionTranscript: 'session.transcript',
  SessionClose: 'session.close',
  /**
   * Names a stored session. Additive: it appends a `meta` record rather than
   * rewriting the file, so a resume replays exactly the history it did before.
   * A host that predates it answers `-32601`, which the client reads as "no
   * rename here".
   */
  SessionRename: 'session.rename',
  /** Removes a stored session's records. */
  SessionDelete: 'session.delete',
  /**
   * Branches a stored conversation at a finished run, answering the new agent
   * id. Additive: a host that predates it answers `-32601`, and the client then
   * offers no fork rather than a button that fails.
   */
  SessionFork: 'session.fork',
  /**
   * Lists the runs of a session that captured a pre-image, newest first.
   * Additive: a host that predates it answers `-32601`, and the panel then
   * offers no restore rather than an error.
   */
  CheckpointList: 'checkpoint.list',
  /** Puts one run's captured files back. */
  CheckpointRestore: 'checkpoint.restore',
  Prompt: 'prompt',
  Cancel: 'cancel',
  ModelsList: 'models.list',
  SkillsList: 'skills.list',
  RulesList: 'rules.list',
  /**
   * The MCP servers the enabled file layers declare. Additive: a host that
   * predates it answers `-32601`, and the panel then shows no card rather than
   * an error.
   */
  McpList: 'mcp.list',
  PermissionsGet: 'permissions.get',
  PermissionsAdd: 'permissions.add',
  /**
   * Replaces the workspace rule file with the list a manager showed a person.
   * Additive: a host that predates it answers `-32601`, and the manager then
   * says so rather than pretending the save happened.
   */
  PermissionsSet: 'permissions.set',
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
  /**
   * Turn on the web tools. Absent means off; there is no `enabled` field
   * because a host that predates the field and a client that does not want the
   * tools should agree about absence.
   *
   * No key travels here. The host reads `ANVIL_WEB_SEARCH_API_KEY` from the
   * environment the extension spawns it with.
   */
  web_search?: WebSearchParams;
}

/** Configuration for `web_search` / `fetch_url`; presence is the switch. */
export interface WebSearchParams {
  /** `http` (default) uses the SDK's JSON adapter; `mcp` registers no builtins. */
  provider?: string;
  /** The JSON search endpoint. Required for the http provider. */
  base_url?: string;
  /** Results per search; 0 uses the SDK default of 5. */
  max_results?: number;
}

export interface SessionResult {
  session_id: string;
  agent_id: string;
  cwd: string;
  provider: string;
  model: string;
  permission_mode: PermissionMode;
  /**
   * Where that mode came from: `request` when this client named it, `file` when
   * the workspace's `.anvil/permissions.json` declared it, `default` when neither
   * did. The middle one is the reason this travels: "why is this session in
   * accept_edits" has two answers and only one of them is something just clicked.
   */
  permission_mode_source: PermissionModeSource;
  /**
   * What the workspace declared and the host would not honour, in words. The
   * session still started — in `ask` — because one bad line in a project file
   * must not be a workspace nobody can open; this is what keeps that from being
   * a silent drop.
   */
  permission_mode_refused?: string;
}

/**
 * Where a session's mode came from: this client asked for it, the workspace's
 * `.anvil/permissions.json` declared it, or neither did. The names are the host's
 * `ModeSource*` constants in `proto.go`, which the same test reads.
 */
export const ModeSourceRequest = 'request';
export const ModeSourceFile = 'file';
export const ModeSourceDefault = 'default';
export const PermissionModeSources = [ModeSourceRequest, ModeSourceFile, ModeSourceDefault] as const;
export type PermissionModeSource = (typeof PermissionModeSources)[number];

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
  /**
   * The mode the file declares for new sessions, and only when the host would
   * honour it. Absent means this workspace starts sessions in the ladder's floor
   * — either because it declares nothing (`default_mode_refused` is absent too)
   * or because what it declares cannot be honoured. Deliberately not filled in
   * with `ask` for a file that said nothing: that would read as if the file had.
   */
  default_mode?: PermissionMode;
  /** What the file declared and the host would not honour, in words. */
  default_mode_refused?: string;
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
 * Replaces the whole rule file: the manager sends the list it showed a person,
 * and the file becomes that list. The host answers with `PermissionsResult`, so
 * the panel renders what a new session will read rather than what it sent.
 *
 * `default_mode` omitted removes the declaration. A word the ladder does not
 * have, and `bypass`, are refused by the host — a project file may not grant a
 * mode that approves everything without showing it.
 */
export interface PermissionsSetParams {
  rules: RuleSpec[];
  default_mode?: PermissionMode;
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

/**
 * The mode ladder, from the one that asks about everything to the one that asks
 * about nothing. The order is the order the composer offers them, and it is the
 * host's `PermissionModes` in `proto.go` — `test/unit/permissionModes.test.ts`
 * reads both as text, because a mode the Go side adds and this line does not have
 * is one nobody can choose.
 */
export const PermissionModes = ['ask', 'accept_edits', 'auto', 'plan', 'dont_ask', 'bypass'] as const;
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
  /**
   * The session's first user message, one line and truncated by the store.
   * Absent when the session was never prompted — a row falls back to the agent
   * id rather than showing a title nobody wrote.
   */
  title?: string;
  created_at?: string;
  updated_at?: string;
  last_status?: string;
  last_run_id?: string;
  runs: number;
  /** Cumulative across the stored runs, when the provider reported any. */
  usage?: Usage;
}

// --- session.transcript ------------------------------------------------------

export interface SessionTranscriptParams {
  agent_id: string;
  /** Caps the entries returned, keeping the newest. Omitted means the host's default. */
  limit?: number;
  store?: string;
}

/** One tool invocation as the store recorded it, arguments included. */
export interface TranscriptCall {
  id: string;
  name: string;
  /**
   * The call's arguments, in the same shape a live `tool_call` event carries
   * them. They are what a replayed card opens its change from, which is why
   * they travel at all.
   */
  args?: unknown;
}

/**
 * One unit of a stored conversation: a message, or where a run ended. The union
 * is by `kind`, and `reasoning` is not one of the values — it was streamed and
 * dropped rather than stored, so there is nothing to replay it from.
 */
export interface TranscriptEntry {
  kind: 'message' | 'run_end';
  run_id?: string;

  /** message */
  role?: 'user' | 'assistant' | 'tool';
  content?: string;
  tool_call_id?: string;
  name?: string;
  tool_calls?: TranscriptCall[];

  /** run_end */
  status?: RunStatus;
  steps?: number;
  usage?: Usage;
}

export interface SessionTranscriptResult {
  entries: TranscriptEntry[];
  /**
   * How many entries before the first one were left out, by the cap and by the
   * run boundary it was moved back to. A replay that shows the tail says so
   * rather than implying the conversation started there.
   */
  dropped?: number;
}

// --- session.rename / session.delete -----------------------------------------

export interface SessionRenameParams {
  agent_id: string;
  title: string;
  store?: string;
}

export interface SessionRenameResult {
  agent_id: string;
  title: string;
}

export interface SessionDeleteParams {
  agent_id: string;
  store?: string;
}

export interface SessionDeleteResult {
  agent_id: string;
  deleted: boolean;
}

// --- session.fork ------------------------------------------------------------

/**
 * The conversation and the run ending the part to keep. Both are required: a
 * fork copies records only through a `run_end`, so a run that never finished
 * has no boundary to name.
 */
export interface SessionForkParams {
  agent_id: string;
  run_id: string;
  store?: string;
}

/** The new conversation. Opening it is the client's next step. */
export interface SessionForkResult {
  agent_id: string;
}

// --- checkpoint.list / checkpoint.restore ------------------------------------

/** Names the session whose run checkpoints to read. */
export interface CheckpointListParams {
  agent_id: string;
}

export interface CheckpointListResult {
  runs: CheckpointRun[];
}

/** One run's captured pre-images. */
export interface CheckpointRun {
  run_id: string;
  created_at?: string;
  files: CheckpointFile[];
}

export interface CheckpointFile {
  /** Workspace-relative, slash-separated. */
  path: string;
  /** False means the run created the file; restoring deletes it. */
  existed: boolean;
  size?: number;
}

export interface CheckpointRestoreParams {
  agent_id: string;
  run_id: string;
}

/** What the restore actually did, rather than what it was asked to do. */
export interface CheckpointRestoreResult {
  run_id: string;
  files: RestoredFile[];
}

export interface RestoredFile {
  path: string;
  deleted: boolean;
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

// --- mcp.list ----------------------------------------------------------------

/**
 * One server a config file declares. It says what would be launched, not
 * whether it launched: the host reports no connection state, so there is no
 * field here to mistake for one.
 */
export interface McpServerInfo {
  name: string;
  /** stdio | http | sse. Empty for an entry naming neither command nor URL. */
  transport?: string;
  /** The config file that declared it. */
  source: string;
}

/** One config file the listing looked at. */
export interface McpSourceInfo {
  source: string;
  path: string;
  /** False when the file is not there; a note, not an error. */
  exists: boolean;
}

export interface McpListParams {
  setting_sources?: string[];
}

export interface McpListResult {
  servers: McpServerInfo[];
  sources: McpSourceInfo[];
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
  | 'reasoning'
  /**
   * The model's complete task list, in `items` (0.40.9). It follows the
   * `todo_write` call that wrote it; an empty list clears the panel. A replay
   * synthesises the same event from the stored call's arguments, so the webview
   * has one path for both.
   */
  | 'todo';

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
   * The run's token usage so far, on a terminal event the host forwards (an
   * `error`), when the provider reported any (0.40.8+). Absent elsewhere and
   * absent when unknown — which is not the same as zero. The authoritative
   * end-of-run usage is the `done` notification's `usage`.
   */
  usage?: Usage;
  /**
   * The workspace rule that decided this tool result, when one did. It is how a
   * call that ran without a prompt says why it did not need one; absent means no
   * rule decided it, which is not the same as "some rule allowed it".
   */
  rule?: RuleInfo;
  /**
   * The complete task list on a `todo` event (0.40.9). Present as [] when a
   * call cleared it — which is different from the field being absent on every
   * other event, and is why the host writes a pointer rather than omitting it.
   */
  items?: TodoItem[];
}

/** One row of the model's plan, as `todo_write` wrote it. */
export interface TodoItem {
  text: string;
  status: 'pending' | 'in_progress' | 'done';
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
