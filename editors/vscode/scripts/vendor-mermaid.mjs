/*
 * Vendors Mermaid's standalone browser bundle into media/vendor/mermaid.
 *
 * The webview has no network access. Mermaid is therefore a build-time
 * dependency and its browser bundle is copied into the VSIX. The renderer may
 * produce an SVG string internally, but chat.js never injects that markup into
 * the page: it encodes the result as an SVG data-image.
 */
import { copyFile, mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');
const SOURCE = join(ROOT, 'node_modules', 'mermaid');
const TARGET = join(ROOT, 'media', 'vendor', 'mermaid');
const EXPECTED = '12.2.0';

const pkg = JSON.parse(await readFile(join(SOURCE, 'package.json'), 'utf8'));
if (pkg.version !== EXPECTED) {
  throw new Error(`mermaid is ${pkg.version}; this script vendors ${EXPECTED}. Update both together.`);
}

const bundle = join(SOURCE, 'dist', 'mermaid.min.js');
await mkdir(TARGET, { recursive: true });
await copyFile(bundle, join(TARGET, 'mermaid.min.js'));
await copyFile(join(SOURCE, 'LICENSE'), join(TARGET, 'LICENSE'));
await writeFile(
  join(TARGET, 'VERSION'),
  `${pkg.version}\n\nCopied from node_modules/mermaid/dist/mermaid.min.js by\nscripts/vendor-mermaid.mjs. Mermaid is MIT licensed; see LICENSE.\n`,
  'utf8',
);

console.log(`vendored mermaid ${pkg.version} into media/vendor/mermaid`);
