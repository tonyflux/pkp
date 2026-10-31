import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Entry, ListPage } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => {
  const answers = new Map<string, (...args: unknown[]) => unknown>();
  const closed: string[] = [];
  const service = () => new Proxy({}, {
    get: (_target, name: string) => (...args: unknown[]) => {
      if (name === "ClosePreview") closed.push(args[0] as string);
      return Promise.resolve(answers.get(name)?.(...args) ?? null);
    },
  });
  return { answers, closed, service };
});
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: backend.service(), PlayerService: backend.service(),
  AppearanceService: backend.service(), AppLockService: backend.service(),
  MenuBarService: backend.service(), NotifyService: backend.service(), TelemetryService: backend.service(),
}));

import { bootstrapViewerWindow, closeViewer, drive, loadViewer, openViewer, player, popOut, rememberViewerImage, ui, viewer } from "./store";

const entry = (id: string, preview = "image"): Entry => ({
  id, name: `${id}.${preview === "image" ? "png" : "txt"}`, parentId: "folder", preview,
  category: preview === "image" ? "image" : "document", folder: false,
  size: 1, modifiedTime: "2026-01-01T00:00:00Z",
} as Entry);
const page = (entries: Entry[], offset: number, total: number): ListPage => ({ entries, offset, total, live: false, nextPageToken: "" });

beforeEach(() => {
  closeViewer();
  backend.answers.clear();
  backend.closed.length = 0;
  ui.route = "files";
  ui.sortKey = "name";
  ui.sortAsc = true;
  drive.entries = [];
  player.presentation = "off";
  backend.answers.set("PreviewFile", (id: unknown) => `http://preview/${id}`);
  backend.answers.set("ViewerList", () => page([], 0, 0));
});

