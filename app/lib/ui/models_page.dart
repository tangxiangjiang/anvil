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
  String? _selected;
  String? _selectedProvider;
  String? _error;
  bool _loading = true;
  bool _saving = false;

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
      final cat = await _api.listModelsCatalog();
      if (!mounted) return;
      final chatModels = cat.models.where((m) => !m.isEmbedding).toList();
      var selected = cat.selectedModel;
      var provider = cat.selectedProvider;
      final ids = chatModels.map((m) => m.id).toSet();
      if ((selected == null ||
              selected.isEmpty ||
              !ids.contains(selected)) &&
          chatModels.isNotEmpty) {
        final first = chatModels.first;
        await _api.selectModel(model: first.id, provider: first.provider);
        selected = first.id;
        provider = first.provider;
      }
      if (!mounted) return;
      setState(() {
        _models = chatModels;
        _selected = selected;
        _selectedProvider = provider;
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

  Future<void> _select(ModelInfo m) async {
    if (_saving || m.id == _selected) return;
    setState(() => _saving = true);
    try {
      await _api.selectModel(model: m.id, provider: m.provider);
      if (!mounted) return;
      setState(() {
        _selected = m.id;
        _selectedProvider = m.provider;
        _saving = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _saving = false;
        _error = e.toString();
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null && (_models == null || _models!.isEmpty)) {
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
            const Text('No models from gateway catalog.'),
            const SizedBox(height: 12),
            FilledButton(onPressed: _reload, child: const Text('Refresh')),
          ],
        ),
      );
    }
    return Column(
      children: [
        if (_selected != null)
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
            child: Align(
              alignment: Alignment.centerLeft,
              child: Text(
                'Selected: $_selected'
                '${_selectedProvider != null && _selectedProvider!.isNotEmpty ? ' · $_selectedProvider' : ''}',
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
          ),
        Expanded(
          child: RefreshIndicator(
            onRefresh: _reload,
            child: ListView.separated(
              itemCount: models.length,
              separatorBuilder: (_, __) => const Divider(height: 1),
              itemBuilder: (context, i) {
                final m = models[i];
                final selected = m.id == _selected;
                return ListTile(
                  leading: Icon(
                    selected
                        ? Icons.radio_button_checked
                        : Icons.radio_button_off,
                  ),
                  title: Text(m.id),
                  subtitle: Text([
                    m.tierLabel,
                    if (m.vision) 'vision',
                    if (m.provider != null && m.provider!.isNotEmpty)
                      m.provider!,
                  ].join(' · ')),
                  enabled: !_saving,
                  onTap: () => _select(m),
                );
              },
            ),
          ),
        ),
      ],
    );
  }
}
