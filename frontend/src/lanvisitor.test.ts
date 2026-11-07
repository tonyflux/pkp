import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Entry = { id: string; parentId: string; name: string; mime: string; size: number; ready: boolean; compatibleReady: boolean; version?: string; hasThumbnail?: boolean };
type Task = { id: string; fileId: string; name: string; kind: string; state: string; done: number; total: number; spaceId?: string };
const makeEntry = (id: string, parentId = ""): Entry => ({ id, parentId, name: `${id}.mp4`, mime: "video/mp4", size: 1024, ready: true, compatibleReady: false });
const info = (codec = "h264") => ({ mime: "video/mp4", container: "mp4", videoCodec: codec, bitDepth: 8, videoMime: `video/mp4; codecs="${codec === "hevc" ? "hvc1.1.6.L120.B0" : "avc1.640028"}"` });
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const settle = async () => { for (let index = 0; index < 40; index++) await Promise.resolve(); };

let entries: Entry[];
let tasks: Task[];
let spaces: { id: string; name: string; kind: string; allowPrepare: boolean }[];
let dismissed: Set<string>;
let taskHook: (() => Promise<Response> | undefined) | undefined;
let cleanupHook: (() => Promise<Response> | undefined) | undefined;
let fileHook: ((id: string) => Promise<Response> | undefined) | undefined;
let playbackHook: ((id: string) => Promise<Response> | undefined) | undefined;
let filesHook: ((parent: string) => Promise<Response> | undefined) | undefined;
let pageSize = Infinity;
let pageHook: ((token: string) => Promise<Response> | undefined) | undefined;
let thumbnailHook: (() => Promise<Response>) | undefined;
let resizeCallback: ResizeObserverCallback;
let documentListeners: [string, EventListenerOrEventListenerObject, boolean | AddEventListenerOptions | undefined][] = [];
let windowListeners: typeof documentListeners = [];

beforeEach(async () => {
  vi.useFakeTimers();
  vi.resetModules();
  documentListeners = [];
  windowListeners = [];
  entries = [makeEntry("a"), makeEntry("b"), makeEntry("c")];
  tasks = [];
  fileHook = undefined;
  playbackHook = undefined; filesHook = undefined;
  pageSize = Infinity; pageHook = undefined;
  thumbnailHook = undefined;
  vi.stubGlobal("ResizeObserver", class {
    constructor(callback: ResizeObserverCallback) { resizeCallback = callback; }
    observe() {}
    unobserve() {}
    disconnect() {}
  });
  spaces = [{ id: "space", name: "Shared", kind: "local", allowPrepare: true }];
  dismissed = new Set();
  taskHook = undefined;
  cleanupHook = undefined;
  document.body.className = "";
  document.body.innerHTML = '<div id="app"></div><dialog id="panel"></dialog><div id="notice" hidden></div>';
  localStorage.setItem("pkp-lan-language", "en");
  localStorage.removeItem('pkp-lan-install-dismissed');
  vi.stubGlobal("innerWidth", 390);
  history.replaceState(null, "", "/");
  vi.stubGlobal("CSS", { escape: (value: string) => value });
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  vi.spyOn(window, "scrollBy").mockImplementation(() => {});
  const dialog = document.querySelector<HTMLDialogElement>("#panel")!;
  dialog.showModal = function () { this.open = true; };
  dialog.close = function () { this.open = false; };
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockImplementation(mime => mime.includes("hvc1") ? "" : "probably");
  vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (this: HTMLMediaElement) {
    Object.defineProperty(this, "paused", { configurable: true, value: false });
    return Promise.resolve();
  });
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(function (this: HTMLMediaElement) {
    Object.defineProperty(this, "paused", { configurable: true, value: true });
  });
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  vi.stubGlobal("fetch", vi.fn(async (path: string, options: RequestInit = {}) => {
    const url = new URL(path, location.href);
    switch (url.pathname) {
      case "/api/session": return json({ authenticated: true });
      case "/api/state": {
        const space = url.searchParams.get("space");
        if (!space) return json({ spaces });
        const current = spaces.find(item => item.id === space);
        if (!current) return json({ error: "forbidden" }, 403);
        const result: Record<string, unknown> = { space: current };
        if (url.searchParams.get("spaces") === "1") result.spaces = spaces;
        if (url.searchParams.get("files") === "1") {
          const parent = url.searchParams.get("parent") || "";
          const token = url.searchParams.get("page") || "";
          const delayed = pageHook?.(token) || filesHook?.(parent); if (delayed) return delayed;
          const files = entries.filter(entry => entry.parentId === parent), start = Number(token || 0);
          const nextPage = start + pageSize < files.length ? String(start + pageSize) : "";
          result.files = { entries: files.slice(start, start + pageSize), revision: "fresh", nextPage };
        }
        if (url.searchParams.has("tasks")) {
          const response = taskHook?.();
          if (response) return json({ ...result, tasks: await response.then(value => value.json()) });
          result.tasks = tasks.filter(task => (task.spaceId || "space") === space && !dismissed.has(task.id) && (url.searchParams.get("tasks") !== "active" || !["ready", "failed", "cancelled"].includes(task.state)));
        }
        if (url.searchParams.has("id")) {
          const id = url.searchParams.get("id")!;
          const delayed = fileHook?.(id);
          if (delayed) {
            const response = await delayed;
            if (!response.ok) return json({ ...result, selectedError: (await response.json()).error });
            result.selected = await response.json();
          } else result.selected = entries.find(entry => entry.id === id);
        }
        return json(result);
      }
      case "/api/file": {
        const id = url.searchParams.get("id")!;
        const delayed = fileHook?.(id);
        return delayed || json(entries.find(entry => entry.id === id));
      }
      case "/api/playback": {
        const request = JSON.parse(String(options.body));
        const delayed = playbackHook?.(request.fileId); if (delayed) return delayed;
        const entry = entries.find(entry => entry.id === request.fileId)!;
        return json({ entry, variant: "original", ...(entry.ready ? { plan: { kind: entry.compatibleReady ? "video" : "direct", info: info(), warnings: [] } } : {}) });
      }
      case "/api/tasks": return taskHook?.() || json(tasks.filter(task => (task.spaceId || "space") === url.searchParams.get("space") && !dismissed.has(task.id)));
      case "/api/tasks/remove":
      case "/api/tasks/clear": {
        const delayed = cleanupHook?.();
        if (delayed) return delayed;
        const request = JSON.parse(String(options.body));
        const removed = tasks.filter(task => (task.spaceId || "space") === request.spaceId && ["ready", "failed", "cancelled"].includes(task.state) && (!request.taskId || task.id === request.taskId)).map(task => task.id);
        removed.forEach(id => dismissed.add(id));
        return json({ removed });
      }
      case "/api/cancel": {
        const request = JSON.parse(String(options.body));
        const task = tasks.find(task => task.id === request.taskId && (task.spaceId || "space") === request.spaceId);
        if (task) task.state = "cancelled";
        return json({});
      }
      case "/api/prepare": {
        const request = JSON.parse(String(options.body));
        const prepared: Task = { id: "retry-task", fileId: request.fileId, spaceId: request.spaceId, name: entries.find(entry => entry.id === request.fileId)!.name, state: "queued", kind: request.kind, done: 0, total: 1024 };
        tasks = [prepared, ...tasks];
        return json(prepared);
      }
      case "/api/thumbnail": return thumbnailHook?.() || json({ error: "source_unavailable" }, 409);
      case "/api/logout": return json({});
      default: throw new Error(`Unexpected request: ${url.pathname}`);
    }
  }));
  const addDocument = document.addEventListener.bind(document);
  const addWindow = window.addEventListener.bind(window);
  vi.spyOn(document, "addEventListener").mockImplementation((type, listener, options) => { documentListeners.push([type, listener, options]); addDocument(type, listener, options); });
  vi.spyOn(window, "addEventListener").mockImplementation((type, listener, options) => { windowListeners.push([type, listener, options]); addWindow(type, listener, options); });
  // The auto-starting module must see this test's isolated DOM and request boundary.
  await import("../../internal/lanshare/web/app.js");
  await settle();
  (document.querySelector(".space-cards [data-space]") as HTMLButtonElement).click();
  await settle();
});

