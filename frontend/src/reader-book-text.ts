import { t } from "./i18n";
export type BookEncoding = "utf-8" | "utf-16le" | "utf-16be" | "gb18030" | "gbk" | "big5";
export const BOOK_ENCODINGS: BookEncoding[] = ["utf-8", "utf-16le", "utf-16be", "gb18030", "gbk", "big5"];
export const TEXT_PAGE = 8000;
const CHUNK = 64 * 1024;
export interface TextChapter { title: string; segment: number; offset: number }
interface Segment { start: number; end: number; length: number }

export function detectBookEncoding(bytes: Uint8Array, fallback: BookEncoding): BookEncoding {
  if (bytes[0] === 0xff && bytes[1] === 0xfe) return "utf-16le";
  if (bytes[0] === 0xfe && bytes[1] === 0xff) return "utf-16be";
  if (bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) return "utf-8";
  return fallback;
}

// Leave incomplete code units in the next byte range. Decoder streaming is still
// used, but every indexed range is independently decodable for bounded caching.
export function safeTextEnd(bytes: Uint8Array, encoding: BookEncoding, final = false): number {
  if (final) return bytes.length;
  let i = 0;
  while (i < bytes.length) {
    const lead = bytes[i];
    let width = 1;
    if (encoding.startsWith("utf-16")) {
      if (i + 2 > bytes.length) return i;
      const unit = encoding === "utf-16le" ? lead | (bytes[i + 1] << 8) : (lead << 8) | bytes[i + 1];
      width = unit >= 0xd800 && unit <= 0xdbff ? 4 : 2;
    } else if (encoding === "utf-8") {
      width = lead >= 0xc2 && lead <= 0xdf ? 2 : lead >= 0xe0 && lead <= 0xef ? 3 : lead >= 0xf0 && lead <= 0xf4 ? 4 : 1;
    } else if (lead >= 0x81 && lead <= 0xfe) {
      if (i + 1 === bytes.length) return i;
      width = encoding === "gb18030" && bytes[i + 1] >= 0x30 && bytes[i + 1] <= 0x39 ? 4 : 2;
    }
    if (i + width > bytes.length) return i;
    i += width;
  }
  return i;
}

export function textPageEnd(text: string, start: number): number {
  let end = Math.min(text.length, start + TEXT_PAGE);
  if (end < text.length && /[\uD800-\uDBFF]/.test(text[end - 1])) end--;
  return end;
}

export function chapterLines(text: string): { title: string; offset: number }[] {
  const result: { title: string; offset: number }[] = [];
  const pattern = /(?:^|\n)[ \t]*((?:第[〇零一二三四五六七八九十百千万两\d]+[章节回卷部篇]|chapter\s+\d+|序章|序言|前言|后记|尾声)[^\r\n]{0,90})/gi;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(text))) result.push({ title: match[1].trim(), offset: match.index + (match[0].startsWith("\n") ? 1 : 0) });
  return result;
}

export function cooperative(signal: AbortSignal): Promise<void> {
  // macOS 12 WebKit lacks Promise.withResolvers; use its native executor API.
  return new Promise((resolve, reject) => {
    if (signal.aborted) { reject(new DOMException("Aborted", "AbortError")); return; }
    const abort = () => { clearTimeout(timer); reject(new DOMException("Aborted", "AbortError")); };
    const timer = setTimeout(() => { signal.removeEventListener("abort", abort); resolve(); }, 0);
    signal.addEventListener("abort", abort, { once: true });
  });
}

export class TextBook {
  readonly chapters: TextChapter[] = [];
  readonly segments: Segment[] = [];
  total: number;
  complete = false;
  private cache = new Map<number, string>();
  private pending: Promise<void> | undefined;
  private chapterTail = "";
  private tailSegment = 0;
  private tailOffset = 0;
  constructor(private url: string, public encoding: BookEncoding, size: number, private signal: AbortSignal) { this.total = size; }