describe("file consumption", () => {
  it("opens the viewer shell before a slow preview completes, then displays the returned preview", async () => {
    let finish!: (url: string) => void;
    backend.answers.set("ViewerWindow", () => ({
      entry: entry("a"), siblings: [entry("a"), entry("b")], source: "folder", sort: "name", asc: true,
    }));
    backend.answers.set("PreviewFile", () => new Promise<string>((resolve) => { finish = resolve; }));
    await bootstrapViewerWindow();
    expect(ui.route).toBe("viewer");
    expect(viewer.entry?.id).toBe("a");
    expect(viewer.loading).toBe(true);
    expect(viewer.url).toBe("");
    finish("http://preview/a");
    await vi.waitFor(() => expect(viewer.url).toBe("http://preview/a"));
    expect(viewer.loading).toBe(false);
  });

  it("makes files beyond the file-page filter and first page selectable without changing browsing or playback", async () => {
    const a = entry("a"), b = entry("b", "text"), c = entry("c");
    drive.entries = [a];
    player.state.position = 37;
    player.playlist = [entry("song", "")];
    backend.answers.set("ViewerList", (req: unknown) => typeof req === "object" && req !== null && "offset" in req && req.offset === 0 ? page([a, b], 0, 3) : page([c], 2, 3));
    await openViewer(a);
    await vi.waitFor(() => expect(viewer.folderLoading).toBe(false));
    expect(viewer.siblings.map(e => e.id)).toEqual(["a", "b", "c"]);
    await openViewer(c);
    expect(viewer.entry?.id).toBe("c");
    expect(drive.entries.map(e => e.id)).toEqual(["a"]);
    expect(player.state.position).toBe(37);
    expect(player.playlist[0].id).toBe("song");
  });

  it("revokes a late preview without replacing or revoking the current file", async () => {
    let finish!: (url: string) => void;
    backend.answers.set("PreviewFile", () => new Promise<string>(resolve => { finish = resolve; }));
    viewer.entry = entry("old");
    viewer.kind = "image";
    const old = loadViewer();
    backend.answers.set("PreviewFile", () => "http://preview/new");
    viewer.entry = entry("new");
    await loadViewer();
    finish("http://preview/old");
    await old;
    expect(viewer.url).toBe("http://preview/new");
    expect(backend.closed).toContain("http://preview/old");
    expect(backend.closed).not.toContain("http://preview/new");
    expect(viewer.loading).toBe(false);
  });

  it("reuses loaded images across navigation, but refreshes on retry and releases on close", async () => {
    let requests = 0;
    backend.answers.set("PreviewFile", (id) => `http://preview/${id}/${++requests}`);
    await openViewer(entry("a"));
    rememberViewerImage(viewer.url, 640, 480);
    const first = viewer.url;
    await openViewer(entry("b"));
    rememberViewerImage(viewer.url, 800, 600);
    await openViewer(entry("a"));
    expect(viewer.url).toBe(first);
    expect(viewer.natural).toEqual({ w: 640, h: 480 });
    expect(requests).toBe(2);
    expect(backend.closed).not.toContain(first);
    await loadViewer();
    expect(viewer.url).not.toBe(first);
    expect(requests).toBe(3);
    expect(backend.closed).toContain(first);
    rememberViewerImage(viewer.url, 640, 480);
    const last = viewer.url;
    closeViewer();
    expect(viewer.images).toEqual([]);
    expect(backend.closed).toContain(last);
    expect(backend.closed).toContain("http://preview/b/2");
  });

  it("evicts least recently viewed images and invalidates changed file versions", async () => {
    for (let i = 0; i < 8; i++) {
      await openViewer(entry(String(i)));
      rememberViewerImage(viewer.url, 100, 100);
    }
    await openViewer(entry("0"));
    await openViewer(entry("8"));
    rememberViewerImage(viewer.url, 100, 100);
    expect(viewer.images.map((image) => image.id)).toEqual(["2", "3", "4", "5", "6", "7", "0", "8"]);
    expect(backend.closed).toContain("http://preview/1");
    expect(backend.closed).not.toContain("http://preview/0");
    await openViewer({ ...entry("0"), modifiedTime: "2026-10-21T00:00:00Z" });
    expect(backend.closed).toContain("http://preview/0");
    expect(viewer.natural).toBeNull();
    expect(viewer.images.some((image) => image.id === "0")).toBe(false);
  });

  it("bounds retained pixel memory and ignores late load events after closing", async () => {
    await openViewer(entry("a"));
    rememberViewerImage(viewer.url, 5000, 5000);
    await openViewer(entry("b"));
    rememberViewerImage(viewer.url, 5000, 5000);
    expect(viewer.images.map((image) => image.id)).toEqual(["b"]);
    expect(backend.closed).toContain("http://preview/a");
    const late = viewer.url;
    closeViewer();
    rememberViewerImage(late, 5000, 5000);
    expect(viewer.images).toEqual([]);
    expect(viewer.natural).toBeNull();
  });

  it("does not resurrect the folder navigation after the viewer closes", async () => {
    let finish!: (page: ListPage) => void;
    backend.answers.set("ViewerList", () => new Promise<ListPage>(resolve => { finish = resolve; }));
    await openViewer(entry("a"));
    closeViewer();
    finish(page([entry("late")], 0, 1));
    await Promise.resolve();
    await Promise.resolve();
    expect(viewer.entry).toBeNull();
    expect(viewer.siblings).toEqual([]);
    expect(ui.route).toBe("files");
  });

  it("keeps the audio session and position when detached, and restores presentation after an open failure", async () => {
    player.playlist = [{ ...entry("song", ""), category: "audio" }];
    player.index = 0;
    player.state.position = 91;
    player.returnRoute = "files";
    player.presentation = "immersive";
    ui.route = "player";
    await popOut();
    expect(player.presentation).toBe("window");
    expect(ui.route).toBe("files");
    expect(player.state.position).toBe(91);
    expect(player.playlist[0].id).toBe("song");
    player.presentation = "immersive";
    ui.route = "player";
    backend.answers.set("OpenWindow", () => Promise.reject(new Error("window failed")));
    await popOut();
    expect(player.presentation).toBe("immersive");
    expect(ui.route).toBe("player");
    expect(player.state.position).toBe(91);
  });
});