afterEach(async () => {
  (document.querySelector('#panel [data-action="close"]') as HTMLButtonElement | null)?.click();
  (document.querySelector(".player-back") as HTMLButtonElement | null)?.click();
  await settle();
  for (const [type, listener, options] of documentListeners) document.removeEventListener(type, listener, options);
  for (const [type, listener, options] of windowListeners) window.removeEventListener(type, listener, options);
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.replaceChildren();
});

async function open(id: string) {
  (document.querySelector(`[data-file="${id}"]`) as HTMLButtonElement).click();
  await settle();
}
const selectedMedia = () => document.querySelector<HTMLVideoElement>(".player-media");

describe("LAN dialog transitions", () => {
  it("restores the pre-dialog list position after playing from file details", async () => {
    vi.stubGlobal("scrollY", 235);
    document.querySelector<HTMLButtonElement>('[data-action="entry-details"][data-file-id="a"]')!.click(); await settle();
    expect(document.body.style.top).toBe("-235px");
    vi.stubGlobal("scrollY", 0);
    document.querySelector<HTMLButtonElement>('#panel [data-action="play"]')!.click(); await settle();
    document.querySelector<HTMLButtonElement>('.player-back')!.click(); await settle();
    expect(window.scrollTo).toHaveBeenLastCalledWith(0, 235);
  });
});

describe("LAN visitor image gallery", () => {
  async function images() {
    entries = ["one", "two", "cloud"].map(id => ({ ...makeEntry(id), name: `${id}.jpg`, mime: "image/jpeg", ready: id !== "cloud" }));
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
  }
  it("locks and restores browsing position while navigating only ready siblings", async () => {
    await images();
    vi.stubGlobal("scrollY", 235);
    const origin = document.querySelector<HTMLButtonElement>('[data-file="one"]')!;
    origin.focus(); await open("one");
    expect(document.body.style.position).toBe("fixed");
    expect(document.body.style.top).toBe("-235px");
    expect(document.querySelector(".gallery-count")?.textContent).toBe("1 / 2");
    document.querySelector<HTMLButtonElement>('[data-gallery-action="next"]')!.click(); await settle();
    expect(document.querySelector("#panel img")?.getAttribute("src")).toContain("id=two&");
    expect(document.querySelector<HTMLButtonElement>('[data-gallery-action="next"]')!.disabled).toBe(true);
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => String(url).startsWith("/api/file?") && String(url).includes("id=cloud"))).toHaveLength(0);
    document.querySelector<HTMLButtonElement>('#panel [data-action="close"]')!.click(); await settle();
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");
    expect(window.scrollTo).toHaveBeenLastCalledWith(0, 235);
    expect(document.activeElement).toBe(origin);
  });
  it("revalidates a neighboring image and does not display it after access is revoked", async () => {
    await images(); await open("one");
    fileHook = id => id === "two" ? Promise.resolve(json({ error: "forbidden" }, 403)) : undefined;
    document.querySelector<HTMLButtonElement>('[data-gallery-action="next"]')!.click(); await settle();
    expect(document.querySelector("#panel img")?.getAttribute("src")).toContain("id=one&");
    expect(document.querySelector(".gallery-status")?.getAttribute("role")).toBe("alert");
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/prepare")).toHaveLength(0);
  });
  it("closes and unlocks when the active image loses authorization", async () => {
    await images(); await open("one");
    fileHook = id => id === "one" ? Promise.resolve(json({ error: "forbidden" }, 403)) : undefined;
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(document.querySelector<HTMLDialogElement>("#panel")?.open).toBe(false);
    expect(document.querySelector("#panel img")).toBeNull();
    expect(document.body.style.position).toBe("");
  });
});

describe("LAN visitor space navigation", () => {
  it("keeps the current space visible and switches space or returns home", async () => {
    spaces.push({ id: "second", name: "Second space", kind: "cloud", allowPrepare: true });
    await vi.advanceTimersByTimeAsync(30000); await settle();
    const face = () => document.querySelector<HTMLButtonElement>(".mobile-space-select .select-face")!;
    expect(face().textContent).toContain("Shared");
    face().click();
    document.querySelector<HTMLButtonElement>('[data-select-option="second"]')!.click();
    await settle();
    expect(face().textContent).toContain("Second space");
    expect(document.querySelector(".space-heading h1")?.textContent).toBe("Second space");
    face().click();
    document.querySelector<HTMLButtonElement>('[data-select-option=""]')!.click();
    await settle();
    expect(face().textContent).toContain("Shared spaces");
    expect(document.querySelectorAll(".space-cards [data-space]")).toHaveLength(2);
  });
});

