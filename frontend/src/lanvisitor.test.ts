import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type Entry = { id: string; parentId: string; name: string; mime: string; size: number; ready: boolean; compatibleReady: boolean };
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
  spaces = [{ id: "space", name: "Shared", kind: "local", allowPrepare: true }];
  dismissed = new Set();
  taskHook = undefined;
  cleanupHook = undefined;
  document.body.className = "";
  document.body.innerHTML = '<div id="app"></div><dialog id="panel"></dialog><div id="notice" hidden></div>';
  localStorage.setItem("pkp-lan-language", "en");
  history.replaceState(null, "", "/");
  vi.stubGlobal("CSS", { escape: (value: string) => value });
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
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
      case "/api/spaces": return json(spaces);
      case "/api/files": return json({ entries: entries.filter(entry => entry.parentId === "") });
      case "/api/file": {
        const id = url.searchParams.get("id")!;
        const delayed = fileHook?.(id);
        return delayed || json(entries.find(entry => entry.id === id));
      }
      case "/api/media": {
        const entry = entries.find(entry => entry.id === url.searchParams.get("id"));
        return json(info(entry?.compatibleReady ? "hevc" : "h264"));
      }
      case "/api/plan": {
        const request = JSON.parse(String(options.body));
        const entry = entries.find(entry => entry.id === request.fileId);
        return json({ kind: entry?.compatibleReady ? "video" : "direct", info: info(), warnings: [] });
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

describe("LAN visitor navigation and readiness", () => {
  it("offers an original conversion plan when another browser's cached copy is unsupported", async () => {
    entries[0].compatibleReady = true;
    await open("a");
    expect(document.querySelector<HTMLDialogElement>("#panel")?.open).toBe(true);
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
    fileHook = id => id === "b" ? delayed : undefined;
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
    await click('[data-action="files"]');
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
