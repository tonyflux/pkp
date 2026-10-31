import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Entry, RecentPlay } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({ answers: new Map<string, (...args: unknown[]) => unknown>(), calls: [] as { name: string; args: unknown[] }[] }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({ DriveService: new Proxy({}, { get: (_target, name: string) => (...args: unknown[]) => {
  backend.calls.push({ name, args });
  return Promise.resolve(backend.answers.get(name)?.(...args));
} }) }));
import { cancelMediaLibraryLoad, continueLibrary, dropAccountMediaLibrary, filterLibrary, groupLibrary, libraryQueue, loadMediaLibrary, mediaLibrary, saveLibraryOverride, type LibraryFile, type LibraryOverride } from "./medialibrary";

function file(id: string, name: string, folders: [string, string][] = []): LibraryFile {
  return {
    entry: { id, name, parentId: folders.at(-1)?.[0] ?? "", category: "video", playable: true, folder: false, size: 100, modifiedTime: "", path: folders.map((f) => f[1]).join(" / ") } as Entry,
    ancestors: folders.map(([id, name]) => ({ id, name })),
  };
}
function record(id: string, position: number, duration = 100, playedAt = "2026-10-22T08:00:00Z"): RecentPlay {
  return { fileId: id, name: id, thumbnail: "", position, duration, playedAt, available: true, local: true };
}
const manual: LibraryOverride = { workId: "explicit-work", title: "Local Series", type: "series", year: 2020, season: 2, episode: 3 };

beforeEach(() => {
  dropAccountMediaLibrary(mediaLibrary.account);
  backend.answers.clear();
  backend.calls.length = 0;
  backend.answers.set("MediaLibraryOverrides", () => ({}));
  backend.answers.set("MediaLibraryHistory", () => []);
  backend.answers.set("LocalPlayHistory", () => []);
});

