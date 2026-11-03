import {
  WorktreeCleanupParams,
  WorktreeDiscardParams,
  WorktreeIntegrateParams,
  WorktreeReviewInfo,
  WorktreeWorkerInfo,
} from './protocol';

export function worktreeIntegrationParams(
  root: string,
  worker: WorktreeWorkerInfo,
  review: WorktreeReviewInfo,
  strategy: WorktreeIntegrateParams['strategy'],
): WorktreeIntegrateParams {
  if (!review.integrable || !review.allowed_strategies.includes(strategy)) {
    throw new Error(`review does not allow ${strategy}`);
  }
  return {
    agent_id: worker.agent_id,
    worker_id: worker.id,
    worktree_root: root,
    expected_revision: review.manifest_revision,
    expected_base: review.current_base,
    expected_head: review.head_commit,
    expected_patch_digest: review.patch_digest,
    strategy,
  };
}

export function worktreeDiscardParams(
  root: string,
  worker: WorktreeWorkerInfo,
  review: WorktreeReviewInfo,
): WorktreeDiscardParams {
  return {
    agent_id: worker.agent_id,
    worker_id: worker.id,
    worktree_root: root,
    expected_revision: review.manifest_revision,
    expected_patch_digest: review.patch_digest,
  };
}

export function worktreeCleanupParams(
  root: string,
  worker: WorktreeWorkerInfo,
): WorktreeCleanupParams {
  return {
    agent_id: worker.agent_id,
    worker_id: worker.id,
    worktree_root: root,
    expected_revision: worker.revision,
    expected_review_digest: worker.state === 'discarded'
      ? worker.review?.patch_digest
      : undefined,
  };
}

export function worktreeReviewMarkdown(
  worker: WorktreeWorkerInfo,
  review: WorktreeReviewInfo,
): string {
  const lines = [
    `# Worktree review: ${worker.worktree_label}`,
    '',
    `- State: ${review.integrable ? 'Ready to integrate' : 'Blocked'}`,
    `- Base: \`${review.base_commit}\``,
    `- Current primary: \`${review.current_base}\``,
    `- Worker head: \`${review.head_commit}\``,
    `- Review digest: \`${review.patch_digest}\``,
    '',
  ];
  if (review.refusal_reasons.length) {
    lines.push('## Blocking reasons', '', ...review.refusal_reasons.map((value) => `- ${value}`), '');
  }
  lines.push('## Commits', '');
  lines.push(...(review.commits.length
    ? review.commits.map((commit) => `- \`${commit.hash.slice(0, 12)}\` ${commit.subject}`)
    : ['- None']));
  lines.push('', '## Changed paths', '');
  lines.push(...(review.changed_paths.length
    ? review.changed_paths.map((path) => `- ${path.status} \`${path.old_path ? `${path.old_path} → ` : ''}${path.path}\``)
    : ['- None']));
  lines.push('', '## Conflicts', '');
  lines.push(...(review.conflicts.length
    ? review.conflicts.map((conflict) => `- **${conflict.kind}** \`${conflict.path}\``)
    : ['- None']));
  if (review.dirty_tracked.length || review.untracked.length || review.ignored.length) {
    lines.push('', '## Uncommitted content', '');
    lines.push(...review.dirty_tracked.map((path) => `- Dirty: \`${path}\``));
    lines.push(...review.untracked.map((path) => `- Untracked: \`${path}\``));
    lines.push(...review.ignored.map((path) => `- Ignored: \`${path}\``));
  }
  if (review.diff_stat) lines.push('', '## Diff stat', '', '```text', review.diff_stat, '```');
  return `${lines.join('\n')}\n`;
}

export function strategyLabel(
  value: WorktreeIntegrateParams['strategy'],
): string {
  switch (value) {
    case 'fast_forward': return 'Fast-forward';
    case 'merge_no_ff': return 'Merge commit';
    case 'cherry_pick': return 'Cherry-pick reviewed commits';
  }
}
