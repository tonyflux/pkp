import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ShareEntry, ShareImport, SharePreview, ShareRestoreResult } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { createApp, nextTick, type App } from "vue";
import ShareImportSheet from "./components/ShareImportSheet.vue";

const backend = vi.hoisted(() => ({
  InspectShare: vi.fn(), ListShareFiles: vi.fn(), PreviewShareFile: vi.fn(), ClosePreview: vi.fn(),
  RestoreShare: vi.fn(), ShareRestoreStatus: vi.fn(),
}));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({ DriveService: backend }));
vi.mock("./store", async () => {
  // Vitest hoists this mock factory before static Vue imports are initialized.
  const { computed, reactive } = await import("vue");
  const appLock = reactive({ state: { locked: false } });
  return {
    appLock, appLocked: computed(() => appLock.state.locked),
    accounts: { list: [{ id: "a", login: "a@example.test" }, { id: "b", login: "b@example.test" }] },
    currentAccount: { value: { id: "a" } }, drive: { trail: [{ id: "destination-a", name: "A folder" }] },
    externalOpen: { paused: false }, relocate: { mode: "" },
    closeRelocate: vi.fn(), openShareFolderPicker: vi.fn(), openCloudFolder: vi.fn(),
    accountLabel: (account: { login: string }) => account.login, notify: vi.fn(),
  };
});
import { appLock, currentAccount, externalOpen, openCloudFolder, openShareFolderPicker } from "./store";
import {
  browseShare, chooseShareAccount, closeShareImport, closeSharePreview, confirmShareRestore,
  inspectShareInput, openShareDestination, openShareImport, pickShareDestination, previewShareEntry,
  retrySharePage, selectVisibleShare, shareCovered, shareImport as s, shareImportBusy, shareNeedsCode, shareSelected, toggleShareEntry,
} from "./shareimport";

const url = "https://mypikpak.com/s/example-share";
function entry(id: string, folder = false): ShareEntry {
  return { id, name: folder ? id : `${id}.mp4`, parentId: "", folder, category: folder ? "other" : "video", thumbnail: `https://images.example.test/${id}.jpg`, size: 100, duration: 65, width: 1920, height: 1080 } as ShareEntry;
}
function info(entries: ShareEntry[] = [entry("film")], status = "OK"): ShareImport {
  return { shareId: "example-share", url, title: "Synthetic share", owner: "Example", status, statusText: "", fileCount: entries.length, entries, nextPageToken: "", passCodeToken: "capability" };
}
function receipt(status = "pending", taskId = "task"): ShareRestoreResult {
  return { status, taskId, parentId: "destination-a", fileIds: status === "complete" ? ["copied-film"] : ["film"], message: "" };
}
// The project's TypeScript 4.9 library does not yet declare Promise.withResolvers.
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function preview(url: string): SharePreview { return { url, kind: "video", mimeType: "video/mp4", name: "film.mp4", text: null }; }

beforeEach(() => {
  closeShareImport();
  vi.resetAllMocks();
  appLock.state!.locked = false;
  backend.InspectShare.mockResolvedValue(info());
  backend.ListShareFiles.mockResolvedValue({ entries: [], nextPageToken: "", passCodeToken: "capability" });
  backend.ClosePreview.mockResolvedValue(undefined);
  backend.RestoreShare.mockResolvedValue(receipt());
  backend.ShareRestoreStatus.mockResolvedValue(receipt());
  externalOpen.paused = false;
});
afterEach(() => { closeShareImport(); vi.useRealTimers(); });

