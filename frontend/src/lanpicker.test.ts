import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { computed, createApp, defineComponent, h, nextTick, reactive, ref, type App } from "vue";
import type { FolderSearch } from "../bindings/github.com/tonyflux/pkp/internal/services/models";
import type { Entry } from "../bindings/github.com/tonyflux/pkp/internal/lanshare/models";

const bridge = vi.hoisted(() => ({ Status: vi.fn(), Tasks: vi.fn(), Interfaces: vi.fn(), CloudFolders: vi.fn(), SearchFolders: vi.fn(), AddSpace: vi.fn(), UpdateSpace: vi.fn(), Start: vi.fn(), Stop: vi.fn(), SetPassword: vi.fn(), SetStorageDirectory: vi.fn(), ClearCache: vi.fn(), OpenFile: vi.fn(), SyncFolderTrail: vi.fn(), DismissTaskHistory: vi.fn(), CancelTask: vi.fn(), ResumeTask: vi.fn(), ListPreparationEntries: vi.fn(), PreparationThumbnail: vi.fn(), PrepareDesktop: vi.fn(), openNav: vi.fn(), copy: vi.fn() }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services/lanservice", () => bridge);
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services/driveservice", () => bridge);
vi.mock("@wailsio/runtime", async importOriginal => ({ ...await importOriginal<object>(), Clipboard: { SetText: bridge.copy }, Dialogs: { OpenFile: bridge.OpenFile } }));
vi.mock("./syncstore", () => ({
  sync: reactive({ pairs: [], loading: false, error: "", scope: "current", query: "", selected: "", detail: false, tab: "activity", draftOpen: false }),
  syncDialog: reactive({ kind: "" }), syncStateName: () => "已同步", loadSync: vi.fn(),
}));
vi.mock("./store", () => {
  const lock = reactive({ state: null as { locked: boolean } | null });
  const ui = reactive({ message: "", messageTone: "info", messageSerial: 0, route: "lan" });
  return {
    accounts: reactive({ list: [{ id: "a", login: "a@example.test" }, { id: "b", login: "b@example.test" }], dialog: null }),
    accountLabel: (account: { id: string } | null) => account?.id ?? "",
    appLock: lock, appLocked: computed(() => !!lock.state?.locked),
    trailPath: (trail: { name: string }[]) => ["全部文件", ...trail.map(item => item.name)].join(" / "),
    openNav: bridge.openNav,
    ui, drive: reactive({ indexProgress: null }), cleanup: { tab: "", "": { stage: "" } }, indexText: () => "",
    notify: (message: string, tone: string) => { ui.message = message; ui.messageTone = tone; ui.messageSerial++; },
    dismiss: () => { ui.message = ""; },
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
import StatusPill from "./components/StatusPill.vue";
import { sync, type SyncPair } from "./syncstore";
import { accounts, appLock, ui } from "./store";
import { lan, refreshLAN, type LANSpace, type LANStatus } from "./lanstore";

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
  ui.message = ""; ui.messageSerial = 0;
  appLock.state = null;
  accounts.list = initialAccounts;
  sync.pairs = []; sync.scope = "current"; sync.query = ""; sync.selected = ""; sync.detail = false;
  bridge.SyncFolderTrail.mockResolvedValue([{ id: "parent", name: "Archive" }, { id: "root", name: "Movies" }]);
  bridge.Status.mockResolvedValue({ running: false, address: "", interfaceId: "", hasPassword: false, preventSleep: false, preventSleepActive: false, preventSleepSupported: true, spaces: [], tasks: [], cacheBytes: 0, originalDir: "", copyDir: "", storageBusy: false, error: "" });
  bridge.Interfaces.mockResolvedValue([]);
  bridge.CloudFolders.mockResolvedValue([{ id: "existing", name: "Existing" }]);
  bridge.AddSpace.mockResolvedValue({ id: "created" });
});
afterEach(() => { app?.unmount(); app = undefined; document.body.replaceChildren(); Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView"); vi.clearAllTimers(); vi.useRealTimers(); });

describe("LAN storage locations", () => {
  const initial = (): LANStatus => ({ running: false, address: "", interfaceId: "", hasPassword: false, preventSleep: false, preventSleepActive: false, preventSleepSupported: true, spaces: [], tasks: [], cacheBytes: 0, originalDir: "/old-originals", copyDir: "/old-copies", storageBusy: false, error: "" });
  async function mountStorage(status: LANStatus) {
    bridge.Status.mockImplementation(async () => structuredClone(status));
    const host = document.createElement("div"); document.body.append(host);
    app = createApp(LANView); app.mount(host); await settle();
    const button = Array.from(document.querySelectorAll<HTMLButtonElement>(".tabs button")).find(item => item.textContent === "存储与副本")!;
    button.click(); await settle();
  }
  it("disables location changes while preparation is active and localizes backend rejection", async () => {
    const status = initial(); status.storageBusy = true;
    await mountStorage(status);
    expect(document.querySelector<HTMLButtonElement>("[data-lan-original-picker]")?.disabled).toBe(true);
    expect(document.querySelector<HTMLButtonElement>("[data-lan-copy-picker]")?.disabled).toBe(true);
    status.storageBusy = false; await refreshLAN(); await settle();
    bridge.OpenFile.mockResolvedValueOnce("/missing-disk");
    bridge.SetStorageDirectory.mockRejectedValueOnce(new Error("storage_unavailable"));
    await click("[data-lan-copy-picker]");
    expect(lan.error).toBe("保存位置不可用，请检查磁盘和目录权限。");
    expect(document.querySelector("[data-lan-copy-directory]")?.textContent).toBe("/old-copies");
  });
  it("enables cleanup only for existing copies and disables it during cleanup and after removal", async () => {
    const status = initial();
    await mountStorage(status);
    const cleanup = () => document.querySelector<HTMLButtonElement>("[data-lan-cache]")!;
    expect(cleanup().disabled).toBe(true);
    await click("[data-lan-cache]");
    expect(document.querySelector('[role="dialog"]')).toBeNull();

    status.cacheBytes = 4096; await refreshLAN(); await settle();
    expect(cleanup().disabled).toBe(false);
    await click("[data-lan-cache]");
    expect(document.querySelector('[role="dialog"]')).not.toBeNull();
    const pending = deferred<{ removedBytes: number; skipped: number }>();
    bridge.ClearCache.mockImplementationOnce(async () => {
      const result = await pending.promise; status.cacheBytes = 0; return result;
    });
    await click("[data-lan-ack]"); await click("[data-lan-confirm]");
    expect(cleanup().disabled).toBe(true);
    pending.resolve({ removedBytes: 4096, skipped: 0 }); await settle();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(cleanup().disabled).toBe(true);
    await click("[data-lan-cache]");
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });
  it("keeps cleanup inaccessible while status is unknown or unavailable", async () => {
    const pending = deferred<LANStatus>();
    bridge.Status.mockReturnValueOnce(pending.promise);
    await mountStorage(initial());
    const cleanup = () => document.querySelector<HTMLButtonElement>("[data-lan-cache]")!;
    expect(cleanup().disabled).toBe(true);
    await click("[data-lan-cache]");
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    pending.resolve({ ...initial(), cacheBytes: 4096 }); await settle();
    expect(cleanup().disabled).toBe(false);
    bridge.Status.mockRejectedValueOnce(new Error("service_closed"));
    await refreshLAN(); await settle();
    expect(cleanup().disabled).toBe(true);
    await click("[data-lan-cache]");
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });
});

describe("LAN network selection", () => {
  const wifi = { id: "wifi@192.168.1.5", name: "Wi-Fi", address: "192.168.1.5" };
  const ethernet = { id: "ethernet@192.168.1.6", name: "Ethernet", address: "192.168.1.6" };
  const shared: LANSpace = { id: "local", name: "Shared", kind: "local", accountId: "", rootId: "", localPath: "/safe", enabled: true, available: true, allowPrepare: false, error: "" };
  const stopped: LANStatus = { running: false, address: "", interfaceId: "", hasPassword: true, preventSleep: false, preventSleepActive: false, preventSleepSupported: true, spaces: [shared], tasks: [], cacheBytes: 0, originalDir: "", copyDir: "", storageBusy: false, error: "" };
  async function mountNetwork() {
    const host = document.createElement("div"); document.body.append(host);
    app = createApp(LANView); app.mount(host); await settle();
  }
  it("selects the sole interface but still waits for explicit sharing confirmation", async () => {
    bridge.Status.mockResolvedValue(stopped); bridge.Interfaces.mockResolvedValue([wifi]); bridge.Start.mockResolvedValue(undefined);
    await mountNetwork();
    expect(document.querySelector<HTMLSelectElement>('select[aria-label="监听网卡"]')!.value).toBe(wifi.id);
    expect(document.querySelector<HTMLButtonElement>("[data-lan-start-stop]")!.disabled).toBe(false);
    expect(bridge.Start).not.toHaveBeenCalled();
    await click("[data-lan-start-stop]");
    expect(bridge.Start).not.toHaveBeenCalled();
    await click("[data-lan-ack]"); await click("[data-lan-confirm]");
    expect(bridge.Start).toHaveBeenCalledExactlyOnceWith({ interfaceId: wifi.id, port: 17891 });
  });
  it("preserves a valid manual choice and reconciles removed interfaces on refresh", async () => {
    bridge.Status.mockResolvedValue(stopped); bridge.Interfaces.mockResolvedValue([wifi, ethernet]);
    await mountNetwork();
    const current = () => document.querySelector<HTMLSelectElement>('select[aria-label="监听网卡"]')!.value;
    expect(current()).toBe("");
    await select("监听网卡", ethernet.id);
    bridge.Interfaces.mockResolvedValue([wifi, ethernet]); await click(".network > button");
    expect(current()).toBe(ethernet.id);
    bridge.Interfaces.mockResolvedValue([wifi]); await click(".network > button");
    expect(current()).toBe(wifi.id);
    bridge.Interfaces.mockResolvedValue([]); await click(".network > button");
    expect(current()).toBe("");
    expect(document.querySelector<HTMLButtonElement>("[data-lan-start-stop]")!.disabled).toBe(true);
    bridge.Interfaces.mockResolvedValue([ethernet]); await click(".network > button");
    expect(current()).toBe(ethernet.id);
    await select("监听网卡", ""); await vi.advanceTimersByTimeAsync(2000); await settle();
    expect(current()).toBe("");
  });
  it("keeps the live interface while sharing and selects a remaining one after stop", async () => {
    const running: LANStatus = { ...stopped, running: true, interfaceId: wifi.id, address: "http://192.168.1.5:17891" };
    bridge.Status.mockResolvedValue(running); bridge.Interfaces.mockResolvedValue([wifi, ethernet]);
    await mountNetwork();
    const current = () => document.querySelector<HTMLSelectElement>('select[aria-label="监听网卡"]')!;
    expect(current().value).toBe(wifi.id); expect(current().disabled).toBe(true);
    bridge.Interfaces.mockResolvedValue([ethernet]); await click(".network > button");
    expect(current().value).toBe(wifi.id); expect(current().disabled).toBe(true);
    bridge.Status.mockResolvedValue(stopped); await vi.advanceTimersByTimeAsync(2000); await settle();
    expect(current().value).toBe(ethernet.id); expect(current().disabled).toBe(false);
  });
  it("reconciles stopped status arriving after interfaces", async () => {
    let resolve!: (value: LANStatus) => void;
    bridge.Status.mockImplementationOnce(() => new Promise<LANStatus>(done => { resolve = done; }));
    bridge.Interfaces.mockResolvedValue([wifi]);
    const host = document.createElement("div"); document.body.append(host);
    app = createApp(LANView); app.mount(host); await settle();
    resolve({ ...stopped, interfaceId: "previous-interface" }); await settle();
    expect(document.querySelector<HTMLSelectElement>('select[aria-label="监听网卡"]')!.value).toBe(wifi.id);
  });
});

describe("LAN address copy", () => {
  it("shows current results without changing the service layout, restarts the success timer, and ignores stale results", async () => {
    bridge.Status.mockResolvedValue({ running: true, address: "http://192.168.1.5:17891", interfaceId: "wifi", hasPassword: true, preventSleep: false, preventSleepActive: false, preventSleepSupported: true, spaces: [], tasks: [], cacheBytes: 0, originalDir: "", copyDir: "", storageBusy: false, error: "" });
    const older = deferred<void>();
    bridge.copy.mockReturnValueOnce(older.promise).mockRejectedValueOnce(new Error("clipboard"));
    const showLAN = ref(true);
    const host = document.createElement("div"); document.body.append(host);
    app = createApp({ render: () => [showLAN.value ? h(LANView) : null, h(StatusPill)] }); app.mount(host); await settle();
    const button = document.querySelector<HTMLButtonElement>("[data-lan-copy]")!;
    const service = document.querySelector("[data-lan-service]")!;
    const previous = service.previousElementSibling, next = service.nextElementSibling;
    const message = () => document.querySelector(".pill .message")?.textContent ?? "";
    button.focus(); await click("[data-lan-copy]"); await click("[data-lan-copy]");
    expect(message()).toContain("复制失败");
    expect(document.activeElement).toBe(button);
    older.resolve(); await settle();
    expect(message()).toContain("复制失败");
    bridge.copy.mockResolvedValue(undefined); await click("[data-lan-copy]");
    expect(message()).toBe("地址已复制");
    expect(service.previousElementSibling).toBe(previous); expect(service.nextElementSibling).toBe(next);
    await vi.advanceTimersByTimeAsync(3000); await click("[data-lan-copy]");
    await vi.advanceTimersByTimeAsync(2000); await settle();
    expect(message()).toBe("地址已复制");
    await vi.advanceTimersByTimeAsync(10000); await settle();
    expect(message()).toBe("");
    const pending = deferred<void>(); bridge.copy.mockReturnValueOnce(pending.promise);
    await click("[data-lan-copy]"); showLAN.value = false; await settle();
    pending.resolve(); await settle();
    expect(message()).toBe("");
  });
});

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

  it("keeps browsing and explicit authorization unchanged when switching directory views", async () => {
    await openPicker();
    await click('.file-view-switch [aria-label="列表"]');
    expect(bridge.CloudFolders).toHaveBeenCalledTimes(1);
    expect(document.querySelector(".folder-current strong")?.textContent).toBe("云盘根目录");
    expect(bridge.AddSpace).not.toHaveBeenCalled();
    await click('[data-lan-folder="existing"]');
    await click('.file-view-switch [aria-label="网格"]');
    expect(bridge.CloudFolders).toHaveBeenLastCalledWith("a", "existing");
    expect(document.querySelector(".folder-current strong")?.textContent).toBe("Existing");
    expect(document.querySelector<HTMLButtonElement>("[data-lan-confirm]")!.disabled).toBe(true);
    await click("[data-lan-choose-cloud]");
    await click("[data-lan-ack]"); await click("[data-lan-confirm]");
    expect(bridge.AddSpace).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ accountId: "a", rootId: "existing", kind: "cloud" }));
  });

  it("restores the directory list after clearing search and resets scrolling for a new directory", async () => {
    bridge.SearchFolders.mockResolvedValue(result("found"));
    await openPicker();
    const list = document.querySelector<HTMLElement>(".folder-list")!;
    const body = document.querySelector<HTMLElement>(".dialog-body")!;
    list.scrollTop = 420; body.scrollTop = 35;
    await search("Movies");
    expect(list.scrollTop).toBe(0);
    list.scrollTop = 80;
    await click('[aria-label="清除搜索"]');
    expect(list.scrollTop).toBe(420);
    expect(body.scrollTop).toBe(35);
    await click('[data-lan-folder="existing"]');
    expect(list.scrollTop).toBe(0);
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
    status = { running: true, address: "", interfaceId: "en0", hasPassword: true, preventSleep: false, preventSleepActive: false, preventSleepSupported: true, spaces: ["first", "second"].map(id => ({ id, name: id, kind: "local", accountId: "", rootId: "", localPath: `/${id}`, enabled: true, available: true, allowPrepare: false, error: "" })), tasks: [], cacheBytes: 0, originalDir: "", copyDir: "", storageBusy: false, error: "" };
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
    expect(document.querySelector<HTMLButtonElement>("[data-lan-choose-preparation]")!.disabled).toBe(false);
    const previous = accounts.list;
    accounts.list = previous.filter(account => account.id !== "b"); await settle();
    expect(document.querySelector<HTMLButtonElement>("[data-lan-choose-preparation]")!.disabled).toBe(false);
    accounts.list = previous;
  });
  it("shows trail errors without blocking independent preparation and supports local spaces", async () => {
    bridge.SyncFolderTrail.mockRejectedValue(new Error("offline"));
    await mountSpaces([cloudSpace("broken"), { ...cloudSpace("local"), kind: "local", localPath: "/local" }]);
    expect(document.querySelector("[data-lan-sync] [role=alert]")).not.toBeNull();
    expect(document.querySelector<HTMLButtonElement>("[data-lan-choose-preparation]")!.disabled).toBe(false);
    await click('[data-lan-space="local"]');
    expect(document.querySelector("[data-lan-sync]")).toBeNull();
    expect(document.querySelector<HTMLButtonElement>("[data-lan-choose-preparation]")!.disabled).toBe(false);
  });
});

