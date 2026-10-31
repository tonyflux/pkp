import { describe, expect, it } from "vitest";
import { failureOf, picksOf, selectionSummary } from "./extract";
import type { ArchiveItem } from "../bindings/github.com/tonyflux/pkp/internal/services";

const item = (p: Partial<ArchiveItem>): ArchiveItem =>
  ({ index: 0, name: "x", folder: false, size: 0, path: "x", category: "other", ...p }) as ArchiveItem;

const items = [
  item({ name: "原片", folder: true, path: "原片/", category: "folder" }),
  item({ index: 3, name: "封面.jpg", size: 380 * 1024 * 1024, path: "封面.jpg", category: "image" }),
  item({ index: 4, name: "说明.pdf", size: 170 * 1024 * 1024, path: "说明.pdf", category: "document" }),
];

describe("Online extraction choices", () => {
  it("every item ticked asks for the whole archive, not the listed members", () => {
    expect(picksOf(items, new Set(items.map((i) => i.path)))).toEqual([]);
  });

  it("a partial pick names files by index and folders by path", () => {
    expect(picksOf(items, new Set(["原片/", "说明.pdf"]))).toEqual([
      { index: 0, folder: true, path: "原片/" },
      { index: 4, folder: false, path: "说明.pdf" },
    ]);
  });

  it("gives a size only when no folder is ticked: folders have none to add", () => {
    expect(selectionSummary(items, new Set(["封面.jpg", "说明.pdf"]))).toBe("已选 2 项 · 550 MB");
    expect(selectionSummary(items, new Set(["原片/", "封面.jpg"]))).toBe("已选 2 项 · 含 1 个文件夹");
    expect(selectionSummary(items, new Set())).toBe("未选择内容");
  });

  it("tells a timeout, an unreadable format and an unknown error apart", () => {
    const timeout = failureOf("timeout");
    const format = failureOf("unsupported");
    const other = failureOf("error", "network down");
    expect(timeout.retry).toBe(true);
    expect(format.retry).toBe(false);
    expect(new Set([timeout.title, format.title, other.title]).size).toBe(3);
    expect(other.detail).toBe("network down");
  });
});
