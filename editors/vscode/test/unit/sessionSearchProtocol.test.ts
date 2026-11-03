import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';

const ROOT = join(__dirname, '..', '..', '..');
const PROTOCOL = readFileSync(join(ROOT, 'src', 'protocol.ts'), 'utf8');
const HOST = readFileSync(join(ROOT, 'src', 'hostManager.ts'), 'utf8');
const HUB = readFileSync(join(ROOT, 'src', 'hub.ts'), 'utf8');

test('session search has one mirrored opaque-cursor protocol', () => {
  assert.match(PROTOCOL, /SessionSearch: 'session\.search'/);
  assert.match(PROTOCOL, /SESSION_SEARCH_CURSOR_ERROR = -32007/);
  for (const field of [
    'query', 'agent_id', 'roles', 'kinds', 'after', 'before',
    'limit', 'cursor', 'include_args', 'store',
  ]) {
    assert.match(PROTOCOL, new RegExp(`${field}[?]?:`), `${field} is mirrored`);
  }
  assert.match(PROTOCOL, /next_cursor\?: string/);
  assert.match(PROTOCOL, /record_pos: number/);
  assert.match(PROTOCOL, /match: SessionSearchTextRange\[\]/);
});

test('the extension forwards cursors without decoding traversal state', () => {
  assert.match(HOST, /request<SessionSearchResult>\(Method\.SessionSearch, params\)/);
  assert.match(HUB, /return this\.manager\.searchSessions\(params\)/);
  assert.doesNotMatch(HOST, /atob|Buffer\.from\(.*cursor|JSON\.parse\(.*cursor/);
  assert.doesNotMatch(HUB, /atob|Buffer\.from\(.*cursor|JSON\.parse\(.*cursor/);
});
