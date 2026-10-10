/// Authenticated session after anvil-web login.
class Session {
  const Session({
    required this.webBaseUrl,
    required this.schedulerBaseUrl,
    required this.token,
    this.expiresAt,
    this.username,
  });

  /// anvil-web origin, no trailing slash.
  final String webBaseUrl;

  /// anvil-scheduler app HTTP origin, no trailing slash.
  final String schedulerBaseUrl;

  /// Bearer from `POST /api/v1/auth/login`.
  final String token;

  final DateTime? expiresAt;
  final String? username;

  bool get isAuthenticated =>
      token.isNotEmpty &&
      webBaseUrl.isNotEmpty &&
      schedulerBaseUrl.isNotEmpty;
}
