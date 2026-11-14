import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { nextTick } from "vue";
import type * as Services from "../bindings/github.com/tonyflux/pkp/internal/services";
import type { DownloadBatchReport, DownloadTask, DriveStatus, Entry, FileBatchReport, FileBatchRequest } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  batch: vi.fn<(request: FileBatchRequest) => Promise<FileBatchReport>>(),
  snapshot: vi.fn<(account: string) => Promise<Entry[]>>(),
  download: vi.fn<(account: string, items: { id: string }[]) => Promise<DownloadBatchReport>>(),
}));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", async (importOriginal) => {
  const real = await importOriginal<typeof Services>();
  return {
    ...real,
    DriveService: {
      ...real.DriveService,
      RunFileBatch: backend.batch,
      TrashSnapshot: backend.snapshot,
      QueueDownloads: backend.download,
      Status: async () => ({ accountId: "a", configured: true }),
      FileDetail: async (id: string) => ({ id, name: id, category: "other", size: 1 }),
    },
  };
});
import { deleteForever, downloadEntries, drive, emptyTrash, setStarred, trash, ui } from "./store";
import { dismissFileTask, fileTasks, retryFileTask, taskCounts } from "./filetasks";

const entry = (id: string, folder = false): Entry => ({ id, name: id, parentId: "", size: 1, category: "other", folder, modifiedTime: "" } as Entry);
const report = (ids: string[], state = "done"): FileBatchReport => ({ items: ids.map((id) => ({ id, name: id, state, error: "" })), stopped: false });
function deferred() {
  let resolve!: (value: FileBatchReport) => void;
  // TypeScript 4.9 has no Promise.withResolvers declaration.
  const promise = new Promise<FileBatchReport>((done) => { resolve = done; });
  return { promise, resolve };
}
const tick = async () => { for (let n = 0; n < 12; n++) await Promise.resolve(); await nextTick(); };

beforeEach(() => {
  vi.useFakeTimers();
  vi.resetAllMocks();
  drive.status = { accountId: "a", configured: true } as DriveStatus;
  drive.live = true;
  drive.entries = [];
  drive.downloads = [];
  drive.picked = [];
  drive.trail = [];
  Object.assign(ui, { route: "files", view: "all", scope: "folder", category: "all", query: "", message: "" });
});
afterEach(() => {
  for (const task of [...fileTasks.items]) dismissFileTask(task.id);
  vi.clearAllTimers();
  vi.useRealTimers();
});

