import { describe, expect, it } from "vitest";
import type { CardRow, FilesCard } from "./agent";
import { hasSearchResults, presentSearchGroups, querySummary, searchSources } from "./assistantresults";
import { emptyQuery } from "./searchquery";

const row = (id: string, name = id): CardRow => ({ id, name, folder: false, path: "", size: 10, modified: "", played: "" });
const source = (id: string, rows: CardRow[], total = rows.length): FilesCard => ({
  id, kind: "files", title: id, state: "done", note: "", rows, total, aggregate: [], query: emptyQuery(),
});

describe("search result presentation", () => {
  it("shows only the selected search after 34/0/12 searches", () => {
    const cards = [source("first", [row("a")], 34), source("empty", [], 0), source("last", [row("b")], 12)];
    const groups = presentSearchGroups(cards, [{ title: "Relevant", sourceIds: ["last"] }], false);
    expect(searchSources(cards)).toHaveLength(3);
    expect(groups).toHaveLength(1);
    expect(groups[0].card.rows.map((item) => item.id)).toEqual(["b"]);
    expect(groups[0].sources[0].total).toBe(12);
    expect(hasSearchResults(cards[1])).toBe(false);
  });

  it("deduplicates within each group with latest source metadata without changing candidates", () => {
    const cards = [source("one", [row("a", "old"), row("b")], 34), source("two", [row("a", "new")], 12)];
    const groups = presentSearchGroups(cards, [
      { title: "First", sourceIds: ["two", "one"] },
      { title: "Second", sourceIds: ["two"], fileIds: ["a"] },
    ], false);
    expect(groups.map((group) => group.card.rows.map((item) => item.name))).toEqual([["new", "b"], ["new"]]);
    expect(groups[0].card.total).toBe(2);
    expect(groups[0].sources.map((item) => item.total)).toEqual([34, 12]);
    expect(cards[0].rows[0].name).toBe("old");
    expect(cards[0].total).toBe(34);
  });

  it("distinguishes explicit empty groups from legacy fallback and does not guess the last query", () => {
    const one = source("one", [row("a")]);
    expect(presentSearchGroups([one], undefined, false)).toHaveLength(1);
    expect(presentSearchGroups([one], [], false)).toEqual([]);
    expect(presentSearchGroups([one], undefined, true)).toEqual([]);
    expect(presentSearchGroups([one, source("two", [row("b")])], undefined, false)).toEqual([]);
    expect(presentSearchGroups([source("zero", [])], undefined, false)).toEqual([]);
  });

  it("deduplicates legacy single-search rows before displaying or organizing them", () => {
    const original = source("one", [row("a", "old"), row("b"), row("a", "new")], 65);
    const [group] = presentSearchGroups([original], undefined, false);
    expect(group.card.rows.map((item) => [item.id, item.name])).toEqual([["a", "new"], ["b", "b"]]);
    expect(group.sources[0].total).toBe(65);
    expect(original.rows.map((item) => item.name)).toEqual(["old", "b", "new"]);
  });

  it("preserves aggregate totals and refuses merged or subset aggregate presentations", () => {
    const aggregate = source("aggregate", [], 200);
    aggregate.query!.aggregate = { by: "folder", measure: "size", limit: 10 };
    aggregate.aggregate = [{ key: "folder", label: "Folder", count: 80, size: 1234 }];
    const cards = [aggregate, source("rows", [row("a")])];
    const groups = presentSearchGroups(cards, [{ title: "Summary", sourceIds: ["aggregate"] }], false);
    expect(groups[0].card.total).toBe(200);
    expect(groups[0].card.aggregate).toBe(aggregate.aggregate);
    expect(groups[0].unique).toBe(false);
    expect(presentSearchGroups(cards, [{ title: "Invalid", sourceIds: ["aggregate", "rows"] }], false)).toEqual([]);
    expect(presentSearchGroups(cards, [{ title: "Invalid", sourceIds: ["aggregate"], fileIds: [] }], false)).toEqual([]);
  });

  it("keeps query-null cards out of searches and accepts old partial queries", () => {
    const ordinary = { ...source("folder", [row("a")]), query: null };
    expect(searchSources([ordinary])).toEqual([]);
    expect(querySummary(null, "Folder contents")).toBe("Folder contents");
    expect(querySummary({ keywords: ["sample"], under: "Movies" })).toContain("sample");
    expect(querySummary({ keywords: ["sample"], under: "Movies" })).toContain("Movies");
  });

  it("ignores malformed persisted constraints without mutating the source", () => {
    const malformed = { keywords: "broken", ext: [12, "mkv"], under: 42, minSize: -1, maxSize: Infinity, createdAfter: 12, aggregate: "broken", asc: "yes" };
    const summary = querySummary(malformed as unknown as Parameters<typeof querySummary>[0]);
    expect(summary).toContain("mkv");
    expect(summary).not.toContain("broken");
    expect(summary).not.toContain("Infinity");
    expect(malformed.under).toBe(42);
    const original = source("stable-source-id", [row("a")]);
    presentSearchGroups([original], [{ title: "Selected", sourceIds: [original.id] }], false);
    expect(original.id).toBe("stable-source-id");
  });
});
