import * as vscode from 'vscode';
import {
  SessionInfo,
  WorkflowArtifact,
  WorkflowGetResult,
  WorkflowWorkItem,
  WorktreeListParams,
  WorktreeListResult,
  WorktreeWorkerInfo,
} from './protocol';
import { HostRpcError } from './hostClient';
import {
  WorkflowSelection,
  latestWorkflowState,
  workflowEvidenceLabel,
  workflowMatches,
} from './workflowView';

export interface WorkflowSource {
  listSessions(): Promise<SessionInfo[]>;
  workflow(agentId: string, artifactId?: string): Promise<WorkflowGetResult>;
  listWorktrees?(params: WorktreeListParams): Promise<WorktreeListResult>;
  openSession(agentId: string): Promise<void>;
  onSessionsChanged(listener: () => void): vscode.Disposable;
}

export interface WorkflowSessionNode {
  kind: 'session';
  agentId: string;
  label: string;
}

export interface WorkflowArtifactNode {
  kind: 'artifact';
  agentId: string;
  artifact: WorkflowArtifact;
}

export interface WorkflowItemNode {
  kind: 'item';
  agentId: string;
  artifact: WorkflowArtifact;
  item: WorkflowWorkItem;
}

export interface WorkflowWorktreeNode {
  kind: 'worktree';
  agentId: string;
  worker: WorktreeWorkerInfo;
}

interface WorkflowLeafNode {
  kind: 'leaf';
  id: string;
  label: string;
  description?: string;
  icon?: string;
}

interface WorkflowNoteNode {
  kind: 'note';
  id: string;
  label: string;
}

export type WorkflowTreeNode =
  | WorkflowSessionNode
  | WorkflowArtifactNode
  | WorkflowItemNode
  | WorkflowWorktreeNode
  | WorkflowLeafNode
  | WorkflowNoteNode;

const SETTLE_MS = 400;

export class WorkflowTreeProvider implements vscode.TreeDataProvider<WorkflowTreeNode>, vscode.Disposable {
  static readonly viewType = 'anvil.workflow';

  private readonly changed = new vscode.EventEmitter<WorkflowTreeNode | undefined>();
  readonly onDidChangeTreeData = this.changed.event;
  private sessions: SessionInfo[] | undefined;
  private sessionsFailure = '';
  private readonly projections = new Map<string, WorkflowGetResult>();
  private readonly failures = new Map<string, string>();
  private readonly worktrees = new Map<string, WorktreeWorkerInfo[]>();
  private selection: { agentId: string; artifactId: string; itemIds: Set<string> } | undefined;
  private filter = '';
  private timer: ReturnType<typeof setTimeout> | undefined;
  private readonly sessionWatch: vscode.Disposable;

  constructor(private readonly source: WorkflowSource) {
    this.sessionWatch = source.onSessionsChanged(() => this.refreshSoon());
  }

