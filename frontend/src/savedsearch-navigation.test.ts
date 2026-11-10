import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { nextTick } from "vue";
import type { DriveStatus, IndexProgress } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  search: vi.fn(async () => ({ hits: [], total: 0, aggregate: null, under: "", underMissing: false })),
  list: vi.fn(async (request: { query: string }) => ({
    entries: [{ id: request.query ? "filtered" : "all-files" }], total: 1, offset: 0, live: false, nextPageToken: "",
  })),
}));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: new Proxy({}, { get: (_, name) => name === "SearchIndex" ? backend.search : name === "List" ? backend.list : async () => null }),
  PlayerService: new Proxy({}, { get: () => async () => null }),
  AppearanceService: new Proxy({}, { get: () => async () => null }),
  MenuBarService: new Proxy({}, { get: () => async () => null }),
  TelemetryService: new Proxy({}, { get: () => async () => null }),
}));
import {
  accounts, canGoBack, closeSavedHistoryDialog, currentSavedSearch, deleteSavedHistory, discardSavedHistoryChanges, drive,
  goBack, localSearch, localSearchShown, openHistory, openLocalSearch, openNav, openSavedHistoryDialog, openSavedSearch, renameSavedHistory,
  resolveSavedHistoryLeave, savedHistory, savedHistoryCanSave, savedHistoryDirty, saveSavedHistoryAs,
  saveSavedHistoryChanges, searchParse, ui,
} from "./store";
import { dropAccountSavedSearches, loadSavedSearches, saveSearch, savedSearches } from "./savedsearches";

async function settle() { await nextTick(); await vi.advanceTimersByTimeAsync(300); }
beforeEach(async () => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 9, 24, 12));
  drive.status = { accountId: "a", configured: true } as DriveStatus;
  drive.indexProgress = { account: "a", ready: true } as IndexProgress;
  accounts.switching = "";
  Object.assign(savedHistory, { account: "", id: "", baseline: null, error: "", dialog: null });
  localSearch.range = null;
  localSearch.force = false;
  ui.query = "";
  ui.category = "all";
  ui.route = "files";
  drive.trail = [];
  await nextTick();
  backend.search.mockClear();
  dropAccountSavedSearches("a");
  loadSavedSearches("a");
});
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

describe("leaving local search", () => {
  it.each(["sidebar", "back"] as const)("returns to the file list from the assistant's unfinished-video search using %s", async (exit) => {
    ui.view = "all";
    ui.scope = "folder";
    ui.route = "assistant";
    openLocalSearch("类型:视频 没看完");
    await settle();
    expect(localSearchShown.value).toBe(true);
    expect(ui.route).toBe("files");

    if (exit === "sidebar") openNav("all");
    else {
      expect(canGoBack.value).toBe(true);
      goBack();
    }
    await settle();
    expect(ui.route).toBe("files");
    expect(ui.query).toBe("");
    expect(localSearchShown.value).toBe(false);
  });

  it.each(["sidebar", "back"] as const)("reloads the unfiltered list when returning from a search with keywords using %s", async (exit) => {
    ui.view = "all";
    ui.scope = "folder";
    openLocalSearch("Ocean 类型:视频");
    await settle();
    expect(drive.entries.map((entry) => entry.id)).toEqual(["filtered"]);
    backend.list.mockClear();

    if (exit === "sidebar") openNav("all");
    else goBack();
    await settle();
    expect(drive.entries.map((entry) => entry.id)).toEqual(["all-files"]);
    expect(backend.list).toHaveBeenCalledTimes(1);
  });

  it.each(["sidebar", "back"] as const)("asks before leaving a modified saved search using %s", async (exit) => {
    ui.view = "all";
    ui.scope = "folder";
    const item = saveSearch("a", "Videos", "Ocean", null, "all");
    openSavedSearch(item.id);
    ui.query = "Ocean 类型:视频";
    await settle();
    if (exit === "sidebar") openNav("all");
    else goBack();
    expect(savedHistory.dialog?.kind).toBe("leave");
    expect(ui.query).toBe("Ocean 类型:视频");
    resolveSavedHistoryLeave("discard");
    await settle();
    expect(localSearchShown.value).toBe(false);
    expect(ui.query).toBe("");
    expect(savedSearches.items[0].query).toBe("Ocean");
  });
});

