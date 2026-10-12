import 'package:flutter/material.dart';

import '../auth/auth_controller.dart';
import '../auth/web_api.dart';
import '../scheduler/scheduler_api.dart';
import '../theme.dart';
import 'node_workers_page.dart';

class FleetPage extends StatefulWidget {
  const FleetPage({super.key, required this.auth});

  final AuthController auth;

  @override
  State<FleetPage> createState() => _FleetPageState();
}

class _FleetPageState extends State<FleetPage> {
  List<ClusterInfo>? _clusters;
  String? _error;
  bool _loading = true;
  final _expanded = <String>{};

  @override
  void initState() {
    super.initState();
    _reload();
  }

  Future<void> _reload() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final list = await widget.auth.listClusters();
      if (!mounted) return;
      setState(() {
        _clusters = list;
        _loading = false;
        // Auto-expand sole online cluster.
        if (list.length == 1 && list.first.online) {
          _expanded.add(list.first.id);
        }
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
    }
  }

  Future<void> _openNode(ClusterInfo cluster, NodeInfo node) async {
    try {
      await widget.auth.selectFleet(cluster.toFleet());
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('$e')));
      return;
    }
    if (!mounted) return;
    await Navigator.of(context).push<void>(
      MaterialPageRoute(
        builder: (_) => NodeWorkersPage(
          auth: widget.auth,
          cluster: cluster,
          node: node,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Clusters'),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            onPressed: _loading ? null : _reload,
            icon: const Icon(Icons.refresh),
          ),
          IconButton(
            tooltip: 'Sign out',
            onPressed: () => widget.auth.signOut(),
            icon: const Icon(Icons.logout),
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
        child: _buildBody(),
      ),
    );
  }

  Widget _buildBody() {
    if (_loading && _clusters == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null && _clusters == null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                _error!,
                textAlign: TextAlign.center,
                style: const TextStyle(color: AnvilColors.danger),
              ),
              const SizedBox(height: 12),
              FilledButton(onPressed: _reload, child: const Text('Retry')),
            ],
          ),
        ),
      );
    }
    final clusters = _clusters ?? const [];
    if (clusters.isEmpty) {
      return const Center(
        child: Text(
          'No clusters from anvil-web.',
          style: TextStyle(color: AnvilColors.muted),
        ),
      );
    }
    return RefreshIndicator(
      color: AnvilColors.accent,
      backgroundColor: AnvilColors.panel,
      onRefresh: _reload,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
        itemCount: clusters.length + 1,
        separatorBuilder: (_, __) => const SizedBox(height: 12),
        itemBuilder: (context, i) {
          if (i == 0) {
            return const Padding(
              padding: EdgeInsets.only(bottom: 4),
              child: Text(
                'Expand a cluster to see nodes. Open a node for its workers.',
                style: TextStyle(color: AnvilColors.muted, height: 1.35),
              ),
            );
          }
          final c = clusters[i - 1];
          final open = _expanded.contains(c.id);
          return _ClusterCard(
            cluster: c,
            expanded: open,
            onToggle: () {
              setState(() {
                if (open) {
                  _expanded.remove(c.id);
                } else {
                  _expanded.add(c.id);
                }
              });
            },
            onOpenNode: (n) => _openNode(c, n),
          );
        },
      ),
    );
  }
}

class _ClusterCard extends StatelessWidget {
  const _ClusterCard({
    required this.cluster,
    required this.expanded,
    required this.onToggle,
    required this.onOpenNode,
  });

  final ClusterInfo cluster;
  final bool expanded;
  final VoidCallback onToggle;
  final ValueChanged<NodeInfo> onOpenNode;

