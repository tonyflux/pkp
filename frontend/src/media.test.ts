import { describe, expect, it } from "vitest";
import {
  buildPlan,
  classify,
  emptiedFolders,
  groupMedia,
  languageTag,
  versionTag,
  type Identity,
  type MediaGroup,
} from "./media";
import { problems, treeOf, type Change } from "./organizer";
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
    size: 1,
    modifiedTime: "2024-01-02T03:04:05+08:00",
    thumbnail: "",
    folder: false,
    playable: false,
    starred: false,
    trashed: false,
    phase: "PHASE_TYPE_COMPLETE",
    category: "other",
    duration: 0,
    preview: "",
    path: "",
    ...p,
  }) as Entry;
const folder = (id: string, name: string, parentId: string) => entry(id, name, parentId, { folder: true, category: "folder" });

/**
 * To Classify (S): one series from two release groups in nested folders, with
 * subtitles from the first and a torrent left beside the second.
 *   Chronicles of Mountains and Seas [GroupA]/ [GroupA] Chronicles of Mountains and Seas - 0N [1080p].mkv + .chs.ass   (A)
 *   Downloads/Shan.Hai.Ji.Xing.S01.2160p/ Shan.Hai.Ji.Xing.S01E0N.2160p.WEB-DL.mkv + .torrent  (B)
 */
function library(): Entry[] {
  const out = [
    folder("A", "山海纪行 [GroupA]", "S"),
    folder("DL", "Downloads", "S"),
    folder("B", "Shan.Hai.Ji.Xing.S01.2160p", "DL"),
    entry("t", "Shan.Hai.Ji.Xing.S01.2160p.torrent", "B", { size: 20000 }),
  ];
  for (const n of [1, 2, 3]) {
    out.push(entry(`a${n}`, `[GroupA] 山海纪行 - 0${n} [1080p].mkv`, "A", { size: 1.2 * GB, category: "video" }));
    out.push(entry(`s${n}`, `[GroupA] 山海纪行 - 0${n} [1080p].chs.ass`, "A", { size: 40000 }));
    out.push(entry(`b${n}`, `Shan.Hai.Ji.Xing.S01E0${n}.2160p.WEB-DL.mkv`, "B", { size: 4.4 * GB, category: "video" }));
  }
  return out;
}

const groupOf = (groups: MediaGroup[], fileId: string) => groups.find((g) => g.files.some((f) => f.id === fileId))!;

function identities(groups: MediaGroup[]): Identity[] {
  const base: Omit<Identity, "id" | "type"> = {
    title: "山海纪行",
    year: 2023,
    yearSource: "guess",
    source: "model",
    season: 1,
    numbers: ["episode"],
    episode: null,
    language: "",
    folder: "",
    reason: "",
  };
  return [
    { ...base, id: groupOf(groups, "a1").id, type: "series" },
    { ...base, id: groupOf(groups, "s1").id, type: "subtitle", language: "cht" },
    { ...base, id: groupOf(groups, "b1").id, type: "series", title: "山海纪行 " },
    { ...base, id: groupOf(groups, "t").id, type: "junk", numbers: [], reason: "种子文件，下载完成后无用" },
  ];
}

const byId = (changes: Change[], id: string) => changes.find((c) => c.item.id === id)!;
const target = (c: Change) => [...c.toPath, c.name].join("/");