describe("share access and receiving account", () => {
  it("rejects foreign URLs locally and never treats a passcode gate as selectable content", async () => {
    await openShareImport("https://foreign.example/s/example-share");
    expect(s.error).not.toBe("");
    expect(backend.InspectShare).not.toHaveBeenCalled();
    s.input = url;
    backend.InspectShare.mockResolvedValueOnce(info([], "PASS_CODE_EMPTY"));
    await inspectShareInput();
    expect(shareNeedsCode()).toBe(true);
    await confirmShareRestore();
    expect(backend.RestoreShare).not.toHaveBeenCalled();
    s.passCode = "wrong";
    backend.InspectShare.mockResolvedValueOnce(info([], "PASS_CODE_ERROR"));
    await inspectShareInput();
    expect(shareNeedsCode()).toBe(true);
    s.passCode = "correct";
    await inspectShareInput();
    expect(shareNeedsCode()).toBe(false);
    expect(s.entries[0].thumbnail).toBe("https://images.example.test/film.jpg");
    expect(s.picked.size).toBe(0);
    expect(backend.RestoreShare).not.toHaveBeenCalled();
  });

  it("changes only the receiving account and discards stale reads, selection, destination, and picker callbacks", async () => {
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    pickShareDestination();
    const chooseOldFolder = vi.mocked(openShareFolderPicker).mock.calls[0][2];
    const old = deferred<ShareImport>(), fresh = deferred<ShareImport>();
    backend.InspectShare.mockReturnValueOnce(old.promise).mockReturnValueOnce(fresh.promise);
    const inspecting = inspectShareInput();
    const switching = chooseShareAccount("b");
    chooseOldFolder([{ id: "old", name: "Old account folder" }]);
    expect(s.account).toBe("b");
    expect(currentAccount.value?.id).toBe("a");
    expect(s.dest).toEqual([]);
    expect(s.picked.size).toBe(0);
    old.resolve(info([entry("old")]));
    await inspecting;
    expect(s.loading).toBe(true);
    expect(s.info).toBeNull();
    fresh.resolve(info([entry("fresh")]));
    await switching;
    expect(s.entries.map(e => e.id)).toEqual(["fresh"]);
    expect(s.loading).toBe(false);
  });

  it("does not replace an occupied draft and fully discards credentials on cancel", async () => {
    await openShareImport(`${url}?pass_code=secret`);
    const retainedURL = s.url;
    toggleShareEntry(s.entries[0]);
    await openShareImport("https://mypikpak.com/s/other-share");
    expect(s.url).toBe(retainedURL);
    expect(s.picked.size).toBe(1);
    closeShareImport();
    expect([s.url, s.input, s.passCode, s.account]).toEqual(["", "", "", ""]);
    expect(s.picked.size).toBe(0);
    expect(s.dest).toEqual([]);
    expect(externalOpen.paused).toBe(true);
  });
});

describe("selection across folders and pages", () => {
  it("keeps siblings selected, replaces descendants with their folder, and splits a selected folder when unchecking a child", async () => {
    const folder = entry("folder", true), other = entry("other");
    backend.InspectShare.mockResolvedValue(info([folder, other]));
    await openShareImport(url);
    toggleShareEntry(other);
    backend.ListShareFiles.mockResolvedValueOnce({ entries: [entry("child"), entry("sibling")], nextPageToken: "", passCodeToken: "capability" });
    await browseShare([{ id: folder.id, name: folder.name }]);
    toggleShareEntry(s.entries[0]);
    expect([...s.picked.keys()]).toEqual(["other", "child"]);
    backend.ListShareFiles.mockResolvedValueOnce({ entries: [folder, other], nextPageToken: "", passCodeToken: "capability" });
    await browseShare([]);
    toggleShareEntry(folder);
    expect([...s.picked.keys()]).toEqual(["other", "folder"]);
    backend.ListShareFiles.mockResolvedValueOnce({ entries: [entry("child"), entry("sibling")], nextPageToken: "", passCodeToken: "capability" });
    await browseShare([{ id: folder.id, name: folder.name }]);
    expect(shareCovered(s.entries[0])).toBe(true);
    expect(shareSelected(s.entries[0])).toBe(true);
    selectVisibleShare(true);
    expect([...s.picked.keys()]).toEqual(["other", "folder"]);
    toggleShareEntry(s.entries[0]);
    expect(shareSelected(s.entries[0])).toBe(false);
    expect(shareSelected(s.entries[1])).toBe(true);
    expect([...s.picked.keys()]).toEqual(["other", "sibling"]);
    selectVisibleShare(false);
    expect([...s.picked.keys()]).toEqual(["other"]);
  });

  it("merges pages without duplicate rows and applies all/none only to the visible folder", async () => {
    backend.InspectShare.mockResolvedValue({ ...info([entry("one")]), nextPageToken: "next" });
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    backend.ListShareFiles.mockResolvedValueOnce({ entries: [entry("one"), entry("two")], nextPageToken: "", passCodeToken: "renewed" });
    await browseShare([], true);
    expect(s.entries.map(e => e.id)).toEqual(["one", "two"]);
    expect(s.info?.passCodeToken).toBe("renewed");
    selectVisibleShare(true);
    expect(s.picked.size).toBe(2);
    backend.ListShareFiles.mockResolvedValueOnce({ entries: [entry("nested")], nextPageToken: "", passCodeToken: "renewed" });
    await browseShare([{ id: "folder", name: "Folder" }]);
    selectVisibleShare(true);
    selectVisibleShare(false);
    expect([...s.picked.keys()]).toEqual(["one", "two"]);
    expect(backend.RestoreShare).not.toHaveBeenCalled();
  });

  it("keeps the previous folder and selection when navigation fails", async () => {
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    backend.ListShareFiles.mockRejectedValueOnce(new Error("Share expired"));
    await browseShare([{ id: "folder", name: "Folder" }]);
    expect(s.trail).toEqual([]);
    expect(s.entries.map(e => e.id)).toEqual(["film"]);
    expect(s.picked.size).toBe(1);
    expect(s.pageError).toBe("Share expired");
    backend.ListShareFiles.mockResolvedValueOnce({ entries: [entry("retried-child")], nextPageToken: "", passCodeToken: "capability" });
    await retrySharePage();
    expect(s.trail).toEqual([{ id: "folder", name: "Folder" }]);
    expect(s.entries.map(e => e.id)).toEqual(["retried-child"]);
    expect(s.picked.size).toBe(1);
    expect(s.pageError).toBe("");
  });
});

