import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlayHistoryPage, RecentPlay } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => {
  const answers = new Map<string, (...args: unknown[]) => unknown>();
  const calls: { name: string; args: unknown[] }[] = [];
  const service = () => new Proxy({}, { get: (_target, name: string) => (...args: unknown[]) => {
    calls.push({ name, args });
    try { return Promise.resolve(answers.get(name)?.(...args) ?? null); }
    catch (err) { return Promise.reject(err); }
  } });
  return { answers, calls, service };
});
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: backend.service(), PlayerService: backend.service(),
  AppearanceService: backend.service(), AppLockService: backend.service(),
  MenuBarService: backend.service(), NotifyService: backend.service(), TelemetryService: backend.service(),
}));

import { clearPlayHistory, drive, loadPlayHistory, loadPlays, playHistory, player, resumePlay, ui } from "./store";

const record = (id: string, position = 40, duration = 100): RecentPlay => ({
  fileId: id, name: `${id}.mp4`, thumbnail: "", position, duration,
  playedAt: "2026-10-22T08:00:00Z", available: true, local: true,
});
const remote = (id: string, position = 40): RecentPlay => ({ ...record(id, position), local: false });
const page = (plays: RecentPlay[] | null = [], nextPageToken = "", account = "a"): PlayHistoryPage => ({ account, plays, nextPageToken, unavailableIds: [] });
const ids = () => playHistory.list.map((play) => play.fileId).sort();
const calls = (name: string) => backend.calls.filter((call) => call.name === name);

beforeEach(() => {
  backend.answers.clear();
  backend.calls.length = 0;
  drive.status = { accountId: "a", configured: true } as typeof drive.status;
  drive.plays = [record("old")];
  drive.playsLoaded = true;
  Object.assign(playHistory, {
    account: "a", list: [record("old")], loaded: true, pages: 0,
    loading: false, localLoading: false, syncing: false, clearing: false, error: "",
  });
  ui.route = "history";
  player.owner = "";
  player.presentation = "off";
  player.playlist = [];
  backend.answers.set("Status", () => drive.status);
  backend.answers.set("LocalPlayHistory", () => []);
  backend.answers.set("PlayHistoryPage", (account) => page([], "", String(account)));
  backend.answers.set("RecentPlays", () => []);
  backend.answers.set("ClearPlays", () => null);
});
afterEach(() => vi.restoreAllMocks());

