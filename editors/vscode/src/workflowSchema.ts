import {
  WorkflowApproval,
  WorkflowArtifact,
  WorkflowGetResult,
  WorkflowItemSource,
  WorkflowItemState,
  WorkflowWorkState,
  WorkflowWorkItem,
} from './protocol';

const ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const APPROVALS = new Set<WorkflowApproval>(['pending', 'approved', 'denied']);
const STATES = new Set<WorkflowItemState>([
  'proposed', 'ready', 'running', 'blocked', 'completed', 'failed', 'cancelled',
]);
const SOURCES = new Set<WorkflowItemSource>(['plan', 'todo', 'complex']);

const ARTIFACT_KEYS = new Set([
  'schema', 'artifact_id', 'run_id', 'kind', 'revision', 'title', 'body', 'items',
]);
const ITEM_KEYS = new Set([
  'id', 'title', 'goal', 'plan', 'files', 'acceptance', 'depends_on',
  'approval', 'state', 'source', 'execution_ids', 'checkpoint_run_id',
]);
const RESULT_KEYS = new Set(['artifacts', 'selected_artifact_id', 'states', 'last_seq']);
const STATE_KEYS = new Set([
  'artifact_id', 'item_id', 'revision', 'execution_id', 'state', 'actor',
  'decision', 'decision_batch_id', 'timestamp', 'reason', 'evidence',
]);

export function parseWorkflowGetResult(value: unknown): WorkflowGetResult {
  const raw = objectOf(value, 'workflow result');
  exactKeys(raw, RESULT_KEYS, 'workflow result');
  if (!Array.isArray(raw.artifacts)) throw new Error('workflow artifacts must be an array');
  if (!Array.isArray(raw.states)) throw new Error('workflow states must be an array');
  optionalString(raw.selected_artifact_id, 'selected_artifact_id');
  if (raw.last_seq !== undefined && (!Number.isSafeInteger(raw.last_seq) || (raw.last_seq as number) < 0)) {
    throw new Error('workflow last_seq must be a non-negative integer');
  }
  return {
    artifacts: raw.artifacts.map(parseWorkflowArtifact),
    states: raw.states.map(parseWorkState),
    ...(typeof raw.selected_artifact_id === 'string'
      ? { selected_artifact_id: idOf(raw.selected_artifact_id, 'selected_artifact_id') }
      : {}),
    ...(typeof raw.last_seq === 'number' ? { last_seq: raw.last_seq } : {}),
  };
}

/** Strictly validates the shared v1 fixture/wire shape without coercion. */
export function parseWorkflowArtifact(value: unknown): WorkflowArtifact {
  const raw = objectOf(value, 'plan artifact');
  exactKeys(raw, ARTIFACT_KEYS, 'plan artifact');
  if (raw.schema !== 1) throw new Error(`unsupported plan schema version ${String(raw.schema)}`);
  const artifactId = idOf(raw.artifact_id, 'artifact_id');
  const runId = idOf(raw.run_id, 'run_id');
  if (raw.kind !== 'plan' && raw.kind !== 'complex') {
    throw new Error(`invalid plan kind ${String(raw.kind)}`);
  }
  if (!Number.isInteger(raw.revision) || (raw.revision as number) < 1) {
    throw new Error('plan revision must be positive');
  }
  optionalString(raw.title, 'title');
  optionalString(raw.body, 'body');
  if (typeof raw.title === 'string' && /[\r\n]/.test(raw.title)) {
    throw new Error('plan title must be one line');
  }
  if (!Array.isArray(raw.items)) {
    throw new Error('plan items must be present');
  }
  const items = raw.items.map((item, index) => parseItem(item, index));
  const byId = new Map<string, WorkflowWorkItem>();
  for (const item of items) {
    if (byId.has(item.id)) throw new Error(`duplicate plan item id "${item.id}"`);
    byId.set(item.id, item);
  }
  for (const item of items) {
    for (const dependency of item.depends_on) {
      if (!byId.has(dependency)) {
        throw new Error(`plan item "${item.id}" depends on missing item "${dependency}"`);
      }
    }
  }
  const cycle = dependencyCycle(items);
  if (cycle.length > 0) throw new Error(`plan dependency cycle: ${cycle.join(' -> ')}`);
  return {
    schema: 1,
    artifact_id: artifactId,
    run_id: runId,
    kind: raw.kind,
    revision: raw.revision as number,
    ...(typeof raw.title === 'string' ? { title: raw.title } : {}),
    ...(typeof raw.body === 'string' ? { body: raw.body } : {}),
    items,
  };
}

