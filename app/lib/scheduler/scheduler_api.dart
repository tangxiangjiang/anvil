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
    this.agentId,
    this.result,
    this.error,
    this.code,
    this.steps,
  });

  final String jobId;
  final String status;
  final String? agentId;
  final String? result;
  final String? error;
  final String? code;
  final int? steps;

  factory JobDone.fromJson(Map<String, dynamic> j) {
    return JobDone(
      jobId: (j['job_id'] as String?) ?? '',
      status: (j['status'] as String?) ?? '',
      agentId: (j['agent_id'] as String?)?.trim(),
      result: j['result'] as String?,
      error: j['error'] as String?,
      code: j['code'] as String?,
      steps: j['steps'] as int?,
    );
  }
}

class ModelInfo {
  const ModelInfo({
    required this.id,
    this.ownedBy,
    this.provider,
    this.vision = false,
    this.params = const [],
  });
  final String id;
  final String? ownedBy;
  final String? provider;
  final bool vision;
  final List<String> params;

  bool get isEmbedding {
    final by = (ownedBy ?? '').toLowerCase();
    if (by == 'embedding') return true;
    if (params.any((p) => p.toLowerCase() == 'embedding')) return true;
    final idLower = id.toLowerCase();
    return idLower.contains('embedding') || idLower.contains('bge-m3');
  }

  /// Short tier label for pickers (complex / general / …).
  String get tierLabel {
    final by = (ownedBy ?? '').trim();
    return by.isEmpty ? 'model' : by;
  }

  String get pickerLabel {
    final bits = <String>[id, tierLabel];
    if (vision) bits.add('vision');
    return bits.join('  ·  ');
  }

  factory ModelInfo.fromJson(Map<String, dynamic> j) {
    final params = <String>[];
    final raw = j['params'];
    if (raw is List) {
      for (final p in raw) {
        if (p is String && p.trim().isNotEmpty) params.add(p.trim());
      }
    }
    return ModelInfo(
      id: (j['id'] as String?) ?? '',
      ownedBy: j['owned_by'] as String?,
      provider: j['provider'] as String?,
      vision: j['vision'] == true,
      params: params,
    );
  }
}

class ModelsCatalog {
  const ModelsCatalog({
    required this.models,
    this.selectedModel,
    this.selectedProvider,
  });
  final List<ModelInfo> models;
  final String? selectedModel;
  final String? selectedProvider;
}

class WorkerInfo {
  const WorkerInfo({
    required this.id,
    required this.status,
    this.name = '',
    this.description = '',
    this.repoId = '',
    this.projectPath = '',
    this.projectRel = '',
    this.gitRemote = '',
    this.gitBranch,
    this.jobId = '',
  });

  final String id;
  final String status;
  final String name;
  final String description;
  final String repoId;
  final String projectPath;
  final String projectRel;
  final String gitRemote;

  /// Nullable so hot-reload of in-memory instances (pre-field) does not crash.
  final String? gitBranch;
  final String jobId;

  String get displayName {
    final n = name;
    return n.isNotEmpty ? n : id;
  }

  /// Checked-out branch, or empty when unknown / not a git repo.
  String get branch => gitBranch ?? '';

  /// Sandbox project path / repo id for list titles (not the operator name).
  String get repoLabel {
    final rel = projectRel;
    if (rel.isNotEmpty) return rel;
    final path = projectPath;
    if (path.isNotEmpty) {
      final p = path.replaceAll('\\', '/');
      final i = p.lastIndexOf('/');
      return i >= 0 && i < p.length - 1 ? p.substring(i + 1) : p;
    }
    final rid = repoId;
    if (rid.isNotEmpty) return rid;
    return id;
  }

  /// Prefer checked-out branch for the card title.
  String get cardTitle {
    final b = branch;
    return b.isNotEmpty ? b : repoLabel;
  }

  factory WorkerInfo.fromJson(Map<String, dynamic> j) {
    return WorkerInfo(
      id: _jsonString(j['id']),
      status: _jsonString(j['status']),
      name: _jsonString(j['name']),
      description: _jsonString(j['description']),
      repoId: _jsonString(j['repo_id']),
      projectPath: _jsonString(j['project_path']),
      projectRel: _jsonString(j['project_rel']),
      gitRemote: _jsonString(j['git_remote']),
      gitBranch: _jsonString(j['git_branch']),
      jobId: _jsonString(j['job_id']),
    );
  }
}

String _jsonString(Object? v) {
  if (v == null) return '';
  if (v is String) return v.trim();
  return v.toString().trim();
}

class NodeInfo {
  const NodeInfo({
    required this.id,
    required this.maxWorkers,
    required this.idle,
    required this.busy,
    required this.accepting,
    required this.stale,
    this.name = '',
    this.description = '',
    this.hostname = '',
    this.sandboxDir = '',
    this.workers = const [],
    this.capabilities = const [],
  });

