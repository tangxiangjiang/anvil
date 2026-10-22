/**
 * The workspace permission rule file, as the extension composes it.
 *
 * No `vscode` import: what is here is the part with rules in it — the bytes a
 * new file starts as, and where a problem points — so it is tested without an
 * extension host. The file belongs to the SDK (`permissions.Load` reads it), so
 * nothing here decides what a rule means; it only has to write bytes that
 * loader accepts.
 */

import { RuleProblem } from './protocol';

/**
 * The rule-file version, matching `permissions.Version`. It is written out
 * rather than left to the loader's default because the file is meant to be
 * committed and read by a person: a version a future build refuses is better
 * than a file that silently means something else.
 */
export const RULES_FILE_VERSION = 1;

/**
 * The bytes of a rule file that decides nothing.
 *
 * A file rather than nothing at all: "there is no policy here" and "the policy
 * is empty" are different states, and a user who asked to see the rules should
 * get a file they can type into rather than an editor explaining there is
 * nothing to open.
 */
export function emptyRulesFile(): string {
  return `${JSON.stringify({ version: RULES_FILE_VERSION, rules: [] }, null, 2)}\n`;
}

/**
 * The bytes of a user rule file that decides nothing (0.46).
 *
 * A different shape from the workspace's, on purpose: the user file carries the
 * protected-path globs and no starting mode. `protected` is written out even
 * when empty so the one field that only exists here is visible to the person
 * about to type into the file.
 */
export function emptyUserRulesFile(): string {
  return `${JSON.stringify({ version: RULES_FILE_VERSION, protected: [], rules: [] }, null, 2)}\n`;
}

/**
 * Where a problem points, as zero-based positions a `vscode.Position` takes.
 *
 * The parser counts lines and columns from one, and reports zero when the
 * problem belongs to no particular place — a version it does not read, a key it
 * does not know. A problem with no place is reported at the top of the file,
 * which is where a reader starts looking anyway.
 */
export function problemPosition(problem: RuleProblem): { line: number; column: number } {
  const line = typeof problem.line === 'number' && problem.line > 0 ? problem.line - 1 : 0;
  const column = typeof problem.column === 'number' && problem.column > 0 ? problem.column - 1 : 0;
  return { line, column };
}

/**
 * One line naming the file and the place, in the shape a compiler error has —
 * because that shape is what makes a path clickable in a terminal and
 * recognizable in a log.
 */
export function describeProblem(path: string, problem: RuleProblem): string {
  const position = problemPosition(problem);
  const where = problem.line ? `${path}:${position.line + 1}:${position.column + 1}` : path;
  return problem.message ? `${where}: ${problem.message}` : where;
}