describe("discard and preview lifetimes", () => {
  it("ignores inspection and folder results that arrive after a new sheet opens", async () => {
    const old = deferred<ShareImport>();
    backend.InspectShare.mockReturnValueOnce(old.promise);
    const opening = openShareImport(url);
    closeShareImport();
    await openShareImport("https://mypikpak.com/s/new-share");
    old.resolve(info([entry("stale")]));
    await opening;
    expect(s.url).toBe("https://mypikpak.com/s/new-share");
    expect(s.entries.map(e => e.id)).toEqual(["film"]);
    const page = deferred<{ entries: ShareEntry[]; nextPageToken: string; passCodeToken: string }>();
    backend.ListShareFiles.mockReturnValueOnce(page.promise);
    const browsing = browseShare([{ id: "stale-folder", name: "Old" }]);
    closeShareImport();
    await openShareImport(url);
    page.resolve({ entries: [entry("stale-child")], nextPageToken: "old", passCodeToken: "old" });
    await browsing;
    expect(s.trail).toEqual([]);
    expect(s.entries.map(e => e.id)).toEqual(["film"]);
    expect(s.nextPageToken).toBe("");
  });

  it("opens a preview without selecting, releases closed previews, and releases late preview responses", async () => {
    await openShareImport(url);
    backend.PreviewShareFile.mockResolvedValueOnce(preview("http://127.0.0.1:1234/preview/first"));
    await previewShareEntry(s.entries[0]);
    expect(s.preview?.kind).toBe("video");
    expect(s.picked.size).toBe(0);
    expect(backend.RestoreShare).not.toHaveBeenCalled();
    closeSharePreview();
    expect(backend.ClosePreview).toHaveBeenCalledWith("http://127.0.0.1:1234/preview/first");
    const late = deferred<SharePreview>();
    backend.PreviewShareFile.mockReturnValueOnce(late.promise);
    const loading = previewShareEntry(s.entries[0]);
    closeShareImport();
    await openShareImport(url);
    late.resolve(preview("http://127.0.0.1:1234/preview/late"));
    await loading;
    expect(s.previewOpen).toBe(false);
    expect(s.preview).toBeNull();
    expect(backend.ClosePreview).toHaveBeenCalledWith("http://127.0.0.1:1234/preview/late");
  });

  it("does not let a previous preview displace the current preview", async () => {
    await openShareImport(url);
    const first = deferred<SharePreview>();
    backend.PreviewShareFile.mockReturnValueOnce(first.promise).mockResolvedValueOnce(preview("http://127.0.0.1:1234/preview/current"));
    const old = previewShareEntry(entry("first"));
    await previewShareEntry(entry("second"));
    first.resolve(preview("http://127.0.0.1:1234/preview/old"));
    await old;
    expect(s.preview?.url).toBe("http://127.0.0.1:1234/preview/current");
    expect(backend.ClosePreview).toHaveBeenCalledWith("http://127.0.0.1:1234/preview/old");
  });
});

