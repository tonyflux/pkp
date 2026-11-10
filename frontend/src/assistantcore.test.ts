import { describe, expect, it } from "vitest";
import { readConversation } from "./assistantcore";
import { Handles } from "./agent";

const operation = {
  kind: "operation", id: "operation", title: "Star", note: "", state: "running", action: "star",
  rows: [], targetIds: [], folderId: "", path: "", name: "", scope: {},
  progress: { done: 1, total: 2 }, results: [{ id: "first", name: "First", error: "" }], stopped: false,
};
const resource = {
  kind: "resources", id: "resource", title: "Trash", note: "", state: "done", resource: "trash", nextPageToken: "",
  rows: [{ id: "trash-file", name: "Document", folder: false, path: "", modified: "", size: 1, kind: "trash_file", parentId: "", state: "", progress: 0 }],
};
function body(cards: unknown[]) {
  return JSON.stringify({ version: 1, turns: [{ id: "turn", cards, traces: [] }] });
}

describe("saved assistant confirmations", () => {
  it("marks every interrupted write as failed without replaying or losing accepted results", () => {
    const cards = [operation, ...["offline", "download", "share"].map((kind) => ({ kind, id: kind, state: "running", note: "", rows: [] }))];
    const loaded = readConversation(body(cards), "conversation")!;
    expect(loaded.turns[0].cards.map((card) => [card.kind, card.state])).toEqual([
      ["operation", "failed"], ["offline", "failed"], ["download", "failed"], ["share", "failed"],
    ]);
    const restored = loaded.turns[0].cards[0];
    expect(restored.kind === "operation" && restored.progress).toEqual({ done: 1, total: 2 });
    expect(restored.kind === "operation" && restored.results).toEqual([{ id: "first", name: "First", error: "" }]);
  });

  it.each(["draft", "done", "failed", "cancelled", "undone"])("preserves a %s confirmation when reopening", (state) => {
    expect(readConversation(body([{ ...operation, state }]), "conversation")?.turns[0].cards[0].state).toBe(state);
  });

  it.each(["draft", "running", "done", "failed", "cancelled", "undone"])("preserves a %s resources card when reopening", (state) => {
    expect(readConversation(body([{ ...resource, state }]), "conversation")?.turns[0].cards[0].state).toBe(state);
  });

  it("rejects malformed enum fields without coercion", () => {
    for (const value of [null, 7, {}, { toString: 1 }, "unknown"]) {
      expect(readConversation(body([{ ...resource, resource: value }]), "conversation")).toBeNull();
      expect(readConversation(body([{ ...resource, rows: [{ ...resource.rows[0], kind: value }] }]), "conversation")).toBeNull();
      expect(readConversation(body([{ ...operation, state: value }]), "conversation")).toBeNull();
      expect(readConversation(body([{ ...resource, state: value }]), "conversation")).toBeNull();
    }
  });

  it.each([
    null, 7, [], "record",
    { handle: "f9", id: "damaged", kind: { toString: 1 }, path: "" },
    { handle: "f9", id: "damaged", kind: "toString", path: "" },
    { handle: { toString: 1 }, id: "damaged", kind: "file", path: "" },
    { handle: "f9", id: { toString: 1 }, kind: "file", path: "" },
    { handle: "f9", id: "damaged", kind: "file", path: { toString: 1 } },
    { handle: "f9", id: "damaged", kind: "file", path: null },
  ])("restores valid handles around a damaged saved record: %j", (damaged) => {
    const valid = [
      { handle: "f1", id: "file", kind: "file", path: "" },
      { handle: "f2", id: "folder", kind: "folder", path: "Archive" },
      { handle: "c1", id: "task", kind: "task", path: "" },
      { handle: "t1", id: "file", kind: "trash_file", path: "" },
      { handle: "s1", id: "share", kind: "share", path: "" },
      { handle: "u1", id: "upload", kind: "upload", path: "" },
      { handle: "d1", id: "download", kind: "download", path: "" },
      { handle: "p1", id: "private-cursor", kind: "page", path: "trash" },
    ];
    const loaded = readConversation(JSON.stringify({
      version: 1, turns: [], handles: { records: [valid[0], damaged, ...valid.slice(1)] },
    }), "conversation");
    expect(loaded).not.toBeNull();
    const restored = new Handles(loaded!.handles);
    expect(restored.snapshot().records).toEqual(valid);
    expect(restored.of("next-file", "file")).toBe("f3");
    expect(restored.of("next-task", "task")).toBe("c2");
    expect(restored.resolve("t1")).toMatchObject({ id: "file", kind: "trash_file" });
  });
});
