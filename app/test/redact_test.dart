import 'package:anvil_app/util/redact.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('redactSecret', () {
    expect(redactSecret(null), '(empty)');
    expect(redactSecret('abcd'), '***');
    expect(redactSecret('abcdefgh'), 'abcd…');
  });
}
