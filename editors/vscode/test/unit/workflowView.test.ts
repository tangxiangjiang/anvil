import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { WorkflowArtifact, WorkflowWorkState } from '../../src/protocol';
import {
  latestWorkflowState,
  workflowDecisionParams,
  workflowEvidenceLabel,
  workflowMatches,
} from '../../src/workflowView';

const artifact: WorkflowArtifact = {
  schema: 1,
  artifact_id: 'artifact-1',
  run_id: 'run-1',
  kind: 'complex',
  revision: 7,
  title: 'Release workflow',
  items: [{
    id: 'build', title: 'Build package', goal: 'Produce VSIX',
    plan: ['build'], files: ['package.json'], acceptance: ['VSIX exists'],
    depends_on: [], approval: 'pending', state: 'proposed', source: 'complex',
    execution_ids: [],
  }],
};

test('workflow filtering searches artifact and item fields by every term', () => {
  assert.equal(workflowMatches(artifact, artifact.items[0], 'release package'), true);
  assert.equal(workflowMatches(artifact, artifact.items[0], 'vsix build'), true);
  assert.equal(workflowMatches(artifact, artifact.items[0], 'release missing'), false);
  assert.equal(workflowMatches(artifact, undefined, 'release'), true);
});

test('workflow state selection takes the newest matching durable record', () => {
  const states: WorkflowWorkState[] = [
    { artifact_id: 'artifact-1', item_id: 'build', revision: 1, state: 'proposed' },
    { artifact_id: 'other', item_id: 'build', revision: 9, state: 'failed' },
    {
      artifact_id: 'artifact-1', item_id: 'build', revision: 2, state: 'blocked',
      decision: 'deny', reason: 'unsafe', timestamp: '2026-11-02T10:00:00Z',
      evidence: { check: false },
    },
  ];
  const latest = latestWorkflowState(states, 'artifact-1', 'build');
  assert.equal(latest?.revision, 2);
  assert.equal(latest?.reason, 'unsafe');
  assert.equal(workflowEvidenceLabel(latest!), '{"check":false}');
});

test('workflow decisions carry the displayed revision and a deterministic subset', () => {
  assert.deepEqual(workflowDecisionParams({
    agentId: 'agent-1', artifact, itemIds: ['z', 'a'],
  }, 'approve'), {
    agent_id: 'agent-1',
    artifact_id: 'artifact-1',
    expected_revision: 7,
    decisions: [
      { item_id: 'a', decision: 'approve' },
      { item_id: 'z', decision: 'approve' },
    ],
  });
  assert.deepEqual(workflowDecisionParams({
    agentId: 'agent-1', artifact, itemIds: ['build'],
  }, 'deny', '  unsafe boundary  ').decisions, [{
    item_id: 'build', decision: 'deny', reason: 'unsafe boundary',
  }]);
});
