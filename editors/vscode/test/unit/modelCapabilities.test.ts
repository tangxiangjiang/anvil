import { strict as assert } from 'node:assert';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  StoredModelCapabilities,
  normalizeCapabilities,
  normalizeModelCapabilities,
  withGatewayCapabilities,
} from '../../src/models';

const ROOT = join(__dirname, '..', '..', '..');
const SETTINGS = readFileSync(join(ROOT, 'src', 'settings.ts'), 'utf8');
const PROTOCOL = readFileSync(join(ROOT, 'src', 'protocol.ts'), 'utf8');

const adaptive = {
  thinking: 'anthropic_adaptive',
  effort_levels: ['low', 'medium', 'high'],
  max_output_tokens: true,
  vision: true,
};

test('an absent profile remains unknown without model-name inference', () => {
  assert.deepEqual(normalizeModelCapabilities(undefined), {});
  assert.deepEqual(normalizeModelCapabilities({ 'claude-new': undefined }), {});
  assert.doesNotMatch(SETTINGS, /startsWith\(['"](?:claude|gpt)/);
});

test('gateway profiles are normalized and malformed combinations are dropped', () => {
  assert.deepEqual(normalizeCapabilities(adaptive), adaptive);
  assert.equal(normalizeCapabilities({ ...adaptive, thinking: 'future' }), undefined);
  assert.equal(normalizeCapabilities({ ...adaptive, effort_levels: ['low', 'low'] }), undefined);
  assert.equal(
    normalizeCapabilities({
      thinking: 'none',
      effort_levels: ['low'],
      max_output_tokens: false,
      vision: false,
    }),
    undefined,
  );
});

test('an explicit saved profile wins over a gateway catalogue profile', () => {
  const explicit: Record<string, StoredModelCapabilities> = {
    model: {
      source: 'explicit',
      capabilities: {
        thinking: 'none',
        max_output_tokens: false,
        vision: false,
      },
    },
  };
  assert.deepEqual(withGatewayCapabilities(explicit, 'model', adaptive), explicit);

  const discovered = withGatewayCapabilities({}, 'model', adaptive);
  assert.equal(discovered.model.source, 'gateway');
  assert.deepEqual(discovered.model.capabilities, adaptive);
  assert.deepEqual(withGatewayCapabilities(discovered, 'model', undefined), {});
});

test('selected profiles travel on sessions and model catalogue rows', () => {
  assert.match(SETTINGS, /session\.model_capabilities = selectedCapabilities\.capabilities/);
  assert.match(PROTOCOL, /model_capabilities\?: ModelCapabilities/);
  assert.match(PROTOCOL, /capabilities\?: ModelCapabilities/);
});