  getTreeItem(node: WorkflowTreeNode): vscode.TreeItem {
    switch (node.kind) {
      case 'session': {
        const item = new vscode.TreeItem(node.label, vscode.TreeItemCollapsibleState.Collapsed);
        item.id = `workflow-session:${node.agentId}`;
        item.contextValue = 'anvilWorkflowSession';
        item.iconPath = new vscode.ThemeIcon('comment-discussion');
        item.tooltip = node.agentId;
        return item;
      }
      case 'artifact': {
        const item = new vscode.TreeItem(
          node.artifact.title?.trim() || node.artifact.artifact_id,
          vscode.TreeItemCollapsibleState.Expanded,
        );
        item.id = `workflow-artifact:${node.agentId}:${node.artifact.artifact_id}`;
        item.contextValue = 'anvilWorkflowArtifact';
        item.description = `${node.artifact.kind} · r${node.artifact.revision}`;
        item.iconPath = new vscode.ThemeIcon('checklist');
        item.tooltip = `${node.artifact.artifact_id}\nOriginating run: ${node.artifact.run_id}`;
        item.command = {
          command: 'anvil.workflow.openArtifact',
          title: 'Open plan',
          arguments: [node],
        };
        return item;
      }
      case 'item': {
        const treeItem = new vscode.TreeItem(node.item.title, vscode.TreeItemCollapsibleState.Collapsed);
        treeItem.id = `workflow-item:${node.agentId}:${node.artifact.artifact_id}:${node.item.id}`;
        treeItem.contextValue = `anvilWorkflowItem.${node.item.approval}`;
        treeItem.description = `${node.item.approval} · ${node.item.state}`;
        treeItem.iconPath = new vscode.ThemeIcon(itemIcon(node.item.state));
        treeItem.tooltip = `${node.item.goal}\nRevision ${node.artifact.revision}`;
        if (node.item.approval === 'pending') {
          treeItem.checkboxState = this.isSelected(node)
            ? vscode.TreeItemCheckboxState.Checked
            : vscode.TreeItemCheckboxState.Unchecked;
        }
        return treeItem;
      }
      case 'worktree': {
        const item = new vscode.TreeItem(
          node.worker.worktree_label || node.worker.id,
          vscode.TreeItemCollapsibleState.Collapsed,
        );
        item.id = `workflow-worktree:${node.agentId}:${node.worker.id}`;
        item.contextValue = `anvilWorktree.${node.worker.state}`;
        item.description = `${node.worker.state} · r${node.worker.revision}`;
        item.iconPath = new vscode.ThemeIcon(worktreeIcon(node.worker.state));
        item.tooltip = [
          node.worker.id,
          `Base ${shortCommit(node.worker.base_commit)}`,
          node.worker.failure,
        ].filter(Boolean).join('\n');
        return item;
      }
      case 'leaf': {
        const item = new vscode.TreeItem(node.label, vscode.TreeItemCollapsibleState.None);
        item.id = node.id;
        item.contextValue = 'anvilWorkflowFact';
        item.description = node.description;
        item.iconPath = node.icon ? new vscode.ThemeIcon(node.icon) : undefined;
        item.tooltip = [node.label, node.description].filter(Boolean).join('\n');
        return item;
      }
      case 'note': {
        const item = new vscode.TreeItem(node.label, vscode.TreeItemCollapsibleState.None);
        item.id = node.id;
        item.contextValue = 'anvilWorkflowNote';
        item.iconPath = new vscode.ThemeIcon('info');
        return item;
      }
    }
  }

  async getChildren(element?: WorkflowTreeNode): Promise<WorkflowTreeNode[]> {
    if (!element) {
      if (!vscode.workspace.isTrusted) {
        return [note('untrusted', 'Trust this workspace to inspect its workflows.')];
      }
      await this.loadSessions();
      if (this.sessionsFailure) {
        return [note('sessions-error', `Could not load sessions: ${this.sessionsFailure}`)];
      }
      if (!this.sessions || this.sessions.length === 0) {
        return [note('empty-sessions', 'No stored sessions.')];
      }
      return this.sessions.map((session) => ({
        kind: 'session' as const,
        agentId: session.agent_id,
        label: session.title?.trim() || session.agent_id,
      }));
    }
    if (element.kind === 'session') {
      const projection = await this.loadWorkflow(element.agentId);
      if (!projection) {
        return [note(`error:${element.agentId}`, this.failures.get(element.agentId) ?? 'Workflow unavailable.')];
      }
      const artifacts = projection.artifacts.filter((artifact) =>
        workflowMatches(artifact, undefined, this.filter) ||
        artifact.items.some((item) => workflowMatches(artifact, item, this.filter)));
      const workers = await this.loadWorktrees(element.agentId);
      if (artifacts.length === 0 && workers.length === 0) {
        return [note(
          `empty:${element.agentId}`,
          this.filter ? 'No workflow items match the filter.' : 'No structured plans in this session.',
        )];
      }
      return [
        ...workers.map((worker) => ({
          kind: 'worktree' as const, agentId: element.agentId, worker,
        })),
        ...artifacts.map((artifact) => ({
          kind: 'artifact' as const, agentId: element.agentId, artifact,
        })),
      ];
    }
    if (element.kind === 'artifact') {
      const items = element.artifact.items
        .filter((item) => workflowMatches(element.artifact, item, this.filter))
        .map((item) => ({
          kind: 'item' as const,
          agentId: element.agentId,
          artifact: element.artifact,
          item,
        }));
      if (items.length === 0) {
        return [note(
          `prose:${element.agentId}:${element.artifact.artifact_id}`,
          'Prose-only plan; open it to review the full body.',
        )];
      }
      return items;
    }
    if (element.kind === 'item') return this.itemFacts(element);
    if (element.kind === 'worktree') return this.worktreeFacts(element);
    return [];
  }