describe("saved history editing", () => {
  it("re-evaluates relative dates when reopening the same stored ID", async () => {
    const item = saveSearch("a", "Today", "今天 类型:视频", null, "all");
    openSavedSearch(item.id);
    await settle();
    const first = searchParse.value.query.createdAfter;
    openHistory();
    vi.setSystemTime(new Date(2026, 9, 25, 12));
    openSavedSearch(item.id);
    await settle();
    expect(new Date(searchParse.value.query.createdAfter).getTime() - new Date(first).getTime()).toBe(86400000);
  });
  it("keeps an exact folder and plain category without falling back to a wider unready search", async () => {
    const item = saveSearch("a", "Audio", "Ocean", { folderId: "exact", under: "" }, "audio");
    drive.indexProgress = { account: "a", ready: false } as IndexProgress;
    openSavedSearch(item.id);
    await settle();
    expect(localSearchShown.value).toBe(true);
    expect(backend.search).not.toHaveBeenCalled();
    drive.indexProgress = { account: "a", ready: true } as IndexProgress;
    await settle();
    expect(backend.search).toHaveBeenLastCalledWith("a", expect.objectContaining({ folderId: "exact", category: "audio", keywords: ["Ocean"] }));
  });
  it("updates the original entry repeatedly and keeps its ID, name and order", async () => {
    const item = saveSearch("a", "My videos", "Ocean", null, "all");
    const other = saveSearch("a", "Other", "Other", null, "all");
    openSavedSearch(item.id);
    ui.query = "Ocean 类型:视频";
    await settle();
    expect(currentSavedSearch.value?.id).toBe(item.id);
    expect(savedHistoryDirty.value).toBe(true);
    expect(saveSavedHistoryChanges()).toBe(true);
    expect(saveSavedHistoryChanges()).toBe(true);
    expect(savedSearches.items.map((entry) => entry.id)).toEqual([item.id, other.id]);
    expect(savedSearches.items[0]).toMatchObject({ name: "My videos", query: ui.query });
    expect(savedHistoryDirty.value).toBe(false);
    ui.query = "Ocean 类型:图片";
    discardSavedHistoryChanges();
    await settle();
    expect(ui.query).toBe("Ocean 类型:视频");
    expect(currentSavedSearch.value?.id).toBe(item.id);
  });
  it("creates a copy only for explicit save as and keeps the original unchanged", async () => {
    const item = saveSearch("a", "My videos", "Ocean", null, "all");
    openSavedSearch(item.id);
    ui.query = "Ocean >100M";
    expect(saveSavedHistoryAs("Large videos")).toBe(true);
    expect(savedSearches.items).toHaveLength(2);
    expect(savedSearches.items[0]).toEqual(item);
    expect(currentSavedSearch.value?.name).toBe("Large videos");
    expect(currentSavedSearch.value?.id).not.toBe(item.id);
  });
  it("does not lose source identity or edits when storage fails or the query is empty", async () => {
    const item = saveSearch("a", "My videos", "Ocean", null, "all");
    openSavedSearch(item.id);
    ui.query = "Ocean 类型:视频";
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("Storage blocked"); });
    expect(saveSavedHistoryChanges()).toBe(false);
    expect(savedSearches.items[0]).toEqual(item);
    expect(ui.query).toBe("Ocean 类型:视频");
    expect(savedHistoryDirty.value).toBe(true);
    expect(savedHistory.error).toBe("Storage blocked");
    ui.query = "";
    await settle();
    expect(localSearchShown.value).toBe(true);
    expect(currentSavedSearch.value?.id).toBe(item.id);
    expect(savedHistoryCanSave.value).toBe(false);
  });
  it("keeps edits on stay, preserves failed-save navigation, then saves before leaving", async () => {
    const item = saveSearch("a", "My videos", "Ocean", null, "all");
    openSavedSearch(item.id);
    ui.query = "Ocean >100M";
    openHistory();
    expect(ui.route).toBe("files");
    expect(savedHistory.dialog?.kind).toBe("leave");
    closeSavedHistoryDialog();
    expect(currentSavedSearch.value?.id).toBe(item.id);
    expect(savedHistoryDirty.value).toBe(true);
    openHistory();
    const blocked = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    resolveSavedHistoryLeave("save");
    expect(ui.route).toBe("files");
    expect(savedHistory.dialog?.kind).toBe("leave");
    blocked.mockRestore();
    resolveSavedHistoryLeave("save");
    expect(ui.route).toBe("history");
    expect(savedSearches.items[0].query).toBe("Ocean >100M");
    expect(currentSavedSearch.value).toBeUndefined();
  });
  it("discards before switching saved entries and leaves original storage unchanged", async () => {
    const first = saveSearch("a", "First", "Ocean", null, "all");
    const second = saveSearch("a", "Second", "Forest", null, "all");
    openSavedSearch(first.id);
    ui.query = "changed";
    openSavedSearch(second.id);
    expect(currentSavedSearch.value?.id).toBe(first.id);
    resolveSavedHistoryLeave("discard");
    expect(currentSavedSearch.value?.id).toBe(second.id);
    expect(ui.query).toBe("Forest");
    expect(savedSearches.items[0].query).toBe("Ocean");
  });
  it("renames and deletes current entry only after successful storage, retaining temporary results", async () => {
    const item = saveSearch("a", "First", "Ocean", null, "all");
    openSavedSearch(item.id);
    openSavedHistoryDialog("rename", item.id);
    expect(renameSavedHistory("Renamed")).toBe(true);
    expect(currentSavedSearch.value?.name).toBe("Renamed");
    ui.query = "Ocean 类型:视频";
    openSavedHistoryDialog("delete", item.id);
    const failed = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    expect(deleteSavedHistory()).toBe(false);
    expect(savedHistory.dialog?.kind).toBe("delete");
    expect(savedSearches.items).toHaveLength(1);
    failed.mockRestore();
    expect(deleteSavedHistory()).toBe(true);
    expect(savedSearches.items).toEqual([]);
    expect(currentSavedSearch.value).toBeUndefined();
    expect(ui.query).toBe("Ocean 类型:视频");
    expect(localSearchShown.value).toBe(true);
  });
  it("never applies an old account dialog to a new screen", async () => {
    const item = saveSearch("a", "First", "Ocean", null, "all");
    openSavedSearch(item.id);
    openSavedHistoryDialog("delete", item.id);
    drive.status = { accountId: "b", configured: true } as DriveStatus;
    await nextTick();
    expect(deleteSavedHistory()).toBe(false);
    loadSavedSearches("a");
    expect(savedSearches.items).toEqual([item]);
  });
});
