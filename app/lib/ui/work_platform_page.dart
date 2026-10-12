import 'dart:async';

import 'package:flutter/material.dart';

import '../auth/session.dart';
import '../scheduler/scheduler_api.dart';
import '../theme.dart';
import 'job_chat_page.dart';

/// Job chat scoped to a selected worker (`repo_id`).
class WorkPlatformPage extends StatefulWidget {
  const WorkPlatformPage({
    super.key,
    required this.session,
    required this.worker,
    required this.repoId,
    required this.clusterName,
    required this.nodeName,
  });

  final Session session;
  final WorkerInfo worker;
  final String repoId;
  final String clusterName;
  final String nodeName;

  @override
  State<WorkPlatformPage> createState() => _WorkPlatformPageState();
}

class _WorkPlatformPageState extends State<WorkPlatformPage> {
  final _chatKey = GlobalKey<JobChatPageState>();

  JobChatPageState? get _chat => _chatKey.currentState;

  @override
  Widget build(BuildContext context) {
    final fleetId = widget.session.fleetId ?? '';
    final chat = _chat;
    final modelLabel = chat?.modelToolbarLabel ?? 'Model';
    final modelEnabled = chat?.modelPickerEnabled ?? false;

    return Scaffold(
      appBar: AppBar(
        title: Text(widget.worker.cardTitle),
        actions: [
          Padding(
            padding: const EdgeInsets.only(right: 2),
            child: Tooltip(
              message: chat?.modelToolbarTooltip ?? 'Select model',
              child: TextButton(
                onPressed: modelEnabled
                    ? () => unawaited(chat?.openModelPicker() ?? Future.value())
                    : null,
                style: TextButton.styleFrom(
                  foregroundColor: AnvilColors.text,
                  disabledForegroundColor: AnvilColors.muted,
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                  minimumSize: Size.zero,
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  visualDensity: VisualDensity.compact,
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(
                      Icons.auto_awesome,
                      size: 16,
                      color: modelEnabled
                          ? AnvilColors.accent
                          : AnvilColors.muted,
                    ),
                    const SizedBox(width: 5),
                    ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 110),
                      child: Text(
                        modelLabel,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                    const Icon(Icons.expand_more_rounded, size: 18),
                  ],
                ),
              ),
            ),
          ),
          IconButton(
            tooltip: 'History',
            onPressed: () =>
                unawaited(chat?.openHistoryDrawer() ?? Future.value()),
            icon: const Icon(Icons.history),
          ),
          IconButton(
            tooltip: 'New chat',
            onPressed: () =>
                unawaited(chat?.newConversation() ?? Future.value()),
            icon: const Icon(Icons.add_comment_outlined),
          ),
        ],
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(28),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
            child: Align(
              alignment: Alignment.centerLeft,
              child: Text(
                [
                  widget.clusterName,
                  widget.nodeName,
                  if (widget.worker.branch.isNotEmpty) widget.worker.repoLabel,
                  if (widget.worker.description.isNotEmpty)
                    widget.worker.description,
                ].join(' · '),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(color: AnvilColors.muted, fontSize: 12),
              ),
            ),
          ),
        ),
      ),
      body: JobChatPage(
        key: _chatKey,
        session: widget.session,
        fixedRepoId: widget.repoId,
        platformMode: true,
        threadFleetId: fleetId,
        threadRepoId: widget.repoId,
        onToolbarChanged: () {
          if (mounted) setState(() {});
        },
      ),
    );
  }
}
