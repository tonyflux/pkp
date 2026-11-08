import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, type Component } from "vue";
import type { AppLockState, DriveStatus, ExternalOpen, SignInResult, TorrentMeta } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  pending: [] as ExternalOpen[],
  pendingRead: null as null | (() => Promise<ExternalOpen[]>),
  account: "a",
  open: vi.fn<() => Promise<string | null>>(),
  clipboard: vi.fn<() => Promise<string>>(),
  calls: [] as string[],
  listeners: new Map<string, () => void>(),
  read: null as null | ((path: string) => Promise<TorrentMeta>),
}));
vi.mock("@wailsio/runtime", () => ({
  Events: { On: (name: string, handler: () => void) => backend.listeners.set(name, handler) },
  Window: new Proxy({}, { get: () => async () => null }),
  Browser: {}, Clipboard: {}, Dialogs: { OpenFile: () => backend.open() },
}));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => {
  const service = new Proxy({}, { get: (_target, name: string) => async (...args: unknown[]) => {
    backend.calls.push(name);
    if (name === "Pending") return backend.pendingRead ? backend.pendingRead() : backend.pending.map((p) => ({ ...p }));
    if (name === "Ack") backend.pending = backend.pending.filter((p) => p.id !== args[0]);
    if (name === "ReadTorrent") return backend.read!(args[0] as string);
    if (name === "ClipboardShare") return backend.clipboard();
    if (name === "InspectShare") return { shareId: "Share_123", url: args[1], title: "Travel collection", owner: "Sample owner", status: "OK", statusText: "", fileCount: 1, entries: [], nextPageToken: "", passCodeToken: "share-capability" };
    if (name === "SwitchAccount") backend.account = args[0] as string;
    if (name === "SwitchAccount" || name === "Status") return { configured: true, accountId: backend.account };
    if (name === "Accounts") return [{ id: "a", label: "Account A" }, { id: "b", label: "Account B" }];
    return null;
  } });
  return { DriveService: service, ExternalOpenService: service, PlayerService: service, AppearanceService: service, AppLockService: service, MenuBarService: service, NotifyService: service, TelemetryService: service };
});

import { accounts, appLock, clearTorrent, closeRelocate, cloudAdd, completeQRSignIn, createRelocateFolder, drive, externalOpen, loadAccounts, openPikPakShare, openShareFolderPicker, openSignIn, pickTorrent, switchAccount, ui, watchExternalOpen } from "./store";
import { closeShareImport, shareImport } from "./shareimport";
import CloudAddSheet from "./components/CloudAddSheet.vue";
import RelocateSheet from "./components/RelocateSheet.vue";

const magnet = "magnet:?xt=urn:btih:0123456789012345678901234567890123456789&dn=Example";
const status = (id = "a") => ({ configured: true, accountId: id } as DriveStatus);
const meta = (path: string): TorrentMeta => ({
  path, name: path, hash: "0123456789012345678901234567890123456789", size: 1030,
  files: [{ index: 0, path: "video.mp4", size: 1000, category: "video" }, { index: 1, path: "notes.txt", size: 30, category: "document" }],
});
const emit = () => backend.listeners.get("external:open")!();
const add = (item: ExternalOpen) => { backend.pending.push(item); emit(); };
const idle = async () => { for (let n = 0; n < 30; n++) await Promise.resolve(); await nextTick(); };

function deferred<T>() {
  return (Promise as PromiseConstructor & {
    withResolvers<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (reason?: unknown) => void };
  }).withResolvers<T>();
}

const unmounts = new Set<() => void>();
async function mountSheet(component: Component = CloudAddSheet) {
  const host = document.createElement("div");
  document.body.append(host);
  const app = createApp(component);
  app.mount(host);
  unmounts.add(() => { app.unmount(); host.remove(); });
  await nextTick();
}
function sheetButton(label: string) {
  const button = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'))
    .find((b) => b.textContent?.trim() === label || b.title === label);
  if (!button) throw new Error(`Missing sheet button: ${label}`);
  return button;
}
afterEach(() => {
  for (const unmount of unmounts) unmount();
  unmounts.clear();
  vi.restoreAllMocks();
});

beforeEach(async () => {
  backend.pending = [];
  backend.pendingRead = null;
  backend.account = "a";
  backend.open.mockReset().mockResolvedValue(null);
  backend.clipboard.mockReset().mockResolvedValue("");
  vi.spyOn(document, "hasFocus").mockReturnValue(false);
  backend.read = async (path) => meta(path);
  drive.status = null;
  closeShareImport();
  closeRelocate();
  accounts.switching = "";
  accounts.list = [{ id: "a", label: "Account A" }, { id: "b", label: "Account B" }] as typeof accounts.list;
  accounts.dialog = null;
  appLock.state = null;
  cloudAdd.draft = "";
  clearTorrent();
  cloudAdd.reading = cloudAdd.submitting = false;
  cloudAdd.dest = [];
  externalOpen.pending = [];
  ui.route = "files";
  ui.cloudAddOpen = false;
  ui.message = "";
  drive.trail = [{ id: "dest", name: "Destination" }];
  await idle();
  await watchExternalOpen();
  backend.calls = [];
});

