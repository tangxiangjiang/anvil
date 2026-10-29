/*
 * Bundles Shiki's browser highlighter into one offline webview script.
 *
 * Shiki publishes ESM with dynamic grammar imports. A VS Code webview has no
 * package resolver, so esbuild folds the selected bundle, grammars, themes and
 * Oniguruma runtime into one ordinary script that exposes a small global.
 */
import { copyFile, mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');
const SOURCE = join(ROOT, 'node_modules', 'shiki');
const TARGET = join(ROOT, 'media', 'vendor', 'shiki');
const EXPECTED = '4.5.0';

const pkg = JSON.parse(await readFile(join(SOURCE, 'package.json'), 'utf8'));
if (pkg.version !== EXPECTED) {
  throw new Error(`shiki is ${pkg.version}; this script vendors ${EXPECTED}. Update both together.`);
}

await mkdir(TARGET, { recursive: true });
await build({
  stdin: {
    contents:
      "import { createHighlighter } from 'shiki/bundle/full';" +
      'self.anvilShiki={createHighlighter};',
    resolveDir: ROOT,
    sourcefile: 'anvil-shiki-entry.mjs',
    loader: 'js',
  },
  bundle: true,
  format: 'iife',
  platform: 'browser',
  target: 'es2022',
  minify: true,
  legalComments: 'none',
  outfile: join(TARGET, 'shiki.min.js'),
});
await copyFile(join(SOURCE, 'LICENSE'), join(TARGET, 'LICENSE'));
await writeFile(
  join(TARGET, 'VERSION'),
  `${pkg.version}\n\nBundled from shiki/bundle/full by scripts/vendor-shiki.mjs.\nShiki is MIT licensed; see LICENSE.\n`,
  'utf8',
);

console.log(`vendored shiki ${pkg.version} into media/vendor/shiki`);
