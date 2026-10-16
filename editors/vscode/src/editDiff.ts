/**
 * The change a tool call carried, read from the call's own arguments.
 *
 * This is what answers "what did that edit do?" for a card whose call is over —
 * including one replayed from the store, which has nothing but the arguments.
 * The approval card's preview cannot answer it: the SDK computes a preview only
 * inside the branch where it is about to ask, so a call that ran under
 * `accept_edits`, under `bypass`, because a rule allowed it, or in a workspace
 * with no permissions file has none at all. And a replayed call never had one.
 *
 * So the derivation is a pure function of `(name, args)` — no `vscode`, no host,
 * nothing thrown — and it is the same call for a live card and a replayed one.
 *
 * What it can honestly say is bounded by what the arguments carry: the two
 * sides of an `edit_file`, the body a `write_file` wrote, the patch text of an
 * `apply_patch`. When the change tracker still holds the file's pre-image, the
 * better answer is that image against disk, which is what `planReview` prefers.
 * A call that changed no file has nothing to offer, and offers nothing.
 */

import { toolCallPaths } from './mentions';

/** What a call's arguments say it changed. */
export type Review =
  | { kind: 'edit'; path: string; before: string; after: string }
  | { kind: 'write'; path: string; body: string }
  | { kind: 'patch'; paths: string[]; patch: string };

/**
 * The change a call carries, or undefined when its arguments do not describe
 * one. Malformed arguments are undefined rather than an exception: they are
 * model output, and a card that cannot offer a review is still a card.
 *
 * The paths come from `toolCallPaths`, the same derivation the change tracker
 * uses to decide what to snapshot. Sharing it is what keeps two answers from
 * drifting — "which files did this call touch" and "does this call have a
 * change to review" are the same question asked twice.
 */
export function reviewOf(name: string | undefined, args: unknown): Review | undefined {
  if (!name || !args || typeof args !== 'object') return undefined;
  const fields = args as Record<string, unknown>;
  const paths = toolCallPaths(name, args);

  if (name === 'edit_file') {
    const before = fields.old_string;
    const after = fields.new_string;
    if (paths.length === 0 || typeof before !== 'string' || typeof after !== 'string') {
      return undefined;
    }
    return { kind: 'edit', path: paths[0], before, after };
  }

  if (name === 'write_file') {
    const body = fields.content;
    if (paths.length === 0 || typeof body !== 'string') return undefined;
    return { kind: 'write', path: paths[0], body };
  }

  if (name === 'apply_patch') {
    const patch = fields.patch;
    if (typeof patch !== 'string' || patch === '') return undefined;
    return { kind: 'patch', paths, patch };
  }

  return undefined;
}

/**
 * The pre-image the change tracker holds for a path, as much of it as a review
 * needs. Structural rather than `ChangedFile`, because that type lives in a
 * module that imports `vscode` and this one must not.
 */
export interface PreImage {
  before: string;
  /** The turn deleted the file, so there is no disk side left to diff against. */
  deleted?: boolean;
}

/** How a review is to be opened. The panel executes this; it does not decide it. */
export type ReviewPlan =
  | { kind: 'disk'; path: string; before: string; deleted: boolean }
  | { kind: 'memory'; label: string; before: string; after: string; title: string }
  | { kind: 'text'; label: string; body: string };

/**
 * Chooses the surface for a review, and the surface is what decides how honest
 * the answer is.
 *
 * A pre-image is the file as it stood before the turn, so diffing it against
 * disk is the real change — the same thing the approval card and the
 * changed-files row open. Without one, all that is left is what the arguments
 * carried, and the titles say so: a `write_file` shown against an empty side is
 * the body the call wrote, not a claim that the file was empty before it.
 *
 * A patch spans files and so has no single pre-image, which is why it is the
 * one case that ignores one. It opens as its own text, which is the fallback
 * the approval card takes for it too.
 */
export function planReview(review: Review, preImage?: PreImage): ReviewPlan {
  if (review.kind === 'patch') {
    return { kind: 'text', label: patchLabel(review.paths), body: review.patch };
  }

  if (preImage) {
    return {
      kind: 'disk',
      path: review.path,
      before: preImage.before,
      deleted: preImage.deleted === true,
    };
  }

  if (review.kind === 'edit') {
    return {
      kind: 'memory',
      label: review.path,
      before: review.before,
      after: review.after,
      title: `${review.path} (what this call replaced)`,
    };
  }

  return {
    kind: 'memory',
    label: review.path,
    before: '',
    after: review.body,
    title: `${review.path} (the body this call wrote)`,
  };
}

/**
 * The document name a patch opens under. One file gets that file's name, so
 * several patches open at once can still be told apart; a patch over several
 * files falls back to the patch itself, whose headers name every one of them.
 */
function patchLabel(paths: string[]): string {
  if (paths.length !== 1) return 'patch.diff';
  const parts = paths[0].split('/');
  return `${parts[parts.length - 1] || 'patch'}.diff`;
}
