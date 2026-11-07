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
let playbackHook: ((id: string) => Promise<Response> | undefined) | undefined;
let filesHook: ((parent: string) => Promise<Response> | undefined) | undefined;
let pageSize = Infinity;
let pageHook: ((token: string) => Promise<Response> | undefined) | undefined;
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
  spaces = [{ id: "space", name: "Shared", kind: "local", allowPrepare: true }];
  dismissed = new Set();
  taskHook = undefined;
  cleanupHook = undefined;
  document.body.className = "";
  document.body.innerHTML = '<div id="app"></div><dialog id="panel"></dialog><div id="notice" hidden></div>';
  localStorage.setItem("pkp-lan-language", "en");
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
    vi.spyOn(list, "getBoundingClientRect").mockImplementation(() => rectangle(-100, 1000, 320));
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
