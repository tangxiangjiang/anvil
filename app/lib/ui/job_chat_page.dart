import 'dart:async';
import 'dart:math';

import 'package:flutter/material.dart';

import '../auth/session.dart';
import '../chat/chat_models.dart';
import '../chat/chat_session_store.dart';
import '../scheduler/scheduler_api.dart';
import '../theme.dart';

class _Turn {
  const _Turn({
    required this.prompt,
    this.done,
    this.error,
    this.pending = false,
  });
  final String prompt;
  final JobDone? done;
  final String? error;
  final bool pending;
}

class JobChatPage extends StatefulWidget {
  const JobChatPage({
    super.key,
    required this.session,
    this.fixedRepoId,
    this.platformMode = false,
    this.threadFleetId,
    this.threadRepoId,
    this.sessionStore,
    this.onToolbarChanged,
  });

  final Session session;
  final String? fixedRepoId;
  final bool platformMode;
  final String? threadFleetId;
  final String? threadRepoId;
  final ChatSessionStore? sessionStore;
  /// Parent AppBar can rebuild when model / busy state changes.
  final VoidCallback? onToolbarChanged;

  @override
  State<JobChatPage> createState() => JobChatPageState();
}

class JobChatPageState extends State<JobChatPage> {
  // Eager fields — avoids hot-reload Lookup failed on late/_private State reshape.
  final TextEditingController _repo = TextEditingController(text: 'demo');
  final TextEditingController _resume = TextEditingController();
  final TextEditingController _prompt = TextEditingController();
  final ScrollController _scroll = ScrollController();
  final List<_Turn> _turns = <_Turn>[];
  ChatSessionStore _store = ChatSessionStore();

  ChatSession? _chat;
  bool _busy = false;
  bool _loadingModels = true;
  String? _modelsError;
  List<ModelInfo> _models = const [];
  String? _selectedModelId;
  String? _selectedProvider;
  bool _showResume = false;

  bool get _lockedRepo =>
      widget.fixedRepoId != null && widget.fixedRepoId!.trim().isNotEmpty;

  bool get _sessionMode =>
      widget.platformMode &&
      (widget.threadRepoId ?? widget.fixedRepoId)?.trim().isNotEmpty == true;

  String get _fleetKey =>
      (widget.threadFleetId ?? widget.session.fleetId ?? '').trim();

  String get _repoKey {
    final scoped = (widget.threadRepoId ?? widget.fixedRepoId)?.trim() ?? '';
    if (scoped.isNotEmpty) return scoped;
    return _repo.text.trim();
  }

  String? get _activeAgentId => _chat?.agentId;

  SchedulerApi get _api => SchedulerApi(
        baseUrl: widget.session.schedulerBaseUrl,
        token: widget.session.token,
      );

  @override
  void initState() {
    super.initState();
    if (widget.sessionStore != null) {
      _store = widget.sessionStore!;
    }
    _repo.text = _lockedRepo ? widget.fixedRepoId!.trim() : 'demo';
    unawaited(_loadModels());
    if (_sessionMode) {
      unawaited(resumeOrBlank());
    }
  }

  @override
  void dispose() {
    _repo.dispose();
    _resume.dispose();
    _prompt.dispose();
    _scroll.dispose();
    super.dispose();
  }

  /// Public: AppBar History.
  Future<void> openHistoryDrawer() => _openHistoryDrawer();

  /// Public: AppBar New chat.
  Future<void> newConversation() => _newConversation();

  /// Public: AppBar model picker.
  Future<void> openModelPicker() => _openModelPicker();

  String get modelToolbarLabel {
    if (_loadingModels) return '…';
    final m = _selectedModel;
    if (m == null) return 'Model';
    final id = m.id;
    return id.contains('/') ? id.split('/').last : id;
  }

  String get modelToolbarTooltip {
    final m = _selectedModel;
    if (m == null) return 'Select model';
    return 'Model · ${m.id} · ${m.tierLabel}';
  }

  bool get modelPickerEnabled =>
      !_busy && !_loadingModels && _models.isNotEmpty;

  void _notifyToolbar() {
    widget.onToolbarChanged?.call();
  }

