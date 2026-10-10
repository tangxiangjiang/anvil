import 'package:anvil_app/app.dart';
import 'package:anvil_app/auth/auth_controller.dart';
import 'package:anvil_app/auth/session_store.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('app boots to login', (tester) async {
    final auth = AuthController(store: MemorySessionStore());
    await auth.bootstrap();
    await tester.pumpWidget(AnvilApp(auth: auth));
    expect(find.text('Anvil'), findsOneWidget);
  });
}
