/*
 * Stable view state for Multi/Complex orchestration.
 *
 * This file has no DOM dependency so live events and transcript replay can use
 * the same update rules, and tests can exercise out-of-order fan-out directly.
 */
(function () {
  'use strict';

  const stageOrder = { decide: 0, analyze: 1, work: 2 };

  function create() {
    return {
      nodes: new Map(),
      completedDependencies: new Set(),
      sequence: 0,
      terminal: null,
    };
  }

  function strings(value) {
    return Array.isArray(value) ? value.filter((item) => typeof item === 'string') : [];
  }

  function brief(value) {
    if (!value || typeof value !== 'object') return undefined;
    return {
      goal: typeof value.goal === 'string' ? value.goal : '',
      plan: strings(value.plan),
      files: strings(value.files),
      acceptance: strings(value.acceptance),
    };
  }

  function acceptance(value) {
    if (!value || typeof value !== 'object') return undefined;
    const items = Array.isArray(value.items)
      ? value.items
          .filter((item) => item && typeof item.text === 'string')
          .map((item) => ({
            text: item.text,
            status:
              item.status === 'met' || item.status === 'not_met'
                ? item.status
                : 'unreported',
          }))
      : [];
    return {
      items,
      changed: strings(value.changed),
      changesRecorded: value.changes_recorded === true,
    };
  }

  function update(model, event) {
    if (!model || !event || typeof event.node_id !== 'string' || event.node_id === '') {
      return undefined;
    }
    let node = model.nodes.get(event.node_id);
    if (!node) {
      node = {
        id: event.node_id,
        parentId: '',
        dependsOn: [],
        agentId: '',
        role: '',
        stage: '',
        status: model.completedDependencies.has(event.node_id) ? 'completed' : 'running',
        reason: '',
        brief: undefined,
        acceptance: undefined,
        sequence: model.sequence++,
      };
      model.nodes.set(node.id, node);
    }

    if (typeof event.parent_node_id === 'string') node.parentId = event.parent_node_id;
    if (Array.isArray(event.depends_on)) node.dependsOn = strings(event.depends_on);
    if (typeof event.agent_id === 'string') node.agentId = event.agent_id;
    if (typeof event.agent_role === 'string') node.role = event.agent_role;
    if (typeof event.stage === 'string') node.stage = event.stage;
    const nextBrief = brief(event.brief);
    if (nextBrief) node.brief = nextBrief;
    const nextAcceptance = acceptance(event.acceptance);
    if (nextAcceptance) {
      node.acceptance = nextAcceptance;
      node.status = 'completed';
      node.reason = '';
    }

    // A stage cannot start until its declared dependencies have completed.
    // Remember the inference even when a dependency's own event arrives late.
    for (const dependency of node.dependsOn) {
      model.completedDependencies.add(dependency);
      const known = model.nodes.get(dependency);
      if (known && known.status === 'running') known.status = 'completed';
    }
    return node;
  }

  function finish(model, status, reason) {
    if (!model) return;
    const terminal = status === 'cancelled' ? 'cancelled' : status === 'error' ? 'failed' : 'completed';
    model.terminal = { status: terminal, reason: typeof reason === 'string' ? reason : '' };
    for (const node of model.nodes.values()) {
      if (node.status !== 'running') continue;
      node.status = terminal;
      if (terminal !== 'completed') node.reason = model.terminal.reason;
    }
  }

  function compare(a, b) {
    const aStage = Object.prototype.hasOwnProperty.call(stageOrder, a.stage) ? stageOrder[a.stage] : 3;
    const bStage = Object.prototype.hasOwnProperty.call(stageOrder, b.stage) ? stageOrder[b.stage] : 3;
    return aStage - bStage || a.id.localeCompare(b.id) || a.sequence - b.sequence;
  }

  function ordered(model) {
    const nodes = Array.from(model.nodes.values());
    const byId = new Map(nodes.map((node) => [node.id, node]));
    const incoming = new Map(nodes.map((node) => [node.id, 0]));
    const outgoing = new Map(nodes.map((node) => [node.id, []]));
    for (const node of nodes) {
      for (const dependency of node.dependsOn) {
        if (!byId.has(dependency)) continue;
        incoming.set(node.id, incoming.get(node.id) + 1);
        outgoing.get(dependency).push(node.id);
      }
    }
    const ready = nodes.filter((node) => incoming.get(node.id) === 0).sort(compare);
    const result = [];
    while (ready.length > 0) {
      const node = ready.shift();
      result.push(node);
      for (const nextId of outgoing.get(node.id)) {
        incoming.set(nextId, incoming.get(nextId) - 1);
        if (incoming.get(nextId) === 0) {
          ready.push(byId.get(nextId));
          ready.sort(compare);
        }
      }
    }
    // Malformed cycles remain visible in their stable arrival/stage order.
    if (result.length !== nodes.length) {
      const emitted = new Set(result.map((node) => node.id));
      result.push(...nodes.filter((node) => !emitted.has(node.id)).sort(compare));
    }
    return result;
  }

  function levelOf(node, byId) {
    let level = 0;
    let parent = node.parentId;
    const seen = new Set([node.id]);
    while (parent && byId.has(parent) && !seen.has(parent) && level < 8) {
      seen.add(parent);
      level += 1;
      parent = byId.get(parent).parentId;
    }
    return level;
  }

  function snapshot(model) {
    const nodes = ordered(model);
    const byId = new Map(nodes.map((node) => [node.id, node]));
    return nodes.map((node) => ({
      id: node.id,
      parentId: node.parentId,
      dependsOn: [...node.dependsOn],
      agentId: node.agentId,
      role: node.role,
      stage: node.stage,
      status: node.status,
      reason: node.reason,
      brief: node.brief,
      acceptance: node.acceptance,
      level: levelOf(node, byId),
    }));
  }

  globalThis.anvilStages = { create, update, finish, snapshot };
})();
