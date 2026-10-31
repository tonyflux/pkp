import { afterEach, describe, expect, it, vi } from "vitest";
import { chapterLines, detectBookEncoding, safeTextEnd, TextBook, textPageEnd, type BookEncoding } from "./reader-book-text";
import { epubTarget, epubXml, paginateEpub, sanitizeEpubChapter } from "./reader-book-epub";

afterEach(() => vi.unstubAllGlobals());

describe("book byte boundaries", () => {
  it.each<[BookEncoding, number[]]>([
    ["utf-8", [0xe4, 0xb8, 0xad]], ["utf-8", [0xf0, 0x9f, 0x98, 0x80]],
    ["utf-16le", [0x3d, 0xd8, 0, 0xde]], ["utf-16be", [0xd8, 0x3d, 0xde, 0]],
    ["gb18030", [0x90, 0x30, 0x81, 0x30]], ["gbk", [0xd6, 0xd0]], ["big5", [0xa4, 0xa4]],
  ])("keeps incomplete %s characters for the next Range", (encoding, character) => {
    for (let cut = 1; cut < character.length; cut++) expect(safeTextEnd(new Uint8Array(character.slice(0, cut)), encoding)).toBe(0);
    expect(safeTextEnd(new Uint8Array(character), encoding)).toBe(character.length);
  });
  it("detects BOMs and does not split rendered surrogate pairs", () => {
    expect(detectBookEncoding(new Uint8Array([0xff, 0xfe]), "gbk")).toBe("utf-16le");
    expect(detectBookEncoding(new Uint8Array([0xfe, 0xff]), "utf-8")).toBe("utf-16be");
    expect(detectBookEncoding(new Uint8Array([0x61]), "gb18030")).toBe("gb18030");
    const text = "a".repeat(7999) + "😀末";
    expect(textPageEnd(text, 0)).toBe(7999);
  });
  it("recognizes Chinese chapters and English chapters", () => {
    expect(chapterLines("前言\n正文\n第二百零一章 中国\nChapter 8 End").map(chapter => chapter.title)).toEqual(["前言", "第二百零一章 中国", "Chapter 8 End"]);
  });
  it("rejects GBK bytes under UTF8 with an actionable error and reads them after encoding selection", async () => {
    const bytes = new Uint8Array([0xd6, 0xd0, 0xce, 0xc4]);
    vi.stubGlobal("fetch", vi.fn(async () => new Response(bytes)));
    const wrong = new TextBook("https://local.invalid/book", "utf-8", bytes.length, new AbortController().signal);
    await expect(wrong.segment(0)).rejects.toThrow("无法按所选编码读取，请切换字符编码");
    expect(wrong.segments).toHaveLength(0);
    const selected = new TextBook("https://local.invalid/book", "gb18030", bytes.length, new AbortController().signal);
    await expect(selected.segment(0)).resolves.toBe("中文");
  });
  it("reads beyond 512 KB, reconstructs Chinese boundaries, and refetches evicted segments", async () => {
    const original = "前言\n" + "长篇中文😀\n".repeat(70000) + "\n第九章 结尾\nFULL_TEXT_END";
    const bytes = new TextEncoder().encode(original), calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (_url: string, init: RequestInit) => {
      const range = (init.headers as Record<string, string>).Range;
      calls.push(range);
      const match = /bytes=(\d+)-(\d+)/.exec(range)!;
      const start = Number(match[1]), end = Math.min(bytes.length - 1, Number(match[2]));
      return new Response(bytes.slice(start, end + 1), { status: 206, headers: { "Content-Range": `bytes ${start}-${end}/${bytes.length}` } });
    }));
    const book = new TextBook("https://local.invalid/book", "utf-8", bytes.length, new AbortController().signal);
    let reconstructed = "";
    await book.scan(new AbortController().signal, text => { reconstructed += text; });
    expect(reconstructed).toBe(original);
    expect(book.complete).toBe(true);
    expect(book.chapters.at(-1)?.title).toBe("第九章 结尾");
    const before = calls.length;
    expect(await book.segment(0)).toBe(original.slice(0, book.segments[0].length));
    expect(calls.length).toBe(before + 1);
  });
  it("cancels cooperative scanning without visiting later segments", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(new TextEncoder().encode("第一章 内容"))));
    const book = new TextBook("https://local.invalid/book", "utf-8", 0, new AbortController().signal);
    const controller = new AbortController(); controller.abort();
    await expect(book.scan(controller.signal, () => { throw new Error("must not visit"); })).rejects.toMatchObject({ name: "AbortError" });
  });
});

