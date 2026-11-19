import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, type App } from "vue";
import { compileStyle, parse } from "vue/compiler-sfc";
import type { DriveStatus, Entry, IndexProgress } from "../bindings/github.com/tonyflux/pkp/internal/services";
import FilesView from "./components/FilesView.vue";
import filesViewSource from "./components/FilesView.vue?raw";
import { drive, folderCovers, player, select, ui } from "./store";

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
  player.presentation = "off";
  player.owner = "";
  player.playlist = [];
  player.index = -1;
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

  it("keeps both loaded and skeleton thumbnail selectors intact after scoped compilation", () => {
    const { descriptor } = parse(filesViewSource, { filename: "FilesView.vue" });
    const compiled = descriptor.styles.map((style) =>
      compileStyle({ source: style.content, id: "data-v-cover-test", scoped: style.scoped, filename: "FilesView.vue" }),
    );
    for (const result of compiled) expect(result.errors).toEqual([]);
    const rules = compiled
      .flatMap((result) => result.code.split("\n"))
      .filter((line) => line.includes(".folder-cover") && line.includes(":not(:has("));
    expect(rules.length).toBeGreaterThanOrEqual(3);
    for (const rule of rules) {
      expect(rule).toContain(".thumb-img.loaded");
      expect(rule).toContain(".thumb-img.skel");
      expect(rule).not.toContain(".thumb-img.loaded[data-v-cover-test]");
      expect(rule).not.toContain(".thumb-img.skel[data-v-cover-test]");
    }
  });
});

describe("file playing indicator", () => {
  it("does not highlight a colliding file id owned by another account", async () => {
    const entry = { id: "shared-id", name: "片段.mp4", folder: false, category: "video", playable: true, size: 1, modifiedTime: "", path: "", thumbnail: "" } as Entry;
    drive.entries = [entry];
    player.presentation = "bar";
    player.owner = "other-account";
    player.playlist = [entry];
    player.index = 0;
    await nextTick();
    expect(host.querySelector('[data-entry="shared-id"] .playing')).toBeNull();

    player.owner = "cover-test";
    await nextTick();
    expect(host.querySelector('[data-entry="shared-id"] .playing')).not.toBeNull();
  });
});

describe("file card actions", () => {
  it("opens the selected file's menu with Enter instead of starting playback", async () => {
    const entry = { id: "video", name: "很长的示例文件名 - 第一集.mp4", folder: false, category: "video", playable: true, size: 1, modifiedTime: "", path: "", thumbnail: "" } as Entry;
    drive.entries = [entry];
    select(entry);
    await nextTick();
    const card = host.querySelector<HTMLElement>('[data-entry="video"]')!;
    expect(card.title).toBe(entry.name);
    const more = card.querySelector<HTMLButtonElement>('.more')!;
    more.focus();
    const key = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    more.dispatchEvent(key);
    expect(key.defaultPrevented).toBe(false);
    more.click();
    await nextTick();
    expect(document.querySelector('[role="menu"]')).not.toBeNull();
    expect(ui.route).toBe("files");
  });

  it("keeps the recycle-bin confirmation actionable and cancelable", async () => {
    ui.view = "trash";
    drive.entries = [{ id: "trash-item", name: "待删除.mp3", folder: false, category: "audio", size: 1, modifiedTime: "", path: "", thumbnail: "" } as Entry];
    await nextTick();
    host.querySelector<HTMLButtonElement>('[data-entry="trash-item"] .more')!.click();
    await nextTick();
    const entry = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')).find((button) => button.textContent?.includes("彻底删除"));
    expect(entry).toBeDefined();
    entry!.click();
    await nextTick();
    const confirm = document.querySelector<HTMLButtonElement>('.confirm-go')!;
    expect(confirm.textContent).toBe("确认删除");
    expect(confirm.classList.contains("irreversible")).toBe(true);
    const keep = Array.from(document.querySelectorAll<HTMLButtonElement>('.confirm button')).find((button) => button.textContent === "保留")!;
    keep.click();
    await nextTick();
    expect(document.querySelector('.confirm-go')).toBeNull();
    expect(drive.entries.map((item) => item.id)).toEqual(["trash-item"]);
  });
});
