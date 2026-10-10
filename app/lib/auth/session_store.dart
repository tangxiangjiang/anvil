import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'session.dart';

abstract class SessionStore {
  Future<Session?> load();
  Future<void> save(Session session);
  Future<void> clear();
}

class SecureSessionStore implements SessionStore {
  SecureSessionStore({FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage();

  static const _kWeb = 'anvil_web_base_url';
  static const _kSched = 'anvil_scheduler_base_url';
  static const _kToken = 'anvil_web_bearer_token';
  static const _kExpires = 'anvil_web_token_expires_at';
  static const _kUser = 'anvil_web_username';

  final FlutterSecureStorage _storage;

  @override
  Future<Session?> load() async {
    final web = await _storage.read(key: _kWeb);
    final sched = await _storage.read(key: _kSched);
    final token = await _storage.read(key: _kToken);
    if (web == null ||
        web.trim().isEmpty ||
        sched == null ||
        sched.trim().isEmpty ||
        token == null ||
        token.trim().isEmpty) {
      return null;
    }
    final expRaw = await _storage.read(key: _kExpires);
    DateTime? expiresAt;
    if (expRaw != null && expRaw.isNotEmpty) {
      expiresAt = DateTime.tryParse(expRaw);
    }
    final user = await _storage.read(key: _kUser);
    return Session(
      webBaseUrl: web.trim(),
      schedulerBaseUrl: sched.trim(),
      token: token.trim(),
      expiresAt: expiresAt,
      username: user,
    );
  }

  @override
  Future<void> save(Session session) async {
    await _storage.write(key: _kWeb, value: session.webBaseUrl);
    await _storage.write(key: _kSched, value: session.schedulerBaseUrl);
    await _storage.write(key: _kToken, value: session.token);
    if (session.expiresAt != null) {
      await _storage.write(
        key: _kExpires,
        value: session.expiresAt!.toUtc().toIso8601String(),
      );
    } else {
      await _storage.delete(key: _kExpires);
    }
    if (session.username != null && session.username!.isNotEmpty) {
      await _storage.write(key: _kUser, value: session.username);
    } else {
      await _storage.delete(key: _kUser);
    }
  }

  @override
  Future<void> clear() async {
    await _storage.delete(key: _kWeb);
    await _storage.delete(key: _kSched);
    await _storage.delete(key: _kToken);
    await _storage.delete(key: _kExpires);
    await _storage.delete(key: _kUser);
  }
}

class MemorySessionStore implements SessionStore {
  Session? _session;

  @override
  Future<Session?> load() async => _session;

  @override
  Future<void> save(Session session) async {
    _session = session;
  }

  @override
  Future<void> clear() async {
    _session = null;
  }
}
