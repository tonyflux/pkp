import { afterEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick } from "vue";

const actions = vi.hoisted(() => ({ unlock: vi.fn(), forget: vi.fn() }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  AppLockService: { Unlock: actions.unlock, ForgetAndSignOut: actions.forget },
  DriveService: {}, PlayerService: {}, AppearanceService: {}, ExternalOpenService: {},
  MenuBarService: {}, NotifyService: {}, TelemetryService: {},
}));
import LockScreen from "./components/LockScreen.vue";
import { appLock } from "./store";
import type { AppLockState } from "../bindings/github.com/tonyflux/pkp/internal/services";

const base = { locked: true, method: "system", hasPassword: false, authenticating: false, unavailable: "no agent", available: false } as AppLockState;
let unmount: (() => void) | undefined;
afterEach(() => {
  unmount?.();
  unmount = undefined;
  appLock.state = null;
  appLock.note = "";
  vi.resetAllMocks();
});

async function screen() {
  appLock.state = { ...base };
  const host = document.createElement("div");
  document.body.append(host);
  const app = createApp(LockScreen);
  app.mount(host);
  unmount = () => { app.unmount(); host.remove(); };
  await nextTick();
  return host;
}

function button(host: HTMLElement, text: string): HTMLButtonElement {
  const found = Array.from(host.querySelectorAll("button")).find((item) => item.textContent?.trim() === text);
  expect(found).toBeDefined();
  return found!;
}

describe("unavailable system authentication", () => {
  it("allows retrying after the system recovers without clearing data", async () => {
    actions.unlock.mockResolvedValue({ result: "ok", message: "", state: { ...base, locked: false, available: true } });
    const host = await screen();
    button(host, "解锁 PKP").click();
    await vi.waitFor(() => expect(appLock.state?.locked).toBe(false));
    expect(actions.unlock).toHaveBeenCalledOnce();
    expect(actions.forget).not.toHaveBeenCalled();
  });

  it("explains the destructive recovery before confirmation", async () => {
    const host = await screen();
    button(host, "无法使用系统验证？").click();
    await nextTick();
    const dialog = host.querySelector('[role="alertdialog"]');
    expect(dialog?.textContent).toContain("退出所有账号");
    expect(dialog?.textContent).toContain("未完成的本地传输");
    expect(actions.forget).not.toHaveBeenCalled();
    button(host, "返回锁屏").click();
    await nextTick();
    expect(host.querySelector('[role="alertdialog"]')).toBeNull();
    expect(actions.forget).not.toHaveBeenCalled();
  });
});
