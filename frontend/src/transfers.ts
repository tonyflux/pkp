import { t, N_ } from "./i18n";
import { shownPath } from "./format";
// The transfer list's view of one task, whichever queue it came from. Cloud
// tasks, local uploads and downloads to this computer carry different fields
// and words for the same states; the page draws one row shape, so the mapping
// lives here and the rules are decided once: only a transfer that is really moving has a progress figure,
// a finished one never draws a full bar, and a reason is only said for a
// failure.
import type { DownloadTask, TaskSummary, UploadTask } from "../bindings/github.com/tonyflux/pkp/internal/services";

export type TransferKind = "cloud" | "upload" | "download";
/** interrupted: an upload or download the app quit under (queued or running when it closed). */
export type TransferState = "running" | "waiting" | "paused" | "failed" | "cancelled" | "interrupted" | "done";
/** The status filter and the page sections: In Progress, Needs Attention, Completed. */
export type TransferGroup = "active" | "attention" | "done";

export interface TransferItem {
  /** Unique across every queue and every account, for v-for keys and the open details. */
  key: string;
  kind: TransferKind;
  /** The account the task belongs to (Account.id): the one it is acted on through. */
  account: string;
  id: string;
  name: string;
  state: TransferState;
  /** Percent while running, null otherwise: waiting is not 0%, done is not a bar. */
  progress: number | null;
  /** Bytes moved so far: while running, and what a stopped transfer that can resume already has. */
  doneBytes: number;
  totalBytes: number;
  /** A stopped transfer that Resume picks up where it stopped, rather than starting over. */
  resumable: boolean;
  /** Bytes per second, 0 when unknown: the server reports no rate for cloud tasks. */
  speed: number;
  /** "All Files / Videos", or "" when the destination could not be named. */
  savedTo: string;
  /** When it finished, failed or was cancelled; "" while it is still active. */
  endedAt: string;
  /** Why it failed, in the page's words; "" for every other state. */
  reason: string;
  /**
   * The failure as the server or the engine put it — English, a code —
   * when that is not already the reason: for the details, never the row.
   */
  rawError: string;
  /**
   * A cloud task whose saved file was deleted after it finished (into the
   * Trash, or for good): nothing to retry and nothing left to delete with
   * it — only the record to remove.
   */
  fileGone: boolean;
  category: string;
  fileId: string;
  /** The local file an upload reads or a download writes; "" for cloud tasks. */
  source: string;
  /** Where a download comes from in the drive, "All Files / Movies"; "" for the other kinds. */
  origin: string;
  /** Connections a download has open while running; 0 otherwise. */
  threads: number;
  /**
   * Its account's session ended (Sign-in required): nothing can be done to it
   * until that account is signed back in. A cloud task's state is then the
   * last one read and may have moved on the server (Sync pending).
   */
  blocked: boolean;
}

const PHASE_STATE: Record<string, TransferState> = {
  PHASE_TYPE_RUNNING: "running",
  PHASE_TYPE_PENDING: "waiting",
  PHASE_TYPE_PAUSED: "paused",
  PHASE_TYPE_ERROR: "failed",
  PHASE_TYPE_COMPLETE: "done",
};

const UPLOAD_STATE: Record<string, TransferState> = {
  queued: "waiting",
  uploading: "running",
  failed: "failed",
  cancelled: "cancelled",
  interrupted: "interrupted",
  done: "done",
};

const DOWNLOAD_STATE: Record<string, TransferState> = {
  queued: "waiting",
  downloading: "running",
  paused: "paused",
  failed: "failed",
  cancelled: "cancelled",
  interrupted: "interrupted",
  done: "done",
};

/** One normalization for task rows, menu summaries and Assistant resource cards. */
export function transferState(kind: TransferKind, state: string): TransferState {
  const states = kind === "cloud" ? PHASE_STATE : kind === "upload" ? UPLOAD_STATE : DOWNLOAD_STATE;
  return states[state] ?? "waiting";
}

// Failures the server names by a code (a task's params.error_detail), in
// the page's words. Only codes seen on real tasks are here: the server
// sends task_file_deleted with "File deleted" or, to clients it takes for
// old, "Please update the App and try again" — the same deleted file.
const CLOUD_FAILURE: Record<string, string> = {
  task_file_deleted: N_("保存的文件已被删除"),
};

const HAN = /\p{Script=Han}/u;

/**
 * failure splits a failure into the row's reason and the original words.
 * The engines here say why in Chinese already; a server's English or a
 * library's error is kept for the details, and the row says `fallback`.
 */
function failure(text: string, fallback: string, code = ""): { reason: string; rawError: string } {
  const known = CLOUD_FAILURE[code];
  if (known) return { reason: t(known), rawError: [text, code].filter(Boolean).join(" · ") };
  if (HAN.test(text)) return { reason: text, rawError: "" };
  return { reason: fallback, rawError: text };
}

const NO_FAILURE = { reason: "", rawError: "" };

