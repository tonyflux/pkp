import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, type App } from "vue";
import type { Account, DriveStatus, Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  list: vi.fn(),
  play: vi.fn(),
  save: vi.fn(),
  accounts: vi.fn(),
  status: vi.fn(),
  driveList: vi.fn(),
  removeAccount: vi.fn(),
  stopPlayback: vi.fn(),
  openWindow: vi.fn(),
}));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: new Proxy({}, {
    get: (_target, name) =>
      name === "ListAllAccounts" ? backend.list
        : name === "PlayFile" ? backend.play
          : name === "SavePlayback" ? backend.save
            : name === "Accounts" ? backend.accounts
              : name === "Status" ? backend.status
                : name === "List" ? backend.driveList
                  : name === "RemoveAccount" ? backend.removeAccount
                    : vi.fn().mockResolvedValue(null),
  }),
  PlayerService: new Proxy({}, {
    get: (_target, name) =>
      name === "StopPlayback" ? backend.stopPlayback
        : name === "OpenWindow" ? backend.openWindow
          : vi.fn().mockResolvedValue(null),
  }),
  AppearanceService: {},
}));
import AggregateFiles from "./components/AggregateFiles.vue";
import { accounts, drive, entryKey, loadAccounts, openLocalSearch, openView, play, playAt, player, ui } from "./store";

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
  accounts.switching = "";
  drive.status = { accountId: "a", configured: true } as DriveStatus;
  ui.route = "files"; ui.view = "all"; ui.scope = "accounts"; ui.query = ""; ui.category = "all"; ui.layout = "grid";
  player.presentation = "off"; player.owner = ""; player.playlist = []; player.index = -1; player.returnRoute = "files";
  backend.list.mockReset(); backend.play.mockReset().mockResolvedValue("url"); backend.save.mockReset().mockResolvedValue(true);
  backend.accounts.mockReset().mockResolvedValue([account("a"), account("b")]);
  backend.status.mockReset().mockResolvedValue({ accountId: "a", configured: true });
  backend.driveList.mockReset().mockResolvedValue({ entries: [], total: 0, offset: 0, live: false, nextPageToken: "" });
  backend.removeAccount.mockReset();
  backend.stopPlayback.mockReset().mockResolvedValue(undefined);
  backend.openWindow.mockReset().mockResolvedValue(undefined);
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

  it("ignores Enter on card and row action buttons while activating the card or row itself", async () => {
    backend.list.mockResolvedValue({ entries: [{ account: "a", entry: a }], accounts: [{ account: "a", state: "ready", error: "" }], total: 1, next: "" });
    host = document.createElement("div"); document.body.append(host); app = createApp(AggregateFiles); app.mount(host);
    await vi.advanceTimersByTimeAsync(180); await settle();

    const card = host.querySelector<HTMLElement>("article.card")!;
    const cardButton = card.querySelector<HTMLButtonElement>('[title="下载到电脑"]')!;
    const buttonEnter = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    cardButton.dispatchEvent(buttonEnter);
    expect(buttonEnter.defaultPrevented).toBe(false);
    expect(backend.play).not.toHaveBeenCalled();

    const cardEnter = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    card.dispatchEvent(cardEnter);
    await settle();
    expect(cardEnter.defaultPrevented).toBe(true);
    expect(backend.play).toHaveBeenCalledTimes(1);

    backend.play.mockClear();
    ui.layout = "list";
    await settle();
    const row = host.querySelector<HTMLElement>("tbody tr")!;
    const rowButton = row.querySelector<HTMLButtonElement>('[title="下载到电脑"]')!;
    const rowBtnEnter = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    rowButton.dispatchEvent(rowBtnEnter);
    expect(rowBtnEnter.defaultPrevented).toBe(false);
    expect(backend.play).not.toHaveBeenCalled();
  });

  it("resets accounts scope when opening local search, dropping to one account, or skipping single-account reload in openView", async () => {
    openLocalSearch("类型:视频");
    expect(ui.scope).toBe("folder");
    expect(ui.view).toBe("all");
    expect(ui.query).toBe("类型:视频");
    expect(backend.driveList).toHaveBeenCalledTimes(1);

    backend.driveList.mockClear();
    ui.scope = "accounts";
    await openView("starred");
    expect(backend.driveList).toHaveBeenCalledTimes(1);
    backend.driveList.mockClear();
    await openView("all");
    expect(backend.driveList).not.toHaveBeenCalled();

    backend.accounts.mockResolvedValue([account("a")]);
    await loadAccounts();
    expect(ui.scope).toBe("folder");
    expect(backend.driveList).toHaveBeenCalledTimes(1);
  });

  it("rolls back player presentation/route on early cancel and stops playback if switched during PlayFile", async () => {
    ui.route = "history";
    player.presentation = "bar";
    player.owner = "a";
    player.playlist = [a];
    player.index = 0;

    let resolvePlay!: (value: string) => void;
    backend.play.mockImplementationOnce(() => new Promise((done) => { resolvePlay = done; }));
    const pending = play(a, [a]);
    await nextTick();
    expect(ui.route).toBe("player");
    expect(player.presentation).toBe("immersive");

    accounts.switching = "b";
    resolvePlay("url");
    expect(await pending).toBe("cancelled");
    expect(player.owner).toBe("a");
    expect(player.presentation).toBe("bar");
    expect(ui.route).toBe("history");
    await settle();
    expect(backend.stopPlayback).toHaveBeenCalled();

    accounts.switching = "";
    player.presentation = "window";
    player.owner = "a";
    player.playlist = [a];
    player.index = 0;
    backend.openWindow.mockRejectedValueOnce(new Error("window failed"));
    expect(await play(a, [a])).toBe("failed");
    expect(player.presentation).toBe("window");
    expect(ui.route).toBe("history");
  });
});
