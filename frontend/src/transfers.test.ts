import { describe, expect, it } from "vitest";
import { batchPlan, counts, fromDownload, fromTask, fromUpload, mergeMoving, retryLabel, sections, stateLabel } from "./transfers";
import type { DownloadTask, TaskSummary, UploadTask } from "../bindings/github.com/tonyflux/pkp/internal/services";

const task = (p: Partial<TaskSummary>): TaskSummary =>
  ({ id: "t", name: "a.mp4", phase: "PHASE_TYPE_RUNNING", progress: 0, fileId: "", fileSize: 0, message: "", errorDetail: "", category: "video", createdTime: "", updatedTime: "", savedTo: "", account: "main", ...p }) as TaskSummary;

const upload = (p: Partial<UploadTask>): UploadTask =>
  ({ id: "u1", name: "a.bin", path: "/tmp/a.bin", parentId: "", dir: "", size: 0, state: "queued", done: 0, speed: 0, error: "", fileId: "", resumable: false, savedTo: "", updatedAt: "", category: "other", account: "main", ...p }) as UploadTask;

const download = (p: Partial<DownloadTask>): DownloadTask =>
  ({ id: "d1", fileId: "f1", name: "a.mkv", path: "/Users/me/Downloads/a.mkv", origin: "全部文件 / 电影", size: 0, state: "queued", done: 0, speed: 0, threads: 0, error: "", resumable: false, updatedAt: "", category: "video", account: "main", savedTo: "~/Downloads", ...p }) as DownloadTask;

describe("transfer states", () => {
  it("only a moving transfer has progress; waiting and finished ones never draw a bar", () => {
    expect(fromTask(task({ phase: "PHASE_TYPE_RUNNING", progress: 68, fileSize: 1000 }))).toMatchObject({ progress: 68, doneBytes: 680 });
    expect(fromTask(task({ phase: "PHASE_TYPE_PENDING", progress: 0 })).progress).toBeNull();
    expect(fromTask(task({ phase: "PHASE_TYPE_COMPLETE", progress: 100 })).progress).toBeNull();
    expect(fromUpload(upload({ state: "queued" })).progress).toBeNull();
    expect(fromUpload(upload({ state: "done", size: 10, done: 10 })).progress).toBeNull();
    // A server overshoot or a negative tick must not overflow the track.
    expect(fromTask(task({ phase: "PHASE_TYPE_RUNNING", progress: 140 })).progress).toBe(100);
  });

  it("an upload's percent comes from bytes sent; an empty file is not stuck at 0%", () => {
    expect(fromUpload(upload({ state: "uploading", size: 200, done: 50, speed: 7 }))).toMatchObject({ progress: 25, doneBytes: 50, speed: 7 });
    expect(fromUpload(upload({ state: "uploading", size: 0 })).progress).toBe(100);
  });

  it("says a reason only for failures, never the server's 'Saved'", () => {
    expect(fromTask(task({ phase: "PHASE_TYPE_COMPLETE", message: "Saved" })).reason).toBe("");
    expect(fromTask(task({ phase: "PHASE_TYPE_ERROR", message: "链接解析失败" })).reason).toBe("链接解析失败");
    expect(fromTask(task({ phase: "PHASE_TYPE_ERROR" })).reason).toBe("云添加失败");
    expect(fromUpload(upload({ state: "cancelled", error: "" })).reason).toBe("");
  });

  it("never puts English on the row: the server's words go to the details", () => {
    const generic = fromTask(task({ phase: "PHASE_TYPE_ERROR", message: "Resource not found" }));
    expect(generic).toMatchObject({ reason: "云添加失败", rawError: "Resource not found", fileGone: false });
    const up = fromUpload(upload({ state: "failed", error: "drive: file_name_invalid (code 10): The file name is invalid" }));
    expect(up).toMatchObject({ reason: "上传失败", rawError: "drive: file_name_invalid (code 10): The file name is invalid" });
    // Chinese from the engines is already the reason; nothing is kept twice.
    expect(fromDownload(download({ state: "failed", error: "磁盘空间不足" }))).toMatchObject({ reason: "磁盘空间不足", rawError: "" });
  });

  it("reads a deleted file from the code, whichever message the server chose", () => {
    // Seen on real tasks: the same code under either message.
    for (const message of ["File deleted", "Please update the App and try again"]) {
      const item = fromTask(task({ phase: "PHASE_TYPE_ERROR", message, errorDetail: "task_file_deleted", progress: 100 }));
      expect(item).toMatchObject({ state: "failed", reason: "保存的文件已被删除", rawError: `${message} · task_file_deleted`, fileGone: true });
    }
    // A finished task carries no failure, whatever its params say.
    expect(fromTask(task({ phase: "PHASE_TYPE_COMPLETE", errorDetail: "task_file_deleted" })).fileGone).toBe(false);
  });

  it("names states per queue", () => {
    expect(stateLabel(fromTask(task({ phase: "PHASE_TYPE_RUNNING" })))).toBe("正在传输");
    expect(stateLabel(fromUpload(upload({ state: "uploading" })))).toBe("上传中");
    expect(stateLabel(fromTask(task({ phase: "PHASE_TYPE_SOMETHING_NEW" })))).toBe("等待中");
  });

  it("an upload the app quit under needs attention, without a bar or a made-up reason", () => {
    const item = fromUpload(upload({ state: "interrupted", size: 50, done: 20, updatedAt: "2026-10-12T03:00:00Z" }));
    expect(item).toMatchObject({ state: "interrupted", progress: null, reason: "", endedAt: "2026-10-12T03:00:00Z" });
    expect(stateLabel(item)).toBe("已中断");
    expect(sections([item], "attention")).toHaveLength(1);
    expect(counts([item])).toMatchObject({ active: 0, attention: 1 });
  });

  it("a stopped upload that can continue shows what is already up; with nothing up, continuing is starting over", () => {
    expect(fromUpload(upload({ state: "interrupted", size: 50, done: 20, resumable: true }))).toMatchObject({ resumable: true, doneBytes: 20, progress: null });
    expect(fromUpload(upload({ state: "failed", size: 50, done: 0, resumable: true }))).toMatchObject({ resumable: false, doneBytes: 0 });
    // Without Resumable Transfer the bytes sent before the stop are gone: nothing to show.
    expect(fromUpload(upload({ state: "interrupted", size: 50, done: 20 }))).toMatchObject({ resumable: false, doneBytes: 0 });
  });
});