  Future<void> resumeOrBlank() async {
    if (!_sessionMode) return;
    try {
      final sess = await _store.resumeOrBlank(
        fleetId: _fleetKey,
        repoId: _repoKey,
      );
      if (!mounted) return;
      setState(() => _applySession(sess));
      _scrollToEnd();
    } catch (_) {}
  }

  void _applySession(ChatSession? sess) {
    _chat = sess;
    _turns
      ..clear()
      ..addAll(_turnsFromSession(sess));
  }

  List<_Turn> _turnsFromSession(ChatSession? sess) {
    if (sess == null) return const [];
    final out = <_Turn>[];
    String? pendingPrompt;
    for (final m in sess.messages) {
      if (m.role == 'user') {
        if (pendingPrompt != null) {
          out.add(_Turn(prompt: pendingPrompt));
        }
        pendingPrompt = m.content;
      } else if (m.role == 'assistant' && pendingPrompt != null) {
        out.add(
          _Turn(
            prompt: pendingPrompt,
            done: JobDone(
              jobId: m.jobId ?? '',
              status: 'finished',
              result: m.content,
              agentId: sess.agentId,
            ),
          ),
        );
        pendingPrompt = null;
      }
    }
    if (pendingPrompt != null) {
      out.add(_Turn(prompt: pendingPrompt));
    }
    return out;
  }