describe("external cloud-add consumption", () => {
  it("retains a cold-start magnet through login and unlock, then asks for confirmation", async () => {
    backend.pending = [{ id: 1, kind: "magnet", value: magnet }];
    await watchExternalOpen();
    expect(cloudAdd.draft).toBe("");
    expect(ui.cloudAddOpen).toBe(false);
    appLock.state = { locked: true } as AppLockState;
    drive.status = status();
    await idle();
    expect(backend.pending.map((p) => p.id)).toEqual([1]);
    expect(cloudAdd.draft).toBe("");
    appLock.state.locked = false;
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    expect(ui.cloudAddOpen).toBe(true);
    expect(cloudAdd.tab).toBe("link");
    expect(cloudAdd.dest.map((p) => p.id)).toEqual(["dest"]);
    expect(backend.calls).not.toContain("AddOfflineTasks");
    expect(backend.calls).not.toContain("AddTorrent");
  });

  it("shows an external torrent without replacing edited links or their destination", async () => {
    drive.status = status();
    cloudAdd.draft = "https://example.com/my-draft.mp4";
    cloudAdd.dest = [{ id: "chosen", name: "Chosen destination" }];
    add({ id: 2, kind: "torrent", value: "/tmp/second.torrent" });
    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/second.torrent"));
    expect(cloudAdd.draft).toBe("https://example.com/my-draft.mp4");
    expect(cloudAdd.dest.map((p) => p.id)).toEqual(["chosen"]);
    expect([...cloudAdd.picked]).toEqual([0, 1]);
    expect(cloudAdd.tab).toBe("torrent");
    expect(ui.cloudAddOpen).toBe(true);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    expect(backend.calls).not.toContain("AddTorrent");
  });

  it.each([true, false])("switches to a new magnet while preserving the previous torrent (sheet open: %s)", async (open) => {
    drive.status = status();
    add({ id: 20, kind: "torrent", value: "/tmp/previous.torrent" });
    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/previous.torrent"));
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    cloudAdd.picked.delete(1);
    cloudAdd.dest = [{ id: "chosen", name: "Chosen destination" }];
    ui.cloudAddOpen = open;

    add({ id: 21, kind: "magnet", value: magnet });
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));
    expect(cloudAdd.tab).toBe("link");
    expect(ui.cloudAddOpen).toBe(true);
    expect(cloudAdd.torrent?.path).toBe("/tmp/previous.torrent");
    expect([...cloudAdd.picked]).toEqual([0]);
    expect(cloudAdd.dest.map((p) => p.id)).toEqual(["chosen"]);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    expect(backend.calls).not.toContain("AddOfflineTasks");
    expect(backend.calls).not.toContain("AddTorrent");
  });

  it("does not let another queued torrent hide a new magnet", async () => {
    drive.status = status();
    backend.pending = [
      { id: 22, kind: "torrent", value: "/tmp/previous.torrent" },
      { id: 23, kind: "torrent", value: "/tmp/queued.torrent" },
    ];
    await watchExternalOpen();
    await vi.waitFor(() => expect(externalOpen.pending.map((p) => p.id)).toEqual([23]));

    add({ id: 24, kind: "magnet", value: magnet });
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));
    expect(cloudAdd.tab).toBe("link");
    expect(cloudAdd.torrent?.path).toBe("/tmp/previous.torrent");
    await vi.waitFor(() => expect(backend.pending.map((p) => p.id)).toEqual([23]));

    clearTorrent();
    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/queued.torrent"));
    expect(cloudAdd.draft).toBe(magnet);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
  });

  it.each(["reading", "submitting"] as const)("waits for %s before showing a magnet beside a retained torrent", async (busy) => {
    drive.status = status();
    cloudAdd.torrent = meta("/tmp/previous.torrent");
    cloudAdd.tab = "torrent";
    cloudAdd[busy] = true;
    add({ id: 25, kind: "magnet", value: magnet });
    await idle();
    expect(cloudAdd.draft).toBe("");
    expect(cloudAdd.tab).toBe("torrent");
    expect(backend.pending.map((p) => p.id)).toEqual([25]);

    cloudAdd[busy] = false;
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));
    expect(cloudAdd.tab).toBe("link");
    expect(cloudAdd.torrent?.path).toBe("/tmp/previous.torrent");
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
  });

  it("keeps additional torrents queued through closing and submission, advancing only after the current input clears", async () => {
    drive.status = status();
    backend.pending = [
      { id: 3, kind: "torrent", value: "/tmp/first.torrent" },
      { id: 4, kind: "torrent", value: "/tmp/next.torrent" },
    ];
    await watchExternalOpen();
    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/first.torrent"));
    await vi.waitFor(() => expect(externalOpen.pending.map((p) => p.id)).toEqual([4]));
    ui.cloudAddOpen = false;
    await idle();
    expect(ui.cloudAddOpen).toBe(false);
    expect(cloudAdd.torrent?.path).toBe("/tmp/first.torrent");
    cloudAdd.submitting = true;
    clearTorrent();
    await idle();
    expect(cloudAdd.torrent).toBe(null);
    expect(backend.pending.map((p) => p.id)).toEqual([4]);
    cloudAdd.submitting = false;
    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/next.torrent"));
    expect(ui.cloudAddOpen).toBe(true);
  });

  it("does not consume a slow torrent read into an account that is switching", async () => {
    drive.status = status();
    let finish!: (value: TorrentMeta) => void;
    backend.read = () => new Promise((resolve) => { finish = resolve; });
    add({ id: 5, kind: "torrent", value: "/tmp/slow.torrent" });
    await vi.waitFor(() => expect(cloudAdd.reading).toBe(true));
    accounts.switching = "b";
    finish(meta("/tmp/slow.torrent"));
    await vi.waitFor(() => expect(cloudAdd.reading).toBe(false));
    expect(cloudAdd.torrent).toBe(null);
    expect(backend.pending.map((p) => p.id)).toEqual([5]);
    backend.read = async (path) => meta(path);
    drive.status = status("b");
    drive.trail = [{ id: "other-folder", name: "Other" }];
    accounts.switching = "";
    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/slow.torrent"));
    expect(cloudAdd.dest.map((p) => p.id)).toEqual(["other-folder"]);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
  });

  it("stages the next link in the new account after discarding the current draft", async () => {
    drive.status = status();
    drive.trail = [{ id: "a-only", name: "Account A folder" }];
    const next = `${magnet}&next=1`;
    backend.pending = [
      { id: 8, kind: "magnet", value: magnet },
      { id: 9, kind: "magnet", value: next },
    ];
    await watchExternalOpen();
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));

    const switching = switchAccount("b");
    await vi.waitFor(() => expect(accounts.dialog?.kind).toBe("leave"));
    const question = accounts.dialog;
    if (question?.kind !== "leave") throw new Error("Missing discard confirmation");
    question.resolve(true);
    expect(await switching).toBe(true);

    await vi.waitFor(() => expect(cloudAdd.draft).toBe(next));
    expect(drive.status?.accountId).toBe("b");
    expect(cloudAdd.dest).toEqual(drive.trail);
    expect(cloudAdd.dest.map((p) => p.id)).not.toContain("a-only");
    expect(ui.cloudAddOpen).toBe(true);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
  });

  it("resumes the queue in the original account after cancelling an account switch", async () => {
    drive.status = status();
    cloudAdd.draft = "https://example.com/current.mp4";
    add({ id: 10, kind: "magnet", value: magnet });
    await idle();

    const switching = switchAccount("b");
    await vi.waitFor(() => expect(accounts.dialog?.kind).toBe("leave"));
    const question = accounts.dialog;
    if (question?.kind !== "leave") throw new Error("Missing discard confirmation");
    question.resolve(false);
    expect(await switching).toBe(false);
    expect(drive.status?.accountId).toBe("a");
    expect(cloudAdd.draft).toBe("https://example.com/current.mp4");

    cloudAdd.draft = "";
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));
    expect(cloudAdd.dest).toEqual(drive.trail);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
  });

  it("restarts a stale torrent read when the account switch finishes before that read", async () => {
    drive.status = status();
    const old = deferred<TorrentMeta>();
    backend.read = vi.fn<(path: string) => Promise<TorrentMeta>>()
      .mockImplementationOnce(() => old.promise)
      .mockImplementation(async (path) => meta(path));
    add({ id: 11, kind: "torrent", value: "/tmp/slow-switch.torrent" });
    await vi.waitFor(() => expect(cloudAdd.reading).toBe(true));

    expect(await switchAccount("b")).toBe(true);
    await idle();
    old.resolve(meta("/tmp/slow-switch.torrent"));

    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/slow-switch.torrent"));
    expect(drive.status?.accountId).toBe("b");
    expect(cloudAdd.dest).toEqual(drive.trail);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
  });

  it("keeps an external torrent queued throughout the native manual file picker", async () => {
    drive.status = status();
    const chooser = deferred<string | null>();
    backend.open.mockReturnValue(chooser.promise);
    const picking = pickTorrent();
    add({ id: 12, kind: "torrent", value: "/tmp/external.torrent" });
    await idle();
    expect(cloudAdd.torrent).toBe(null);
    expect(backend.pending.map((p) => p.id)).toEqual([12]);

    chooser.resolve("/tmp/manual.torrent");
    await picking;
    await idle();
    expect(cloudAdd.torrent?.path).toBe("/tmp/manual.torrent");
    expect(backend.pending.map((p) => p.id)).toEqual([12]);

    clearTorrent();
    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/external.torrent"));
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
  });

  it("keeps a successor read busy after a manual read is invalidated by an account switch", async () => {
    drive.status = status();
    const manual = deferred<TorrentMeta>();
    const external = deferred<TorrentMeta>();
    backend.open.mockResolvedValue("/tmp/stale-manual.torrent");
    backend.read = (path) => path === "/tmp/stale-manual.torrent" ? manual.promise : external.promise;
    const picking = pickTorrent();
    await idle();
    add({ id: 14, kind: "torrent", value: "/tmp/current-external.torrent" });

    expect(await switchAccount("b")).toBe(true);
    manual.resolve(meta("/tmp/stale-manual.torrent"));
    await picking;
    await idle();
    expect(cloudAdd.torrent).toBe(null);
    expect(cloudAdd.reading).toBe(true);
    expect(backend.pending.map((p) => p.id)).toEqual([14]);

    external.resolve(meta("/tmp/current-external.torrent"));
    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/current-external.torrent"));
    expect(drive.status?.accountId).toBe("b");
    expect(cloudAdd.dest).toEqual(drive.trail);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
  });

  it.each(["cancel", "error"])("continues queued requests after a native picker %s", async (outcome) => {
    drive.status = status();
    const chooser = deferred<string | null>();
    backend.open.mockReturnValue(chooser.promise);
    const picking = pickTorrent();
    add({ id: 13, kind: "magnet", value: magnet });
    await idle();
    expect(cloudAdd.draft).toBe("");
    expect(backend.pending.map((p) => p.id)).toEqual([13]);

    if (outcome === "error") {
      const rejected = expect(picking).rejects.toThrow("Picker failed");
      chooser.reject(new Error("Picker failed"));
      await rejected;
    } else {
      chooser.resolve(null);
      await picking;
    }
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));
    expect(cloudAdd.reading).toBe(false);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
  });

  it("reports an unreadable torrent without blocking the next external link", async () => {
    drive.status = status();
    backend.read = async () => { throw new Error("种子文件已被删除"); };
    backend.pending = [
      { id: 6, kind: "torrent", value: "/tmp/deleted.torrent" },
      { id: 7, kind: "magnet", value: magnet },
    ];
    await watchExternalOpen();
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));
    expect(ui.message).toContain("种子文件已被删除");
    expect(cloudAdd.torrent).toBe(null);
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    expect(backend.calls).not.toContain("AddOfflineTasks");
  });
});

