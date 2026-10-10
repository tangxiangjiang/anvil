import 'package:flutter/foundation.dart';

import '../util/redact.dart';
import 'session.dart';
import 'session_store.dart';
import 'web_api.dart';

enum AuthStatus { unknown, signedOut, signedIn }

/// Owns session lifecycle: load secure store → web login → clear.
class AuthController extends ChangeNotifier {
  AuthController({
    required SessionStore store,
    WebAuthApi? api,
  })  : _store = store,
        _api = api ?? WebAuthApi();

  final SessionStore _store;
  final WebAuthApi _api;

  AuthStatus status = AuthStatus.unknown;
  Session? session;
  String? lastError;

  bool get isSignedIn =>
      status == AuthStatus.signedIn && session?.isAuthenticated == true;

  Future<void> bootstrap() async {
    lastError = null;
    try {
      final loaded = await _store.load();
      if (loaded != null && loaded.isAuthenticated) {
        session = loaded;
        status = AuthStatus.signedIn;
        debugPrint(
          'session restored token=${redactSecret(loaded.token)} '
          'web=${loaded.webBaseUrl} sched=${loaded.schedulerBaseUrl}',
        );
      } else {
        session = null;
        status = AuthStatus.signedOut;
      }
    } catch (e) {
      session = null;
      status = AuthStatus.signedOut;
      lastError = 'Failed to load session';
      debugPrint('session load failed: $e');
    }
    notifyListeners();
  }

  Future<bool> login({
    required String webBaseUrl,
    required String schedulerBaseUrl,
    required String username,
    required String password,
  }) async {
    lastError = null;
    notifyListeners();
    try {
      final next = await _api.login(
        webBaseUrl: webBaseUrl,
        schedulerBaseUrl: schedulerBaseUrl,
        username: username,
        password: password,
      );
      await _store.save(next);
      session = next;
      status = AuthStatus.signedIn;
      debugPrint(
        'login ok token=${redactSecret(next.token)} web=${next.webBaseUrl}',
      );
      notifyListeners();
      return true;
    } on AuthException catch (e) {
      lastError = e.message;
      notifyListeners();
      return false;
    } catch (e) {
      lastError = 'Login failed';
      debugPrint('login unexpected: $e');
      notifyListeners();
      return false;
    }
  }

  Future<void> signOut() async {
    final s = session;
    if (s != null) {
      await _api.logout(webBaseUrl: s.webBaseUrl, token: s.token);
    }
    await _store.clear();
    session = null;
    status = AuthStatus.signedOut;
    lastError = null;
    notifyListeners();
  }
}