describe("playback history", () => {
  it("shows local records before cloud history finishes", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const localResult = Promise.withResolvers<RecentPlay[]>();
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const cloudResult = Promise.withResolvers<PlayHistoryPage>();
    backend.answers.set("LocalPlayHistory", () => localResult.promise);
    backend.answers.set("PlayHistoryPage", () => cloudResult.promise);
    Object.assign(playHistory, { list: [], loaded: false });
    const pending = loadPlayHistory(true);
    expect(playHistory.localLoading).toBe(true);
    expect(playHistory.loading).toBe(true);
    localResult.resolve([record("local", 75)]);
    await vi.waitFor(() => expect(playHistory.localLoading).toBe(false));
    expect(playHistory.account).toBe("a");
    expect(ids()).toEqual(["local"]);
    expect(playHistory.list[0].position).toBe(75);
    expect(playHistory.localLoading).toBe(false);
    expect(playHistory.loading).toBe(true);
    expect(playHistory.syncing).toBe(true);
    cloudResult.resolve(page([remote("cloud")]));
    await pending;
    expect(ids()).toEqual(["cloud", "local"]);
    expect(playHistory.loading).toBe(false);
    expect(playHistory.syncing).toBe(false);
    expect(playHistory.loaded).toBe(true);
  });

  it("shows each cloud page while later pages wait and keeps the latest per-file progress", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const nextResult = Promise.withResolvers<PlayHistoryPage>();
    backend.answers.set("LocalPlayHistory", () => [record("local", 75)]);
    backend.answers.set("PlayHistoryPage", (_account, token) => token ? nextResult.promise : page([remote("first", 20)], "next"));
    const pending = loadPlayHistory(true);
    await vi.waitFor(() => expect(playHistory.pages).toBe(1));
    expect(ids()).toEqual(["first", "local"]);
    expect(playHistory.loading).toBe(true);
    expect(playHistory.syncing).toBe(true);
    nextResult.resolve(page([
      remote("second"),
      { ...remote("first", 60), playedAt: "2026-10-23T08:00:00Z" },
      { ...remote("local", 10), playedAt: "2026-10-21T08:00:00Z" },
    ]));
    await pending;
    expect(ids()).toEqual(["first", "local", "second"]);
    expect(playHistory.list.find((play) => play.fileId === "first")?.position).toBe(60);
    expect(playHistory.list.find((play) => play.fileId === "local")?.position).toBe(75);
    expect(playHistory.pages).toBe(2);
    expect(playHistory.loading).toBe(false);
  });

  it("shares an in-flight local read and cloud sync between repeated loads", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const localResult = Promise.withResolvers<RecentPlay[]>();
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const cloudResult = Promise.withResolvers<PlayHistoryPage>();
    backend.answers.set("LocalPlayHistory", () => localResult.promise);
    backend.answers.set("PlayHistoryPage", () => cloudResult.promise);
    const first = loadPlayHistory(true);
    const second = loadPlayHistory(true);
    const automatic = loadPlayHistory(false);
    expect(calls("LocalPlayHistory")).toHaveLength(1);
    localResult.resolve([record("local")]);
    await vi.waitFor(() => expect(calls("PlayHistoryPage")).toHaveLength(1));
    const duringCloud = loadPlayHistory(true);
    cloudResult.resolve(page([remote("cloud")]));
    await Promise.all([first, second, automatic, duringCloud]);
    expect(calls("LocalPlayHistory")).toHaveLength(1);
    expect(calls("PlayHistoryPage")).toHaveLength(1);
    expect(ids()).toEqual(["cloud", "local"]);
    expect(playHistory.loading).toBe(false);
  });

  it("reuses recent cloud history on automatic reopen but rereads newer local progress", async () => {
    let now = Date.parse("2026-10-24T08:00:00Z");
    vi.spyOn(Date, "now").mockImplementation(() => now);
    backend.answers.set("LocalPlayHistory", () => [record("same", 40)]);
    backend.answers.set("PlayHistoryPage", () => page([remote("cloud"), remote("same", 20)]));
    await loadPlayHistory(true);
    now += 30_000;
    backend.answers.set("LocalPlayHistory", () => [
      { ...record("same", 85), playedAt: "2026-10-24T08:00:20Z" }, record("new-local"),
    ]);
    await loadPlayHistory(false);
    expect(calls("PlayHistoryPage")).toHaveLength(1);
    expect(calls("LocalPlayHistory")).toHaveLength(2);
    expect(ids()).toEqual(["cloud", "new-local", "same"]);
    expect(playHistory.list.find((play) => play.fileId === "same")?.position).toBe(85);
    expect(playHistory.loading).toBe(false);
    expect(playHistory.syncing).toBe(false);
  });

  it("refreshes an expired cloud snapshot on automatic reopen", async () => {
    let now = Date.parse("2026-10-24T08:00:00Z");
    vi.spyOn(Date, "now").mockImplementation(() => now);
    backend.answers.set("PlayHistoryPage", () => page([remote("cached")]));
    await loadPlayHistory(true);
    now += 60_001;
    backend.answers.set("PlayHistoryPage", () => page([remote("fresh")]));
    await loadPlayHistory(false);
    expect(calls("PlayHistoryPage")).toHaveLength(2);
    expect(ids()).toContain("fresh");
  });

  it("manually refreshes even when the cloud snapshot is still fresh", async () => {
    vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-10-24T08:00:00Z"));
    backend.answers.set("PlayHistoryPage", () => page([remote("cached")]));
    await loadPlayHistory(true);
    backend.answers.set("PlayHistoryPage", () => page([remote("fresh")]));
    await loadPlayHistory(true);
    expect(calls("PlayHistoryPage")).toHaveLength(2);
    expect(ids()).toContain("fresh");
  });

  it("keeps local history readable when cloud sync fails and can retry", async () => {
    backend.answers.set("LocalPlayHistory", () => [record("local", 65)]);
    backend.answers.set("PlayHistoryPage", () => { throw new Error("cloud offline"); });
    await loadPlayHistory(true);
    expect(ids()).toEqual(["local"]);
    expect(playHistory.list[0].position).toBe(65);
    expect(playHistory.loading).toBe(false);
    expect(playHistory.localLoading).toBe(false);
    expect(playHistory.syncing).toBe(false);
    backend.answers.set("PlayHistoryPage", () => page([remote("cloud")]));
    await loadPlayHistory(true);
    expect(ids()).toEqual(["cloud", "local"]);
    expect(playHistory.error).toBe("");
  });

  it("keeps successfully loaded cloud pages when a later page fails", async () => {
    backend.answers.set("LocalPlayHistory", () => [record("local")]);
    Object.assign(playHistory, { list: [], loaded: false });
    backend.answers.set("PlayHistoryPage", (_account, token) => {
      if (token) throw new Error("next page offline");
      return page([remote("first")], "next");
    });
    await loadPlayHistory(true);
    expect(ids()).toEqual(["first", "local"]);
    expect(playHistory.pages).toBe(1);
    expect(playHistory.loading).toBe(false);
    expect(playHistory.syncing).toBe(false);
  });

  it("stops when a cloud page repeats its continuation token", async () => {
    let count = 0;
    backend.answers.set("PlayHistoryPage", () => page([remote(`page-${++count}`)], "repeat"));
    await loadPlayHistory(true);
    expect(calls("PlayHistoryPage")).toHaveLength(2);
    expect(ids()).toEqual(["page-1", "page-2"]);
    expect(playHistory.loading).toBe(false);
  });

  it("limits a cloud refresh to four pages", async () => {
    let count = 0;
    backend.answers.set("PlayHistoryPage", () => {
      count += 1;
      return page([remote(`page-${count}`)], `next-${count}`);
    });
    await loadPlayHistory(true);
    expect(calls("PlayHistoryPage")).toHaveLength(4);
    expect(playHistory.pages).toBe(4);
    expect(ids()).toEqual(["page-1", "page-2", "page-3", "page-4"]);
  });

  it("does not resurrect a gone file from an older page and retains its local unavailable row", async () => {
    backend.answers.set("LocalPlayHistory", () => [record("local-gone")]);
    backend.answers.set("PlayHistoryPage", (_account, token) => token
      ? page([remote("cloud-gone"), remote("local-gone")])
      : { ...page([], "next"), unavailableIds: ["cloud-gone", "local-gone"] });
    await loadPlayHistory(true);
    expect(ids()).toEqual(["local-gone"]);
    expect(playHistory.list[0]).toMatchObject({ local: true, available: false });
  });

  it("does not put the previous account's late cloud page on the new account's history", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const result = Promise.withResolvers<PlayHistoryPage>();
    backend.answers.set("LocalPlayHistory", (account) => [record(`${account}-local`)]);
    backend.answers.set("PlayHistoryPage", (account, token) => account === "a"
      ? token ? result.promise : page([remote("a-first")], "next")
      : page([remote("b-first")], "", "b"));
    const previous = loadPlayHistory(true);
    await vi.waitFor(() => expect(playHistory.pages).toBe(1));
    drive.status = { accountId: "b", configured: true } as typeof drive.status;
    await loadPlayHistory(true);
    expect(playHistory.account).toBe("b");
    expect(ids()).toEqual(["b-first", "b-local"]);
    result.resolve(page([remote("a-late")]));
    await previous;
    expect(playHistory.account).toBe("b");
    expect(ids()).toEqual(["b-first", "b-local"]);
    expect(playHistory.pages).toBe(1);
    expect(playHistory.loading).toBe(false);
    expect(playHistory.error).toBe("");
  });

  it("does not resurrect cleared history from earlier cloud or overview requests", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const historyResult = Promise.withResolvers<PlayHistoryPage>();
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const recentResult = Promise.withResolvers<RecentPlay[]>();
    backend.answers.set("LocalPlayHistory", () => [record("old")]);
    backend.answers.set("PlayHistoryPage", () => historyResult.promise);
    backend.answers.set("RecentPlays", () => recentResult.promise);
    const historyLoad = loadPlayHistory(true);
    const recentLoad = loadPlays();
    await vi.waitFor(() => expect(calls("PlayHistoryPage")).toHaveLength(1));
    expect(await clearPlayHistory()).toBe(true);
    historyResult.resolve(page([remote("late")]));
    recentResult.resolve([record("late")]);
    await Promise.all([historyLoad, recentLoad]);
    expect(playHistory.list).toEqual([]);
    expect(drive.plays).toEqual([]);
    expect(playHistory.loading).toBe(false);
    expect(playHistory.localLoading).toBe(false);
    expect(playHistory.syncing).toBe(false);
  });

  it("does not resurrect cleared history from an earlier local read", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const localResult = Promise.withResolvers<RecentPlay[]>();
    backend.answers.set("LocalPlayHistory", () => localResult.promise);
    const pending = loadPlayHistory(true);
    expect(playHistory.localLoading).toBe(true);
    expect(calls("PlayHistoryPage")).toHaveLength(1);
    expect(await clearPlayHistory()).toBe(true);
    localResult.resolve([record("late")]);
    await pending;
    expect(playHistory.list).toEqual([]);
    expect(drive.plays).toEqual([]);
    expect(playHistory.loading).toBe(false);
    expect(playHistory.localLoading).toBe(false);
    expect(playHistory.syncing).toBe(false);
  });

  it("does not restore stale cached cloud rows when history is cleared", async () => {
    backend.answers.set("PlayHistoryPage", () => page([remote("cached")]));
    await loadPlayHistory(true);
    expect(await clearPlayHistory()).toBe(true);
    backend.answers.set("PlayHistoryPage", () => page(null));
    await loadPlayHistory(false);
    expect(calls("LocalPlayHistory")).toHaveLength(2);
    expect(playHistory.list).toEqual([]);
    expect(drive.plays).toEqual([]);
  });

  it("preserves rows after failed clear, rejects late pages, invalidates cache, and allows another attempt", async () => {
    backend.answers.set("LocalPlayHistory", () => [record("local")]);
    backend.answers.set("PlayHistoryPage", () => page([remote("cached")]));
    await loadPlayHistory(true);
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const lateResult = Promise.withResolvers<PlayHistoryPage>();
    backend.answers.set("PlayHistoryPage", () => lateResult.promise);
    const pending = loadPlayHistory(true);
    await vi.waitFor(() => expect(playHistory.syncing).toBe(true));
    const previous = [...playHistory.list];
    backend.answers.set("ClearPlays", () => { throw new Error("Failed to save"); });
    expect(await clearPlayHistory()).toBe(false);
    expect(playHistory.list).toEqual(previous);
    expect(drive.plays.map((play) => play.fileId)).toEqual(["old"]);
    expect(playHistory.clearing).toBe(false);
    lateResult.resolve(page([remote("late")]));
    await pending;
    expect(playHistory.list).toEqual(previous);
    backend.answers.set("PlayHistoryPage", () => page([remote("fresh")]));
    await loadPlayHistory(false);
    expect(calls("PlayHistoryPage")).toHaveLength(3);
    expect(ids()).toContain("fresh");
    expect(ids()).not.toContain("late");
    backend.answers.set("ClearPlays", () => null);
    expect(await clearPlayHistory()).toBe(true);
    expect(playHistory.list).toEqual([]);
    expect(drive.plays).toEqual([]);
  });

  it("does not start a video after its file-detail request is overtaken by an account switch", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const result = Promise.withResolvers<unknown>();
    backend.answers.set("FileDetail", () => result.promise);
    const pending = resumePlay(record("a"));
    drive.status = { accountId: "b", configured: true } as typeof drive.status;
    result.resolve({ id: "a", category: "video", playable: true });
    await pending;
    expect(ui.route).toBe("history");
    expect(player.presentation).toBe("off");
    expect(player.playlist).toEqual([]);
  });
});
