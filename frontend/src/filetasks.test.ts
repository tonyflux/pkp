import { createApp, h, nextTick, type App } from "vue";
import { afterEach, describe, expect, it, vi } from "vitest";
import FileTasks from "./components/FileTasks.vue";
import { dismissFileTask, enqueueFileTask, fileTaskUnseen, fileTasks, retryFileTask, settleFileTask, stopFileTask, taskCounts, type FileTask } from "./filetasks";

function gate() {
  let resolve!: () => void;
  // TypeScript 4.9 has no Promise.withResolvers declaration.
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}
const tick = async () => { for (let n = 0; n < 8; n++) await Promise.resolve(); };
const options = (id: string, run: (task: FileTask) => Promise<void>, account = "a") => ({
  account, accountName: account, action: "trash", label: "Move to Trash", items: [{ id, name: id }], run,
  errorText: (error: unknown) => String(error),
});
const done = (task: FileTask) => settleFileTask(task, task.items.map((item) => ({ id: item.id, state: "done" })));

let app: App | undefined;
async function mountTasks() {
  const host = document.createElement("div");
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  document.body.append(host);
  app = createApp({ render: () => h(FileTasks) });
  app.mount(host);
  await nextTick();
  return host;
}
async function click(host: HTMLElement, selector: string) {
  const button = host.querySelector<HTMLButtonElement>(selector);
  expect(button).not.toBeNull();
  button!.click();
  await nextTick();
}

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.replaceChildren();
  for (const task of [...fileTasks.items]) dismissFileTask(task.id);
  vi.clearAllTimers();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("session file task queue", () => {
  it("serializes one account's mutations without blocking downloads or another account", async () => {
    const hold = gate();
    const order: string[] = [];
    const first = enqueueFileTask(options("first", async (task) => { order.push("first"); await hold.promise; done(task); }));
    const next = enqueueFileTask(options("next", async (task) => { order.push("next"); done(task); }));
    const other = enqueueFileTask(options("other", async (task) => { order.push("other"); done(task); }, "b"));
    const download = enqueueFileTask({ ...options("download", async (task) => { order.push("download"); done(task); }), lane: "downloads" });
    await Promise.all([other.finished, download.finished]);
    expect(next.task.phase).toBe("queued");
    expect(order).toEqual(["first", "other", "download"]);
    hold.resolve();
    await Promise.all([first.finished, next.finished]);
    expect(order).toEqual(["first", "other", "download", "next"]);
    expect(next.task.phase).toBe("done");
  });

  it("deduplicates an active snapshot but permits a new deliberate submission after completion", async () => {
    const hold = gate();
    let submissions = 0;
    const run = async (task: FileTask) => { submissions++; await hold.promise; done(task); };
    const first = enqueueFileTask(options("same", run));
    const duplicate = enqueueFileTask(options("same", run));
    expect(duplicate.created).toBe(false);
    expect(duplicate.task).toBe(first.task);
    hold.resolve();
    await duplicate.finished;
    const later = enqueueFileTask(options("same", run));
    await later.finished;
    expect(submissions).toBe(2);
    expect(later.task.id).not.toBe(first.task.id);
  });

  it("does not merge a request whose confirmed snapshot changed", async () => {
    const hold = gate();
    const submissions: string[] = [];
    const run = async (task: FileTask) => { submissions.push(task.items[0].name); await hold.promise; done(task); };
    const before = enqueueFileTask(options("snap", run));
    const renamed = enqueueFileTask({ ...options("snap", run), items: [{ id: "snap", name: "renamed" }] });
    expect(renamed.created).toBe(true);
    expect(renamed.task).not.toBe(before.task);
    hold.resolve();
    await Promise.all([before.finished, renamed.finished]);
    expect(submissions).toEqual(["snap", "renamed"]);
  });

  it("cancels a queued job without submitting it", async () => {
    const hold = gate();
    const first = enqueueFileTask(options("active", async (task) => { await hold.promise; done(task); }));
    let submitted = false;
    const queued = enqueueFileTask(options("unsent", async (task) => { submitted = true; done(task); }));
    await stopFileTask(queued.task.id);
    expect(taskCounts(await queued.finished)).toMatchObject({ done: 0, unprocessed: 1 });
    expect(queued.task.phase).toBe("stopped");
    hold.resolve();
    await first.finished;
    expect(submitted).toBe(false);
  });

  it("settles the in-flight item and drains a delayed stop before starting the next job", async () => {
    const accepted = gate();
    const stopReply = gate();
    const events: string[] = [];
    const first = enqueueFileTask({
      ...options("sent", async (task) => {
        await accepted.promise;
        settleFileTask(task, [{ id: "sent", state: "done" }]);
        if (!task.stopping) settleFileTask(task, [{ id: "not-sent", state: "done" }]);
      }),
      items: [{ id: "sent", name: "sent" }, { id: "not-sent", name: "not-sent" }],
      stop: async () => { await stopReply.promise; events.push("stop-settled"); },
    });
    const next = enqueueFileTask(options("next", async (task) => { events.push("next-started"); done(task); }));
    const stopping = stopFileTask(first.task.id);
    accepted.resolve();
    await tick();
    expect(next.task.phase).toBe("queued");
    expect(events).toEqual([]);
    stopReply.resolve();
    await Promise.all([stopping, first.finished, next.finished]);
    expect(taskCounts(first.task)).toMatchObject({ done: 1, unprocessed: 1 });
    expect(events).toEqual(["stop-settled", "next-started"]);
  });

  it("never offers an ambiguous write for replay and preserves known per-item results on interruption", async () => {
    const retried: string[][] = [];
    const submission = enqueueFileTask({
      ...options("confirmed", async (task) => {
        settleFileTask(task, [{ id: "confirmed", state: "done" }, { id: "refused", state: "failed", error: "permission denied" }]);
        throw new Error("reply lost");
      }),
      items: ["confirmed", "refused", "uncertain"].map((id) => ({ id, name: id })),
      retry: (ids) => { retried.push(ids); },
    });
    const task = await submission.finished;
    expect(task.items.map((item) => item.state)).toEqual(["done", "failed", "unknown"]);
    expect(task.phase).toBe("partial");
    retryFileTask(task.id, ["confirmed", "refused", "uncertain"]);
    expect(retried).toEqual([["refused"]]);
    retryFileTask(task.id, ["uncertain"]);
    expect(retried).toEqual([["refused"]]);
    retryFileTask(task.id, ["refused"]);
    expect(retried).toEqual([["refused"]]);
    expect(task.items.find((item) => item.id === "refused")?.retried).toBe(true);
  });
});

