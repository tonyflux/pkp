import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, type App } from "vue";
import type { Account, DriveStatus, Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  list: vi.fn(),
  play: vi.fn(),
  save: vi.fn(),
}));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: new Proxy({}, { get: (_target, name) => name === "ListAllAccounts" ? backend.list : name === "PlayFile" ? backend.play : name === "SavePlayback" ? backend.save : vi.fn().mockResolvedValue(null) }),
  PlayerService: new Proxy({}, { get: () => vi.fn().mockResolvedValue(null) }),
  AppearanceService: {},
}));
import AggregateFiles from "./components/AggregateFiles.vue";
import { accounts, drive, entryKey, play, playAt, player, ui } from "./store";

const account = (id: string) => ({ id, login: `${id}@example.com`, label: id.toUpperCase(), needsPassword: false } as Account);
const file = (account: string, name: string) => ({ account, id: "same-id", name, parentId: "", path: "全部文件", category: "video", playable: true, folder: false, size: 42, modifiedTime: "", thumbnail: "", duration: 0, preview: "", starred: false, trashed: false } as Entry & { account: string });
const a = file("a", "alpha.mp4");
const b = file("b", "beta.mp4");
let app: App | undefined;
let host: HTMLElement;

async function settle() { for (let i = 0; i < 15; i++) await Promise.resolve(); await nextTick(); }
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  accounts.list = [account("a"), account("b")];
  drive.status = { accountId: "a", configured: true } as DriveStatus;
  ui.route = "files"; ui.view = "all"; ui.scope = "accounts"; ui.query = ""; ui.category = "all"; ui.layout = "grid";
  player.presentation = "off"; player.owner = ""; player.playlist = []; player.index = -1;
  backend.list.mockReset(); backend.play.mockReset().mockResolvedValue("url"); backend.save.mockReset().mockResolvedValue(true);
});
afterEach(() => {
  app?.unmount(); app = undefined; document.body.replaceChildren();
  vi.clearAllTimers(); vi.useRealTimers(); vi.unstubAllGlobals();
});

describe("all-accounts files", () => {
  it("keeps owners distinct and sends cross-account queue items to their owners", async () => {
    backend.list.mockResolvedValue({ entries: [{ account: "a", entry: a }, { account: "b", entry: b }], accounts: [{ account: "a", state: "ready", error: "" }, { account: "b", state: "index", error: "" }], total: 2, next: "" });
    host = document.createElement("div"); document.body.append(host); app = createApp(AggregateFiles); app.mount(host);
    await vi.advanceTimersByTimeAsync(180); await settle();
    expect(host.textContent).toContain("alpha.mp4");
    expect(host.textContent).toContain("beta.mp4");
    expect(host.textContent).toContain("B · 索引尚未就绪");
    expect(entryKey(a)).not.toBe(entryKey(b));
    expect(host.querySelectorAll(".card")).toHaveLength(2);
    expect(await play(a, [a, b])).toBe("opened");
    expect(backend.play).toHaveBeenLastCalledWith("a", "same-id");
    await playAt(1);
    expect(backend.play).toHaveBeenLastCalledWith("b", "same-id");
    expect(player.owner).toBe("b");
    expect(drive.status?.accountId).toBe("a");
  });

  it("keeps the previous queue owner when switching tracks fails", async () => {
    expect(await play(a, [a, b])).toBe("opened");
    backend.play.mockRejectedValueOnce(new Error("offline"));
    await playAt(1);
    expect(player.owner).toBe("a");
    expect(player.index).toBe(0);
  });

  it("restores the prior playback session if replacing it fails", async () => {
    expect(await play(a, [a, b])).toBe("opened");
    backend.play.mockRejectedValueOnce(new Error("offline"));
    const replacing = play(b, [a, b]);
    if (accounts.dialog?.kind !== "replace") throw new Error("expected replacement confirmation");
    accounts.dialog.resolve(true);
    expect(await replacing).toBe("failed");
    expect(player.owner).toBe("a");
    expect(player.index).toBe(0);
    expect(entryKey(player.playlist[0])).toBe(entryKey(a));
  });

  it("discards a late page after the query changes", async () => {
    let resolve!: (value: unknown) => void;
    backend.list.mockImplementationOnce(() => new Promise((done) => { resolve = done; })).mockResolvedValueOnce({ entries: [{ account: "b", entry: b }], accounts: [], total: 1, next: "" });
    host = document.createElement("div"); document.body.append(host); app = createApp(AggregateFiles); app.mount(host);
    await vi.advanceTimersByTimeAsync(180);
    ui.query = "beta";
    await nextTick();
    resolve({ entries: [{ account: "a", entry: a }], accounts: [], total: 1, next: "" });
    await settle();
    expect(host.textContent).not.toContain("alpha.mp4");
    await vi.advanceTimersByTimeAsync(180); await settle();
    expect(host.textContent).toContain("beta.mp4");
  });
});
