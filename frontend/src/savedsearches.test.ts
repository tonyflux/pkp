import { afterEach, describe, expect, it, vi } from "vitest";
import { dropAccountSavedSearches, loadSavedSearches, parseSavedSearches, removeSavedSearch, renameSavedSearch, saveSearch, savedSearches, updateSavedSearch } from "./savedsearches";
import { parseSearch } from "./searchquery";

const first = "saved-search-test-a";
const second = "saved-search-test-b";
afterEach(() => {
  vi.restoreAllMocks();
  dropAccountSavedSearches(first);
  dropAccountSavedSearches(second);
  loadSavedSearches("");
});

describe("saved searches", () => {
  it("restores query text and exact folder scope only for its account", () => {
    loadSavedSearches(first);
    const query = "旅行 in:旅行 本周";
    const item = saveSearch(first, " 本周旅行 ", query, { folderId: "folder-1", under: "旅行" }, "all");
    loadSavedSearches(second);
    expect(savedSearches.items).toEqual([]);
    saveSearch(second, "Music", "concert", null, "audio");
    loadSavedSearches(first);
    expect(savedSearches.items).toEqual([{ ...item, name: "本周旅行", query, range: { folderId: "folder-1", under: "旅行" } }]);
    expect(parseSearch(savedSearches.items[0].query, new Date(2026, 9, 16)).query.createdAfter).toBe("2026-10-12");
    expect(parseSearch(savedSearches.items[0].query, new Date(2026, 9, 23)).query.createdAfter).toBe("2026-10-19");
    renameSavedSearch(first, item.id, " 新名字 ");
    loadSavedSearches(first);
    expect(savedSearches.items[0]?.name).toBe("新名字");
    removeSavedSearch(first, item.id);
    loadSavedSearches(first);
    expect(savedSearches.items).toEqual([]);
    loadSavedSearches(second);
    expect(savedSearches.items[0]?.category).toBe("audio");
    dropAccountSavedSearches(second);
    expect(localStorage.getItem(`pkp.savedSearches.${second}`)).toBeNull();
    expect(savedSearches.items).toEqual([]);
  });

  it("updates conditions in place without changing the name, ID, order or other account", () => {
    loadSavedSearches(first);
    const item = saveSearch(first, "My videos", "Ocean", null, "all");
    const sibling = saveSearch(first, "Other", "Other", null, "all");
    loadSavedSearches(second);
    const foreign = saveSearch(second, "Foreign", "Ocean", null, "all");
    loadSavedSearches(first);
    const updated = updateSavedSearch(first, item.id, "Ocean 类型:视频 >100M", { folderId: "root", under: "" }, "all");
    expect(updated).toMatchObject({ id: item.id, name: "My videos", query: "Ocean 类型:视频 >100M", range: { folderId: "root", under: "" } });
    expect(savedSearches.items.map((entry) => entry.id)).toEqual([item.id, sibling.id]);
    loadSavedSearches(first);
    expect(savedSearches.items[0]).toEqual(updated);
    expect(() => updateSavedSearch(first, foreign.id, "changed", null, "all")).toThrow();
    loadSavedSearches(second);
    expect(savedSearches.items).toEqual([foreign]);
  });

  it("rejects selected IDs, malformed scope, duplicate IDs and unsupported versions", () => {
    const item = { id: "one", name: "One", query: "type:video", range: null, category: "all" };
    const encode = (items: unknown[], version = 1) => JSON.stringify({ version, items });
    expect(parseSavedSearches(null)).toEqual([]);
    expect(() => parseSavedSearches(encode([item], 2))).toThrow();
    expect(() => parseSavedSearches(encode([item, item]))).toThrow();
    expect(() => parseSavedSearches(encode([{ ...item, range: { ids: [], under: "" } }]))).toThrow();
    expect(() => parseSavedSearches(encode([{ ...item, range: { folderId: "", under: "" } }]))).toThrow();
    expect(() => parseSavedSearches(encode([{ ...item, range: { folderId: "folder" } }]))).toThrow();
    expect(() => parseSavedSearches(encode([{ ...item, category: "toString" }]))).toThrow();
    localStorage.setItem(`pkp.savedSearches.${second}`, "invalid-json");
    loadSavedSearches(first);
    saveSearch(first, "A", "a", null, "all");
    loadSavedSearches(second);
    expect(() => parseSavedSearches(encode([{ ...item, range: { folderId: "folder", under: "other" } }]))).toThrow();
    expect(savedSearches.items).toEqual([]);
    expect(savedSearches.error).not.toBe("");
    expect(() => saveSearch(second, "B", "b", null, "all")).toThrow();
    expect(localStorage.getItem(`pkp.savedSearches.${second}`)).toBe("invalid-json");
  });

  it("never updates memory or reports a successful write when storage fails", () => {
    loadSavedSearches(first);
    const item = saveSearch(first, "One", "one", null, "all");
    const original = localStorage.getItem(`pkp.savedSearches.${first}`);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("quota exceeded"); });
    expect(() => saveSearch(first, "Two", "two", null, "all")).toThrow("quota exceeded");
    expect(() => renameSavedSearch(first, item.id, "Changed")).toThrow("quota exceeded");
    expect(() => removeSavedSearch(first, item.id)).toThrow("quota exceeded");
    expect(() => updateSavedSearch(first, item.id, "changed", null, "all")).toThrow("quota exceeded");
    expect(savedSearches.items).toEqual([item]);
    expect(localStorage.getItem(`pkp.savedSearches.${first}`)).toBe(original);
  });

  it("does not claim account cleanup succeeded when local storage rejects removal", () => {
    loadSavedSearches(first);
    const item = saveSearch(first, "One", "one", null, "all");
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("blocked"); });
    expect(() => dropAccountSavedSearches(first)).toThrow("blocked");
    expect(savedSearches.items).toEqual([item]);
  });
});