  private async range(start: number, count: number): Promise<{ bytes: Uint8Array; final: boolean }> {
    this.signal.throwIfAborted();
    const response = await fetch(this.url, { headers: { Range: `bytes=${start}-${start + count - 1}` }, signal: this.signal });
    if (response.status === 416 && this.total > 0 && start >= this.total) return { bytes: new Uint8Array(), final: true };
    if (!response.ok || !response.body) throw new Error(t("读取文件失败（HTTP {status}）", { status: response.status }));
    const contentRange = /^bytes (\d+)-(\d+)\/(\d+|\*)$/.exec(response.headers.get("content-range") ?? "");
    if (response.status === 206 && (!contentRange || Number(contentRange[1]) !== start)) {
      await response.body.cancel();
      throw new Error(t("文本字节范围无效"));
    }
    if (contentRange && contentRange[3] !== "*") this.total = Number(contentRange[3]);
    if (response.status === 200 && response.headers.has("content-length")) this.total = Number(response.headers.get("content-length"));
    const reader = response.body.getReader();
    const result = new Uint8Array(count);
    let skipped = response.status === 200 ? start : 0, length = 0, ended = false;
    try {
      while (length < count) {
        const { value, done } = await reader.read();
        if (done) { ended = true; break; }
        const skip = Math.min(skipped, value.length);
        skipped -= skip;
        const take = Math.min(count - length, value.length - skip);
        result.set(value.subarray(skip, skip + take), length);
        length += take;
      }
    } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
    return { bytes: result.subarray(0, length), final: ended || (this.total > 0 && start + length >= this.total) };
  }

  private remember(index: number, text: string) {
    this.cache.delete(index);
    this.cache.set(index, text);
    while (this.cache.size > 8) this.cache.delete(this.cache.keys().next().value!);
  }

  async extend(): Promise<void> {
    if (this.pending) return this.pending;
    if (this.complete) return;
    this.pending = this.readNext();
    try { await this.pending; } finally { this.pending = undefined; }
  }

  private async readNext() {
    const start = this.segments.at(-1)?.end ?? 0;
    const { bytes, final } = await this.range(start, CHUNK);
    if (!start) this.encoding = detectBookEncoding(bytes, this.encoding);
    const length = safeTextEnd(bytes, this.encoding, final);
    if (!length) { this.complete = true; this.total = start; return; }
    const decoder = new TextDecoder(this.encoding, { ignoreBOM: start > 0, fatal: true });
    let text: string;
    try { text = decoder.decode(bytes.subarray(0, length), { stream: true }) + decoder.decode(); }
    catch { throw new Error(t("无法按所选编码读取，请切换字符编码")); }
    const index = this.segments.length;
    this.segments.push({ start, end: start + length, length: text.length });
    this.remember(index, text);
    const previousTailLength = this.chapterTail.length;
    const joined = this.chapterTail + text;
    const lastNewline = joined.lastIndexOf("\n");
    const completeLines = final ? joined : lastNewline >= 0 ? joined.slice(0, lastNewline) : "";
    for (const chapter of chapterLines(completeLines)) {
      if (chapter.offset < this.chapterTail.length) {
        this.chapters.push({ title: chapter.title, segment: this.tailSegment, offset: this.tailOffset + chapter.offset });
      } else this.chapters.push({ title: chapter.title, segment: index, offset: chapter.offset - this.chapterTail.length });
    }
    const tailStart = Math.max(lastNewline, joined.length - 256, 0);
    this.chapterTail = joined.slice(tailStart);
    if (tailStart >= previousTailLength) {
      this.tailSegment = index;
      this.tailOffset = tailStart - previousTailLength;
    } else this.tailOffset += tailStart;
    this.complete = final;
    if (final) this.total = start + length;
  }

  async segment(index: number): Promise<string> {
    while (index >= this.segments.length && !this.complete) await this.extend();
    if (!this.segments[index]) return "";
    const cached = this.cache.get(index);
    if (cached !== undefined) { this.remember(index, cached); return cached; }
    const part = this.segments[index];
    const { bytes } = await this.range(part.start, part.end - part.start);
    if (bytes.length !== part.end - part.start) throw new Error(t("文本字节范围不完整"));
    let text: string;
    try { text = new TextDecoder(this.encoding, { ignoreBOM: index > 0, fatal: true }).decode(bytes); }
    catch { throw new Error(t("无法按所选编码读取，请切换字符编码")); }
    this.remember(index, text);
    return text;
  }

  async scan(signal: AbortSignal, visit: (text: string, segment: number) => void): Promise<void> {
    for (let index = 0; !this.complete || index < this.segments.length; index++) {
      signal.throwIfAborted();
      const text = await this.segment(index);
      signal.throwIfAborted();
      if (index < this.segments.length) visit(text, index);
      await cooperative(signal);
    }
  }
}
