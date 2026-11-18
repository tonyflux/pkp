import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, type App } from "vue";
import type { Account, DriveStatus, Entry, IndexProgress, ListRequest } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type * as Services from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({ list: vi.fn() }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", async (importOriginal) => {
  const services = await importOriginal<typeof Services>();
  return { ...services, DriveService: { ...services.DriveService, List: backend.list } };
});

import FilesView from "./components/FilesView.vue";
import { accounts, drive, ui } from "./store";

let app: App;
let host: HTMLElement;

beforeEach(async () => {
  vi.stubGlobal("IntersectionObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
  backend.list.mockResolvedValue({ entries: [], total: 0, offset: 0, live: false, nextPageToken: "" });
  accounts.list = [{ id: "a", label: "Tony", login: "a@example.com" }, { id: "b", label: "Other", login: "b@example.com" }] as Account[];
  drive.status = { accountId: "a", configured: true } as DriveStatus;
  drive.indexProgress = { ready: true, phase: "idle", account: "a", syncedAt: "" } as IndexProgress;
  drive.trail = [{ id: "parent", name: "父级" }, { id: "child", name: "当前文件夹" }];
  drive.entries = [{ id: "file", name: "示例.mp4", folder: false, category: "video", size: 1, modifiedTime: "", path: "" } as Entry];
  drive.picked = [];
  drive.pickAnchor = "";
  ui.route = "files"; ui.view = "all"; ui.scope = "folder"; ui.query = ""; ui.category = "all";
  host = document.createElement("div");
  document.body.append(host);
  app = createApp(FilesView);
  app.mount(host);
  await nextTick();
});

afterEach(() => {
  app.unmount();
  host.remove();
  Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView");
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  accounts.list = [];
  drive.status = null;
  drive.indexProgress = null;
  drive.trail = [];
  drive.entries = [];
});

describe("file location header", () => {
  it("navigates ancestors in path order and requests the chosen folder", async () => {
    const heading = host.querySelector(".head")!;
    expect(Array.from(heading.querySelectorAll("button, h1")).map((item) => item.textContent?.trim())).toEqual(["全部文件", "父级", "当前文件夹"]);
    expect(heading.querySelector('[title="当前账号"]')).toBeNull();
    heading.querySelector<HTMLButtonElement>('[title="父级"]')!.click();
    await vi.waitFor(() => expect(backend.list).toHaveBeenCalled());
    expect((backend.list.mock.calls[0][0] as ListRequest).parentId).toBe("parent");
    expect(drive.trail.map((crumb) => crumb.name)).toEqual(["父级"]);
  });

  it("lets breadcrumb keys reach navigation without selecting a file", async () => {
    const path = host.querySelector<HTMLElement>('.crumbs')!;
    path.focus();
    path.dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true, cancelable: true }));
    await nextTick();
    expect(drive.picked).toEqual([]);
    expect(drive.selectedId).toBe("");
  });

  it("keeps the incomplete-index warning when browsing the whole drive", async () => {
    drive.indexProgress = { ...drive.indexProgress!, ready: false };
    ui.scope = "drive";
    await nextTick();
    expect(host.querySelector(".head h1")?.textContent).toBe("全部文件");
    expect(host.querySelector(".crumbs")).toBeNull();
    expect(host.querySelector('[role="status"]')?.textContent).toContain("本地索引建立中");
  });
});