  Future<void> _newConversation() async {
    if (_busy || !_sessionMode) return;
    await _store.setActiveSessionId(
      fleetId: _fleetKey,
      repoId: _repoKey,
      id: null,
    );
    if (!mounted) return;
    setState(() {
      _chat = null;
      _turns.clear();
      _resume.clear();
    });
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('New chat — send to start')),
    );
  }

  Future<void> _openHistoryDrawer() async {
    if (!_sessionMode) return;
    final list = await _store.listSessions(
      fleetId: _fleetKey,
      repoId: _repoKey,
    );
    if (!mounted) return;
    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: AnvilColors.panel,
      showDragHandle: true,
      builder: (ctx) {
        return SafeArea(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 8, 8),
                child: Row(
                  children: [
                    const Expanded(
                      child: Text(
                        'History',
                        style: TextStyle(
                          fontSize: 17,
                          fontWeight: FontWeight.w600,
                          color: AnvilColors.text,
                        ),
                      ),
                    ),
                    IconButton(
                      tooltip: 'New chat',
                      onPressed: () {
                        Navigator.pop(ctx);
                        unawaited(_newConversation());
                      },
                      icon: const Icon(Icons.add_comment_outlined),
                    ),
                  ],
                ),
              ),
              const Divider(height: 1, color: AnvilColors.border),
              Expanded(
                child: list.isEmpty
                    ? const Center(
                        child: Text(
                          'No saved conversations yet.',
                          style: TextStyle(color: AnvilColors.muted),
                        ),
                      )
                    : ListView.separated(
                        itemCount: list.length,
                        separatorBuilder: (_, __) => const Divider(
                          height: 1,
                          color: AnvilColors.border,
                        ),
                        itemBuilder: (context, i) {
                          final c = list[i];
                          final selected = c.id == _chat?.id;
                          return ListTile(
                            selected: selected,
                            selectedTileColor:
                                AnvilColors.accent.withValues(alpha: 0.12),
                            title: Text(
                              c.displayTitle,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(color: AnvilColors.text),
                            ),
                            subtitle: Text(
                              [
                                if (c.messageCount > 0)
                                  '${c.messageCount} messages',
                                if (c.agentId != null) 'context on',
                                c.updatedAt ?? '',
                              ].where((s) => s.isNotEmpty).join(' · '),
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(
                                color: AnvilColors.muted,
                                fontSize: 12,
                              ),
                            ),
                            onTap: () async {
                              Navigator.pop(ctx);
                              await _continueChat(c);
                            },
                          );
                        },
                      ),
              ),
            ],
          ),
        );
      },
    );
  }

  Future<void> _continueChat(ChatSession summary) async {
    final full = await _store.getSession(
      fleetId: _fleetKey,
      repoId: _repoKey,
      id: summary.id,
    );
    if (full == null || !mounted) return;
    await _store.setActiveSessionId(
      fleetId: _fleetKey,
      repoId: _repoKey,
      id: full.id,
    );
    if (!mounted) return;
    setState(() => _applySession(full));
    _scrollToEnd();
  }

  Future<ChatSession> _ensureSessionForSend() async {
    final existing = _chat;
    if (existing != null) return existing;
    final created = await _store.createSession(
      fleetId: _fleetKey,
      repoId: _repoKey,
      model: _selectedModelId,
    );
    if (mounted) setState(() => _chat = created);
    return created;
  }

  Future<void> _persistTurn({
    required String prompt,
    JobDone? done,
    String? error,
  }) async {
    if (!_sessionMode) return;
    var sess = await _ensureSessionForSend();
    final now = DateTime.now().toUtc().toIso8601String();
    final msgs = [
      ...sess.messages,
      ChatMessage(
        id: _msgId(),
        role: 'user',
        content: prompt,
        at: now,
      ),
      if (done != null)
        ChatMessage(
          id: _msgId(),
          role: 'assistant',
          content: done.error?.isNotEmpty == true
              ? done.error!
              : (done.result ?? ''),
          jobId: done.jobId,
          at: now,
        )
      else if (error != null)
        ChatMessage(
          id: _msgId(),
          role: 'assistant',
          content: error,
          at: now,
        ),
    ];
    final agent = done?.agentId?.trim();
    sess = sess.copyWith(
      messages: msgs,
      agentId: (agent != null && agent.isNotEmpty) ? agent : sess.agentId,
      model: _selectedModelId,
    );
    sess = await _store.saveSession(sess);
    if (!mounted) return;
    setState(() {
      _chat = sess;
    });
  }

  String _msgId() {
    final r = Random.secure().nextInt(0xFFFFF).toRadixString(16);
    return 'm_${DateTime.now().microsecondsSinceEpoch}_$r';
  }

  void _scrollToEnd() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scroll.hasClients) return;
      _scroll.animateTo(
        _scroll.position.maxScrollExtent,
        duration: const Duration(milliseconds: 200),
        curve: Curves.easeOut,
      );
    });
  }

  Future<void> _loadModels() async {
    setState(() {
      _loadingModels = true;
      _modelsError = null;
    });
    try {
      final cat = await _api.listModelsCatalog();
      if (!mounted) return;
      final models = cat.models.where((m) => !m.isEmbedding).toList();
      String? selected = cat.selectedModel?.trim();
      String? provider = cat.selectedProvider?.trim();
      final ids = models.map((m) => m.id).toSet();
      if (selected == null || selected.isEmpty || !ids.contains(selected)) {
        if (models.isNotEmpty) {
          final first = models.first;
          selected = first.id;
          provider = first.provider;
          try {
            await _api.selectModel(model: first.id, provider: first.provider);
          } catch (_) {}
        } else {
          selected = null;
          provider = null;
        }
      } else {
        for (final m in models) {
          if (m.id == selected) {
            provider ??= m.provider;
            break;
          }
        }
      }
      if (!mounted) return;
      setState(() {
        _models = models;
        _selectedModelId = selected;
        _selectedProvider = provider;
        _loadingModels = false;
      });
      _notifyToolbar();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _modelsError = e.toString();
        _loadingModels = false;
      });
      _notifyToolbar();
    }
  }

  Future<void> _onModelPicked(String? id) async {
    if (id == null || id.isEmpty || _busy) return;
    ModelInfo? picked;
    for (final x in _models) {
      if (x.id == id) {
        picked = x;
        break;
      }
    }
    if (picked == null) return;
    setState(() {
      _selectedModelId = picked!.id;
      _selectedProvider = picked.provider;
    });
    _notifyToolbar();
    try {
      await _api.selectModel(model: picked.id, provider: picked.provider);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Model select failed: $e')),
      );
    }
  }

  Future<void> _send() async {
    final prompt = _prompt.text.trim();
    final repo = _repo.text.trim();
    final model = _selectedModelId?.trim() ?? '';
    if (prompt.isEmpty || repo.isEmpty || _busy) return;
    if (model.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Pick a model first')),
      );
      return;
    }
    if (_sessionMode) {
      await _ensureSessionForSend();
    }
    setState(() {
      _busy = true;
      _turns.add(_Turn(prompt: prompt, pending: true));
      _prompt.clear();
    });
    _notifyToolbar();
    _scrollToEnd();
    final idx = _turns.length - 1;
    final resume = _sessionMode
        ? _activeAgentId
        : (_resume.text.trim().isEmpty ? null : _resume.text.trim());
    try {
      final done = await _api.submitJob(
        repoId: repo,
        prompt: prompt,
        model: model,
        provider: _selectedProvider,
        resumeId: resume,
      );
      if (!mounted) return;
      setState(() {
        _turns[idx] = _Turn(prompt: prompt, done: done);
        _busy = false;
      });
      _notifyToolbar();
      await _persistTurn(prompt: prompt, done: done);
      _scrollToEnd();
    } catch (e) {
      if (!mounted) return;
      final err = e.toString();
      setState(() {
        _turns[idx] = _Turn(prompt: prompt, error: err);
        _busy = false;
      });
      _notifyToolbar();
      await _persistTurn(prompt: prompt, error: err);
      _scrollToEnd();
    }
  }

  @override
  Widget build(BuildContext context) {
    final modelHint = _selectedModel == null
        ? null
        : modelToolbarLabel;

    return Column(
      children: [
        if (!widget.platformMode) ...[
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (!_lockedRepo) ...[
                  TextField(
                    controller: _repo,
                    decoration: const InputDecoration(
                      labelText: 'repo_id',
                      isDense: true,
                    ),
                    enabled: !_busy,
                  ),
                  const SizedBox(height: 10),
                ],
                _buildModelField(),
                if (_showResume) ...[
                  const SizedBox(height: 10),
                  TextField(
                    controller: _resume,
                    decoration: const InputDecoration(
                      labelText: 'resume_id (optional)',
                      isDense: true,
                    ),
                    enabled: !_busy,
                  ),
                ] else
                  Align(
                    alignment: Alignment.centerLeft,
                    child: TextButton(
                      onPressed: _busy
                          ? null
                          : () => setState(() => _showResume = true),
                      child: const Text('Resume previous job…'),
                    ),
                  ),
              ],
            ),
          ),
          const Divider(height: 1, color: AnvilColors.border),
        ],
        Expanded(child: _buildChat()),
        const Divider(height: 1, color: AnvilColors.border),
        Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Expanded(
                child: TextField(
                  controller: _prompt,
                  decoration: InputDecoration(
                    hintText: widget.platformMode
                        ? (modelHint == null
                            ? 'What should this worker do?'
                            : 'Message · $modelHint')
                        : 'Prompt',
                  ),
                  minLines: 1,
                  maxLines: 4,
                  enabled: !_busy && !_loadingModels && _models.isNotEmpty,
                  onSubmitted: (_) => _send(),
                ),
              ),
              const SizedBox(width: 8),
              IconButton.filled(
                onPressed: (_busy || _loadingModels || _models.isEmpty)
                    ? null
                    : _send,
                icon: const Icon(Icons.send),
                tooltip: 'Send',
              ),
            ],
          ),
        ),
      ],
    );
  }

  ModelInfo? get _selectedModel {
    final id = _selectedModelId;
    if (id == null) return _models.isEmpty ? null : _models.first;
    for (final m in _models) {
      if (m.id == id) return m;
    }
    return _models.isEmpty ? null : _models.first;
  }

  Color _tierColor(String tier) {
    switch (tier.toLowerCase()) {
      case 'complex':
        return AnvilColors.accent;
      case 'general':
        return AnvilColors.ok;
      case 'fast':
      case 'lite':
        return AnvilColors.warn;
      case 'embedding':
        return AnvilColors.muted;
      default:
        return AnvilColors.muted;
    }
  }

  Widget _tierBadge(String tier, {bool compact = false}) {
    final color = _tierColor(tier);
    return Container(
      padding: EdgeInsets.symmetric(
        horizontal: compact ? 6 : 8,
        vertical: compact ? 2 : 3,
      ),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: color.withValues(alpha: 0.45)),
      ),
      child: Text(
        tier,
        style: TextStyle(
          color: color,
          fontSize: compact ? 10.5 : 11,
          fontWeight: FontWeight.w600,
          letterSpacing: 0.2,
        ),
      ),
    );
  }

  Future<void> _openModelPicker() async {
    if (_busy || _models.isEmpty) return;
    final selectedId = _selectedModel?.id;
    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: AnvilColors.panel,
      showDragHandle: true,
      isScrollControlled: true,
      builder: (ctx) {
        final h = MediaQuery.sizeOf(ctx).height * 0.62;
        return SafeArea(
          child: SizedBox(
            height: h,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 4, 8, 8),
                  child: Row(
                    children: [
                      const Expanded(
                        child: Text(
                          'Select model',
                          style: TextStyle(
                            fontSize: 17,
                            fontWeight: FontWeight.w600,
                            color: AnvilColors.text,
                          ),
                        ),
                      ),
                      Text(
                        '${_models.length}',
                        style: const TextStyle(
                          color: AnvilColors.muted,
                          fontSize: 13,
                        ),
                      ),
                      IconButton(
                        tooltip: 'Refresh',
                        onPressed: () {
                          Navigator.pop(ctx);
                          unawaited(_loadModels());
                        },
                        icon: const Icon(Icons.refresh, size: 20),
                      ),
                    ],
                  ),
                ),
                const Divider(height: 1, color: AnvilColors.border),
                Expanded(
                  child: ListView.separated(
                    itemCount: _models.length,
                    separatorBuilder: (_, __) => const Divider(
                      height: 1,
                      color: AnvilColors.border,
                    ),
                    itemBuilder: (context, i) {
                      final m = _models[i];
                      final selected = m.id == selectedId;
                      final shortId = m.id.contains('/')
                          ? m.id.split('/').last
                          : m.id;
                      return ListTile(
                        selected: selected,
                        selectedTileColor:
                            AnvilColors.accent.withValues(alpha: 0.12),
                        leading: Icon(
                          selected
                              ? Icons.check_circle_rounded
                              : Icons.auto_awesome_outlined,
                          color: selected
                              ? AnvilColors.accent
                              : AnvilColors.muted,
                          size: 22,
                        ),
                        title: Text(
                          shortId,
                          style: TextStyle(
                            color: AnvilColors.text,
                            fontWeight:
                                selected ? FontWeight.w600 : FontWeight.w500,
                            fontSize: 14.5,
                          ),
                        ),
                        subtitle: Padding(
                          padding: const EdgeInsets.only(top: 4),
                          child: Row(
                            children: [
                              _tierBadge(m.tierLabel, compact: true),
                              if (m.vision) ...[
                                const SizedBox(width: 6),
                                _tierBadge('vision', compact: true),
                              ],
                              if ((m.provider ?? '').isNotEmpty) ...[
                                const SizedBox(width: 8),
                                Flexible(
                                  child: Text(
                                    m.provider!,
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: const TextStyle(
                                      color: AnvilColors.muted,
                                      fontSize: 11.5,
                                    ),
                                  ),
                                ),
                              ],
                            ],
                          ),
                        ),
                        onTap: () {
                          Navigator.pop(ctx);
                          unawaited(_onModelPicked(m.id));
                        },
                      );
                    },
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }

  Widget _buildModelField() {
    if (_loadingModels) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 6),
        child: Row(
          children: [
            SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
            SizedBox(width: 10),
            Text(
              'Loading models…',
              style: TextStyle(color: AnvilColors.muted, fontSize: 13),
            ),
          ],
        ),
      );
    }
    if (_modelsError != null) {
      return Row(
        children: [
          Expanded(
            child: Text(
              'Models failed: $_modelsError',
              style: const TextStyle(color: AnvilColors.danger, fontSize: 12),
            ),
          ),
          TextButton(onPressed: _loadModels, child: const Text('Retry')),
        ],
      );
    }
    if (_models.isEmpty) {
      return Row(
        children: [
          const Expanded(
            child: Text(
              'No models in gateway catalog.',
              style: TextStyle(color: AnvilColors.warn, fontSize: 13),
            ),
          ),
          TextButton(onPressed: _loadModels, child: const Text('Refresh')),
        ],
      );
    }

    final m = _selectedModel!;
    final shortId = m.id.contains('/') ? m.id.split('/').last : m.id;
    final enabled = !_busy;

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: enabled ? () => unawaited(_openModelPicker()) : null,
        borderRadius: BorderRadius.circular(10),
        child: Ink(
          decoration: BoxDecoration(
            color: AnvilColors.panel,
            borderRadius: BorderRadius.circular(10),
            border: Border.all(
              color: enabled
                  ? AnvilColors.border
                  : AnvilColors.border.withValues(alpha: 0.5),
            ),
          ),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 10, 10),
            child: Row(
              children: [
                Container(
                  width: 34,
                  height: 34,
                  decoration: BoxDecoration(
                    color: AnvilColors.accent.withValues(alpha: 0.12),
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(
                      color: AnvilColors.accent.withValues(alpha: 0.35),
                    ),
                  ),
                  child: const Icon(
                    Icons.auto_awesome,
                    size: 18,
                    color: AnvilColors.accent,
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Text(
                        'Model',
                        style: TextStyle(
                          color: AnvilColors.muted,
                          fontSize: 11,
                          fontWeight: FontWeight.w500,
                        ),
                      ),
                      const SizedBox(height: 2),
                      Text(
                        shortId,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          color: enabled
                              ? AnvilColors.text
                              : AnvilColors.muted,
                          fontSize: 14.5,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ],
                  ),
                ),
                _tierBadge(m.tierLabel),
                if (m.vision) ...[
                  const SizedBox(width: 6),
                  _tierBadge('vision'),
                ],
                const SizedBox(width: 4),
                Icon(
                  Icons.expand_more_rounded,
                  color: enabled ? AnvilColors.muted : AnvilColors.border,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildChat() {
    if (_turns.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(28),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                Icons.chat_bubble_outline_rounded,
                size: 40,
                color: AnvilColors.muted.withValues(alpha: 0.7),
              ),
              const SizedBox(height: 14),
              Text(
                widget.platformMode
                    ? 'No messages yet — send to start.\nHistory / New chat are in the top bar.'
                    : 'Submit a prompt; the app waits for job.done.',
                textAlign: TextAlign.center,
                style: const TextStyle(
                  color: AnvilColors.muted,
                  height: 1.4,
                  fontSize: 14,
                ),
              ),
            ],
          ),
        ),
      );
    }
    return ListView.builder(
      controller: _scroll,
      padding: const EdgeInsets.all(16),
      itemCount: _turns.length,
      itemBuilder: (context, i) {
        final t = _turns[i];
        return Padding(
          padding: const EdgeInsets.only(bottom: 16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Align(
                alignment: Alignment.centerRight,
                child: Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: AnvilColors.accentDim.withValues(alpha: 0.45),
                    borderRadius: BorderRadius.circular(12),
                    border: Border.all(
                      color: AnvilColors.accent.withValues(alpha: 0.4),
                    ),
                  ),
                  child: Text(t.prompt),
                ),
              ),
              const SizedBox(height: 8),
              if (t.pending)
                const Align(
                  alignment: Alignment.centerLeft,
                  child: Padding(
                    padding: EdgeInsets.all(8),
                    child: CircularProgressIndicator(),
                  ),
                )
              else if (t.error != null)
                Text(
                  t.error!,
                  style: const TextStyle(color: AnvilColors.danger),
                )
              else if (t.done != null) ...[
                Text(
                  'status: ${t.done!.status}  job: ${t.done!.jobId}'
                  '${t.done!.steps != null ? '  steps: ${t.done!.steps}' : ''}',
                  style: const TextStyle(
                    color: AnvilColors.muted,
                    fontSize: 12,
                  ),
                ),
                const SizedBox(height: 4),
                SelectableText(
                  t.done!.error?.isNotEmpty == true
                      ? t.done!.error!
                      : (t.done!.result ?? '(no result)'),
                ),
              ],
            ],
          ),
        );
      },
    );
  }
}
