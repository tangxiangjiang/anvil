import 'dart:io' show Platform;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';

import '../auth/auth_controller.dart';

String defaultWebUrl() {
  if (kIsWeb) return 'http://127.0.0.1:17900';
  try {
    if (Platform.isAndroid) return 'http://10.0.2.2:17900';
  } catch (_) {}
  return 'http://127.0.0.1:17900';
}

String defaultSchedulerUrl() {
  if (kIsWeb) return 'http://127.0.0.1:17800';
  try {
    if (Platform.isAndroid) return 'http://10.0.2.2:17800';
  } catch (_) {}
  return 'http://127.0.0.1:17800';
}

class LoginPage extends StatefulWidget {
  const LoginPage({super.key, required this.auth});

  final AuthController auth;

  @override
  State<LoginPage> createState() => _LoginPageState();
}

class _LoginPageState extends State<LoginPage> {
  late final TextEditingController _web;
  late final TextEditingController _sched;
  late final TextEditingController _user;
  late final TextEditingController _pass;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _web = TextEditingController(text: defaultWebUrl());
    _sched = TextEditingController(text: defaultSchedulerUrl());
    _user = TextEditingController(text: 'admin');
    _pass = TextEditingController();
  }

  @override
  void dispose() {
    _web.dispose();
    _sched.dispose();
    _user.dispose();
    _pass.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() => _busy = true);
    final ok = await widget.auth.login(
      webBaseUrl: _web.text,
      schedulerBaseUrl: _sched.text,
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
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 420),
            child: ListView(
              padding: const EdgeInsets.all(24),
              children: [
                Text(
                  'Anvil',
                  style: Theme.of(context).textTheme.headlineMedium?.copyWith(
                        fontWeight: FontWeight.w700,
                      ),
                ),
                const SizedBox(height: 8),
                Text(
                  'Sign in with anvil-web, then run jobs on the scheduler.',
                  style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                        color: Theme.of(context).colorScheme.onSurfaceVariant,
                      ),
                ),
                const SizedBox(height: 24),
                TextField(
                  controller: _web,
                  decoration: const InputDecoration(
                    labelText: 'anvil-web URL',
                    border: OutlineInputBorder(),
                  ),
                  keyboardType: TextInputType.url,
                  enabled: !_busy,
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _sched,
                  decoration: const InputDecoration(
                    labelText: 'anvil-scheduler URL',
                    border: OutlineInputBorder(),
                  ),
                  keyboardType: TextInputType.url,
                  enabled: !_busy,
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _user,
                  decoration: const InputDecoration(
                    labelText: 'Username',
                    border: OutlineInputBorder(),
                  ),
                  enabled: !_busy,
                  textInputAction: TextInputAction.next,
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _pass,
                  decoration: const InputDecoration(
                    labelText: 'Password',
                    border: OutlineInputBorder(),
                  ),
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
    );
  }
}
