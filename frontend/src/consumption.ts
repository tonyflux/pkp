import { t } from "./i18n";
import { viewerError } from "./viewer";
import * as zip from "@zip.js/zip.js";
import type { FileEntry, ZipReader } from "@zip.js/zip.js";

export type ConsumptionEntry = { id: string; name: string; size: number; modifiedTime: string; parentId: string };
export const errorMessage = viewerError;
const MiB = 1024 * 1024;

export function loadReading<T>(identity: string, fallback: T): T {
  try { return JSON.parse(localStorage.getItem(`pkp.reading.${identity}`) ?? "null") ?? fallback; }
  catch { return fallback; }
}
export function saveReading(identity: string, value: unknown): void {
  try { localStorage.setItem(`pkp.reading.${identity}`, JSON.stringify(value)); } catch { /* Storage may be unavailable or full. */ }
}

async function responseBytes(response: Response, maxBytes: number): Promise<Uint8Array> {
  if (!response.ok) throw new Error(t("读取文件失败（HTTP {status}）", { status: response.status }));
  if (Number(response.headers.get("Content-Length")) > maxBytes) {
    await response.body?.cancel();
    throw new Error(t("内容超过安全读取上限"));
  }
  const reader = response.body?.getReader();
  if (!reader) return new Uint8Array();
  const parts: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxBytes) throw new Error(t("内容超过安全读取上限"));
      parts.push(value);
    }
  } catch (error) { await reader.cancel().catch(() => undefined); throw error; }
  finally { reader.releaseLock(); }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const part of parts) { bytes.set(part, offset); offset += part.byteLength; }
  return bytes;
}
export async function readBounded(url: string, maxBytes: number, signal?: AbortSignal): Promise<Uint8Array> {
  return responseBytes(await fetch(url, { signal }), maxBytes);
}

