import 'package:flutter/material.dart';

import '../auth/auth_controller.dart';
import 'job_chat_page.dart';
import 'models_page.dart';
import 'nodes_page.dart';

class HomeShell extends StatefulWidget {
  const HomeShell({super.key, required this.auth});

  final AuthController auth;

  @override
  State<HomeShell> createState() => _HomeShellState();
}

class _HomeShellState extends State<HomeShell> {
  int _index = 0;

  @override
  Widget build(BuildContext context) {
    final session = widget.auth.session!;
    final fleetTitle = session.fleetName ?? session.fleetId ?? 'Fleet';
    final titles = ['Cluster', 'Jobs', 'Models'];
    final pages = [
      NodesPage(session: session),
      JobChatPage(session: session),
      ModelsPage(session: session),
    ];
    return Scaffold(
      appBar: AppBar(
        title: Text('${titles[_index]} · $fleetTitle'),
        leading: IconButton(
          tooltip: 'Fleets',
          onPressed: () => widget.auth.leaveFleet(),
          icon: const Icon(Icons.arrow_back),
        ),
        actions: [
          if (session.username != null)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              child: Center(child: Text(session.username!)),
            ),
          IconButton(
            tooltip: 'Sign out',
            onPressed: () => widget.auth.signOut(),
            icon: const Icon(Icons.logout),
          ),
        ],
      ),
      body: IndexedStack(index: _index, children: pages),
      bottomNavigationBar: NavigationBar(
        selectedIndex: _index,
        onDestinationSelected: (i) => setState(() => _index = i),
        destinations: const [
          NavigationDestination(
            icon: Icon(Icons.hub_outlined),
            selectedIcon: Icon(Icons.hub),
            label: 'Cluster',
          ),
          NavigationDestination(
            icon: Icon(Icons.chat_bubble_outline),
            selectedIcon: Icon(Icons.chat_bubble),
            label: 'Jobs',
          ),
          NavigationDestination(
            icon: Icon(Icons.memory_outlined),
            selectedIcon: Icon(Icons.memory),
            label: 'Models',
          ),
        ],
      ),
    );
  }
}
