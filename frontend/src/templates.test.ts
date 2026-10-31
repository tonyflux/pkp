import { describe, expect, it } from "vitest";
import { namingFor, PRESETS, previewPath, renderTemplate, sanitizeValue, templateError } from "./templates";

const episode = { title: "山海纪行", year: null, season: 1, episode: 1, version: "2160p" };

describe("presets", () => {
  it("Plex / Infuse renders the expected names", () => {
    expect(renderTemplate(PRESETS.plex.series, episode)).toEqual(["山海纪行", "Season 01", "山海纪行 - S01E01 - 2160p"]);
    expect(renderTemplate(PRESETS.plex.series, { ...episode, year: 2023, version: "" })).toEqual([
      "山海纪行 (2023)",
      "Season 01",
      "山海纪行 - S01E01",
    ]);
    expect(renderTemplate(PRESETS.plex.movie, { title: "长河", year: 2021, version: "1080p" })).toEqual([
      "长河 (2021)",
      "长河 (2021) - 1080p",
    ]);
  });

  it("Chinese renders the expected names", () => {
    expect(renderTemplate(PRESETS.chinese.series, episode)).toEqual(["山海纪行", "第1季", "山海纪行 · 第01集 (2160p)"]);
    expect(renderTemplate(PRESETS.chinese.movie, { title: "长河", year: null, version: "" })).toEqual(["长河", "长河"]);
  });

  it("puts specials in Season 00 or Specials", () => {
    expect(renderTemplate(PRESETS.plex.special, { ...episode, season: 0, version: "" })).toEqual([
      "山海纪行",
      "Season 00",
      "山海纪行 - S00E01",
    ]);
    expect(renderTemplate(PRESETS.chinese.special, { ...episode, season: 0, episode: 2, version: "" })).toEqual([
      "山海纪行",
      "特别篇",
      "山海纪行 · SP02",
    ]);
    const custom = namingFor("custom", { series: "{title}/{title} {episode}", movie: "{title}" });
    expect(custom.special).toBe(custom.series);
  });

  it("previews the settings sample", () => {
    expect(previewPath(PRESETS.plex.series, "series")).toBe("山海纪行 / Season 01 / 山海纪行 - S01E01 - 2160p.mkv");
    expect(previewPath(PRESETS.chinese.series, "series")).toBe("山海纪行 / 第1季 / 山海纪行 · 第01集 (2160p).mkv");
    expect(previewPath("{title", "series")).toBe("");
  });
});

describe("syntax", () => {
  it("drops an optional segment when any variable in it is empty", () => {
    expect(renderTemplate("{title}< [{year} {version}]>", { title: "A", year: 2020, version: "" })).toEqual(["A"]);
    expect(renderTemplate("{title}< [{year} {version}]>", { title: "A", year: 2020, version: "4K" })).toEqual(["A [2020 4K]"]);
  });

  it("pads episodes to the series' widest number, at least 2", () => {
    expect(renderTemplate("E{episode}", { episode: 7 })).toEqual(["E07"]);
    expect(renderTemplate("E{episode}", { episode: 7, episodeWidth: 3 })).toEqual(["E007"]);
    expect(renderTemplate("E{episode:1}", { episode: 7, episodeWidth: 3 })).toEqual(["E7"]);
    expect(renderTemplate("S{season:02}/S{season}", { season: 3 })).toEqual(["S03", "S3"]);
  });

  it("sanitizes values a model wrote", () => {
    expect(sanitizeValue(' Fate/Zero: "Part"  2.. ')).toBe("Fate Zero Part 2");
    expect(renderTemplate("{title}/{title}", { title: "a<b>|c?" })).toEqual(["a b c", "a b c"]);
  });
});

describe("templateError", () => {
  it("accepts the presets", () => {
    for (const p of [PRESETS.plex, PRESETS.chinese]) {
      expect(templateError(p.series, "series")).toBe("");
      expect(templateError(p.movie, "movie")).toBe("");
    }
  });

  it("refuses a series template without {episode}", () => {
    expect(templateError("{title}/第{season}季/{title}", "series")).toBe("剧集模板必须包含 {episode}");
    expect(templateError("{title}", "movie")).toBe("");
  });

  it("refuses unknown variables, bad widths and broken syntax", () => {
    expect(templateError("{name}/{episode}", "series")).toContain("没有变量 {name}");
    expect(templateError("{title:02}/{episode}", "series")).toBe("只有 {season} 和 {episode} 可以补零");
    expect(templateError("{title}/{episode:x}", "series")).toContain("写法不对");
    expect(templateError("{title}<<{year}>>/{episode}", "series")).toBe("可选段 <…> 不能嵌套");
    expect(templateError("{title}< ({year})/{episode}", "series")).toBe("可选段 <…> 里不能有 /");
  });

  it("refuses more than 4 levels", () => {
    expect(templateError("a/b/c/d/{episode}", "series")).toContain("最多 4 层");
    expect(templateError("a/b/c/{episode}", "series")).toBe("");
  });

  it("refuses empty levels and names no folder can hold", () => {
    expect(templateError("{title}//{episode}", "series")).toContain("不能为空");
    expect(templateError("<{year}>/{title} {episode}", "series")).toContain("不能为空");
    expect(templateError("{title}: {episode}", "series")).toContain("名称不能含");
    expect(templateError("{title} {episode} <{version}>", "series")).toContain("名称不能含");
  });

  it("falls back to Plex for a custom template that no longer passes", () => {
    const n = namingFor("custom", { series: "{title}", movie: "{title}/{title}" });
    expect(n.series).toBe(PRESETS.plex.series);
    expect(n.movie).toBe("{title}/{title}");
    expect(namingFor("anything")).toBe(PRESETS.plex);
  });
});