  toggle(node: WorkflowTreeNode, checked: boolean): void {
    if (node.kind !== 'item' || node.item.approval !== 'pending') return;
    const key = `${node.agentId}\0${node.artifact.artifact_id}`;
    const currentKey = this.selection
      ? `${this.selection.agentId}\0${this.selection.artifactId}`
      : '';
    if (!this.selection || currentKey !== key) {
      this.selection = {
        agentId: node.agentId,
        artifactId: node.artifact.artifact_id,
        itemIds: new Set(),
      };
    }
    if (checked) this.selection.itemIds.add(node.item.id);
    else this.selection.itemIds.delete(node.item.id);
    if (this.selection.itemIds.size === 0) this.selection = undefined;
    this.changed.fire(undefined);
  }

  selected(): WorkflowSelection | undefined {
    if (!this.selection) return undefined;
    const projection = this.projections.get(this.selection.agentId);
    const artifact = projection?.artifacts.find(
      (candidate) => candidate.artifact_id === this.selection?.artifactId,
    );
    if (!artifact) return undefined;
    return {
      agentId: this.selection.agentId,
      artifact,
      itemIds: [...this.selection.itemIds],
    };
  }

  clearSelection(): void {
    this.selection = undefined;
    this.changed.fire(undefined);
  }

  async refresh(): Promise<void> {
    this.sessions = undefined;
    this.sessionsFailure = '';
    this.projections.clear();
    this.failures.clear();
    this.worktrees.clear();
    this.selection = undefined;
    await this.loadSessions();
    this.changed.fire(undefined);
  }

  async refreshAgent(agentId: string): Promise<void> {
    this.projections.delete(agentId);
    this.failures.delete(agentId);
    this.worktrees.delete(agentId);
    await this.loadWorkflow(agentId);
    this.changed.fire(undefined);
  }

  setFilter(value: string): void {
    this.filter = value.trim();
    this.selection = undefined;
    this.changed.fire(undefined);
  }

  dispose(): void {
    if (this.timer) clearTimeout(this.timer);
    this.sessionWatch.dispose();
    this.changed.dispose();
  }

  private async loadSessions(): Promise<void> {
    if (this.sessions !== undefined) return;
    try {
      this.sessions = await this.source.listSessions();
      this.sessionsFailure = '';
    } catch (err) {
      this.sessions = [];
      this.sessionsFailure = (err as Error).message;
    }
  }

  private async loadWorkflow(agentId: string): Promise<WorkflowGetResult | undefined> {
    const cached = this.projections.get(agentId);
    if (cached) return cached;
    try {
      const projection = await this.source.workflow(agentId);
      this.projections.set(agentId, projection);
      this.failures.delete(agentId);
      return projection;
    } catch (err) {
      const message = (err as Error).message;
      this.failures.set(
        agentId,
        err instanceof HostRpcError && err.code === -32601
          ? 'This Host does not support the Workflow view.'
          : message,
      );
      return undefined;
    }
  }

  private async loadWorktrees(agentId: string): Promise<WorktreeWorkerInfo[]> {
    const cached = this.worktrees.get(agentId);
    if (cached) return cached;
    const root = vscode.workspace.getConfiguration('anvil').get<string>('worktreeRoot', '').trim();
    if (!root || !this.source.listWorktrees) {
      this.worktrees.set(agentId, []);
      return [];
    }
    try {
      const result = await this.source.listWorktrees({
        agent_id: agentId, worktree_root: root,
      });
      const workers = result.workers ?? [];
      this.worktrees.set(agentId, workers);
      return workers;
    } catch (err) {
      this.failures.set(agentId, (err as Error).message);
      this.worktrees.set(agentId, []);
      return [];
    }
  }

  private itemFacts(node: WorkflowItemNode): WorkflowTreeNode[] {
    const projection = this.projections.get(node.agentId);
    const state = latestWorkflowState(
      projection?.states ?? [], node.artifact.artifact_id, node.item.id,
    );
    const facts: WorkflowTreeNode[] = [
      leaf(node, 'goal', 'Goal', node.item.goal, 'target'),
      ...node.item.depends_on.map((dependency) => {
        const dep = node.artifact.items.find((item) => item.id === dependency);
        return leaf(
          node, `dependency:${dependency}`, `Requires ${dep?.title ?? dependency}`,
          dep?.state ?? 'unavailable', 'references',
        );
      }),
      ...node.item.acceptance.map((check, index) =>
        leaf(node, `acceptance:${index}`, `Acceptance ${index + 1}`, check, 'pass')),
      ...node.item.files.map((file, index) =>
        leaf(node, `file:${index}`, file, 'write boundary', 'file')),
    ];
    if (state?.reason) {
      facts.push(leaf(node, 'reason', 'Status reason', state.reason, 'issues'));
    }
    const evidence = state ? workflowEvidenceLabel(state) : '';
    if (evidence) facts.push(leaf(node, 'evidence', 'Evidence', evidence, 'verified'));
    for (const executionId of node.item.execution_ids) {
      facts.push(leaf(node, `execution:${executionId}`, executionId, 'execution', 'run'));
    }
    return facts;
  }

