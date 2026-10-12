/// Bake anvil-web base URL at build/package time.
///
/// ```bash
/// flutter run --dart-define=ANVIL_WEB_URL=http://10.0.2.2:17900
/// flutter build apk --dart-define=ANVIL_WEB_URL=https://web.example.com
/// ```
const String kAnvilWebUrl = String.fromEnvironment(
  'ANVIL_WEB_URL',
  defaultValue: 'http://127.0.0.1:17900',
);