describe("classify and tags", () => {
  it("sorts by extension, then the drive's category", () => {
    expect(classify({ name: "a.ASS", category: "other" })).toBe("subtitle");
    expect(classify({ name: "a.mkv", category: "other" })).toBe("video");
    expect(classify({ name: "a.bin", category: "video" })).toBe("video");
    expect(classify({ name: "a.flac", category: "other" })).toBe("audio");
    expect(classify({ name: "a.torrent", category: "other" })).toBe("other");
  });

  it("reads language tags", () => {
    expect(languageTag("Show.E01.CHS.ass")).toBe("chs");
    expect(languageTag("Show E01 [简体].ass")).toBe("简体");
    expect(languageTag("Show.E01.chs&eng.ass")).toBe("chs&eng");
    expect(languageTag("Show.E01.ass")).toBe("");
  });

  it("reads version tags", () => {
    expect(versionTag("Show.S01E01.2160p.WEB-DL.mkv")).toBe("2160p WEB-DL");
    expect(versionTag("Show [4K HDR10].mkv")).toBe("2160p HDR");
    expect(versionTag("Show.1080p.BluRay.REMUX.mkv")).toBe("1080p REMUX BluRay");
    expect(versionTag("Show 01.mkv")).toBe("");
  });
});

describe("groupMedia (Coarse grouping)", () => {
  it("groups by class and name shape across nested folders", () => {
    const groups = groupMedia(treeOf(library(), "S"));
    expect(groups).toHaveLength(4);
    expect(groups.map((g) => [g.kind, g.files.map((f) => f.id)])).toEqual(expect.arrayContaining([
      ["subtitle", ["s1", "s2", "s3"]],
      ["video", ["a1", "a2", "a3"]],
      ["other", ["t"]],
      ["video", ["b1", "b2", "b3"]],
    ]));
    const a = groupOf(groups, "a2");
    expect(a.pattern).toBe("[GroupA] 山海纪行 - {n1} [1080p].mkv");
    expect(a.variables).toEqual([{ name: "n1", min: 1, max: 3, distinct: 3 }]);
    expect(a.files.map((f) => f.numbers)).toEqual([[1], [2], [3]]);
    expect(a.dirs).toEqual([["山海纪行 [GroupA]"]]);
    const b = groupOf(groups, "b1");
    expect(b.pattern).toBe("Shan.Hai.Ji.Xing.S01E{n1}.2160p.WEB-DL.mkv");
    expect(b.dirs).toEqual([["Downloads", "Shan.Hai.Ji.Xing.S01.2160p"]]);
    expect(groups.map((g) => g.id)).toEqual(["g1", "g2", "g3", "g4"]);
  });

  it("merges episode-titled releases in one folder", () => {
    const entries = [
      folder("F", "Show", "S"),
      ...["Show - 01 - Pilot.mkv", "Show - 02 - The Return.mkv", "Show - 03 - Finale Part 2.mkv", "Show - 04 - Home.mkv"].map((n, i) =>
        entry(`v${i}`, n, "F", { category: "video", size: (i + 1) * GB }),
      ),
      entry("o", "Other 1.mkv", "F", { category: "video" }),
    ];
    const groups = groupMedia(treeOf(entries, "S"));
    expect(groups.map((g) => g.files.length)).toEqual([1, 4]);
    const show = groups[1];
    expect(show.pattern).toBe("Show - {n1}….mkv");
    expect(show.variables[0]).toMatchObject({ min: 1, max: 4 });
    expect(show.samples.length).toBeLessThanOrEqual(4);
  });

  it("samples the first, the last and the odd ones", () => {
    const entries = [folder("F", "Show", "S")];
    for (let i = 1; i <= 20; i++) {
      const ext = i === 10 ? "mp4" : "mkv";
      entries.push(entry(`v${i}`, `Show ${String(i).padStart(2, "0")}.${ext}`, "F", { size: i === 20 ? 10 : GB, category: "video" }));
    }
    const [g] = groupMedia(treeOf(entries, "S"));
    expect(g.pattern).toBe("Show {n1}.[mkv|mp4]");
    expect(g.samples.map((f) => f.id)).toEqual(["v1", "v10", "v20"]);
  });

  it("takes only the chosen files and folders", () => {
    const groups = groupMedia(treeOf(library(), "S"), new Set(["DL", "a1"]));
    expect(groups.flatMap((g) => g.files.map((f) => f.id)).sort()).toEqual(["a1", "b1", "b2", "b3", "t"]);
  });

  it("keeps one name shape in two season folders apart", () => {
    const entries = [folder("Z", "Show", "S"), folder("Z1", "Season1", "Z"), folder("Z2", "Season2", "Z")];
    for (const [dir, s] of [["Z1", 1], ["Z2", 2]] as const) {
      for (const n of [1, 2, 3]) entries.push(entry(`${s}-${n}`, `[Show][0${n}].mkv`, dir, { category: "video" }));
    }
    const groups = groupMedia(treeOf(entries, "S"));
    expect(groups.map((g) => [g.dirs, g.files.map((f) => f.id)])).toEqual([
      [[["Show", "Season1"]], ["1-1", "1-2", "1-3"]],
      [[["Show", "Season2"]], ["2-1", "2-2", "2-3"]],
    ]);
  });
});

