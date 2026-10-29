/**
 * The Markdown parser, and the one property the streaming renderer rests on.
 *
 * The parser is a plain browser script assigning `globalThis.anvilMarkdown`, not
 * a module: `media/` is linted as browser code and `tsc` never copies it into
 * `out/`, so there is nothing to `import`. It is loaded here by **absolute
 * path** for its side effect, the same way `chatLayout.test.ts` reads the
 * stylesheet as text.
 *
 * Most of these tests are about the two things a hand-written parser gets wrong
 * when it is fed a document that is still growing: a blank line is not a parse
 * boundary, and re-parsing from the last block's start has to reproduce the
 * last block exactly — otherwise the renderer's frozen prefix and its fresh
 * tail disagree, and the transcript shows the same sentence twice or not at all.
 */

import { strict as assert } from 'node:assert';
import { join } from 'node:path';
import { test } from 'node:test';

const MARKDOWN_PATH = join(__dirname, '..', '..', '..', 'media', 'markdown.js');
// A script loaded for its side effect, not a module imported for its exports.
// eslint-disable-next-line @typescript-eslint/no-require-imports
require(MARKDOWN_PATH);

interface Span {
  type: string;
  value?: string;
  href?: string;
  spans?: Span[];
}

interface Block {
  type: string;
  level?: number;
  lang?: string;
  text?: string;
  closed?: boolean;
  ordered?: boolean;
  from?: number;
  /** The offset the block started at — the streaming contract, not the ordinal. */
  start: number;
  spans?: Span[];
  blocks?: Block[];
  items?: { spans: Span[] | null; blocks: Block[] }[];
}

const { parse, inline } = (
  globalThis as unknown as {
    anvilMarkdown: { parse: (text: string) => { blocks: Block[] }; inline: (text: string) => Span[] };
  }
).anvilMarkdown;

/** The plain text a span list carries, with markers dropped. */
function textOf(spans: Span[] | undefined): string {
  return (spans || [])
    .map((span) => (span.value !== undefined ? span.value : textOf(span.spans)))
    .join('');
}

/** The types of a top-level parse, which is usually all a test cares about. */
function types(text: string): string[] {
  return parse(text).blocks.map((block) => block.type);
}

test('every block type parses', () => {
  assert.deepEqual(types('plain words'), ['paragraph']);
  assert.deepEqual(types('# Title'), ['heading']);
  assert.deepEqual(types('```js\nx\n```'), ['code']);
  assert.deepEqual(types('    indented'), ['code']);
  assert.deepEqual(types('- one\n- two'), ['list']);
  assert.deepEqual(types('1. one\n2. two'), ['list']);
  assert.deepEqual(types('> quoted'), ['blockquote']);
  assert.deepEqual(types('---'), ['hr']);
  assert.deepEqual(types('a\n\nb'), ['paragraph', 'paragraph']);
  assert.deepEqual(types(''), []);
});

test('headings carry their level, and a seventh hash is not a heading', () => {
  const [h3] = parse('### Three').blocks;
  assert.equal(h3.level, 3);
  assert.equal(textOf(h3.spans), 'Three');
  // `####### x` exceeds the six levels, so it stays a paragraph.
  assert.deepEqual(types('####### x'), ['paragraph']);
});

test('a closing fence is not part of the code, and an open fence is not an error', () => {
  const closed = parse('```js\nconst a = 1;\n```').blocks[0];
  assert.equal(closed.type, 'code');
  assert.equal(closed.lang, 'js');
  assert.equal(closed.text, 'const a = 1;');
  assert.equal(closed.closed, true);

  // The open case is what a streaming code block looks like on every frame
  // until it ends, so it must parse as code rather than as prose.
  const open = parse('```js\nconst a = 1;').blocks[0];
  assert.equal(open.type, 'code');
  assert.equal(open.text, 'const a = 1;');
  assert.equal(open.closed, false);
});

