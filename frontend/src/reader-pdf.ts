import type { TextContent } from "pdfjs-dist/types/src/display/api";
import { t } from "./i18n";

export type PdfMatch = { page: number; start: number; end: number };
export type PdfTextRun = { text: string; start: number; end: number };
export type PdfOutline = { title: string; depth: number; dest: string | unknown[] | null };
export function pdfText(content: TextContent): { text: string; runs: PdfTextRun[] } {
  let text = "";
  const runs: PdfTextRun[] = [];
  for (const item of content.items) {
    if (!("str" in item)) continue;
    const start = text.length;
    text += item.str;
    runs.push({ text: item.str, start, end: text.length });
    if (item.hasEOL) text += "\n";
  }
  return { text, runs };
}
export function findPdfText(text: string, query: string, page: number, limit = 100000): PdfMatch[] {
  if (!query) return [];
  const pattern = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "gi");
  const matches: PdfMatch[] = [];
  let match: RegExpExecArray | null;
  while (matches.length < limit && (match = pattern.exec(text))) matches.push({ page, start: match.index, end: match.index + match[0].length });
  return matches;
}
export function flattenPdfOutline(nodes: Array<{ title: string; dest: string | unknown[] | null; url?: string | null; items?: unknown[] }>): PdfOutline[] {
  const output: PdfOutline[] = [];
  const stack = nodes.map(node => ({ node, depth: 0 })).reverse();
  while (stack.length && output.length < 10000) {
    const { node, depth } = stack.pop()!;
    output.push({ title: node.title, depth, dest: node.url ? null : node.dest });
    if (depth < 32 && node.items) for (let i = node.items.length - 1; i >= 0; i--) stack.push({ node: node.items[i] as typeof node, depth: depth + 1 });
  }
  return output;
}

/** Only bundled PDF.js resources are addressable; document-supplied paths never reach fetch. */
const cmaps = import.meta.glob<string>("../node_modules/pdfjs-dist/cmaps/*.bcmap", { eager: true, query: "?url", import: "default" });
const fonts = import.meta.glob<string>("../node_modules/pdfjs-dist/standard_fonts/*.{pfb,ttf}", { eager: true, query: "?url", import: "default" });
const wasm = import.meta.glob<string>("../node_modules/pdfjs-dist/wasm/*.wasm", { eager: true, query: "?url", import: "default" });
async function bundledResource(files: Record<string, string>, name: string): Promise<Uint8Array> {
  if (!/^[\w.-]+$/.test(name)) throw new Error(t("文档资源不存在：{path}", { path: name }));
  const path = Object.keys(files).find(key => key.endsWith(`/${name}`));
  if (!path) throw new Error(t("文档资源不存在：{path}", { path: name }));
  const response = await fetch(files[path]);
  if (!response.ok) throw new Error(t("读取文件失败（HTTP {status}）", { status: response.status }));
  return new Uint8Array(await response.arrayBuffer());
}
export class PdfCMapFactory {
  async fetch({ name }: { name: string }) { return { cMapData: await bundledResource(cmaps, `${name}.bcmap`), compressionType: 1 }; }
}
export class PdfFontFactory {
  async fetch({ filename }: { filename: string }) { return bundledResource(fonts, filename); }
}
export class PdfWasmFactory {
  async fetch({ filename }: { filename: string }) { return bundledResource(wasm, filename); }
}
