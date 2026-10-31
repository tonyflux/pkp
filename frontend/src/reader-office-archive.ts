import { Uint8ArrayReader, Uint8ArrayWriter, ZipReader, ZipWriter } from "@zip.js/zip.js";
import type { Entry, FileEntry } from "@zip.js/zip.js";
import { rasterMime, readBounded, resolvePart } from "./consumption";
import { t } from "./i18n";
import { checkXmlComplexity } from "./reader-xml";

export const OFFICE_LIMITS = { compressed: 32 * 1024 * 1024, expanded: 96 * 1024 * 1024, member: 16 * 1024 * 1024, xml: 8 * 1024 * 1024, entries: 2000, pages: 300, nodes: 100000 };
const namespaces: Record<string, true> = {
  "http://schemas.openxmlformats.org/wordprocessingml/2006/main": true,
  "http://schemas.openxmlformats.org/drawingml/2006/main": true,
  "http://schemas.openxmlformats.org/drawingml/2006/picture": true,
  "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing": true,
  "http://schemas.openxmlformats.org/package/2006/relationships": true,
  "http://schemas.openxmlformats.org/package/2006/content-types": true,
  "http://schemas.openxmlformats.org/officeDocument/2006/relationships": true,
  "http://schemas.openxmlformats.org/markup-compatibility/2006": true,
  "http://schemas.openxmlformats.org/officeDocument/2006/extended-properties": true,
  "http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes": true,
};
const forbidden: Record<string, true> = Object.fromEntries(["altChunk", "object", "oleObject", "control", "controls", "embeddedFontLst", "embedRegular", "embedBold", "embedItalic", "embedBoldItalic", "video", "audio", "audioFile", "videoFile", "wavAudioFile", "snd", "hlinkClick", "hlinkHover", "instrText", "fldSimple", "subDoc", "attachedTemplate", "webSettings"].map(name => [name, true as const]));
const allowedRelationships = /\/(?:officeDocument|styles|numbering|settings|theme|fontTable|header|footer|footnotes|endnotes|image|extended-properties)$/;

export function parseOfficeXml(source: string): XMLDocument {
  if (/<!\s*(?:DOCTYPE|ENTITY)/i.test(source)) throw new Error(t("office|文档包含不安全的 XML"));
  checkXmlComplexity(source, OFFICE_LIMITS.nodes);
  const xml = new DOMParser().parseFromString(source, "application/xml");
  if (xml.getElementsByTagName("parsererror").length) throw new Error(t("office|文档的 XML 已损坏"));
  return xml;
}

