import { beforeEach, describe, expect, it, vi } from "vitest";
import { nextTick } from "vue";
import type { AppLockState, DriveStatus, ExternalOpen, TorrentMeta } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  pending: [] as ExternalOpen[],
  account: "a",
  open: vi.fn<() => Promise<string | null>>(),
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
    if (name === "Pending") return backend.pending.map((p) => ({ ...p }));
    if (name === "Ack") backend.pending = backend.pending.filter((p) => p.id !== args[0]);
    if (name === "ReadTorrent") return backend.read!(args[0] as string);
    if (name === "SwitchAccount") backend.account = args[0] as string;
    if (name === "SwitchAccount" || name === "Status") return { configured: true, accountId: backend.account };
    if (name === "Accounts") return [{ id: "a", label: "Account A" }, { id: "b", label: "Account B" }];
    return null;
  } });
  return { DriveService: service, ExternalOpenService: service, PlayerService: service, AppearanceService: service, AppLockService: service, MenuBarService: service, NotifyService: service, TelemetryService: service };
});

import { accounts, appLock, clearTorrent, cloudAdd, drive, externalOpen, pickTorrent, switchAccount, ui, watchExternalOpen } from "./store";

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

beforeEach(async () => {
  backend.pending = [];
  backend.account = "a";
  backend.open.mockReset().mockResolvedValue(null);
  backend.read = async (path) => meta(path);
  drive.status = null;
  accounts.switching = "";
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

  it("does not replace an edited draft when a torrent arrives in the running app", async () => {
    drive.status = status();
    cloudAdd.draft = "https://example.com/my-draft.mp4";
    add({ id: 2, kind: "torrent", value: "/tmp/second.torrent" });
    await vi.waitFor(() => expect(externalOpen.pending.map((p) => p.id)).toEqual([2]));
    expect(cloudAdd.draft).toBe("https://example.com/my-draft.mp4");
    expect(backend.calls).not.toContain("ReadTorrent");
    expect(ui.cloudAddOpen).toBe(true);
    cloudAdd.draft = "";
    await vi.waitFor(() => expect(cloudAdd.torrent?.path).toBe("/tmp/second.torrent"));
    expect([...cloudAdd.picked]).toEqual([0, 1]);
    expect(cloudAdd.tab).toBe("torrent");
    expect(backend.calls).not.toContain("AddTorrent");
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