test('a fence with no language still parses, and a tilde fence closes on tildes', () => {
  assert.equal(parse('```\nx\n```').blocks[0].lang, '');
  const tilde = parse('~~~\nx\n~~~').blocks[0];
  assert.equal(tilde.type, 'code');
  assert.equal(tilde.text, 'x');
  // A backtick fence is not closed by a tilde one.
  assert.equal(parse('```\nx\n~~~').blocks[0].text, 'x\n~~~');
});

test('a blank line does not end an indented code block', () => {
  // This is the case that makes "seal a block on a blank line" unsound: the
  // blank line is inside the block, so sealing there would split it in two.
  const blocks = parse('    a\n\n    b').blocks;
  assert.equal(blocks.length, 1);
  assert.equal(blocks[0].type, 'code');
  assert.equal(blocks[0].text, 'a\n\nb');
});

test('a loose list is one list, and an item keeps its second paragraph', () => {
  const loose = parse('- a\n\n- b').blocks;
  assert.equal(loose.length, 1, 'a blank line between items must not start a second list');
  assert.equal(loose[0].items?.length, 2);

  // The blank line here is *inside* the item, which is the other half of why a
  // blank line cannot be treated as a block boundary.
  const items = parse('- a\n\n  second para\n\n- b').blocks[0].items;
  assert.equal(items?.length, 2);
  assert.equal(items?.[0].blocks.length, 2);
  assert.equal(items?.[0].blocks[1].type, 'paragraph');
});

test('a list does not change marker style partway through', () => {
  const blocks = parse('- a\n1. b').blocks;
  assert.equal(blocks.length, 2, '- and 1. are two lists, not one');
  assert.equal(blocks[0].ordered, false);
  assert.equal(blocks[1].ordered, true);
  assert.equal(blocks[0].from, 1);
});

test('an ordered list records where it starts, without overloading the offset', () => {
  const list = parse('3. three').blocks[0];
  assert.equal(list.ordered, true);
  assert.equal(list.from, 3);
  // `start` is the block's offset and is what the streaming contract reads, so
  // the ordinal must not be written there: it would break the renderer's
  // re-parse for every list that does not begin at 1.
  assert.equal(list.start, 0);
});

test('a nested list is nested, not a sibling', () => {
  const list = parse('- a\n  - b').blocks[0];
  assert.equal(list.items?.length, 1, '`  - b` belongs to the first item');
  const nested = list.items?.[0].blocks || [];
  assert.equal(nested.some((block) => block.type === 'list'), true);
});

test('a blockquote keeps its inner blocks', () => {
  const quote = parse('> # Title\n> body').blocks[0];
  assert.equal(quote.type, 'blockquote');
  assert.deepEqual(
    quote.blocks?.map((block) => block.type),
    ['heading', 'paragraph'],
  );
});

test('a multi-line paragraph joins on spaces', () => {
  const [p] = parse('one\ntwo').blocks;
  assert.equal(textOf(p.spans), 'one two');
});

test('inline emphasis, code, strikethrough and links', () => {
  assert.deepEqual(inline('**b**')[0], { type: 'strong', spans: [{ type: 'text', value: 'b' }] });
  assert.deepEqual(inline('*i*')[0], { type: 'em', spans: [{ type: 'text', value: 'i' }] });
  assert.deepEqual(inline('~~s~~')[0], { type: 'del', spans: [{ type: 'text', value: 's' }] });
  assert.deepEqual(inline('`c`')[0], { type: 'code', value: 'c' });
  assert.deepEqual(inline('[t](http://x)')[0], {
    type: 'link',
    href: 'http://x',
    spans: [{ type: 'text', value: 't' }],
  });
});

test('an unclosed or empty marker stays literal', () => {
  // No input is ever lost: what is not recognised is text.
  assert.equal(textOf(inline('un**closed')), 'un**closed');
  assert.equal(textOf(inline('****')), '****');
  assert.equal(textOf(inline('``')), '``');
  assert.equal(textOf(inline('[]()')), '[]()');
});

