/*
 * Vendors KaTeX's browser bundle into `media/vendor/katex`.
 *
 * The extension ships the file rather than installing the package at runtime,
 * because a webview can fetch nothing: the page's CSP has no network source,
 * and a formula has to render with the network unplugged. It is a build-time
 * copy, run by hand when the version changes, and the result is committed —
 * `package:vsix` therefore needs no `node_modules` for it.
 *
 * Only the script is copied. The renderer is asked for MathML, which the
 * browser draws itself, so KaTeX's stylesheet and its twenty font files (a
 * further ~300 KB) are not shipped. If the output is ever switched to HTML,
 * `katex.min.css` and `dist/fonts/*.woff2` have to come with it, and the
 * stylesheet would need the page's CSP to allow inline style attributes that
 * KaTeX writes on its spans.
 *
 * The bundle is checked for `innerHTML` on the way in: the transcript's rule is
 * that host text never becomes markup, and a renderer that assigned HTML would
 * be a hole in it. An upgrade that introduced one fails here rather than in a
 * review.
 */
import { copyFile, mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');
const SOURCE = join(ROOT, 'node_modules', 'katex');
const TARGET = join(ROOT, 'media', 'vendor', 'katex');
const EXPECTED = '0.19.0';

const pkg = JSON.parse(await readFile(join(SOURCE, 'package.json'), 'utf8'));
if (pkg.version !== EXPECTED) {
  throw new Error(`katex is ${pkg.version}; this script vendors ${EXPECTED}. Update both together.`);
}

const bundle = join(SOURCE, 'dist', 'katex.min.js');
const script = await readFile(bundle, 'utf8');
if (/\binnerHTML\b/.test(script)) {
  throw new Error('the bundle writes innerHTML; the transcript may not trust it with host text');
}

await mkdir(TARGET, { recursive: true });
await copyFile(bundle, join(TARGET, 'katex.min.js'));
await copyFile(join(SOURCE, 'LICENSE'), join(TARGET, 'LICENSE'));
await writeFile(
  join(TARGET, 'VERSION'),
  `${pkg.version}\n\nCopied from node_modules/katex/dist/katex.min.js by\nscripts/vendor-katex.mjs. KaTeX is MIT licensed; see LICENSE. Only the script\nis shipped: formulas are rendered as MathML, so no stylesheet or font file is\npart of this directory.\n`,
  'utf8',
);
console.log(`vendored katex ${pkg.version} into media/vendor/katex`);