describe("LAN visitor resize anchoring", () => {
  it("preserves the visible row and keyboard focus when changing viewport width", async () => {
    const row = document.querySelector<HTMLElement>('.file-row[data-virtual-id="a"]')!;
    const face = row.querySelector<HTMLElement>(".file-main")!;
    const list = document.getElementById("file-list")!;
    let top = -20;
    const rectangle = (y: number, height: number, width: number) => ({ top: y, bottom: y + height, left: 0, right: width, width, height, x: 0, y, toJSON: () => ({}) });
    vi.spyOn(row, "getBoundingClientRect").mockImplementation(() => rectangle(top, 80, 320));
    vi.spyOn(list, "getBoundingClientRect").mockImplementation(() => rectangle(top, 1000, 320));
    window.dispatchEvent(new Event("scroll"));
    await vi.advanceTimersByTimeAsync(20);
    face.focus();
    top = 22;
    vi.stubGlobal("innerWidth", 320);
    vi.mocked(window.scrollBy).mockImplementation((_x, y) => { top -= Number(y); });
    window.dispatchEvent(new Event("resize"));
    expect(top).toBe(-20);
    expect(document.activeElement).toBe(face);
  });
});
describe("LAN visitor native scroll preservation", () => {
  it("does not pull a native scroll back when only the viewport height changes", async () => {
    const row = document.querySelector<HTMLElement>('.file-row[data-virtual-id="a"]')!;
    const list = document.getElementById("file-list")!;
    let top = -20;
    const rectangle = (y: number, height: number, width: number) => ({ top: y, bottom: y + height, left: 0, right: width, width, height, x: 0, y, toJSON: () => ({}) });
    vi.stubGlobal("scrollY", 200);
    vi.spyOn(row, "getBoundingClientRect").mockImplementation(() => rectangle(top, 80, 320));
    vi.spyOn(list, "getBoundingClientRect").mockImplementation(() => rectangle(top, 1000, 320));
    window.dispatchEvent(new Event("scroll")); await vi.advanceTimersByTimeAsync(20);
    vi.stubGlobal("scrollY", 300); top = -120;
    vi.stubGlobal("innerHeight", 900);
    vi.mocked(window.scrollBy).mockImplementation((_x, y) => { top -= Number(y); });
    window.dispatchEvent(new Event("resize"));
    expect(window.scrollBy).not.toHaveBeenCalled();
    expect(top).toBe(-120);
  });
  it("does not undo momentum between measuring a row and receiving its size", async () => {
    const row = document.querySelector<HTMLElement>('.file-row[data-virtual-id="a"]')!;
    const list = document.getElementById("file-list")!;
    let top = -20;
    const rectangle = (y: number, height: number) => ({ top: y, bottom: y + height, left: 0, right: 320, width: 320, height, x: 0, y, toJSON: () => ({}) });
    vi.stubGlobal("scrollY", 200);
    vi.spyOn(row, "getBoundingClientRect").mockImplementation(() => rectangle(top, 100));
    vi.spyOn(list, "getBoundingClientRect").mockImplementation(() => rectangle(top, 1000));
    window.dispatchEvent(new Event("scroll")); await vi.advanceTimersByTimeAsync(20);
    vi.stubGlobal("scrollY", 300); top = -120;
    vi.mocked(window.scrollBy).mockImplementation((_x, y) => { top -= Number(y); });
    resizeCallback([{ target: row, borderBoxSize: [{ blockSize: 100 }] } as unknown as ResizeObserverEntry], {} as ResizeObserver);
    expect(window.scrollBy).not.toHaveBeenCalled();
    expect(top).toBe(-120);
  });
});

describe("LAN visitor bounded image previews", () => {
  it("queues small thumbnails two at a time and keeps original reads explicit", async () => {
    const finish: ((response: Response) => void)[] = [];
    thumbnailHook = () => new Promise(resolve => finish.push(resolve));
    vi.stubGlobal("URL", class extends URL {
      static createObjectURL = vi.fn(() => "blob:thumbnail");
      static revokeObjectURL = vi.fn();
    });
    entries = ["a", "b", "c"].map(id => ({ ...makeEntry(id), name: `${id}.jpg`, mime: "image/jpeg", version: "v1" }));
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
    expect(finish).toHaveLength(2);
    finish[0](new Response(new Blob(["jpeg"], { type: "image/jpeg" }), { headers: { "Content-Type": "image/jpeg" } })); await settle();
    expect(finish).toHaveLength(3);
    expect(document.querySelector<HTMLImageElement>('[data-virtual-id="a"] [data-thumbnail]')?.getAttribute("src")).toBe("blob:thumbnail");
    const urls = vi.mocked(fetch).mock.calls.map(([url]) => String(url));
    expect(urls.filter(url => url.startsWith("/api/thumbnail"))).toHaveLength(3);
    expect(urls.some(url => url.startsWith("/api/content"))).toBe(false);
    document.querySelector<HTMLButtonElement>('[data-action="home"]')!.click(); await settle();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:thumbnail");
    finish[1](new Response(new Blob(["old"], { type: "image/jpeg" }), { headers: { "Content-Type": "image/jpeg" } })); await settle();
    expect(document.querySelector(".space-cards")).not.toBeNull();
    expect(document.querySelector('[data-thumbnail]')).toBeNull();
  });

  it("shows cloud video covers without preparing originals and retains icons on failed image decode", async () => {
    const createObjectURL = vi.fn(() => "blob:video-cover");
    vi.stubGlobal("URL", class extends URL { static createObjectURL = createObjectURL; static revokeObjectURL = vi.fn(); });
    entries = [
      { ...makeEntry("cover"), ready: false, hasThumbnail: true, version: "v1" },
      { ...makeEntry("no-cover"), ready: false, version: "v1" }
    ];
    thumbnailHook = () => Promise.resolve(new Response(new Blob(["jpeg"], { type: "image/jpeg" }), { headers: { "Content-Type": "image/jpeg" } }));
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
    const image = document.querySelector<HTMLImageElement>('[data-virtual-id="cover"] [data-thumbnail]')!;
    expect(image.getAttribute("src")).toBe("blob:video-cover");
    expect(document.querySelector('[data-virtual-id="no-cover"] [data-thumbnail]')).toBeNull();
    image.dispatchEvent(new Event("load"));
    expect(image.hidden).toBe(false);
    expect(image.nextElementSibling?.hasAttribute("hidden")).toBe(true);
    image.dispatchEvent(new Event("error"));
    expect(image.hidden).toBe(true);
    expect(image.nextElementSibling?.hasAttribute("hidden")).toBe(false);
    const urls = vi.mocked(fetch).mock.calls.map(([url]) => String(url));
    expect(urls.filter(url => url.startsWith("/api/thumbnail"))).toEqual([expect.stringContaining("id=cover&")]);
    expect(urls.some(url => url.startsWith("/api/content") || url === "/api/prepare")).toBe(false);
  });

  it("loads a newly available cover even if the original thumbnail previously failed", async () => {
    vi.stubGlobal("URL", class extends URL { static createObjectURL = vi.fn(() => "blob:new-cover"); static revokeObjectURL = vi.fn(); });
    entries = [{ ...makeEntry("image"), name: "image.jpg", mime: "image/jpeg", version: "v1" }];
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
    thumbnailHook = () => Promise.resolve(new Response(new Blob(["jpeg"], { type: "image/jpeg" }), { headers: { "Content-Type": "image/jpeg" } }));
    entries[0].hasThumbnail = true;
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
    expect(document.querySelector<HTMLImageElement>('[data-virtual-id="image"] [data-thumbnail]')?.getAttribute("src")).toBe("blob:new-cover");
  });
});