test('backticks win over emphasis inside them', () => {
  const spans = inline('`a *b* c`');
  assert.deepEqual(spans, [{ type: 'code', value: 'a *b* c' }]);
});

test('underscores inside a word are not emphasis', () => {
  // `foo_bar_baz` is an identifier, not an italic `bar`.
  assert.deepEqual(inline('foo_bar_baz'), [{ type: 'text', value: 'foo_bar_baz' }]);
  assert.deepEqual(inline('_i_')[0], { type: 'em', spans: [{ type: 'text', value: 'i' }] });
});

test('a link with a space in an unbracketed target is prose', () => {
  assert.equal(textOf(inline('[a](b c)')), '[a](b c)');
  // Angle brackets are how a target with a space is actually written.
  assert.equal(inline('[a](<b c>)')[0].href, 'b c');
});

test('every block records the offset it started at', () => {
  const blocks = parse('one\n\n# two\n\n```\nx').blocks;
  for (const block of blocks) {
    assert.equal(typeof block.start, 'number');
  }
  // Slicing at the recorded offset reproduces the block's own source, which is
  // what the streaming renderer relies on when it re-parses from there.
  assert.equal('one\n\n# two\n\n```\nx'.slice(blocks[1].start).startsWith('# two'), true);
  assert.equal('one\n\n# two\n\n```\nx'.slice(blocks[2].start).startsWith('```'), true);
});

test('re-parsing from the last block start reproduces the last block', () => {
  // The renderer freezes every block except the last and re-parses the rest of
  // the document. If that did not reproduce the last block, the frozen prefix
  // and the fresh tail would disagree about it.
  const documents = [
    'one\n\ntwo',
    'one\n\n# head',
    'one\n\n```js\nconst a = 1;\n```',
    'one\n\n```js\nconst a = 1;',
    'one\n\n- a\n- b',
    'one\n\n- a\n\n- b',
    'one\n\n> quoted',
    'one\n\n    indented',
    'one\n\n    indented\n\n    more',
    'one\n\n1. a\n2. b',
    'one\n\ntail *with* `inline`',
  ];
  for (const document of documents) {
    const blocks = parse(document).blocks;
    const last = blocks[blocks.length - 1];
    const reparsed = parse(document.slice(last.start)).blocks;
    assert.ok(reparsed.length > 0, `${JSON.stringify(document)}: tail parsed to nothing`);
    assert.deepEqual(
      { ...reparsed[0], start: 0 },
      { ...last, start: 0 },
      `${JSON.stringify(document)}: re-parsing from the last block's start changed it`,
    );
  }
});

test('the commit rule holds as a document grows character by character', () => {
  // The whole streaming contract, exercised the way the renderer does it: parse,
  // freeze everything before the last block, re-parse the remainder next frame,
  // and check that the freeze was still true. Anything the parser reads as two
  // blocks that a later character merges into one shows up here as a frozen
  // block that changed — which is a duplicate or a lost line on screen.
  const documents = [
    '# Title\n\nA paragraph with *emphasis* and `code`.\n\n```js\nconst a = 1;\n```\n\n- one\n- two\n\n> done\n',
    'one\n\ntwo\n\nthree',
    '- a\n- b\n-',
    '- a\n\n- b\n\n  more\n\n1. c',
    '```\ncode\nmore code\n```\n\nafter',
    '> quote\n> more\n\ntail',
    '    indented\n\n    more\n\nafter',
    'text with a [link](http://x) and a trailing ',
    '# h\n\n- a\n  1. nested\n- b\n\n---\n\nend',
  ];
  for (const document of documents) {
    let frozen: Block[] = [];
    for (let end = 1; end <= document.length; end++) {
      const text = document.slice(0, end);
      const blocks = parse(text).blocks;
      const fresh = parse(text).blocks;
      assert.deepEqual(
        fresh.slice(0, frozen.length).map((block) => ({ ...block, start: 0 })),
        frozen.map((block) => ({ ...block, start: 0 })),
        `${JSON.stringify(document)}: a frozen block changed at ${JSON.stringify(text)}`,
      );
      frozen = blocks.slice(0, -1);
    }
  }
});

