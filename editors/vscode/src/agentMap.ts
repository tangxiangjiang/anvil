import { SessionInfo, TaskInfo, Usage } from './protocol';

export type AgentMapStatus = 'working' | 'finished' | 'failed' | 'idle';

export interface AgentMapNode {
  agentId: string;
  parentAgentId?: string;
  title: string;
  model: string;
  status: AgentMapStatus;
  depth: number;
  createdAt?: string;
  endedAt?: string;
  usage?: Usage;
  background: boolean;
  taskId?: string;
}

export interface AgentMapState {
  rootId: string;
  nodes: AgentMapNode[];
  counts: { total: number; working: number; finished: number; failed: number };
}

function statusOf(session: SessionInfo, task: TaskInfo | undefined, running: ReadonlySet<string>): AgentMapStatus {
  if (running.has(session.agent_id) || task?.status === 'running') return 'working';
  const status = task?.status ?? session.last_status ?? '';
  if (status === 'failed' || status === 'error' || status === 'cancelled') return 'failed';
  if (status === 'finished' || status === 'success') return 'finished';
  return 'idle';
}

/** Builds only the selected root's descendants; malformed cycles cannot escape the visited set. */
export function buildAgentMap(
  rootId: string,
  sessions: readonly SessionInfo[],
  tasks: readonly TaskInfo[],
  running: ReadonlySet<string> = new Set(),
  rootModel = '',
): AgentMapState {
  const byId = new Map(sessions.map((session) => [session.agent_id, session]));
  const children = new Map<string, SessionInfo[]>();
  for (const session of sessions) {
    if (!session.parent_agent_id || session.parent_agent_id === session.agent_id) continue;
    const list = children.get(session.parent_agent_id) ?? [];
    list.push(session);
    children.set(session.parent_agent_id, list);
  }
  const taskByChild = new Map(
    tasks.filter((task) => task.child_agent_id).map((task) => [task.child_agent_id as string, task]),
  );
  const root = byId.get(rootId) ?? {
    agent_id: rootId,
    cwd: '',
    provider: '',
    model: '',
    runs: 0,
  };
  const nodes: AgentMapNode[] = [];
  const visited = new Set<string>();
  const visit = (session: SessionInfo, depth: number): void => {
    if (visited.has(session.agent_id)) return;
    visited.add(session.agent_id);
    const task = taskByChild.get(session.agent_id);
    nodes.push({
      agentId: session.agent_id,
      parentAgentId: session.parent_agent_id,
      title: session.task_title?.trim() || session.title?.trim() || session.agent_id,
      // The root is the open conversation, whose picker may have changed the
      // model since the session listing was persisted. Children have their own
      // sessions and must keep reporting those models.
      model: depth === 0 && rootModel.trim() !== ''
        ? rootModel
        : session.model || 'default model',
      status: statusOf(session, task, running),
      depth,
      createdAt: task?.started_at ?? session.created_at,
      endedAt: task?.ended_at ?? session.updated_at,
      usage: session.usage,
      background: task !== undefined,
      taskId: task?.id,
    });
    for (const child of children.get(session.agent_id) ?? []) visit(child, depth + 1);
  };
  visit(root, 0);
  const counts = {
    total: Math.max(0, nodes.length - 1),
    working: nodes.filter((node) => node.depth > 0 && node.status === 'working').length,
    finished: nodes.filter((node) => node.depth > 0 && node.status === 'finished').length,
    failed: nodes.filter((node) => node.depth > 0 && node.status === 'failed').length,
  };
  return { rootId, nodes, counts };
}