describe("LAN visitor virtual list", () => {
  it("reaches the last entry and filters without mounting the full library", async () => {
    entries = Array.from({ length: 5000 }, (_, index) => makeEntry(`file-${String(index).padStart(4, "0")}`));
    await vi.advanceTimersByTimeAsync(30000); await settle();
    const list = document.querySelector<HTMLElement>("#file-list")!;
    expect(list.querySelectorAll(".file-row").length).toBeLessThan(100);
    vi.spyOn(list, "getBoundingClientRect").mockReturnValue({ top: -399000, bottom: 1000, left: 0, right: 500, width: 500, height: 400000, x: 0, y: -399000, toJSON: () => ({}) });
    window.dispatchEvent(new Event("scroll"));
    await vi.advanceTimersByTimeAsync(20);
    expect(list.querySelector('[data-file="file-4999"]')).not.toBeNull();
    const search = document.querySelector<HTMLInputElement>("#search")!;
    search.value = "file-4999"; search.dispatchEvent(new Event("input", { bubbles: true }));
    expect(list.querySelectorAll(".file-row")).toHaveLength(1);
    expect(list.querySelector('[data-file="file-4999"]')).not.toBeNull();
  });

  it("keeps a focused offscreen row mounted while recycling other rows", async () => {
    entries = Array.from({ length: 10000 }, (_, index) => makeEntry(`file-${String(index).padStart(5, "0")}`));
    await vi.advanceTimersByTimeAsync(30000); await settle();
    const list = document.querySelector<HTMLElement>("#file-list")!;
    const focused = list.querySelector<HTMLButtonElement>('[data-file="file-00000"]')!;
    focused.focus();
    vi.spyOn(list, "getBoundingClientRect").mockReturnValue({ top: -799500, bottom: 500, left: 0, right: 500, width: 500, height: 800000, x: 0, y: -799500, toJSON: () => ({}) });
    window.dispatchEvent(new Event("scroll"));
    await vi.advanceTimersByTimeAsync(20);
    expect(document.activeElement).toBe(focused);
    expect(focused.isConnected).toBe(true);
    expect(list.querySelector('[data-file="file-09999"]')).not.toBeNull();
    expect(list.querySelectorAll(".file-row").length).toBeLessThan(100);
  });

  it("refreshes cached rows when readiness, task state and language change", async () => {
    const row = () => document.querySelector<HTMLElement>('.file-row[data-virtual-id="a"]')!;
    entries[0].ready = false;
    tasks = [{ id: "one", fileId: "a", name: "a.mp4", kind: "original", state: "downloading", done: 1, total: 10 }];
    await vi.advanceTimersByTimeAsync(30000); await settle();
    expect(row().textContent).toContain("View progress");
    tasks = [];
    entries[0].compatibleReady = true;
    await vi.advanceTimersByTimeAsync(30000); await settle();
    expect(row().textContent).toContain("Compatible copy");
    expect(row().textContent).not.toContain("View progress");
    document.querySelector<HTMLButtonElement>(".language-select .select-face")!.click();
    document.querySelector<HTMLButtonElement>('.language-select [data-select-option="ja"]')!.click();
    await settle();
    expect(row().textContent).toContain("互換コピー");
    expect(row().textContent).not.toContain("Compatible copy");
  });

  it("updates progress without replacing unrelated rows or task cards", async () => {
    tasks = [
      { id: "one", fileId: "a", name: "a.mp4", kind: "original", state: "downloading", done: 1, total: 10 },
      { id: "two", fileId: "b", name: "b.mp4", kind: "original", state: "downloading", done: 1, total: 10 }
    ];
    await vi.advanceTimersByTimeAsync(5000); await settle();
    const unrelated = document.querySelector('[data-file="b"]');
    const target = document.querySelector('[data-file="a"]');
    (unrelated as HTMLElement).focus();
    tasks[0].done = 5;
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(document.querySelector('[data-file="b"]')).toBe(unrelated);
    expect(document.querySelector('[data-file="a"]')).toBe(target);
    expect(document.activeElement).toBe(unrelated);
    expect(document.querySelector("#preparation-summary")?.textContent).toContain("50%");
    (document.querySelector('[data-action="tasks"]') as HTMLButtonElement).click(); await settle();
    const card = document.querySelector('[data-task="two"]');
    tasks[0].done = 7;
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(document.querySelector('[data-task="two"]')).toBe(card);
    expect(document.querySelector('[data-task="one"]')?.textContent).toContain("70%");
  });
  it("does not round incomplete byte progress up to 100%", async () => {
    tasks = [{ id: "one", fileId: "a", name: "a.mp4", kind: "original", state: "downloading", done: 999, total: 1000 }];
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(document.querySelector("#preparation-summary")?.textContent).toContain("99%");
    tasks[0].done = 1000;
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(document.querySelector("#preparation-summary")?.textContent).toContain("100%");
  });

  it("preserves progress and focused controls across polls on every task surface", async () => {
    entries[0].ready = false;
    tasks = [{ id: "one", fileId: "a", name: "a.mp4", kind: "original", state: "downloading", done: 1, total: 10 }];
    const poll = async () => { await vi.advanceTimersByTimeAsync(5000); await settle(); };
    const bar = (container: string) => document.querySelector<HTMLElement>(`${container} [role="progressbar"]`)!;
    const update = async (container: string, focus: HTMLElement) => {
      const progress = bar(container), fill = progress.firstElementChild;
      focus.focus();
      tasks[0].done++;
      await poll();
      expect(bar(container)).toBe(progress);
      expect(progress.firstElementChild).toBe(fill);
      expect(progress.getAttribute("aria-valuenow")).toBe(String(tasks[0].done * 10));
      expect(document.querySelector(container)?.textContent).toContain(`${tasks[0].done * 10}%`);
      expect(document.activeElement).toBe(focus);
    };
    await poll();
    await update("#preparation-summary", document.querySelector<HTMLElement>(".preparation-summary")!);
    document.querySelector<HTMLButtonElement>('[data-action="tasks"]')!.click(); await settle();
    await update("#task-list", document.querySelector<HTMLElement>('[data-task="one"] [data-action="cancel-task"]')!);
    document.querySelector<HTMLButtonElement>('[data-task="one"] [data-action="task-open"]')!.click(); await settle();
    await update("#preparation-status", document.querySelector<HTMLElement>('[data-action="preparation-tasks"]')!);
    tasks[0].state = "held"; await poll();
    expect(bar("#preparation-status").getAttribute("aria-valuenow")).toBe("40");
    expect(document.querySelector("#preparation-status")?.textContent).toContain("40%");
    tasks[0].state = "failed"; await poll();
    expect(document.querySelector('#preparation-status [role="progressbar"]')).toBeNull();
  });

  it("resets progress on task and stage changes and hides unknown or completed values", async () => {
    tasks = [{ id: "one", fileId: "a", name: "a.mp4", kind: "original", state: "downloading", done: 2, total: 10 }];
    const poll = async () => { await vi.advanceTimersByTimeAsync(5000); await settle(); };
    const bar = () => document.querySelector<HTMLElement>('#preparation-summary [role="progressbar"]');
    await poll();
    const first = bar();
    tasks[0].state = "converting"; tasks[0].done = 1; await poll();
    expect(bar()).not.toBe(first);
    expect(bar()?.getAttribute("aria-valuenow")).toBe("10");
    const stage = bar();
    tasks[0].id = "two"; await poll();
    expect(bar()).not.toBe(stage);
    tasks[0].total = 0; await poll();
    expect(document.querySelector("#preparation-summary")?.textContent).not.toContain("%");
    expect(bar()).toBeNull();
    tasks[0].state = "ready"; await poll();
    expect(bar()).toBeNull();
  });

  it("keeps the file details task bar and button focused during progress updates", async () => {
    entries[0].ready = false;
    tasks = [{ id: "one", fileId: "a", name: "a.mp4", kind: "original", state: "downloading", done: 1, total: 10 }];
    await vi.advanceTimersByTimeAsync(5000); await settle();
    document.querySelector<HTMLButtonElement>('[data-action="entry-details"][data-file-id="a"]')!.click(); await settle();
    const progress = document.querySelector<HTMLElement>('#panel [role="progressbar"]')!;
    const fill = progress.firstElementChild;
    const button = document.querySelector<HTMLElement>('#panel [data-action="cancel-task"]')!;
    button.focus();
    tasks[0].done = 6;
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(document.querySelector('#panel [role="progressbar"]')).toBe(progress);
    expect(progress.firstElementChild).toBe(fill);
    expect(progress.getAttribute("aria-valuenow")).toBe("60");
    expect(document.querySelector("#panel")?.textContent).toContain("60%");
    expect(document.activeElement).toBe(button);
  });

  it("updates actions when preparation permission is revoked", async () => {
    entries[0].ready = false;
    await vi.advanceTimersByTimeAsync(30000); await settle();
    const action = () => document.querySelector<HTMLButtonElement>('[data-action="entry-primary"][data-file-id="a"]')!;
    expect(action().disabled).toBe(false);
    spaces[0].allowPrepare = false;
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(action().disabled).toBe(true);
    spaces[0].allowPrepare = true;
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(action().disabled).toBe(false);
  });
});

