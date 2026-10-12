import 'package:flutter/material.dart';

import '../auth/session.dart';
import '../scheduler/scheduler_api.dart';

/// Worker cluster ops against the selected fleet scheduler.
class NodesPage extends StatefulWidget {
  const NodesPage({super.key, required this.session});

  final Session session;

  @override
  State<NodesPage> createState() => _NodesPageState();
}

class _NodesPageState extends State<NodesPage> {
  List<NodeInfo>? _nodes;
  String? _error;
  bool _loading = true;
  final _busy = <String>{};

  SchedulerApi get _api => SchedulerApi(
        baseUrl: widget.session.schedulerBaseUrl,
        token: widget.session.token,
      );

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
      final list = await _api.listNodes();
      if (!mounted) return;
      setState(() {
        _nodes = list;
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

  Future<void> _patch(NodeInfo n, {int? maxWorkers, bool? accepting}) async {
    setState(() => _busy.add(n.id));
    try {
      await _api.patchNode(n.id, maxWorkers: maxWorkers, accepting: accepting);
      await _reload();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('$e')));
    } finally {
      if (mounted) setState(() => _busy.remove(n.id));
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading && _nodes == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null && _nodes == null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(_error!, textAlign: TextAlign.center),
              const SizedBox(height: 12),
              FilledButton(onPressed: _reload, child: const Text('Retry')),
            ],
          ),
        ),
      );
    }
    final nodes = _nodes ?? const [];
    return RefreshIndicator(
      onRefresh: _reload,
      child: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Text(
            'Cluster workers on this scheduler. Scale max_workers or drain/enable.',
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
          ),
          const SizedBox(height: 12),
          if (nodes.isEmpty)
            const Padding(
              padding: EdgeInsets.only(top: 40),
              child: Center(child: Text('No agentd nodes joined')),
            )
          else
            ...nodes.map(_nodeCard),
        ],
      ),
    );
  }

  Widget _nodeCard(NodeInfo n) {
    final busy = _busy.contains(n.id);
    final status = n.stale
        ? 'stale'
        : (n.accepting ? 'accepting' : 'draining');
    return Card(
      margin: const EdgeInsets.only(bottom: 12),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    n.id,
                    style: const TextStyle(fontWeight: FontWeight.w600),
                  ),
                ),
                Chip(label: Text(status), visualDensity: VisualDensity.compact),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              'capacity ${n.busy}/${n.maxWorkers}  idle ${n.idle}',
              style: Theme.of(context).textTheme.bodySmall,
            ),
            if (n.workers.isNotEmpty) ...[
              const SizedBox(height: 6),
              Text(
                n.workers.map((w) => '${w.id}:${w.status}').join('  '),
                style: Theme.of(context).textTheme.labelSmall,
              ),
            ],
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                OutlinedButton(
                  onPressed: busy || n.maxWorkers <= 0
                      ? null
                      : () => _patch(n, maxWorkers: n.maxWorkers - 1),
                  child: const Text('- worker'),
                ),
                OutlinedButton(
                  onPressed: busy
                      ? null
                      : () => _patch(n, maxWorkers: n.maxWorkers + 1),
                  child: const Text('+ worker'),
                ),
                if (n.accepting)
                  FilledButton.tonal(
                    onPressed: busy ? null : () => _patch(n, accepting: false),
                    child: const Text('Drain'),
                  )
                else
                  FilledButton.tonal(
                    onPressed: busy ? null : () => _patch(n, accepting: true),
                    child: const Text('Enable'),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
