import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createApp, h, nextTick, ref, type App } from "vue";
import type { DriveStatus } from "../bindings/github.com/tonyflux/pkp/internal/services";
import SavedHistoryDialogs from "./components/SavedHistoryDialogs.vue";
import SavedSearches from "./components/SavedSearches.vue";
import { saveSearch } from "./savedsearches";
import { drive, savedHistory, ui } from "./store";

const account = "sidebar-test-a";
const other = "sidebar-test-b";
let app: App;
let host: HTMLElement;
const compact = ref(false);

beforeEach(() => {
  localStorage.removeItem(`pkp.savedSearches.${account}`);
  localStorage.removeItem(`pkp.savedSearches.${other}`);
  compact.value = false;
  drive.status = { accountId: account, configured: true } as DriveStatus;
  Object.assign(savedHistory, { account: "", id: "", baseline: null, error: "", dialog: null });
  ui.route = "files";
  host = document.createElement("div");
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  host.remove();
  localStorage.removeItem(`pkp.savedSearches.${account}`);
  localStorage.removeItem(`pkp.savedSearches.${other}`);
  drive.status = null;
  savedHistory.dialog = null;
});

describe("saved searches sidebar", () => {
  it("does not focus the heading on startup, and reveals the empty state only on request", async () => {
    document.activeElement instanceof HTMLElement && document.activeElement.blur();
    app = createApp({ render: () => [h(SavedSearches, { compact: compact.value }), h(SavedHistoryDialogs)] });
    app.mount(host);
    await nextTick();
    const heading = host.querySelector<HTMLButtonElement>("[data-saved-history-heading]")!;
    expect(document.activeElement).not.toBe(heading);
    expect(heading.getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector<HTMLElement>(".saved-panel")!.style.display).toBe("none");
    heading.click();
    await nextTick();
    expect(heading.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector(".saved-empty")?.textContent).toBe("暂无已保存的搜索");
  });

  it("reveals the first saved search and respects a later manual collapse", async () => {
    app = createApp(SavedSearches, { compact: false });
    app.mount(host);
    await nextTick();
    const heading = host.querySelector<HTMLButtonElement>("[data-saved-history-heading]")!;
    saveSearch(account, "旅行", "旅行", null, "all");
    await nextTick();
    expect(heading.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector(".saved-open")?.textContent).toContain("旅行");
    heading.click();
    saveSearch(account, "电影", "电影", null, "all");
    await nextTick();
    expect(heading.getAttribute("aria-expanded")).toBe("false");
  });

  it("shows existing searches on account switch and exposes storage errors", async () => {
    saveSearch(other, "工作", "工作", null, "all");
    app = createApp(SavedSearches, { compact: false });
    app.mount(host);
    await nextTick();
    drive.status = { accountId: other, configured: true } as DriveStatus;
    await nextTick();
    expect(host.querySelector(".saved-open")?.textContent).toContain("工作");
    expect(host.querySelector("[data-saved-history-heading]")?.getAttribute("aria-expanded")).toBe("true");
    localStorage.setItem(`pkp.savedSearches.${account}`, "invalid");
    drive.status = { accountId: account, configured: true } as DriveStatus;
    await nextTick();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("没能读取已保存的搜索");
    expect(host.querySelector("[data-saved-history-heading]")?.getAttribute("aria-expanded")).toBe("true");
  });

  it("keeps compact navigation and returns focus after a search dialog closes", async () => {
    const item = saveSearch(account, "旅行", "旅行", null, "all");
    compact.value = true;
    app = createApp({ render: () => [h(SavedSearches, { compact: compact.value }), h(SavedHistoryDialogs)] });
    app.mount(host);
    await nextTick();
    const heading = host.querySelector<HTMLButtonElement>("[data-saved-history-heading]")!;
    heading.click();
    await nextTick();
    const row = document.querySelector<HTMLButtonElement>("[data-saved-history-action]")!;
    row.click();
    await nextTick();
    document.querySelector<HTMLButtonElement>('[role="menuitem"]')!.nextElementSibling!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await nextTick();
    expect(document.querySelector<HTMLInputElement>(".saved-history-sheet input")?.value).toBe(item.name);
    document.querySelector<HTMLButtonElement>(".saved-history-sheet [data-cancel]")!.click();
    await nextTick();
    await nextTick();
    expect(document.activeElement).toBe(heading);
  });
});
