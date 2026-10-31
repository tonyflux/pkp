import { describe, expect, it } from "vitest";
import { addToken, emptyQuery, FILTER_OPTIONS, folderToken, parseSearch, removeChip, searchText, sentenceSearch, type AIQuery } from "./searchquery";

// Friday 2026-10-16: the week runs Monday 10-12 to Sunday 10-18.
const today = new Date(2026, 9, 16, 15, 30);
const parse = (text: string) => parseSearch(text, today);
const GiB = 1024 ** 3;
const MiB = 1024 ** 2;

describe("parseSearch", () => {
  it("keeps plain words as keywords and is not filtered", () => {
    const p = parse("  山海纪行   s01 山海纪行 ");
    expect(p.keywords).toEqual(["山海纪行", "s01"]);
    expect(p.query).toEqual({ ...emptyQuery(), keywords: ["山海纪行", "s01"] });
    expect(p.chips).toEqual([]);
    expect(p.filtered).toBe(false);
  });

  it("reads sizes in binary units, strict bounds one byte in", () => {
    expect(parse(">1G").query.minSize).toBe(GiB + 1);
    expect(parse(">1G").chips[0].label).toBe("大小：> 1 GB");
    expect(parse("<500M").query.maxSize).toBe(500 * MiB - 1);
    expect(parse("<500M").chips[0].label).toBe("大小：< 500 MB");
    expect(parse(">=2GB").query.minSize).toBe(2 * GiB);
    expect(parse(">=2GB").chips[0].label).toBe("大小：≥ 2 GB");
    expect(parse("<=1.5g").query.maxSize).toBe(1.5 * GiB);
    expect(parse("大于1KiB").query.minSize).toBe(1025);
    expect(parse("size:<2T").query.maxSize).toBe(2 * 1024 ** 4 - 1);
    const both = parse(">1G <10G");
    expect([both.query.minSize, both.query.maxSize]).toEqual([GiB + 1, 10 * GiB - 1]);
    expect(both.chips.map((c) => c.key)).toEqual(["minSize", "maxSize"]);
    // No unit: a keyword, not a guess.
    expect(parse(">100").keywords).toEqual([">100"]);
  });

  it("reads Type / type in both languages and colons", () => {
    expect(parse("类型:视频").query.category).toBe("video");
    expect(parse("类型:视频").chips[0]).toEqual({ key: "category", label: "类型：视频", token: "类型:视频" });
    expect(parse("类型：文档").query.category).toBe("document");
    expect(parse("type:Image").query.category).toBe("image");
    expect(parse("type:other").chips[0].label).toBe("类型：其他");
    expect(parse("type:foo").keywords).toEqual(["type:foo"]);
  });

  it("reads extensions", () => {
    const p = parse("ext:MKV .mp4 ext:mkv");
    expect(p.query.ext).toEqual(["mkv", "mp4"]);
    expect(p.chips.map((c) => c.label)).toEqual(["扩展名：mkv", "扩展名：mp4"]);
    expect(parse("ext:mkv,ass").query.ext).toEqual(["mkv", "ass"]);
  });

  it("reads the folder to look under, quoted when it has spaces", () => {
    expect(parse("in:纪录片").query.under).toBe("纪录片");
    expect(parse("in:纪录片").chips[0].label).toBe("在：纪录片");
    expect(parse("在:/影视/纪录片/").query.under).toBe("影视/纪录片");
    const quoted = parse('在:"美剧 合集" 第一季');
    expect(quoted.query.under).toBe("美剧 合集");
    expect(quoted.keywords).toEqual(["第一季"]);
    expect(parse(folderToken("美剧 合集")).query.under).toBe("美剧 合集");
    expect(folderToken("影视/纪录片")).toBe("在:影视/纪录片");
  });

  it("reads play state words and played:", () => {
    expect(parse("没看完").query.played).toBe("unfinished");
    expect(parse("没看完").chips[0].label).toBe("播放：没看完");
    expect(parse("看过").query.played).toBe("finished");
    expect(parse("看完了").query.played).toBe("finished");
    expect(parse("播放过").query.played).toBe("any");
    expect(parse("played:unfinished").query.played).toBe("unfinished");
    expect(parse("played:finished").chips[0].label).toBe("播放：看过");
    expect(parse("played:any").query.played).toBe("any");
  });

  it("reads Starred and Folder", () => {
    expect(parse("星标").query.starred).toBe("yes");
    expect(parse("is:starred").chips[0].label).toBe("只看星标");
    expect(parse("文件夹").query.kind).toBe("folder");
    expect(parse("is:folder").chips[0].label).toBe("只看文件夹");
    expect(parse("is:file").query.kind).toBe("file");
  });

  it("reads saved dates relative to today, weeks from Monday", () => {
    const range = (t: string) => {
      const q = parse(t).query;
      return [q.createdAfter, q.createdBefore];
    };
    expect(range("今天")).toEqual(["2026-10-16", "2026-10-16"]);
    expect(range("昨天")).toEqual(["2026-10-15", "2026-10-15"]);
    expect(range("本周")).toEqual(["2026-10-12", "2026-10-18"]);
    expect(range("上周")).toEqual(["2026-10-05", "2026-10-11"]);
    expect(range("本月")).toEqual(["2026-10-01", "2026-10-31"]);
    expect(range("上月")).toEqual(["2026-09-01", "2026-09-30"]);
    expect(range("今年")).toEqual(["2026-01-01", "2026-12-31"]);
    expect(range("去年")).toEqual(["2025-01-01", "2025-12-31"]);
    expect(range("最近7天")).toEqual(["2026-10-10", "2026-10-16"]);
    expect(range("2026-10")).toEqual(["2026-10-01", "2026-10-31"]);
    expect(range("2026-02")).toEqual(["2026-02-01", "2026-02-28"]);
    expect(range("2026-10-01")).toEqual(["2026-10-01", "2026-10-01"]);
    expect(parse("上周").chips[0]).toEqual({ key: "created", label: "保存时间：上周", token: "上周" });
    expect(parse("最近7天").chips[0].label).toBe("保存时间：最近 7 天");
    expect(parse("created:lastweek").query.createdAfter).toBe("2026-10-05");
  });

  it("starts a Sunday's week on the Monday before", () => {
    const sunday = parseSearch("本周 上周", new Date(2026, 9, 18)).query;
    expect([sunday.createdAfter, sunday.createdBefore]).toEqual(["2026-10-05", "2026-10-11"]);
    expect(parseSearch("本周", new Date(2026, 9, 18)).query.createdAfter).toBe("2026-10-12");
  });

  it("reads modified dates and after/before bounds", () => {
    const m = parse("修改:上周").query;
    expect([m.modifiedAfter, m.modifiedBefore, m.createdAfter]).toEqual(["2026-10-05", "2026-10-11", ""]);
    expect(parse("修改:上周").chips[0].label).toBe("修改时间：上周");
    expect(parse("modified:thismonth").query.modifiedAfter).toBe("2026-10-01");
    const b = parse("after:2026-10-01 before:2026-10-31");
    expect([b.query.createdAfter, b.query.createdBefore]).toEqual(["2026-10-01", "2026-10-31"]);
    expect(b.chips).toEqual([{ key: "created", label: "保存时间：2026-10-01 至 2026-10-31", token: "after:2026-10-01 before:2026-10-31" }]);
    expect(parse("before:2026-02").query.createdBefore).toBe("2026-02-28");
    expect(parse("after:2026-09-01").chips[0].label).toBe("保存时间：2026-09-01 之后");
  });

  it("leaves words that only look like dates as keywords", () => {
    expect(parse("today").keywords).toEqual(["today"]);
    expect(parse("2026-02-30").keywords).toEqual(["2026-02-30"]);
    expect(parse("2026-13").keywords).toEqual(["2026-13"]);
    expect(parse("2023").keywords).toEqual(["2023"]);
  });

  it("reads aggregates and sorting", () => {
    expect(parse("按文件夹统计").query.aggregate).toEqual({ by: "folder", measure: "size", limit: 10 });
    expect(parse("按文件夹统计").chips[0].label).toBe("按文件夹统计大小");
    expect(parse("group:folder").query.aggregate).toEqual({ by: "folder", measure: "size", limit: 10 });
    expect(parse("group:ext").query.aggregate?.by).toBe("ext");
    const size = parse("排序:大小").query;
    expect([size.sort, size.asc]).toEqual(["size", false]);
    const name = parse("sort:name").query;
    expect([name.sort, name.asc]).toEqual(["name", true]);
    expect(parse("排序:时间").query.sort).toBe("modified");
    expect(parse("排序:播放").query.sort).toBe("played");
  });

  it("lets a later condition of one key override, as one chip", () => {
    const p = parse("类型:视频 abc 类型:图片");
    expect(p.query.category).toBe("image");
    expect(p.chips).toEqual([{ key: "category", label: "类型：图片", token: "类型:视频 类型:图片" }]);
  });

  it("treats a quoted token as a keyword", () => {
    const p = parse('"没看完" "two words"');
    expect(p.keywords).toEqual(["没看完", "two words"]);
    expect(p.filtered).toBe(false);
  });

  it("combines everything into one query", () => {
    const p = parse("纪录片 类型:视频 没看完 上周 >1G in:影视");
    expect(p.filtered).toBe(true);
    expect(p.query).toEqual({
      ...emptyQuery(),
      keywords: ["纪录片"],
      category: "video",
      played: "unfinished",
      createdAfter: "2026-10-05",
      createdBefore: "2026-10-11",
      minSize: GiB + 1,
      under: "影视",
      // A play condition orders by the last play unless a sort is typed.
      sort: "played",
    });
    expect(p.chips.map((c) => c.label)).toEqual(["类型：视频", "播放：没看完", "保存时间：上周", "大小：> 1 GB", "在：影视"]);
  });
  it("decodes JSON escapes only inside double quotes without changing ordinary backslashes", () => {
    expect(parse(String.raw`C:\new\title "line\nquote\" and \\ slash" "\u661f\u6807"`).keywords).toEqual([
      String.raw`C:\new\title`, 'line\nquote" and \\ slash', "星标",
    ]);
    expect(parse(String.raw`“C:\new\title” 「C:\new\title」`).keywords).toEqual([String.raw`C:\new\title`]);
    expect(parse(String.raw`"unknown\q"`).keywords).toEqual([String.raw`unknown\q`]);
  });

  it("rejects malformed absolute ranges and aggregates instead of losing keywords", () => {
    const invalid = ["created:2026-02-30..2026-03-01", "modified:..", "group:folder:average:10", "group:ext:count:51", "group:folder:count:-1", "group:folder:count:10:extra"];
    for (const token of invalid) {
      expect(parse(token).keywords).toEqual([token]);
      expect(parse(token).filtered).toBe(false);
    }
  });

});

