import 'package:flutter/material.dart';

import 'auth/auth_controller.dart';
import 'theme.dart';
import 'ui/auth_gate.dart';

class AnvilApp extends StatelessWidget {
  const AnvilApp({super.key, required this.auth});

  final AuthController auth;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Anvil',
      theme: anvilTheme(),
      home: AuthGate(auth: auth),
    );
  }
}
