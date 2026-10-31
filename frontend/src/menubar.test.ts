import { describe, expect, it } from "vitest";
import { countLine, inboxCard, panelCard } from "./menubar";
import type { Arrival, DownloadTask, MenuBarState, TaskSummary, UploadTask } from "../bindings/github.com/tonyflux/pkp/internal/services";

const GB = 1024 ** 3;
const names: Record<string, string> = { main: "影视收藏" };
const name = (id: string) => names[id] ?? "原账号";

const state = (p: Partial<MenuBarState>): MenuBarState =>
  ({ windowOpen: false, account: "main", running: 0, attention: 0, inbox: 0, inboxItem: null, cloud: "ok", item: { kind: "", upload: null, download: null, task: null, blocked: false }, ...p }) as MenuBarState;

const arrival = (p: Partial<Arrival>): Arrival =>
  ({ id: "task-1", account: "main", name: "[字幕组] 山海纪行 S01", fileId: "f9", parentId: "", savedTo: "云添加", source: "cloud", completedAt: "", state: "new", recordId: "", ...p }) as Arrival;

const download = (p: Partial<DownloadTask>): DownloadTask =>
  ({ id: "d1", fileId: "f1", name: "海岸纪行 · 第01集.mp4", path: "", origin: "", size: 4 * GB, state: "downloading", done: 2.7 * GB, speed: 0, threads: 8, error: "", resumable: false, updatedAt: "", category: "video", account: "main", savedTo: "~/Downloads", ...p }) as DownloadTask;

const upload = (p: Partial<UploadTask>): UploadTask =>
  ({ id: "u1", name: "a.bin", path: "", parentId: "", dir: "", size: 10, state: "interrupted", done: 0, speed: 0, error: "", fileId: "", resumable: false, savedTo: "", updatedAt: "", category: "other", account: "main", ...p }) as UploadTask;

const task = (p: Partial<TaskSummary>): TaskSummary =>
  ({ id: "t", name: "海岸影像.zip", phase: "PHASE_TYPE_PENDING", progress: 0, fileId: "", fileSize: 0, message: "", errorDetail: "", category: "archive", createdTime: "", updatedTime: "", savedTo: "", account: "main", ...p }) as TaskSummary;

describe("menu bar panel", () => {
  it("a moving download shows its real percentage, amount and owner", () => {
    const card = panelCard(state({ running: 1, item: { kind: "download", download: download({}), upload: null, task: null, blocked: false } }), name);
    expect(card).toMatchObject({
      tone: "running",
      title: "下载中 · 海岸纪行 · 第01集.mp4",
      detail: "影视收藏 · 下载到电脑",
      amount: "2.7 GB / 4.0 GB",
      note: "关闭窗口后仍在继续",
      key: "download:main:d1",
      source: "download",
    });
    expect(card.progress).toBeCloseTo(67.5);
  });

  it("waiting and paused say so in words, with no percentage", () => {
    for (const s of ["queued", "paused"]) {
      const card = panelCard(state({ running: 1, item: { kind: "download", download: download({ state: s }), upload: null, task: null, blocked: false } }), name);
      expect(card.tone).toBe("waiting");
      expect(card.progress).toBeNull();
      expect(card.amount).toBe("共 4.0 GB");
    }
    const parsing = panelCard(state({ running: 1, item: { kind: "cloud", task: task({}), upload: null, download: null, blocked: false } }), name);
    expect(parsing).toMatchObject({ tone: "waiting", title: "等待中 · 海岸影像.zip", progress: null, amount: "" });
  });

  it("a transfer of an account whose login ended asks for signing in, not a failure", () => {
    const card = panelCard(state({ attention: 2, item: { kind: "upload", upload: upload({}), download: null, task: null, blocked: true } }), name);
    expect(card).toMatchObject({ tone: "attention", title: "2 项传输需要处理", detail: "影视收藏 · 登录已失效，请在应用内恢复" });
  });

  it("a card about something moving still says what needs the user", () => {
    const card = panelCard(state({ running: 1, attention: 1, item: { kind: "download", download: download({}), upload: null, task: null, blocked: false } }), name);
    expect(card.extra).toBe("另有 1 项传输需要处理");
  });

  it("an unread or unreadable cloud is never idle or zero", () => {
    expect(panelCard(state({ cloud: "unknown" }), name).tone).toBe("unknown");
    expect(panelCard(state({ cloud: "" }), name).tone).toBe("reading");
    expect(panelCard(state({}), name).title).toBe("暂无进行中的传输");
    expect(countLine(state({ cloud: "unknown" }))).toBe("全部账号 · 状态待同步");
    expect(countLine(state({ running: 1, attention: 1, cloud: "unknown" }))).toBe("全部账号 · 1 项进行中 · 1 项需处理 · 云端待同步");
    expect(countLine(state({}))).toBe("全部账号 · 暂无活动任务");
  });

  it("an arrival gets its own card that opens the assistant on it", () => {
    const card = inboxCard(state({ inbox: 1, inboxItem: arrival({}) }), name);
    expect(card).toEqual({ title: "「[字幕组] 山海纪行 S01」已存入云盘", detail: "待整理 · 来自云添加", extra: "", key: "task-1" });
  });

  it("names the owner when several accounts are here and counts the others", () => {
    const card = inboxCard(state({ inbox: 3, inboxItem: arrival({}) }), name, true);
    expect(card).toMatchObject({ detail: "影视收藏 · 待整理 · 来自云添加", extra: "另有 2 项新入库待整理" });
  });

  it("no arrival, no inbox card, and the transfer card is unchanged by one", () => {
    expect(inboxCard(state({}), name)).toBeNull();
    const withArrival = state({ inbox: 1, inboxItem: arrival({}) });
    expect(panelCard(withArrival, name).title).toBe("暂无进行中的传输");
    expect(countLine(withArrival)).toBe("全部账号 · 暂无活动任务");
  });
});