describe("searchText", () => {
  const roundtrip = (changes: Partial<AIQuery>) => {
    const query = { ...emptyQuery(), ...changes };
    const parsed = parse(searchText(query));
    const { ids: _ids, folderId: _folderId, ...visible } = query;
    expect(parsed.query).toEqual({ ...visible, sort: query.sort || "modified", limit: 0 });
    expect(parsed.filtered).toBe(true);
    return parsed;
  };

  it("keeps reserved words, multiple words and literal quotes as keywords", () => {
    roundtrip({
      keywords: ["星标", "文件夹", "今天", "没看完", "type:video", ".mkv", ">1G", "two words", 'the "quoted" title', "“中文”「标题」", String.raw`C:\new\title`, "line\nbreak"],
    });
  });

  it("preserves every visible filter together, but leaves the file page its row limit", () => {
    roundtrip({
      keywords: ["山海 纪行", "2026-10-01"],
      category: "video", ext: ["mkv", "mp4"],
      minSize: GiB + 1, maxSize: 10 * GiB - 1,
      createdAfter: "2026-01-01", createdBefore: "2026-10-16",
      modifiedAfter: "2026-09-01", modifiedBefore: "2026-10-01",
      under: '影视/美剧 "合集"', starred: "no", played: "unfinished", kind: "file",
      sort: "size", asc: true, limit: 40,
      aggregate: { by: "folder", measure: "count", limit: 17 },
    });
  });

  it("retains inclusive byte boundaries without rounding to display units", () => {
    for (const bounds of [
      { minSize: 1 }, { maxSize: 1 }, { minSize: 1025, maxSize: 1025 },
      { minSize: GiB + 1, maxSize: Number.MAX_SAFE_INTEGER },
    ]) roundtrip(bounds);
  });

  it("keeps both dates and each open side on created and modified dates", () => {
    for (const field of ["created", "modified"] as const) {
      for (const [after, before] of [["2026-02-01", "2026-02-28"], ["2026-02-01", ""], ["", "2026-02-28"], ["2026-02-01", "2026-02-01"]]) {
        roundtrip({ [`${field}After`]: after, [`${field}Before`]: before });
      }
    }
  });

  it("keeps path spaces, full-width colons, quotes and literal backslashes", () => {
    for (const under of ['影视/美剧 "合集"/第 一季', '资料：备份/「原稿」', String.raw`C:\new\title "合集"`, String.raw`备份\原稿`]) {
      roundtrip({ under });
      expect(parse(folderToken(under)).query.under).toBe(under);
    }
  });

  it("keeps category, extensions, starred exclusion, file and folder filters", () => {
    for (const category of ["video", "image", "document", "other"]) roundtrip({ category });
    roundtrip({ ext: ["mkv", "mp4", "ass"] });
    for (const starred of ["yes", "no"]) roundtrip({ starred });
    for (const kind of ["file", "folder"]) roundtrip({ kind });
  });

  it("preserves aggregate grouping, measure and its own result limit", () => {
    for (const by of ["folder", "category", "ext"]) {
      for (const measure of ["size", "count"]) {
        for (const limit of [0, 1, 10, 50]) roundtrip({ aggregate: { by, measure, limit } });
      }
    }
    const parsed = roundtrip({ aggregate: { by: "ext", measure: "count", limit: 7 } });
    expect(parsed.chips.find((chip) => chip.key === "aggregate")?.label).toBe("统计：扩展名 · 数量 · 最多 7 组");
  });

  it("makes empty and keyword-only queries filtered with explicit modified order", () => {
    roundtrip({});
    roundtrip({ keywords: ["纪录片"], limit: 40 });
    roundtrip({ ids: ["selected-id"], folderId: "local-folder", keywords: ["纪录片"] });
  });

  it("preserves every explicit sort direction rather than a sort token's default", () => {
    for (const sort of ["", "modified", "created", "size", "name", "played"]) {
      for (const asc of [false, true]) roundtrip({ sort, asc });
    }
  });

  it("does not let played conditions override the source query's order", () => {
    for (const played of ["any", "unfinished", "finished"] as const) {
      roundtrip({ played });
      roundtrip({ played, sort: "name", asc: true });
      roundtrip({ played, sort: "played" });
    }
  });
});