  @override
  Widget build(BuildContext context) {
    final title = cluster.name.isEmpty ? cluster.id : cluster.name;
    final nodeCount = cluster.nodes.length;
    final workerCount =
        cluster.nodes.fold<int>(0, (n, node) => n + node.workers.length);

    return Material(
      color: Colors.transparent,
      child: Ink(
        decoration: BoxDecoration(
          color: AnvilColors.panel,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(color: AnvilColors.border),
          boxShadow: const [
            BoxShadow(
              color: Color(0x40000000),
              blurRadius: 12,
              offset: Offset(0, 4),
            ),
          ],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            InkWell(
              onTap: onToggle,
              borderRadius: BorderRadius.circular(14),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 14, 16),
                child: Row(
                  children: [
                    Container(
                      width: 44,
                      height: 44,
                      decoration: BoxDecoration(
                        color: AnvilColors.sidebar,
                        borderRadius: BorderRadius.circular(12),
                        border: Border.all(color: AnvilColors.border),
                      ),
                      child: const Icon(
                        Icons.hub_outlined,
                        color: AnvilColors.accent,
                      ),
                    ),
                    const SizedBox(width: 14),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Row(
                            children: [
                              Expanded(
                                child: Text(
                                  title,
                                  style: const TextStyle(
                                    color: AnvilColors.text,
                                    fontSize: 17,
                                    fontWeight: FontWeight.w600,
                                    letterSpacing: 0.2,
                                  ),
                                ),
                              ),
                              _StatusChip(online: cluster.online),
                            ],
                          ),
                          const SizedBox(height: 4),
                          Text(
                            cluster.id,
                            style: const TextStyle(
                              color: AnvilColors.muted,
                              fontSize: 13,
                            ),
                          ),
                          if (cluster.description.isNotEmpty) ...[
                            const SizedBox(height: 4),
                            Text(
                              cluster.description,
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(
                                color: AnvilColors.muted,
                                fontSize: 12,
                                height: 1.3,
                              ),
                            ),
                          ],
                          const SizedBox(height: 6),
                          Text(
                            cluster.online
                                ? '$nodeCount nodes · $workerCount workers'
                                : (cluster.error.isNotEmpty
                                    ? cluster.error
                                    : 'Offline'),
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: cluster.online
                                  ? AnvilColors.muted
                                  : AnvilColors.danger,
                              fontSize: 12,
                            ),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(width: 4),
                    Icon(
                      expanded
                          ? Icons.expand_less_rounded
                          : Icons.expand_more_rounded,
                      color: AnvilColors.muted,
                    ),
                  ],
                ),
              ),
            ),
            if (expanded)
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
                child: Container(
                  decoration: BoxDecoration(
                    color: AnvilColors.sidebar,
                    borderRadius: BorderRadius.circular(12),
                    border: Border.all(
                      color: AnvilColors.border.withValues(alpha: 0.85),
                    ),
                  ),
                  child: (!cluster.online || cluster.nodes.isEmpty)
                      ? Padding(
                          padding: const EdgeInsets.fromLTRB(14, 14, 14, 14),
                          child: Text(
                            !cluster.online
                                ? 'Cluster offline — no nodes.'
                                : 'No nodes registered yet.',
                            style: const TextStyle(
                              color: AnvilColors.muted,
                              fontSize: 12,
                            ),
                          ),
                        )
                      : Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            const Padding(
                              padding: EdgeInsets.fromLTRB(12, 10, 12, 4),
                              child: Text(
                                'NODES',
                                style: TextStyle(
                                  color: AnvilColors.muted,
                                  fontSize: 10,
                                  fontWeight: FontWeight.w700,
                                  letterSpacing: 1.1,
                                ),
                              ),
                            ),
                            for (var i = 0; i < cluster.nodes.length; i++) ...[
                              if (i > 0)
                                Divider(
                                  height: 1,
                                  indent: 12,
                                  endIndent: 12,
                                  color: AnvilColors.border.withValues(alpha: 0.7),
                                ),
                              _NodeRow(
                                node: cluster.nodes[i],
                                onTap: () => onOpenNode(cluster.nodes[i]),
                              ),
                            ],
                          ],
                        ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.online});
  final bool online;

  @override
  Widget build(BuildContext context) {
    final color = online ? AnvilColors.ok : AnvilColors.danger;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        online ? 'online' : 'offline',
        style: TextStyle(color: color, fontSize: 11, fontWeight: FontWeight.w600),
      ),
    );
  }
}

class _NodeRow extends StatelessWidget {
  const _NodeRow({required this.node, required this.onTap});

  final NodeInfo node;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final desc =
        node.description.isNotEmpty ? node.description : 'No description';
    final meta =
        '${node.workers.length} workers${node.stale ? ' · stale' : ''}${node.accepting ? '' : ' · drained'}';

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(10),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(10, 8, 10, 10),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Nesting rail — marks this row as a child of the cluster.
              Container(
                width: 3,
                height: 44,
                margin: const EdgeInsets.only(top: 4, right: 10),
                decoration: BoxDecoration(
                  color: AnvilColors.accent.withValues(alpha: 0.55),
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              Container(
                width: 28,
                height: 28,
                margin: const EdgeInsets.only(top: 2),
                decoration: BoxDecoration(
                  color: AnvilColors.panel,
                  borderRadius: BorderRadius.circular(8),
                  border: Border.all(
                    color: AnvilColors.border.withValues(alpha: 0.9),
                  ),
                ),
                child: Icon(
                  Icons.computer_outlined,
                  size: 15,
                  color: node.stale ? AnvilColors.warn : AnvilColors.accent,
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      node.displayName,
                      style: const TextStyle(
                        color: AnvilColors.text,
                        fontSize: 13.5,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      'PC · ${node.pcName}  ·  $meta',
                      style: const TextStyle(
                        color: AnvilColors.muted,
                        fontSize: 11,
                      ),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      desc,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: AnvilColors.muted.withValues(alpha: 0.9),
                        fontSize: 11,
                        height: 1.3,
                      ),
                    ),
                  ],
                ),
              ),
              const Padding(
                padding: EdgeInsets.only(top: 6),
                child: Icon(
                  Icons.chevron_right_rounded,
                  size: 20,
                  color: AnvilColors.muted,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
