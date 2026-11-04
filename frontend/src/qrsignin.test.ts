import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, h, nextTick, ref } from "vue";
import type { QRSignInRequest, QRSignInState, SignInResult } from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  begin: vi.fn<(request: QRSignInRequest) => Promise<QRSignInState>>(),
  poll: vi.fn<(id: string) => Promise<QRSignInState>>(),
  cancel: vi.fn<(id: string) => Promise<void>>(),
}));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  DriveService: { BeginQRSignIn: backend.begin, PollQRSignIn: backend.poll, CancelQRSignIn: backend.cancel },
}));
import QRSignIn from "./components/QRSignIn.vue";

const signedIn = {
  failure: "", message: "", existed: true,
  status: { accountId: "current", configured: true },
  account: { id: "current", current: true },
} as SignInResult;
const unmounts = new Set<() => void>();

function response(overrides: Partial<QRSignInState> = {}): QRSignInState {
  return {
    id: "qr-current", url: "https://example.test/qr", expiresAt: new Date(Date.now() + 4000).toISOString(),
    interval: 1, state: "waiting", message: "", result: null, ...overrides,
  };
}
function deferred<T>() {
  return (Promise as PromiseConstructor & {
    withResolvers<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (reason?: unknown) => void };
  }).withResolvers<T>();
}
async function settle() { await Promise.resolve(); await nextTick(); }
async function mountSignIn() {
  const account = ref("current");
  const visible = ref(true);
  const complete = vi.fn();
  const host = document.createElement("div");
  document.body.append(host);
  const app = createApp({
    setup: () => () => visible.value
      ? h(QRSignIn, { key: account.value, account: account.value, onComplete: complete })
      : h("div", "password sign-in"),
  });
  app.mount(host);
  const unmount = () => { app.unmount(); host.remove(); unmounts.delete(unmount); };
  unmounts.add(unmount);
  await settle();
  return { host, account, visible, complete, unmount };
}
function button(host: HTMLElement, label: string) {
  const found = Array.from(host.querySelectorAll("button")).find((element) => element.textContent === label);
  expect(found, `button: ${label}`).toBeDefined();
  return found!;
}
function expectExpired(host: HTMLElement) {
  expect(host.querySelector(".status-label")?.textContent).toBe("二维码已失效");
  expect(button(host, "刷新二维码").disabled).toBe(false);
  expect(host.querySelector(".network-actions")).toBeNull();
  expect(host.querySelector(".orbit")).toBeNull();
  expect(host.querySelector(".qr-square")?.getAttribute("aria-busy")).toBe("false");
  expect(vi.getTimerCount()).toBe(0);
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-11-04T12:00:00Z"));
  vi.resetAllMocks();
  backend.begin.mockImplementation(async ({ account }) => response({ id: `qr-${account}` }));
  backend.cancel.mockResolvedValue(undefined);
});
afterEach(() => {
  for (const unmount of unmounts) unmount();
  vi.clearAllTimers();
  vi.useRealTimers();
});