describe("explicit transfer and honest receipts", () => {
  it("writes only on confirmation and locks an uncertain request against duplicate submission", async () => {
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    const restoring = deferred<ShareRestoreResult>();
    backend.RestoreShare.mockReturnValueOnce(restoring.promise);
    expect(backend.RestoreShare).not.toHaveBeenCalled();
    const confirming = confirmShareRestore();
    await confirmShareRestore();
    await chooseShareAccount("b");
    expect(s.account).toBe("a");
    expect(shareImportBusy()).toBe(true);
    expect(backend.RestoreShare).toHaveBeenCalledTimes(1);
    expect(backend.RestoreShare.mock.calls[0]).toEqual(["a", { shareId: "example-share", passCodeToken: "capability", parentId: "destination-a", fileIds: ["film"] }]);
    restoring.reject(new Error("Response lost"));
    await confirming;
    expect(s.result).toBeNull();
    expect(s.error).toContain("Response lost");
    expect(s.attempted).toBe(true);
    expect(shareImportBusy()).toBe(false);
    await confirmShareRestore();
    expect(backend.RestoreShare).toHaveBeenCalledTimes(1);
  });

  it("keeps an accepted transfer pending until a terminal receipt and opens the receiving account's folder", async () => {
    vi.useFakeTimers();
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    await confirmShareRestore();
    expect(s.result?.status).toBe("pending");
    backend.ShareRestoreStatus.mockResolvedValueOnce(receipt("complete"));
    await vi.advanceTimersByTimeAsync(1500);
    expect(s.result?.status).toBe("complete");
    expect(shareImportBusy()).toBe(false);
    await confirmShareRestore();
    expect(backend.RestoreShare).toHaveBeenCalledTimes(1);
    await openShareDestination();
    expect(openCloudFolder).toHaveBeenCalledWith("a", "destination-a");
    expect(s.open).toBe(false);
  });

  it("stops the busy indicator at a finite deadline even if a status read hangs, retaining the queued receipt", async () => {
    vi.useFakeTimers();
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    backend.ShareRestoreStatus.mockReturnValue(deferred<ShareRestoreResult>().promise);
    await confirmShareRestore();
    vi.advanceTimersByTime(30000);
    expect(s.result?.status).toBe("pending");
    expect(shareImportBusy()).toBe(false);
    expect(s.attempted).toBe(true);
    await confirmShareRestore();
    expect(backend.RestoreShare).toHaveBeenCalledTimes(1);
  });

  it("does not mark an untracked accepted request complete or poll without a task", async () => {
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    backend.RestoreShare.mockResolvedValueOnce(receipt("pending", ""));
    await confirmShareRestore();
    expect(s.result?.status).toBe("pending");
    expect(s.submitting).toBe(false);
    expect(s.attempted).toBe(true);
    expect(backend.ShareRestoreStatus).not.toHaveBeenCalled();
  });

  it("shows a terminal task error and never resubmits it automatically", async () => {
    vi.useFakeTimers();
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    await confirmShareRestore();
    backend.ShareRestoreStatus.mockResolvedValueOnce({ ...receipt("error"), message: "Destination full" });
    await vi.advanceTimersByTimeAsync(1500);
    expect(s.result?.status).toBe("error");
    expect(s.result?.message).toBe("Destination full");
    expect(s.submitting).toBe(false);
    await vi.advanceTimersByTimeAsync(30000);
    expect(backend.ShareRestoreStatus).toHaveBeenCalledTimes(1);
    expect(backend.RestoreShare).toHaveBeenCalledTimes(1);
  });

  it("ignores a previous account's late mutation response after discard", async () => {
    const response = deferred<ShareRestoreResult>();
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    backend.RestoreShare.mockReturnValueOnce(response.promise);
    const submitting = confirmShareRestore();
    closeShareImport();
    await openShareImport(url);
    await chooseShareAccount("b");
    response.resolve(receipt("complete"));
    await submitting;
    expect(s.account).toBe("b");
    expect(s.result).toBeNull();
    expect(s.attempted).toBe(false);
    expect(s.submitting).toBe(false);
  });

  it("ignores an old task status after cancellation and stops scheduling polls", async () => {
    vi.useFakeTimers();
    const status = deferred<ShareRestoreResult>();
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    backend.ShareRestoreStatus.mockReturnValueOnce(status.promise);
    await confirmShareRestore();
    await vi.advanceTimersByTimeAsync(1500);
    closeShareImport();
    await openShareImport(url);
    await chooseShareAccount("b");
    status.resolve(receipt("complete"));
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(30000);
    expect(s.account).toBe("b");
    expect(s.result).toBeNull();
    expect(backend.ShareRestoreStatus).toHaveBeenCalledTimes(1);
  });
});

