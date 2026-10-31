import { DocumentArchive, resolvePart } from "./consumption";
import { t } from "./i18n";
import { checkXmlComplexity } from "./reader-xml";

export interface EpubTarget { path: string; fragment: string }
export interface EpubChapter { path: string; title: string }
export interface EpubToc extends EpubTarget { title: string; depth: number }
export interface EpubPage { html: string; text: string; ids: string[] }
export interface EpubBook { archive: DocumentArchive; chapters: EpubChapter[]; toc: EpubToc[]; images: Set<string> }
const XML_LIMIT = 8 * 1024 * 1024;
const SAFE_TAGS: Record<string, true> = { p:true, div:true, section:true, article:true, header:true, footer:true, h1:true, h2:true, h3:true, h4:true, h5:true, h6:true, blockquote:true, pre:true, code:true, em:true, strong:true, b:true, i:true, u:true, s:true, sub:true, sup:true, small:true, span:true, br:true, hr:true, ul:true, ol:true, li:true, dl:true, dt:true, dd:true, table:true, thead:true, tbody:true, tfoot:true, tr:true, th:true, td:true, caption:true, colgroup:true, col:true, ruby:true, rt:true, rp:true, figure:true, figcaption:true, a:true, img:true, abbr:true, q:true, cite:true, mark:true };
const DROP_TAGS: Record<string, true> = { script:true, style:true, link:true, meta:true, base:true, iframe:true, frame:true, frameset:true, object:true, embed:true, form:true, input:true, button:true, textarea:true, select:true, option:true, video:true, audio:true, source:true, track:true, svg:true, math:true, canvas:true, template:true, noscript:true };
const SAFE_ATTRIBUTES: Record<string, true> = { id:true, title:true, lang:true, dir:true, colspan:true, rowspan:true, start:true, value:true, scope:true, alt:true };

