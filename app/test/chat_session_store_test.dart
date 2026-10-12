import 'package:anvil_app/chat/chat_models.dart';
import 'package:anvil_app/chat/chat_session_store.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() {
    FlutterSecureStorage.setMockInitialValues({});
  });

  test('create save list resume session with messages', () async {
    final store = ChatSessionStore();
    final created = await store.createSession(
      fleetId: 'local',
      repoId: 'Test',
      model: 'private/demo',
    );
    expect(created.id, isNotEmpty);

    final saved = await store.saveSession(
      created.copyWith(
        agentId: 'agent-1',
        messages: const [
          ChatMessage(id: 'm1', role: 'user', content: 'hello world'),
          ChatMessage(id: 'm2', role: 'assistant', content: 'hi', jobId: 'j1'),
        ],
      ),
    );
    expect(saved.messageCount, 2);
    expect(saved.agentId, 'agent-1');
    expect(saved.displayTitle.toLowerCase(), contains('hello'));

    final list = await store.listSessions(fleetId: 'local', repoId: 'Test');
    expect(list.length, 1);
    expect(list.first.id, saved.id);
    expect(list.first.messageCount, 2);

    final resumed = await store.resumeOrBlank(fleetId: 'local', repoId: 'Test');
    expect(resumed?.messages.length, 2);
    expect(resumed?.agentId, 'agent-1');
  });
}
