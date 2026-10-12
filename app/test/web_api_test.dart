import 'dart:convert';

import 'package:anvil_app/auth/web_api.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

void main() {
  test('normalizeBaseUrl strips slash and adds scheme', () {
    expect(normalizeBaseUrl('127.0.0.1:17900'), 'http://127.0.0.1:17900');
    expect(normalizeBaseUrl('http://host/path/'), 'http://host/path');
  });

  test('login maps token and web url only', () async {
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
      username: 'admin',
      password: 'password1',
    );
    expect(s.token, 'abc123');
    expect(s.webBaseUrl, 'http://127.0.0.1:17900');
    expect(s.hasFleet, isFalse);
    expect(s.username, 'admin');
    expect(s.isSignedIn, isTrue);
  });

  test('listFleets parses catalog', () async {
    final api = WebAuthApi(
      get: (uri, {headers}) async {
        expect(uri.path, '/api/v1/fleets');
        expect(headers!['Authorization'], 'Bearer tok');
        return http.Response(
          jsonEncode({
            'fleets': [
              {
                'id': 'local',
                'name': 'Local cluster',
                'scheduler_url': 'http://127.0.0.1:17800',
              },
            ],
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      },
    );
    final list = await api.listFleets(
      webBaseUrl: 'http://127.0.0.1:17900',
      token: 'tok',
    );
    expect(list.length, 1);
    expect(list.first.id, 'local');
    expect(list.first.schedulerUrl, 'http://127.0.0.1:17800');
  });

  test('listClusters parses nodes and workers', () async {
    final api = WebAuthApi(
      get: (uri, {headers}) async {
        expect(uri.path, '/api/v1/clusters');
        return http.Response(
          jsonEncode({
            'clusters': [
              {
                'id': 'local',
                'name': 'Local cluster',
                'description': 'dev',
                'scheduler_url': 'http://127.0.0.1:17800',
                'online': true,
                'nodes': [
                  {
                    'id': 'n1',
                    'name': 'desk',
                    'description': 'main PC',
                    'hostname': 'DESKTOP-A',
                    'max_workers': 2,
                    'idle': 1,
                    'busy': 0,
                    'accepting': true,
                    'stale': false,
                    'workers': [
                      {
                        'id': 'w1',
                        'name': 'Demo',
                        'description': 'demo worker',
                        'status': 'idle',
                        'repo_id': 'repo-1',
                      },
                    ],
                  },
                ],
              },
            ],
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      },
    );
    final list = await api.listClusters(
      webBaseUrl: 'http://127.0.0.1:17900',
      token: 'tok',
    );
    expect(list.length, 1);
    expect(list.first.online, isTrue);
    expect(list.first.nodes.first.hostname, 'DESKTOP-A');
    expect(list.first.nodes.first.workers.first.repoId, 'repo-1');
  });
}
