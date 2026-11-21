import { afterEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, reactive, type App } from "vue";
import type * as Dom from "../dom";
import { applyLanguage } from "../i18n";
import { installUpdate, updates } from "../updatestore";
import UpdateSettings from "../components/UpdateSettings.vue";

vi.mock("../dom", async (importOriginal) => ({ ...(await importOriginal<typeof Dom>()), onMac: false, onWindows: true, onLinux: false, systemName: "Windows" }));
vi.mock("../updatestore", () => ({
  updates: reactive({ state: null, pending: false, error: "" }),
  watchUpdates: vi.fn(), checkUpdates: vi.fn(), downloadUpdate: vi.fn(), cancelUpdate: vi.fn(),
  installUpdate: vi.fn(), ignoreUpdate: vi.fn(), remindUpdateLater: vi.fn(), setAutomaticUpdates: vi.fn(),
}));

let app: App | undefined;
afterEach(() => {
  app?.unmount(); app = undefined;
  document.body.replaceChildren();
  applyLanguage("zh-Hans", "zh-Hans");
});

describe("Windows updates", () => {
  it("offers restart, explains authorization, and identifies unsupported locations", async () => {
    applyLanguage("en", "en");
    updates.state = {
      current: "1.0.1", supported: true, automatic: true, phase: "ready", error: "", lastChecked: "", received: 1024, notice: false,
      info: { version: "1.0.2", publishedAt: "2026-11-21T00:00:00Z", size: 1024, minOS: "10", compatible: true, notes: "" },
    };
    const host = document.createElement("div"); document.body.append(host);
    app = createApp(UpdateSettings); app.mount(host); await nextTick();
    const button = Array.from(host.querySelectorAll("button")).find((item) => item.textContent?.includes("Restart & Update"));
    expect(button).toBeDefined();
    button!.click();
    expect(installUpdate).toHaveBeenCalledOnce();
    expect(host.textContent).toContain("approving Windows authorization if prompted");
    expect(host.textContent).toContain("Quit other PKP instances first");
    updates.state.error = "installLocation"; await nextTick();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("portable or non-standard installations");
    expect(host.textContent).not.toContain("Quit & Run Installer");
    updates.state.error = "install"; await nextTick();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Check disk space and permissions");
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("official Release");
    updates.state.error = "userPackage"; await nextTick();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("no per-user Windows update package");
    expect(host.querySelector('[role="alert"]')?.textContent).not.toContain("still running");
  });

  it("offers the installer wizard for a legacy protocol", async () => {
    applyLanguage("en", "en");
    updates.state = {
      current: "1.0.1", supported: true, automatic: true, phase: "ready", error: "", lastChecked: "", received: 1024, notice: false,
      info: Object.assign({ version: "1.0.2", publishedAt: "2026-11-21T00:00:00Z", size: 1024, minOS: "10", compatible: true, notes: "" }, { manualInstall: true }),
    };
    const host = document.createElement("div"); document.body.append(host);
    app = createApp(UpdateSettings); app.mount(host); await nextTick();
    const button = Array.from(host.querySelectorAll("button")).find((item) => item.textContent?.includes("Quit & Run Installer"));
    expect(button).toBeDefined();
    expect(host.textContent).toContain("complete the installation wizard");
    expect(host.textContent).not.toContain("Restart & Update");
    expect(host.textContent).not.toContain("updates and reopens automatically");
    button!.click();
    expect(installUpdate).toHaveBeenCalled();
  });
});