describe("removeChip / addToken", () => {
  it("removes exactly one chip's tokens and keeps the rest", () => {
    const text = "纪录片 类型:视频 没看完 上周 >1G 类型:图片";
    const before = parse(text);
    for (const chip of before.chips) {
      const after = parse(removeChip(text, chip));
      expect(after.chips.map((c) => c.key)).toEqual(before.chips.filter((c) => c.key !== chip.key).map((c) => c.key));
      expect(after.keywords).toEqual(["纪录片"]);
    }
    const all = before.chips.reduce((t, c) => removeChip(t, c), text);
    expect(all).toBe("纪录片");
  });

  it("removes a quoted folder chip whole", () => {
    const text = '在:"美剧 合集" 第一季';
    const chip = parse(text).chips[0];
    expect(removeChip(text, chip)).toBe("第一季");
  });

  it("adds a condition, replacing one of the same key", () => {
    expect(addToken("abc", "类型:视频")).toBe("abc 类型:视频");
    expect(addToken("类型:视频 abc", "类型:图片")).toBe("abc 类型:图片");
    expect(addToken("上周 abc", "本月")).toBe("abc 本月");
    expect(addToken("修改:上周 abc", "本月")).toBe("修改:上周 abc 本月");
    expect(addToken("", "没看完")).toBe("没看完");
  });

  it("offers menu tokens that each parse back to one chip", () => {
    const groups = FILTER_OPTIONS.map((g) => g.group);
    expect(groups).toEqual(["类型", "大小", "保存时间", "修改时间", "播放", "星标", "只看文件夹", "统计"]);
    for (const g of FILTER_OPTIONS) {
      for (const o of g.options) {
        const p = parse(o.token);
        expect(p.chips, o.token).toHaveLength(1);
        expect(p.keywords, o.token).toEqual([]);
        expect(p.filtered).toBe(true);
      }
    }
    const modified = FILTER_OPTIONS.find((g) => g.group === "修改时间")!;
    expect(modified.options.every((o) => parse(o.token).chips[0].key === "modified")).toBe(true);
  });
});

describe("sentenceSearch", () => {
  const read = (text: string) => sentenceSearch(text, today);

  it("turns a sentence into conditions and the words that name things", () => {
    const p = read("整理上周云添加的山海纪行");
    expect(p.keywords).toEqual(["山海纪行"]);
    // Last Week in a sentence runs from last Monday to today.
    expect(p.query.createdAfter).toBe("2026-10-05");
    expect(p.query.createdBefore).toBe("");
  });

  it("drops a destination and reads genre and play words as conditions", () => {
    const p = read("把上周云添加的纪录片放到「纪录片」里，集数补零");
    expect(p.keywords).toEqual([]);
    expect(p.query.category).toBe("video");
    expect(read("找上次没看完的纪录片").query.played).toBe("unfinished");
  });

  it("reads typed syntax as the search box does", () => {
    expect(read("类型:视频 没看完")).toEqual(parseSearch("类型:视频 没看完", today));
  });
});
