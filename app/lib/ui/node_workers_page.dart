import 'package:flutter/material.dart';

import '../auth/auth_controller.dart';
import '../auth/web_api.dart';
import '../scheduler/scheduler_api.dart';
import '../theme.dart';
import 'work_platform_page.dart';

class NodeWorkersPage extends StatefulWidget {
  const NodeWorkersPage({
    super.key,
    required this.auth,
    required this.cluster,
    required this.node,
  });

  final AuthController auth;
  final ClusterInfo cluster;
  final NodeInfo node;

  @override
  State<NodeWorkersPage> createState() => _NodeWorkersPageState();
}

class _NodeWorkersPageState extends State<NodeWorkersPage> {
  NodeInfo? _node;
  String? _error;
  bool _loading = true;

  SchedulerApi get _api {
    final s = widget.auth.session!;
    return SchedulerApi(baseUrl: s.schedulerBaseUrl, token: s.token);
  }

  @override
  void initState() {
    super.initState();
    _node = widget.node;
    _reload();
  }

  Future<void> _reload() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final nodes = await _api.listNodes();
      if (!mounted) return;
      final match = nodes.where((n) => n.id == widget.node.id).toList();
      setState(() {
        _node = match.isNotEmpty ? match.first : widget.node;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
    }
  }

  void _openWorker(WorkerInfo w) {
    final session = widget.auth.session;
    if (session == null || !session.hasFleet) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Fleet not selected')),
      );
      return;
    }
    // Jobs must use worker id (allowlist key). project_rel is only a label.
    final repo = w.id.isNotEmpty
        ? w.id
        : (w.repoId.isNotEmpty ? w.repoId : w.repoLabel);
    Navigator.of(context).push<void>(
      MaterialPageRoute(
        builder: (_) => WorkPlatformPage(
          session: session,
          worker: w,
          repoId: repo,
          clusterName: widget.cluster.name.isEmpty
              ? widget.cluster.id
              : widget.cluster.name,
          nodeName: (_node ?? widget.node).displayName,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final node = _node ?? widget.node;
    final clusterTitle =
        widget.cluster.name.isEmpty ? widget.cluster.id : widget.cluster.name;

    return Scaffold(
      appBar: AppBar(
        title: Text(node.displayName),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            onPressed: _loading ? null : _reload,
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: Container(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            center: Alignment.topCenter,
            radius: 1.2,
            colors: [Color(0xFF1A2A3A), AnvilColors.bg],
            stops: [0.0, 0.55],
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '$clusterTitle · ${node.pcName}',
                    style: const TextStyle(color: AnvilColors.muted, fontSize: 13),
                  ),
                  if (node.description.isNotEmpty) ...[
                    const SizedBox(height: 6),
                    Text(
                      node.description,
                      style: const TextStyle(
                        color: AnvilColors.muted,
                        fontSize: 13,
                        height: 1.35,
                      ),
                    ),
                  ],
                  const SizedBox(height: 8),
                  const Text(
                    'Workers on this node. Tap one to open the work platform.',
                    style: TextStyle(color: AnvilColors.muted, fontSize: 13),
                  ),
                ],
              ),
            ),
            if (_error != null)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Text(
                  _error!,
                  style: const TextStyle(color: AnvilColors.warn, fontSize: 12),
                ),
              ),
            Expanded(
              child: _loading && _node == null
                  ? const Center(child: CircularProgressIndicator())
                  : RefreshIndicator(
                      color: AnvilColors.accent,
                      backgroundColor: AnvilColors.panel,
                      onRefresh: _reload,
                      child: node.workers.isEmpty
                          ? ListView(
                              physics: const AlwaysScrollableScrollPhysics(),
                              children: const [
                                SizedBox(height: 80),
                                Center(
                                  child: Text(
                                    'No workers on this node.',
                                    style: TextStyle(color: AnvilColors.muted),
                                  ),
                                ),
                              ],
                            )
                          : ListView.separated(
                              padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
                              itemCount: node.workers.length,
                              separatorBuilder: (_, __) =>
                                  const SizedBox(height: 10),
                              itemBuilder: (context, i) {
                                final w = node.workers[i];
                                return _WorkerCard(
                                  worker: w,
                                  onTap: () => _openWorker(w),
                                );
                              },
                            ),
                    ),
            ),
          ],
        ),
      ),
    );
  }
}

class _WorkerCard extends StatelessWidget {
  const _WorkerCard({required this.worker, required this.onTap});

  final WorkerInfo worker;
  final VoidCallback onTap;

  Color _statusColor(String status) {
    switch (status) {
      case 'idle':
        return AnvilColors.ok;
      case 'busy':
        return AnvilColors.accent;
      case 'starting':
        return AnvilColors.warn;
      case 'stopped':
        return AnvilColors.muted;
      case 'unhealthy':
        return AnvilColors.danger;
      default:
        return AnvilColors.muted;
    }
  }

  @override
  Widget build(BuildContext context) {
    final color = _statusColor(worker.status);
    final title = worker.cardTitle;
    final repo = worker.repoLabel;
    final branch = worker.branch;
    final showRepoUnder =
        branch.isNotEmpty && repo.isNotEmpty && repo != branch;
    final desc = worker.description;

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(12),
        child: Ink(
          decoration: BoxDecoration(
            color: AnvilColors.panel,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: AnvilColors.border),
          ),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(14, 14, 12, 14),
            child: Row(
              children: [
                Container(
                  width: 40,
                  height: 40,
                  decoration: BoxDecoration(
                    color: AnvilColors.sidebar,
                    borderRadius: BorderRadius.circular(10),
                    border: Border.all(color: AnvilColors.border),
                  ),
                  child: Icon(
                    branch.isNotEmpty
                        ? Icons.account_tree_outlined
                        : Icons.folder_outlined,
                    color: AnvilColors.accent,
                    size: 20,
                  ),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        title,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          color: AnvilColors.text,
                          fontSize: 15,
                          fontWeight: FontWeight.w600,
                          fontFamily: 'monospace',
                        ),
                      ),
                      if (showRepoUnder) ...[
                        const SizedBox(height: 3),
                        Text(
                          repo,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AnvilColors.muted,
                            fontSize: 12,
                            fontFamily: 'monospace',
                          ),
                        ),
                      ],
                      if (desc.isNotEmpty) ...[
                        const SizedBox(height: 3),
                        Text(
                          desc,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AnvilColors.muted,
                            fontSize: 12,
                            height: 1.3,
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
                const SizedBox(width: 8),
                Column(
                  children: [
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 8,
                        vertical: 3,
                      ),
                      decoration: BoxDecoration(
                        color: color.withValues(alpha: 0.15),
                        borderRadius: BorderRadius.circular(999),
                        border: Border.all(color: color.withValues(alpha: 0.5)),
                      ),
                      child: Text(
                        worker.status.isEmpty ? 'unknown' : worker.status,
                        style: TextStyle(
                          color: color,
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                    const SizedBox(height: 8),
                    const Icon(
                      Icons.arrow_forward_rounded,
                      size: 18,
                      color: AnvilColors.accent,
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
