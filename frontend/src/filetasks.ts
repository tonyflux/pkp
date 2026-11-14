import { computed, reactive } from "vue";
import { t } from "./i18n";

export type FileTaskPhase = "queued" | "submitting" | "processing" | "verifying" | "done" | "partial" | "failed" | "stopped";
export type FileTaskItemState = "pending" | "done" | "failed" | "unknown" | "unprocessed";
export interface FileTaskItem {
  id: string;
  name: string;
  state: FileTaskItemState;
  error: string;
  retried: boolean;
}
export interface FileTask {
  id: string;
  account: string;
  accountName: string;
  action: string;
  label: string;
  destination: string;
  phase: FileTaskPhase;
  items: FileTaskItem[];
  processed: number;
  stopping: boolean;
  error: string;
  viewed: boolean;
  createdAt: number;
}
interface FileTaskOptions {
  account: string;
  accountName: string;
  action: string;
  label: string;
  destination?: string;
  target?: string;
  lane?: "files" | "downloads";
  items: readonly { id: string; name: string; parentId?: string }[];
  run: (task: FileTask) => Promise<void>;
  stop?: () => Promise<void>;
  retry?: (ids: string[]) => void;
  errorText: (error: unknown) => string;
}
interface Job {
  task: FileTask;
  options: FileTaskOptions;
  lane: string;
  key: string;
  finished: Promise<FileTask>;
  resolve: (task: FileTask) => void;
  stopping?: Promise<void>;
}

export const fileTasks = reactive({ items: [] as FileTask[], open: false, visible: false, selectedId: "" });
const jobs = new Map<string, Job>();
const duplicates = new Map<string, Job>();
const running = new Set<string>();
const session = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
let serial = 0;

export function taskFinished(task: FileTask): boolean {
  return task.phase === "done" || task.phase === "partial" || task.phase === "failed" || task.phase === "stopped";
}

export const fileTaskActiveCount = computed(() => fileTasks.items.filter((task) => !taskFinished(task)).length);
export const fileTaskUnseen = computed(() => fileTasks.items.filter((task) => taskFinished(task) && task.phase !== "done" && !task.viewed));

export function taskCounts(task: FileTask) {
  const counts = { done: 0, failed: 0, unknown: 0, unprocessed: 0, total: task.items.length };
  for (const item of task.items) if (item.state !== "pending") counts[item.state] += 1;
  return counts;
}

/** Capture one confirmed submission; identical active requests share its receipt. */
export function enqueueFileTask(options: FileTaskOptions): { task: FileTask; finished: Promise<FileTask>; created: boolean } {
  const items = [...new Map(options.items.map((item) => [item.id, item])).values()];
  const lane = JSON.stringify([options.account, options.lane ?? "files"]);
  // The confirmed snapshot is part of the identity: the same ids renamed or
  // moved between submissions are a different request, not one to join.
  const key = JSON.stringify([lane, options.action, options.target ?? "", items.map((item) => `${item.id}\u0000${item.name}\u0000${item.parentId ?? ""}`).sort()]);
  const duplicate = duplicates.get(key);
  if (duplicate) return { task: duplicate.task, finished: duplicate.finished, created: false };
  const task = reactive<FileTask>({
    id: `files-${session}-${++serial}`,
    account: options.account,
    accountName: options.accountName,
    action: options.action,
    label: options.label,
    destination: options.destination ?? "",
    phase: "queued",
    items: items.map((item) => ({ id: item.id, name: item.name, state: "pending", error: "", retried: false })),
    processed: 0,
    stopping: false,
    error: "",
    viewed: false,
    createdAt: Date.now(),
  });
  let resolve!: (task: FileTask) => void;
  // TypeScript 4.9 has no Promise.withResolvers declaration.
  const finished = new Promise<FileTask>((done) => { resolve = done; });
  const job: Job = { task, options, lane, key, finished, resolve };
  jobs.set(task.id, job);
  duplicates.set(key, job);
  fileTasks.items.push(task);
  fileTasks.visible = true;
  fileTasks.selectedId = task.id;
  void drain(lane);
  return { task, finished, created: true };
}