describe("foreground clipboard share detection", () => {
  async function activate() {
    vi.mocked(document.hasFocus).mockReturnValue(true);
    window.dispatchEvent(new Event("focus"));
    await idle();
  }
  function leave() {
    vi.mocked(document.hasFocus).mockReturnValue(false);
    window.dispatchEvent(new Event("blur"));
  }

  it("waits for the cold-start receiving account before opening without a paste", async () => {
    const link = "https://mypikpak.com/s/Clipboard_start";
    accounts.list = [];
    drive.status = status();
    backend.clipboard.mockResolvedValue(link);
    await activate();
    expect(backend.clipboard).not.toHaveBeenCalled();
    await loadAccounts();
    await vi.waitFor(() => expect(shareImport.url).toBe(link));
    expect(shareImport.account).toBe("a");
    expect(backend.calls).not.toContain("RestoreShare");
    expect(backend.calls).not.toContain("AddOfflineTasks");
  });

  it("does not reopen a canceled clipboard share, but permits an explicit reopen", async () => {
    const link = "https://mypikpak.com/s/Clipboard_cancel";
    drive.status = status();
    backend.clipboard.mockResolvedValue(link);
    await activate();
    await vi.waitFor(() => expect(shareImport.url).toBe(link));
    closeShareImport();
    leave();
    await activate();
    document.dispatchEvent(new Event("visibilitychange"));
    await idle();
    expect(shareImport.open).toBe(false);
    expect(shareImport.url).toBe("");
    expect(openPikPakShare(link)).toBe(true);
    expect(shareImport.url).toBe(link);
  });

  it("keeps unchanged clipboard content dismissed after opening a different explicit share", async () => {
    const link = "https://mypikpak.com/s/Clipboard_unchanged";
    drive.status = status();
    backend.clipboard.mockResolvedValue(link);
    await activate();
    await vi.waitFor(() => expect(shareImport.url).toBe(link));
    closeShareImport();
    expect(openPikPakShare("https://mypikpak.com/s/Explicit_other")).toBe(true);
    closeShareImport();
    leave();
    await activate();
    expect(shareImport.open).toBe(false);
  });

  it("waits for the pending-request snapshot before checking clipboard after accounts become ready", async () => {
    const ready = deferred<void>();
    accounts.list = [];
    backend.pending = [{ id: 90, kind: "share", value: "https://mypikpak.com/s/Explicit_first" }];
    backend.pendingRead = async () => { await ready.promise; return backend.pending.map(p => ({ ...p })); };
    backend.clipboard.mockResolvedValue("https://mypikpak.com/s/Clipboard_later");
    drive.status = status();
    await activate();
    await loadAccounts();
    await idle();
    expect(shareImport.open).toBe(false);
    expect(backend.clipboard).not.toHaveBeenCalled();
    ready.resolve(undefined);
    await vi.waitFor(() => expect(shareImport.url).toBe("https://mypikpak.com/s/Explicit_first"));
    expect(backend.clipboard).not.toHaveBeenCalled();
  });

  it("remembers manually opened shares and detects a different copied share without clearing torrent input", async () => {
    const link = "https://mypikpak.com/s/Clipboard_manual";
    drive.status = status();
    cloudAdd.torrent = meta("/tmp/kept.torrent");
    cloudAdd.picked = new Set([0]);
    cloudAdd.draft = magnet;
    expect(openPikPakShare(link)).toBe(true);
    closeShareImport();
    backend.clipboard.mockResolvedValue(link);
    await activate();
    expect(shareImport.open).toBe(false);
    backend.clipboard.mockResolvedValue("https://mypikpak.com/s/Clipboard_next");
    leave();
    await activate();
    await vi.waitFor(() => expect(shareImport.url).toBe("https://mypikpak.com/s/Clipboard_next"));
    expect(cloudAdd.torrent?.path).toBe("/tmp/kept.torrent");
    expect([...cloudAdd.picked]).toEqual([0]);
    expect(cloudAdd.draft).toBe(magnet);
  });

  it.each(["locked", "signedout", "signin", "cloud-add", "other-sheet", "hidden"])("does not read the clipboard while %s", async kind => {
    drive.status = status();
    backend.clipboard.mockResolvedValue(`https://mypikpak.com/s/Clipboard_${kind}`);
    const scrim = document.createElement("div");
    if (kind === "locked") appLock.state = { locked: true } as AppLockState;
    if (kind === "signedout") drive.status = null;
    if (kind === "signin") accounts.dialog = { kind: "signin", account: "" };
    if (kind === "cloud-add") { ui.cloudAddOpen = true; cloudAdd.draft = magnet; }
    if (kind === "other-sheet") { scrim.className = "scrim"; document.body.append(scrim); }
    if (kind === "hidden") vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    try {
      await activate();
      expect(backend.clipboard).not.toHaveBeenCalled();
      expect(shareImport.open).toBe(false);
    } finally { scrim.remove(); }
  });

  it("drops a clipboard reply after blur and reads the current value on the next activation", async () => {
    drive.status = status();
    const late = deferred<string>();
    backend.clipboard.mockReturnValueOnce(late.promise);
    await activate();
    await vi.waitFor(() => expect(backend.clipboard).toHaveBeenCalled());
    leave();
    late.resolve("https://mypikpak.com/s/Clipboard_stale");
    await idle();
    expect(shareImport.open).toBe(false);
    backend.clipboard.mockResolvedValue("https://mypikpak.com/s/Clipboard_current");
    await activate();
    await vi.waitFor(() => expect(shareImport.url).toBe("https://mypikpak.com/s/Clipboard_current"));
  });

  it("does not replace an occupied share with a clipboard reply", async () => {
    drive.status = status();
    const late = deferred<string>();
    backend.clipboard.mockReturnValueOnce(late.promise);
    await activate();
    expect(openPikPakShare("https://mypikpak.com/s/Clipboard_keep")).toBe(true);
    late.resolve("https://mypikpak.com/s/Clipboard_replace");
    await idle();
    expect(shareImport.url).toBe("https://mypikpak.com/s/Clipboard_keep");
  });

  it("ignores clipboard failures and non-share content without surfacing an error", async () => {
    drive.status = status();
    backend.clipboard.mockRejectedValueOnce(new Error("clipboard unavailable"));
    await activate();
    expect(shareImport.open).toBe(false);
    expect(ui.message).toBe("");
    backend.clipboard.mockResolvedValue("https://foreign.example/s/not-a-share");
    leave();
    await activate();
    expect(shareImport.open).toBe(false);
    expect(backend.calls).not.toContain("InspectShare");
  });
});

