import { describe, expect, it } from "vitest";
import {
  active,
  edited,
  nameError,
  nameKey,
  PLAN_LIMIT,
  problems,
  rename,
  summarize,
  toChanges,
  treeOf,
  type Change,
  type Tree,
} from "./organizer";
import type { Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";

let serial = 0;
const entry = (name: string, parentId: string, p: Partial<Entry> = {}): Entry =>
  ({
    id: p.id ?? `e${++serial}`,
    name,
    parentId,
    kind: p.folder ? "drive#folder" : "drive#file",
    mimeType: "",
    size: 1,
    modifiedTime: "",
    thumbnail: "",
    folder: false,
    playable: false,
    starred: false,
    trashed: false,
    phase: "PHASE_TYPE_COMPLETE",
    category: p.folder ? "folder" : "video",
    duration: 0,
    preview: "",
    path: "",
    ...p,
  }) as Entry;
const folder = (id: string, name: string, parentId: string) => entry(name, parentId, { id, folder: true });

const move = (tree: Tree, id: string, toPath: string[], name?: string, p: Partial<Change> = {}): Change => {
  const item = tree.byId.get(id)!;
  const n = name ?? item.name;
  return {
    item,
    from: tree.pathOf(item.parentId),
    op: "move",
    toPath,
    name: n,
    proposed: n,
    selected: true,
    locked: "",
    note: "",
    series: "",
    role: item.folder ? "folder" : "video",
    ...p,
  };
};

// Scope S: D/ (a.mkv, b.mkv), E/ (F/ (c.mkv)), x.mkv, a file named Shanhai Journey, two folders named Duplicate
const scope = () =>
  treeOf(
    [
      folder("D", "D", "S"),
      entry("a.mkv", "D", { id: "a" }),
      entry("b.mkv", "D", { id: "b" }),
      folder("E", "E", "S"),
      folder("F", "F", "E"),
      entry("c.mkv", "F", { id: "c", size: 7 }),
      entry("x.mkv", "S", { id: "x", size: 5 }),
      entry("山海纪行", "S", { id: "file" }),
      folder("T1", "双", "S"),
      folder("T2", "双", "S"),
    ],
    "S",
  );

describe("tree", () => {
  it("knows paths and ancestors below the scope", () => {
    const t = scope();
    expect(t.pathOf("c")).toEqual(["E", "F", "c.mkv"]);
    expect(t.pathOf("S")).toEqual([]);
    expect(t.ancestorsOf("c")).toEqual(["F", "E"]);
    expect(t.childrenOf("S").map((e) => e.id)).toEqual(["D", "E", "x", "file", "T1", "T2"]);
  });
});

describe("names", () => {
  it("compare regardless of case and Unicode form", () => {
    expect(nameKey("Cafe\u0301.MKV")).toBe(nameKey("café.mkv"));
  });
  it("refuses what no folder can hold", () => {
    expect(nameError("a/b")).not.toBe("");
    expect(nameError("a.")).not.toBe("");
    expect(nameError(" ")).toBe("请输入文件名");
    expect(nameError("山海纪行 - S01E01.mkv")).toBe("");
  });
  it("rename keeps the proposal so an edit shows", () => {
    const t = scope();
    const c = move(t, "a", ["D"], "A1.mkv");
    const r = rename(c, " mine.mkv ");
    expect(r.name).toBe("mine.mkv");
    expect(r.proposed).toBe("A1.mkv");
    expect(edited(r)).toBe(true);
    expect(edited(c)).toBe(false);
  });
});

describe("problems", () => {
  it("reuses an existing folder and makes the missing layers", () => {
    const t = scope();
    const changes = [move(t, "x", ["D", "Season 01"]), move(t, "c", ["D", "Season 01"], "c2.mkv")];
    const errors = problems(changes, t);
    expect(errors.size).toBe(0);
    expect(summarize(changes, t, errors).newFolders).toEqual([["D", "Season 01"]]);
  });

  it("refuses every item through a layer held by a file", () => {
    const t = scope();
    const changes = [move(t, "x", ["山海纪行", "Season 01"]), move(t, "a", ["山海纪行"])];
    const errors = problems(changes, t);
    expect(errors.get("x")).toBe("「山海纪行」已被同名文件占用");
    expect(errors.get("a")).toBe("「山海纪行」已被同名文件占用");
  });

  it("refuses a layer several entries hold", () => {
    const t = scope();
    expect(problems([move(t, "x", ["双"])], t).get("x")).toBe("这里有多个「双」");
  });

  it("refuses destination folders with case-folded Unicode name collisions", () => {
    const t = treeOf([
      folder("a", "Café", "S"),
      folder("b", "CAFE\u0301", "S"),
      entry("video.mkv", "S", { id: "video" }),
    ], "S");
    const changes = [move(t, "video", ["café"])];
    const errors = problems(changes, t);
    expect([...errors.keys()]).toEqual(["video"]);
    expect(summarize(changes, t, errors).newFolders).toEqual([]);
  });

  it("marks both sides of a clash, case and form aside", () => {
    const t = scope();
    const changes = [move(t, "x", ["新"], "Same.mkv"), move(t, "c", ["新"], "same.MKV")];
    const errors = problems(changes, t);
    expect(errors.has("x")).toBe(true);
    expect(errors.has("c")).toBe(true);
  });

  it("refuses a folder and an item inside it changed together", () => {
    const t = scope();
    const changes = [move(t, "E", [], "E2"), move(t, "c", ["E", "F"], "c2.mkv")];
    const errors = problems(changes, t);
    expect(errors.has("E")).toBe(true);
    expect(errors.has("c")).toBe(true);
  });

  it("refuses moving into a folder the plan itself renames, on both sides", () => {
    const t = scope();
    const errors = problems([move(t, "D", [], "D2"), move(t, "x", ["D"])], t);
    expect(errors.get("x")).toBe("「D」本身也在这次计划里改动");
    expect(errors.has("D")).toBe(true);
  });

  it("lets emptied source folders go after their contents", () => {
    const t = scope();
    const gone = move(t, "F", ["E"], "F", { op: "trashIfEmpty", toPath: ["E"] });
    expect(problems([move(t, "c", ["新"]), gone], t).size).toBe(0);
  });

  it("an unticked item keeps its name", () => {
    const t = scope();
    const changes = [move(t, "a", ["新"], "a2.mkv", { selected: false }), move(t, "b", ["D"], "A.MKV")];
    const errors = problems(changes, t);
    expect(errors.get("b")).toBe("目标位置重名，请修改名称或取消此项");
    expect(errors.has("a")).toBe(false);
  });

  it("a trashed item holds its name until the end", () => {
    const t = scope();
    const changes = [move(t, "a", ["D"], "a.mkv", { op: "trash" }), move(t, "b", ["D"], "a.mkv")];
    expect(problems(changes, t).has("b")).toBe(true);
  });

  it("a new folder clashing with a moved file fails both", () => {
    const t = scope();
    const changes = [move(t, "x", ["D"], "Season 01"), move(t, "c", ["D", "Season 01"])];
    const errors = problems(changes, t);
    expect(errors.has("x")).toBe(true);
    expect(errors.get("c")).toBe("要新建的文件夹「Season 01」和那里的其他项重名");
  });

  it("checks generated and edited names alike", () => {
    const t = scope();
    expect(problems([move(t, "x", ["D"], "a:b.mkv")], t).get("x")).toBe(nameError("a:b.mkv"));
    expect(problems([move(t, "x", ["bad."])], t).get("x")).toContain("文件夹名「bad.」不可用");
  });
});

describe("summary and request", () => {
  it("counts what the run does", () => {
    const t = scope();
    const changes = [
      move(t, "x", ["D"], "x2.mkv"),
      move(t, "a", ["D"], "a2.mkv"),
      move(t, "c", ["E", "F"], "c.mkv", { op: "trash" }),
      move(t, "b", ["新"], "b.mkv", { selected: false }),
    ];
    const s = summarize(changes, t, problems(changes, t));
    expect(s).toEqual({
      selected: 3,
      moves: 1,
      renames: 2,
      newFolders: [],
      trash: { count: 1, size: 7 },
      trashIfEmpty: 0,
      conflicts: 0,
      overLimit: false,
    });
  });

  it("refuses a plan over the limit", () => {
    const entries: Entry[] = [];
    for (let i = 0; i <= PLAN_LIMIT; i++) entries.push(entry(`f${i}.mkv`, "S", { id: `f${i}` }));
    const t = treeOf(entries, "S");
    const changes = entries.map((e) => move(t, e.id, ["新"]));
    expect(summarize(changes, t, new Map()).overLimit).toBe(true);
    expect(summarize(changes.slice(1), t, new Map()).overLimit).toBe(false);
  });

  it("sends paths, keeps names and puts emptied folders last, deepest first", () => {
    const t = scope();
    const changes = [
      move(t, "E", [], "E", { op: "trashIfEmpty" }),
      move(t, "F", ["E"], "F", { op: "trashIfEmpty" }),
      move(t, "c", ["新", "层"], "c2.mkv"),
      move(t, "a", ["D"], "a.mkv", { op: "trash" }),
      move(t, "b", ["D"]),
    ];
    expect(active(changes).map((c) => c.item.id)).toEqual(["E", "F", "c", "a"]);
    expect(toChanges(changes)).toEqual([
      { fileId: "c", folder: false, name: "c.mkv", parentId: "F", op: "", newName: "c2.mkv", toPath: ["新", "层"], toParentId: "" },
      { fileId: "a", folder: false, name: "a.mkv", parentId: "D", op: "trash", newName: "", toPath: ["D"], toParentId: "" },
      { fileId: "F", folder: true, name: "F", parentId: "E", op: "trashIfEmpty", newName: "", toPath: ["E"], toParentId: "" },
      { fileId: "E", folder: true, name: "E", parentId: "S", op: "trashIfEmpty", newName: "", toPath: [], toParentId: "" },
    ]);
  });
});