describe("LAN visitor slow operations", () => {
  it("shows pending verification, blocks duplicate playback and releases cancellation for the next file", async () => {
    let finish!: (response: Response) => void;
    const delayed = new Promise<Response>(resolve => { finish = resolve; });
    playbackHook = id => id === "a" ? delayed : undefined;
    const opener = document.querySelector<HTMLButtonElement>('[data-action="entry-primary"][data-file-id="a"]')!;
    opener.click(); await settle();
    expect(document.querySelector<HTMLElement>("#operation-status")?.hidden).toBe(false);
    expect(opener.getAttribute("aria-busy")).toBe("true");
    document.querySelector<HTMLButtonElement>('[data-file="a"]')!.click();
    await settle();
    expect(vi.mocked(fetch).mock.calls.filter(([path]) => path === "/api/playback")).toHaveLength(1);
    document.querySelector<HTMLButtonElement>('[data-action="cancel-operation"]')!.click();
    expect(opener.disabled).toBe(false);
    expect(document.querySelector<HTMLElement>("#operation-status")?.hidden).toBe(true);
    await open("b");
    expect(selectedMedia()?.getAttribute("src")).toContain("id=b&");
    finish(json(entries[0])); await settle();
    expect(selectedMedia()?.getAttribute("src")).toContain("id=b&");
    expect(document.querySelector<HTMLElement>("#operation-status")?.hidden).toBe(true);
  });

  it("shows failed verification without leaving playback locked and retries using fresh permissions", async () => {
    playbackHook = () => Promise.resolve(json({ error: "source_unavailable" }, 409));
    await open("a");
    expect(selectedMedia()).toBeNull();
    expect(document.querySelector<HTMLElement>("#operation-status")?.hidden).toBe(true);
    expect(document.querySelector<HTMLElement>("#notice")?.hidden).toBe(false);
    expect(document.querySelector<HTMLButtonElement>('[data-file="a"]')?.disabled).toBe(false);
    playbackHook = undefined;
    await open("a");
    expect(selectedMedia()?.getAttribute("src")).toContain("id=a&");
  });

  it("restores focus to the details action after its verification completes", async () => {
    const opener = document.querySelector<HTMLButtonElement>('[data-action="entry-details"][data-file-id="a"]')!;
    opener.focus(); opener.click(); await settle();
    document.querySelector<HTMLButtonElement>('[data-action="close"]')!.click();
    expect(document.activeElement).toBe(document.querySelector('[data-action="entry-details"][data-file-id="a"]'));
  });
  it("navigates away from a slow folder without allowing its late response to overwrite the new folder", async () => {
    const folder = (id: string) => ({ ...makeEntry(id), isDir: true, mime: "" });
    entries = [folder("slow"), folder("fast"), makeEntry("fresh", "fast")];
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
    let finish!: (response: Response) => void;
    filesHook = parent => parent === "slow" ? new Promise<Response>(resolve => { finish = resolve; }) : undefined;
    await open("slow");
    document.querySelector<HTMLButtonElement>('[data-path="0"]')!.click(); await settle();
    await open("fast");
    expect(document.querySelector('[data-file="fresh"]')).not.toBeNull();
    finish(json({ space: spaces[0], files: { entries: [makeEntry("stale", "slow")], revision: "old" } })); await settle();
    expect(document.querySelector('[data-file="stale"]')).toBeNull();
    expect(document.querySelector('[data-file="fresh"]')).not.toBeNull();
  });

  it("renders the first page immediately and searches later pages without claiming a premature no-match", async () => {
    entries = [makeEntry("first"), makeEntry("second"), makeEntry("target")]; pageSize = 1;
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
    expect(document.querySelector('[data-file="first"]')).not.toBeNull();
    expect(document.querySelector('[data-file="target"]')).toBeNull();
    let finish!: (response: Response) => void;
    pageHook = token => token === "1" ? new Promise<Response>(resolve => { finish = resolve; }) : undefined;
    const search = document.querySelector<HTMLInputElement>("#search")!;
    search.value = "target"; search.dispatchEvent(new Event("input", { bubbles: true })); await settle();
    expect(document.querySelector(".empty")?.textContent).not.toContain("No matching files");
    expect(document.querySelector("#file-paging [role=status]")).not.toBeNull();
    pageHook = undefined;
    finish(json({ space: spaces[0], files: { entries: [entries[1]], revision: "second", nextPage: "2" } })); await settle();
    expect(document.querySelector('[data-file="target"]')).not.toBeNull();
    expect(document.querySelector('[data-file="first"]')).toBeNull();
    expect(document.querySelector<HTMLElement>("#file-paging")?.hidden).toBe(true);
  });

  it("ignores a later page after leaving its folder", async () => {
    entries = [makeEntry("first"), makeEntry("second")]; pageSize = 1;
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
    let finish!: (response: Response) => void;
    pageHook = token => token ? new Promise<Response>(resolve => { finish = resolve; }) : undefined;
    document.querySelector<HTMLButtonElement>('[data-action="load-more"]')!.click(); await settle();
    document.querySelector<HTMLButtonElement>('[data-action="home"]')!.click(); await settle();
    finish(json({ space: spaces[0], files: { entries: [makeEntry("stale")], revision: "old" } })); await settle();
    expect(document.querySelector(".space-cards")).not.toBeNull();
    expect(document.querySelector('[data-file="stale"]')).toBeNull();
  });
  it("resumes the remaining search after retrying a failed page", async () => {
    entries = [makeEntry("first"), makeEntry("second"), makeEntry("target")]; pageSize = 1;
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
    pageHook = token => token === "1" ? Promise.resolve(json({ error: "offline" }, 503)) : undefined;
    const search = document.querySelector<HTMLInputElement>("#search")!;
    search.value = "target"; search.dispatchEvent(new Event("input", { bubbles: true })); await settle();
    expect(document.querySelector('#file-paging [role="alert"]')).not.toBeNull();
    pageHook = undefined;
    document.querySelector<HTMLButtonElement>('[data-action="load-more"]')!.click(); await settle();
    expect(document.querySelector('[data-file="target"]')).not.toBeNull();
    expect(document.querySelector<HTMLElement>("#file-paging")?.hidden).toBe(true);
  });

  it("revalidates the visible later page without discarding earlier entries", async () => {
    entries = [makeEntry("first"), makeEntry("second"), makeEntry("third")]; pageSize = 1;
    document.querySelector<HTMLButtonElement>('[data-action="refresh"]')!.click(); await settle();
    document.querySelector<HTMLButtonElement>('[data-action="load-more"]')!.click(); await settle();
    const second = document.querySelector<HTMLElement>('.file-row[data-virtual-id="second"]')!;
    vi.spyOn(second, "getBoundingClientRect").mockReturnValue({ top: 20, bottom: 100, left: 0, right: 300, width: 300, height: 80, x: 0, y: 20, toJSON: () => ({}) });
    entries[1].ready = false;
    await vi.advanceTimersByTimeAsync(30000); await settle();
    expect(document.querySelector('[data-file="first"]')).not.toBeNull();
    expect(document.querySelector('[data-action="entry-primary"][data-file-id="second"]')?.textContent).toContain("Prepare");
    expect(document.querySelector('[data-file="third"]')).toBeNull();
  });
});