describe("background file operation integration", () => {
  it("queues conflicting work and preserves failed files and a newer selection", async () => {
    const first = deferred();
    backend.batch.mockReturnValueOnce(first.promise).mockImplementationOnce(async (request) => report((request.items ?? []).map((item) => item.id)));
    const rows = [entry("a"), entry("b"), entry("next")];
    drive.entries = rows;
    drive.picked = ["a", "b"];
    const running = trash(rows.slice(0, 2));
    drive.picked = ["b", "next"];
    const queued = setStarred([rows[2]], true);
    expect(fileTasks.items.map((task) => task.phase)).toEqual(["submitting", "queued"]);
    first.resolve({ items: [{ id: "a", name: "a", state: "done", error: "" }, { id: "b", name: "b", state: "failed", error: "没有权限" }], stopped: false });
    await Promise.all([running, queued]);
    await tick();
    expect(drive.entries.map((file) => file.id)).toEqual(["b", "next"]);
    expect(drive.picked).toEqual(["b", "next"]);
    expect(drive.entries.find((file) => file.id === "next")?.starred).toBe(true);
    expect(taskCounts(fileTasks.items[0])).toMatchObject({ done: 1, failed: 1 });
    expect(fileTasks.items[1].phase).toBe("done");
  });

  it("coalesces repeated clicks without issuing another destructive request", async () => {
    const first = deferred();
    backend.batch.mockReturnValueOnce(first.promise);
    drive.entries = [entry("same")];
    const a = deleteForever([...drive.entries]);
    const b = deleteForever([...drive.entries]);
    expect(fileTasks.items).toHaveLength(1);
    expect(backend.batch).toHaveBeenCalledTimes(1);
    first.resolve(report(["same"]));
    await Promise.all([a, b]);
    expect(drive.entries).toEqual([]);
  });

  it("keeps unknown results in place and retries only a known refusal", async () => {
    drive.entries = [entry("unknown"), entry("refused")];
    backend.batch.mockResolvedValueOnce({ ...report([]), items: [
      { id: "unknown", name: "unknown", state: "unknown", error: "云盘尚未确认结果" },
      { id: "refused", name: "refused", state: "failed", error: "没有权限" },
    ] }).mockImplementationOnce(async (request) => report((request.items ?? []).map((item) => item.id)));
    const task = await trash([...drive.entries]);
    expect(drive.entries.map((file) => file.id)).toEqual(["unknown", "refused"]);
    retryFileTask(task!.id, ["unknown", "refused"]);
    await tick();
    expect(drive.entries.map((file) => file.id)).toEqual(["unknown"]);
    expect(backend.batch).toHaveBeenCalledTimes(2);
    expect(fileTasks.items[1].items.map((item) => item.id)).toEqual(["refused"]);
  });

  it("executes a queued source snapshot in its original account after switching away", async () => {
    const hold = deferred();
    const cloud = { a: new Set<string>(), b: new Set<string>() };
    backend.batch.mockReturnValueOnce(hold.promise).mockImplementationOnce(async (request) => {
      for (const item of request.items ?? []) cloud[request.account as "a" | "b"].add(item.name);
      return report((request.items ?? []).map((item) => item.id));
    });
    const source = entry("snapshot-name");
    drive.entries = [entry("first"), source];
    const first = trash([drive.entries[0]]);
    const second = setStarred([source], true);
    source.name = "new-name-after-submission";
    drive.status = { accountId: "b", configured: true } as DriveStatus;
    drive.entries = [entry("b-file")];
    drive.picked = ["b-file"];
    hold.resolve(report(["first"]));
    await Promise.all([first, second]);
    expect([...cloud.a]).toEqual(["snapshot-name"]);
    expect([...cloud.b]).toEqual([]);
    expect(drive.entries.map((file) => file.id)).toEqual(["b-file"]);
    expect(drive.picked).toEqual(["b-file"]);
  });

  it("never grows a queued empty-trash scope to include later arrivals", async () => {
    const hold = deferred();
    const bin = new Set(["old"]);
    backend.snapshot.mockResolvedValueOnce([entry("old")]);
    backend.batch.mockReturnValueOnce(hold.promise).mockImplementationOnce(async (request) => {
      for (const item of request.items ?? []) bin.delete(item.id);
      return report((request.items ?? []).map((item) => item.id));
    });
    const first = trash([entry("in-flight")]);
    const clearing = emptyTrash();
    await tick();
    bin.add("arrived-later");
    hold.resolve(report(["in-flight"]));
    await Promise.all([first, clearing]);
    expect([...bin]).toEqual(["arrived-later"]);
  });

  it("retains successfully queued downloads when a folder is only partly submitted", async () => {
    const child = { id: "downloaded-child", name: "child", account: "a", state: "queued" } as DownloadTask;
    backend.download.mockResolvedValueOnce({ tasks: [child], items: [{ id: "folder", name: "folder", state: "unknown", error: "部分目录读取失败" }] })
      .mockResolvedValueOnce({ tasks: [], items: [{ id: "file", name: "file", state: "done", error: "" }] });
    drive.entries = [entry("folder", true), entry("file")];
    const task = await downloadEntries([...drive.entries]);
    expect(drive.downloads.map((download) => download.id)).toEqual(["downloaded-child"]);
    expect(drive.entries.map((file) => file.id)).toEqual(["folder", "file"]);
    expect(taskCounts(task!)).toMatchObject({ done: 1, unknown: 1 });
    retryFileTask(task!.id, ["folder"]);
    expect(backend.download).toHaveBeenCalledTimes(2);
  });
});
