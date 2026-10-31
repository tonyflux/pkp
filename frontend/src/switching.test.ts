// Multi-account switching: of quick switches only the last account reaches the screen,
// whatever order the backend's answers come back in. The services binding
// is replaced by one whose answers are computed when a call is made (as the
// Go side would) and, for the methods a test holds, delivered later.
import { describe, expect, it, vi } from "vitest";
import { watch } from "vue";
import type { Account, DriveStatus, IndexProgress, ListPage, ListRequest, QuotaInfo } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => {
  const state = {
    current: "a",
    hold: new Set<string>(),
    held: new Map<string, Array<() => void>>(),
  };
  const answers = new Map<string, (...args: unknown[]) => unknown>();
  const service = () =>
    new Proxy(
      {},
      {
        get: (_target, name: string) =>
          (...args: unknown[]) => {
            const answer = answers.get(name);
            const value = answer ? answer(...args) : null;
            if (!state.hold.has(name)) return Promise.resolve(value);
            return new Promise((resolve) => {
              state.held.set(name, [...(state.held.get(name) ?? []), () => resolve(value)]);
            });
          },
      },
    );
  return { state, answers, service };
});

vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: backend.service(),
  PlayerService: backend.service(),
  AppearanceService: backend.service(),
}));

import { canGoBack, drive, goToCrumb, loadAccounts, loadQuota, openCloudFolder, openFolder, openNav, openView, reload, switchAccount, ui } from "./store";

const ids = ["a", "b", "c"];

function statusOf(id: string): DriveStatus {
  return {
    accountId: id,
    configured: true,
    account: `${id}@example.com`,
    clientId: "",
    deviceId: id,
    userId: `user-${id}`,
    apiBaseUrl: "",
    tokenExpiry: "",
    configDir: "",
    needsPassword: false,
  };
}

function accountOf(id: string): Account {
  return {
    id,
    login: `${id}@example.com`,
    label: id.toUpperCase(),
    nickname: "",
    avatar: "",
    current: id === backend.state.current,
    needsPassword: false,
    usedAt: "",
    aiConsent: false,
    aiContentConsent: false,
  };
}

function pageOf(id: string): ListPage {
  const entry = {
    id: `${id}-file`,
    name: `${id}.mp4`,
    parentId: "",
    kind: "drive#file",
    mimeType: "video/mp4",
    size: 1,
    modifiedTime: "",
    thumbnail: "",
    folder: false,
    playable: true,
    starred: false,
    trashed: false,
    phase: "",
    category: "video",
    duration: 0,
    preview: "",
    path: "",
    deleteTime: "",
  };
  return { entries: [entry], total: 1, offset: 0, live: false, nextPageToken: "" };
}

function quotaOf(id: string): QuotaInfo {
  return { limit: 10, usage: ids.indexOf(id) + 1, usageInTrash: 0, isUnlimited: false, breakdown: null };
}

backend.answers.set("Status", () => statusOf(backend.state.current));
backend.answers.set("Accounts", () => ids.map(accountOf));
backend.answers.set("SwitchAccount", (id) => {
  backend.state.current = String(id);
  return statusOf(backend.state.current);
});
backend.answers.set("List", () => pageOf(backend.state.current));
backend.answers.set("Quota", () => quotaOf(backend.state.current));

function hold(...names: string[]) {
  for (const n of names) backend.state.hold.add(n);
}

function answerLater(...names: string[]) {
  for (const n of names) backend.state.hold.delete(n);
}

/** release delivers every held answer of a method, oldest first. */
function release(name: string) {
  const waiting = backend.state.held.get(name) ?? [];
  backend.state.held.set(name, []);
  for (const deliver of waiting) deliver();
}

// The store's awaits run a few levels deep; draining the microtask queue
// settles them without a clock.
async function settle() {
  for (let i = 0; i < 200; i++) await Promise.resolve();
}

const shownFiles = () => drive.entries.map((e) => e.id);

describe("switching accounts", () => {
  it("drops the late answers of the accounts left behind", async () => {
    backend.state.current = "a";
    drive.status = statusOf("a");
    await loadAccounts();

    // A's list and quota are still on their way…
    hold("List", "Quota");
    void reload(true);
    void loadQuota();
    // …B comes on screen and asks for its own, which are slow too…
    const toB = switchAccount("b");
    await settle();
    expect(drive.status?.accountId).toBe("b");
    // …and C, answered at once.
    answerLater("List", "Quota");
    await switchAccount("c");

    release("List");
    release("Quota");
    await settle();
    await toB;
    expect(drive.status?.accountId).toBe("c");
    expect(shownFiles()).toEqual(["c-file"]);
    expect(drive.quota?.usage).toBe(quotaOf("c").usage);
  });

  it("lands only the last of two switches in flight", async () => {
    hold("SwitchAccount");
    const toA = switchAccount("a");
    const toB = switchAccount("b");
    await settle();
    release("SwitchAccount");
    await settle();
    answerLater("SwitchAccount");
    release("SwitchAccount");

    expect(await toA).toBe(false);
    expect(await toB).toBe(true);
    expect(backend.state.current).toBe("b");
    expect(drive.status?.accountId).toBe("b");
    expect(shownFiles()).toEqual(["b-file"]);
  });

  it("does not put an account back on screen from a read a switch overtook", async () => {
    // On B, an account event's read is answered for B but arrives late.
    hold("Status", "Accounts");
    const stale = loadAccounts();
    answerLater("Status", "Accounts");
    await switchAccount("c");

    const shown: string[] = [];
    const stop = watch(
      () => drive.status?.accountId ?? "",
      (id) => shown.push(id),
      { flush: "sync" },
    );
    release("Status");
    release("Accounts");
    await stale;
    await settle();
    stop();
    expect(shown).toEqual([]);
    expect(drive.status?.accountId).toBe("c");
    expect(shownFiles()).toEqual(["c-file"]);
  });
});

