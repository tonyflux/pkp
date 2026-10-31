import { reactive } from "vue";
import { t } from "./i18n";
import { parseSearch } from "./searchquery";

export type SavedSearchCategory = "all" | "video" | "audio" | "image" | "document" | "other";
export interface SavedSearchRange {
  folderId?: string;
  under: string;
}
export interface SavedSearch {
  id: string;
  name: string;
  query: string;
  range: SavedSearchRange | null;
  category: SavedSearchCategory;
}

const MAX_ITEMS = 100;
const categories: Record<SavedSearchCategory, true> = { all: true, video: true, audio: true, image: true, document: true, other: true };
const key = (account: string) => `pkp.savedSearches.${account}`;
const record = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value);
const text = (value: unknown, max: number): value is string => typeof value === "string" && value.length <= max;

/** Reject the whole document rather than silently losing a scope or dropping a damaged item. */
export function parseSavedSearches(raw: string | null): SavedSearch[] {
  if (raw === null) return [];
  const data: unknown = JSON.parse(raw);
  if (!record(data) || data.version !== 1 || !Array.isArray(data.items) || data.items.length > MAX_ITEMS) throw new Error(t("已保存历史数据无效，未作更改"));
  const ids = new Set<string>();
  return data.items.map((item: unknown) => {
    if (!record(item) || !text(item.id, 256) || !item.id || ids.has(item.id) || !text(item.name, 100) || !item.name.trim() || !text(item.query, 8192) || !item.query.trim() || typeof item.category !== "string" || !Object.hasOwn(categories, item.category)) throw new Error(t("已保存历史数据无效，未作更改"));
    ids.add(item.id);
    let range: SavedSearchRange | null = null;
    if (item.range !== null) {
      if (!record(item.range) || Object.keys(item.range).some((field) => field !== "folderId" && field !== "under") || !text(item.range.under, 4096) || (item.range.folderId !== undefined && (!text(item.range.folderId, 256) || !item.range.folderId))) throw new Error(t("已保存历史数据无效，未作更改"));
      range = { under: item.range.under, ...(item.range.folderId ? { folderId: item.range.folderId as string } : {}) };
      if (range.under !== parseSearch(item.query, new Date()).query.under) throw new Error(t("已保存历史数据无效，未作更改"));
    }
    return { id: item.id, name: item.name, query: item.query, range, category: item.category as SavedSearchCategory };
  });
}

export const savedSearches = reactive({ account: "", items: [] as SavedSearch[], error: "" });


function readAccount(account: string): SavedSearch[] {
  if (!account) throw new Error(t("请先选择账号"));
  return parseSavedSearches(localStorage.getItem(key(account)));
}

export function loadSavedSearches(account: string): void {
  savedSearches.account = account;
  savedSearches.items = [];
  savedSearches.error = "";
  if (!account) return;
  try {
    savedSearches.items = readAccount(account);
  } catch (error) {
    savedSearches.error = String(error instanceof Error ? error.message : error);
  }
}

function writeAccount(account: string, items: SavedSearch[]): void {
  // Validate our own writes too: callers cannot accidentally persist a selected-ID range.
  const value = JSON.stringify({ version: 1, items });
  parseSavedSearches(value);
  localStorage.setItem(key(account), value);
  if (savedSearches.account === account) {
    savedSearches.items = items;
    savedSearches.error = "";
  }
}

export function saveSearch(account: string, name: string, query: string, range: SavedSearchRange | null, category: SavedSearchCategory): SavedSearch {
  const items = readAccount(account);
  if (items.length >= MAX_ITEMS) throw new Error(t("最多保存 100 个搜索，请先移除不再需要的搜索"));
  const item: SavedSearch = { id: crypto.randomUUID(), name: name.trim(), query, range: range ? { ...range } : null, category };
  if (!item.name) throw new Error(t("请输入搜索名称"));
  writeAccount(account, [...items, item]);
  return item;
}

/** Replace only the definition: its identity, display name and order are unchanged. */
export function updateSavedSearch(account: string, id: string, query: string, range: SavedSearchRange | null, category: SavedSearchCategory): SavedSearch {
  const items = readAccount(account);
  const item = items.find((entry) => entry.id === id);
  if (!item) throw new Error(t("这条已保存历史已不存在，请重新打开列表"));
  const updated = { ...item, query, range: range ? { ...range } : null, category };
  writeAccount(account, items.map((entry) => entry.id === id ? updated : entry));
  return updated;
}

export function renameSavedSearch(account: string, id: string, name: string): void {
  const items = readAccount(account);
  const item = items.find((entry) => entry.id === id);
  if (!item) throw new Error(t("这条已保存历史已不存在，请重新打开列表"));
  if (!name.trim()) throw new Error(t("请输入搜索名称"));
  writeAccount(account, items.map((entry) => entry.id === id ? { ...entry, name: name.trim() } : entry));
}

export function removeSavedSearch(account: string, id: string): void {
  const items = readAccount(account);
  if (!items.some((entry) => entry.id === id)) throw new Error(t("这条已保存历史已不存在，请重新打开列表"));
  writeAccount(account, items.filter((entry) => entry.id !== id));
}

/** Called only after the backend confirms account removal. Storage failure must be reported. */
export function dropAccountSavedSearches(account: string): void {
  if (!account) return;
  localStorage.removeItem(key(account));
  if (savedSearches.account === account) loadSavedSearches("");
}