describe("QR sign-in expiration", () => {
  it("emits an in-flight success once even after the local deadline", async () => {
    const poll = deferred<QRSignInState>();
    backend.poll.mockReturnValue(poll.promise);
    const view = await mountSignIn();
    await vi.advanceTimersByTimeAsync(1000);
    expect(backend.poll).toHaveBeenCalledExactlyOnceWith("qr-current");

    await vi.advanceTimersByTimeAsync(5000);
    expect(backend.cancel).not.toHaveBeenCalled();
    expect(view.host.textContent).not.toContain("二维码已失效");
    expect(view.host.querySelector(".qr-status small")?.textContent).toBe("00:00");
    expect(vi.getTimerCount()).toBe(0);
    poll.resolve(response({ state: "complete", result: signedIn }));
    await settle();

    expect(view.complete).toHaveBeenCalledExactlyOnceWith(signedIn);
    expect(view.host.querySelector(".status-label")?.textContent).toBe("登录成功");
    expect(view.host.querySelector(".qr-status small")).toBeNull();
    await vi.advanceTimersByTimeAsync(60000);
    expect(view.complete).toHaveBeenCalledTimes(1);
    expect(backend.poll).toHaveBeenCalledTimes(1);
    expect(backend.cancel).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each(["waiting", "scanned", "network"])("expires a late %s response without polling again", async (state) => {
    const poll = deferred<QRSignInState>();
    backend.poll.mockReturnValue(poll.promise);
    const view = await mountSignIn();
    await vi.advanceTimersByTimeAsync(1000);
    await vi.advanceTimersByTimeAsync(5000);
    expect(backend.cancel).not.toHaveBeenCalled();
    poll.resolve(response({ state }));
    await settle();

    expectExpired(view.host);
    expect(view.complete).not.toHaveBeenCalled();
    expect(backend.cancel).toHaveBeenCalledExactlyOnceWith("qr-current");
    await vi.advanceTimersByTimeAsync(60000);
    expect(backend.poll).toHaveBeenCalledTimes(1);
    expect(backend.begin).toHaveBeenCalledTimes(1);
  });

  it("expires a rejected in-flight request and permits an explicit fresh QR code", async () => {
    const poll = deferred<QRSignInState>();
    backend.poll.mockReturnValueOnce(poll.promise);
    const view = await mountSignIn();
    await vi.advanceTimersByTimeAsync(1000);
    await vi.advanceTimersByTimeAsync(5000);
    poll.reject(new Error("connection lost"));
    await settle();

    expectExpired(view.host);
    expect(view.complete).not.toHaveBeenCalled();
    expect(backend.cancel).toHaveBeenCalledExactlyOnceWith("qr-current");
    await vi.advanceTimersByTimeAsync(60000);
    expect(backend.poll).toHaveBeenCalledTimes(1);
    expect(backend.begin).toHaveBeenCalledTimes(1);

    backend.poll.mockResolvedValueOnce(response({ state: "complete", result: signedIn }));
    button(view.host, "刷新二维码").click();
    await settle();
    expect(backend.begin).toHaveBeenCalledTimes(2);
    expect(view.host.querySelector(".status-label")?.textContent).toBe("等待扫码");
    await vi.advanceTimersByTimeAsync(1000);
    expect(view.complete).toHaveBeenCalledExactlyOnceWith(signedIn);
    expect(backend.poll).toHaveBeenCalledTimes(2);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("expires without polling when the deadline precedes the first poll", async () => {
    backend.begin.mockResolvedValueOnce(response({ interval: 5 }));
    const view = await mountSignIn();
    await vi.advanceTimersByTimeAsync(6000);

    expectExpired(view.host);
    expect(backend.poll).not.toHaveBeenCalled();
    expect(backend.cancel).toHaveBeenCalledExactlyOnceWith("qr-current");
    expect(view.complete).not.toHaveBeenCalled();
  });

  it("does not refresh or schedule another retry while a network retry is unsettled", async () => {
    const poll = deferred<QRSignInState>();
    backend.poll.mockRejectedValueOnce(new Error("connection lost")).mockReturnValueOnce(poll.promise);
    const view = await mountSignIn();
    await vi.advanceTimersByTimeAsync(1000);
    expect(view.host.querySelector(".status-label")?.textContent).toBe("网络连接中断，正在重试");
    await vi.advanceTimersByTimeAsync(1000);
    expect(backend.poll).toHaveBeenCalledTimes(2);

    button(view.host, "重试连接").click();
    await vi.advanceTimersByTimeAsync(4000);
    button(view.host, "重试连接").click();
    await settle();
    expect(backend.begin).toHaveBeenCalledTimes(1);
    expect(backend.poll).toHaveBeenCalledTimes(2);
    expect(backend.cancel).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);

    poll.resolve(response({ state: "complete", result: signedIn }));
    await settle();
    expect(view.complete).toHaveBeenCalledExactlyOnceWith(signedIn);
    expect(view.host.querySelector(".network-actions")).toBeNull();
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each(["unmount", "mode switch"])("ignores a stale success after deliberate %s", async (action) => {
    const poll = deferred<QRSignInState>();
    backend.poll.mockReturnValue(poll.promise);
    const view = await mountSignIn();
    await vi.advanceTimersByTimeAsync(1000);
    if (action === "unmount") view.unmount();
    else { view.visible.value = false; await nextTick(); }
    expect(backend.cancel).toHaveBeenCalledExactlyOnceWith("qr-current");

    await vi.advanceTimersByTimeAsync(5000);
    poll.resolve(response({ state: "complete", result: signedIn }));
    await settle();
    expect(view.complete).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(60000);
    expect(backend.poll).toHaveBeenCalledTimes(1);
    expect(backend.begin).toHaveBeenCalledTimes(1);
  });

  it("keeps a deliberate successor authoritative over an old success", async () => {
    const oldPoll = deferred<QRSignInState>();
    const newPoll = deferred<QRSignInState>();
    backend.poll.mockReturnValueOnce(oldPoll.promise).mockReturnValueOnce(newPoll.promise);
    const view = await mountSignIn();
    await vi.advanceTimersByTimeAsync(1000);
    view.account.value = "successor";
    await settle();
    expect(backend.cancel).toHaveBeenCalledExactlyOnceWith("qr-current");
    expect(backend.begin).toHaveBeenLastCalledWith({ account: "successor", label: "" });
    await vi.advanceTimersByTimeAsync(1000);
    expect(backend.poll).toHaveBeenLastCalledWith("qr-successor");

    oldPoll.resolve(response({ state: "complete", result: signedIn }));
    await settle();
    expect(view.complete).not.toHaveBeenCalled();
    expect(view.host.querySelector(".status-label")?.textContent).toBe("等待扫码");
    await vi.advanceTimersByTimeAsync(6000);
    expect(backend.cancel).toHaveBeenCalledTimes(1);
    const successor = { ...signedIn, account: { ...signedIn.account, id: "successor" }, status: { ...signedIn.status, accountId: "successor" } };
    newPoll.resolve(response({ id: "qr-successor", state: "complete", result: successor }));
    await settle();

    expect(view.complete).toHaveBeenCalledExactlyOnceWith(successor);
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(60000);
    expect(backend.poll).toHaveBeenCalledTimes(2);
    expect(view.complete).toHaveBeenCalledTimes(1);
  });
});