describe("share intake alongside cloud add", () => {
  const link = "https://mypikpak.com/s/Share_123";

  it("stages the next share in the new account after accepting discard", async () => {
    drive.status = status();
    expect(openPikPakShare("https://mypikpak.com/s/Before_switch")).toBe(true);
    add({ id: 91, kind: "share", value: "https://mypikpak.com/s/After_switch" });
    await idle();
    const switching = switchAccount("b");
    await vi.waitFor(() => expect(accounts.dialog?.kind).toBe("leave"));
    const question = accounts.dialog;
    if (question?.kind !== "leave") throw new Error("Missing discard confirmation");
    question.resolve(true);
    expect(await switching).toBe(true);
    await vi.waitFor(() => expect(shareImport.url).toBe("https://mypikpak.com/s/After_switch"));
    expect(shareImport.account).toBe("b");
    expect(backend.calls).not.toContain("RestoreShare");
  });

  it("does not acknowledge the queued share while Add Account is being installed", async () => {
    drive.status = status();
    expect(openPikPakShare("https://mypikpak.com/s/Before_signin")).toBe(true);
    add({ id: 92, kind: "share", value: "https://mypikpak.com/s/After_signin" });
    await idle();
    const signing = openSignIn();
    await vi.waitFor(() => expect(accounts.dialog?.kind).toBe("leave"));
    const question = accounts.dialog;
    if (question?.kind !== "leave") throw new Error("Missing discard confirmation");
    question.resolve(true);
    await signing;
    await idle();
    expect(accounts.dialog?.kind).toBe("signin");
    expect(shareImport.open).toBe(false);
    expect(backend.pending.map(p => p.id)).toEqual([92]);
    backend.account = "b";
    await completeQRSignIn({ status: status("b"), account: { id: "b", label: "Account B" }, existed: false } as SignInResult);
    await vi.waitFor(() => expect(shareImport.url).toBe("https://mypikpak.com/s/After_signin"));
    expect(shareImport.account).toBe("b");
  });

  it("does not resume an earlier cancellation when discarding another draft during account switching", async () => {
    drive.status = status();
    backend.pending = [
      { id: 93, kind: "share", value: "https://mypikpak.com/s/Cancel_first" },
      { id: 94, kind: "share", value: "https://mypikpak.com/s/Still_paused" },
    ];
    await watchExternalOpen();
    await vi.waitFor(() => expect(shareImport.url).toBe("https://mypikpak.com/s/Cancel_first"));
    closeShareImport();
    expect(openPikPakShare("https://mypikpak.com/s/Unrelated_draft")).toBe(true);
    const switching = switchAccount("b");
    await vi.waitFor(() => expect(accounts.dialog?.kind).toBe("leave"));
    const question = accounts.dialog;
    if (question?.kind !== "leave") throw new Error("Missing discard confirmation");
    question.resolve(true);
    expect(await switching).toBe(true);
    await idle();
    expect(externalOpen.paused).toBe(true);
    expect(shareImport.open).toBe(false);
    expect(backend.pending.map(p => p.id)).toEqual([94]);
  });

  it("resumes queued shares after account cleanup closes a completed receipt", async () => {
    drive.status = status();
    expect(openPikPakShare("https://mypikpak.com/s/Receipt_finished")).toBe(true);
    shareImport.attempted = true;
    shareImport.result = { status: "complete", parentId: "dest", fileIds: ["saved"], taskId: "", message: "" };
    add({ id: 95, kind: "share", value: "https://mypikpak.com/s/After_receipt" });
    await idle();
    expect(await switchAccount("b")).toBe(true);
    await vi.waitFor(() => expect(shareImport.url).toBe("https://mypikpak.com/s/After_receipt"));
    expect(shareImport.account).toBe("b");
    expect(backend.pending).toEqual([]);
  });

  it("retains a cold-start share until the receiving account list is ready", async () => {
    accounts.list = [];
    backend.pending = [{ id: 44, kind: "share", value: link }];
    drive.status = status();
    await watchExternalOpen();
    await idle();
    expect(shareImport.open).toBe(false);
    expect(backend.pending.map(p => p.id)).toEqual([44]);
    expect(backend.calls).not.toContain("InspectShare");

    await loadAccounts();
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    expect(shareImport.account).toBe("a");
    expect(shareImport.info?.title).toBe("Travel collection");
    expect(shareImport.error).toBe("");
    expect(backend.calls).not.toContain("RestoreShare");
  });

  it.each(["complete", "cancel"])("retains incoming shares until an account sign-in dialog settles (%s)", async (outcome) => {
    drive.status = status();
    accounts.dialog = { kind: "signin", account: "" };
    add({ id: 43, kind: "share", value: link });
    await idle();
    expect(shareImport.open).toBe(false);
    expect(backend.pending.map(p => p.id)).toEqual([43]);
    if (outcome === "complete") {
      backend.account = "b";
      await completeQRSignIn({ status: status("b"), account: { id: "b", label: "Account B" }, existed: false } as SignInResult);
    } else accounts.dialog = null;
    await vi.waitFor(() => expect(shareImport.url).toBe(link));
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    expect(shareImport.account).toBe(outcome === "complete" ? "b" : "a");
    expect(backend.calls).not.toContain("RestoreShare");
  });

  it("hands keyboard focus to the read-only destination picker and restores it on close", async () => {
    drive.status = status();
    await mountSheet(RelocateSheet);
    const trigger = document.createElement("button");
    document.body.append(trigger);
    unmounts.add(() => trigger.remove());
    trigger.focus();
    openShareFolderPicker("b", [], () => undefined);
    await idle();
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(document.activeElement).toBe(dialog.querySelector('input[type="search"]'));
    const first = dialog.querySelector<HTMLButtonElement>("button")!;
    first.focus();
    first.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
    const buttons = Array.from(dialog.querySelectorAll<HTMLButtonElement>("button:not(:disabled)"));
    expect(document.activeElement).toBe(buttons.at(-1));
    expect(await createRelocateFolder("Unconfirmed folder")).toBe(false);
    const lockField = document.createElement("input");
    lockField.setAttribute("data-lock-screen", "");
    document.body.append(lockField);
    unmounts.add(() => lockField.remove());
    appLock.state = { locked: true } as AppLockState;
    lockField.focus();
    const tab = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });
    lockField.dispatchEvent(tab);
    expect(tab.defaultPrevented).toBe(false);
    expect(document.activeElement).toBe(lockField);
    appLock.state.locked = false;
    expect(backend.calls).not.toContain("CreateFolder");
    closeRelocate();
    await nextTick();
    expect(document.activeElement).toBe(trigger);
  });

  it("opens the share preview instead of the retained torrent without submitting either", async () => {
    drive.status = status();
    cloudAdd.torrent = meta("/tmp/retained.torrent");
    cloudAdd.picked = new Set([0]);
    add({ id: 40, kind: "share", value: link });
    await vi.waitFor(() => expect(shareImport.open).toBe(true));
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    expect(shareImport.url).toBe(link);
    expect(ui.cloudAddOpen).toBe(false);
    expect(cloudAdd.torrent?.path).toBe("/tmp/retained.torrent");
    expect([...cloudAdd.picked]).toEqual([0]);
    expect(backend.calls).not.toContain("RestoreShare");
    expect(backend.calls).not.toContain("AddOfflineTasks");
  });

  it("discards a share without reopening it or immediately presenting its queued successor", async () => {
    drive.status = status();
    backend.pending = [{ id: 41, kind: "share", value: link }, { id: 42, kind: "share", value: `${link}_next` }];
    await watchExternalOpen();
    await vi.waitFor(() => expect(backend.pending.map((p) => p.id)).toEqual([42]));
    closeShareImport();
    await idle();
    expect(shareImport.url).toBe("");
    expect(shareImport.open).toBe(false);
    expect(ui.cloudAddOpen).toBe(false);
    expect(backend.pending.map((p) => p.id)).toEqual([42]);
    emit();
    await vi.waitFor(() => expect(shareImport.url).toBe(`${link}_next`));
  });

  it("routes a pasted share from the link field before it can become an offline task", async () => {
    drive.status = status();
    await mountSheet();
    cloudAdd.tab = "link";
    ui.cloudAddOpen = true;
    await nextTick();
    const input = document.querySelector<HTMLTextAreaElement>("textarea")!;
    const paste = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(paste, "clipboardData", { value: { getData: () => link } });
    input.dispatchEvent(paste);
    await idle();
    expect(paste.defaultPrevented).toBe(true);
    expect(shareImport.url).toBe(link);
    expect(shareImport.open).toBe(true);
    expect(cloudAdd.draft).toBe("");
    expect(ui.cloudAddOpen).toBe(false);
    expect(backend.calls).not.toContain("AddOfflineTasks");
  });

  it("keeps a mixed link batch for later while previewing its share", async () => {
    drive.status = status();
    await mountSheet();
    cloudAdd.tab = "link";
    cloudAdd.draft = `${magnet}\n${link}`;
    ui.cloudAddOpen = true;
    await nextTick();
    sheetButton("预览分享").click();
    await idle();
    expect(shareImport.url).toBe(link);
    expect(cloudAdd.draft).toBe(magnet);
    expect(backend.calls).not.toContain("AddOfflineTasks");
  });
});