/** Resolve package-relative paths without allowing a package to address the network. */
export function resolvePart(base: string, relative: string): string | null {
  let path: string;
  try { path = decodeURIComponent(relative.split(/[?#]/, 1)[0]); } catch { return null; }
  if (!path || /[\u0000-\u001f\\]/.test(path) || /^(?:[a-z][a-z\d+.-]*:|\/)/i.test(path)) return null;
  const parts = base.split("/").slice(0, -1);
  for (const part of path.split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") { if (!parts.length) return null; parts.pop(); }
    else parts.push(part);
  }
  return parts.join("/") || null;
}

export class DocumentArchive {
  private files = new Map<string, FileEntry>();
  private images = new Map<string, string>();
  private pendingImages = new Map<string, Promise<string>>();
  private controller = new AbortController();
  private zip!: ZipReader<unknown>;
  private closed = false;
  private detach?: () => void;

  static async open(url: string, signal?: AbortSignal): Promise<DocumentArchive> {
    const archive = new DocumentArchive();
    const cancel = () => archive.controller.abort();
    signal?.addEventListener("abort", cancel, { once: true });
    archive.detach = () => signal?.removeEventListener("abort", cancel);
    if (signal?.aborted) cancel();
    try {
      const first = await fetch(url, { headers: { Range: "bytes=0-0" }, signal: archive.controller.signal });
      let source;
      if (first.status === 206) {
        const match = /^bytes 0-0\/(\d+)$/.exec(first.headers.get("Content-Range") ?? "");
        if (!match || !Number.isSafeInteger(Number(match[1]))) { await first.body?.cancel(); throw new Error(t("文件服务器没有返回有效的字节范围")); }
        const size = Number(match[1]);
        await first.body?.cancel();
        source = new class extends zip.Reader<string> {
          constructor() { super(url); this.size = size; }
          async readUint8Array(offset: number, length: number) {
            if (length === 0) return new Uint8Array();
            if (length > 64 * MiB || offset < 0 || offset + length > size) throw new Error(t("内容超过安全读取上限"));
            const response = await fetch(url, { headers: { Range: `bytes=${offset}-${offset + length - 1}` }, signal: archive.controller.signal });
            const expected = `bytes ${offset}-${offset + length - 1}/${size}`;
            if (response.status !== 206 || response.headers.get("Content-Range") !== expected) {
              await response.body?.cancel(); throw new Error(t("文件服务器没有返回有效的字节范围"));
            }
            const bytes = await responseBytes(response, length);
            if (bytes.length !== length) throw new Error(t("文件内容不完整"));
            return bytes;
          }
        }();
      } else {
        // Servers without Range may still serve small documents, never an unbounded archive.
        source = new zip.Uint8ArrayReader(await responseBytes(first, 64 * MiB));
      }
      archive.zip = new zip.ZipReader(source, { useWebWorkers: false, checkSignature: true });
      let expanded = 0;
      let entries = 0;
      for await (const entry of archive.zip.getEntriesGenerator()) {
        if (++entries > 20000) throw new Error(t("压缩容器中的项目过多"));
        const name = entry.filename;
        if (entry.directory) continue;
        if (!name || name.length > 2048 || name.startsWith("/") || /[\\\u0000-\u001f]/.test(name) || /^[a-z][a-z\d+.-]*:/i.test(name) || name.split("/").some(p => p === ".." || p === ".") || archive.files.has(name)) throw new Error(t("压缩容器包含不安全的路径"));
        expanded += entry.uncompressedSize;
        if (!Number.isSafeInteger(expanded) || expanded > 2 * 1024 * MiB || entry.uncompressedSize > 256 * MiB || (entry.uncompressedSize > MiB && entry.uncompressedSize / Math.max(entry.compressedSize, 1) > 1000)) throw new Error(t("压缩容器超过安全展开上限"));
        if (entry.encrypted) throw new Error(t("不支持加密的压缩容器"));
        archive.files.set(name, entry);
      }
      return archive;
    } catch (error) { await archive.close(); throw error; }
  }
  names(): string[] { return [...this.files.keys()]; }
  async read(path: string, maxBytes = 16 * MiB): Promise<Uint8Array> {
    if (this.closed) throw new DOMException("Aborted", "AbortError");
    const entry = this.files.get(path);
    if (!entry) throw new Error(t("文档资源不存在：{path}", { path }));
    if (entry.uncompressedSize > maxBytes) throw new Error(t("内容超过安全读取上限"));
    const parts: Uint8Array[] = [];
    let count = 0;
    const writer = new WritableStream<Uint8Array>({ write(chunk) {
      count += chunk.byteLength;
      if (count > maxBytes) throw new Error(t("内容超过安全读取上限"));
      parts.push(chunk);
    } });
    await entry.getData({ writable: writer }, { signal: this.controller.signal, checkSignature: true });
    const bytes = new Uint8Array(count);
    let offset = 0;
    for (const part of parts) { bytes.set(part, offset); offset += part.byteLength; }
    return bytes;
  }
  async text(path: string, maxBytes = 8 * MiB): Promise<string> {
    return new TextDecoder("utf-8", { fatal: true }).decode(await this.read(path, maxBytes));
  }
  async image(path: string): Promise<string> {
    if (this.images.has(path)) return this.images.get(path)!;
    if (this.pendingImages.has(path)) return this.pendingImages.get(path)!;
    const pending = (async () => {
      const bytes = await this.read(path, 32 * MiB);
      const mime = rasterMime(bytes);
      if (!mime) throw new Error(t("此图片格式不能安全预览"));
      if (this.closed) throw new DOMException("Aborted", "AbortError");
      const url = URL.createObjectURL(new Blob([bytes], { type: mime }));
      this.images.set(path, url);
      return url;
    })();
    this.pendingImages.set(path, pending);
    try { return await pending; } finally { this.pendingImages.delete(path); }
  }
  releaseImage(url: string): void {
    for (const [path, value] of this.images) if (value === url) { URL.revokeObjectURL(url); this.images.delete(path); }
  }
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    this.controller.abort(); this.detach?.();
    for (const url of this.images.values()) URL.revokeObjectURL(url);
    this.images.clear(); this.files.clear();
    await this.zip?.close().catch(() => undefined);
  }
}

export function rasterMime(bytes: Uint8Array): string | null {
  if (bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff) return "image/jpeg";
  if (bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47) return "image/png";
  const head = String.fromCharCode(...bytes.subarray(0, 12));
  if (head.startsWith("GIF87a") || head.startsWith("GIF89a")) return "image/gif";
  if (head.startsWith("RIFF") && head.slice(8) === "WEBP") return "image/webp";
  if (head.startsWith("BM")) return "image/bmp";
  if (head.slice(4, 8) === "ftyp" && /avif|avis/.test(head.slice(8))) return "image/avif";
  return null;
}
