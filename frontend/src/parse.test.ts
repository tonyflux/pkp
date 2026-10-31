import { describe, expect, it } from "vitest";
import { buildPlan, groupMedia, type Identity, type MediaGroup } from "./media";
import { treeOf } from "./organizer";
import { applyMatches, parseGroups, titleQueries, type ParsedGroup } from "./parse";
import { PRESETS } from "./templates";
import type { Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";

const GB = 1024 ** 3;
const entry = (id: string, name: string, parentId: string, p: Partial<Entry> = {}): Entry =>
  ({
    id,
    name,
    parentId,
    kind: p.folder ? "drive#folder" : "drive#file",
    mimeType: "",
    size: GB,
    modifiedTime: "2024-01-02T03:04:05+08:00",
    thumbnail: "",
    folder: false,
    playable: false,
    starred: false,
    trashed: false,
    phase: "PHASE_TYPE_COMPLETE",
    category: /\.(mkv|mp4|avi|ts)$/i.test(name) ? "video" : "other",
    duration: 0,
    preview: "",
    path: "",
    ...p,
  }) as Entry;
const folder = (id: string, name: string, parentId: string) => entry(id, name, parentId, { folder: true, category: "folder", size: 0 });

/** parse runs the L0 pipeline over the entries (scope "S") and indexes the result by file id. */
function parse(entries: Entry[], scopeName = "待分类") {
  const tree = treeOf(entries, "S");
  const groups = groupMedia(tree);
  const parsed = parseGroups(groups, scopeName);
  const of = (fileId: string): ParsedGroup & { group: MediaGroup } => {
    const group = groups.find((g) => g.files.some((f) => f.id === fileId))!;
    return { ...parsed.find((p) => p.id === group.id)!, group };
  };
  return { tree, groups, parsed, of };
}

/** The Shanhai Journey folder as found: two episodes and four unnumbered specials (a copy, two cuts); `more` go in beside them. */
const specialsFolder = (...more: Entry[]) => [
  folder("F", "山海纪行", "S"),
  entry("s1", "山海纪行.[01].2160p.mkv", "F"),
  entry("s1b", "山海纪行.[01].1080p.mp4", "F"),
  entry("s2", "山海纪行.[02].2160p.mkv", "F"),
  entry("a", "特别篇.mkv", "F"),
  entry("b", "特别篇 (2).mkv", "F"),
  entry("c", "特别篇 加长.mkv", "F"),
  entry("d", "特别篇 先导.mkv", "F"),
  ...more,
];
const numberedSpecials = () => [entry("sp", "山海纪行 SP01.mkv", "F"), entry("ova", "山海纪行 OVA 02.mkv", "F")];

describe("parseGroups (L0 filename recognition)", () => {
  it("reads a fansub group release with [01] episodes", () => {
    const entries = Array.from({ length: 12 }, (_, i) =>
      entry(`e${i + 1}`, `[Sakurato] 山海纪行 [${String(i + 1).padStart(2, "0")}][1080p][CHS].mkv`, "S"),
    );
    const p = parse(entries).of("e1");
    expect(p.group.files).toHaveLength(12);
    expect(p).toMatchObject({ confidence: "high", why: "" });
    expect(p.identity).toMatchObject({ type: "series", title: "山海纪行", year: null, season: null, numbers: ["episode"], source: "rules" });
  });

  it("reads SxxExx with release tags around it", () => {
    const p = parse([entry("x", "Shan.Hai.Ji.Xing.S01E03.2160p.WEB-DL.H265.mkv", "S")]).of("x");
    expect(p.confidence).toBe("high");
    expect(p.identity).toMatchObject({ type: "series", title: "Shan Hai Ji Xing", season: 1, episode: 3, numbers: [] });
  });

  it("reads Episode N, S01 - 03 and EP marks", () => {
    const a = parse([entry("x", "山海纪行 第03集.mp4", "S")]).of("x");
    expect(a.identity).toMatchObject({ type: "series", title: "山海纪行", episode: 3 });
    expect(a.confidence).toBe("high");
    const b = parse([entry("x", "[Group] Show Name S02 - 07 [720p].mkv", "S")]).of("x");
    expect(b.identity).toMatchObject({ type: "series", title: "Show Name", season: 2, episode: 7 });
    const c = parse([1, 2, 3].map((n) => entry(`e${n}`, `长河.EP0${n}.4K.mp4`, "S"))).of("e1");
    expect(c.identity).toMatchObject({ type: "series", title: "长河", numbers: ["episode"] });
    const d = parse([entry("x", "山海纪行 第三季 第十二集.mp4", "S")]).of("x");
    expect(d.identity).toMatchObject({ title: "山海纪行", season: 3, episode: 12 });
  });

  it("takes the title and season from folders when the name is only a number", () => {
    const entries = [
      folder("T", "山海纪行", "S"),
      folder("S2", "Season 2", "T"),
      entry("x", "05.mkv", "S2"),
      ...[1, 2, 3].map((n) => entry(`e${n}`, `0${n}.mkv`, "S2")),
    ];
    const { of } = parse(entries);
    expect(of("e1").identity).toMatchObject({ type: "series", title: "山海纪行", season: 2, numbers: ["episode"] });
    expect(of("e1").confidence).toBe("high");
    const one = parse([folder("T", "山海纪行", "S"), folder("S2", "第二季", "T"), entry("x", "05.mkv", "S2")]).of("x");
    expect(one.identity).toMatchObject({ type: "series", title: "山海纪行", season: 2, episode: 5 });
  });

  it("reads movies with a year, and their versions", () => {
    const a = parse([entry("x", "Movie.Name.2019.1080p.BluRay.x264.mkv", "S")]).of("x");
    expect(a).toMatchObject({ confidence: "high" });
    expect(a.identity).toMatchObject({ type: "movie", title: "Movie Name", year: 2019, yearSource: "filename", episode: null });
    const b = parse([entry("x", "电影名 (2021) 4K.mp4", "S")]).of("x");
    expect(b.identity).toMatchObject({ type: "movie", title: "电影名", year: 2021 });
    const c = parse([entry("x", "[电影天堂www.dytt89.com]长河-2021_HD国语中字.mp4", "S")]).of("x");
    expect(c.identity).toMatchObject({ type: "movie", title: "长河", year: 2021 });
    const d = parse([entry("x", "1917.2019.2160p.mkv", "S")]).of("x");
    expect(d.identity).toMatchObject({ type: "movie", title: "1917", year: 2019 });
    const v = parse([entry("x", "Dune.2021.1080p.mkv", "S"), entry("y", "Dune.2021.2160p.mkv", "S")]).of("x");
    expect(v.group.files).toHaveLength(2);
    expect(v.identity).toMatchObject({ type: "movie", title: "Dune", year: 2021, numbers: ["other"] });
    expect(v.confidence).toBe("high");
  });

  it("reads specials", () => {
    const sp = parse([1, 2].map((n) => entry(`e${n}`, `[Group] 山海纪行 SP0${n} [1080p].mkv`, "S"))).of("e1");
    expect(sp.identity).toMatchObject({ type: "special", title: "山海纪行", numbers: ["episode"] });
    expect(sp.confidence).toBe("high");
    const ova = parse([entry("x", "山海纪行 OVA.mkv", "S")]).of("x");
    expect(ova).toMatchObject({ confidence: "low", why: "特别篇没有编号" });
    expect(ova.identity).toMatchObject({ type: "unknown", title: "山海纪行", reason: "特别篇没有编号" });
    const ova2 = parse([entry("x", "山海纪行 OVA 02.mkv", "S")]).of("x");
    expect(ova2).toMatchObject({ confidence: "high" });
    expect(ova2.identity).toMatchObject({ type: "special", title: "山海纪行", episode: 2 });
    const folderSp = parse([folder("T", "山海纪行", "S"), folder("P", "Specials", "T"), entry("x", "01.mkv", "P")]).of("x");
    expect(folderSp.identity).toMatchObject({ type: "special", title: "山海纪行", episode: 1 });
  });

  it("leaves unnumbered specials low, whatever copy suffix or descriptor they carry", () => {
    const { of } = parse(specialsFolder());
    expect(of("s1").identity).toMatchObject({ type: "series", title: "山海纪行" });
    expect(of("s1").confidence).toBe("high");
    for (const id of ["a", "b", "c", "d"]) {
      expect(of(id), id).toMatchObject({ confidence: "low", why: "特别篇没有编号" });
      expect(of(id).identity, id).toMatchObject({ type: "unknown", title: "山海纪行", episode: null, reason: "特别篇没有编号" });
    }
    const numbered = parse(specialsFolder(...numberedSpecials())).of;
    expect(numbered("sp")).toMatchObject({ confidence: "high" });
    expect(numbered("sp").identity).toMatchObject({ type: "special", title: "山海纪行", episode: 1 });
    expect(numbered("ova")).toMatchObject({ confidence: "high" });
    expect(numbered("ova").identity).toMatchObject({ type: "special", title: "山海纪行", episode: 2 });
    expect(numbered("b")).toMatchObject({ confidence: "low", why: "特别篇没有编号" });
  });

  it("takes a special's title from the series beside it when no folder names one", () => {
    const { of } = parse([
      ...[1, 2].map((n) => entry(`e${n}`, `山海纪行.S01E0${n}.mkv`, "S")),
      entry("a", "特别篇 加长.mkv", "S"),
      entry("sp", "特别篇 第2集.mkv", "S"),
    ]);
    expect(of("a").identity).toMatchObject({ type: "unknown", title: "山海纪行" });
    expect(of("sp")).toMatchObject({ confidence: "high" });
    expect(of("sp").identity).toMatchObject({ type: "special", title: "山海纪行", episode: 2 });
  });

  it("names subtitles like their video, with the language from the name", () => {
    const entries: Entry[] = [];
    for (const n of [1, 2, 3]) {
      entries.push(entry(`v${n}`, `[Sakurato] 山海纪行 [0${n}][1080p].mkv`, "S"));
      entries.push(entry(`c${n}`, `[Sakurato] 山海纪行 [0${n}][1080p].chs.ass`, "S", { size: 40000 }));
      entries.push(entry(`t${n}`, `[Sakurato] 山海纪行 [0${n}][1080p].cht.ass`, "S", { size: 40000 }));
    }
    const { of } = parse(entries);
    expect(of("c1").identity).toMatchObject({ type: "subtitle", title: "山海纪行", numbers: ["episode"], language: "chs" });
    expect(of("t1").identity).toMatchObject({ type: "subtitle", title: "山海纪行", numbers: ["episode"], language: "cht" });
    expect(of("c1").confidence).toBe("high");
    const movie = parse([entry("m", "Dune.2021.1080p.mkv", "S"), entry("s", "Dune.2021.1080p.chs.srt", "S", { size: 9000 })]).of("s");
    expect(movie.identity).toMatchObject({ type: "subtitle", title: "Dune", year: 2021, episode: null, language: "chs" });
  });

  it("calls obvious junk junk, with a reason", () => {
    const { of } = parse([
      entry("v", "Movie.Name.2019.1080p.mkv", "S"),
      entry("r", "RARBG.txt", "S", { size: 30 }),
      entry("t", "xxx.torrent", "S", { size: 20000 }),
      entry("u", "更多资源.url", "S", { size: 100 }),
      entry("i", "扫码关注.jpg", "S", { size: 40000, category: "image" }),
      entry("p", "poster.jpg", "S", { size: 4 * 1024 * 1024, category: "image" }),
      folder("SD", "Sample", "S"),
      entry("s", "movie.name.2019.sample.mkv", "SD", { size: 30 * 1024 * 1024 }),
      entry("s2", "sample-mn.mkv", "S", { size: 30 * 1024 * 1024 }),
    ]);
    for (const id of ["r", "t", "u", "i", "s", "s2"]) {
      expect(of(id).confidence, id).toBe("high");
      expect(of(id).identity?.type, id).toBe("junk");
      expect(of(id).identity?.reason).not.toBe("");
    }
    expect(of("t").identity?.reason).toBe("种子文件，下载完成后无用");
    expect(of("p").identity).toMatchObject({ type: "unknown", reason: "不是视频、音频或字幕" });
  });

  it("leaves ambiguous names low, saying why", () => {
    const { of } = parse([
      ...[1, 2, 3].map((n) => entry(`v${n}`, `VID_000${n}.mp4`, "S")),
      entry("h", "我的视频.mp4", "S"),
      ...[1, 2].map((n) => entry(`r${n}`, `rec ${n}9${n}-${n}2 片段.mp4`, "S")),
    ]);
    expect(of("v1")).toMatchObject({ confidence: "low", why: "无法确定片名" });
    expect(of("v1").identity).toMatchObject({ type: "unknown", reason: "无法确定片名" });
    expect(of("h")).toMatchObject({ confidence: "low", why: "无法确定是电影还是其他视频" });
    expect(of("r1").confidence).toBe("low");
  });

  it("reads a varying episode inside a name that otherwise differs", () => {
    const entries = [1, 2, 3].map((n) => entry(`e${n}`, `山海纪行 - 0${n} - 第${"一二三"[n - 1]}个故事.mkv`, "S"));
    const p = parse(entries).of("e1");
    expect(p.group.files).toHaveLength(3);
    expect(p.identity).toMatchObject({ type: "series", title: "山海纪行", numbers: ["episode"] });
  });
});

describe("L0 plan end to end", () => {
  const entries = () => [
    folder("A", "山海纪行", "S"),
    entry("lo", "[Sakurato] 山海纪行 [01][1080p][CHS].mp4", "A", { size: 1.2 * GB }),
    entry("hi", "山海纪行.S01E01.2160p.H265.mkv", "A", { size: 4.4 * GB }),
    entry("t", "山海纪行.torrent", "A", { size: 20000 }),
  ];

  it("plans two versions of one episode under one title", () => {
    const tree = treeOf(entries(), "S");
    const groups = groupMedia(tree);
    const parsed = parseGroups(groups, "待分类");
    expect(parsed.every((p) => p.confidence === "high")).toBe(true);
    const plan = buildPlan(tree, groups, parsed.map((p) => p.identity!), { naming: PRESETS.plex });
    const targets = plan.changes.filter((c) => c.op === "move").map((c) => [...c.toPath, c.name].join("/"));
    expect(targets.sort()).toEqual(["山海纪行/Season 01/山海纪行 - S01E01 - 1080p.mp4", "山海纪行/Season 01/山海纪行 - S01E01 - 2160p.mkv"]);
    expect(plan.series).toEqual([expect.objectContaining({ title: "山海纪行", source: "rules", year: null })]);
    expect(plan.changes.find((c) => c.item.id === "t")).toMatchObject({ op: "trash", selected: false });
  });

  it("names from TMDB once matched", () => {
    const tree = treeOf(entries(), "S");
    const groups = groupMedia(tree);
    const identities = parseGroups(groups, "待分类").map((p) => p.identity!);
    const queries = titleQueries(identities);
    expect(queries).toEqual([{ key: expect.any(String), title: "山海纪行", year: 0, kind: "tv" }]);
    const matched = applyMatches(identities, [{ key: queries[0].key, found: true, title: "山海经纪行", year: 2024 }]);
    expect(matched.filter((i) => i.source === "tmdb")).toHaveLength(2);
    expect(matched.find((i) => i.type === "junk")?.source).toBe("rules");
    const plan = buildPlan(tree, groups, matched, { naming: PRESETS.plex });
    const targets = plan.changes.filter((c) => c.op === "move").map((c) => [...c.toPath, c.name].join("/"));
    expect(targets.sort()).toEqual([
      "山海经纪行 (2024)/Season 01/山海经纪行 - S01E01 - 1080p.mp4",
      "山海经纪行 (2024)/Season 01/山海经纪行 - S01E01 - 2160p.mkv",
    ]);
    expect(plan.series).toEqual([expect.objectContaining({ title: "山海经纪行", year: 2024, source: "tmdb", yearGuessed: false })]);
  });

  it("uses the resolutions MediaResolutions measured", () => {
    const tree = treeOf(entries(), "S");
    const groups = groupMedia(tree);
    const identities = parseGroups(groups, "待分类").map((p) => p.identity!);
    const plan = buildPlan(tree, groups, identities, {
      naming: PRESETS.plex,
      resolutions: new Map([["lo", "1080p"], ["hi", "2160p"]]),
    });
    expect(plan.changes.find((c) => c.item.id === "hi")?.name).toBe("山海纪行 - S01E01 - 2160p.mkv");
  });
});

describe("titleQueries / applyMatches", () => {
  const base: Identity = {
    id: "g1",
    type: "series",
    title: "Show",
    year: null,
    yearSource: "none",
    source: "rules",
    season: 1,
    numbers: ["episode"],
    episode: null,
    language: "",
    folder: "",
    reason: "",
  };

  it("asks once per work and skips what is matched or not media", () => {
    const qs = titleQueries([
      base,
      { ...base, id: "g2", title: "show", year: 2020, yearSource: "filename" },
      { ...base, id: "g3", type: "subtitle", language: "chs" },
      { ...base, id: "g4", type: "movie", title: "Dune", year: 2021, yearSource: "filename", numbers: [] },
      { ...base, id: "g5", type: "movie", title: "Dune", year: 1984, yearSource: "guess", numbers: [] },
      { ...base, id: "g6", type: "junk", title: "x" },
      { ...base, id: "g7", type: "unknown", title: "y" },
      { ...base, id: "g9", source: "tmdb", title: "Matched" },
      { ...base, id: "g10", title: " " },
    ]);
    expect(qs.map(({ title, year, kind }) => ({ title, year, kind }))).toEqual([
      { title: "Show", year: 2020, kind: "tv" },
      { title: "Dune", year: 2021, kind: "movie" },
      { title: "Dune", year: 0, kind: "movie" },
    ]);
    expect(new Set(qs.map((q) => q.key)).size).toBe(3);
  });

  it("applies found matches only, keeping a year TMDB lacks", () => {
    const ids: Identity[] = [base, { ...base, id: "g2", type: "movie", title: "Other", year: 2001, yearSource: "filename", numbers: [] }];
    const qs = titleQueries(ids);
    const out = applyMatches(ids, [
      { key: qs[0].key, found: true, title: "节目", year: 0 },
      { key: qs[1].key, found: false, title: "", year: 0 },
    ]);
    expect(out[0]).toMatchObject({ title: "节目", year: null, yearSource: "none", source: "tmdb" });
    expect(out[1]).toBe(ids[1]);
  });
});
