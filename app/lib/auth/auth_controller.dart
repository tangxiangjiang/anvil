import 'package:flutter/foundation.dart';

import '../util/redact.dart';
import 'session.dart';
import 'session_store.dart';
import 'web_api.dart';

enum AuthStatus { unknown, signedOut, signedIn }

/// Owns session lifecycle: web login → fleet select → clear.
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
      status == AuthStatus.signedIn && session?.isSignedIn == true;

  bool get hasFleet => isSignedIn && session?.hasFleet == true;

  Future<void> bootstrap() async {
    lastError = null;
    try {
      final loaded = await _store.load();
      if (loaded != null && loaded.isSignedIn) {
        session = loaded;
        status = AuthStatus.signedIn;
        debugPrint(
          'session restored token=${redactSecret(loaded.token)} '
          'web=${loaded.webBaseUrl} fleet=${loaded.fleetId}',
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
    required String username,
    required String password,
  }) async {
    lastError = null;
    notifyListeners();
    try {
      final next = await _api.login(
        webBaseUrl: webBaseUrl,
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

  Future<List<FleetInfo>> listFleets() async {
    final s = session;
    if (s == null || !s.isSignedIn) {
      throw AuthException('Not signed in');
    }
    return _api.listFleets(webBaseUrl: s.webBaseUrl, token: s.token);
  }

  Future<List<ClusterInfo>> listClusters() async {
    final s = session;
    if (s == null || !s.isSignedIn) {
      throw AuthException('Not signed in');
    }
    return _api.listClusters(webBaseUrl: s.webBaseUrl, token: s.token);
  }

  Future<void> selectFleet(FleetInfo fleet) async {
    final s = session;
    if (s == null || !s.isSignedIn) {
      throw AuthException('Not signed in');
    }
    final sched = normalizeBaseUrl(fleet.schedulerUrl, label: 'Scheduler URL');
    final next = s.copyWith(
      schedulerBaseUrl: sched,
      fleetId: fleet.id,
      fleetName: fleet.name.isEmpty ? fleet.id : fleet.name,
    );
    await _store.save(next);
    session = next;
    notifyListeners();
  }

  Future<void> leaveFleet() async {
    final s = session;
    if (s == null) return;
    final next = s.copyWith(clearFleet: true);
    await _store.save(next);
    session = next;
    notifyListeners();
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
