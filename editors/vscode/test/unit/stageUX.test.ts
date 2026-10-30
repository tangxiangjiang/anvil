import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { HostEvent, TranscriptEntry } from '../../src/protocol';
import { replayOf } from '../../src/transcript';

const ROOT = join(__dirname, '..', '..', '..');
const MODEL_PATH = join(ROOT, 'media', 'stageModel.js');
const CHAT = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');
const CSS = readFileSync(join(ROOT, 'media', 'chat.css'), 'utf8');
const PANEL = readFileSync(join(ROOT, 'src', 'chatPanel.ts'), 'utf8');
const FIXTURE = JSON.parse(
  readFileSync(join(ROOT, 'test', 'fixtures', 'complex-stages.json'), 'utf8'),
) as { run_id: string; events: HostEvent[] };

// A plain browser script exposing one testable global, like meter.js/history.js.
// eslint-disable-next-line @typescript-eslint/no-require-imports
require(MODEL_PATH);

interface StageSnapshot {
  id: string;
  parentId: string;
  dependsOn: string[];
  status: 'running' | 'completed' | 'failed' | 'cancelled';
  reason: string;
  level: number;
  brief?: { goal: string; plan: string[]; files: string[]; acceptance: string[] };
  acceptance?: {
    items: Array<{ text: string; status: 'met' | 'not_met' | 'unreported' }>;
    changed: string[];
    changesRecorded: boolean;
  };
}

interface StageModel {
  nodes: Map<string, unknown>;
}

interface StageMath {
  create(): StageModel;
  update(model: StageModel, event: HostEvent): void;
  finish(model: StageModel, status: string, reason?: string): void;
  snapshot(model: StageModel): StageSnapshot[];
}

const stages = (globalThis as typeof globalThis & { anvilStages: StageMath }).anvilStages;

function modelOf(events: HostEvent[]): StageModel {
  const model = stages.create();
  for (const event of events) stages.update(model, event);
  return model;
}

test('out-of-order Complex fan-out is keyed, nested, and dependency ordered', () => {
  const snapshot = stages.snapshot(modelOf(FIXTURE.events));
  assert.deepEqual(
    snapshot.map((node) => node.id),
    ['stage:decide', 'stage:analyze', 'work:a', 'work:b', 'work:c'],
  );
  assert.deepEqual(
    snapshot.map((node) => node.level),
    [0, 1, 2, 2, 2],
  );
  assert.deepEqual(snapshot.find((node) => node.id === 'work:b')?.dependsOn, [
    'stage:analyze',
    'work:a',
  ]);
  assert.equal(snapshot.find((node) => node.id === 'stage:decide')?.status, 'completed');
  assert.equal(snapshot.find((node) => node.id === 'stage:analyze')?.status, 'completed');
});

test('brief and worker-reported acceptance remain structured facts', () => {
  const snapshot = stages.snapshot(modelOf(FIXTURE.events));
  const worker = snapshot.find((node) => node.id === 'work:b');
  assert.deepEqual(worker?.brief, {
    goal: 'Update B after A',
    files: ['b.ts'],
    plan: ['Read A output', 'Update B', 'Run B tests'],
    acceptance: ['B uses A', 'B tests pass'],
  });
  assert.deepEqual(worker?.acceptance, {
    items: [
      { text: 'B uses A', status: 'not_met' },
      { text: 'B tests pass', status: 'unreported' },
    ],
    changed: ['b.ts'],
    changesRecorded: true,
  });
  assert.equal(
    stages.snapshot(modelOf(FIXTURE.events)).find((node) => node.id === 'work:c')
      ?.acceptance?.changesRecorded,
    false,
  );
});

test('live events and transcript stage entries produce the same view state', () => {
  const entries: TranscriptEntry[] = FIXTURE.events.map((event) => ({
    kind: 'stage',
    run_id: event.run_id,
    node_id: event.node_id,
    parent_node_id: event.parent_node_id,
    depends_on: event.depends_on,
    agent_id: event.agent_id,
    agent_role: event.agent_role,
    stage: event.stage,
    brief: event.brief,
    acceptance: event.acceptance,
  }));
  const replayed = replayOf(entries, 'session-replay')
    .filter((step) => step.kind === 'event')
    .map((step) => step.event);
  assert.deepEqual(stages.snapshot(modelOf(replayed)), stages.snapshot(modelOf(FIXTURE.events)));
});

test('failed and cancelled terminals preserve completed workers and explain partial stages', () => {
  const starts = FIXTURE.events.slice(0, 6);
  const failed = modelOf(starts);
  stages.finish(failed, 'error', 'worker b failed');
  const failedSnapshot = stages.snapshot(failed);
  assert.equal(failedSnapshot.find((node) => node.id === 'work:a')?.status, 'completed');
  assert.equal(failedSnapshot.find((node) => node.id === 'work:b')?.status, 'failed');
  assert.equal(failedSnapshot.find((node) => node.id === 'work:b')?.reason, 'worker b failed');

  const cancelled = modelOf(FIXTURE.events.slice(0, 5));
  stages.finish(cancelled, 'cancelled', 'Stopped by user.');
  assert.equal(stages.snapshot(cancelled).find((node) => node.id === 'work:b')?.status, 'cancelled');
});

test('stage UI is collapsible, accessible, and labels claims rather than verification', () => {
  assert.match(PANEL, /stageModel\.js/);
  assert.match(CHAT, /el\('details', 'agent-stages'\)/);
  assert.match(CHAT, /el\('details', 'stage-node level-'/);
  assert.match(CHAT, /setAttribute\('role', 'list'\)/);
  assert.match(CHAT, /setAttribute\('role', 'listitem'\)/);
  assert.match(CHAT, /Worker-reported acceptance/);
  assert.match(CHAT, /'Worker report: ' \+ status/);
  assert.match(CHAT, /Changes not recorded\./);
  assert.match(CHAT, /event\.name !== 'run_workflow' && event\.name !== 'delegate'/);
  assert.match(CHAT, /textContent =/);
  assert.doesNotMatch(CHAT, /renderAcceptance[\s\S]{0,2400}innerHTML/);
  assert.match(CSS, /\.agent-stages/);
  assert.match(CSS, /\.acceptance-item\.not_met/);
  assert.match(CSS, /\.stage-terminal-reason/);
});
