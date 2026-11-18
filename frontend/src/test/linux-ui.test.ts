import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, reactive, type App } from "vue";
import type * as Dom from "../dom";
import { applyLanguage } from "../i18n";
import { appLock } from "../store";
import { updates, installUpdate } from "../updatestore";
import PrivacySettings from "../components/PrivacySettings.vue";
import UpdateSettings from "../components/UpdateSettings.vue";

vi.mock("../dom", async (importOriginal) => ({ ...(await importOriginal<typeof Dom>()), onMac: false, onWindows: false, onLinux: true, systemName: "Linux" }));
vi.mock("../updatestore", () => ({
  updates: reactive({ state: null, pending: false, error: "" }),
  watchUpdates: vi.fn(), checkUpdates: vi.fn(), downloadUpdate: vi.fn(), cancelUpdate: vi.fn(),
  installUpdate: vi.fn(), ignoreUpdate: vi.fn(), remindUpdateLater: vi.fn(), setAutomaticUpdates: vi.fn(),
}));
vi.mock("../store", () => ({
  APP_LOCK_PRESETS: [0, 1, 5], MAX_APP_LOCK_DELAY: 1440,
  appLock: reactive({ state: null, note: "" }), appLockSheet: reactive({ mode: "" }),
  telemetry: reactive({ state: null, saving: false, error: "" }),
  lockDelayLabel: (n: number) => String(n), lockNow: vi.fn(), manageLockPassword: vi.fn(),
  setAppLockDelay: vi.fn(), setAppLockEnabled: vi.fn(), setAppLockMethod: vi.fn(),
  setTelemetryEnabled: vi.fn(), loadTelemetry: vi.fn(),
}));

let app: App | undefined;
beforeEach(() => {
  applyLanguage("en", "en");
  vi.clearAllMocks();
});
afterEach(() => {
  app?.unmount(); app = undefined;
  document.body.replaceChildren();
  applyLanguage("zh-Hans", "zh-Hans");
});

function mount(component: typeof PrivacySettings | typeof UpdateSettings) {
  const host = document.createElement("div"); document.body.append(host);
  app = createApp(component); app.mount(host);
  return host;
}

function updateState() {
  return {
    current: "0.3.0", supported: true, automatic: true, phase: "ready", error: "", lastChecked: "", received: 1024, notice: false,
    info: { version: "0.4.0", publishedAt: "2026-11-08T16:43:40Z", size: 1024, minOS: "2.39", compatible: true, notes: "" },
  };
}

function lockState() {
  return {
    enabled: false, delay: 5, locked: false, method: "system", hasPassword: false, proveWith: "",
    available: false, unavailable: "No desktop authentication agent", authenticating: false,
    attemptsLeft: 5, cooldownUntil: 0, playback: "", screenshotProtectionAvailable: false,
  };
}

describe("Linux desktop settings", () => {
  it("hands verified updates to the user without promising restart or silent installation", async () => {
    updates.state = updateState();
    const host = mount(UpdateSettings); await nextTick();
    const install = Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Show Package"));
    expect(install).toBeDefined();
    install!.click();
    expect(installUpdate).toHaveBeenCalledOnce();
    expect(host.textContent).toContain("does not automatically write to system folders or replace the running app");
    expect(host.textContent).toContain("package manager");
    expect(host.textContent).toContain("replace the original file manually");
    expect(host.textContent).not.toMatch(/macOS|Windows|Gatekeeper|Restart & Update/);
  });

  it("describes the Linux userspace baseline rather than a kernel or macOS version", async () => {
    updates.state = updateState(); updates.state.info!.compatible = false;
    const host = mount(UpdateSettings); await nextTick();
    expect(host.querySelector(".error")?.textContent).toContain("glibc 2.39");
    expect(host.querySelector(".error")?.textContent).toContain("Ubuntu 24.04");
    updates.state.supported = false; await nextTick();
    expect(host.textContent).toContain("development builds or on this platform");
    expect(host.querySelector<HTMLButtonElement>(".check")?.disabled).toBe(true);
  });

  it("uses reported security capability and keeps standalone password available", async () => {
    appLock.state = lockState();
    const host = mount(PrivacySettings); await nextTick();
    const methods = host.querySelectorAll<HTMLButtonElement>('[role="radio"]');
    expect(methods[0].disabled).toBe(true);
    expect(methods[1].disabled).toBe(false);
    expect(host.textContent).toContain("No desktop authentication agent");
    expect(host.textContent).toContain("Linux cannot guarantee blocking screenshots");
    expect(host.textContent).not.toMatch(/Touch ID|Windows Hello|this Mac/);
    appLock.state.available = true; appLock.state.unavailable = ""; await nextTick();
    expect(methods[0].disabled).toBe(false);
    expect(host.textContent).toContain("polkit");
    appLock.state.screenshotProtectionAvailable = true; await nextTick();
    expect(host.textContent).not.toContain("Linux cannot guarantee blocking screenshots");
  });
});