describe("thumbnail interactions", () => {
  it("starts a newly opened folder at the top but retains position while loading another page", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const app = createApp(ShareImportSheet);
    app.mount(host);
    try {
      backend.InspectShare.mockResolvedValue(info([entry("folder", true), ...Array.from({ length: 20 }, (_, i) => entry(`root-${i}`))]));
      await openShareImport(url);
      await nextTick();
      document.querySelector<HTMLElement>(".file-scroll")!.scrollTop = 180;
      backend.ListShareFiles.mockResolvedValueOnce({ entries: Array.from({ length: 20 }, (_, i) => entry(`child-${i}`)), nextPageToken: "more", passCodeToken: "capability" });
      await browseShare([{ id: "folder", name: "Folder" }]);
      await nextTick();
      expect(document.querySelector<HTMLElement>(".file-scroll")!.scrollTop).toBe(0);
      document.querySelector<HTMLElement>(".file-scroll")!.scrollTop = 180;
      backend.ListShareFiles.mockResolvedValueOnce({ entries: [entry("next-page")], nextPageToken: "", passCodeToken: "capability" });
      await browseShare(s.trail, true);
      await nextTick();
      expect(document.querySelector<HTMLElement>(".file-scroll")!.scrollTop).toBe(180);
    } finally {
      closeShareImport();
      app.unmount();
      host.remove();
    }
  });

  it("closes private media on App Lock and does not resume it after unlock", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const app = createApp(ShareImportSheet);
    app.mount(host);
    try {
      await openShareImport(url);
      backend.PreviewShareFile.mockResolvedValueOnce(preview("http://127.0.0.1:1234/preview/private"));
      await previewShareEntry(s.entries[0]);
      await nextTick();
      expect(document.querySelector(".preview-card video")).not.toBeNull();
      appLock.state!.locked = true;
      await nextTick();
      expect(document.querySelector(".preview-card video")).toBeNull();
      expect(s.previewOpen).toBe(false);
      expect(backend.ClosePreview).toHaveBeenCalledWith("http://127.0.0.1:1234/preview/private");
      appLock.state!.locked = false;
      await nextTick();
      expect(document.querySelector(".preview-card video")).toBeNull();
    } finally {
      closeShareImport();
      app.unmount();
      host.remove();
    }
  });
  it("loads real DTO thumbnails, falls back after image errors, and keeps preview separate from selection with autoplay on open", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const app = createApp(ShareImportSheet);
    app.mount(host);
    try {
      await openShareImport(url);
      await nextTick();
      const thumbnail = document.querySelector<HTMLButtonElement>(".thumbnail")!;
      const image = thumbnail.querySelector("img")!;
      expect(image.getAttribute("src")).toBe("https://images.example.test/film.jpg");
      image.dispatchEvent(new Event("error"));
      await nextTick();
      expect(thumbnail.querySelector("img")).toBeNull();
      expect(thumbnail.querySelector(".fallback svg")).not.toBeNull();
      backend.PreviewShareFile.mockResolvedValueOnce(preview("http://127.0.0.1:1234/preview/explicit"));
      thumbnail.focus();
      thumbnail.click();
      await nextTick();
      expect(s.picked.size).toBe(1);
      expect(s.previewOpen).toBe(false);
      thumbnail.click();
      await nextTick();
      expect(s.picked.size).toBe(0);
      thumbnail.dispatchEvent(new MouseEvent("click", { bubbles: true, detail: 1 }));
      thumbnail.dispatchEvent(new MouseEvent("click", { bubbles: true, detail: 2 }));
      thumbnail.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, detail: 2 }));
      await Promise.resolve();
      await nextTick();
      expect(s.picked.size).toBe(0);
      const video = document.querySelector<HTMLVideoElement>(".preview-card video")!;
      expect(video).not.toBeNull();
      expect(video.autoplay).toBe(true);
      expect(video.controls).toBe(true);
      const close = document.querySelector<HTMLButtonElement>(".preview-card button")!;
      expect(document.activeElement).toBe(close);
      video.focus();
      video.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true }));
      expect(document.activeElement).toBe(close);
      closeSharePreview();
      await nextTick();
      await vi.waitFor(() => expect(document.activeElement).toBe(thumbnail));
      backend.PreviewShareFile.mockResolvedValueOnce(preview("http://127.0.0.1:1234/preview/filename-click"));
      document.querySelector<HTMLButtonElement>(".filename")!.click();
      await Promise.resolve();
      await nextTick();
      expect(s.picked.size).toBe(0);
      expect(s.previewOpen).toBe(true);
      closeSharePreview();
      await nextTick();
      const checkbox = document.querySelector<HTMLInputElement>(".pick")!;
      checkbox.dispatchEvent(new Event("change", { bubbles: true }));
      await nextTick();
      expect(checkbox.checked).toBe(true);
      expect(s.picked.size).toBe(1);
      expect(s.previewOpen).toBe(false);
      backend.PreviewShareFile.mockResolvedValueOnce(preview("http://127.0.0.1:1234/preview/keep-selected"));
      thumbnail.dispatchEvent(new MouseEvent("click", { bubbles: true, detail: 1 }));
      thumbnail.dispatchEvent(new MouseEvent("click", { bubbles: true, detail: 2 }));
      thumbnail.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, detail: 2 }));
      await Promise.resolve();
      await nextTick();
      expect(s.picked.size).toBe(1);
      expect(s.previewOpen).toBe(true);
      expect(backend.RestoreShare).not.toHaveBeenCalled();
    } finally {
      closeShareImport();
      app.unmount();
      host.remove();
    }
  });
});

