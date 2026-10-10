import 'package:flutter/material.dart';

import '../auth/session.dart';
import '../scheduler/scheduler_api.dart';

class ModelsPage extends StatefulWidget {
  const ModelsPage({super.key, required this.session});

  final Session session;

  @override
  State<ModelsPage> createState() => _ModelsPageState();
}

class _ModelsPageState extends State<ModelsPage> {
  List<ModelInfo>? _models;
  String? _error;
  bool _loading = true;

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
      final api = SchedulerApi(
        baseUrl: widget.session.schedulerBaseUrl,
        token: widget.session.token,
      );
      final list = await api.listModels();
      if (!mounted) return;
      setState(() {
        _models = list;
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

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
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
    final models = _models ?? const [];
    if (models.isEmpty) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text('No models in scheduler catalog.'),
            const SizedBox(height: 12),
            FilledButton(onPressed: _reload, child: const Text('Refresh')),
          ],
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _reload,
      child: ListView.separated(
        itemCount: models.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (context, i) {
          final m = models[i];
          return ListTile(
            title: Text(m.id),
            subtitle: m.ownedBy != null ? Text(m.ownedBy!) : null,
          );
        },
      ),
    );
  }
}
