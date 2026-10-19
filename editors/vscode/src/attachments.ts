/**
 * Pasted and picked images, turned into protocol attachments.
 *
 * A paste arrives from the webview as a `data:` URL, because that is what a
 * `FileReader` produces and the webview has the `File` object. The host wants
 * base64 plus a MIME type, so the conversion and its refusals live here — one
 * place, no `vscode` import, testable directly.
 */

import { Attachment } from './protocol';

/** The image types the vision path accepts. */
const IMAGE_MIME = /^image\/(png|jpeg|jpg|gif|webp)$/i;

/**
 * The most base64 the composer accepts for one image.
 *
 * This is a bound on what one paste may add to a prompt, not a copy of the
 * SDK's budget: the vision pipeline downscales to a 2048-longest side and only
 * then applies its own 4 MiB encoded cap, so a large screenshot is expected to
 * be shrunk on the way to the model rather than refused. What this stops is the
 * case nothing downstream stops — a paste big enough that the prompt, not the
 * image, becomes the problem.
 */
export const MAX_ATTACHMENT_BASE64 = 8 << 20;

export interface AttachmentResult {
  attachment?: Attachment;
  /** Why it was refused, for the UI. Absent when it was accepted. */
  error?: string;
}

/**
 * Converts one `data:` URL into an attachment.
 *
 * Only images are accepted: the protocol has one attachment channel and it is
 * the vision path, so a pasted PDF or text snippet would be dropped by the SDK
 * or, worse, sent as bytes nothing can render.
 */
export function attachmentFromDataUrl(dataUrl: string): AttachmentResult {
  const match = /^data:([^;,]*)(;base64)?,(.*)$/s.exec(dataUrl);
  if (!match) {
    return { error: 'that paste was not a data URL' };
  }
  const [, mime, base64, payload] = match;
  if (!base64) {
    return { error: 'that paste was not base64-encoded' };
  }
  if (!IMAGE_MIME.test(mime)) {
    return { error: `only images can be attached, and that is ${mime || 'an unknown type'}` };
  }
  if (payload === '') {
    return { error: 'that image was empty' };
  }
  if (payload.length > MAX_ATTACHMENT_BASE64) {
    return { error: 'that image is too large to attach' };
  }
  return { attachment: { data: payload, mime } };
}

/** The image extensions the picker offers, mapped to the MIME type to declare. */
const IMAGE_EXTENSIONS: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
};

/**
 * The MIME type for an image path, or undefined when the extension is not one.
 *
 * The type is derived from the name rather than sniffed from the bytes: the
 * picker's filter already limits what can be chosen, and the webview only ever
 * sees the data URL, so the name is the one statement about the type there is.
 * A wrong name is caught downstream by the vision path, which refuses what it
 * cannot decode.
 */
export function mimeForImagePath(path: string): string | undefined {
  const match = /\.([A-Za-z0-9]+)$/.exec(path);
  return match ? IMAGE_EXTENSIONS[match[1].toLowerCase()] : undefined;
}

/**
 * Encodes bytes as the data URL the composer already holds for a paste.
 *
 * The size is checked on the encoded length, because that is what the paste
 * path caps: a picked file and a pasted image must not be refused by two
 * different tests, or a file would be attachable one way and not the other.
 */
export function dataUrlFromBytes(
  bytes: Uint8Array,
  mime: string | undefined,
): { dataUrl?: string; error?: string } {
  if (!mime || !IMAGE_MIME.test(mime)) {
    return { error: `only images can be attached, and that is ${mime || 'an unknown type'}` };
  }
  if (bytes.byteLength === 0) return { error: 'that image was empty' };
  if (Math.ceil(bytes.byteLength / 3) * 4 > MAX_ATTACHMENT_BASE64) {
    return { error: 'that image is too large to attach' };
  }
  return { dataUrl: `data:${mime};base64,${Buffer.from(bytes).toString('base64')}` };
}

/** Converts a batch, reporting the first refusal by index. */
export function attachmentsFromDataUrls(dataUrls: string[]): { attachments: Attachment[]; errors: string[] } {
  const attachments: Attachment[] = [];
  const errors: string[] = [];
  for (const dataUrl of dataUrls) {
    const result = attachmentFromDataUrl(dataUrl);
    if (result.attachment) attachments.push(result.attachment);
    else if (result.error) errors.push(result.error);
  }
  return { attachments, errors };
}