  final String id;
  final String name;
  final String description;
  final String hostname;
  final String sandboxDir;
  final int maxWorkers;
  final int idle;
  final int busy;
  final bool accepting;
  final bool stale;
  final List<WorkerInfo> workers;
  final List<String> capabilities;

  String get displayName => name.isNotEmpty ? name : id;
  String get pcName => hostname.isNotEmpty ? hostname : '—';

  factory NodeInfo.fromJson(Map<String, dynamic> j) {
    final workers = <WorkerInfo>[];
    final rawW = j['workers'];
    if (rawW is List) {
      for (final w in rawW) {
        if (w is Map<String, dynamic>) {
          workers.add(WorkerInfo.fromJson(w));
        }
      }
    }
    final caps = <String>[];
    final rawC = j['capabilities'];
    if (rawC is List) {
      for (final c in rawC) {
        if (c is String) caps.add(c);
      }
    }
    return NodeInfo(
      id: (j['id'] as String?) ?? '',
      name: (j['name'] as String?)?.trim() ?? '',
      description: (j['description'] as String?)?.trim() ?? '',
      hostname: (j['hostname'] as String?)?.trim() ?? '',
      sandboxDir: (j['sandbox_dir'] as String?)?.trim() ?? '',
      maxWorkers: (j['max_workers'] as num?)?.toInt() ?? 0,
      idle: (j['idle'] as num?)?.toInt() ?? 0,
      busy: (j['busy'] as num?)?.toInt() ?? 0,
      accepting: j['accepting'] != false,
      stale: j['stale'] == true,
      workers: workers,
      capabilities: caps,
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

  Future<List<NodeInfo>> listNodes() async {
    final res = await _get(Uri.parse('$_base/v1/nodes'), headers: _headers);
    _check(res, 'Nodes');
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final list = body['nodes'];
    if (list is! List) return const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(NodeInfo.fromJson)
        .where((n) => n.id.isNotEmpty)
        .toList();
  }

  Future<void> patchNode(
    String id, {
    int? maxWorkers,
    bool? accepting,
  }) async {
    final body = <String, dynamic>{
      if (maxWorkers != null) 'max_workers': maxWorkers,
      if (accepting != null) 'accepting': accepting,
    };
    if (body.isEmpty) {
      throw SchedulerException('max_workers or accepting required');
    }
    final res = await http.patch(
      Uri.parse('$_base/v1/nodes/${Uri.encodeComponent(id)}'),
      headers: _headers,
      body: jsonEncode(body),
    );
    _check(res, 'Patch node');
  }

  Future<List<ModelInfo>> listModels() async {
    final cat = await listModelsCatalog();
    return cat.models;
  }

  Future<ModelsCatalog> listModelsCatalog() async {
    final res = await _get(Uri.parse('$_base/v1/models'), headers: _headers);
    _check(res, 'Models');
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final data = body['data'];
    final models = data is List
        ? data
            .whereType<Map<String, dynamic>>()
            .map(ModelInfo.fromJson)
            .where((m) => m.id.isNotEmpty)
            .toList()
        : const <ModelInfo>[];
    return ModelsCatalog(
      models: models,
      selectedModel: body['selected_model'] as String?,
      selectedProvider: body['selected_provider'] as String?,
    );
  }

  Future<void> selectModel({required String model, String? provider}) async {
    final res = await http.put(
      Uri.parse('$_base/v1/llm/selection'),
      headers: _headers,
      body: jsonEncode({
        'model': model.trim(),
        if (provider != null && provider.trim().isNotEmpty)
          'provider': provider.trim(),
      }),
    );
    _check(res, 'Select model');
  }

  Future<JobDone> submitJob({
    required String repoId,
    required String prompt,
    String? model,
    String? provider,
    String? resumeId,
    bool forceStub = false,
  }) async {
    final body = <String, dynamic>{
      'repo_id': repoId.trim(),
      'prompt': prompt,
      // Omit model/provider to use scheduler selection (gateway catalog default).
      if (model != null && model.trim().isNotEmpty) 'model': model.trim(),
      if (provider != null && provider.trim().isNotEmpty)
        'provider': provider.trim(),
      if (resumeId != null && resumeId.trim().isNotEmpty)
        'resume_id': resumeId.trim(),
      if (forceStub) 'force_stub': true,
    };
    final res = await _post(
      Uri.parse('$_base/v1/jobs'),
      headers: _headers,
      body: jsonEncode(body),
    );
    _check(res, 'Job');
    final map = jsonDecode(res.body) as Map<String, dynamic>;
    return JobDone.fromJson(map);
  }

  void _check(http.Response res, String label) {
    if (res.statusCode == 401) {
      throw SchedulerException('Unauthorized', statusCode: 401);
    }
    if (res.statusCode < 200 || res.statusCode >= 300) {
      final msg = res.body.trim().isEmpty
          ? '$label failed (${res.statusCode})'
          : res.body.trim();
      throw SchedulerException(msg, statusCode: res.statusCode);
    }
  }
}
