import 'package:flutter/material.dart';

import '../auth/auth_controller.dart';
import '../config.dart';

class LoginPage extends StatefulWidget {
  const LoginPage({super.key, required this.auth});

  final AuthController auth;

  @override
  State<LoginPage> createState() => _LoginPageState();
}

class _LoginPageState extends State<LoginPage> {
  late final TextEditingController _user;
  late final TextEditingController _pass;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _user = TextEditingController(text: 'admin');
    _pass = TextEditingController();
  }

  @override
  void dispose() {
    _user.dispose();
    _pass.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() => _busy = true);
    final ok = await widget.auth.login(
      webBaseUrl: kAnvilWebUrl,
      username: _user.text,
      password: _pass.text,
    );
    if (!mounted) return;
    setState(() => _busy = false);
    if (!ok && widget.auth.lastError != null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.auth.lastError!)),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: Container(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            center: Alignment.topCenter,
            radius: 1.2,
            colors: [Color(0xFF1A2A3A), Color(0xFF0F1419)],
            stops: [0.0, 0.55],
          ),
        ),
        child: SafeArea(
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 420),
              child: ListView(
                padding: const EdgeInsets.all(24),
                children: [
                  const Text(
                    'Anvil',
                    style: TextStyle(
                      fontSize: 28,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 0.4,
                      color: Color(0xFFE7EEF7),
                    ),
                  ),
                  const SizedBox(height: 8),
                  const Text(
                    'Sign in, then pick a scheduler cluster.',
                    style: TextStyle(color: Color(0xFF8B9BB0), height: 1.35),
                  ),
                  const SizedBox(height: 24),
                  TextField(
                    controller: _user,
                    decoration: const InputDecoration(labelText: 'Username'),
                    enabled: !_busy,
                    textInputAction: TextInputAction.next,
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: _pass,
                    decoration: const InputDecoration(labelText: 'Password'),
                    obscureText: true,
                    enabled: !_busy,
                    onSubmitted: (_) => _busy ? null : _submit(),
                  ),
                  const SizedBox(height: 20),
                  FilledButton(
                    onPressed: _busy ? null : _submit,
                    child: _busy
                        ? const SizedBox(
                            height: 20,
                            width: 20,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Text('Sign in'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