function parseItem(value: unknown, index: number): WorkflowWorkItem {
  const raw = objectOf(value, `plan item ${index + 1}`);
  exactKeys(raw, ITEM_KEYS, `plan item ${index + 1}`);
  const id = idOf(raw.id, 'id');
  const title = nonEmptyString(raw.title, 'title');
  if (/[\r\n]/.test(title)) throw new Error('title must be one line');
  const goal = nonEmptyString(raw.goal, 'goal');
  const plan = stringsOf(raw.plan, 'plan');
  if (plan.length === 0) throw new Error('plan must contain at least one step');
  const files = stringsOf(raw.files, 'files');
  const acceptance = stringsOf(raw.acceptance, 'acceptance');
  if (acceptance.length === 0) throw new Error('acceptance must contain at least one check');
  const dependsOn = stringsOf(raw.depends_on, 'depends_on').map((dependency) =>
    idOf(dependency, 'dependency'));
  if (new Set(dependsOn).size !== dependsOn.length) throw new Error('duplicate dependency');
  if (dependsOn.includes(id)) throw new Error(`item "${id}" cannot depend on itself`);
  if (!APPROVALS.has(raw.approval as WorkflowApproval)) {
    throw new Error(`invalid approval ${String(raw.approval)}`);
  }
  if (!STATES.has(raw.state as WorkflowItemState)) {
    throw new Error(`invalid work state ${String(raw.state)}`);
  }
  const approval = raw.approval as WorkflowApproval;
  const state = raw.state as WorkflowItemState;
  if (approval === 'pending' && state !== 'proposed') {
    throw new Error('pending item must remain proposed');
  }
  if (approval === 'denied' && state !== 'blocked') {
    throw new Error('denied item must be blocked');
  }
  if (approval !== 'approved' && ['ready', 'running', 'completed', 'failed', 'cancelled'].includes(state)) {
    throw new Error(`${approval} item cannot be ${state}`);
  }
  if (!SOURCES.has(raw.source as WorkflowItemSource)) {
    throw new Error(`invalid work item source ${String(raw.source)}`);
  }
  const executionIds = stringsOf(raw.execution_ids, 'execution_ids').map((execution) =>
    idOf(execution, 'execution_id'));
  if (new Set(executionIds).size !== executionIds.length) throw new Error('duplicate execution id');
  optionalString(raw.checkpoint_run_id, 'checkpoint_run_id');
  const checkpoint = typeof raw.checkpoint_run_id === 'string'
    ? idOf(raw.checkpoint_run_id, 'checkpoint_run_id')
    : undefined;
  return {
    id, title, goal, plan, files, acceptance, depends_on: dependsOn,
    approval, state, source: raw.source as WorkflowItemSource,
    execution_ids: executionIds,
    ...(checkpoint !== undefined ? { checkpoint_run_id: checkpoint } : {}),
  };
}

function parseWorkState(value: unknown): WorkflowWorkState {
  const raw = objectOf(value, 'workflow state');
  exactKeys(raw, STATE_KEYS, 'workflow state');
  const state = raw.state as WorkflowItemState;
  if (!STATES.has(state)) throw new Error(`invalid work state ${String(raw.state)}`);
  if (!Number.isInteger(raw.revision) || (raw.revision as number) < 1) {
    throw new Error('work-state revision must be positive');
  }
  optionalString(raw.execution_id, 'execution_id');
  optionalString(raw.actor, 'actor');
  optionalString(raw.decision_batch_id, 'decision_batch_id');
  optionalString(raw.timestamp, 'timestamp');
  optionalString(raw.reason, 'reason');
  if (raw.decision !== undefined && raw.decision !== 'approve' && raw.decision !== 'deny') {
    throw new Error(`invalid work decision ${String(raw.decision)}`);
  }
  return {
    artifact_id: idOf(raw.artifact_id, 'artifact_id'),
    item_id: idOf(raw.item_id, 'item_id'),
    revision: raw.revision as number,
    state,
    ...(typeof raw.execution_id === 'string'
      ? { execution_id: idOf(raw.execution_id, 'execution_id') }
      : {}),
    ...(typeof raw.actor === 'string' ? { actor: raw.actor } : {}),
    ...(raw.decision === 'approve' || raw.decision === 'deny' ? { decision: raw.decision } : {}),
    ...(typeof raw.decision_batch_id === 'string'
      ? { decision_batch_id: idOf(raw.decision_batch_id, 'decision_batch_id') }
      : {}),
    ...(typeof raw.timestamp === 'string' ? { timestamp: raw.timestamp } : {}),
    ...(typeof raw.reason === 'string' ? { reason: raw.reason } : {}),
    ...(raw.evidence !== undefined ? { evidence: raw.evidence } : {}),
  };
}

function objectOf(value: unknown, field: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${field} must be an object`);
  }
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, allowed: Set<string>, field: string): void {
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) throw new Error(`${field} has unknown field "${key}"`);
  }
}

function idOf(value: unknown, field: string): string {
  if (typeof value !== 'string' || !ID.test(value)) {
    throw new Error(`${field} is not a stable identifier`);
  }
  return value;
}

function nonEmptyString(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.trim() === '') throw new Error(`${field} is required`);
  return value;
}

function optionalString(value: unknown, field: string): void {
  if (value !== undefined && typeof value !== 'string') throw new Error(`${field} must be a string`);
}

function stringsOf(value: unknown, field: string): string[] {
  if (!Array.isArray(value)) throw new Error(`${field} must be present`);
  return value.map((entry) => nonEmptyString(entry, field));
}

function dependencyCycle(items: WorkflowWorkItem[]): string[] {
  const byId = new Map(items.map((item) => [item.id, item]));
  const state = new Map<string, 'visiting' | 'visited'>();
  const stack: string[] = [];
  const visit = (id: string): string[] => {
    state.set(id, 'visiting');
    stack.push(id);
    for (const dependency of byId.get(id)?.depends_on ?? []) {
      if (state.get(dependency) === 'visiting') {
        return [...stack.slice(stack.indexOf(dependency)), dependency];
      }
      if (state.get(dependency) !== 'visited') {
        const cycle = visit(dependency);
        if (cycle.length > 0) return cycle;
      }
    }
    stack.pop();
    state.set(id, 'visited');
    return [];
  };
  for (const item of items) {
    if (!state.has(item.id)) {
      const cycle = visit(item.id);
      if (cycle.length > 0) return cycle;
    }
  }
  return [];
}