describe("library identities", () => {
  it("keeps movie remakes and different real folders apart, but retains same-work versions", () => {
    const works = groupLibrary([
      file("old", "Dune.1984.1080p.mkv"), file("new", "Dune.2021.1080p.mkv"), file("uhd", "Dune.2021.2160p.mkv"),
      file("other", "Dune.2021.1080p.mkv", [["other-dir", "Dune (2021)"]]),
    ], []);
    expect(works).toHaveLength(3);
    expect(works.find((w) => w.year === 1984)?.type).toBe("movie");
    expect(works.find((w) => w.episodes[0].versions.length === 2)?.year).toBe(2021);
  });

  it("uses folder ancestry for bare episodes and orders seasons/episodes naturally", () => {
    const works = groupLibrary([
      file("s2e10", "EP10.mkv", [["show", "Example Show"], ["s2", "Season 2"]]),
      file("s1e2", "EP02.mkv", [["show", "Example Show"], ["s1", "Season 1"]]),
      file("s1e1", "01.mkv", [["show", "Example Show"], ["s1", "Season 1"]]),
    ], []);
    expect(works).toHaveLength(1);
    expect(works[0].title).toBe("Example Show");
    expect(works[0].episodes.map((e) => [e.season, e.episode])).toEqual([[1, 1], [1, 2], [2, 10]]);
  });

  it("never merges unknown home videos or yearless same-title movies", () => {
    const works = groupLibrary([file("a", "holiday.mp4"), file("b", "holiday.mov"), file("c", "Dune.1080p.mkv"), file("d", "Dune.2160p.mkv")], []);
    expect(works).toHaveLength(4);
    expect(works.filter((w) => w.type === "unknown")).toHaveLength(2);
  });

  it("separates identical series folder names by folder id and movie/TV identities", () => {
    const works = groupLibrary([
      file("a", "Example.S01E01.mkv", [["a", "Example"]]),
      file("b", "Example.S01E02.mkv", [["b", "Example"]]),
      file("c", "Example.2020.mkv", [["a", "Example"]]),
    ], []);
    expect(works).toHaveLength(3);
  });

  it("tracks real history, credits completion, newest resume, unknown runtime, and work-only queues", () => {
    const works = groupLibrary([
      file("e1", "Example.S01E01.1080p.mkv"), file("e1v2", "Example.S01E01.2160p.mkv"),
      file("e2", "Example.S01E02.mkv"), file("e3", "Example.S01E03.mkv"), file("else", "Else.2020.mkv"),
    ], [record("e1", 99), record("e2", 40), record("e2", 10, 100, "2025-01-01T00:00:00Z"), record("e3", 300, 0)]);
    const work = works.find((w) => w.type === "series")!;
    expect(work.progress).toBe(true);
    expect(work.finished).toBe(false);
    expect(work.episodes[0].finished).toBe(true);
    expect(work.episodes[2].progress).toBe(false);
    expect(continueLibrary(work).entry.id).toBe("e2");
    expect(continueLibrary(work).history?.position).toBe(40);
    const alternate = work.episodes[0].versions.find((v) => v.entry.id === "e1v2")!;
    expect(libraryQueue(work, alternate).map((e) => e.id)).toEqual(["e1v2", "e2", "e3"]);
  });

  it("manual ids group unknown files without conflating explicit same-title works", () => {
    const files = [file("a", "home-a.mkv"), file("b", "home-b.mkv"), file("c", "home-c.mkv")];
    const works = groupLibrary(files, [], { a: manual, b: { ...manual, episode: 4 }, c: { ...manual, workId: "another-work" } });
    expect(works).toHaveLength(2);
    expect(works.find((w) => w.episodes.length === 2)?.episodes.map((e) => e.episode)).toEqual([3, 4]);
    expect(groupLibrary(files, [])).toHaveLength(3);
  });

  it("scopes versions to folder descendants, stars and file search without widening playback queues", () => {
    const files = [
      file("a", "Example.S01E01.1080p.mkv", [["root", "Videos"], ["s1", "Season 1"]]),
      file("b", "Example.S01E01.2160p.mkv", [["root", "Videos"], ["s2", "Season 2"]]),
      file("c", "Other.2020.mkv", [["outside", "Elsewhere"]]),
    ];
    files[1].entry.starred = true;
    const overrides = { a: { ...manual, season: 1, episode: 1 }, b: { ...manual, season: 1, episode: 1 } };
    const works = groupLibrary(files, [], overrides);
    const scoped = filterLibrary(works, "", { folderId: "s1" });
    expect(scoped.map((work) => work.episodes.flatMap((episode) => episode.versions.map((version) => version.entry.id)))).toEqual([["a"]]);
    expect(libraryQueue(scoped[0], scoped[0].episodes[0].versions[0]).map((entry) => entry.id)).toEqual(["a"]);
    expect(filterLibrary(works, "", { folderId: "root", starred: true })[0].episodes[0].versions.map((version) => version.entry.id)).toEqual(["b"]);
    expect(filterLibrary(works, "2160p")[0].episodes[0].versions.map((version) => version.entry.id)).toEqual(["b"]);
    expect(filterLibrary(works, "Example", { folderId: "missing" })).toEqual([]);
    expect(filterLibrary(works, "nonexistent")).toEqual([]);
  });
});

