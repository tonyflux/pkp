import { beforeAll, beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import type { DriveStatus, Entry, IndexProgress, UploadTask } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  listeners: new Map<string, (event: { data: unknown }) => void>(),
  rows: [] as unknown[],
  ready: true,
}));

vi.mock("@wailsio/runtime", () => ({
  Events: { On: (name: string, handler: (event: { data: unknown }) => void) => backend.listeners.set(name, handler) },
  Window: { Get: () => ({}) },
  Browser: {}, Clipboard: {}, Dialogs: {},
}));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => {
  const service = new Proxy({}, { get: (_target, name) => async () => {
    if (name === "Status") return { configured: false };
    if (name === "IndexProgress") return { account: "a", ready: backend.ready, phase: "idle" };
    if (name === "List") return { entries: [...backend.rows], total: backend.rows.length, offset: 0, live: !backend.ready, nextPageToken: "" };
    return null;
  } });
  return { DriveService: service, AppearanceService: service, MenuBarService: service, TelemetryService: service, ExternalOpenService: service };
});

import { bootstrap, drive, loadIndexProgress, reload, ui } from "./store";

const entry = (id: string) => ({ id, name: `${id}.txt`, parentId: "folder", category: "other" } as Entry);
const task = (state: string, account = "a", id = "upload") => ({ id, account, state, name: `${id}.txt`, parentId: "folder" } as UploadTask);
const emit = (name: string, data: unknown) => backend.listeners.get(name)!({ data });
const progress = (phase: string, account = "a") => emit("drive:index", { account, ready: true, phase } as IndexProgress);
const settle = async () => { for (let i = 0; i < 30; i++) await Promise.resolve(); };
const shown = () => drive.entries.map((row) => row.id);

beforeAll(async () => { await bootstrap(); });
beforeEach(async () => {
  vi.useFakeTimers();
  backend.ready = true;
  backend.rows = [entry("existing")];
  drive.status = { accountId: "a", configured: true } as DriveStatus;
  drive.uploads = [];
  drive.trail = [{ id: "folder", name: "目标文件夹" }];
  ui.route = "files";
  ui.view = "all";
  ui.scope = "folder";
  ui.query = "";
  await loadIndexProgress();
  await reload();
});
afterEach(() => { vi.clearAllTimers(); vi.useRealTimers(); });

describe("upload completion refresh", () => {
  it("shows uploaded files after the backend index sync, without leaving the folder", async () => {
    emit("upload:state", task("uploading"));
    emit("upload:state", task("done"));
    await settle();
    expect(shown()).toEqual(["existing"]);
    backend.rows.push(entry("uploaded"));
    progress("incremental");
    progress("idle");
    await settle();
    expect(shown()).toEqual(["existing", "uploaded"]);
    expect(drive.trail.map((crumb) => crumb.id)).toEqual(["folder"]);
    expect(ui.route).toBe("files");
  });

  it("refreshes even when the first event seen for a fast upload is done", async () => {
    emit("upload:state", task("done"));
    backend.rows.push(entry("fast"));
    progress("idle");
    await settle();
    expect(shown()).toEqual(["existing", "fast"]);
  });

  it("shows uploads directly from the server before the index is ready", async () => {
    backend.ready = false;
    await loadIndexProgress();
    backend.rows.push(entry("live-upload"));
    emit("upload:state", task("done"));
    await settle();
    expect(shown()).toEqual(["existing", "live-upload"]);
  });

  it("does not let failed or another account's uploads replace the current rows", async () => {
    backend.rows = [entry("unrelated")];
    emit("upload:state", task("failed"));
    emit("upload:state", task("done", "b"));
    progress("incremental", "b");
    progress("idle", "b");
    progress("idle");
    await settle();
    expect(shown()).toEqual(["existing"]);
  });

  it("does not refresh again for duplicate completion events", async () => {
    emit("upload:state", task("done"));
    backend.rows.push(entry("uploaded"));
    progress("idle");
    await settle();
    backend.rows = [entry("later-server-change")];
    emit("upload:state", task("done"));
    progress("idle");
    await settle();
    expect(shown()).toEqual(["existing", "uploaded"]);
  });
});