describe("downloads", () => {
  const ids = (items: { id: string }[]) => items.map((item) => item.id);

  it("a running download has a percent, a speed and its connections; it writes the local file", () => {
    expect(fromDownload(download({ state: "downloading", size: 200, done: 50, speed: 9, threads: 6 }))).toMatchObject({
      kind: "download",
      progress: 25,
      doneBytes: 50,
      speed: 9,
      threads: 6,
      source: "/Users/me/Downloads/a.mkv",
      origin: "全部文件 / 电影",
      savedTo: "~/Downloads",
    });
    // Stopped, the connections are closed whatever the record says.
    expect(fromDownload(download({ state: "paused", threads: 6 })).threads).toBe(0);
  });

  it("a paused download keeps what it has on disk, without a bar", () => {
    const paused = fromDownload(download({ state: "paused", size: 200, done: 80, resumable: true, speed: 9 }));
    expect(paused).toMatchObject({ state: "paused", resumable: true, doneBytes: 80, progress: null, speed: 0 });
    expect(stateLabel(paused)).toBe("已暂停");
    // Nothing on disk yet: continuing is starting over.
    expect(fromDownload(download({ state: "paused", done: 0, resumable: true }))).toMatchObject({ resumable: false, doneBytes: 0 });
  });

  it("says why it failed, with a word of its own when the engine gave none", () => {
    expect(fromDownload(download({ state: "failed", error: "磁盘空间不足" })).reason).toBe("磁盘空间不足");
    expect(fromDownload(download({ state: "failed" })).reason).toBe("下载失败");
    expect(fromDownload(download({ state: "cancelled", error: "x" })).reason).toBe("");
  });

  it("is Downloading while running, and waits for Sign in again when its account is signed out", () => {
    expect(stateLabel(fromDownload(download({ state: "downloading" })))).toBe("下载中");
    expect(stateLabel(fromDownload(download({ state: "paused" }), true))).toBe("需要重新登录");
    expect(stateLabel(fromDownload(download({ state: "done" }), true))).toBe("已完成");
  });

  it("batch actions: pause what moves, continue what stopped, never remove a running one", () => {
    const plan = batchPlan([
      fromDownload(download({ id: "running", state: "downloading", size: 10 })),
      fromDownload(download({ id: "waiting", state: "queued" })),
      fromDownload(download({ id: "paused", state: "paused" })),
      fromDownload(download({ id: "failed", state: "failed" })),
      fromDownload(download({ id: "done", state: "done" })),
    ]);
    expect(ids(plan.pause)).toEqual(["running", "waiting"]);
    expect(ids(plan.cancel)).toEqual(["running", "waiting", "paused"]);
    expect(ids(plan.retry)).toEqual(["paused", "failed"]);
    expect(ids(plan.remove)).toEqual(["paused", "failed", "done"]);
    expect(plan.deleteFiles).toEqual([]);
  });

  it("a paused download that kept bytes is continued, not retried", () => {
    expect(retryLabel([fromDownload(download({ state: "paused", size: 50, done: 20, resumable: true }))])).toBe("继续");
  });
});