describe("file task receipt visibility", () => {
  it("hides a completed strip without deleting the receipt, and keeps a reopened receipt until closed", async () => {
    vi.useFakeTimers();
    const host = await mountTasks();
    const hold = gate();
    const submission = enqueueFileTask(options("done", async (task) => { await hold.promise; done(task); }));
    await nextTick();
    expect(host.querySelector<HTMLElement>("[data-file-tasks]")!.style.display).not.toBe("none");
    hold.resolve();
    await submission.finished;
    await nextTick();
    await vi.advanceTimersByTimeAsync(5000);
    expect(host.querySelector<HTMLElement>("[data-file-tasks]")!.style.display).toBe("none");
    expect(fileTasks.items).toContain(submission.task);

    fileTasks.visible = true;
    fileTasks.open = true;
    await nextTick();
    await vi.advanceTimersByTimeAsync(10000);
    expect(host.querySelector<HTMLElement>("[data-file-tasks]")!.style.display).not.toBe("none");
    await click(host, "[data-file-task-hide]");
    expect(fileTasks.items).toContain(submission.task);
    fileTasks.visible = true;
    fileTasks.open = true;
    await nextTick();
    await click(host, "[data-file-task-dismiss]");
    expect(fileTasks.items).not.toContain(submission.task);
  });

  it("retains unseen failed and stopped receipts while another task runs and completes", async () => {
    vi.useFakeTimers();
    const host = await mountTasks();
    const hold = gate();
    const first = enqueueFileTask(options("first", async (task) => { await hold.promise; done(task); }));
    const stopped = enqueueFileTask(options("queued", async (task) => done(task)));
    await stopFileTask(stopped.task.id);
    const failed = enqueueFileTask(options("failed", async (task) => settleFileTask(task, [{ id: "failed", state: "failed", error: "denied" }]), "b"));
    await failed.finished;
    await nextTick();
    expect(fileTaskUnseen.value.map((task) => task.id)).toEqual([stopped.task.id, failed.task.id]);
    expect(fileTasks.selectedId).toBe(first.task.id);
    await vi.advanceTimersByTimeAsync(6000);
    expect(fileTasks.visible).toBe(true);
    hold.resolve();
    await first.finished;
    await nextTick();
    expect(fileTasks.selectedId).toBe(failed.task.id);
    await vi.advanceTimersByTimeAsync(6000);
    expect(fileTasks.visible).toBe(true);

    await click(host, "[data-file-task-toggle]");
    expect(fileTaskUnseen.value.map((task) => task.id)).toEqual([stopped.task.id]);
    expect(host.querySelector(`[data-task-id="${stopped.task.id}"]`)?.textContent).toContain("待查看");
    await click(host, `[data-file-task-history][data-task-id="${stopped.task.id}"]`);
    expect(fileTaskUnseen.value).toEqual([]);
    await click(host, "[data-file-task-hide]");
    expect(fileTasks.items.map((task) => task.id)).toEqual([first.task.id, stopped.task.id, failed.task.id]);
  });
});