async function drain(lane: string) {
  if (running.has(lane)) return;
  running.add(lane);
  try {
    for (;;) {
      const task = fileTasks.items.find((item) => item.phase === "queued" && jobs.get(item.id)?.lane === lane);
      if (!task) break;
      const job = jobs.get(task.id)!;
      task.phase = "submitting";
      try {
        await job.options.run(task);
      } catch (error) {
        task.error = job.options.errorText(error);
        // A lost reply is not evidence that a write was refused. Never replay it.
        for (const item of task.items) if (item.state === "pending") {
          item.state = "unknown";
          item.error = task.error;
        }
      }
      // A delayed StopRemove must not cancel the following account task.
      await job.stopping;
      for (const item of task.items) if (item.state === "pending") {
        item.state = task.stopping ? "unprocessed" : "unknown";
        item.error = task.stopping ? t("任务已停止，尚未提交") : t("未能确认操作结果");
      }
      const counts = taskCounts(task);
      task.processed = counts.done + counts.failed + counts.unknown;
      task.phase = counts.unprocessed ? "stopped" : counts.done === counts.total ? "done" : counts.done || counts.unknown ? "partial" : "failed";
      task.stopping = false;
      finish(job);
    }
  } finally {
    running.delete(lane);
  }
}

function finish(job: Job) {
  duplicates.delete(job.key);
  if (!fileTasks.open && fileTasks.selectedId === job.task.id) {
    const active = [...fileTasks.items].reverse().find((task) => !taskFinished(task));
    const unseen = fileTaskUnseen.value.at(-1);
    if (active || unseen) fileTasks.selectedId = (active ?? unseen)!.id;
  }
  job.resolve(job.task);
}

export function settleFileTask(task: FileTask, results: readonly { id: string; state: FileTaskItemState; error?: string }[]) {
  const byId = new Map(results.map((item) => [item.id, item]));
  for (const item of task.items) {
    const result = byId.get(item.id);
    if (result) {
      item.state = result.state;
      item.error = result.error ?? "";
    }
  }
  const counts = taskCounts(task);
  task.processed = counts.done + counts.failed + counts.unknown;
}

export async function stopFileTask(id: string): Promise<void> {
  const job = jobs.get(id);
  if (!job || taskFinished(job.task)) return;
  const task = job.task;
  if (task.phase === "queued") {
    for (const item of task.items) {
      item.state = "unprocessed";
      item.error = t("任务已停止，尚未提交");
    }
    task.phase = "stopped";
    finish(job);
    return;
  }
  if (task.stopping) return job.stopping;
  task.stopping = true;
  job.stopping = Promise.resolve().then(() => job.options.stop?.()).catch((error) => {
    task.error = job.options.errorText(error);
    task.stopping = false;
  });
  await job.stopping;
}

/** Only a known refusal is retryable; ambiguous submissions require inspection. */
export function retryFileTask(id: string, itemIds?: string[]): void {
  const job = jobs.get(id);
  if (!job?.options.retry || !taskFinished(job.task)) return;
  const requested = itemIds ? new Set(itemIds) : null;
  const failed = job.task.items.filter((item) => item.state === "failed" && !item.retried && (!requested || requested.has(item.id)));
  for (const item of failed) item.retried = true;
  if (failed.length) job.options.retry(failed.map((item) => item.id));
}

export function dismissFileTask(id: string): void {
  const job = jobs.get(id);
  if (!job || !taskFinished(job.task)) return;
  jobs.delete(id);
  fileTasks.items = fileTasks.items.filter((item) => item.id !== id);
  if (fileTasks.selectedId === id) fileTasks.selectedId = fileTasks.items.at(-1)?.id ?? "";
  if (!fileTasks.items.length) {
    fileTasks.open = false;
    fileTasks.visible = false;
  }
}
