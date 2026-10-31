import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Account, DriveStatus, Entry, ViewerReturnRequest } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => {
  const answers = new Map<string, (...args: unknown[]) => unknown>();
  const acknowledgements: { id: unknown; accepted: unknown; account: string; file: string; route: string }[] = [];
  const state = { account: "a" };
  const service = new Proxy({}, { get: (_target, name: string) => (...args: unknown[]) => Promise.resolve(answers.get(name)?.(...args) ?? null) });
  return { answers, acknowledgements, state, service };
});
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: backend.service, PlayerService: backend.service, AppearanceService: backend.service,
  AppLockService: backend.service, MenuBarService: backend.service, NotifyService: backend.service, TelemetryService: backend.service,
}));

import { accounts, cloudAdd, closeViewer, drive, openViewer, player, popOutViewer, restoreViewer, ui, viewer } from "./store";

const status = (id: string): DriveStatus => ({
  accountId: id, configured: true, account: `${id}@example.com`, clientId: "", deviceId: id, userId: id,
  apiBaseUrl: "", tokenExpiry: "", configDir: "", needsPassword: false,
});
const account = (id: string): Account => ({
  id, login: `${id}@example.com`, label: id, nickname: "", avatar: "", current: backend.state.account === id,
  needsPassword: false, usedAt: "", aiConsent: false, aiContentConsent: false,
});
const image = (id: string): Entry => ({
  id, name: `${id}.png`, parentId: "folder", preview: "image", category: "image", folder: false,
  size: 10, modifiedTime: "2026-10-21",
} as Entry);
const request = (id = 21): ViewerReturnRequest => ({
  account: "a", state: { windowId: id, entry: image("stepped"), siblings: [image("original"), image("stepped")], source: "original folder", sort: "name", asc: true, comic: false },
});

beforeEach(() => {
  closeViewer();
  backend.answers.clear();
  backend.acknowledgements.length = 0;
  backend.state.account = "a";
  accounts.switching = "";
  accounts.dialog = null;
  cloudAdd.draft = "";
  ui.route = "files";
  drive.status = status("a");
  drive.entries = [];
  player.presentation = "off";
  backend.answers.set("Status", () => status(backend.state.account));
  backend.answers.set("Accounts", () => [account("a"), account("b")]);
  backend.answers.set("SwitchAccount", (id) => { backend.state.account = String(id); return status(String(id)); });
  backend.answers.set("List", () => ({ entries: [], total: 0, offset: 0, live: false, nextPageToken: "" }));
  backend.answers.set("ViewerList", () => ({ entries: request().state.siblings, total: 2, offset: 0, live: false, nextPageToken: "" }));
  backend.answers.set("Quota", () => ({ limit: 100, usage: 10, usageInTrash: 0, isUnlimited: false, breakdown: null }));
  backend.answers.set("PreviewFile", (id) => `http://preview/${id}`);
  backend.answers.set("FinishViewerReturn", (id, accepted) => {
    backend.acknowledgements.push({ id, accepted, account: drive.status?.accountId ?? "", file: viewer.entry?.id ?? "", route: ui.route });
  });
});

describe("detached document handoff", () => {
  it("leaves the original browsing page only after the detached window opens, and keeps it on failure", async () => {
    let finish!: () => void;
    backend.answers.set("OpenViewerWindow", () => new Promise<void>((resolve) => { finish = resolve; }));
    await openViewer(image("original"));
    const opening = popOutViewer();
    expect(viewer.entry?.id).toBe("original");
    expect(viewer.detaching).toBe(true);
    finish();
    await opening;
    expect(viewer.entry).toBeNull();
    expect(ui.route).toBe("files");
    expect(viewer.detaching).toBe(false);
    await openViewer(image("original"));
    backend.answers.set("OpenViewerWindow", () => Promise.reject(new Error("window failed")));
    await popOutViewer();
    expect(viewer.entry?.id).toBe("original");
    expect(ui.route).toBe("viewer");
  });

  it("does not close a new document when an earlier popout reply arrives late", async () => {
    let finish!: () => void;
    backend.answers.set("OpenViewerWindow", () => new Promise<void>((resolve) => { finish = resolve; }));
    await openViewer(image("original"));
    const opening = popOutViewer();
    await openViewer(image("newer"));
    finish();
    await opening;
    expect(viewer.entry?.id).toBe("newer");
    expect(ui.route).toBe("viewer");
  });

  it("returns the file selected in the detached window even after the main page changes", async () => {
    ui.route = "settings";
    await restoreViewer(request());
    expect(viewer.entry?.id).toBe("stepped");
    expect(viewer.source).toBe("original folder");
    expect(ui.route).toBe("viewer");
    expect(backend.acknowledgements).toEqual([{ id: 21, accepted: true, account: "a", file: "stepped", route: "viewer" }]);
    closeViewer();
    expect(ui.route).toBe("settings");
  });

  it("switches back to the opening account before accepting its document", async () => {
    backend.state.account = "b";
    drive.status = status("b");
    ui.route = "transfers";
    await restoreViewer(request(22));
    expect(drive.status?.accountId).toBe("a");
    expect(viewer.entry?.id).toBe("stepped");
    expect(backend.acknowledgements).toEqual([{ id: 22, accepted: true, account: "a", file: "stepped", route: "viewer" }]);
  });

  it("keeps the detached window when the user cancels the account switch", async () => {
    backend.state.account = "b";
    drive.status = status("b");
    cloudAdd.draft = "unsubmitted link";
    const returning = restoreViewer(request(23));
    await vi.waitFor(() => expect(accounts.dialog?.kind).toBe("leave"));
    const dialog = accounts.dialog;
    if (dialog?.kind !== "leave") throw new Error("account-switch question was not shown");
    dialog.resolve(false);
    await returning;
    expect(drive.status?.accountId).toBe("b");
    expect(viewer.entry).toBeNull();
    expect(cloudAdd.draft).toBe("unsubmitted link");
    expect(backend.acknowledgements).toEqual([{ id: 23, accepted: false, account: "b", file: "", route: "files" }]);
  });

  it("does not accept the file when switching to its account fails", async () => {
    backend.state.account = "b";
    drive.status = status("b");
    backend.answers.set("SwitchAccount", () => Promise.reject(new Error("account unavailable")));
    await restoreViewer(request(24));
    expect(drive.status?.accountId).toBe("b");
    expect(viewer.entry).toBeNull();
    expect(backend.acknowledgements).toEqual([{ id: 24, accepted: false, account: "b", file: "", route: "files" }]);
  });
});