export function epubXml(source: string): Document {
  if (/<!ENTITY|<!DOCTYPE[^>]*\[/i.test(source)) throw new Error(t("电子书包含不安全的 XML 声明"));
  checkXmlComplexity(source, 120000);
  // A declaration is never needed for rendering. Strip ordinary XHTML doctypes
  // rather than allowing the parser to resolve any external DTD.
  const doc = new DOMParser().parseFromString(source.replace(/<!DOCTYPE[^>]*>/gi, ""), "application/xml");
  if (doc.getElementsByTagName("parsererror").length) throw new Error(t("电子书结构损坏"));
  return doc;
}
function elements(root: Document | Element, name: string): Element[] { return Array.from(root.getElementsByTagNameNS("*", name)); }
function first(root: Document | Element, name: string): Element | undefined { return elements(root, name)[0]; }
export function epubTarget(base: string, href: string): EpubTarget | null {
  if (!href || /[\u0000-\u0020]/.test(href)) return null;
  const [relative, hash = ""] = href.split("#", 2);
  const path = relative ? resolvePart(base, relative) : base;
  if (!path) return null;
  try { return { path, fragment: decodeURIComponent(hash) }; } catch { return null; }
}

export async function openEpub(url: string, signal: AbortSignal): Promise<EpubBook> {
  const archive = await DocumentArchive.open(url, signal);
  try {
    const names = archive.names();
    if (names.includes("META-INF/encryption.xml") || names.includes("META-INF/rights.xml")) throw new Error(t("不支持 DRM 或加密电子书"));
    const container = epubXml(await archive.text("META-INF/container.xml", 1024 * 1024));
    const packagePath = first(container, "rootfile")?.getAttribute("full-path");
    if (!packagePath || resolvePart("root", packagePath) !== packagePath) throw new Error(t("电子书结构损坏"));
    const opf = epubXml(await archive.text(packagePath, XML_LIMIT));
    if (elements(opf, "meta").some(meta => (meta.getAttribute("property") === "rendition:layout" && meta.textContent?.trim() === "pre-paginated") || (["fixed-layout", "original-resolution"].includes(meta.getAttribute("name") ?? "") && meta.getAttribute("content") !== "false"))) throw new Error(t("不支持固定版式 EPUB"));
    const manifest = new Map<string, { path: string; type: string; properties: string[] }>();
    for (const item of elements(opf, "item")) {
      const path = resolvePart(packagePath, item.getAttribute("href") ?? "");
      if (path) manifest.set(item.getAttribute("id") ?? "", { path, type: item.getAttribute("media-type") ?? "", properties: (item.getAttribute("properties") ?? "").split(/\s+/) });
    }
    const chapters: EpubChapter[] = [];
    for (const item of elements(opf, "itemref")) {
      if ((item.getAttribute("properties") ?? "").split(/\s+/).includes("rendition:layout-pre-paginated")) throw new Error(t("不支持固定版式 EPUB"));
      const entry = manifest.get(item.getAttribute("idref") ?? "");
      if (!entry || !["application/xhtml+xml", "text/html"].includes(entry.type)) throw new Error(t("电子书结构损坏"));
      chapters.push({ path: entry.path, title: t("第 {n} 节", { n: chapters.length + 1 }) });
    }
    if (!chapters.length) throw new Error(t("电子书没有可阅读的章节"));
    const toc: EpubToc[] = [];
    const nav = [...manifest.values()].find(item => item.properties.includes("nav"));
    if (nav) {
      const doc = epubXml(await archive.text(nav.path, XML_LIMIT));
      const root = elements(doc, "nav").find(node => (node.getAttributeNS("http://www.idpf.org/2007/ops", "type") ?? node.getAttribute("epub:type") ?? "").split(/\s+/).includes("toc"));
      if (root) for (const a of elements(root, "a")) {
        const target = epubTarget(nav.path, a.getAttribute("href") ?? "");
        if (!target || !chapters.some(chapter => chapter.path === target.path)) continue;
        let depth = -1, ancestor = a.parentElement;
        while (ancestor && ancestor !== root) { if (ancestor.localName === "ol") depth++; ancestor = ancestor.parentElement; }
        toc.push({ ...target, title: a.textContent?.trim().slice(0, 256) || target.path, depth: Math.max(0, depth) });
      }
    }
    if (!toc.length) {
      const ncx = [...manifest.values()].find(item => item.type === "application/x-dtbncx+xml");
      if (ncx) {
        const doc = epubXml(await archive.text(ncx.path, XML_LIMIT));
        for (const point of elements(doc, "navPoint")) {
          const target = epubTarget(ncx.path, first(point, "content")?.getAttribute("src") ?? "");
          if (!target || !chapters.some(chapter => chapter.path === target.path)) continue;
          let depth = 0, ancestor = point.parentElement;
          while (ancestor) { if (ancestor.localName === "navPoint") depth++; ancestor = ancestor.parentElement; }
          toc.push({ ...target, title: first(point, "text")?.textContent?.trim().slice(0, 256) || target.path, depth });
        }
      }
    }
    for (const chapter of chapters) chapter.title = toc.find(item => item.path === chapter.path)?.title ?? chapter.title;
    const images = new Set([...manifest.values()].filter(item => /^image\/(png|jpeg|gif|webp|bmp|avif)$/.test(item.type)).map(item => item.path));
    signal.throwIfAborted();
    return { archive, chapters, toc: toc.length ? toc : chapters.map(chapter => ({ ...chapter, fragment: "", depth: 0 })), images };
  } catch (error) { await archive.close(); throw error; }
}

/** Parse XML inertly and rebuild an allowlisted HTML tree. No archive CSS, URL
 * attribute, custom element or active content ever reaches a live document. */
export function sanitizeEpubChapter(source: string, path: string, images: Set<string>): DocumentFragment {
  const parsed = epubXml(source);
  const output = document.createDocumentFragment();
  let nodes = 0;
  function copy(node: Node, parent: Node, depth: number) {
    if (++nodes > 120000 || depth > 100) throw new Error(t("电子书章节超过安全排版上限"));
    if (node.nodeType === 3) { parent.appendChild(document.createTextNode(node.textContent ?? "")); return; }
    if (node.nodeType !== 1) return;
    const sourceElement = node as Element, tag = sourceElement.localName.toLowerCase();
    if (Object.prototype.hasOwnProperty.call(DROP_TAGS, tag)) return;
    let destination = parent;
    if (Object.prototype.hasOwnProperty.call(SAFE_TAGS, tag)) {
      const element = document.createElement(tag);
      for (const attribute of Array.from(sourceElement.attributes)) if (Object.prototype.hasOwnProperty.call(SAFE_ATTRIBUTES, attribute.name) && attribute.value.length < 2048) element.setAttribute(attribute.name, attribute.value);
      if (tag === "a") {
        const target = epubTarget(path, sourceElement.getAttribute("href") ?? "");
        if (target) { element.dataset.bookPath = target.path; element.dataset.bookFragment = target.fragment; element.setAttribute("href", "#"); }
      }
      if (tag === "img") {
        const image = resolvePart(path, sourceElement.getAttribute("src") ?? "");
        if (!image || !images.has(image)) return;
        element.dataset.bookImage = image;
        element.setAttribute("loading", "lazy");
      }
      parent.appendChild(element);
      destination = element;
    }
    for (const child of Array.from(node.childNodes)) copy(child, destination, depth + 1);
  }
  const body = first(parsed, "body") ?? parsed.documentElement;
  for (const child of Array.from(body.childNodes)) copy(child, output, 0);
  return output;
}

/** Split even a single enormous paragraph/table into bounded DOM sections while
 * retaining its semantic ancestor chain. Only one section is mounted at a time. */
export function paginateEpub(fragment: DocumentFragment): EpubPage[] {
  const pages: EpubPage[] = [];
  let root = document.createElement("div"), count = 0, chars = 0;
  const originals: Element[] = [], clones: Element[] = [];
  const flush = () => {
    pages.push({ html: root.innerHTML, text: root.textContent ?? "", ids: Array.from(root.querySelectorAll("[id]")).map(node => node.id) });
    root = document.createElement("div"); count = 0; chars = 0; clones.length = 0;
    let parent: Element = root;
    for (const original of originals) { const clone = original.cloneNode(false) as Element; clone.removeAttribute("id"); parent.appendChild(clone); clones.push(clone); parent = clone; count++; }
  };
  const walk = (node: Node) => {
    if (count >= 1200 || chars >= 16000) flush();
    if (node.nodeType === 3) {
      const text = node.textContent ?? "";
      let offset = 0;
      while (offset < text.length) {
        if (chars >= 16000) flush();
        let end = Math.min(text.length, offset + 16000 - chars);
        if (end < text.length && /[\uD800-\uDBFF]/.test(text[end - 1])) end--;
        if (end === offset) { flush(); continue; }
        (clones.at(-1) ?? root).appendChild(document.createTextNode(text.slice(offset, end))); count++; chars += end - offset; offset = end;
      }
      return;
    }
    if (node.nodeType !== 1) return;
    const original = node as Element, clone = original.cloneNode(false) as Element;
    (clones.at(-1) ?? root).appendChild(clone); count++;
    originals.push(original); clones.push(clone);
    for (const child of Array.from(original.childNodes)) walk(child);
    originals.pop(); clones.pop();
  };
  for (const child of Array.from(fragment.childNodes)) walk(child);
  if (root.childNodes.length || !pages.length) flush();
  return pages;
}

export async function epubPages(book: EpubBook, chapter: number): Promise<EpubPage[]> {
  const path = book.chapters[chapter].path;
  return paginateEpub(sanitizeEpubChapter(await book.archive.text(path, XML_LIMIT), path, book.images));
}
