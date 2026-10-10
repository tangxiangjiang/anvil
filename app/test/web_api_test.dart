import 'dart:convert';

import 'package:anvil_app/auth/web_api.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

void main() {
  test('normalizeBaseUrl strips slash and adds scheme', () {
    expect(normalizeBaseUrl('127.0.0.1:17900'), 'http://127.0.0.1:17900');
    expect(normalizeBaseUrl('http://host/path/'), 'http://host/path');
  });

  test('login maps token and urls', () async {
    final api = WebAuthApi(
      post: (uri, {headers, body}) async {
        expect(uri.path, '/api/v1/auth/login');
        return http.Response(
          jsonEncode({
            'token': 'abc123',
            'expires_in': 3600,
            'user': {'id': 1, 'username': 'admin'},
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      },
    );
    final s = await api.login(
      webBaseUrl: 'http://127.0.0.1:17900',
      schedulerBaseUrl: 'http://127.0.0.1:17800',
      username: 'admin',
      password: 'password1',
    );
    expect(s.token, 'abc123');
    expect(s.webBaseUrl, 'http://127.0.0.1:17900');
    expect(s.schedulerBaseUrl, 'http://127.0.0.1:17800');
    expect(s.username, 'admin');
    expect(s.isAuthenticated, isTrue);
  });

  test('login 401', () async {
    final api = WebAuthApi(
      post: (uri, {headers, body}) async =>
          http.Response('invalid credentials', 401),
    );
    expect(
      () => api.login(
        webBaseUrl: 'http://127.0.0.1:17900',
        schedulerBaseUrl: 'http://127.0.0.1:17800',
        username: 'a',
        password: 'b',
      ),
      throwsA(isA<AuthException>()),
    );
  });
}
