import { describe, expect, it, vi } from "vitest";
import { TextWriter, Uint8ArrayReader, Uint8ArrayWriter, ZipReader, ZipWriter } from "@zip.js/zip.js";
import { loadOfficePackage, sanitizeOfficeXml } from "./reader-office-archive";
import { officeFrameDocument, safeOfficeCss, sanitizeOfficeHtml } from "./reader-office-render";

const relNamespace = "http://schemas.openxmlformats.org/package/2006/relationships";
const officeRelationship = "http://schemas.openxmlformats.org/officeDocument/2006/relationships";
const w = "http://schemas.openxmlformats.org/wordprocessingml/2006/main";
const a = "http://schemas.openxmlformats.org/drawingml/2006/main";

describe("Office security boundary", () => {
  it("rejects XML entities before parsing", () => {
    expect(() => sanitizeOfficeXml('<!DOCTYPE x [<!ENTITY leak SYSTEM "https://attacker.invalid">]><x>&leak;</x>', "word/document.xml", new Set())).toThrow();
  });
  it("retains only package-local allowlisted relationships", () => {
    const xml = `<Relationships xmlns="${relNamespace}"><Relationship Id="ok" Type="${officeRelationship}/image" Target="media/image1.png"/><Relationship Id="external" Type="${officeRelationship}/image" Target="https://attacker.invalid/image.png" TargetMode="External"/><Relationship Id="escape" Type="${officeRelationship}/image" Target="../../outside.png"/><Relationship Id="ole" Type="${officeRelationship}/oleObject" Target="embeddings/object.bin"/></Relationships>`;
    const safe = sanitizeOfficeXml(xml, "word/_rels/document.xml.rels", new Set(["word/media/image1.png", "word/embeddings/object.bin"]));
    expect(safe).toContain('Id="ok"');
    expect(safe).not.toContain('Id="external"');
    expect(safe).not.toContain('Id="escape"');
    expect(safe).not.toContain('Id="ole"');
    expect(safe).toContain(`${officeRelationship}/image`);
  });
  it("strips active HTML, remote images and CSS before the display frame", () => {
    const root = document.implementation.createHTMLDocument("").createElement("div");
    root.innerHTML = '<style>body{background:url(https://attacker.invalid)}</style><script>alert(1)</script><iframe src="https://attacker.invalid"></iframe><img src="https://attacker.invalid" onerror="alert(1)"><p style="background:url(//attacker.invalid)">Text</p>';
    const safe = sanitizeOfficeHtml(root);
    expect(safe).not.toContain("attacker.invalid");
    expect(safe).not.toContain("onerror");
    expect(safe).not.toContain("<iframe");
    expect(safe).not.toContain("<script");
    expect(safe).toContain("Text");
    expect(officeFrameDocument(safe)).toContain("default-src 'none'");
    expect(officeFrameDocument(safe)).toContain("connect-src 'none'");
    expect(safeOfficeCss('color:red;background:url("https://attacker.invalid")')).toBe("");
    expect(safeOfficeCss('fill:url(#shape)')).toBe('fill:url(#shape)');
  });
  it("keeps safe raster data images including DOCX octet-stream blobs", () => {
    const root = document.createElement("div");
    root.innerHTML = '<img src="data:application/octet-stream;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB">';
    expect(sanitizeOfficeHtml(root)).toContain("data:image/png;base64,");
  });
  it("bounds package entry expansion and rejects traversal before rendering", async () => {
    const writer = new ZipWriter(new Uint8ArrayWriter(), { useWebWorkers: false });
    await writer.add("../outside.xml", new Uint8ArrayReader(new TextEncoder().encode("unsafe")));
    const bytes = await writer.close();
    const request = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(bytes));
    try { await expect(loadOfficePackage("https://local.invalid/document", new AbortController().signal)).rejects.toThrow(); }
    finally { request.mockRestore(); }
  });
  it("retains DOCX text, drawing namespaces and local image relationships in the sanitized package", async () => {
    const writer = new ZipWriter(new Uint8ArrayWriter(), { useWebWorkers: false });
    const wp = "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing";
    const files: Record<string, string> = {
      "word/document.xml": `<w:document xmlns:w="${w}" xmlns:a="${a}" xmlns:wp="${wp}" xmlns:r="${officeRelationship}" xmlns:h="http://www.w3.org/1999/xhtml"><w:body><w:p><w:r><w:t>Document text</w:t><w:drawing><wp:inline><a:graphic><a:blip r:embed="image"/></a:graphic></wp:inline></w:drawing></w:r><h:script>unsafe</h:script></w:p></w:body></w:document>`,
      "word/_rels/document.xml.rels": `<Relationships xmlns="${relNamespace}"><Relationship Id="image" Type="${officeRelationship}/image" Target="media/image1.png"/><Relationship Id="external" Type="${officeRelationship}/image" Target="https://attacker.invalid/image.png" TargetMode="External"/></Relationships>`,
    };
    for (const [path, source] of Object.entries(files)) await writer.add(path, new Uint8ArrayReader(new TextEncoder().encode(source)));
    const image = Uint8Array.of(137, 80, 78, 71, 13, 10, 26, 10);
    await writer.add("word/media/image1.png", new Uint8ArrayReader(image));
    const request = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(await writer.close()));
    try {
      const pkg = await loadOfficePackage("https://local.invalid/document", new AbortController().signal);
      const reader = new ZipReader(new Uint8ArrayReader(pkg.bytes), { useWebWorkers: false });
      try {
        const entries = await reader.getEntries();
        const documentEntry = entries.find(entry => entry.filename === "word/document.xml");
        const relationEntry = entries.find(entry => entry.filename === "word/_rels/document.xml.rels");
        if (!documentEntry || documentEntry.directory || !relationEntry || relationEntry.directory) throw new Error("Missing DOCX parts");
        const xml = new DOMParser().parseFromString(await documentEntry.getData(new TextWriter()), "application/xml");
        expect(xml.getElementsByTagNameNS(w, "t")[0]?.textContent).toBe("Document text");
        expect(xml.getElementsByTagNameNS(wp, "inline")).toHaveLength(1);
        expect(xml.getElementsByTagNameNS(a, "blip")[0]?.getAttributeNS(officeRelationship, "embed")).toBe("image");
        expect(xml.getElementsByTagNameNS("http://www.w3.org/1999/xhtml", "script")).toHaveLength(0);
        const relations = new DOMParser().parseFromString(await relationEntry.getData(new TextWriter()), "application/xml");
        const retained = Array.from(relations.getElementsByTagNameNS(relNamespace, "Relationship"));
        expect(retained.map(node => [node.getAttribute("Id"), node.getAttribute("Target")])).toEqual([["image", "media/image1.png"]]);
        const imageEntry = entries.find(entry => entry.filename === "word/media/image1.png");
        if (!imageEntry || imageEntry.directory) throw new Error("Missing DOCX image");
        expect(await imageEntry.getData(new Uint8ArrayWriter())).toEqual(image);
      } finally { await reader.close(); }
    } finally { request.mockRestore(); }
  });
});
