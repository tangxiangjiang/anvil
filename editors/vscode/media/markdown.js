/*
 * A small Markdown parser: text in, a tree of plain objects out.
 *
 * It never touches the DOM. That is the point of the split — the part with the
 * bugs is the part that can be tested, and `node --test` has no browser — so
 * this file is pure functions and `chat.js` is the only thing that builds
 * nodes. Nothing here decides what is safe to display either; it reports a
 * link's target and lets the renderer refuse a scheme it will not make
 * clickable.
 *
 * A plain script assigning a global rather than a module: `media/` is linted as
 * browser code with no Node globals, and `tsc` never copies `media/` into
 * `out/`, so a module would fail the linter and still not be importable from a
 * test. The test loads this file by absolute path for its side effect, the same
 * way `chatLayout.test.ts` reads the stylesheet.
 *
 * Deliberately out of scope: images, tables, footnotes, HTML passthrough (a tag
 * renders as the literal text it is), setext headings, reference links, and
 * nested inline containers beyond one level.
 */
(function (global) {
  'use strict';

  /**
   * Splits a line's inline Markdown into spans.
   *
   * A span is `{ type }` plus, for the leaf types, `value`:
   *   `{ type: 'text', value }` `{ type: 'code', value }`
   * and for the containers, `spans` (and `href` for a link):
   *   `{ type: 'em' | 'strong' | 'del' | 'link', spans, href? }`
   *
   * Everything unrecognised stays text, so no input is ever lost — an unclosed
   * `**` renders as the two characters it is.
   */
  function inline(text) {
    const spans = [];
    let literal = '';
    let i = 0;

    /** Flushes the pending literal text, so spans stay in source order. */
    function flush() {
      if (literal !== '') {
        spans.push({ type: 'text', value: literal });
        literal = '';
      }
    }

    while (i < text.length) {
      const rest = text.slice(i);

      // Code spans bind tightest: a `*` inside backticks is a character, not
      // emphasis, so this arm runs before the emphasis ones.
      if (text[i] === '`') {
        const close = text.indexOf('`', i + 1);
        if (close > i + 1) {
          flush();
          spans.push({ type: 'code', value: text.slice(i + 1, close).trim() });
          i = close + 1;
          continue;
        }
      }

      // A link whose label or target is empty is not a link; both stay literal.
      if (text[i] === '[') {
        const link = matchLink(rest);
        if (link) {
          flush();
          spans.push({ type: 'link', href: link.href, spans: inline(link.label) });
          i += link.length;
          continue;
        }
      }

      const wrapped = matchWrapped(rest, i, text);
      if (wrapped) {
        flush();
        spans.push({ type: wrapped.type, spans: inline(wrapped.inner) });
        i += wrapped.length;
        continue;
      }

      literal += text[i];
      i++;
    }

    flush();
    return spans;
  }

  /**
   * `[label](target)` at the start of `text`, or null.
   *
   * The label may not contain `[`, which anchors the match on the first opening
   * bracket instead of scanning for a balanced pair; every case that matters
   * here is one level deep.
   */
  function matchLink(text) {
    const close = text.indexOf('](');
    if (close < 1) return null;
    const end = text.indexOf(')', close + 2);
    if (end < 0) return null;
    const label = text.slice(1, close);
    let href = text.slice(close + 2, end).trim();
    if (label.includes('[') || href === '') return null;
    // A space in an unbracketed destination means this was prose, not a link.
    if (/\s/.test(href)) {
      if (!/^<.*>$/.test(href)) return null;
      href = href.slice(1, -1);
    }
    // Emptied by the unwrapping above, or empty to begin with.
    if (href === '') return null;
    return { label, href, length: end + 1 };
  }

  /**
   * `**bold**`, `__bold__`, `*italic*`, `_italic_`, `~~struck~~` at the start of
   * `text`, or null. `text` is `whole.slice(at)`, and the surrounding characters
   * are read from `whole` — which is what keeps `snake_case_name` from
   * italicising `case`.
   */
  function matchWrapped(text, at, whole) {
    const before = at > 0 ? whole[at - 1] : '';
    const forms = [
      { marker: '**', type: 'strong' },
      { marker: '__', type: 'strong' },
      { marker: '~~', type: 'del' },
      { marker: '*', type: 'em' },
      { marker: '_', type: 'em' },
    ];
    for (const form of forms) {
      if (!text.startsWith(form.marker)) continue;
      // Underscores only mark emphasis at a word boundary, the same rule that
      // leaves `foo_bar_baz` alone. Asterisks are free to sit inside a word.
      if (form.marker[0] === '_' && isWord(before)) continue;

      const end = text.indexOf(form.marker, form.marker.length);
      // An empty span is not emphasis, and reading `**` as `*` would swallow
      // the markers of a genuine `**bold**`.
      if (end <= form.marker.length) continue;
      const inner = text.slice(form.marker.length, end);
      if (inner.trim() === '') continue;
      if (form.marker[0] === '_' && isWord(whole[at + end + form.marker.length])) continue;
      return { type: form.type, inner, length: end + form.marker.length };
    }
    return null;
  }

  function isWord(character) {
    return /[0-9A-Za-z]/.test(character || '');
  }

  const FENCE = /^(\s{0,3})(`{3,}|~{3,})\s*([^`]*)$/;
  const HEADING = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
  // A marker with nothing after it is still an item — the line `-` on its own
  // is an empty item, not a paragraph. That matters beyond fidelity: if the
  // parser read it as a paragraph, the *next* character would make the list two
  // blocks back swallow it, and a block that is not the last one would then
  // change under the streaming renderer's frozen prefix.
  const BULLET = /^(\s*)([-*+])(?:\s+(.*))?$/;
  const ORDERED = /^(\s*)(\d{1,9}[.)])(?:\s+(.*))?$/;
  const RULE = /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/;
  const QUOTE = /^ {0,3}>\s?(.*)$/;

  /** The list marker a line starts with, and whether it is an ordered one. */
  function markerOf(text) {
    const ordered = ORDERED.exec(text);
    if (ordered) return { match: ordered, ordered: true };
    const bullet = BULLET.exec(text);
    if (bullet) return { match: bullet, ordered: false };
    return null;
  }

  /**
   * Parses a document into blocks, each recording the offset it started at.
   *
   * The offset is what makes streaming possible: a caller may re-parse from any
   * block's start and get that block, and everything after it, back unchanged.
   */
  function parse(text) {
    const lines = splitLines(text);
    const blocks = [];
    let i = 0;

    while (i < lines.length) {
      const line = lines[i];

      if (line.text.trim() === '') {
        i++;
        continue;
      }

      const start = line.at;
      const fence = FENCE.exec(line.text);

      if (fence) {
        const closer = new RegExp('^\\s{0,3}' + fence[2][0].repeat(3) + '[,`~\\s]*$');
        let j = i + 1;
        while (j < lines.length && !closer.test(lines[j].text)) j++;
        // The body runs to the line before the closing fence, or to the end of
        // the document while the fence is still open. An open fence is the
        // ordinary state of a code block that is still streaming, not an error.
        const last = (j >= lines.length ? lines.length : j) - 1;
        const body = last > i ? text.slice(lines[i + 1].at, lines[last].at + lines[last].text.length) : '';
        blocks.push({ type: 'code', lang: fence[3].trim(), text: body, start });
        i = j >= lines.length ? lines.length : j + 1;
        continue;
      }

      const heading = HEADING.exec(line.text);
      if (heading) {
        blocks.push({ type: 'heading', level: heading[1].length, spans: inline(heading[2]), start });
        i++;
        continue;
      }

      if (RULE.test(line.text)) {
        blocks.push({ type: 'hr', start });
        i++;
        continue;
      }

      if (QUOTE.test(line.text)) {
        const inner = [];
        while (i < lines.length && QUOTE.test(lines[i].text)) {
          inner.push(lines[i].text.replace(QUOTE, '$1'));
          i++;
        }
        blocks.push({ type: 'blockquote', blocks: parse(inner.join('\n')).blocks, start });
        continue;
      }

      const list = matchList(lines, i);
      if (list) {
        // `start` is the block's offset and nothing else — every block has one
        // and the streaming contract reads it. The number an ordered list begins
        // at is a different thing, so it is a different field.
        blocks.push({ type: 'list', ordered: list.ordered, from: list.from, items: list.items, start });
        i = list.next;
        continue;
      }

      if (/^ {4}/.test(line.text)) {
        // An indented code block continues across blank lines and only ends
        // where the indentation does. A blank line inside it is content — which
        // is one of the reasons a blank line cannot be a parse boundary.
        const body = [];
        let j = i;
        let lastCode = i;
        while (j < lines.length) {
          const current = lines[j].text;
          if (/^ {4}/.test(current)) {
            body.push(current.slice(4));
            lastCode = j;
            j++;
            continue;
          }
          if (current.trim() === '') {
            body.push('');
            j++;
            continue;
          }
          break;
        }
        body.length = lastCode - i + 1;
        blocks.push({ type: 'code', lang: '', text: body.join('\n'), start });
        i = lastCode + 1;
        continue;
      }

      // A paragraph runs to the next blank line or block start, and stays one
      // paragraph however many lines it spans: the newlines inside it are soft
      // wraps, not breaks.
      const body = [];
      let j = i;
      while (j < lines.length && !isBoundary(lines[j].text)) {
        body.push(lines[j].text.trim());
        j++;
      }
      blocks.push({ type: 'paragraph', spans: inline(body.join(' ')), start });
      i = j;
    }

    return { blocks };
  }

  /** Splits into lines that remember where they started. */
  function splitLines(text) {
    const lines = [];
    let at = 0;
    for (const raw of text.split('\n')) {
      lines.push({ text: raw, at });
      at += raw.length + 1;
    }
    return lines;
  }

  /** Whether a line starts something other than a paragraph continuation. */
  function isBoundary(text) {
    if (text.trim() === '') return true;
    return (
      FENCE.test(text) ||
      HEADING.test(text) ||
      RULE.test(text) ||
      QUOTE.test(text) ||
      BULLET.test(text) ||
      ORDERED.test(text) ||
      /^ {4}/.test(text)
    );
  }

  /**
   * A list starting at `lines[index]`, with its items and their nesting, or
   * null if no list starts there.
   *
   * Nesting is by indentation: an item's continuation lines are read at their
   * own indent and re-parsed, so a nested list inside an item is a list inside
   * an item rather than a sibling of it.
   */
  function matchList(lines, index) {
    const opening = markerOf(lines[index].text);
    if (!opening) return null;
    const ordered = opening.ordered;
    const base = opening.match[1].length;
    // The column the item's text starts at, which is where a continuation line
    // has to reach to belong to this item.
    const content = opening.match[1].length + opening.match[2].length + 1;
    const from = ordered ? parseInt(opening.match[2], 10) : 1;
    const items = [];

    let i = index;
    while (i < lines.length) {
      // A blank line between two items makes the list loose; it does not end it
      // and does not start a second list.
      if (lines[i].text.trim() === '' && items.length > 0 && i + 1 < lines.length) {
        const ahead = markerOf(lines[i + 1].text);
        if (!ahead || ahead.match[1].length !== base || ahead.ordered !== ordered) break;
        i++;
      }

      const marker = markerOf(lines[i].text);
      if (!marker || marker.ordered !== ordered || marker.match[1].length !== base) break;
      const match = marker.match;

      const parts = [match[3] || ''];
      i++;
      while (i < lines.length) {
        const line = lines[i].text;
        if (line.trim() === '') {
          // A blank line ends the item unless the next line is indented into it,
          // which is how an item gets a second paragraph.
          const next = lines[i + 1];
          if (!next || (next.text.length - next.text.trimStart().length) < content) break;
          parts.push('');
          i++;
          continue;
        }
        const indent = line.length - line.trimStart().length;
        if (indent < content) break;
        parts.push(line.slice(Math.min(indent, content)));
        i++;
      }

      const inner = parse(parts.join('\n')).blocks;
      items.push({ spans: soleSpans(inner), blocks: inner });
    }

    return items.length > 0 ? { ordered, from, items, next: i } : null;
  }

  /**
   * An item whose whole content is one paragraph is rendered as its inline
   * spans, so the common `- a` does not pay for a nested paragraph. Anything
   * else keeps its blocks and is rendered as a block container.
   */
  function soleSpans(inner) {
    if (inner.length === 1 && inner[0].type === 'paragraph') return inner[0].spans;
    return null;
  }

  global.anvilMarkdown = { parse, inline };
})(typeof globalThis === 'undefined' ? this : globalThis);
