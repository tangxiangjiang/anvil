import 'dart:convert';

import 'package:http/http.dart' as http;

import '../auth/web_api.dart' show normalizeBaseUrl, AuthException;

class SchedulerException implements Exception {
  SchedulerException(this.message, {this.statusCode});
  final String message;
  final int? statusCode;

  @override
  String toString() => message;
}

class JobDone {
  const JobDone({
    required this.jobId,
    required this.status,
    this.result,
    this.error,
    this.code,
    this.steps,
  });

  final String jobId;
  final String status;
  final String? result;
  final String? error;
  final String? code;
  final int? steps;

  factory JobDone.fromJson(Map<String, dynamic> j) {
    return JobDone(
      jobId: (j['job_id'] as String?) ?? '',
      status: (j['status'] as String?) ?? '',
      result: j['result'] as String?,
      error: j['error'] as String?,
      code: j['code'] as String?,
      steps: j['steps'] as int?,
    );
  }
}

class ModelInfo {
  const ModelInfo({required this.id, this.ownedBy});
  final String id;
  final String? ownedBy;

  factory ModelInfo.fromJson(Map<String, dynamic> j) {
    return ModelInfo(
      id: (j['id'] as String?) ?? '',
      ownedBy: j['owned_by'] as String?,
    );
  }
}

typedef HttpGet = Future<http.Response> Function(
  Uri url, {
  Map<String, String>? headers,
});

typedef HttpPost = Future<http.Response> Function(
  Uri url, {
  Map<String, String>? headers,
  Object? body,
});

/// Thin client for anvil-scheduler app HTTP (Bearer = web session token).
class SchedulerApi {
  SchedulerApi({
    required this.baseUrl,
    required this.token,
    HttpGet? get,
    HttpPost? post,
  })  : _get = get ?? http.get,
        _post = post ?? http.post;

  final String baseUrl;
  final String token;
  final HttpGet _get;
  final HttpPost _post;

  String get _base {
    try {
      return normalizeBaseUrl(baseUrl, label: 'Scheduler URL');
    } on AuthException catch (e) {
      throw SchedulerException(e.message);
    }
  }

  Map<String, String> get _headers => {
        'Authorization': 'Bearer $token',
        'Content-Type': 'application/json; charset=utf-8',
      };

  Future<List<ModelInfo>> listModels() async {
    final res = await _get(Uri.parse('$_base/v1/models'), headers: _headers);
    if (res.statusCode == 401) {
      throw SchedulerException('Unauthorized', statusCode: 401);
    }
    if (res.statusCode < 200 || res.statusCode >= 300) {
      throw SchedulerException(
        'Models failed (${res.statusCode})',
        statusCode: res.statusCode,
      );
    }
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final data = body['data'];
    if (data is! List) return const [];
    return data
        .whereType<Map<String, dynamic>>()
        .map(ModelInfo.fromJson)
        .where((m) => m.id.isNotEmpty)
        .toList();
  }

  Future<JobDone> submitJob({
    required String repoId,
    required String prompt,
    String? model,
    String? resumeId,
    bool forceStub = false,
  }) async {
    final body = <String, dynamic>{
      'repo_id': repoId.trim(),
      'prompt': prompt,
      if (model != null && model.trim().isNotEmpty) 'model': model.trim(),
      if (resumeId != null && resumeId.trim().isNotEmpty)
        'resume_id': resumeId.trim(),
      if (forceStub) 'force_stub': true,
    };
    final res = await _post(
      Uri.parse('$_base/v1/jobs'),
      headers: _headers,
      body: jsonEncode(body),
    );
    if (res.statusCode == 401) {
      throw SchedulerException('Unauthorized', statusCode: 401);
    }
    if (res.statusCode < 200 || res.statusCode >= 300) {
      final msg = res.body.trim().isEmpty
          ? 'Job failed (${res.statusCode})'
          : res.body.trim();
      throw SchedulerException(msg, statusCode: res.statusCode);
    }
    final map = jsonDecode(res.body) as Map<String, dynamic>;
    return JobDone.fromJson(map);
  }
}