describe("file navigation", () => {
  it("retains the current folder and filters when returning from another sidebar page", async () => {
    ui.view = "all";
    drive.trail = [{ id: "nested", name: "我的目录" }];
    ui.query = "episode";
    ui.category = "video";
    openNav("sync");
    await openView("all");
    expect(ui.route).toBe("files");
    expect(drive.trail).toEqual([{ id: "nested", name: "我的目录" }]);
    expect(ui.query).toBe("episode");
    expect(ui.category).toBe("video");
  });

  it("restores the all-files path after starred/trash without leaking it to a different account", async () => {
    await switchAccount("a");
    ui.view = "all";
    drive.trail = [{ id: "a-folder", name: "A" }];
    await openView("starred");
    expect(drive.trail).toEqual([]);
    await switchAccount("b");
    await openView("all");
    expect(drive.trail.some((c) => c.id === "a-folder")).toBe(false);
    await switchAccount("a");
    await openView("all");
    expect(drive.trail).toEqual([{ id: "a-folder", name: "A" }]);
    await openView("trash");
    await openView("all");
    expect(drive.trail).toEqual([{ id: "a-folder", name: "A" }]);
  });

  it.each([true, false])("opens a starred folder's unstarred children and real parent (index ready: %s)", async (ready) => {
    const folder = { ...pageOf("star").entries![0], folder: true, playable: false, starred: true, parentId: "parent", category: "folder" };
    const child = { ...pageOf("child").entries![0], parentId: folder.id };
    const trail = [{ id: "parent", name: "父目录" }, { id: folder.id, name: folder.name }];
    backend.answers.set("FolderTrail", () => trail);
    backend.answers.set("List", (value) => {
      const req = value as ListRequest;
      const entries = req.view === "starred" ? [folder] : req.parentId === folder.id ? [child] : req.parentId === "parent" ? [folder] : [];
      return { entries, total: entries.length, offset: 0, live: !ready, nextPageToken: "" };
    });
    try {
      ui.category = "all";
      ui.scope = "drive";
      drive.indexProgress = { ready } as IndexProgress;
      await openView("starred");
      await openFolder(folder);
      expect(ui.view).toBe("all");
      expect(ui.scope).toBe("folder");
      expect(drive.trail).toEqual(trail);
      expect(shownFiles()).toEqual([child.id]);
      expect(canGoBack.value).toBe(true);
      await goToCrumb(drive.trail.length - 1);
      expect(drive.trail).toEqual([trail[0]]);
      expect(shownFiles()).toEqual([folder.id]);
      await openView("starred");
      expect(drive.trail).toEqual([]);
      expect(shownFiles()).toEqual([folder.id]);
    } finally {
      drive.indexProgress = null;
      backend.answers.delete("FolderTrail");
      backend.answers.set("List", () => pageOf(backend.state.current));
    }
  });

  it("opens a sync folder in its owning account with its full breadcrumb", async () => {
    await switchAccount("a");
    const trail = [{ id: "parent-b", name: "Parent" }, { id: "sync-b", name: "Synced" }];
    const child = { ...pageOf("b").entries![0], parentId: "sync-b" };
    const previousList = backend.answers.get("List")!;
    backend.answers.set("FolderTrail", () => trail);
    backend.answers.set("List", (value) => {
      const req = value as ListRequest;
      const entries = backend.state.current === "b" && req.parentId === "sync-b" ? [child] : [];
      return { entries, total: entries.length, offset: 0, live: true, nextPageToken: "" };
    });
    try {
      ui.route = "sync";
      ui.query = "old search";
      ui.scope = "drive";
      ui.workspace = "info";
      await openCloudFolder("b", "sync-b");
      expect(drive.status?.accountId).toBe("b");
      expect(ui.route).toBe("files");
      expect(ui.workspace).toBe("files");
      expect(ui.scope).toBe("folder");
      expect(ui.query).toBe("");
      expect(drive.trail).toEqual(trail);
      expect(shownFiles()).toEqual([child.id]);
    } finally {
      backend.answers.delete("FolderTrail");
      backend.answers.set("List", previousList);
    }
  });

  it("does not install a folder breadcrumb after switching away from its account", async () => {
    await switchAccount("a");
    backend.answers.set("FolderTrail", () => [{ id: "sync-a", name: "A sync root" }]);
    hold("FolderTrail");
    try {
      const opening = openCloudFolder("a", "sync-a");
      await settle();
      await switchAccount("b");
      const trail = drive.trail.map((crumb) => ({ ...crumb }));
      release("FolderTrail");
      await opening;
      expect(drive.status?.accountId).toBe("b");
      expect(drive.trail).toEqual(trail);
      expect(shownFiles()).toEqual(["b-file"]);
    } finally {
      answerLater("FolderTrail");
      backend.answers.delete("FolderTrail");
    }
  });
});