describe("discarding cloud-add input", () => {
  it("clears links without submitting or removing a retained torrent", async () => {
    drive.status = status();
    await mountSheet();
    cloudAdd.torrent = meta("/tmp/retained.torrent");
    cloudAdd.picked = new Set([0]);
    cloudAdd.dest = [{ id: "chosen", name: "Chosen destination" }];
    add({ id: 30, kind: "magnet", value: magnet });
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));
    await vi.waitFor(() => expect(backend.pending).toEqual([]));

    sheetButton("清空链接").click();
    await idle();
    expect(document.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("");
    expect(sheetButton("添加 0 项").disabled).toBe(true);
    expect(cloudAdd.torrent?.path).toBe("/tmp/retained.torrent");
    expect([...cloudAdd.picked]).toEqual([0]);
    expect(cloudAdd.dest.map((p) => p.id)).toEqual(["chosen"]);
    sheetButton("关闭").click();
    await nextTick();
    ui.cloudAddOpen = true;
    await idle();
    expect(document.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("");
    expect(backend.calls).not.toContain("AddOfflineTasks");
  });

  it("coalesces repeated opens so deleting the link does not fill it again", async () => {
    drive.status = status();
    await mountSheet();
    add({ id: 31, kind: "magnet", value: magnet });
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    add({ id: 32, kind: "magnet", value: magnet });
    await idle();
    const input = document.querySelector<HTMLTextAreaElement>("textarea")!;
    input.value = "";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await idle();
    expect(input.value).toBe("");
    expect(cloudAdd.draft).toBe("");
    expect(backend.pending).toEqual([]);
  });

  it("cancels the current links and waits until reopening before staging the next", async () => {
    drive.status = status();
    await mountSheet();
    cloudAdd.torrent = meta("/tmp/retained.torrent");
    cloudAdd.picked = new Set([0]);
    backend.pending = [
      { id: 33, kind: "magnet", value: magnet },
      { id: 34, kind: "magnet", value: `${magnet}&next=1` },
    ];
    await watchExternalOpen();
    await vi.waitFor(() => expect(externalOpen.pending.map((p) => p.id)).toEqual([34]));

    sheetButton("取消").click();
    await idle();
    expect(ui.cloudAddOpen).toBe(false);
    expect(cloudAdd.draft).toBe("");
    expect(cloudAdd.torrent?.path).toBe("/tmp/retained.torrent");
    expect([...cloudAdd.picked]).toEqual([0]);
    expect(backend.pending.map((p) => p.id)).toEqual([34]);
    ui.cloudAddOpen = true;
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(`${magnet}&next=1`));
    expect(backend.calls).not.toContain("AddOfflineTasks");
  });

  it("cancels a torrent without discarding the other tab's edited links", async () => {
    drive.status = status();
    await mountSheet();
    cloudAdd.draft = "https://example.com/keep.mp4";
    cloudAdd.torrent = meta("/tmp/discard.torrent");
    cloudAdd.picked = new Set([0]);
    cloudAdd.tab = "torrent";
    ui.cloudAddOpen = true;
    await nextTick();
    sheetButton("取消").click();
    await idle();
    expect(ui.cloudAddOpen).toBe(false);
    expect(cloudAdd.torrent).toBe(null);
    expect([...cloudAdd.picked]).toEqual([]);
    expect(cloudAdd.draft).toBe("https://example.com/keep.mp4");
    expect(backend.calls).not.toContain("AddTorrent");
  });

  it.each(["reading", "submitting"] as const)("does not discard input while %s", async (busy) => {
    drive.status = status();
    await mountSheet();
    cloudAdd.draft = magnet;
    cloudAdd.tab = "link";
    cloudAdd[busy] = true;
    ui.cloudAddOpen = true;
    await nextTick();
    expect(sheetButton("清空链接").disabled).toBe(true);
    expect(sheetButton("取消").disabled).toBe(true);
    sheetButton("清空链接").click();
    sheetButton("取消").click();
    expect(cloudAdd.draft).toBe(magnet);
    expect(ui.cloudAddOpen).toBe(true);
  });

  it("keeps a draft when closed rather than cancelled", async () => {
    drive.status = status();
    await mountSheet();
    cloudAdd.draft = magnet;
    cloudAdd.tab = "link";
    ui.cloudAddOpen = true;
    await nextTick();
    sheetButton("关闭").click();
    await nextTick();
    expect(ui.cloudAddOpen).toBe(false);
    ui.cloudAddOpen = true;
    await nextTick();
    expect(document.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(magnet);
  });

  it("resumes from a new external open after cancelling instead of keeping the queue paused", async () => {
    drive.status = status();
    await mountSheet();
    add({ id: 35, kind: "magnet", value: magnet });
    await vi.waitFor(() => expect(backend.pending).toEqual([]));
    sheetButton("取消").click();
    await idle();
    add({ id: 36, kind: "magnet", value: magnet });
    await vi.waitFor(() => expect(cloudAdd.draft).toBe(magnet));
    expect(ui.cloudAddOpen).toBe(true);
    expect(cloudAdd.tab).toBe("link");
  });
});
