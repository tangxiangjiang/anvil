import {
  WorkflowArtifact,
  WorkflowDecision,
  WorkflowDecideParams,
  WorkflowWorkItem,
  WorkflowWorkState,
} from './protocol';

export interface WorkflowSelection {
  agentId: string;
  artifact: WorkflowArtifact;
  itemIds: string[];
}

export function workflowMatches(
  artifact: WorkflowArtifact,
  item: WorkflowWorkItem | undefined,
  query: string,
): boolean {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return true;
  const text = [
    artifact.artifact_id, artifact.title ?? '', artifact.kind,
    item?.id ?? '', item?.title ?? '', item?.goal ?? '',
  ].join(' ').toLocaleLowerCase();
  return terms.every((term) => text.includes(term));
}

export function latestWorkflowState(
  states: WorkflowWorkState[],
  artifactId: string,
  itemId: string,
): WorkflowWorkState | undefined {
  for (let i = states.length - 1; i >= 0; i--) {
    const state = states[i];
    if (state.artifact_id === artifactId && state.item_id === itemId) return state;
  }
  return undefined;
}

export function workflowDecisionParams(
  selection: WorkflowSelection,
  decision: 'approve' | 'deny',
  reason?: string,
): WorkflowDecideParams {
  const decisions: WorkflowDecision[] = [...selection.itemIds]
    .sort()
    .map((itemId) => ({
      item_id: itemId,
      decision,
      ...(decision === 'deny' ? { reason: reason?.trim() ?? '' } : {}),
    }));
  return {
    agent_id: selection.agentId,
    artifact_id: selection.artifact.artifact_id,
    expected_revision: selection.artifact.revision,
    decisions,
  };
}

export function workflowEvidenceLabel(state: WorkflowWorkState): string {
  if (state.evidence === undefined) return '';
  try {
    const text = JSON.stringify(state.evidence);
    return text.length <= 120 ? text : `${text.slice(0, 119)}…`;
  } catch {
    return 'Evidence unavailable';
  }
}