/** Only static OOXML and package-local relationships reach the renderer. */
export function sanitizeOfficeXml(source: string, path: string, members: ReadonlySet<string>): string {
  const xml = parseOfficeXml(source);
  const relationBase = path.replace(/(?:^|\/)\_rels\//, "/").replace(/\.rels$/, "").replace(/^\//, "");
  for (const node of Array.from(xml.getElementsByTagName("*"))) {
    if (!node.isConnected) continue;
    const local = node.localName;
    if (namespaces[node.namespaceURI ?? ""] !== true || forbidden[local] === true) { node.remove(); continue; }
    if (local === "Relationship") {
      const target = node.getAttribute("Target") ?? "";
      const resolved = resolvePart(relationBase, target);
      if (node.getAttribute("TargetMode")?.toLowerCase() === "external" || !allowedRelationships.test(node.getAttribute("Type") ?? "") || !resolved || !members.has(resolved)) { node.remove(); continue; }
    }
    if (local === "Override") {
      const part = (node.getAttribute("PartName") ?? "").replace(/^\//, "");
      if (!members.has(part)) { node.remove(); continue; }
    }
    if (local === "sym" && !/^[a-f\d]{1,6}$/i.test(node.getAttribute("w:char") ?? "")) { node.remove(); continue; }
    if (local === "hyperlink") { node.replaceWith(...Array.from(node.childNodes)); continue; }
    for (const attr of Array.from(node.attributes)) {
      if (attr.name === "xmlns" || attr.prefix === "xmlns") continue;
      if ((attr.namespaceURI && namespaces[attr.namespaceURI] !== true && attr.namespaceURI !== "http://www.w3.org/XML/1998/namespace") || /^on/i.test(attr.localName) || /^(?:href|src|action|link)$/i.test(attr.localName) || ((!/^(?:Type|uri)$/.test(attr.localName)) && /(?:javascript|vbscript|https?|file|ftp):|\/\/|url\s*\(/i.test(attr.value))) node.removeAttributeNode(attr);
    }
  }
  if (!xml.documentElement) throw new Error(t("office|文档的 XML 已损坏"));
  return new XMLSerializer().serializeToString(xml);
}

export type OfficePackage = { bytes: Uint8Array };
export async function loadOfficePackage(url: string, signal: AbortSignal): Promise<OfficePackage> {
  const bytes = await readBounded(url, OFFICE_LIMITS.compressed, signal);
  const reader = new ZipReader(new Uint8ArrayReader(bytes), { useWebWorkers: false, checkSignature: true });
  const writer = new ZipWriter(new Uint8ArrayWriter(), { useWebWorkers: false });
  let outputClosed = false;
  try {
    const entries: Entry[] = [];
    for await (const entry of reader.getEntriesGenerator()) {
      if (entries.length >= OFFICE_LIMITS.entries) throw new Error(t("office|文档超过安全预览上限"));
      entries.push(entry);
    }
    let expanded = 0;
    const members = new Set<string>();
    for (const entry of entries) {
      if (entry.directory) continue;
      const name = entry.filename;
      expanded += entry.uncompressedSize;
      if (!name || name.length > 512 || name.startsWith("/") || /[\\\u0000-\u001f]/.test(name) || name.split("/").some(part => part === "." || part === "..") || members.has(name) || entry.encrypted || !Number.isSafeInteger(expanded) || expanded > OFFICE_LIMITS.expanded || entry.uncompressedSize > OFFICE_LIMITS.member || (entry.uncompressedSize > 1024 * 1024 && entry.uncompressedSize / Math.max(entry.compressedSize, 1) > 200)) throw new Error(t("office|文档超过安全预览上限"));
      members.add(name);
    }
    if (!members.has("word/document.xml")) throw new Error(t("office|文档结构不完整"));
    const files = new Map<string, Uint8Array>();
    const rawXml = new Map<string, string>();
    const retained = new Set<string>();
    let xmlNodes = 0;
    for (const entry of entries) {
      if (signal.aborted) throw new DOMException("Aborted", "AbortError");
      if (entry.directory) continue;
      const path = entry.filename;
      if (/\.(?:xml|rels)$/i.test(path) && (/^word\//.test(path) || ["[Content_Types].xml", "_rels/.rels", "docProps/app.xml"].includes(path)) && !/(?:embeddings|activeX|customXml|webExtensions)\//i.test(path)) {
        if (entry.uncompressedSize > OFFICE_LIMITS.xml) throw new Error(t("office|文档超过安全预览上限"));
        const data = await extractOfficeEntry(entry, OFFICE_LIMITS.xml, signal);
        const source = new TextDecoder("utf-8", { fatal: true }).decode(data);
        xmlNodes += checkXmlComplexity(source, OFFICE_LIMITS.nodes - xmlNodes);
        if (namespaces[parseOfficeXml(source).documentElement.namespaceURI ?? ""] !== true) continue;
        rawXml.set(path, source); retained.add(path);
      } else if (/^word\/media\//.test(path) && /\.(?:png|jpe?g|gif|webp|bmp)$/i.test(path)) {
        const data = await extractOfficeEntry(entry, OFFICE_LIMITS.member, signal);
        if (rasterMime(data)) { files.set(path, data); retained.add(path); }
      }
    }
    for (const [path, value] of rawXml) {
      const safe = sanitizeOfficeXml(value, path, retained);
      files.set(path, new TextEncoder().encode(safe));
    }
    for (const [path, value] of files) await writer.add(path, new Uint8ArrayReader(value), { signal, level: 0 });
    const result = await writer.close(); outputClosed = true;
    return { bytes: result };
  } finally { await reader.close(); if (!outputClosed) await writer.close().catch(() => undefined); }
}
async function extractOfficeEntry(entry: FileEntry, maximum: number, signal: AbortSignal): Promise<Uint8Array> {
  const chunks: Uint8Array[] = [];
  let size = 0;
  const writable = new WritableStream<Uint8Array>({ write(chunk) {
    size += chunk.byteLength;
    if (size > maximum || size > entry.uncompressedSize) throw new Error(t("office|文档超过安全预览上限"));
    chunks.push(chunk);
  } });
  await entry.getData({ writable }, { signal, checkSignature: true });
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  return bytes;
}
