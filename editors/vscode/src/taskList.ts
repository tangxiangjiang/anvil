/**
 * What the task table can say about a task without asking the host again.
 *
 * `tasksPanel.ts` needs `vscode` for its document and its messages; this half
 * needs only the two wire shapes, so it is where the matching can be tested.
 * The split is the same one `sessionList.ts` and `sessionTree.ts` sit on.
 */

import { SessionInfo, TaskInfo } from './protocol';

/**
 * The stored child a background subagent task wrote.
 *
 * `TaskInfo.agent_id` is the agent whose call started the task — the parent —
 * not the child's own id, so the child is found by pairing the parent with the
 * task's description. `task_title` is that description as stored, and the task
 * row prefixes it with "subagent " so the two are not the same string. An
 * absent match is a normal answer: the transcript may not have been written
 * yet, and a button that cannot go anywhere is left off rather than disabled.
 */
export function transcriptFor(task: TaskInfo, sessions: SessionInfo[]): string | undefined {
  if (task.kind !== 'agent' || !task.agent_id) return undefined;
  const title = task.title.replace(/^subagent /, '');
  const children = sessions.filter(
    (session) => session.subagent === true && session.parent_agent_id === task.agent_id,
  );
  return children.find((session) => (session.task_title ?? '') === title)?.agent_id;
}