describe("LAN visitor lossless startup preparation", () => {

  it.each([true, false])("plays supported originals immediately when preparation permission is %s", async allowPrepare => {
    spaces[0].allowPrepare = allowPrepare;
    await vi.advanceTimersByTimeAsync(5000); await settle();
    playbackHook = () => Promise.resolve(json({ entry: entries[0], variant: "original", plan: { kind: "direct", canOptimizeStartup: true, info: { ...info(), mp4IndexBytes: 6653294 }, warnings: [] } }));
    await open("a");
    expect(document.querySelector<HTMLDialogElement>("#panel")?.open).toBe(false);
    expect(selectedMedia()?.getAttribute("src")).toContain("variant=original");
    document.querySelector<HTMLButtonElement>('.player-more')!.click();
    expect(!!document.querySelector('[data-player-action="optimize-startup"]')).toBe(allowPrepare);
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/prepare")).toHaveLength(0);
  });

  it("keeps the original player when optional optimization is dismissed", async () => {
    const originalFetch = vi.mocked(fetch).getMockImplementation()!;
    vi.mocked(fetch).mockImplementation(async (path, options = {}) => {
      if (String(path) !== "/api/playback") return originalFetch(path, options);
      const request = JSON.parse(String(options.body));
      return json({ entry: entries[0], variant: "original", plan: { kind: request.optimizeStartup ? "remux" : "direct", reason: request.optimizeStartup ? "startup_index" : "compatible", canOptimizeStartup: !request.optimizeStartup, info: info(), warnings: [] } });
    });
    await open("a");
    const media = selectedMedia();
    document.querySelector<HTMLButtonElement>('.player-more')!.click();
    document.querySelector<HTMLButtonElement>('[data-player-action="optimize-startup"]')!.click(); await settle();
    expect(document.querySelector<HTMLDialogElement>("#panel")?.open).toBe(true);
    document.querySelector<HTMLButtonElement>('[data-action="close"].secondary')!.click(); await settle();
    expect(selectedMedia()).toBe(media);
    expect(selectedMedia()?.getAttribute("src")).toContain("variant=original");
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/prepare")).toHaveLength(0);
  });

  it("prepares only on explicit menu choice and confirmation, then manually plays the copy", async () => {
    const originalFetch = vi.mocked(fetch).getMockImplementation()!;
    vi.mocked(fetch).mockImplementation(async (path, options = {}) => {
      if (String(path) !== "/api/playback") return originalFetch(path, options);
      const request = JSON.parse(String(options.body));
      const compatible = request.variant === "compatible" && entries[0].compatibleReady;
      return json({ entry: entries[0], variant: compatible ? "compatible" : "original", plan: { kind: request.optimizeStartup ? "remux" : "direct", reason: request.optimizeStartup ? "startup_index" : "compatible", canOptimizeStartup: !compatible && !request.optimizeStartup, info: info(), warnings: [] } });
    });
    await open("a");
    document.querySelector<HTMLButtonElement>('.player-more')!.click();
    document.querySelector<HTMLButtonElement>('[data-player-action="optimize-startup"]')!.click(); await settle();
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/prepare")).toHaveLength(0);
    document.querySelector<HTMLButtonElement>('[data-action="confirm-prepare"]')!.click(); await settle();
    const preparation = vi.mocked(fetch).mock.calls.find(([url]) => url === "/api/prepare")!;
    expect(JSON.parse(String(preparation[1]?.body))).toMatchObject({ spaceId: "space", fileId: "a", kind: "compatible", optimizeStartup: true });
    expect(document.querySelector<HTMLButtonElement>('[data-action="preparation-play"]')?.disabled).toBe(true);
    expect(selectedMedia()).toBeNull();
    tasks[0].state = "ready"; entries[0].compatibleReady = true;
    await vi.advanceTimersByTimeAsync(5000); await settle();
    expect(document.querySelector<HTMLButtonElement>('[data-action="preparation-play"]')?.disabled).toBe(false);
    expect(selectedMedia()).toBeNull();
    document.querySelector<HTMLButtonElement>('[data-action="preparation-play"]')!.click(); await settle();
    expect(selectedMedia()?.getAttribute("src")).toContain("variant=compatible");
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/prepare")).toHaveLength(1);
  });

  it.each(["video", "audio", "remux"])("still confirms required %s compatibility preparation", async kind => {
    const reason = kind === "remux" ? "container_unsupported" : `${kind}_unsupported`;
    playbackHook = () => Promise.resolve(json({ entry: entries[0], variant: "original", plan: { kind, reason, info: info(), warnings: [] } }));
    await open("a");
    expect(document.querySelector('[data-action="confirm-prepare"]')).not.toBeNull();
    expect(selectedMedia()).toBeNull();
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/prepare")).toHaveLength(0);
  });
});

