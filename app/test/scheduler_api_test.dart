import 'dart:convert';

import 'package:anvil_app/scheduler/scheduler_api.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

void main() {
  test('listModels parses data array', () async {
    final api = SchedulerApi(
      baseUrl: 'http://127.0.0.1:17800',
      token: 'tok',
      get: (uri, {headers}) async {
        expect(uri.path, '/v1/models');
        expect(headers!['Authorization'], 'Bearer tok');
        return http.Response(
          jsonEncode({
            'data': [
              {'id': 'm1', 'owned_by': 'anvil'},
            ],
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      },
    );
    final list = await api.listModels();
    expect(list.length, 1);
    expect(list.first.id, 'm1');
  });

  test('listNodes parses workers', () async {
    final api = SchedulerApi(
      baseUrl: 'http://127.0.0.1:17800',
      token: 'tok',
      get: (uri, {headers}) async {
        expect(uri.path, '/v1/nodes');
        return http.Response(
          jsonEncode({
            'nodes': [
              {
                'id': 'local-1',
                'max_workers': 2,
                'idle': 1,
                'busy': 1,
                'accepting': true,
                'stale': false,
                'name': 'local',
                'hostname': 'PC-1',
                'description': 'dev node',
                'workers': [
                  {
                    'id': 'worker-1',
                    'name': 'Demo',
                    'status': 'idle',
                    'repo_id': 'r1',
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
    final nodes = await api.listNodes();
    expect(nodes.length, 1);
    expect(nodes.first.id, 'local-1');
    expect(nodes.first.hostname, 'PC-1');
    expect(nodes.first.workers.first.status, 'idle');
    expect(nodes.first.workers.first.repoId, 'r1');
  });

  test('submitJob posts body and parses done', () async {
    final api = SchedulerApi(
      baseUrl: 'http://127.0.0.1:17800',
      token: 'tok',
      post: (uri, {headers, body}) async {
        expect(uri.path, '/v1/jobs');
        final map = jsonDecode(body as String) as Map<String, dynamic>;
        expect(map['repo_id'], 'demo');
        expect(map['prompt'], 'hi');
        return http.Response(
          jsonEncode({
            'job_id': 'j1',
            'agent_id': 'agent-abc',
            'status': 'finished',
            'result': 'ok',
            'steps': 1,
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      },
    );
    final done = await api.submitJob(repoId: 'demo', prompt: 'hi');
    expect(done.jobId, 'j1');
    expect(done.agentId, 'agent-abc');
    expect(done.status, 'finished');
    expect(done.result, 'ok');
  });
}
