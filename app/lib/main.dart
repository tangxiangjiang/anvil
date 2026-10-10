import 'package:flutter/material.dart';

import 'app.dart';
import 'auth/auth_controller.dart';
import 'auth/session_store.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final auth = AuthController(store: SecureSessionStore());
  await auth.bootstrap();
  runApp(AnvilApp(auth: auth));
}
