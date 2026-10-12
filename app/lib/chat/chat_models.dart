// Local chat session models (cloud-agent Project Chat aligned, client-persisted).

class ChatMessage {
  const ChatMessage({
    required this.id,
    required this.role,
    required this.content,
    this.jobId,
    this.at,
  });

  final String id;
  final String role; // user | assistant
  final String content;
  final String? jobId;
  final String? at;

  Map<String, dynamic> toJson() => {
        'id': id,
        'role': role,
        'content': content,
        if (jobId != null && jobId!.isNotEmpty) 'job_id': jobId,
        if (at != null && at!.isNotEmpty) 'at': at,
      };

  factory ChatMessage.fromJson(Map<String, dynamic> j) {
    return ChatMessage(
      id: (j['id'] as String?) ?? '',
      role: (j['role'] as String?) ?? 'user',
      content: (j['content'] as String?) ?? '',
      jobId: (j['job_id'] as String?)?.trim(),
      at: j['at'] as String?,
    );
  }
}

class ChatSession {
  const ChatSession({
    required this.id,
    required this.fleetId,
    required this.repoId,
    this.agentId,
    this.title,
    this.preview,
    this.model,
    this.messages = const [],
    this.createdAt,
    this.updatedAt,
    this.listedMessageCount,
  });

  final String id;
  final String fleetId;
  final String repoId;
  final String? agentId;
  final String? title;
  final String? preview;
  final String? model;
  final List<ChatMessage> messages;
  final String? createdAt;
  final String? updatedAt;
  /// Present on history index rows (messages omitted).
  final int? listedMessageCount;

  int get messageCount => listedMessageCount ?? messages.length;

  String get displayTitle {
    final t = title?.trim();
    if (t != null && t.isNotEmpty) return t;
    final p = preview?.trim();
    if (p != null && p.isNotEmpty) return p;
    return id;
  }

  ChatSession copyWith({
    String? agentId,
    String? title,
    String? preview,
    String? model,
    List<ChatMessage>? messages,
    String? updatedAt,
    bool clearAgentId = false,
  }) {
    return ChatSession(
      id: id,
      fleetId: fleetId,
      repoId: repoId,
      agentId: clearAgentId ? null : (agentId ?? this.agentId),
      title: title ?? this.title,
      preview: preview ?? this.preview,
      model: model ?? this.model,
      messages: messages ?? this.messages,
      createdAt: createdAt,
      updatedAt: updatedAt ?? this.updatedAt,
      listedMessageCount: listedMessageCount,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'fleet_id': fleetId,
        'repo_id': repoId,
        if (agentId != null && agentId!.isNotEmpty) 'agent_id': agentId,
        if (title != null) 'title': title,
        if (preview != null) 'preview': preview,
        if (model != null) 'model': model,
        'messages': messages.map((m) => m.toJson()).toList(),
        if (createdAt != null) 'created_at': createdAt,
        if (updatedAt != null) 'updated_at': updatedAt,
      };

  /// Summary row for history list (no full messages).
  Map<String, dynamic> toSummaryJson() => {
        'id': id,
        'fleet_id': fleetId,
        'repo_id': repoId,
        if (agentId != null && agentId!.isNotEmpty) 'agent_id': agentId,
        if (title != null) 'title': title,
        if (preview != null) 'preview': preview,
        if (model != null) 'model': model,
        'message_count': messageCount,
        if (createdAt != null) 'created_at': createdAt,
        if (updatedAt != null) 'updated_at': updatedAt,
      };

  factory ChatSession.fromJson(Map<String, dynamic> j) {
    final msgs = <ChatMessage>[];
    final raw = j['messages'];
    if (raw is List) {
      for (final m in raw) {
        if (m is Map<String, dynamic>) {
          msgs.add(ChatMessage.fromJson(m));
        } else if (m is Map) {
          msgs.add(ChatMessage.fromJson(Map<String, dynamic>.from(m)));
        }
      }
    }
    return ChatSession(
      id: (j['id'] as String?) ?? '',
      fleetId: (j['fleet_id'] as String?) ?? '',
      repoId: (j['repo_id'] as String?) ?? '',
      agentId: (j['agent_id'] as String?)?.trim(),
      title: j['title'] as String?,
      preview: j['preview'] as String?,
      model: j['model'] as String?,
      messages: msgs,
      createdAt: j['created_at'] as String?,
      updatedAt: j['updated_at'] as String?,
    );
  }

  factory ChatSession.fromSummaryJson(Map<String, dynamic> j) {
    final mc = j['message_count'];
    return ChatSession(
      id: (j['id'] as String?) ?? '',
      fleetId: (j['fleet_id'] as String?) ?? '',
      repoId: (j['repo_id'] as String?) ?? '',
      agentId: (j['agent_id'] as String?)?.trim(),
      title: j['title'] as String?,
      preview: j['preview'] as String?,
      model: j['model'] as String?,
      messages: const [],
      createdAt: j['created_at'] as String?,
      updatedAt: j['updated_at'] as String?,
      listedMessageCount: mc is int ? mc : int.tryParse('$mc'),
    );
  }
}
