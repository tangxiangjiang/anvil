import 'dart:convert';
import 'dart:math';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'chat_models.dart';

/// Client-side chat sessions per (fleet, worker/repo) — cloud-agent style.
///
/// Persists transcripts locally so reopening Work Platform restores history.
/// Agent continuity still uses [ChatSession.agentId] as job `resume_id`.
class ChatSessionStore {
  ChatSessionStore({FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage();

  final FlutterSecureStorage _storage;

  static const _maxMessages = 80;
  static const _maxContentChars = 4000;

  static String _scope(String fleetId, String repoId) {
    final f = fleetId.trim().isEmpty ? '_' : fleetId.trim();
    final r = repoId.trim().isEmpty ? '_' : repoId.trim();
    return 'anvil.chat.v1.$f.$r';
  }

  String _indexKey(String fleetId, String repoId) =>
      '${_scope(fleetId, repoId)}.index';

  String _sessKey(String fleetId, String repoId, String id) =>
      '${_scope(fleetId, repoId)}.sess.$id';

  String _activeKey(String fleetId, String repoId) =>
      '${_scope(fleetId, repoId)}.active';

  Future<List<ChatSession>> listSessions({
    required String fleetId,
    required String repoId,
  }) async {
    final raw = await _storage.read(key: _indexKey(fleetId, repoId));
    if (raw == null || raw.trim().isEmpty) return const [];
    try {
      final list = jsonDecode(raw) as List<dynamic>;
      final out = <ChatSession>[];
      for (final e in list) {
        if (e is Map<String, dynamic>) {
          out.add(ChatSession.fromSummaryJson(e));
        } else if (e is Map) {
          out.add(ChatSession.fromSummaryJson(Map<String, dynamic>.from(e)));
        }
      }
      out.sort((a, b) {
        final au = a.updatedAt ?? '';
        final bu = b.updatedAt ?? '';
        return bu.compareTo(au);
      });
      return out;
    } catch (_) {
      return const [];
    }
  }

  Future<ChatSession?> getSession({
    required String fleetId,
    required String repoId,
    required String id,
  }) async {
    final raw = await _storage.read(key: _sessKey(fleetId, repoId, id));
    if (raw == null || raw.trim().isEmpty) return null;
    try {
      final map = jsonDecode(raw) as Map<String, dynamic>;
      return ChatSession.fromJson(map);
    } catch (_) {
      return null;
    }
  }

  Future<String?> activeSessionId({
    required String fleetId,
    required String repoId,
  }) async {
    final v = await _storage.read(key: _activeKey(fleetId, repoId));
    final s = v?.trim() ?? '';
    return s.isEmpty ? null : s;
  }

  Future<void> setActiveSessionId({
    required String fleetId,
    required String repoId,
    String? id,
  }) async {
    final key = _activeKey(fleetId, repoId);
    if (id == null || id.trim().isEmpty) {
      await _storage.delete(key: key);
      return;
    }
    await _storage.write(key: key, value: id.trim());
  }

  Future<ChatSession> createSession({
    required String fleetId,
    required String repoId,
    String? model,
  }) async {
    final now = DateTime.now().toUtc().toIso8601String();
    final id = _newId();
    final sess = ChatSession(
      id: id,
      fleetId: fleetId,
      repoId: repoId,
      model: model,
      createdAt: now,
      updatedAt: now,
    );
    await _writeSession(sess);
    await _upsertIndex(sess);
    await setActiveSessionId(fleetId: fleetId, repoId: repoId, id: id);
    return sess;
  }

  Future<ChatSession> saveSession(ChatSession sess) async {
    final trimmed = _truncate(sess.copyWith(
      updatedAt: DateTime.now().toUtc().toIso8601String(),
    ));
    await _writeSession(trimmed);
    await _upsertIndex(trimmed);
    await setActiveSessionId(
      fleetId: trimmed.fleetId,
      repoId: trimmed.repoId,
      id: trimmed.id,
    );
    return trimmed;
  }

  Future<void> deleteSession({
    required String fleetId,
    required String repoId,
    required String id,
  }) async {
    await _storage.delete(key: _sessKey(fleetId, repoId, id));
    final list = await listSessions(fleetId: fleetId, repoId: repoId);
    final next = list.where((s) => s.id != id).toList();
    await _writeIndex(fleetId, repoId, next);
    final active = await activeSessionId(fleetId: fleetId, repoId: repoId);
    if (active == id) {
      await setActiveSessionId(
        fleetId: fleetId,
        repoId: repoId,
        id: next.isEmpty ? null : next.first.id,
      );
    }
  }

  /// Open latest (or active) session with full messages; null if none.
  Future<ChatSession?> resumeOrBlank({
    required String fleetId,
    required String repoId,
  }) async {
    final active = await activeSessionId(fleetId: fleetId, repoId: repoId);
    if (active != null) {
      final s = await getSession(fleetId: fleetId, repoId: repoId, id: active);
      if (s != null) return s;
    }
    final list = await listSessions(fleetId: fleetId, repoId: repoId);
    if (list.isEmpty) return null;
    return getSession(fleetId: fleetId, repoId: repoId, id: list.first.id);
  }

  Future<void> _writeSession(ChatSession sess) async {
    await _storage.write(
      key: _sessKey(sess.fleetId, sess.repoId, sess.id),
      value: jsonEncode(sess.toJson()),
    );
  }

  Future<void> _upsertIndex(ChatSession sess) async {
    final list = await listSessions(
      fleetId: sess.fleetId,
      repoId: sess.repoId,
    );
    final out = <ChatSession>[
      sess,
      ...list.where((s) => s.id != sess.id),
    ];
    await _writeIndex(sess.fleetId, sess.repoId, out);
  }

  Future<void> _writeIndex(
    String fleetId,
    String repoId,
    List<ChatSession> list,
  ) async {
    await _storage.write(
      key: _indexKey(fleetId, repoId),
      value: jsonEncode(list.map((s) => s.toSummaryJson()).toList()),
    );
  }

  ChatSession _truncate(ChatSession sess) {
    var msgs = sess.messages;
    if (msgs.length > _maxMessages) {
      msgs = msgs.sublist(msgs.length - _maxMessages);
    }
    msgs = [
      for (final m in msgs)
        ChatMessage(
          id: m.id,
          role: m.role,
          content: m.content.length > _maxContentChars
              ? '${m.content.substring(0, _maxContentChars)}…'
              : m.content,
          jobId: m.jobId,
          at: m.at,
        ),
    ];
    final preview = msgs.isEmpty
        ? sess.preview
        : msgs.lastWhere(
            (m) => m.role == 'user',
            orElse: () => msgs.last,
          ).content;
    String? title = sess.title;
    if (title == null || title.trim().isEmpty) {
      final firstUser = msgs.where((m) => m.role == 'user').map((m) => m.content);
      if (firstUser.isNotEmpty) {
        final t = firstUser.first.trim();
        title = t.length > 48 ? '${t.substring(0, 48)}…' : t;
      }
    }
    return sess.copyWith(
      messages: msgs,
      preview: preview,
      title: title,
    );
  }

  String _newId() {
    final r = Random.secure().nextInt(0xFFFFFF).toRadixString(16).padLeft(6, '0');
    return 'chat_${DateTime.now().millisecondsSinceEpoch}_$r';
  }
}