describe("share panel controls", () => {
  let host: HTMLElement, app: App;
  beforeEach(() => {
    host = document.createElement("div");
    document.body.append(host);
    app = createApp(ShareImportSheet);
    app.mount(host);
  });
  afterEach(() => {
    closeShareImport();
    app.unmount();
    host.remove();
  });

  it("closes only the account menu on Escape, switches the receiver with the keyboard, and locks it after submission", async () => {
    await openShareImport(url);
    toggleShareEntry(s.entries[0]);
    await nextTick();
    const face = document.querySelector<HTMLElement>('[role="combobox"][aria-label="转存账号"]')!;
    vi.spyOn(face, "getBoundingClientRect").mockReturnValue(new DOMRect(20, 200, 300, 32));
    const key = (target: Element, value: string) => target.dispatchEvent(new KeyboardEvent("keydown", { key: value, bubbles: true, cancelable: true }));
    face.focus();
    key(face, "Enter");
    await vi.waitFor(() => expect(document.activeElement?.getAttribute("role")).toBe("option"));
    key(document.activeElement!, "Escape");
    await nextTick();
    expect(document.querySelector('[role="listbox"]')).toBeNull();
    expect(document.activeElement).toBe(face);
    expect(s.open).toBe(true);
    expect(s.picked.has("film")).toBe(true);
    key(face, "ArrowDown");
    await vi.waitFor(() => expect(document.activeElement?.getAttribute("role")).toBe("option"));
    key(document.activeElement!, "ArrowDown");
    key(document.activeElement!, "Enter");
    await vi.waitFor(() => expect(s.account).toBe("b"));
    expect(currentAccount.value?.id).toBe("a");
    expect(s.dest).toEqual([]);
    expect(s.picked.size).toBe(0);
    expect(backend.RestoreShare).not.toHaveBeenCalled();
    s.attempted = true;
    await nextTick();
    expect(face.getAttribute("aria-disabled")).toBe("true");
    expect(face.tabIndex).toBe(-1);
    face.click();
    key(face, "Enter");
    await nextTick();
    expect(document.querySelector('[role="listbox"]')).toBeNull();
  });

  it("keeps loading after the preview URL arrives, until a frame is ready, and handles buffering and errors", async () => {
    await openShareImport(url);
    const pending = deferred<SharePreview>();
    backend.PreviewShareFile.mockReturnValueOnce(pending.promise);
    const reading = previewShareEntry(s.entries[0]);
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).not.toBeNull();
    pending.resolve(preview("http://127.0.0.1:1234/preview/buffering"));
    await reading;
    await nextTick();
    const video = document.querySelector<HTMLVideoElement>(".preview-card video")!;
    const playSpy = vi.spyOn(video, "play").mockResolvedValue(undefined);
    expect(document.querySelector('.preview-card [role="status"]')).not.toBeNull();
    video.dispatchEvent(new Event("loadedmetadata"));
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).not.toBeNull();
    expect(playSpy).not.toHaveBeenCalled();
    video.dispatchEvent(new Event("loadeddata"));
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).toBeNull();
    expect(playSpy).toHaveBeenCalledTimes(1);
    video.dispatchEvent(new Event("canplay"));
    await nextTick();
    expect(playSpy).toHaveBeenCalledTimes(1);
    video.dispatchEvent(new Event("waiting"));
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).not.toBeNull();
    video.dispatchEvent(new Event("playing"));
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).toBeNull();
    video.dispatchEvent(new Event("seeking"));
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).not.toBeNull();
    video.dispatchEvent(new Event("error"));
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).toBeNull();
    expect(document.querySelector('.preview-card [role="alert"]')).not.toBeNull();
    expect(s.picked.size).toBe(0);
    expect(backend.RestoreShare).not.toHaveBeenCalled();
    closeSharePreview();
    await nextTick();
    expect(document.querySelector(".preview-layer")).toBeNull();
  });

  it("resets loading for each image and stops on arrival or failure without carrying a stale error", async () => {
    await openShareImport(url);
    backend.PreviewShareFile.mockResolvedValueOnce({ ...preview("http://127.0.0.1:1234/preview/image-one"), kind: "image", name: "one.jpg" });
    await previewShareEntry(s.entries[0]);
    await nextTick();
    const image = document.querySelector<HTMLImageElement>(".preview-card img")!;
    expect(document.querySelector('.preview-card [role="status"]')).not.toBeNull();
    image.dispatchEvent(new Event("load"));
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).toBeNull();
    backend.PreviewShareFile.mockResolvedValueOnce({ ...preview("http://127.0.0.1:1234/preview/image-two"), kind: "image", name: "two.jpg" });
    await previewShareEntry(s.entries[0]);
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).not.toBeNull();
    document.querySelector<HTMLImageElement>(".preview-card img")!.dispatchEvent(new Event("error"));
    await nextTick();
    expect(document.querySelector('.preview-card [role="status"]')).toBeNull();
    expect(document.querySelector('.preview-card [role="alert"]')).not.toBeNull();
    backend.PreviewShareFile.mockResolvedValueOnce({ ...preview("http://127.0.0.1:1234/preview/image-three"), kind: "image", name: "three.jpg" });
    await previewShareEntry(s.entries[0]);
    await nextTick();
    expect(document.querySelector('.preview-card [role="alert"]')).toBeNull();
    expect(document.querySelector('.preview-card [role="status"]')).not.toBeNull();
  });

  it("retains selected and covered files when switching between grid and list without starting preview or restore", async () => {
    backend.InspectShare.mockResolvedValueOnce(info([entry("folder", true), entry("film")]));
    await openShareImport(url);
    await nextTick();
    document.querySelector<HTMLInputElement>(".pick")!.click();
    await nextTick();
    document.querySelector<HTMLButtonElement>('[aria-label="列表"]')!.click();
    await nextTick();
    expect(document.querySelector('[aria-label="列表"]')?.getAttribute("aria-pressed")).toBe("true");
    expect(document.querySelector<HTMLInputElement>(".pick")!.checked).toBe(true);
    expect(s.picked.has("folder")).toBe(true);
    backend.ListShareFiles.mockResolvedValueOnce({ entries: [entry("child"), entry("child-two")], nextPageToken: "", passCodeToken: "capability" });
    await browseShare([{ id: "folder", name: "Folder" }]);
    await nextTick();
    document.querySelector<HTMLButtonElement>('[aria-label="网格"]')!.click();
    await nextTick();
    expect(document.querySelector('[aria-label="网格"]')?.getAttribute("aria-pressed")).toBe("true");
    const covered = document.querySelector<HTMLInputElement>(".pick")!;
    expect(covered.checked).toBe(true);
    expect(covered.disabled).toBe(false);
    expect([...s.picked.keys()]).toEqual(["folder"]);
    covered.click();
    await nextTick();
    expect(covered.checked).toBe(false);
    expect([...s.picked.keys()]).toEqual(["child-two"]);
    expect(s.dest.map(crumb => crumb.id)).toEqual(["destination-a"]);
    expect(backend.PreviewShareFile).not.toHaveBeenCalled();
    expect(backend.RestoreShare).not.toHaveBeenCalled();
  });
});