describe("batch actions", () => {
  const ids = (items: { id: string }[]) => items.map((item) => item.id);

  it("each action takes only the picked rows it can act on", () => {
    const plan = batchPlan([
      fromUpload(upload({ id: "moving", state: "uploading", size: 10 })),
      fromUpload(upload({ id: "queued", state: "queued" })),
      fromUpload(upload({ id: "failed", state: "failed" })),
      fromUpload(upload({ id: "done", state: "done" })),
    ]);
    expect(ids(plan.cancel)).toEqual(["moving", "queued"]);
    expect(ids(plan.retry)).toEqual(["failed"]);
    // A moving upload is never removed from under itself, and uploads have no files to delete.
    expect(ids(plan.remove)).toEqual(["failed", "done"]);
    expect(plan.deleteFiles).toEqual([]);
  });

  it("cloud tasks get only what the API does: remove the record, or delete the files with it", () => {
    const plan = batchPlan([fromTask(task({ id: "a", phase: "PHASE_TYPE_RUNNING" })), fromTask(task({ id: "b", phase: "PHASE_TYPE_ERROR" }))]);
    expect(plan.cancel).toEqual([]);
    expect(plan.retry).toEqual([]);
    expect(ids(plan.remove)).toEqual(["a", "b"]);
    expect(ids(plan.deleteFiles)).toEqual(["a", "b"]);
  });

  it("a task whose file is already deleted has only its record to remove", () => {
    const gone = fromTask(task({ id: "gone", phase: "PHASE_TYPE_ERROR", message: "File deleted", errorDetail: "task_file_deleted" }));
    const plan = batchPlan([gone, fromTask(task({ id: "other", phase: "PHASE_TYPE_ERROR" }))]);
    expect(ids(plan.remove)).toEqual(["gone", "other"]);
    expect(ids(plan.deleteFiles)).toEqual(["other"]);
  });

  it("says Resume or Retry by what the stopped uploads kept", () => {
    const kept = fromUpload(upload({ id: "kept", state: "interrupted", size: 50, done: 20, resumable: true }));
    const lost = fromUpload(upload({ id: "lost", state: "failed" }));
    expect(retryLabel([kept])).toBe("继续");
    expect(retryLabel([lost])).toBe("重试");
    expect(retryLabel([kept, lost])).toBe("继续 / 重试");
  });
});