describe("LAN visitor navigation and readiness", () => {
  it("changes and remembers language without losing the current space", async () => {
    document.querySelector<HTMLButtonElement>(".language-select .select-face")!.click();
    document.querySelector<HTMLButtonElement>('.language-select [data-select-option="ja"]')!.click();
    expect(document.documentElement.lang).toBe("ja");
    expect(localStorage.getItem("pkp-lan-language")).toBe("ja");
    expect(document.querySelector('[data-file="a"]')).not.toBeNull();
    expect(document.activeElement).toBe(document.querySelector(".language-select .select-face"));
  });
  it("offers an original conversion plan when another browser's cached copy is unsupported", async () => {
    entries[0].compatibleReady = true;
    await open("a");
    expect(document.querySelector<HTMLDialogElement>("#panel")?.open).toBe(true);
    expect(document.querySelector('[data-action="confirm-prepare"]')).not.toBeNull();
    expect(selectedMedia()).toBeNull();
  });
  it("shows an existing full conversion plan without repeating an irrelevant source check", async () => {
    playbackHook = () => Promise.resolve(json({ entry: entries[0], variant: "original", plan: { kind: "video", copyAudio: false, info: info("hevc"), warnings: [] } }));
    await open("a");
    expect(document.querySelector('[data-action="confirm-prepare"]')).not.toBeNull();
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/playback")).toHaveLength(1);
  });
  it("replans a direct source when its exact codec is rejected", async () => {
    let calls = 0;
    playbackHook = () => Promise.resolve(json({ entry: entries[0], variant: "original", plan: { kind: ++calls === 1 ? "direct" : "video", info: info("hevc"), warnings: [] } }));
    await open("a");
    expect(calls).toBe(2);
    expect(document.querySelector('[data-action="confirm-prepare"]')).not.toBeNull();
    expect(selectedMedia()).toBeNull();
  });

  it("ignores a rejected revalidation from a player that has already been replaced", async () => {
    await open("a");
    let finish!: (response: Response) => void;
    const delayed = new Promise<Response>(resolve => { finish = resolve; });
    fileHook = id => id === "a" ? delayed : undefined;
    const old = selectedMedia()!;
    Object.defineProperty(old, "error", { configurable: true, value: { code: 2 } });
    old.dispatchEvent(new Event("error"));
    await settle();
    (document.querySelector(".player-playlist-toggle") as HTMLButtonElement).click();
    (document.querySelector('[data-player-select="b"]') as HTMLButtonElement).click();
    await settle();
    expect(selectedMedia()?.getAttribute("src")).toContain("id=b&");
    finish(json({ error: "source_unavailable" }, 409));
    await settle();
    expect(selectedMedia()?.getAttribute("src")).toContain("id=b&");
  });

  it("releases an abandoned verification without letting it overwrite the next player", async () => {
    await open("a");
    let finish!: (response: Response) => void;
    const delayed = new Promise<Response>(resolve => { finish = resolve; });
    playbackHook = id => id === "b" ? delayed : undefined;
    (document.querySelector(".player-playlist-toggle") as HTMLButtonElement).click();
    (document.querySelector('[data-player-select="b"]') as HTMLButtonElement).click();
    await settle();
    window.dispatchEvent(new PopStateEvent("popstate"));
    await open("c");
    expect(selectedMedia()?.getAttribute("src")).toContain("id=c&");
    finish(json(entries[1]));
    await settle();
    expect(selectedMedia()?.getAttribute("src")).toContain("id=c&");
  });


  it("exits custom fullscreen on browser back without replacing the active media", async () => {
    await open("a");
    const media = selectedMedia();
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    expect(document.querySelector('.player-page')?.classList.contains('is-fullscreen')).toBe(true);
    window.dispatchEvent(new PopStateEvent('popstate')); await settle();
    expect(selectedMedia()).toBe(media);
    expect(document.querySelector('.player-page')?.classList.contains('is-fullscreen')).toBe(false);
    expect(document.body.style.overflow).toBe('');
  });

  it("still revokes playback after opening and closing playback information", async () => {
    await open("a");
    (document.querySelector(".player-more") as HTMLButtonElement).click();
    (document.querySelector('[data-player-action="details"]') as HTMLButtonElement).click();
    await settle();
    (document.querySelector('[data-action="close"]') as HTMLButtonElement).click();
    fileHook = id => id === "a" ? Promise.resolve(json({ error: "forbidden" }, 403)) : undefined;
    const media = selectedMedia()!;
    Object.defineProperty(media, "error", { configurable: true, value: { code: 2 } });
    media.dispatchEvent(new Event("error"));
    await settle();
    expect(selectedMedia()).toBeNull();
    expect(document.querySelector<HTMLDivElement>("#library-view")?.hidden).toBe(false);
  });

  it("restores keyboard focus to the replacement file row after returning", async () => {
    await open("a");
    (document.querySelector(".player-back") as HTMLButtonElement).click();
    expect((document.activeElement as HTMLElement).dataset.file).toBe("a");
    expect(document.querySelector<HTMLDivElement>("#library-view")?.hidden).toBe(false);
  });

  it("restores the initiating task action even when the browser did not focus its button", async () => {
    tasks = [{ id: "task", fileId: "a", name: "a.mp4", kind: "original", state: "ready", done: 1, total: 1 }];
    (document.querySelector('[data-action="tasks"]') as HTMLButtonElement).click();
    await settle();
    (document.querySelector('[data-action="task-open"]') as HTMLButtonElement).click();
    await settle();
    (document.querySelector('[data-action="return-library"]') as HTMLButtonElement).click();
    expect((document.activeElement as HTMLElement).dataset.taskId).toBe("task");
    expect((document.activeElement as HTMLElement).dataset.action).toBe("task-open");
  });

  it("does not label the current library folder as siblings of a task from another folder", async () => {
    const foreign = makeEntry("foreign", "other-folder");
    entries.push(foreign);
    tasks = [{ id: "task", fileId: foreign.id, name: foreign.name, kind: "original", state: "ready", done: 1, total: 1 }];
    (document.querySelector('[data-action="tasks"]') as HTMLButtonElement).click();
    await settle();
    (document.querySelector('[data-action="task-open"]') as HTMLButtonElement).click();
    await settle();
    (document.querySelector('[data-action="preparation-play"]') as HTMLButtonElement).click();
    await settle();
    (document.querySelector('.player-playlist-toggle') as HTMLButtonElement).click();
    expect(Array.from(document.querySelectorAll<HTMLElement>("[data-player-select]"), node => node.dataset.playerSelect)).toEqual(["foreign"]);
  });
});

describe("LAN visitor preparation history cleanup", () => {
  const task = (id: string, state: string, spaceId = "space"): Task => ({ id, state, spaceId, fileId: "a", name: `${id}.mp4`, kind: "original", done: 1, total: 1 });
  const click = async (selector: string) => {
    const button = document.querySelector<HTMLButtonElement>(selector)!;
    button.focus(); button.click(); await settle();
  };
  const records = () => Array.from(document.querySelectorAll<HTMLElement>("#task-list [data-task]"), node => node.dataset.task);

  it("deletes one record, preserves keyboard focus and keeps it hidden after a fresh poll", async () => {
    tasks = [task("cancelled", "cancelled"), task("failed", "failed")];
    await click('[data-action="tasks"]');
    await click('[data-action="remove-task"][data-task-id="cancelled"]');
    expect(records()).toEqual(["failed"]);
    expect((document.activeElement as HTMLElement).dataset.taskId).toBe("failed");
    expect(document.querySelector<HTMLDivElement>("#notice")?.hidden).toBe(false);
    await vi.advanceTimersByTimeAsync(5000);
    await settle();
    expect(records()).toEqual(["failed"]);
    expect(tasks).toHaveLength(2);
  });

  it("clears only finished records in the selected space and retains active and held cards", async () => {
    tasks = ["ready", "failed", "cancelled", "held", "queued", "downloading", "converting"].map(state => task(state, state));
    tasks.push(task("other", "cancelled", "other"));
    spaces.push({ id: "other", name: "Other space", kind: "local", allowPrepare: true });
    await click('[data-action="tasks"]');
    expect(document.querySelector('[data-action="remove-task"][data-task-id="held"]')).toBeNull();
    expect(document.querySelector('[data-action="cancel-task"][data-task-id="held"]')).not.toBeNull();
    await click('[data-action="clear-tasks"]');
    expect(records()).toEqual(["held", "queued", "downloading", "converting"]);
    expect(document.querySelector<HTMLButtonElement>('[data-action="clear-tasks"]')?.disabled).toBe(true);
    await vi.advanceTimersByTimeAsync(5000);
    await settle();
    await click('[data-action="home"]');
    await click('[data-space="other"]');
    await click('[data-action="tasks"]');
    expect(records()).toEqual(["other"]);
    expect(tasks).toHaveLength(8);
  });

  it("offers removal after a held preparation is cancelled", async () => {
    tasks = [task("held", "held")];
    await click('[data-action="tasks"]');
    await click('[data-action="cancel-task"]');
    expect(document.querySelector('[data-action="remove-task"]')).not.toBeNull();
    await click('[data-action="remove-task"]');
    expect(records()).toEqual([]);
    expect(document.activeElement).toBe(document.querySelector(".tasks-heading h1"));
  });

  it("does not restore deleted cards from an older in-flight task poll", async () => {
    tasks = [task("cancelled", "cancelled")];
    await click('[data-action="tasks"]');
    let resolveStale!: (response: Response) => void;
    const stale = new Promise<Response>(resolve => { resolveStale = resolve; });
    taskHook = () => stale;
    await vi.advanceTimersByTimeAsync(5000);
    await settle();
    await click('[data-action="remove-task"]');
    expect(records()).toEqual([]);
    resolveStale(json(tasks));
    await settle();
    expect(records()).toEqual([]);
    taskHook = undefined;
    await vi.advanceTimersByTimeAsync(5000);
    await settle();
    expect(records()).toEqual([]);
  });

  it("shows cleanup failures without losing records and ignores an abandoned response", async () => {
    tasks = [task("cancelled", "cancelled")];
    await click('[data-action="tasks"]');
    cleanupHook = () => Promise.resolve(json({ error: "source_unavailable" }, 409));
    await click('[data-action="remove-task"]');
    expect(records()).toEqual(["cancelled"]);
    expect(document.querySelector<HTMLDivElement>("#notice")?.hidden).toBe(false);
    expect(document.querySelector("#notice")?.textContent).not.toBe("Record deleted");
    let resolveAbandoned!: (response: Response) => void;
    const abandoned = new Promise<Response>(resolve => { resolveAbandoned = resolve; });
    cleanupHook = () => abandoned;
    await click('[data-action="remove-task"]');
    window.dispatchEvent(new PopStateEvent("popstate"));
    resolveAbandoned(json({ removed: ["cancelled"] }));
    await settle();
    expect(records()).toEqual(["cancelled"]);
    expect(document.querySelector("#notice")?.textContent).not.toBe("Record deleted");
  });
});

