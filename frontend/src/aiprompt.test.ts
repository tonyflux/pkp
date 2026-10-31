import { describe, expect, it } from "vitest";
import {
  estimateTokens,
  followUp,
  formatTokens,
  identify,
  mergeIdentities,
  ModelOutputError,
  parseIdentities,
  type ModelRequest,
} from "./aiprompt";
import { groupMedia, type Identity } from "./media";
import { treeOf } from "./organizer";
import type { Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";

const entry = (id: string, name: string, parentId: string, p: Partial<Entry> = {}): Entry =>
  ({
    id,
    name,
    parentId,
    kind: "drive#file",
    mimeType: "",
    size: 1024 ** 3,
    modifiedTime: "2024-05-06T07:08:09+08:00",
    thumbnail: "https://thumb.example/secret",
    folder: false,
    playable: false,
    starred: false,
    trashed: false,
    phase: "PHASE_TYPE_COMPLETE",
    category: "video",
    duration: 0,
    preview: "",
    path: "全部文件 / 影视",
    ...p,
  }) as Entry;

const identity = (p: Partial<Identity>): Identity => ({
  id: "",
  type: "series",
  title: "T",
  year: null,
  yearSource: "none",
  source: "model",
  season: 1,
  numbers: ["episode"],
  episode: null,
  language: "",
  folder: "",
  reason: "",
  ...p,
});

describe("identify (Film and TV identification)", () => {
  const tree = () => {
    const entries: Entry[] = [entry("D", "剧", "S", { folder: true, category: "folder" })];
    for (let s = 0; s < 30; s++) {
      const series = String.fromCharCode(65 + Math.floor(s / 26), 65 + (s % 26));
      for (let e = 1; e <= 5; e++) entries.push(entry(`s${s}e${e}`, `Show${series} - 0${e}.mkv`, "D"));
    }
    entries.push(entry("x", "readme.txt", "S", { category: "document" }));
    return treeOf(entries, "S");
  };

  it("batches the groups and counts the disclosure from the requests", () => {
    const groups = groupMedia(tree());
    const task = identify(groups, "动漫单独放", "待分类", 3000);
    expect(task.batches.length).toBeGreaterThan(1);
    expect(task.batches.flatMap((b) => b.groups)).toEqual(groups.map((g) => g.id));
    for (const b of task.batches) expect(b.groups.length).toBeLessThanOrEqual(60);
    const samples = groups.reduce((n, g) => n + g.samples.length, 0);
    expect(task.disclosure.files).toBe(samples);
    const body = task.batches.map((b) => b.request.messages[0].content).join("\n");
    expect(body).toContain("待分类/剧");
    expect(body).not.toContain("thumb.example");
    const sum = task.batches.reduce((n, b) => n + estimateTokens(b.request, b.expectedOutput).high, 0);
    expect(task.disclosure.estimate.high).toBe(sum);
  });

  it("reads identities strictly, as the model's", () => {
    const groups = groupMedia(tree());
    const g = groups.find((x) => x.variables.length === 1)!;
    const out = parseIdentities(
      {
        groups: [
          { id: g.id, type: "series", title: " ShowA0 ", year: 3000, season: 2, numbers: ["episode", "other"], episode: null, language: "chs/", x: 1 },
          { id: "g999", type: "movie", title: "nobody asked" },
          { id: g.id, type: "junk" },
        ],
      },
      groups,
    );
    expect(out).toEqual([identity({ id: g.id, title: "ShowA0", season: 2 })]);
    for (const bad of [
      { groups: [{ id: g.id, type: "tv" }] },
      { groups: [{ id: g.id, type: "series", numbers: ["ep"] }] },
      { groups: [{ id: g.id, type: "series", season: "1" }] },
      { groups: {} },
    ]) {
      expect(() => parseIdentities(bad, groups)).toThrow(ModelOutputError);
    }
  });

  it("follows up with patterns and the last answers only, then merges", () => {
    const groups = groupMedia(tree());
    const prev = groups.slice(0, 2).map((g) => identity({ id: g.id }));
    const p = followUp(prev, groups, "动漫单独放");
    const body = p.request.messages[0].content;
    expect(body).not.toContain("待分类");
    expect(body).not.toContain("剧/");
    expect(p.disclosure.files).toBe(0);
    const merged = mergeIdentities(prev, [{ ...prev[1], folder: "动漫" }]);
    expect(merged.map((i) => i.folder)).toEqual(["", "动漫"]);
  });

  it("never sends a TMDB title and year, and keeps them on merge", () => {
    const groups = groupMedia(tree());
    const prev = [
      identity({ id: groups[0].id, title: "山海纪行TMDB", year: 2023, yearSource: "tmdb", source: "tmdb" }),
      identity({ id: groups[1].id, title: "海岸纪行TMDB", year: 2019, yearSource: "tmdb", source: "tmdb" }),
      identity({ id: groups[2].id, title: "规则片名", year: 2001, yearSource: "filename", source: "rules" }),
    ];
    const p = followUp(prev, groups, "动漫单独放");
    const sent = JSON.stringify(p.request);
    expect(sent).not.toContain("山海纪行TMDB");
    expect(sent).not.toContain("海岸纪行TMDB");
    expect(sent).not.toContain("2023");
    expect(sent).not.toContain("2019");
    expect(sent).toContain("规则片名");
    const merged = mergeIdentities(prev, [
      identity({ id: groups[0].id, title: "模型乱改", year: 1999, yearSource: "guess", folder: "动漫" }),
      identity({ id: groups[2].id, title: "规则片名", folder: "动漫" }),
    ]);
    expect(merged[0]).toEqual({ ...prev[0], folder: "动漫" });
    expect(merged[1]).toBe(prev[1]);
    expect(merged[2]).toMatchObject({ source: "model", folder: "动漫" });
  });
});

describe("tokens", () => {
  it("estimates CJK above Latin and the answer in", () => {
    const req = (content: string): ModelRequest => ({
      system: "",
      messages: [{ role: "user", content }],
      outputName: "x",
      outputSchema: "{}",
      maxTokens: 0,
    });
    const cjk = estimateTokens(req("山".repeat(1000)), 0);
    const latin = estimateTokens(req("a".repeat(1000)), 0);
    expect(cjk.low).toBeGreaterThan(latin.high);
    const answered = estimateTokens(req("a"), 1000);
    expect(answered.low).toBeGreaterThanOrEqual(500);
    expect(answered.high).toBeGreaterThanOrEqual(1500);
  });

  it("formats like the card", () => {
    expect(formatTokens({ low: 800, high: 1200 })).toBe("约 800–1,200");
    expect(formatTokens({ low: 812, high: 1234 })).toBe("约 810–1,300");
    expect(formatTokens({ low: 12000, high: 18000 })).toBe("约 1.2–1.8 万");
    expect(formatTokens({ low: 8000, high: 12001 })).toBe("约 0.8–1.3 万");
    expect(formatTokens(50000)).toBe("5 万");
    expect(formatTokens(8000)).toBe("8,000");
  });
});