describe("library account lifecycle", () => {
  it("enumerates all keyset pages beyond 200 and keeps explicit ownership", async () => {
    backend.answers.set("MediaLibraryPage", (account, after) => {
      const start = after ? Number(after) : 0;
      return { account, ready: true, revision: "r", total: 601, next: start === 0 ? "500" : "", files: Array.from({ length: start ? 101 : 500 }, (_, i) => file(String(start + i + 1), `Movie.${1900 + i % 100}.mkv`)) };
    });
    await loadMediaLibrary("a");
    expect(mediaLibrary.files).toHaveLength(601);
    expect(mediaLibrary.total).toBe(601);
    expect(backend.calls.filter((c) => c.name === "MediaLibraryPage").map((c) => c.args)).toEqual([["a", "", 500], ["a", "500", 2000]]);
  });

  it("ignores late previous-account pages and never carries manual overrides across accounts", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const result = Promise.withResolvers<unknown>();
    backend.answers.set("MediaLibraryPage", (account) => account === "a" ? result.promise : { account, ready: true, revision: "b", total: 1, next: "", files: [file("same", "holiday.mkv")] });
    backend.answers.set("MediaLibraryOverrides", (account) => account === "a" ? { same: manual } : {});
    const pending = loadMediaLibrary("a");
    await loadMediaLibrary("b");
    result.resolve({ account: "a", ready: true, revision: "a", total: 1, next: "", files: [file("same", "holiday.mkv")] });
    await pending;
    expect(mediaLibrary.account).toBe("b");
    expect(mediaLibrary.overrides).toEqual({});
    expect(mediaLibrary.works[0].type).toBe("unknown");
  });

  it("does not publish stale manual-save replies into the next account", async () => {
    Object.assign(mediaLibrary, { account: "a", files: [file("same", "holiday.mkv")] });
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const result = Promise.withResolvers<void>();
    backend.answers.set("SetMediaLibraryOverride", () => result.promise);
    const pending = saveLibraryOverride("a", "same", manual);
    dropAccountMediaLibrary("a");
    mediaLibrary.account = "b";
    result.resolve();
    expect(await pending).toBe(false);
    expect(mediaLibrary.overrides).toEqual({});
  });

  it("reports changing snapshots rather than silently showing incomplete files", async () => {
    backend.answers.set("MediaLibraryPage", (account, after) => ({ account, ready: true, revision: after ? "changed" : "r", total: 2, next: after ? "" : "1", files: [file(after ? "2" : "1", "Movie.2020.mkv")] }));
    await loadMediaLibrary("a");
    expect(mediaLibrary.error).toContain("索引已变化");
    expect(mediaLibrary.files).toEqual([]);
    expect(mediaLibrary.loading).toBe(false);
  });

  it("rejects a short final page and total changes, and clears a rebuilding index", async () => {
    backend.answers.set("MediaLibraryPage", (account) => ({ account, ready: true, revision: "r", total: 2, next: "", files: [file("one", "Movie.2020.mkv")] }));
    await loadMediaLibrary("a");
    expect(mediaLibrary.error).toContain("索引已变化");
    backend.answers.set("MediaLibraryPage", (account, after) => ({ account, ready: true, revision: "r", total: after ? 3 : 2, next: after ? "" : "one", files: [file(after ? "two" : "one", "Movie.2020.mkv")] }));
    await loadMediaLibrary("a");
    expect(mediaLibrary.error).toContain("索引已变化");
    backend.answers.set("MediaLibraryPage", (account) => ({ account, ready: false, revision: "", total: 0, next: "", files: [] }));
    await loadMediaLibrary("a");
    expect(mediaLibrary.ready).toBe(false);
    expect(mediaLibrary.works).toEqual([]);
  });

  it("shows the first batch and local resume while later pages and cloud history are pending", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const pageResult = Promise.withResolvers<unknown>();
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const historyResult = Promise.withResolvers<RecentPlay[]>();
    backend.answers.set("MediaLibraryHistory", () => historyResult.promise);
    backend.answers.set("LocalPlayHistory", () => [record("one", 40)]);
    backend.answers.set("MediaLibraryPage", (account, after) => after ? pageResult.promise : { account, ready: true, revision: "r", total: 2, next: "one", files: [file("one", "Example.S01E01.mkv")] });
    const loading = loadMediaLibrary("a");
    const duplicate = loadMediaLibrary("a");
    expect(duplicate).toBe(loading);
    await vi.waitFor(() => expect(mediaLibrary.works[0]?.episodes[0].versions[0].history?.position).toBe(40));
    expect(mediaLibrary.loading).toBe(true);
    expect(mediaLibrary.read).toBe(1);
    pageResult.resolve({ account: "a", ready: true, revision: "r", total: 2, next: "", files: [file("two", "Example.S01E02.mkv")] });
    await loading;
    expect(mediaLibrary.loading).toBe(false);
    expect(mediaLibrary.historyLoading).toBe(true);
    expect(mediaLibrary.works[0].episodes.map((episode) => episode.episode)).toEqual([1, 2]);
    historyResult.resolve([record("two", 50)]);
    await vi.waitFor(() => expect(mediaLibrary.works[0].episodes[1].versions[0].history?.position).toBe(50));
    const calls = backend.calls.filter((call) => call.name === "MediaLibraryPage").length;
    await loadMediaLibrary("a", false);
    expect(backend.calls.filter((call) => call.name === "MediaLibraryPage")).toHaveLength(calls);
  });

  it("abandons hidden-view pages and cloud history even after a new same-account load finishes", async () => {
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const oldPage = Promise.withResolvers<unknown>();
    // @ts-expect-error TypeScript 4.9 lacks ES2024 Promise declarations; tests run on Node 24.
    const oldHistory = Promise.withResolvers<RecentPlay[]>();
    backend.answers.set("MediaLibraryPage", () => oldPage.promise);
    backend.answers.set("MediaLibraryHistory", () => oldHistory.promise);
    const oldLoad = loadMediaLibrary("a");
    await vi.waitFor(() => expect(backend.calls.some((call) => call.name === "MediaLibraryPage")).toBe(true));
    cancelMediaLibraryLoad();
    expect(mediaLibrary.loading).toBe(false);
    backend.answers.set("MediaLibraryPage", () => ({ account: "a", ready: true, revision: "new", total: 1, next: "", files: [file("new", "New.2024.mkv")] }));
    backend.answers.set("MediaLibraryHistory", () => [record("new", 40)]);
    await loadMediaLibrary("a", false);
    oldPage.resolve({ account: "a", ready: true, revision: "old", total: 1, next: "more", files: [file("old", "Old.2020.mkv")] });
    oldHistory.resolve([record("old", 99)]);
    await oldLoad;
    expect(mediaLibrary.files.map((value) => value.entry.id)).toEqual(["new"]);
    expect(mediaLibrary.works[0].episodes[0].versions[0].history?.position).toBe(40);
    expect(mediaLibrary.revision).toBe("new");
    expect(mediaLibrary.error).toBe("");
  });

  it("lets a queued tab switch cancel CPU grouping before it publishes", async () => {
    vi.useFakeTimers();
    let clock = 0;
    const now = vi.spyOn(performance, "now").mockImplementation(() => clock += 10);
    try {
      backend.answers.set("MediaLibraryPage", () => ({ account: "a", ready: true, revision: "r", total: 512, next: "", files: Array.from({ length: 512 }, (_, i) => file(String(i), `Example.S01E${i + 1}.mkv`)) }));
      const loading = loadMediaLibrary("a");
      setTimeout(() => cancelMediaLibraryLoad(), 0);
      await vi.runAllTimersAsync();
      await loading;
      expect(mediaLibrary.read).toBe(512);
      expect(mediaLibrary.loaded).toBe(false);
      expect(mediaLibrary.loading).toBe(false);
      expect(mediaLibrary.works).toEqual([]);
    } finally {
      now.mockRestore();
      vi.useRealTimers();
    }
  });

  it("keeps specials inferred from siblings across batches and sorts a multi-run result", async () => {
    const files = [file("special", "S00E01.mkv", [["series", "Series"]]), ...Array.from({ length: 600 }, (_, i) => file(String(i), `Movie.${600 - i}.2024.mkv`)), file("episode", "Example.S01E01.mkv", [["series", "Series"]])];
    backend.answers.set("MediaLibraryPage", () => ({ account: "a", ready: true, revision: "r", total: files.length, next: "", files }));
    await loadMediaLibrary("a");
    expect(mediaLibrary.works.find((work) => work.episodes.some((episode) => episode.season === 0))?.title).toBe("Example");
    expect(mediaLibrary.works.filter((work) => work.type === "movie").map((work) => work.title)).toEqual(Array.from({ length: 600 }, (_, i) => `Movie ${i + 1}`));
  });
});