/** blocked marks a task of an account whose session ended. */
export function fromTask(task: TaskSummary, blocked = false): TransferItem {
  const state = transferState("cloud", task.phase);
  // A signed-out account's task may have moved on: no progress is claimed for it.
  const progress = state === "running" && !blocked ? Math.max(0, Math.min(100, task.progress)) : null;
  const failed = state === "failed";
  return {
    key: `cloud:${task.account}:${task.id}`,
    kind: "cloud",
    account: task.account,
    id: task.id,
    name: task.name || task.id,
    state,
    progress,
    doneBytes: progress === null ? 0 : Math.round((task.fileSize * progress) / 100),
    totalBytes: task.fileSize,
    resumable: false,
    speed: 0,
    savedTo: shownPath(task.savedTo),
    endedAt: state === "done" || failed ? task.updatedTime : "",
    // A completed task's message is the server's "Saved"; only a failure says something.
    ...(failed ? failure(task.message, t("云添加失败"), task.errorDetail) : NO_FAILURE),
    fileGone: failed && task.errorDetail === "task_file_deleted",
    category: task.category,
    fileId: task.fileId,
    source: "",
    origin: "",
    threads: 0,
    blocked,
  };
}

/** A cloud task the server is still working on: what polling waits for. Paused ones only move when asked. */
export function taskMoving(task: TaskSummary): boolean {
  return task.phase === "PHASE_TYPE_RUNNING" || task.phase === "PHASE_TYPE_PENDING";
}

/**
 * The wait before the next poll: as long as the server holds its reading
 * fresh (expires_in, 10 s on real accounts), 10 s when it does not say,
 * kept within 3 s – 5 min so a stray value neither hammers nor stalls.
 */
export function pollDelay(expiresIn: number): number {
  const seconds = expiresIn > 0 ? expiresIn : 10;
  return Math.min(300, Math.max(3, seconds)) * 1000;
}

/**
 * mergeMoving lays a fresh reading of the moving tasks over the list. When
 * the same tasks are still moving it returns the list with their progress
 * swapped in. When the set changed — one finished, failed or was added
 * elsewhere — it returns null: only a full read knows where those went.
 */
export function mergeMoving(tasks: TaskSummary[], moving: TaskSummary[]): TaskSummary[] | null {
  const fresh = new Map(moving.map((t) => [t.id, t]));
  const before = tasks.filter(taskMoving);
  if (before.length !== fresh.size || before.some((t) => !fresh.has(t.id))) return null;
  return tasks.map((t) => fresh.get(t.id) ?? t);
}

export function fromUpload(task: UploadTask, blocked = false): TransferItem {
  const state = transferState("upload", task.state);
  const running = state === "running";
  // An empty file has nothing to send; it is not stuck at 0%.
  const progress = running ? (task.size > 0 ? Math.min(100, (task.done / task.size) * 100) : 100) : null;
  // Kept with nothing sent yet, continuing is starting over: say so.
  const resumable = task.resumable && task.done > 0;
  return {
    key: `upload:${task.account}:${task.id}`,
    kind: "upload",
    account: task.account,
    id: task.id,
    name: task.name,
    state,
    progress,
    doneBytes: running || resumable ? task.done : 0,
    totalBytes: task.size,
    resumable,
    speed: running ? task.speed : 0,
    savedTo: shownPath(task.savedTo),
    endedAt: state === "done" || groupOf(state) === "attention" ? task.updatedAt : "",
    ...(state === "failed" ? failure(task.error, t("上传失败")) : NO_FAILURE),
    fileGone: false,
    category: task.category,
    fileId: task.fileId,
    source: task.path,
    origin: "",
    threads: 0,
    blocked: blocked && state !== "done",
  };
}

export function fromDownload(task: DownloadTask, blocked = false): TransferItem {
  const state = transferState("download", task.state);
  const running = state === "running";
  // An empty file has nothing to fetch; it is not stuck at 0%.
  const progress = running ? (task.size > 0 ? Math.min(100, (task.done / task.size) * 100) : 100) : null;
  // Nothing on disk yet: continuing is starting over.
  const resumable = task.resumable && task.done > 0;
  return {
    key: `download:${task.account}:${task.id}`,
    kind: "download",
    account: task.account,
    id: task.id,
    name: task.name,
    state,
    progress,
    doneBytes: running || resumable ? task.done : 0,
    totalBytes: task.size,
    resumable,
    speed: running ? task.speed : 0,
    savedTo: task.savedTo,
    endedAt: state === "done" || groupOf(state) === "attention" ? task.updatedAt : "",
    ...(state === "failed" ? failure(task.error, t("下载失败")) : NO_FAILURE),
    fileGone: false,
    category: task.category,
    fileId: task.fileId,
    source: task.path,
    origin: task.origin,
    threads: running ? task.threads : 0,
    blocked: blocked && state !== "done",
  };
}