describe("EPUB inert parsing and bounded layout", () => {
  it("resolves local anchors but refuses network and traversal links", () => {
    expect(epubTarget("OPS/chapter.xhtml", "other.xhtml#%E4%B8%AD")).toEqual({ path: "OPS/other.xhtml", fragment: "中" });
    expect(epubTarget("OPS/chapter.xhtml", "#note")).toEqual({ path: "OPS/chapter.xhtml", fragment: "note" });
    for (const url of ["https://evil.invalid/x", "//evil.invalid/x", "../../escape", "javascript:alert(1)"]) expect(epubTarget("OPS/chapter.xhtml", url)).toBeNull();
  });
  it("strips DTD declarations and rejects internal entities", () => {
    expect(epubXml('<!DOCTYPE html PUBLIC "x" "https://evil.invalid/x"><html><body>safe</body></html>').documentElement.localName).toBe("html");
    expect(() => epubXml('<!DOCTYPE html [<!ENTITY x SYSTEM "file:///x">]><html>&x;</html>')).toThrow();
  });
  it("preserves book semantics without any executable or external resource attributes", () => {
    const source = `<html xmlns="http://www.w3.org/1999/xhtml"><body onload="bad()"><h1 id="heading">Title</h1><script>bad()</script><style>@import 'https://evil.invalid/x';</style><form><input /></form><iframe src="https://evil.invalid/x"/><svg><image href="https://evil.invalid/x"/></svg><p style="background:url(https://evil.invalid/x)"><em>emphasis</em><a href="next.xhtml#note">next</a><img src="https://evil.invalid/x"/><img src="pic.png" onerror="bad()" srcset="https://evil.invalid/y"/></p><table><tr><td>cell</td></tr></table></body></html>`;
    const fragment = sanitizeEpubChapter(source, "OPS/chapter.xhtml", new Set(["OPS/pic.png"]));
    const root = document.createElement("div"); root.append(fragment);
    expect(root.querySelector("h1")?.textContent).toBe("Title");
    expect(root.querySelector("em")?.textContent).toBe("emphasis");
    expect(root.querySelector("td")?.textContent).toBe("cell");
    expect(root.querySelector("a")?.dataset.bookPath).toBe("OPS/next.xhtml");
    expect(root.querySelectorAll("img")).toHaveLength(1);
    expect(root.querySelector("img")?.dataset.bookImage).toBe("OPS/pic.png");
    expect(root.querySelector("script,style,iframe,svg,form,input,[src],[srcset],[style],[onerror],[onload]")).toBeNull();
    expect(root.innerHTML).not.toContain("evil.invalid");
  });
  it("splits a very long paragraph into small pages without losing text or anchors", () => {
    const text = "中文😀".repeat(30000);
    const fragment = sanitizeEpubChapter(`<html><body><h1 id="title">Heading</h1><p>${text}</p><p id="end">End</p></body></html>`, "chapter.xhtml", new Set());
    const pages = paginateEpub(fragment);
    expect(pages.length).toBeGreaterThan(5);
    expect(pages.map(page => page.text).join("")).toBe("Heading" + text + "End");
    expect(pages.every(page => page.text.length <= 16000)).toBe(true);
    expect(pages.flatMap(page => page.ids)).toContain("end");
  });
});
