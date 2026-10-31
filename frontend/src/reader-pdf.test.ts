import { describe, expect, it } from "vitest";
import type { TextContent, TextItem } from "pdfjs-dist/types/src/display/api";
import { findPdfText, flattenPdfOutline, pdfText } from "./reader-pdf";

const item = (str: string, hasEOL = false): TextItem => ({ str, hasEOL, dir: "ltr", transform: [1, 0, 0, 1, 0, 0], width: str.length, height: 1, fontName: "test" });
describe("controlled PDF text and destinations", () => {
  it("indexes cross-run text while preserving line boundaries and highlight offsets", () => {
    const content: TextContent = { items: [item("Hello "), item("world", true), item("next")], styles: {}, lang: null };
    const indexed = pdfText(content);
    expect(indexed.text).toBe("Hello world\nnext");
    expect(indexed.runs).toEqual([{ text: "Hello ", start: 0, end: 6 }, { text: "world", start: 6, end: 11 }, { text: "next", start: 12, end: 16 }]);
    expect(findPdfText(indexed.text, "o wo", 2)).toEqual([{ page: 2, start: 4, end: 8 }]);
  });
  it("treats regex punctuation literally, preserves Unicode offsets, and caps matches", () => {
    expect(findPdfText("a.b A.B", "a.b", 1)).toHaveLength(2);
    expect(findPdfText("你好你好", "你好", 1, 1)).toEqual([{ page: 1, start: 0, end: 2 }]);
    expect(findPdfText("İ hello", "hello", 1)).toEqual([{ page: 1, start: 2, end: 7 }]);
  });
  it("retains internal outline destinations but disables external outline actions", () => {
    expect(flattenPdfOutline([{ title: "Chapter", dest: "chapter", items: [{ title: "Remote", dest: null, url: "https://example.com" }] }])).toEqual([{ title: "Chapter", depth: 0, dest: "chapter" }, { title: "Remote", depth: 1, dest: null }]);
  });
});
