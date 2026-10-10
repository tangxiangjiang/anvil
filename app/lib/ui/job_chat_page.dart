import 'package:flutter/material.dart';

import '../auth/session.dart';
import '../scheduler/scheduler_api.dart';

class _Turn {
  const _Turn({required this.prompt, this.done, this.error, this.pending = false});
  final String prompt;
  final JobDone? done;
  final String? error;
  final bool pending;
}

class JobChatPage extends StatefulWidget {
  const JobChatPage({super.key, required this.session});

  final Session session;

  @override
  State<JobChatPage> createState() => _JobChatPageState();
}

class _JobChatPageState extends State<JobChatPage> {
  final _repo = TextEditingController(text: 'demo');
  final _resume = TextEditingController();
  final _model = TextEditingController();
  final _prompt = TextEditingController();
  final _turns = <_Turn>[];
  bool _busy = false;

  SchedulerApi get _api => SchedulerApi(
        baseUrl: widget.session.schedulerBaseUrl,
        token: widget.session.token,
      );

  @override
  void dispose() {
    _repo.dispose();
    _resume.dispose();
    _model.dispose();
    _prompt.dispose();
    super.dispose();
  }

  Future<void> _send() async {
    final prompt = _prompt.text.trim();
    final repo = _repo.text.trim();
    if (prompt.isEmpty || repo.isEmpty || _busy) return;
    setState(() {
      _busy = true;
      _turns.add(_Turn(prompt: prompt, pending: true));
      _prompt.clear();
    });
    final idx = _turns.length - 1;
    try {
      final done = await _api.submitJob(
        repoId: repo,
        prompt: prompt,
        model: _model.text.trim().isEmpty ? null : _model.text.trim(),
        resumeId: _resume.text.trim().isEmpty ? null : _resume.text.trim(),
      );
      if (!mounted) return;
      setState(() {
        _turns[idx] = _Turn(prompt: prompt, done: done);
        _busy = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _turns[idx] = _Turn(prompt: prompt, error: e.toString());
        _busy = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
          child: Column(
            children: [
              TextField(
                controller: _repo,
                decoration: const InputDecoration(
                  labelText: 'repo_id',
                  border: OutlineInputBorder(),
                  isDense: true,
                ),
                enabled: !_busy,
              ),
              const SizedBox(height: 8),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _model,
                      decoration: const InputDecoration(
                        labelText: 'model (optional)',
                        border: OutlineInputBorder(),
                        isDense: true,
                      ),
                      enabled: !_busy,
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: TextField(
                      controller: _resume,
                      decoration: const InputDecoration(
                        labelText: 'resume_id (optional)',
                        border: OutlineInputBorder(),
                        isDense: true,
                      ),
                      enabled: !_busy,
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
        const Divider(height: 1),
        Expanded(
          child: _turns.isEmpty
              ? Center(
                  child: Text(
                    'Submit a prompt; the app waits for job.done on the scheduler.',
                    textAlign: TextAlign.center,
                    style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
                )
              : ListView.builder(
                  padding: const EdgeInsets.all(16),
                  itemCount: _turns.length,
                  itemBuilder: (context, i) {
                    final t = _turns[i];
                    return Padding(
                      padding: const EdgeInsets.only(bottom: 16),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          Align(
                            alignment: Alignment.centerRight,
                            child: Container(
                              padding: const EdgeInsets.all(12),
                              decoration: BoxDecoration(
                                color: Theme.of(context)
                                    .colorScheme
                                    .primaryContainer,
                                borderRadius: BorderRadius.circular(12),
                              ),
                              child: Text(t.prompt),
                            ),
                          ),
                          const SizedBox(height: 8),
                          if (t.pending)
                            const Align(
                              alignment: Alignment.centerLeft,
                              child: Padding(
                                padding: EdgeInsets.all(8),
                                child: CircularProgressIndicator(),
                              ),
                            )
                          else if (t.error != null)
                            Text(
                              t.error!,
                              style: TextStyle(
                                color: Theme.of(context).colorScheme.error,
                              ),
                            )
                          else if (t.done != null) ...[
                            Text(
                              'status: ${t.done!.status}  job: ${t.done!.jobId}'
                              '${t.done!.steps != null ? '  steps: ${t.done!.steps}' : ''}',
                              style: Theme.of(context).textTheme.labelMedium,
                            ),
                            const SizedBox(height: 4),
                            SelectableText(
                              t.done!.error?.isNotEmpty == true
                                  ? t.done!.error!
                                  : (t.done!.result ?? '(no result)'),
                            ),
                          ],
                        ],
                      ),
                    );
                  },
                ),
        ),
        const Divider(height: 1),
        Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _prompt,
                  decoration: const InputDecoration(
                    hintText: 'Prompt',
                    border: OutlineInputBorder(),
                  ),
                  minLines: 1,
                  maxLines: 4,
                  enabled: !_busy,
                  onSubmitted: (_) => _send(),
                ),
              ),
              const SizedBox(width: 8),
              IconButton.filled(
                onPressed: _busy ? null : _send,
                icon: const Icon(Icons.send),
              ),
            ],
          ),
        ),
      ],
    );
  }
}
