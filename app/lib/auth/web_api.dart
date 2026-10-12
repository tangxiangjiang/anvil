import 'dart:convert';

import 'package:http/http.dart' as http;

import '../scheduler/scheduler_api.dart' show NodeInfo;
import '../util/redact.dart';
import 'session.dart';

class AuthException implements Exception {
  AuthException(this.message, {this.statusCode});
  final String message;
  final int? statusCode;

  @override
  String toString() => message;
}

class FleetInfo {
  const FleetInfo({
    required this.id,
    required this.name,
    required this.schedulerUrl,
  });

  final String id;
  final String name;
  final String schedulerUrl;

  factory FleetInfo.fromJson(Map<String, dynamic> j) {
    return FleetInfo(
      id: (j['id'] as String?)?.trim() ?? '',
      name: (j['name'] as String?)?.trim() ?? '',
      schedulerUrl: (j['scheduler_url'] as String?)?.trim() ?? '',
    );
  }
}

/// Rich fleet card from `GET /api/v1/clusters` (embedded nodes).
class ClusterInfo {
  const ClusterInfo({
    required this.id,
    required this.name,
    required this.schedulerUrl,
    this.description = '',
    this.listenApp = '',
    this.online = false,
    this.error = '',
    this.nodes = const [],
  });

  final String id;
  final String name;
  final String description;
  final String schedulerUrl;
  final String listenApp;
  final bool online;
  final String error;
  final List<NodeInfo> nodes;

  FleetInfo toFleet() => FleetInfo(
        id: id,
        name: name.isEmpty ? id : name,
        schedulerUrl: schedulerUrl,
      );

  factory ClusterInfo.fromJson(Map<String, dynamic> j) {
    final nodes = <NodeInfo>[];
    final raw = j['nodes'];
    if (raw is List) {
      for (final n in raw) {
        if (n is Map<String, dynamic>) {
          final node = NodeInfo.fromJson(n);
          if (node.id.isNotEmpty) nodes.add(node);
        }
      }
    }
    return ClusterInfo(
      id: (j['id'] as String?)?.trim() ?? '',
      name: (j['name'] as String?)?.trim() ?? '',
      description: (j['description'] as String?)?.trim() ?? '',
      schedulerUrl: (j['scheduler_url'] as String?)?.trim() ?? '',
      listenApp: (j['listen_app'] as String?)?.trim() ?? '',
      online: j['online'] == true,
      error: (j['error'] as String?)?.trim() ?? '',
      nodes: nodes,
    );
  }
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

/// Thin client for anvil-web auth + fleet catalog.
class WebAuthApi {
  WebAuthApi({HttpPost? post, HttpGet? get})
      : _post = post ?? http.post,
        _get = get ?? http.get;

  final HttpPost _post;
  final HttpGet _get;

  /// `POST {web}/api/v1/auth/login` → [Session] (no fleet yet).
  Future<Session> login({
    required String webBaseUrl,
    required String username,
    required String password,
  }) async {
    final web = normalizeBaseUrl(webBaseUrl, label: 'Web URL');
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
      print('login ok token=${redactSecret(token)} web=$web');
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
      token: token,
      expiresAt: expiresAt,
      username: uname,
    );
  }

  Future<List<FleetInfo>> listFleets({
    required String webBaseUrl,
    required String token,
  }) async {
    final web = normalizeBaseUrl(webBaseUrl, label: 'Web URL');
    final res = await _get(
      Uri.parse('$web/api/v1/fleets'),
      headers: {'Authorization': 'Bearer $token'},
    );
    if (res.statusCode == 401) {
      throw AuthException('Unauthorized', statusCode: 401);
    }
    if (res.statusCode < 200 || res.statusCode >= 300) {
      throw AuthException(
        'Fleets failed (${res.statusCode})',
        statusCode: res.statusCode,
      );
    }
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final list = body['fleets'];
    if (list is! List) return const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(FleetInfo.fromJson)
        .where((f) => f.id.isNotEmpty && f.schedulerUrl.isNotEmpty)
        .toList();
  }

  Future<List<ClusterInfo>> listClusters({
    required String webBaseUrl,
    required String token,
  }) async {
    final web = normalizeBaseUrl(webBaseUrl, label: 'Web URL');
    final res = await _get(
      Uri.parse('$web/api/v1/clusters'),
      headers: {'Authorization': 'Bearer $token'},
    );
    if (res.statusCode == 401) {
      throw AuthException('Unauthorized', statusCode: 401);
    }
    if (res.statusCode < 200 || res.statusCode >= 300) {
      throw AuthException(
        'Clusters failed (${res.statusCode})',
        statusCode: res.statusCode,
      );
    }
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final list = body['clusters'];
    if (list is! List) return const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(ClusterInfo.fromJson)
        .where((c) => c.id.isNotEmpty && c.schedulerUrl.isNotEmpty)
        .toList();
  }

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
    } catch (_) {}
  }
}
