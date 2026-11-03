import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const EXTENSION = readFileSync(join(ROOT, 'src', 'extension.ts'), 'utf8');
const SEARCH = readFileSync(join(ROOT, 'src', 'sessionSearch.ts'), 'utf8');
const CHAT = readFileSync(join(ROOT, 'media', 'chat.js'), 'utf8');

test('Sessions content search is debounced and suppresses superseded replies', () => {
  assert.match(EXTENSION, /setTimeout\([\s\S]*?250\)/);
  assert.match(EXTENSION, /const requestEpoch = epoch\.supersede\(\)/);
  assert.match(EXTENSION, /!epoch\.current\(requestEpoch\)/);
  assert.match(EXTENSION, /requestAbort\?\.abort\(\)/);
  assert.match(EXTENSION, /abort\.signal/);
  assert.match(EXTENSION, /hub\.searchSessions\(/);
});

test('paging, stale cursors, and old Hosts have explicit picker states', () => {
  assert.match(EXTENSION, /chosen\.choiceKind === 'loadMore'/);
  assert.match(EXTENSION, /SESSION_SEARCH_CURSOR_ERROR/);
  assert.match(EXTENSION, /snapshot expired/);
  assert.match(EXTENSION, /CodeMethodNotFound/);
  assert.match(EXTENSION, /title\/id search only/);
  assert.match(EXTENSION, /result\.partial/);
});

test('filters and sensitive tool arguments are visible and reset the search', () => {
  assert.match(EXTENSION, /Role: \$\{filters\.role\}/);
  assert.match(EXTENSION, /Kind: \$\{filters\.kind\}/);
  assert.match(EXTENSION, /Tool arguments included; they may contain secrets/);
  assert.match(EXTENSION, /nextCursor = undefined/);
  assert.match(SEARCH, /include_args: filters\.includeArgs/);
});

test('results remain plain Quick Pick text and open the selected session', () => {
  assert.match(SEARCH, /never interpreted as Markdown, HTML, or a[\s\S]*command URI/);
  assert.doesNotMatch(SEARCH, /MarkdownString|openExternal|executeCommand/);
  assert.match(EXTENSION, /hub\.openSearchHit\(chosen\.hit\)/);
  assert.match(EXTENSION, /matchOnDetail = true/);
  assert.match(CHAT, /case 'historyContext'/);
  assert.match(CHAT, /textContent/);
  assert.doesNotMatch(CHAT, /historyContext[\s\S]{0,1000}innerHTML/);
});
