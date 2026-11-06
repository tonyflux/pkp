import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, defineComponent, h, nextTick, reactive, ref, type App } from "vue";
import type { FolderSearch } from "../bindings/github.com/tonyflux/pkp/internal/services/models";

const bridge = vi.hoisted(() => ({ Status: vi.fn(), Interfaces: vi.fn(), CloudFolders: vi.fn(), SearchFolders: vi.fn(), AddSpace: vi.fn(), UpdateSpace: vi.fn(), Stop: vi.fn(), SetPassword: vi.fn(), SyncFolderTrail: vi.fn(), openSyncCreate: vi.fn(), openNav: vi.fn() }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services/lanservice", () => bridge);
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services/driveservice", () => bridge);
vi.mock("./syncstore", () => ({
  sync: reactive({ pairs: [], loading: false, error: "", scope: "current", query: "", selected: "", detail: false, tab: "activity", draftOpen: false }),
  syncDialog: reactive({ kind: "" }), syncStateName: () => "已同步", openSyncCreate: bridge.openSyncCreate, loadSync: vi.fn(),
}));
vi.mock("./store", () => {
  return {
    accounts: reactive({ list: [{ id: "a", login: "a@example.test" }, { id: "b", login: "b@example.test" }], dialog: null }),
    accountLabel: (account: { id: string } | null) => account?.id ?? "",
    appLocked: ref(false),
    trailPath: (trail: { name: string }[]) => ["全部文件", ...trail.map(item => item.name)].join(" / "),
    openNav: bridge.openNav,
    syncDraft: reactive({ busy: false }),
  };
});
vi.mock("./components/SelectMenu.vue", () => {
  return { default: defineComponent({
    props: ["modelValue", "options", "label", "disabled"], emits: ["update:modelValue"],
    setup: (props, { emit }) => () => h("select", {
      "aria-label": props.label, value: props.modelValue, disabled: props.disabled,
      onChange: (event: Event) => emit("update:modelValue", (event.target as HTMLSelectElement).value),
    }, props.options.map((item: { value: string; label: string }) => h("option", { value: item.value }, item.label))),
  }) };
});
import LANView from "./components/LANView.vue";
import { sync, type SyncPair } from "./syncstore";
import { accounts } from "./store";
import { lan, type LANSpace, type LANStatus } from "./lanstore";

const initialAccounts = accounts.list;
let app: App | undefined;
async function settle() { for (let i = 0; i < 5; i++) { await Promise.resolve(); await nextTick(); } }
async function click(selector: string) { document.querySelector<HTMLButtonElement>(selector)!.click(); await settle(); }
async function select(label: string, value: string) {
  const field = document.querySelector<HTMLSelectElement>(`select[aria-label="${label}"]`)!;
  field.value = value; field.dispatchEvent(new Event("change", { bubbles: true })); await settle();
}
async function search(value: string) {
  const field = document.querySelector<HTMLInputElement>("[data-lan-folder-search]")!;
  field.value = value; field.dispatchEvent(new Event("input", { bubbles: true }));
  await vi.advanceTimersByTimeAsync(200); await settle();
}
function deferred<T = FolderSearch>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
const result = (id: string): FolderSearch => ({ ready: true, folders: [{ id, name: "Movies", subfolders: 0, trail: [{ id: "parent", name: "Archive" }, { id, name: "Movies" }] }] });
async function openPicker() {
  const host = document.createElement("div"); document.body.append(host);
  app = createApp(LANView); app.mount(host); await settle();
  await click("[data-lan-create]"); await select("来源类型", "cloud"); await select("固定来源账号", "a");
}
beforeEach(() => {
  // jsdom has no layout or scrolling implementation.
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
  vi.useFakeTimers(); vi.clearAllMocks();
  for (const key of Object.keys(localStorage)) if (key.startsWith("pkp.lan.confirmed.")) localStorage.removeItem(key);
  lan.status = null; lan.busy = false; lan.error = "";
  accounts.list = initialAccounts;
  sync.pairs = []; sync.scope = "current"; sync.query = ""; sync.selected = ""; sync.detail = false;
  bridge.SyncFolderTrail.mockResolvedValue([{ id: "parent", name: "Archive" }, { id: "root", name: "Movies" }]);
  bridge.Status.mockResolvedValue({ running: false, address: "", interfaceId: "", hasPassword: false, spaces: [], tasks: [], cacheBytes: 0, cacheLimit: 1024, originalDir: "", error: "" });
  bridge.Interfaces.mockResolvedValue([]);
  bridge.CloudFolders.mockResolvedValue([{ id: "existing", name: "Existing" }]);
  bridge.AddSpace.mockResolvedValue({ id: "created" });
});
afterEach(() => { app?.unmount(); app = undefined; document.body.replaceChildren(); Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView"); vi.clearAllTimers(); vi.useRealTimers(); });

describe("LAN folder search authorization", () => {
  it("ignores an older query and requires directory selection plus scope acknowledgement before sharing", async () => {
    const older = deferred();
    bridge.SearchFolders.mockImplementation((_account: string, query: string) => query === "old" ? older.promise : Promise.resolve(result("current")));
    await openPicker(); await search("old"); await search("Movies");
    older.resolve(result("outdated")); await settle();
    expect(document.querySelector('[data-lan-folder-result="outdated"]')).toBeNull();
    expect(document.querySelector('[data-lan-folder-result="current"]')?.textContent).toContain("Archive / Movies");
    expect(document.querySelector<HTMLButtonElement>("[data-lan-choose-cloud]")!.disabled).toBe(true);
    expect(bridge.AddSpace).not.toHaveBeenCalled();
    await click('[data-lan-folder-result="current"]');
    expect(bridge.CloudFolders).toHaveBeenLastCalledWith("a", "current");
    expect(document.querySelector("[data-lan-chosen-directory]")).toBeNull();
    await click("[data-lan-choose-cloud]");
    expect(document.querySelector<HTMLButtonElement>("[data-lan-confirm]")!.disabled).toBe(true);
    await click("[data-lan-ack]"); await click("[data-lan-confirm]");
    expect(bridge.AddSpace).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ accountId: "a", rootId: "current", kind: "cloud" }));
  });

  it("discards results from the previous account and restores browsing when a pending search is cleared", async () => {
    const previousAccount = deferred(), cleared = deferred();
    bridge.SearchFolders.mockImplementation((account: string) => account === "a" ? previousAccount.promise : cleared.promise);
    await openPicker(); await search("Movies");
    await select("固定来源账号", "b");
    previousAccount.resolve(result("account-a-only")); await settle();
    expect(document.querySelector<HTMLInputElement>("[data-lan-folder-search]")!.value).toBe("");
    expect(document.querySelector("[data-lan-folder-result]")).toBeNull();
    await click('[data-lan-folder="existing"]'); await search("Movies");
    expect(bridge.SearchFolders).toHaveBeenLastCalledWith("b", "Movies");
    await click('[aria-label="清除搜索"]');
    cleared.resolve(result("cleared-result")); await settle();
    expect(document.querySelector("[data-lan-folder-result]")).toBeNull();
    expect(document.querySelector(".folder-current strong")?.textContent).toBe("Existing");
    expect(document.querySelector("[data-lan-chosen-directory]")).toBeNull();
    expect(bridge.AddSpace).not.toHaveBeenCalled();
  });
});

describe("LAN one-time confirmations", () => {
  let status: LANStatus;
  async function mountOwner() {
    const host = document.createElement("div"); document.body.append(host);
    app = createApp(LANView); app.mount(host); await settle();
  }
  async function input(selector: string, value: string) {
    const field = document.querySelector<HTMLInputElement>(selector)!;
    field.value = value; field.dispatchEvent(new Event("input", { bubbles: true })); await settle();
  }
  beforeEach(() => {
    status = { running: true, address: "", interfaceId: "en0", hasPassword: true, spaces: ["first", "second"].map(id => ({ id, name: id, kind: "local", accountId: "", rootId: "", localPath: `/${id}`, enabled: true, available: true, allowPrepare: false, error: "" })), tasks: [], cacheBytes: 0, cacheLimit: 1024, originalDir: "", error: "" };
    bridge.Status.mockImplementation(async () => structuredClone(status));
    bridge.UpdateSpace.mockImplementation(async (request: Partial<LANSpace> & { id: string }) => { Object.assign(status.spaces.find(item => item.id === request.id)!, request); });
    bridge.SetPassword.mockResolvedValue(undefined);
  });

  it("remembers only successful confirmations across remounts and spaces without approving another action", async () => {
    await mountOwner(); await click("[data-lan-prepare]");
    await click('[aria-label="关闭对话框"]'); await click("[data-lan-prepare]");
    expect(document.querySelector<HTMLButtonElement>("[data-lan-confirm]")!.disabled).toBe(true);
    expect(status.spaces[0]!.allowPrepare).toBe(false);
    bridge.UpdateSpace.mockRejectedValueOnce(new Error("invalid_request"));
    await click("[data-lan-ack]"); await click("[data-lan-confirm]");
    expect(document.querySelector("[role=dialog] [role=alert]")).not.toBeNull();
    await click('[aria-label="关闭对话框"]'); await click("[data-lan-prepare]");
    expect(document.querySelector<HTMLInputElement>("[data-lan-ack]")!.checked).toBe(false);
    await click("[data-lan-ack]"); await click("[data-lan-confirm]");
    expect(document.querySelector("[role=dialog]")).toBeNull();
    expect(status.spaces[0]!.allowPrepare).toBe(true);
    app!.unmount(); document.body.replaceChildren(); await mountOwner();
    expect(status.spaces[0]!.allowPrepare).toBe(true);
    await click("[data-lan-prepare]");
    expect(document.querySelector("[role=dialog]")).toBeNull();
    expect(status.spaces[0]!.allowPrepare).toBe(false);
    await click('[data-lan-space="second"]'); await click("[data-lan-prepare]");
    expect(document.querySelector("[role=dialog]")).toBeNull();
    expect(status.spaces[1]!.allowPrepare).toBe(true);
    await click("[data-lan-start-stop]");
    expect(document.querySelector<HTMLButtonElement>("[data-lan-confirm]")!.disabled).toBe(true);
    expect(status.running).toBe(true);
  });

  it("keeps folder selection mandatory after the creation reminder has been acknowledged", async () => {
    await openPicker(); await click("[data-lan-choose-cloud]"); await click("[data-lan-ack]"); await click("[data-lan-confirm]");
    await click("[data-lan-create]");
    expect(document.querySelector("[role=dialog]")).not.toBeNull();
    expect(document.querySelector("[data-lan-ack]")).toBeNull();
    expect(document.querySelector<HTMLButtonElement>("[data-lan-confirm]")!.disabled).toBe(true);
    await select("来源类型", "cloud"); await select("固定来源账号", "b");
    expect(document.querySelector<HTMLButtonElement>("[data-lan-confirm]")!.disabled).toBe(true);
    await click('[data-lan-folder="existing"]');
    expect(document.querySelector<HTMLButtonElement>("[data-lan-confirm]")!.disabled).toBe(true);
    await click("[data-lan-choose-cloud]"); await click("[data-lan-confirm]");
    expect(bridge.AddSpace).toHaveBeenLastCalledWith(expect.objectContaining({ accountId: "b", rootId: "existing", allowPrepare: false }));
  });

  it("keeps password entry and matching mandatory without repeating an acknowledged reset warning", async () => {
    await mountOwner(); await click("[data-lan-password]");
    await input('[role=dialog] input[autocomplete=new-password]', "first");
    await input('[role=dialog] .field + .field input', "first");
    await click("[data-lan-ack]"); await click("[data-lan-confirm]");
    await click("[data-lan-password]");
    expect(document.querySelector("[role=dialog]")).not.toBeNull();
    expect(document.querySelector("[data-lan-ack]")).toBeNull();
    expect(document.querySelector<HTMLButtonElement>("[data-lan-confirm]")!.disabled).toBe(true);
    await input('[role=dialog] input[autocomplete=new-password]', "next");
    await input('[role=dialog] .field + .field input', "different");
    expect(document.querySelector<HTMLButtonElement>("[data-lan-confirm]")!.disabled).toBe(true);
    await input('[role=dialog] .field + .field input', "next"); await click("[data-lan-confirm]");
    expect(bridge.SetPassword).toHaveBeenCalledTimes(2);
    expect(bridge.SetPassword).toHaveBeenLastCalledWith("next");
  });
});

describe("LAN owner sync entry", () => {
  const cloudSpace = (id: string, accountId = "a", rootId = "root"): LANSpace => ({ id, name: id, kind: "cloud", accountId, rootId, localPath: "", enabled: true, available: true, allowPrepare: false, error: "" });
  async function mountSpaces(spaces: LANSpace[]) {
    bridge.Status.mockResolvedValue({ running: false, spaces, tasks: [] });
    const host = document.createElement("div"); document.body.append(host);
    app = createApp(LANView); app.mount(host); await settle();
  }
  it("matches explicit account and ancestor IDs without accepting path-prefix or upload-only relationships", async () => {
    sync.pairs = [
      { id: "covering", account: "a", cloudId: "parent", cloudPath: "Elsewhere", name: "Covering", localPath: "/sync", mode: "download" },
      { id: "wrong-account", account: "b", cloudId: "parent", mode: "download" },
      { id: "path-prefix-only", account: "a", cloudId: "unrelated", cloudPath: "全部文件 / Archive", mode: "download" },
      { id: "upload-only", account: "a", cloudId: "root", mode: "backup" },
    ] as SyncPair[];
    await mountSpaces([cloudSpace("shared")]);
    expect(Array.from(document.querySelectorAll("[data-lan-sync-pair]"), item => item.getAttribute("data-lan-sync-pair"))).toEqual(["covering"]);
    await click('[data-lan-sync-pair="covering"] button');
    expect(sync).toMatchObject({ scope: "all", selected: "covering", detail: true, tab: "settings" });
  });
  it("discards stale folder trails and disables creation when the source account disappears", async () => {
    const { promise, resolve } = deferred<{ id: string; name: string }[]>();
    sync.pairs = [
      { id: "current-b", account: "b", cloudId: "b-parent", mode: "download" },
      { id: "old-ancestor", account: "b", cloudId: "root", mode: "download" },
    ] as SyncPair[];
    bridge.SyncFolderTrail.mockImplementation((account: string) => account === "a" ? promise : Promise.resolve([{ id: "b-parent", name: "Current B" }, { id: "other", name: "Other" }]));
    await mountSpaces([cloudSpace("first"), cloudSpace("second", "b", "other")]);
    await click('[data-lan-space="second"]');
    resolve([{ id: "root", name: "Old account" }]); await settle();
    expect(Array.from(document.querySelectorAll("[data-lan-sync-pair]"), item => item.getAttribute("data-lan-sync-pair"))).toEqual(["current-b"]);
    expect(document.querySelector<HTMLButtonElement>("[data-lan-sync-create]")!.disabled).toBe(false);
    const previous = accounts.list;
    accounts.list = previous.filter(account => account.id !== "b"); await settle();
    expect(document.querySelector<HTMLButtonElement>("[data-lan-sync-create]")!.disabled).toBe(true);
    accounts.list = previous;
  });
  it("shows trail errors without enabling preparation and leaves local spaces unchanged", async () => {
    bridge.SyncFolderTrail.mockRejectedValue(new Error("offline"));
    await mountSpaces([cloudSpace("broken"), { ...cloudSpace("local"), kind: "local", localPath: "/local" }]);
    expect(document.querySelector("[data-lan-sync] [role=alert]")).not.toBeNull();
    expect(document.querySelector<HTMLButtonElement>("[data-lan-sync-create]")!.disabled).toBe(true);
    await click('[data-lan-space="local"]');
    expect(document.querySelector("[data-lan-sync]")).toBeNull();
  });
});
