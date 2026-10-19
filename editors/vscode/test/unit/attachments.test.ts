/**
 * Pasted images, converted and refused without a window.
 *
 * The refusals matter more than the acceptance: an attachment that reaches the
 * host carrying the wrong MIME type or a truncated payload is a model request
 * made from something the user did not mean to send.
 */

import { strict as assert } from 'node:assert';
import { test } from 'node:test';
import {
  MAX_ATTACHMENT_BASE64,
  attachmentFromDataUrl,
  attachmentsFromDataUrls,
  dataUrlFromBytes,
  mimeForImagePath,
} from '../../src/attachments';

/** A real 1x1 PNG, so the payload is a thing an image reader would accept. */
const PNG =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

test('an image data URL becomes an attachment', () => {
  const result = attachmentFromDataUrl(`data:image/png;base64,${PNG}`);
  assert.equal(result.error, undefined);
  assert.deepEqual(result.attachment, { data: PNG, mime: 'image/png' });
});

test('the JPEG and WEBP MIME types are accepted', () => {
  for (const mime of ['image/jpeg', 'image/jpg', 'image/gif', 'image/webp']) {
    const result = attachmentFromDataUrl(`data:${mime};base64,${PNG}`);
    assert.equal(result.error, undefined, mime);
    assert.equal(result.attachment?.mime, mime);
  }
});

test('a non-image is refused by name', () => {
  const result = attachmentFromDataUrl(`data:application/pdf;base64,${PNG}`);
  assert.equal(result.attachment, undefined);
  assert.ok(result.error?.includes('application/pdf'), result.error);
});

test('a URL that is not a data URL is refused', () => {
  assert.ok(attachmentFromDataUrl('https://example.com/a.png')?.error);
  assert.ok(attachmentFromDataUrl('')?.error);
});

test('a non-base64 data URL is refused', () => {
  // A URL-encoded paste is data URL shaped but has no `;base64`, and decoding
  // it as base64 would silently produce the wrong bytes.
  assert.ok(attachmentFromDataUrl('data:image/png,abc')?.error);
});

test('an empty payload is refused', () => {
  assert.ok(attachmentFromDataUrl('data:image/png;base64,')?.error);
});

test('an oversized image is refused before it reaches the wire', () => {
  const huge = `${'A'.repeat(MAX_ATTACHMENT_BASE64 + 4)}`;
  const result = attachmentFromDataUrl(`data:image/png;base64,${huge}`);
  assert.equal(result.attachment, undefined);
  assert.ok(result.error?.includes('too large'), result.error);
});

test('a batch keeps the good ones and reports the rest', () => {
  const { attachments, errors } = attachmentsFromDataUrls([
    `data:image/png;base64,${PNG}`,
    'data:text/plain;base64,Zm9v',
    `data:image/webp;base64,${PNG}`,
  ]);
  assert.equal(attachments.length, 2);
  assert.equal(attachments[0].mime, 'image/png');
  assert.equal(attachments[1].mime, 'image/webp');
  assert.equal(errors.length, 1);
});

test('an empty batch is empty, not undefined', () => {
  const { attachments, errors } = attachmentsFromDataUrls([]);
  assert.deepEqual(attachments, []);
  assert.deepEqual(errors, []);
});

// --- images read from disk (the picker) -------------------------------------

test('an image extension maps to the MIME type to declare', () => {
  assert.equal(mimeForImagePath('C:\\work\\shot.PNG'), 'image/png');
  assert.equal(mimeForImagePath('/tmp/a.jpeg'), 'image/jpeg');
  assert.equal(mimeForImagePath('/tmp/a.jpg'), 'image/jpeg');
  assert.equal(mimeForImagePath('/tmp/a.gif'), 'image/gif');
  assert.equal(mimeForImagePath('/tmp/a.webp'), 'image/webp');
  assert.equal(mimeForImagePath('/tmp/a.svg'), undefined);
  assert.equal(mimeForImagePath('/tmp/noextension'), undefined);
});

test('bytes read from disk become the same data URL a paste produces', () => {
  const bytes = Buffer.from(PNG, 'base64');
  const result = dataUrlFromBytes(bytes, mimeForImagePath('shot.png'));
  assert.equal(result.error, undefined);
  assert.equal(result.dataUrl, `data:image/png;base64,${PNG}`);
  // Round trip: the paste path accepts exactly what the picker produced.
  assert.deepEqual(attachmentFromDataUrl(result.dataUrl ?? '').attachment, {
    data: PNG,
    mime: 'image/png',
  });
});

test('a non-image type is refused by name', () => {
  const result = dataUrlFromBytes(Buffer.from('hello'), 'text/plain');
  assert.equal(result.dataUrl, undefined);
  assert.ok(result.error?.includes('text/plain'), result.error);
});

test('an empty file and an oversized one are refused', () => {
  assert.ok(dataUrlFromBytes(new Uint8Array(0), 'image/png').error);
  // One byte past the encoded cap: the exact cap is accepted, so the refusal is
  // about the boundary rather than about rounding.
  const tooBig = new Uint8Array(Math.ceil(MAX_ATTACHMENT_BASE64 / 4) * 3 + 1);
  assert.ok(dataUrlFromBytes(tooBig, 'image/png').error?.includes('too large'));
});