  private worktreeFacts(node: WorkflowWorktreeNode): WorkflowTreeNode[] {
    const review = node.worker.review;
    const facts: WorkflowTreeNode[] = [
      {
        kind: 'leaf', id: `worktree:${node.worker.id}:base`,
        label: `Base ${shortCommit(node.worker.base_commit)}`,
        description: review ? `reviewed at ${shortCommit(review.current_base)}` : 'recorded base',
        icon: 'git-commit',
      },
      {
        kind: 'leaf', id: `worktree:${node.worker.id}:head`,
        label: `Head ${shortCommit(node.worker.head_commit ?? node.worker.base_commit)}`,
        description: node.worker.integration_commit
          ? `integrated as ${shortCommit(node.worker.integration_commit)}`
          : 'worker branch',
        icon: 'git-branch',
      },
    ];
    if (review) {
      facts.push({
        kind: 'leaf', id: `worktree:${node.worker.id}:review`,
        label: review.integrable ? 'Ready to integrate' : 'Integration blocked',
        description: review.integrable
          ? review.allowed_strategies.join(', ')
          : review.refusal_reasons.join('; '),
        icon: review.integrable ? 'pass-filled' : 'warning',
      });
      for (const conflict of review.conflicts) {
        facts.push({
          kind: 'leaf', id: `worktree:${node.worker.id}:conflict:${conflict.path}`,
          label: conflict.path, description: conflict.kind, icon: 'merge',
        });
      }
    }
    if (node.worker.integration_phase) {
      facts.push({
        kind: 'leaf', id: `worktree:${node.worker.id}:integration`,
        label: `Integration ${node.worker.integration_phase}`,
        description: node.worker.integration_failure,
        icon: node.worker.integration_phase === 'recovery_required' ? 'warning' : 'git-merge',
      });
    }
    if (node.worker.failure) {
      facts.push({
        kind: 'leaf', id: `worktree:${node.worker.id}:failure`,
        label: 'Blocked', description: node.worker.failure, icon: 'error',
      });
    }
    return facts;
  }

  private isSelected(node: WorkflowItemNode): boolean {
    return this.selection?.agentId === node.agentId &&
      this.selection.artifactId === node.artifact.artifact_id &&
      this.selection.itemIds.has(node.item.id);
  }

  private refreshSoon(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = undefined;
      this.sessions = undefined;
      this.projections.clear();
      this.failures.clear();
      this.worktrees.clear();
      this.selection = undefined;
      this.changed.fire(undefined);
    }, SETTLE_MS);
  }
}

function shortCommit(value: string): string {
  return value ? value.slice(0, 8) : 'unknown';
}

function worktreeIcon(state: string): string {
  switch (state) {
    case 'review_ready': return 'git-pull-request';
    case 'integrating': return 'loading~spin';
    case 'integrated': return 'pass-filled';
    case 'discarded': return 'trash';
    case 'cleanup_failed': return 'warning';
    default: return 'git-branch';
  }
}

function note(id: string, label: string): WorkflowNoteNode {
  return { kind: 'note', id: `workflow-note:${id}`, label };
}

function leaf(
  node: WorkflowItemNode,
  suffix: string,
  label: string,
  description: string,
  icon: string,
): WorkflowLeafNode {
  return {
    kind: 'leaf',
    id: `workflow-fact:${node.agentId}:${node.artifact.artifact_id}:${node.item.id}:${suffix}`,
    label,
    description,
    icon,
  };
}

function itemIcon(state: WorkflowWorkItem['state']): string {
  switch (state) {
    case 'completed': return 'pass-filled';
    case 'failed': return 'error';
    case 'running': return 'loading~spin';
    case 'blocked': return 'circle-slash';
    case 'cancelled': return 'circle-large-outline';
    case 'ready': return 'play-circle';
    default: return 'circle-outline';
  }
}