describe("desktop preparation picker", () => {
  const spaces: LANSpace[] = ["first", "second"].map(id => ({ id, name: id, kind: "local", accountId: "", rootId: "", localPath: `/${id}`, enabled: true, available: true, allowPrepare: false, error: "" }));
  const entry = (id: string, isDir = false) => ({ id, name: id, isDir, parentId: "", version: "", mime: "", size: 0, modified: "", ready: false, compatibleReady: false });
  let observer: { callback: IntersectionObserverCallback; elements: Element[]; disconnected: boolean };
  function intersect(id: string, isIntersecting = true) {
    const target = observer.elements.find(element => (element as HTMLElement).dataset.prepareCard === id)!;
    observer.callback([{ target, isIntersecting } as IntersectionObserverEntry], {} as IntersectionObserver);
  }
  beforeEach(() => {
    vi.stubGlobal("IntersectionObserver", class {
      constructor(callback: IntersectionObserverCallback) { observer = { callback, elements: [], disconnected: false }; }
      observe(element: Element) { observer.elements.push(element); }
      disconnect() { observer.disconnected = true; }
    });
    bridge.PreparationThumbnail.mockReset().mockResolvedValue("data:image/jpeg;base64,AAAA");
  });
  afterEach(() => vi.unstubAllGlobals());
  async function mount() {
    bridge.Status.mockResolvedValue({ running: false, spaces: structuredClone(spaces), tasks: [] });
    bridge.Tasks.mockResolvedValue([]);
    bridge.ListPreparationEntries.mockImplementation(async (_space: string, parent: string) => parent ? [entry("nested")] : [entry("root-file"), entry("folder", true)]);
    bridge.PrepareDesktop.mockResolvedValue({});
    const host = document.createElement("div"); document.body.append(host); app = createApp(LANView); app.mount(host); await settle();
    await click("[data-lan-choose-preparation]");
  }
  it("distinguishes original and desktop compatibility readiness, including selections across modes", async () => {
    await mount();
    bridge.ListPreparationEntries.mockResolvedValue([
      { ...entry("original"), ready: true },
      { ...entry("compatible"), compatibleReady: true, desktopCompatibleReady: true },
      { ...entry("visitor"), compatibleReady: true },
      { ...entry("unknown"), needsVerification: true },
      entry("missing"),
    ]);
    await click('[data-prepare-folder="folder"]');
    const input = (id: string) => document.querySelector<HTMLInputElement>(`[data-prepare-file="${id}"]`)!;
    expect(input("original").disabled).toBe(true);
    expect(input("compatible").disabled).toBe(false);
    expect(input("unknown").disabled).toBe(false);
    await click('[data-prepare-file="compatible"]');
    await click('[data-prepare-file="missing"]');
    await click('input[value="compatible"]');
    expect(input("original").disabled).toBe(false);
    expect(input("visitor").disabled).toBe(false);
    expect(input("compatible").checked).toBe(true);
    expect(document.querySelector('.selection')?.textContent).toContain("兼容准备已就绪");
    await click('[data-prepare-start]');
    expect(bridge.PrepareDesktop).toHaveBeenCalledExactlyOnceWith("first", "missing", "compatible");
  });
  it("updates selected readiness after a directory refresh", async () => {
    await mount();
    await click('[data-prepare-file="root-file"]');
    bridge.ListPreparationEntries.mockResolvedValue([{ ...entry("root-file"), ready: true }]);
    await click('[data-prepare-refresh]');
    expect(document.querySelector<HTMLButtonElement>('[data-prepare-start]')!.disabled).toBe(true);
    await click('input[value="compatible"]');
    expect(document.querySelector<HTMLButtonElement>('[data-prepare-start]')!.disabled).toBe(false);
    await click('[data-prepare-start]');
    expect(bridge.PrepareDesktop).toHaveBeenCalledExactlyOnceWith("first", "root-file", "compatible");
  });
  it("selects across folders, removes a selection, and only submits on confirmation", async () => {
    await mount();
    await click('[data-prepare-file="root-file"]'); await click('[data-prepare-folder="folder"]'); await click('[data-prepare-file="nested"]');
    expect(document.querySelector(".selection")?.textContent).toContain("folder / nested");
    expect(bridge.PrepareDesktop).not.toHaveBeenCalled();
    await click('[data-prepare-remove="root-file"]'); await click("[data-prepare-start]");
    expect(bridge.PrepareDesktop).toHaveBeenCalledExactlyOnceWith("first", "nested", "original");
    expect(document.querySelector("[data-prepare-start]")).toBeNull();
  });
  it("keeps cross-folder selections through grid and list and does not submit on navigation", async () => {
    await mount();
    await click('[data-prepare-file="root-file"]'); await click('[aria-label="网格"]');
    expect(document.querySelector<HTMLInputElement>('[data-prepare-file="root-file"]')!.checked).toBe(true);
    expect(document.querySelector('[data-prepare-file="root-file"]')!.closest('.card')!.classList.contains('chosen')).toBe(true);
    await click('[data-prepare-folder="folder"]'); await click('[data-prepare-file="nested"]');
    await click('[aria-label="列表"]');
    expect(document.querySelector<HTMLInputElement>('[data-prepare-file="nested"]')!.checked).toBe(true);
    expect(document.querySelectorAll('.selection li')).toHaveLength(2);
    expect(bridge.PrepareDesktop).not.toHaveBeenCalled();
  });
  it("reuses visited directories and thumbnails and restores each view's scroll position", async () => {
    await mount();
    bridge.ListPreparationEntries.mockResolvedValue([{ ...entry("photo"), name: "photo.png", mime: "image/png", ready: true }]);
    const list = document.querySelector<HTMLElement>(".prepare-body")!;
    list.scrollTop = 96;
    await click('[data-prepare-folder="folder"]'); await click('[aria-label="网格"]'); intersect("photo"); await settle();
    const originalThumbnail = document.querySelector<HTMLImageElement>('[data-prepare-card="photo"] img')!.src;
    list.scrollTop = 144;
    await click('nav[aria-label="授权根目录"] button');
    expect(document.querySelector('[data-prepare-folder="folder"]')).not.toBeNull();
    await click('[aria-label="列表"]');
    expect(list.scrollTop).toBe(96);
    await click('[data-prepare-folder="folder"]'); await click('[aria-label="网格"]'); intersect("photo"); await settle();
    expect(list.scrollTop).toBe(144);
    expect(document.querySelector<HTMLImageElement>('[data-prepare-card="photo"] img')!.src).toBe(originalThumbnail);
    expect(bridge.ListPreparationEntries).toHaveBeenCalledTimes(2);
    expect(bridge.PreparationThumbnail).toHaveBeenCalledExactlyOnceWith("first", "photo");
  });
  it("keeps selected files reachable from the fixed action area", async () => {
    await mount(); await click('[data-prepare-file="root-file"]');
    await click('.selection-jump');
    expect(document.activeElement).toBe(document.querySelector('.prepare-body h3'));
    expect(document.querySelector('.selection')?.textContent).toContain('root-file');
    expect(bridge.PrepareDesktop).not.toHaveBeenCalled();
  });
  it("refreshes a cached directory explicitly without discarding the selection", async () => {
    await mount(); await click('[data-prepare-file="root-file"]');
    bridge.ListPreparationEntries.mockResolvedValue([entry("updated")]);
    await click('[data-prepare-refresh]');
    expect(document.querySelector('[data-prepare-file="updated"]')).not.toBeNull();
    expect(document.querySelector('[data-prepare-file="root-file"]')).toBeNull();
    expect(document.querySelector('.selection')?.textContent).toContain("root-file");
    expect(bridge.PrepareDesktop).not.toHaveBeenCalled();
  });
  it("returns to a cached parent while a directory is still loading and ignores the abandoned result", async () => {
    await mount();
    const pending = deferred<Entry[]>(); bridge.ListPreparationEntries.mockReturnValueOnce(pending.promise);
    document.querySelector<HTMLButtonElement>('[data-prepare-folder="folder"]')!.click(); await settle();
    expect(document.querySelector('.entries')?.getAttribute("aria-busy")).toBe("true");
    expect(document.querySelector('[data-prepare-loading] [role="status"]')).not.toBeNull();
    await click('nav[aria-label="授权根目录"] button');
    expect(document.querySelector('[data-prepare-file="root-file"]')).not.toBeNull();
    expect(document.querySelector('.entries')?.getAttribute("aria-busy")).toBe("false");
    pending.resolve([entry("outdated")]); await settle();
    expect(document.querySelector('[data-prepare-file="outdated"]')).toBeNull();
    await click('[data-prepare-folder="folder"]');
    expect(bridge.ListPreparationEntries).toHaveBeenCalledTimes(3);
    expect(document.querySelector('[data-prepare-file="nested"]')).not.toBeNull();
  });
  it("drops cached directories when the picker closes", async () => {
    await mount(); await click('[data-prepare-folder="folder"]');
    await click('[aria-label="关闭对话框"]');
    bridge.ListPreparationEntries.mockResolvedValue([entry("fresh")]);
    await click('[data-lan-choose-preparation]');
    expect(bridge.ListPreparationEntries).toHaveBeenCalledTimes(3);
    expect(document.querySelector('[data-prepare-file="fresh"]')).not.toBeNull();
    expect(document.querySelector('[data-prepare-folder="folder"]')).toBeNull();
  });
  it("loads only visible cloud thumbnails and ready images and ignores stale replies", async () => {
    await mount();
    const pending = deferred<string>();
    bridge.PreparationThumbnail.mockReturnValueOnce(pending.promise);
    bridge.ListPreparationEntries.mockResolvedValue([
      { ...entry('cloud'), name: 'cloud.jpg', mime: 'image/jpeg', thumbnail: 'https://example.test/thumb' },
      { ...entry('local'), name: 'local.jpg', mime: 'image/jpeg', ready: true },
      { ...entry('hidden'), name: 'hidden.png', ready: true },
      { ...entry('unready'), name: 'unready.png' },
      { ...entry('video'), name: 'video.mp4', mime: 'video/mp4', ready: true },
    ]);
    await click('[data-prepare-folder="folder"]'); await click('[aria-label="网格"]');
    expect(bridge.PreparationThumbnail).not.toHaveBeenCalled();
    intersect('cloud'); intersect('local'); intersect('unready'); intersect('video'); await settle();
    expect(bridge.PreparationThumbnail).toHaveBeenCalledExactlyOnceWith('first', 'local');
    expect(document.querySelector<HTMLImageElement>('[data-prepare-card="cloud"] img')?.src).toBe('https://example.test/thumb');
    await click('[aria-label="列表"]'); pending.resolve('data:image/jpeg;base64,AAAA'); await settle();
    await click('[aria-label="网格"]');
    expect(document.querySelector('[data-prepare-card="local"] img')).toBeNull();
    expect(bridge.PrepareDesktop).not.toHaveBeenCalled();
  });
  it("falls back after image errors and dismisses with Escape without preparing", async () => {
    await mount();
    bridge.ListPreparationEntries.mockResolvedValue([{ ...entry('image'), name: 'image.jpg', ready: true }]);
    await click('[data-prepare-folder="folder"]'); await click('[aria-label="网格"]'); intersect('image'); await settle();
    document.querySelector('[data-prepare-card="image"] img')!.dispatchEvent(new Event('error')); await settle();
    expect(document.querySelector('[data-prepare-card="image"] img')).toBeNull();
    expect(document.querySelector('[data-prepare-card="image"] svg')).not.toBeNull();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); await settle();
    expect(document.querySelector('[data-prepare-start]')).toBeNull();
    expect(bridge.PrepareDesktop).not.toHaveBeenCalled();
  });
  it("returns focus to the preparation control when dismissed", async () => {
    await mount();
    await click('[aria-label="关闭对话框"]');
    expect(document.activeElement).toBe(document.querySelector('[data-lan-choose-preparation]'));
    expect(bridge.PrepareDesktop).not.toHaveBeenCalled();
  });
  it("retains failed files and retries only them in compatible mode", async () => {
    await mount();
    await click('[data-prepare-file="root-file"]'); await click('[data-prepare-folder="folder"]'); await click('[data-prepare-file="nested"]');
    await click('input[value="compatible"]');
    bridge.PrepareDesktop.mockRejectedValueOnce(new Error("disk_full"));
    await click("[data-prepare-start]");
    expect(bridge.PrepareDesktop.mock.calls).toEqual([["first", "root-file", "compatible"], ["first", "nested", "compatible"]]);
    expect(document.querySelector(".selection")?.textContent).toContain("root-file");
    await click("[data-prepare-start]");
    expect(bridge.PrepareDesktop).toHaveBeenLastCalledWith("first", "root-file", "compatible");
    expect(bridge.PrepareDesktop).toHaveBeenCalledTimes(3);
  });
  it("retains capacity failures across a batch of 32 files and can navigate back", async () => {
    await mount();
    bridge.ListPreparationEntries.mockResolvedValue(Array.from({ length: 32 }, (_, index) => entry(`file-${index}`)));
    await click('[data-prepare-folder="folder"]');
    for (let index = 0; index < 32; index++) await click(`[data-prepare-file="file-${index}"]`);
    expect(document.querySelectorAll(".selection li")).toHaveLength(32);
    bridge.PrepareDesktop.mockRejectedValueOnce(new Error("disk_full"));
    await click("[data-prepare-start]");
    for (let index = 0; index < 32; index++) await settle();
    expect(bridge.PrepareDesktop).toHaveBeenCalledTimes(32);
    expect(document.querySelectorAll(".selection li")).toHaveLength(1);
    expect(document.querySelector(".selection")?.textContent).toContain("file-0");
    bridge.ListPreparationEntries.mockResolvedValue([entry("root-file")]);
    await click('nav[aria-label="授权根目录"] button');
    expect(bridge.ListPreparationEntries).toHaveBeenCalledTimes(2);
    expect(document.querySelector('[data-prepare-file="root-file"]')).not.toBeNull();
  });
  it("discards closed and switched-space responses and stops subsequent submissions", async () => {
    const listing = deferred<Entry[]>();
    bridge.Status.mockResolvedValue({ running: false, spaces: structuredClone(spaces), tasks: [] });
    bridge.ListPreparationEntries.mockReturnValueOnce(listing.promise).mockResolvedValue([entry("new")]);
    const host = document.createElement("div"); document.body.append(host); app = createApp(LANView); app.mount(host); await settle();
    await click("[data-lan-choose-preparation]"); await click('[aria-label="关闭对话框"]');
    await click('[data-lan-space="second"]'); await click("[data-lan-choose-preparation]");
    listing.resolve([entry("stale")]); await settle();
    expect(document.querySelector('[data-prepare-file="stale"]')).toBeNull();
    expect(document.querySelector('[data-prepare-file="new"]')).not.toBeNull();
    await click('[data-prepare-file="new"]'); await click('[aria-label="关闭对话框"]');
    expect(bridge.PrepareDesktop).not.toHaveBeenCalled();
  });
  it("stops submitting additional files if the app locks during a batch", async () => {
    await mount(); await click('[data-prepare-file="root-file"]'); await click('[data-prepare-folder="folder"]'); await click('[data-prepare-file="nested"]');
    const pending = deferred(); bridge.PrepareDesktop.mockReturnValueOnce(pending.promise);
    document.querySelector<HTMLButtonElement>("[data-prepare-start]")!.click(); await settle();
    expect(document.querySelector<HTMLButtonElement>('[aria-label="网格"]')!.disabled).toBe(true);
    expect(document.querySelector<HTMLButtonElement>('[aria-label="列表"]')!.disabled).toBe(true);
    appLock.state = { locked: true } as NonNullable<typeof appLock.state>; pending.resolve(result("first")); await settle();
    expect(bridge.PrepareDesktop).toHaveBeenCalledTimes(1);
    expect(document.querySelector('[data-prepare-remove="nested"]')).not.toBeNull();
    appLock.state = null; await click('[data-prepare-start]');
    expect(bridge.PrepareDesktop).toHaveBeenCalledTimes(2);
    expect(bridge.PrepareDesktop).toHaveBeenLastCalledWith("first", "nested", "original");
  });
  it("ends an in-flight batch when its space is disabled", async () => {
    await mount(); await click('[data-prepare-file="root-file"]'); await click('[data-prepare-folder="folder"]'); await click('[data-prepare-file="nested"]');
    const pending = deferred(); bridge.PrepareDesktop.mockReturnValueOnce(pending.promise);
    document.querySelector<HTMLButtonElement>("[data-prepare-start]")!.click(); await settle();
    lan.status!.spaces[0]!.enabled = false; await settle();
    pending.resolve(result("irrelevant")); await settle();
    expect(bridge.PrepareDesktop).toHaveBeenCalledTimes(1);
    expect(document.querySelector("[data-prepare-start]")).toBeNull();
  });
});