describe("several accounts", () => {
  it("tasks of two accounts with the same id stay two rows, each acted on through its own account", () => {
    const a = fromUpload(upload({ id: "u1", account: "main" }));
    const b = fromUpload(upload({ id: "u1", account: "movie" }));
    expect(a.key).not.toBe(b.key);
    expect([a.account, b.account]).toEqual(["main", "movie"]);
    expect(fromTask(task({ id: "t1", account: "main" })).key).not.toBe(fromTask(task({ id: "t1", account: "movie" })).key);
  });

  it("a signed-out account's transfers wait for Sign in again: no action reaches its server and no state is made up", () => {
    const stopped = fromUpload(upload({ id: "s", state: "interrupted", size: 50, done: 20, resumable: true }), true);
    const running = fromTask(task({ id: "r", phase: "PHASE_TYPE_RUNNING", progress: 40 }), true);
    const finished = fromTask(task({ id: "f", phase: "PHASE_TYPE_COMPLETE" }), true);
    expect(stateLabel(stopped)).toBe("需要重新登录");
    // The cloud may have moved on: no progress is claimed, and no failure.
    expect(running.progress).toBeNull();
    expect(stateLabel(running)).toBe("状态待同步");
    expect(stateLabel(finished)).toBe("已完成");

    const plan = batchPlan([stopped, running, fromUpload(upload({ id: "ok", state: "failed" }))]);
    expect(plan.retry.map((item) => item.id)).toEqual(["ok"]);
    expect([...plan.cancel, ...plan.remove, ...plan.deleteFiles].some((item) => item.blocked)).toBe(false);
    expect(plan.blocked.map((item) => item.id)).toEqual(["s", "r"]);
  });
});

describe("sections and counts", () => {
  const items = [
    fromUpload(upload({ id: "1", state: "uploading", size: 10 })),
    fromUpload(upload({ id: "2", state: "done", updatedAt: "2026-10-12T01:00:00Z" })),
    fromUpload(upload({ id: "3", state: "failed", updatedAt: "2026-10-12T02:00:00Z" })),
    fromUpload(upload({ id: "4", state: "done", updatedAt: "2026-10-12T03:00:00Z" })),
    fromUpload(upload({ id: "5", state: "queued" })),
    fromUpload(upload({ id: "6", state: "cancelled", updatedAt: "2026-10-12T00:30:00Z" })),
  ];

  it("orders sections active → attention → done; active keeps queue order, finished reads newest first", () => {
    const all = sections(items, "all");
    expect(all.map((s) => s.group)).toEqual(["active", "attention", "done"]);
    expect(all[0].items.map((i) => i.id)).toEqual(["1", "5"]);
    expect(all[1].items.map((i) => i.id)).toEqual(["3", "6"]);
    expect(all[2].items.map((i) => i.id)).toEqual(["4", "2"]);
  });

  it("a filter shows only its section, and counts match the rows each filter shows", () => {
    expect(sections(items, "done").map((s) => s.group)).toEqual(["done"]);
    expect(sections([], "all")).toEqual([]);
    const c = counts(items);
    expect(c).toEqual({ all: 6, active: 2, attention: 2, done: 2 });
    for (const f of ["active", "attention", "done"] as const) {
      expect(sections(items, f).flatMap((s) => s.items)).toHaveLength(c[f]);
    }
  });
});

describe("polling moving cloud tasks", () => {
  const list = [
    task({ id: "a", phase: "PHASE_TYPE_RUNNING", progress: 10 }),
    task({ id: "b", phase: "PHASE_TYPE_COMPLETE" }),
    task({ id: "c", phase: "PHASE_TYPE_PENDING" }),
    task({ id: "p", phase: "PHASE_TYPE_PAUSED" }),
  ];

  it("the same tasks still moving: progress is swapped in, order and the rest kept", () => {
    const merged = mergeMoving(list, [task({ id: "c", phase: "PHASE_TYPE_RUNNING", progress: 3 }), task({ id: "a", progress: 55 })]);
    expect(merged?.map((t) => [t.id, t.phase, t.progress])).toEqual([
      ["a", "PHASE_TYPE_RUNNING", 55],
      ["b", "PHASE_TYPE_COMPLETE", 0],
      ["c", "PHASE_TYPE_RUNNING", 3],
      ["p", "PHASE_TYPE_PAUSED", 0],
    ]);
  });

  it("a task that left the moving set, or joined it elsewhere, needs a full read", () => {
    // c finished: the poll no longer sees it, and only a full read knows how it ended.
    expect(mergeMoving(list, [task({ id: "a" })])).toBeNull();
    // A task added from another client.
    expect(mergeMoving(list, [task({ id: "a" }), task({ id: "c" }), task({ id: "new" })])).toBeNull();
  });
});