export function groupOf(state: TransferState): TransferGroup {
  if (state === "done") return "done";
  if (state === "failed" || state === "cancelled" || state === "interrupted") return "attention";
  return "active";
}

/**
 * stateLabel is the word the row and the details use for a state. A task of
 * an account that needs signing back in says so: an upload or a download
 * waits for it (Sign-in required), and a cloud task still under way may have moved
 * on the server since it was last read (Sync pending) — neither is claimed failed.
 */
export function stateLabel(item: Pick<TransferItem, "kind" | "state"> & { blocked?: boolean }): string {
  if (item.blocked) {
    if (item.kind !== "cloud" && item.state !== "done") return t("需要重新登录");
    if (item.kind === "cloud" && item.state !== "done" && item.state !== "failed") return t("状态待同步");
  }
  switch (item.state) {
    case "running":
      return item.kind === "upload" ? t("上传中") : item.kind === "download" ? t("下载中") : t("正在传输");
    case "waiting":
      return t("等待中");
    case "paused":
      return t("已暂停");
    case "failed":
      return t("失败");
    case "cancelled":
      return t("已取消");
    case "interrupted":
      return t("已中断");
    case "done":
      return t("已完成");
  }
}

export type TransferFilter = "all" | TransferGroup;

export interface TransferSection {
  group: TransferGroup;
  items: TransferItem[];
}

const GROUP_ORDER: TransferGroup[] = ["active", "attention", "done"];

/**
 * sections splits items into the page's sections, in page order, leaving out
 * empty ones and those the filter hides. Active work keeps its queue order —
 * the transfer in flight stays at the top — while finished and failed ones
 * read newest first.
 */
export function sections(items: TransferItem[], filter: TransferFilter): TransferSection[] {
  const out: TransferSection[] = [];
  for (const group of GROUP_ORDER) {
    if (filter !== "all" && filter !== group) continue;
    const rows = items.filter((item) => groupOf(item.state) === group);
    if (!rows.length) continue;
    if (group !== "active") rows.sort((a, b) => (Date.parse(b.endedAt) || 0) - (Date.parse(a.endedAt) || 0));
    out.push({ group, items: rows });
  }
  return out;
}

/** counts tallies items per status filter, for the filter chips. */
export function counts(items: TransferItem[]): Record<TransferFilter, number> {
  const tally: Record<TransferFilter, number> = { all: items.length, active: 0, attention: 0, done: 0 };
  for (const item of items) tally[groupOf(item.state)] += 1;
  return tally;
}

/**
 * BatchPlan says which picked items each batch action applies to. A mixed
 * pick keeps every action whose list is not empty; the others are offered
 * switched off, with the reason the bar gives. Items of an account that
 * needs signing back in take part in none: nothing reaches its server.
 */
export interface BatchPlan {
  /** Downloads queued or under way: Pause, which keeps what they downloaded. */
  pause: TransferItem[];
  /** Uploads still queued or moving, and downloads queued, under way or paused: Cancel discards what they moved. */
  cancel: TransferItem[];
  /** Uploads and downloads that stopped short: Resume where they kept their progress, Retry otherwise. */
  retry: TransferItem[];
  /** Records that may leave the list: every cloud task, uploads and downloads no longer moving. */
  remove: TransferItem[];
  /** Cloud tasks whose saved files can be deleted along with the record — not those already deleted. */
  deleteFiles: TransferItem[];
  /** Picked items of a signed-out account, left out of every action. */
  blocked: TransferItem[];
}

export function batchPlan(items: TransferItem[]): BatchPlan {
  const plan: BatchPlan = { pause: [], cancel: [], retry: [], remove: [], deleteFiles: [], blocked: [] };
  for (const item of items) {
    if (item.blocked) {
      plan.blocked.push(item);
      continue;
    }
    if (item.kind === "cloud") {
      plan.remove.push(item);
      if (!item.fileGone) plan.deleteFiles.push(item);
      continue;
    }
    const group = groupOf(item.state);
    if (item.kind === "download") {
      if (item.state === "running" || item.state === "waiting") {
        plan.pause.push(item);
        plan.cancel.push(item);
      } else if (item.state === "paused") {
        plan.retry.push(item);
        plan.cancel.push(item);
        plan.remove.push(item);
      } else if (group === "attention") {
        plan.retry.push(item);
        plan.remove.push(item);
      } else {
        plan.remove.push(item);
      }
      continue;
    }
    if (group === "active") plan.cancel.push(item);
    else plan.remove.push(item);
    if (group === "attention") plan.retry.push(item);
  }
  return plan;
}

/**
 * retryLabel names Resume / Retry for a set of stopped transfers by what they kept.
 * The Simplified Chinese word is data: store compares it and builds its notice from it;
 * the bar shows it through t().
 */
export function retryLabel(items: TransferItem[]): string {
  const kept = items.filter((item) => item.resumable).length;
  if (kept === 0) return "重试"; // i18n-ignore
  return kept === items.length ? "继续" : "继续 / 重试"; // i18n-ignore
}