describe("LAN preparation history", () => {
  let status: LANStatus;
  const finished = ["ready", "failed", "cancelled"];
  const row = (id: string) => document.querySelector<HTMLElement>(`[data-lan-task="${id}"]`);
  beforeEach(async () => {
    const spaces = ["first", "second"].map(id => ({ id, name: id, kind: "local", accountId: "", rootId: "", localPath: `/${id}`, enabled: true, available: true, allowPrepare: true, error: "" }));
    const task = (id: string, spaceId: string, state: string) => ({ id, spaceId, state, name: id, fileId: id, kind: "local", done: 0, total: 0, error: "" });
    status = { running: false, address: "", interfaceId: "", hasPassword: true, preventSleep: false, preventSleepActive: false, preventSleepSupported: true, spaces, tasks: [task("ready", "first", "ready"), task("failed", "first", "failed"), task("cancelled", "second", "cancelled"), task("orphan", "removed", "ready"), ...["queued", "downloading", "converting", "held"].map(state => task(state, "first", state))], cacheBytes: 0, originalDir: "", copyDir: "", storageBusy: false, error: "" };
    bridge.Status.mockImplementation(async () => structuredClone(status));
    bridge.Tasks.mockImplementation(async () => structuredClone(status.tasks));
    bridge.DismissTaskHistory.mockImplementation(async (scope: string, id: string) => { status.tasks = status.tasks.filter(item => !finished.includes(item.state) || (scope && item.spaceId !== scope) || (id && item.id !== id)); });
    bridge.CancelTask.mockResolvedValue(undefined); bridge.ResumeTask.mockResolvedValue(undefined);
    const host = document.createElement("div"); document.body.append(host); app = createApp(LANView); app.mount(host); await settle();
    await click(".tabs button:nth-child(2)");
  });
  it("removes one finished record and restores focus to a neighbour", async () => {
    const button = row("ready")!.querySelector<HTMLButtonElement>("[data-lan-dismiss]")!; button.focus(); button.click(); await settle();
    expect(bridge.DismissTaskHistory).toHaveBeenCalledExactlyOnceWith("first", "ready");
    expect(row("ready")).toBeNull(); expect(row("failed")).not.toBeNull();
    expect(document.activeElement).toBe(row("failed")!.querySelector("button"));
    expect(document.querySelector<HTMLSelectElement>('select[aria-label="任务范围"]')!.value).toBe("first");
  });
  it("clears only the selected space, then all spaces including removed ones", async () => {
    await click("[data-lan-dismiss-all]");
    expect(bridge.DismissTaskHistory).toHaveBeenLastCalledWith("first", "");
    expect(row("ready")).toBeNull(); expect(row("failed")).toBeNull();
    expect(document.querySelector<HTMLButtonElement>("[data-lan-dismiss-all]")!.disabled).toBe(true);
    await select("任务范围", ""); expect(row("cancelled")).not.toBeNull(); expect(row("orphan")).not.toBeNull();
    await click("[data-lan-dismiss-all]");
    expect(bridge.DismissTaskHistory).toHaveBeenLastCalledWith("", "");
    expect(row("cancelled")).toBeNull(); expect(row("orphan")).toBeNull();
    expect(document.querySelector<HTMLSelectElement>('select[aria-label="任务范围"]')!.value).toBe("");
    for (const id of ["queued", "downloading", "converting", "held"]) expect(row(id)).not.toBeNull();
  });
  it("updates live progress and exposes the reported value without losing a paused task", async () => {
    const item = status.tasks.find(task => task.id === "downloading")!;
    item.done = 25; item.total = 100;
    await vi.advanceTimersByTimeAsync(500); await settle();
    expect(row("downloading")!.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("25");
    item.done = 60; item.state = "held";
    await vi.advanceTimersByTimeAsync(500); await settle();
    expect(row("downloading")!.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("60");
    expect(row("downloading")!.textContent).toContain("恢复准备");
    item.total = 1000; item.done = 999;
    await vi.advanceTimersByTimeAsync(500); await settle();
    expect(row("downloading")!.querySelector(".task-percent")?.textContent).toBe("99%");
    item.done = 1000;
    await vi.advanceTimersByTimeAsync(500); await settle();
    expect(row("downloading")!.querySelector(".task-percent")?.textContent).toBe("100%");
  });
  it("keeps focus in the task list when polling removes the focused record", async () => {
    row("ready")!.querySelector<HTMLButtonElement>("button")!.focus();
    status.tasks = status.tasks.filter(item => item.id !== "ready");
    await vi.advanceTimersByTimeAsync(2000); await settle();
    expect(row("ready")).toBeNull();
    expect(document.activeElement).toBe(row("failed")!.querySelector("button"));
    expect(document.querySelector<HTMLSelectElement>('select[aria-label="任务范围"]')!.value).toBe("first");
  });
  it("keeps active task actions and reports dismiss failures", async () => {
    for (const id of ["queued", "downloading", "converting", "held"]) {
      expect(row(id)!.querySelector("[data-lan-dismiss]")).toBeNull();
      expect(row(id)!.textContent).toContain("取消准备");
    }
    expect(row("held")!.textContent).toContain("恢复准备");
    localStorage.setItem("pkp.lan.confirmed.cancel", "1"); localStorage.setItem("pkp.lan.confirmed.resume", "1");
    await click('[data-lan-task="queued"] button'); expect(bridge.CancelTask).toHaveBeenCalledWith("queued");
    await click('[data-lan-task="held"] button'); expect(bridge.ResumeTask).toHaveBeenCalledWith("held");
    bridge.DismissTaskHistory.mockRejectedValueOnce(new Error("storage_unavailable"));
    await click('[data-lan-task="ready"] [data-lan-dismiss]');
    expect(row("ready")).not.toBeNull(); expect(document.querySelector('[role="alert"]')!.textContent).toContain("保存位置不可用");
  });
  it("guards simultaneous actions while dismissing", async () => {
    const pending = deferred<void>(); bridge.DismissTaskHistory.mockReturnValueOnce(pending.promise);
    const button = row("ready")!.querySelector<HTMLButtonElement>("[data-lan-dismiss]")!; button.click(); await settle();
    expect(button.disabled).toBe(true); expect(document.querySelector<HTMLButtonElement>("[data-lan-dismiss-all]")!.disabled).toBe(true);
    expect(document.querySelector<HTMLSelectElement>('select[aria-label="任务范围"]')!.disabled).toBe(true);
    pending.resolve(); await settle(); expect(bridge.DismissTaskHistory).toHaveBeenCalledTimes(1);
  });
});
