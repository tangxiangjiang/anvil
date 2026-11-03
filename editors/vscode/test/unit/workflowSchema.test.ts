import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import { parseWorkflowArtifact, parseWorkflowGetResult } from '../../src/workflowSchema';

const FIXTURE = join(
  __dirname, '..', '..', '..', '..', '..',
  'doc', 'fixtures', 'plan-schema-v1.json',
);

function fixture(): unknown {
  return JSON.parse(readFileSync(FIXTURE, 'utf8')) as unknown;
}

test('the shared plan fixture is consumed without coercion or editor fields', () => {
  const raw = fixture();
  const artifact = parseWorkflowArtifact(raw);
  assert.deepEqual(artifact, raw);
  assert.equal(artifact.schema, 1);
  assert.equal(artifact.items[1].depends_on[0], 'prepare');
});

test('the TypeScript boundary refuses unknown versions and fields', () => {
  const version = fixture() as Record<string, unknown>;
  version.schema = 2;
  assert.throws(() => parseWorkflowArtifact(version), /unsupported plan schema version/);

  const field = fixture() as Record<string, unknown>;
  field.editorOnly = true;
  assert.throws(() => parseWorkflowArtifact(field), /unknown field "editorOnly"/);
});

test('the TypeScript boundary refuses missing dependencies and cycles', () => {
  const missing = fixture() as { items: Array<Record<string, unknown>> };
  missing.items[1].depends_on = ['absent'];
  assert.throws(() => parseWorkflowArtifact(missing), /depends on missing item/);

  const cycle = fixture() as { items: Array<Record<string, unknown>> };
  cycle.items[0].depends_on = ['build'];
  assert.throws(() => parseWorkflowArtifact(cycle), /dependency cycle/);
});

test('the TypeScript boundary refuses executable pending work', () => {
  const raw = fixture() as { items: Array<Record<string, unknown>> };
  raw.items[1].state = 'running';
  assert.throws(() => parseWorkflowArtifact(raw), /pending item must remain proposed/);
});

test('the workflow projection preserves audit details without coercion', () => {
  const artifact = fixture();
  const raw = {
    artifacts: [artifact],
    selected_artifact_id: 'artifact-release-1',
    states: [{
      artifact_id: 'artifact-release-1',
      item_id: 'prepare',
      revision: 2,
      execution_id: 'run-prepare',
      state: 'completed',
      actor: 'local-user',
      decision: 'approve',
      decision_batch_id: 'decision-batch-1',
      timestamp: '2026-11-02T10:00:00Z',
      reason: 'reviewed',
      evidence: { checks: 3, passed: true },
    }],
    last_seq: 9,
  };
  assert.deepEqual(parseWorkflowGetResult(raw), raw);
});

test('the workflow projection refuses unknown state fields and invalid revisions', () => {
  const state = {
    artifact_id: 'artifact-release-1', item_id: 'prepare',
    revision: 0, state: 'ready', editorOnly: true,
  };
  assert.throws(
    () => parseWorkflowGetResult({ artifacts: [fixture()], states: [state] }),
    /unknown field "editorOnly"/,
  );
  delete (state as { editorOnly?: boolean }).editorOnly;
  assert.throws(
    () => parseWorkflowGetResult({ artifacts: [fixture()], states: [state] }),
    /revision must be positive/,
  );
});
