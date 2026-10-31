import { describe, expect, it } from "vitest";
import { applyRules, renameChanges, type RenameRule } from "./renamerules";
import type { Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";

const strip: RenameRule[] = [{ kind: "stripTags" }];
const pad = (width: number): RenameRule[] => [{ kind: "padNumbers", width }];

const entry = (id: string, name: string, p: Partial<Entry> = {}): Entry =>
  ({
    id,
    name,
    parentId: "p1",
    kind: "drive#file",
    mimeType: "",
    size: 1024,
    modifiedTime: "2026-10-01T00:00:00+08:00",
    thumbnail: "",
    folder: false,
    playable: false,
    starred: false,
    trashed: false,
    phase: "PHASE_TYPE_COMPLETE",
    category: "video",
    duration: 0,
    preview: "",
    path: "",
    deleteTime: "",
    ...p,
  }) as Entry;

describe("stripTags", () => {
  it("removes release tags at both ends and keeps title, episode, year and extension", () => {
    expect(applyRules("[SweetSub] 山海纪行 - 03 [WebRip 1080p HEVC-10bit AAC][简繁内封].mkv", strip)).toBe("山海纪行 - 03.mkv");
    expect(applyRules("【幻樱字幕组】山海纪行 第03集【1080P】.mp4", strip)).toBe("山海纪行 第03集.mp4");
    expect(applyRules("Movie Title (2023) [2160p].mkv", strip)).toBe("Movie Title (2023).mkv");
    expect(applyRules("(BDRip 1080p) Title.mkv", strip)).toBe("Title.mkv");
    expect(applyRules("Show [01][1080p].mkv", strip)).toBe("Show [01].mkv");
  });

  it("reads a name made only of brackets as group, title, numbers", () => {
    expect(applyRules("[Nekomoe kissaten][Shanhai][01][1080p][CHS].mp4", strip)).toBe("Shanhai [01].mp4");
    expect(applyRules("[山海纪行][02].ass", strip)).toBe("山海纪行 [02].ass");
  });

  it("leaves untagged names and middle brackets alone", () => {
    expect(applyRules("山海纪行 S01E01.mkv", strip)).toBe("山海纪行 S01E01.mkv");
    expect(applyRules("A [B] C.mkv", strip)).toBe("A [B] C.mkv");
  });
});

describe("padNumbers", () => {
  it("pads episode and season numbers", () => {
    expect(applyRules("Show - 3.mkv", pad(2))).toBe("Show - 03.mkv");
    expect(applyRules("Show S1E3 1080p x264.mkv", pad(2))).toBe("Show S01E03 1080p x264.mkv");
    expect(applyRules("山海纪行 第3集.mp4", pad(2))).toBe("山海纪行 第03集.mp4");
    expect(applyRules("Show.EP7.2160p.HDR10.mkv", pad(2))).toBe("Show.EP07.2160p.HDR10.mkv");
    expect(applyRules("Show 1x5.mkv", pad(2))).toBe("Show 01x05.mkv");
    expect(applyRules("Show.2023.3.720p.mkv", pad(2))).toBe("Show.2023.03.720p.mkv");
    expect(applyRules("Show 1.5.mkv", pad(2))).toBe("Show 1.5.mkv");
    expect(applyRules("Show - 03 (2023).mkv", pad(3))).toBe("Show - 003 (2023).mkv");
  });

  it("leaves years, resolutions, codecs and audio alone", () => {
    expect(applyRules("Movie 2023 4K.mkv", pad(2))).toBe("Movie 2023 4K.mkv");
    expect(applyRules("Show 1920x1080 3.mkv", pad(5))).toBe("Show 1920x1080 00003.mkv");
    expect(applyRules("Show 3 AAC2.0 5.1 H.264 10bit.mkv", pad(4))).toBe("Show 0003 AAC2.0 5.1 H.264 10bit.mkv");
    expect(applyRules("Movie (2023) 720p.mkv", pad(6))).toBe("Movie (2023) 720p.mkv");
    expect(applyRules("Show 01v2.mkv", pad(2))).toBe("Show 01v2.mkv");
  });

  it("keeps the extension even when it holds digits", () => {
    expect(applyRules("Track 3.mp3", pad(2))).toBe("Track 03.mp3");
    expect(applyRules("Clip 3.mp4", pad(3))).toBe("Clip 003.mp4");
  });
});

describe("replace and folders", () => {
  it("replaces in the name only, all occurrences, literally", () => {
    expect(applyRules("Show.S01E01.Title.mkv", [{ kind: "replace", find: ".", with: " " }])).toBe("Show S01E01 Title.mkv");
    expect(applyRules("a(1).mkv", [{ kind: "replace", find: "(1)", with: "" }])).toBe("a.mkv");
    expect(applyRules("abc.mkv", [{ kind: "replace", find: "", with: "x" }])).toBe("abc.mkv");
    // A rule that would leave nothing keeps what was there.
    expect(applyRules("abc.mkv", [{ kind: "replace", find: "abc", with: "" }])).toBe("abc.mkv");
  });

  it("applies rules in order", () => {
    const rules: RenameRule[] = [{ kind: "stripTags" }, { kind: "padNumbers", width: 2 }, { kind: "replace", find: " - ", with: " " }];
    expect(applyRules("[Group] 山海纪行 - 3 [1080p].mkv", rules)).toBe("山海纪行 03.mkv");
  });

  it("treats a folder name as having no extension", () => {
    expect(applyRules("Vol.2", pad(2), true)).toBe("Vol.02");
    expect(applyRules("Season 1", pad(2), true)).toBe("Season 01");
  });
});

describe("renameChanges", () => {
  it("proposes in-place renames for changed names only", () => {
    const entries = [
      entry("a", "[G] 剧 - 1 [1080p].mkv"),
      entry("b", "剧 - 02.mkv"),
      entry("c", "Season 1", { folder: true, category: "" }),
      entry("d", "[G] 剧 - 3.ass", { category: "other" }),
    ];
    const changes = renameChanges(entries, [{ kind: "stripTags" }, { kind: "padNumbers", width: 2 }], (e) => (e.id === "c" ? ["影视"] : ["影视", "剧"]));
    expect(changes.map((c) => [c.item.id, c.name])).toEqual([
      ["a", "剧 - 01.mkv"],
      ["c", "Season 01"],
      ["d", "剧 - 03.ass"],
    ]);
    const a = changes[0];
    expect(a).toMatchObject({ op: "move", from: ["影视", "剧"], toPath: ["影视", "剧"], proposed: "剧 - 01.mkv", selected: true, locked: "", note: "", role: "video" });
    expect(a.toPath).not.toBe(a.from);
    expect(changes[1].role).toBe("folder");
    expect(changes[2].role).toBe("subtitle");
  });

  it("keeps a name no folder can hold unticked, with the reason", () => {
    const changes = renameChanges([entry("a", "a-b.mkv")], [{ kind: "replace", find: "-", with: "/" }], () => []);
    expect(changes).toHaveLength(1);
    expect(changes[0]).toMatchObject({ name: "a/b.mkv", selected: false });
    expect(changes[0].note).toContain("名称不能含路径分隔符");
  });
});
