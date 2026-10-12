import 'package:anvil_app/app.dart';
import 'package:anvil_app/auth/auth_controller.dart';
import 'package:anvil_app/auth/session.dart';
import 'package:anvil_app/auth/session_store.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('signed out shows login with username', (tester) async {
    final auth = AuthController(store: MemorySessionStore());
    auth.status = AuthStatus.signedOut;
    await tester.pumpWidget(AnvilApp(auth: auth));
    expect(find.text('Sign in'), findsOneWidget);
    expect(find.text('Username'), findsOneWidget);
  });

  testWidgets('signed in shows clusters browser', (tester) async {
    final store = MemorySessionStore();
    await store.save(const Session(
      webBaseUrl: 'http://127.0.0.1:17900',
      token: 'tok',
      username: 'admin',
    ));
    final auth = AuthController(store: store);
    await auth.bootstrap();
    await tester.pumpWidget(AnvilApp(auth: auth));
    await tester.pump();
    expect(find.text('Clusters'), findsOneWidget);
  });

  testWidgets('signed in with fleet still shows clusters browser', (tester) async {
    final store = MemorySessionStore();
    await store.save(const Session(
      webBaseUrl: 'http://127.0.0.1:17900',
      schedulerBaseUrl: 'http://127.0.0.1:17800',
      fleetId: 'local',
      fleetName: 'Local cluster',
      token: 'tok',
      username: 'admin',
    ));
    final auth = AuthController(store: store);
    await auth.bootstrap();
    await tester.pumpWidget(AnvilApp(auth: auth));
    await tester.pump();
    expect(find.text('Clusters'), findsOneWidget);
  });
}
