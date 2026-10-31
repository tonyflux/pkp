import { describe, expect, it, vi, afterEach } from "vitest";
import { BlobReader, BlobWriter, TextReader, ZipWriter } from "@zip.js/zip.js";
import { DocumentArchive, readBounded, resolvePart } from "./consumption";

afterEach(() => vi.unstubAllGlobals());
describe("document package boundaries", () => {
  it("resolves encoded chapter-relative assets without escaping the package", () => {
    expect(resolvePart("OPS/chapters/a.xhtml", "../images/a%20b.png")).toBe("OPS/images/a b.png");
    for (const path of ["../../../secret", "%2f%2fevil.test/a", "https://evil.test/a", "data:text/html,x", "..%5csecret"]) expect(resolvePart("OPS/a.xhtml", path)).toBeNull();
  });
  it("cancels bodies that exceed the limit even without a Content-Length", async () => {
    let cancelled = false;
    vi.stubGlobal("fetch", vi.fn(async () => new Response(new ReadableStream({
      start(c) { c.enqueue(new Uint8Array(9)); }, cancel() { cancelled = true; },
    }))));
    await expect(readBounded("http://local/file", 8)).rejects.toThrow();
    expect(cancelled).toBe(true);
  });
  it("reads chapter bytes with Range without fetching the entire ZIP", async () => {
    const writer = new ZipWriter(new BlobWriter(), { useWebWorkers: false });
    await writer.add("OPS/chapter.xhtml", new TextReader("<p>Hello</p>"));
    const blob = await writer.close();
    const data = new Uint8Array(await blob.arrayBuffer());
    const ranges: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (_url: string, init: RequestInit) => {
      const range = (init.headers as Record<string,string>).Range;
      ranges.push(range);
      const [,a,b] = /^bytes=(\d+)-(\d+)$/.exec(range)!;
      const start = Number(a), end = Number(b);
      return new Response(data.slice(start,end+1), {status:206, headers:{"Content-Range":`bytes ${start}-${end}/${data.length}`}});
    }));
    const archive = await DocumentArchive.open("http://local/book");
    expect(await archive.text("OPS/chapter.xhtml")).toBe("<p>Hello</p>");
    await archive.close();
    expect(ranges.every(r => /^bytes=\d+-\d+$/.test(r))).toBe(true);
    await expect(archive.text("OPS/chapter.xhtml")).rejects.toMatchObject({name:"AbortError"});
  });
});
