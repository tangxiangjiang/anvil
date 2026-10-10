import 'package:flutter/material.dart';

import 'auth/auth_controller.dart';
import 'ui/auth_gate.dart';

class AnvilApp extends StatelessWidget {
  const AnvilApp({super.key, required this.auth});

  final AuthController auth;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Anvil',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFF1B4D3E),
          brightness: Brightness.light,
        ),
        useMaterial3: true,
      ),
      home: AuthGate(auth: auth),
    );
  }
}