describe("buildPlan", () => {
  const plan = (opts: Partial<Parameters<typeof buildPlan>[3]> = {}) => {
    const tree = treeOf(library(), "S");
    const groups = groupMedia(tree);
    return { tree, ...buildPlan(tree, groups, identities(groups), { naming: PRESETS.plex, ...opts }) };
  };

  it("brings one series from several groups and folders under one title", () => {
    const { changes, series, kept, tree } = plan();
    expect(kept).toEqual([]);
    expect(series).toHaveLength(1);
    expect(series[0]).toMatchObject({ title: "山海纪行", type: "series", dirs: 2, items: 9, year: null, guess: 2023 });
    for (const id of ["a1", "a2", "b3", "s2"]) expect(byId(changes, id).toPath).toEqual(["山海纪行", "Season 01"]);
    expect(problems(changes, tree).size).toBe(0);
  });

  it("keeps every version of an episode with its own suffix", () => {
    const { changes, resolutionIds } = plan();
    expect(target(byId(changes, "a1"))).toBe("山海纪行/Season 01/山海纪行 - S01E01 - 1080p.mkv");
    expect(target(byId(changes, "b1"))).toBe("山海纪行/Season 01/山海纪行 - S01E01 - 2160p WEB-DL.mkv");
    expect(resolutionIds.sort()).toEqual(["a1", "a2", "a3", "b1", "b2", "b3"]);
    const measured = plan({ resolutions: new Map([["a1", "2160p"], ["b1", "2160p"]]) }).changes;
    expect(byId(measured, "b1").name).toBe("山海纪行 - S01E01 - 2160p.mkv");
    expect(byId(measured, "a1").name).toBe("山海纪行 - S01E01 - 2160p (2).mkv");
  });

  it("uses a guessed year only when the user adopts it, for the whole series", () => {
    const off = plan();
    expect(off.changes.every((c) => !target(c).includes("2023"))).toBe(true);
    const key = off.series[0].key;
    const on = plan({ years: new Set([key]) });
    expect(on.series[0]).toMatchObject({ year: 2023, yearGuessed: true });
    const moved = on.changes.filter((c) => c.op === "move");
    expect(moved.every((c) => c.toPath[0] === "山海纪行 (2023)")).toBe(true);
  });

  it("uses a year the names carry without asking", () => {
    const tree = treeOf(
      [folder("F", "长河 (2021)", "S"), entry("m", "长河.1080p.mkv", "F", { category: "video" })],
      "S",
    );
    const groups = groupMedia(tree);
    const who: Identity = {
      id: groups[0].id,
      type: "movie",
      title: "长河",
      year: 2021,
      yearSource: "filename",
      source: "rules",
      season: null,
      numbers: [],
      episode: null,
      language: "",
      folder: "电影",
      reason: "",
    };
    const p = buildPlan(tree, groups, [who], { naming: PRESETS.plex });
    expect(target(byId(p.changes, "m"))).toBe("电影/长河 (2021)/长河 (2021).mkv");
    expect(p.series[0]).toMatchObject({ year: 2021, yearGuessed: false, guess: null, folder: "电影" });
  });

  it("takes a TMDB year as confirmed and says where the series came from", () => {
    const tree = treeOf(library(), "S");
    const groups = groupMedia(tree);
    const base = identities(groups);
    const rules = buildPlan(tree, groups, base.map((i) => ({ ...i, source: "rules" as const })), { naming: PRESETS.plex });
    expect(rules.series[0]).toMatchObject({ source: "rules", year: null, guess: 2023 });
    const b = groupOf(groups, "b1").id;
    const mixed = base.map((i) =>
      i.id === b ? { ...i, title: "山海纪行", year: 2024, yearSource: "tmdb" as const, source: "tmdb" as const } : { ...i, source: "rules" as const },
    );
    const p = buildPlan(tree, groups, mixed, { naming: PRESETS.plex });
    expect(p.series).toHaveLength(1);
    expect(p.series[0]).toMatchObject({ source: "tmdb", year: 2024, yearGuessed: false, guess: null });
    expect(target(byId(p.changes, "a1"))).toBe("山海纪行 (2024)/Season 01/山海纪行 - S01E01 - 1080p.mkv");
  });

  it("names subtitles after the video beside them, keeping the name's language tag", () => {
    const { changes } = plan();
    expect(target(byId(changes, "s1"))).toBe("山海纪行/Season 01/山海纪行 - S01E01 - 1080p.chs.ass");
  });

  it("proposes junk for the trash unticked, with the reason", () => {
    const t = byId(plan().changes, "t");
    expect(t).toMatchObject({ op: "trash", selected: false, note: "种子文件，下载完成后无用", toPath: ["Downloads", "Shan.Hai.Ji.Xing.S01.2160p"] });
  });

  it("proposes source folders that end up empty", () => {
    const { changes, tree } = plan();
    const empties = changes.filter((c) => c.op === "trashIfEmpty");
    // B still holds the unticked torrent, so neither it nor Downloads empties.
    expect(empties.map((c) => c.item.id)).toEqual(["A"]);
    expect(empties[0]).toMatchObject({ selected: true, note: "执行后为空才移到回收站" });
    const ticked = changes.map((c) => (c.op === "trash" ? { ...c, selected: true } : c));
    expect(new Set(emptiedFolders(tree, ticked).map((c) => c.item.id))).toEqual(new Set(["B", "A", "DL"]));
  });

  it("keeps what it cannot place, saying why", () => {
    const tree = treeOf(library(), "S");
    const groups = groupMedia(tree);
    const ids = identities(groups).filter((i) => i.id !== groupOf(groups, "b1").id);
    ids[0] = { ...ids[0], numbers: ["other"] };
    const p = buildPlan(tree, groups, ids, { naming: PRESETS.plex, missing: "已达到 token 上限，未识别" });
    expect(byId(p.kept, "a1").locked).toBe("没有识别出集数");
    expect(byId(p.kept, "b1").locked).toBe("已达到 token 上限，未识别");
    expect(byId(p.kept, "b1").selected).toBe(false);
  });

  it("numbers specials without a number after the numbered ones", () => {
    const tree = treeOf(
      [entry("x", "Show SP.mkv", "S", { category: "video" }), entry("y", "Show OVA.mkv", "S", { category: "video" })],
      "S",
    );
    const groups = groupMedia(tree);
    const ids: Identity[] = groups.map((g) => ({
      id: g.id,
      type: "special",
      title: "Show",
      year: null,
      yearSource: "none",
      source: "rules",
      season: null,
      numbers: [],
      episode: null,
      language: "",
      folder: "",
      reason: "",
    }));
    const p = buildPlan(tree, groups, ids, { naming: PRESETS.chinese });
    expect(target(byId(p.changes, "y"))).toBe("Show/特别篇/Show · SP01.mkv");
    expect(target(byId(p.changes, "x"))).toBe("Show/特别篇/Show · SP02.mkv");
  });
});
