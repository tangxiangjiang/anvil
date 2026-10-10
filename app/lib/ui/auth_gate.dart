import 'package:flutter/material.dart';

import '../auth/auth_controller.dart';
import 'home_shell.dart';
import 'login_page.dart';

class AuthGate extends StatelessWidget {
  const AuthGate({super.key, required this.auth});

  final AuthController auth;

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: auth,
      builder: (context, _) {
        switch (auth.status) {
          case AuthStatus.unknown:
            return const Scaffold(
              body: Center(child: CircularProgressIndicator()),
            );
          case AuthStatus.signedOut:
            return LoginPage(auth: auth);
          case AuthStatus.signedIn:
            return HomeShell(auth: auth);
        }
      },
    );
  }
}