describe("LAN visitor held preparation recovery", () => {
  it.each(["original", "compatible"])("revalidates and explicitly confirms a held %s retry without desktop resume", async kind => {
    entries[0].ready = false;
    tasks = [{ id: "held", fileId: "a", name: "a.mp4", kind, state: "held", done: 0, total: 1024 }];
    (document.querySelector('[data-action="tasks"]') as HTMLButtonElement).click();
    await settle();
    (document.querySelector('[data-action="task-open"]') as HTMLButtonElement).click();
    await settle();
    const retry = document.querySelector<HTMLButtonElement>('[data-action="retry-preparation"]')!;
    expect(document.querySelector<HTMLButtonElement>('[data-action="preparation-play"]')?.disabled).toBe(true);
    retry.click();
    await settle();
    expect(document.querySelector<HTMLDialogElement>("#panel")?.open).toBe(true);
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/prepare")).toHaveLength(0);
    (document.querySelector('[data-action="confirm-prepare"]') as HTMLButtonElement).click();
    await settle();
    const preparation = vi.mocked(fetch).mock.calls.find(([url]) => url === "/api/prepare")!;
    expect(JSON.parse(String(preparation[1]?.body))).toMatchObject({ spaceId: "space", fileId: "a", kind });
    if (kind === "compatible") expect(JSON.parse(String(preparation[1]?.body)).capabilities).toBeTruthy();
    expect(document.querySelector('[data-action="retry-preparation"]')).toBeNull();
  });

  it("exposes a freshly available original without starting another preparation", async () => {
    entries[0].ready = false;
    tasks = [{ id: "held", fileId: "a", name: "a.mp4", kind: "original", state: "held", done: 0, total: 1024 }];
    (document.querySelector('[data-action="tasks"]') as HTMLButtonElement).click();
    await settle();
    (document.querySelector('[data-action="task-open"]') as HTMLButtonElement).click();
    await settle();
    entries[0].ready = true;
    (document.querySelector('[data-action="retry-preparation"]') as HTMLButtonElement).click();
    await settle();
    expect(document.querySelector('[data-action="retry-preparation"]')).toBeNull();
    expect(document.querySelector<HTMLButtonElement>('[data-action="preparation-play"]')?.disabled).toBe(false);
    expect(vi.mocked(fetch).mock.calls.filter(([url]) => url === "/api/prepare")).toHaveLength(0);
  });
});

describe('LAN home screen web app', () => {
  it('dismisses installation guidance without removing its entry or changing the selected space', async () => {
    document.querySelector<HTMLButtonElement>('[data-action="home"]')!.click(); await settle();
    expect(document.querySelector('.install-card')).not.toBeNull();
    document.querySelector<HTMLButtonElement>('[data-action="dismiss-install"]')!.click();
    expect(document.querySelector('.install-card')).toBeNull();
    expect(localStorage.getItem('pkp-lan-install-dismissed')).toBe('1');
    document.querySelector<HTMLButtonElement>('[data-action="install"]')!.click();
    expect(document.querySelector<HTMLDialogElement>('#panel')!.open).toBe(true);
    document.querySelector<HTMLButtonElement>('#panel [data-action="close"]')!.click();
    document.querySelector<HTMLButtonElement>('.space-cards [data-space]')!.click(); await settle();
    expect(document.querySelector('[data-file="a"]')).not.toBeNull();
    document.querySelector<HTMLButtonElement>('[data-action="home"]')!.click(); await settle();
    expect(document.querySelector('.install-card')).toBeNull();
    expect(document.querySelector('[data-action="install"]')).not.toBeNull();
  });
  it('requires a live session again when reconnecting after a password reset', async () => {
    const boundary = vi.mocked(fetch).getMockImplementation()!;
    let live = false;
    vi.mocked(fetch).mockImplementation((path, options) => {
      if (!live) return Promise.reject(new TypeError('Network unavailable'));
      if (new URL(String(path), location.href).pathname === '/api/session') return Promise.resolve(json({ authenticated: false }));
      return boundary(path, options);
    });
    window.dispatchEvent(new Event('offline'));
    const banner = document.querySelector<HTMLElement>('#connection-status')!;
    expect(banner.hidden).toBe(false);
    document.querySelector<HTMLButtonElement>('[data-action="reconnect"]')!.click(); await settle();
    expect(banner.hidden).toBe(false);
    live = true;
    document.querySelector<HTMLButtonElement>('[data-action="reconnect"]')!.click(); await settle();
    expect(document.querySelector('#login-form')).not.toBeNull();
    expect(document.querySelector('[data-file="a"]')).toBeNull();
    expect(banner.hidden).toBe(true);
  });
  it('clears the connection banner only after a live request succeeds without replacing the active player', async () => {
    await open('a');
    const media = selectedMedia();
    window.dispatchEvent(new Event('offline'));
    expect(document.querySelector<HTMLElement>('#connection-status')!.hidden).toBe(false);
    document.querySelector<HTMLButtonElement>('[data-action="reconnect"]')!.click(); await settle();
    expect(document.querySelector<HTMLElement>('#connection-status')!.hidden).toBe(true);
    expect(selectedMedia()).toBe(media);
  });
});