test('a formula is one span, and display math is one block', () => {
  assert.deepEqual(inline('a $x^2$ b'), [
    { type: 'text', value: 'a ' },
    { type: 'math', value: 'x^2', display: false },
    { type: 'text', value: ' b' },
  ]);
  // `$$` mid-sentence is the same formula; the renderer decides how to draw it.
  assert.deepEqual(inline('so $$E=mc^2$$ here'), [
    { type: 'text', value: 'so ' },
    { type: 'math', value: 'E=mc^2', display: true },
    { type: 'text', value: ' here' },
  ]);

  const [block] = parse('$$\n\\frac{a}{b}\n$$').blocks;
  assert.equal(block.type, 'math');
  assert.equal(block.text, '\\frac{a}{b}');

  const [oneLine] = parse('$$x^2$$').blocks;
  assert.equal(oneLine.type, 'math');
  assert.equal(oneLine.text, 'x^2');
});

test('money keeps its dollars', () => {
  // The closing rule — no space before it, no digit after it — is what tells
  // prose about prices apart from mathematics, which is the only reason a
  // parser has to guess at all.
  for (const text of ['$5 and $6', 'price is $5.00', 'costs $5 to $6', '$ x $', 'a $ b', '$x$5']) {
    assert.deepEqual(inline(text), [{ type: 'text', value: text }], text);
  }
});

test('an unclosed formula stays literal', () => {
  assert.deepEqual(parse('an open $x^2').blocks[0].spans, [{ type: 'text', value: 'an open $x^2' }]);
  // A display opener with no closer is a paragraph, not a code block or an
  // error: this is the ordinary state of a formula that is still streaming.
  assert.deepEqual(types('$$\nx^2'), ['paragraph']);
  assert.deepEqual(types('$$\nx^2\n'), ['paragraph']);
  // A closer that is not alone on its line is not a closer: taking it would
  // drop the text after it. The line still *opens* a formula, so it starts a
  // paragraph of its own — a paragraph may not run across a line that could
  // become a block once the rest of it arrives.
  assert.deepEqual(types('$$\nx^2\n$$ after'), ['paragraph', 'paragraph']);
  // `$$$$` is four characters, not an empty formula.
  assert.deepEqual(inline('$$$$'), [{ type: 'text', value: '$$$$' }]);
});

test('a formula inside code or emphasis keeps the container it was in', () => {
  assert.deepEqual(inline('`$x$`'), [{ type: 'code', value: '$x$' }]);
  assert.deepEqual(inline('**$x$**'), [
    { type: 'strong', spans: [{ type: 'math', value: 'x', display: false }] },
  ]);
  const list = parse('- item $z$').blocks[0];
  assert.deepEqual(list.items?.[0].spans?.map((span) => span.type), ['text', 'math']);
});

test('a formula keeps its place as the document grows', () => {
  // The commit rule matters for math more than for prose: an open `$$` is a
  // paragraph and a closed one is a block, and a frozen paragraph must never be
  // rewritten into something else.
  const document = 'before\n\n$$\nx^2 + y^2\n$$\n\nafter $z$ and';
  let frozen: Block[] = [];
  for (let end = 1; end <= document.length; end++) {
    const text = document.slice(0, end);
    const blocks = parse(text).blocks;
    assert.deepEqual(
      blocks.slice(0, frozen.length).map((block) => ({ ...block, start: 0 })),
      frozen.map((block) => ({ ...block, start: 0 })),
      `a frozen block changed at ${JSON.stringify(text)}`,
    );
    frozen = blocks.slice(0, -1);
  }
});
