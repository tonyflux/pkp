import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, type App } from "vue";
import type { DriveStatus, Entry, IndexProgress } from "../bindings/github.com/tonyflux/pkp/internal/services";
import FilesView from "./components/FilesView.vue";
import { drive, folderCovers, ui } from "./store";

let app: App;
let host: HTMLElement;

beforeEach(async () => {
  vi.stubGlobal("IntersectionObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  drive.status = { accountId: "cover-test", configured: true } as DriveStatus;
  drive.indexProgress = { ready: true, phase: "idle", account: "cover-test", syncedAt: "" } as IndexProgress;
  drive.trail = [];
  drive.entries = [{ id: "folder", name: "电影", folder: true, category: "other", size: 0, modifiedTime: "", path: "", starred: false } as Entry];
  ui.route = "files"; ui.view = "all"; ui.scope = "folder"; ui.query = ""; ui.category = "all"; ui.layout = "grid";
  await nextTick();
  host = document.createElement("div");
  document.body.append(host);
  app = createApp(FilesView);
  app.mount(host);
  await nextTick();
});

afterEach(() => {
  app.unmount();
  host.remove();
  folderCovers.clear();
  drive.status = null;
  drive.indexProgress = null;
  drive.entries = [];
  vi.unstubAllGlobals();
});

describe("folder cover thumbnails", () => {
  it("replaces child-folder mosaics with a single placeholder while preserving metadata", async () => {
    const coverBeforeLoading = host.querySelector(".folder-cover")!;
    expect(coverBeforeLoading.querySelector(".folder-placeholder")).not.toBeNull();
    expect(host.querySelector(".folder-text")?.textContent).toContain("电影");

    folderCovers.set("folder", { id: "folder", children: 3, items: [
      { id: "one", name: "子目录一", folder: true, thumbnail: "" },
      { id: "two", name: "子目录二", folder: true, thumbnail: "" },
      { id: "three", name: "说明文档", folder: false, thumbnail: "" },
    ] as Entry[] });
    await nextTick();
    const cover = host.querySelector(".folder-cover")!;
    expect(cover.querySelectorAll(".folder-placeholder")).toHaveLength(1);
    expect(cover.querySelectorAll("img")).toHaveLength(0);
    expect(cover.textContent).not.toContain("子目录");
    expect(host.querySelector(".folder-text")?.textContent).toContain("3 项");
  });

  it("retains real thumbnails in mixed folders and uses a placeholder when they fail", async () => {
    folderCovers.set("folder", { id: "folder", children: 3, items: [
      { id: "nested", name: "子目录", folder: true, thumbnail: "ignored" },
      { id: "image", name: "海报.jpg", folder: false, thumbnail: "https://example.invalid/poster.jpg" },
      { id: "video", name: "视频.mp4", folder: false, thumbnail: "https://example.invalid/video.jpg" },
    ] as Entry[] });
    await nextTick();
    const cover = host.querySelector(".folder-cover")!;
    const images = cover.querySelectorAll<HTMLImageElement>("img");
    expect(Array.from(images, (image) => image.getAttribute("src"))).toEqual(["https://example.invalid/poster.jpg", "https://example.invalid/video.jpg"]);
    expect(cover.querySelectorAll(".cover-tile-placeholder")).toHaveLength(1);
    images[0].dispatchEvent(new Event("error"));
    images[1].dispatchEvent(new Event("error"));
    await nextTick();
    expect(cover.querySelector(".folder-placeholder")).not.toBeNull();
    expect(cover.querySelectorAll("img")).toHaveLength(0);
    expect(cover.querySelectorAll(".cover-tile-placeholder")).toHaveLength(3);
    expect(cover.textContent).not.toContain("子目录");
  });
});
