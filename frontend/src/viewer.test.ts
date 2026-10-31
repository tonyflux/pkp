import { describe, expect, it } from "vitest";
import { fitScale, viewerError, viewerKind, zoomIn, zoomOut } from "./viewer";

describe("viewerKind", () => {
  it("reads Markdown apart from other text", () => {
    expect(viewerKind({ name: "海岸旅行计划.md", preview: "text" })).toBe("markdown");
    expect(viewerKind({ name: "README.MARKDOWN", preview: "text" })).toBe("markdown");
    expect(viewerKind({ name: "notes", preview: "text", mimeType: "text/markdown" })).toBe("markdown");
    expect(viewerKind({ name: ".md", preview: "text" })).toBe("text");
  });

  it("anything the drive offers no preview for is unsupported", () => {
    expect(viewerKind({ name: "a.jpg", preview: "image" })).toBe("image");
    expect(viewerKind({ name: "a.pdf", preview: "pdf" })).toBe("pdf");
    expect(viewerKind({ name: "项目素材.zip", preview: "" })).toBe("unsupported");
  });

  it("keeps presentations unsupported while retaining document and spreadsheet readers", () => {
    expect(viewerKind({ name: "slides.PPTX", preview: "document" })).toBe("unsupported");
    expect(viewerKind({ name: "slides.ppt", preview: "" })).toBe("unsupported");
    expect(viewerKind({ name: "report.DOCX", preview: "document" })).toBe("office");
    expect(viewerKind({ name: "table.xlsx", preview: "document" })).toBe("table");
  });
});

describe("zoom", () => {
  it("fits without enlarging a small picture", () => {
    expect(fitScale({ w: 640, h: 440 }, { w: 1200, h: 800 })).toBe(1);
    expect(fitScale({ w: 4000, h: 3000 }, { w: 1000, h: 1000 })).toBe(0.25);
    expect(fitScale({ w: 0, h: 0 }, { w: 1000, h: 1000 })).toBe(1);
  });

  it("steps up from the fitted size and back down to it", () => {
    expect(zoomIn("fit", 0.3)).toBe(0.5);
    expect(zoomIn("fit", 1)).toBe(1.5);
    expect(zoomIn(1, 0.3)).toBe(1.5);
    expect(zoomIn(8, 0.3)).toBeNull();
    expect(zoomOut(1.5, 0.3)).toBe(1);
    expect(zoomOut(0.5, 0.3)).toBe("fit");
    expect(zoomOut(1.5, 1)).toBe("fit");
    expect(zoomOut("fit", 0.3)).toBeNull();
  });
});

describe("viewerError", () => {
  it("puts the service's failures in words, keeping unknown Chinese ones as they are", () => {
    expect(viewerError(new Error("drive: fetch text: upstream status 403"))).toBe("云盘没有返回文件内容（HTTP 403），可以稍后再试");
    expect(viewerError(new Error("这个文件暂时没有可用的下载地址，可能还没有处理完"))).toMatch(/没有可用的下载地址/);
    expect(viewerError(new Error('Get "https://x": context deadline exceeded'))).toMatch(/读取超时/);
    expect(viewerError("回收站里已经没有这个文件")).toBe("回收站里已经没有这个文件");
    expect(viewerError("something else")).toBe("没有读到文件内容，可以稍后再试");
  });
});
