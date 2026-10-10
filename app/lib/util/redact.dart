/// Redact secrets for logs (show prefix only).
String redactSecret(String? value, {int keep = 4}) {
  if (value == null || value.isEmpty) return '(empty)';
  if (value.length <= keep) return '***';
  return '${value.substring(0, keep)}…';
}
