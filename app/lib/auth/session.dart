/// Authenticated session after anvil-web login (fleet optional until selected).
class Session {
  const Session({
    required this.webBaseUrl,
    required this.token,
    this.schedulerBaseUrl = '',
    this.fleetId,
    this.fleetName,
    this.expiresAt,
    this.username,
  });

  /// anvil-web origin, no trailing slash.
  final String webBaseUrl;

  /// Selected fleet scheduler origin (empty until fleet chosen).
  final String schedulerBaseUrl;

  final String? fleetId;
  final String? fleetName;

  /// Bearer from `POST /api/v1/auth/login`.
  final String token;

  final DateTime? expiresAt;
  final String? username;

  bool get isSignedIn => token.isNotEmpty && webBaseUrl.isNotEmpty;

  bool get hasFleet =>
      isSignedIn &&
      schedulerBaseUrl.isNotEmpty &&
      (fleetId != null && fleetId!.isNotEmpty);

  Session copyWith({
    String? webBaseUrl,
    String? schedulerBaseUrl,
    String? fleetId,
    String? fleetName,
    String? token,
    DateTime? expiresAt,
    String? username,
    bool clearFleet = false,
  }) {
    return Session(
      webBaseUrl: webBaseUrl ?? this.webBaseUrl,
      schedulerBaseUrl:
          clearFleet ? '' : (schedulerBaseUrl ?? this.schedulerBaseUrl),
      fleetId: clearFleet ? null : (fleetId ?? this.fleetId),
      fleetName: clearFleet ? null : (fleetName ?? this.fleetName),
      token: token ?? this.token,
      expiresAt: expiresAt ?? this.expiresAt,
      username: username ?? this.username,
    );
  }
}
