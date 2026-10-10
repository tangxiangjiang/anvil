import 'dart:convert';

import 'package:http/http.dart' as http;

import '../util/redact.dart';
import 'session.dart';

class AuthException implements Exception {
  AuthException(this.message, {this.statusCode});
  final String message;
  final int? statusCode;

  @override
  String toString() => message;
}

/// Normalize HTTP(S) base URL; strip trailing slashes.
String normalizeBaseUrl(String raw, {String label = 'URL'}) {
  var s = raw.trim();
  if (s.isEmpty) {
    throw AuthException('$label is required');
  }
  if (!s.contains('://')) {
    s = 'http://$s';
  }
  final uri = Uri.tryParse(s);
  if (uri == null || !uri.hasScheme || uri.host.isEmpty) {
    throw AuthException('Invalid $label');
  }
  if (uri.scheme != 'http' && uri.scheme != 'https') {
    throw AuthException('$label must be http or https');
  }
  var path = uri.path;
  while (path.endsWith('/')) {
    path = path.substring(0, path.length - 1);
  }
  if (path == '/') {
    path = '';
  }
  final port = uri.hasPort ? ':${uri.port}' : '';
  return '${uri.scheme}://${uri.host}$port$path';
}

typedef HttpPost = Future<http.Response> Function(
  Uri url, {
  Map<String, String>? headers,
  Object? body,
});

typedef HttpGet = Future<http.Response> Function(
  Uri url, {
  Map<String, String>? headers,
});

/// Thin client for anvil-web auth.
class WebAuthApi {
  WebAuthApi({HttpPost? post, HttpGet? get})
      : _post = post ?? http.post,
        _get = get ?? http.get;

  final HttpPost _post;
  final HttpGet _get;

  /// `POST {web}/api/v1/auth/login` → [Session].
  Future<Session> login({
    required String webBaseUrl,
    required String schedulerBaseUrl,
    required String username,
    required String password,
  }) async {
    final web = normalizeBaseUrl(webBaseUrl, label: 'Web URL');
    final sched = normalizeBaseUrl(schedulerBaseUrl, label: 'Scheduler URL');
    final user = username.trim();
    if (user.isEmpty) {
      throw AuthException('Username is required');
    }
    if (password.isEmpty) {
      throw AuthException('Password is required');
    }

    final uri = Uri.parse('$web/api/v1/auth/login');
    final http.Response res;
    try {
      res = await _post(
        uri,
        headers: {'Content-Type': 'application/json; charset=utf-8'},
        body: jsonEncode({'username': user, 'password': password}),
      );
    } catch (e) {
      throw AuthException('Network error: $e');
    }

    if (res.statusCode == 401) {
      throw AuthException('Invalid credentials', statusCode: 401);
    }
    if (res.statusCode < 200 || res.statusCode >= 300) {
      throw AuthException(
        'Login failed (${res.statusCode})',
        statusCode: res.statusCode,
      );
    }

    final Map<String, dynamic> body;
    try {
      body = jsonDecode(res.body) as Map<String, dynamic>;
    } catch (_) {
      throw AuthException('Invalid login response');
    }
    final token = (body['token'] as String?)?.trim() ?? '';
    if (token.isEmpty) {
      throw AuthException('Login response missing token');
    }

    assert(() {
      // ignore: avoid_print
      print('login ok token=${redactSecret(token)} web=$web sched=$sched');
      return true;
    }());

    DateTime? expiresAt;
    final expiresIn = body['expires_in'];
    if (expiresIn is int && expiresIn > 0) {
      expiresAt = DateTime.now().toUtc().add(Duration(seconds: expiresIn));
    }

    String? uname = user;
    final u = body['user'];
    if (u is Map<String, dynamic>) {
      uname = (u['username'] as String?)?.trim() ?? user;
    }

    return Session(
      webBaseUrl: web,
      schedulerBaseUrl: sched,
      token: token,
      expiresAt: expiresAt,
      username: uname,
    );
  }

  /// Best-effort logout on web.
  Future<void> logout({
    required String webBaseUrl,
    required String token,
  }) async {
    try {
      final web = normalizeBaseUrl(webBaseUrl, label: 'Web URL');
      await _post(
        Uri.parse('$web/api/v1/auth/logout'),
        headers: {
          'Authorization': 'Bearer $token',
          'Content-Type': 'application/json; charset=utf-8',
        },
        body: '{}',
      );
    } catch (_) {
      // ignore network errors on sign-out
    }
  }

  /// Optional session check.
  Future<bool> me({
    required String webBaseUrl,
    required String token,
  }) async {
    try {
      final web = normalizeBaseUrl(webBaseUrl, label: 'Web URL');
      final res = await _get(
        Uri.parse('$web/api/v1/auth/me'),
        headers: {'Authorization': 'Bearer $token'},
      );
      return res.statusCode >= 200 && res.statusCode < 300;
    } catch (_) {
      return false;
    }
  }
}
