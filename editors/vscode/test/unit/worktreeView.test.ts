import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import { WorktreeReviewInfo, WorktreeWorkerInfo } from '../../src/protocol';
import {
  worktreeCleanupParams,
  worktreeDiscardParams,
  worktreeIntegrationParams,
  worktreeReviewMarkdown,
} from '../../src/worktreeView';

const worker: WorktreeWorkerInfo = {
  id: 'w-123', agent_id: 'agent-1', artifact_id: 'artifact-1',
  work_item_id: 'item-1', execution_id: 'exec-1', revision: 9,
  state: 'review_ready', worktree_label: 'wt-w-123',
  base_commit: 'base', head_commit: 'head',
};

const review: WorktreeReviewInfo = {
  schema: 1, worker_id: worker.id, manifest_revision: 9,
  base_commit: 'base', current_base: 'current', head_commit: 'head',
  commits: [{ hash: '1234567890abcdef', subject: 'reviewed change' }],
  changed_paths: [{ status: 'M', path: 'src/a.ts' }],
  patch_digest: 'digest', dirty_tracked: [], untracked: [], ignored: [],
  conflicts: [], allowed_strategies: ['merge_no_ff'], integrable: true,
  refusal_reasons: [], interrupted: false, reviewed_at: '2026-11-03T00:00:00Z',
};

test('integration request binds the displayed revision, base, head, and digest', () => {
  assert.deepEqual(worktreeIntegrationParams('C:\\workers', worker, review, 'merge_no_ff'), {
    agent_id: 'agent-1', worker_id: 'w-123', worktree_root: 'C:\\workers',
    expected_revision: 9, expected_base: 'current', expected_head: 'head',
    expected_patch_digest: 'digest', strategy: 'merge_no_ff',
  });
  assert.throws(
    () => worktreeIntegrationParams('C:\\workers', worker, review, 'fast_forward'),
    /does not allow/,
  );
});

test('discard and cleanup remain separate explicit requests', () => {
  assert.deepEqual(worktreeDiscardParams('C:\\workers', worker, review), {
    agent_id: 'agent-1', worker_id: 'w-123', worktree_root: 'C:\\workers',
    expected_revision: 9, expected_patch_digest: 'digest',
  });
  const discarded = { ...worker, state: 'discarded', review };
  assert.deepEqual(worktreeCleanupParams('C:\\workers', discarded), {
    agent_id: 'agent-1', worker_id: 'w-123', worktree_root: 'C:\\workers',
    expected_revision: 9, expected_review_digest: 'digest',
  });
});

test('conflicts and blocked details render without issuing a mutation', () => {
  const blocked: WorktreeReviewInfo = {
    ...review,
    integrable: false,
    allowed_strategies: [],
    refusal_reasons: ['merge preflight found conflicts'],
    conflicts: [{ path: 'src/a.ts', kind: 'content' }],
  };
  const markdown = worktreeReviewMarkdown(worker, blocked);
  assert.match(markdown, /Integration blocked|Blocked/);
  assert.match(markdown, /merge preflight found conflicts/);
  assert.match(markdown, /\*\*content\*\* `src\/a\.ts`/);
});
