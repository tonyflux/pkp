import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const listeners: [string, EventListenerOrEventListenerObject, boolean | AddEventListenerOptions | undefined][] = [];
const windowListeners: typeof listeners = [];
let hint = "ja";
let saved: string | null = null;
let browserLanguages = ["en-US"];

async function launch() {
  document.body.innerHTML = '<div id="app"></div><dialog id="panel"></dialog><div id="notice" hidden></div>';
  if (saved === null) localStorage.removeItem("pkp-lan-language");
  else localStorage.setItem("pkp-lan-language", saved);
  Object.defineProperty(navigator, "languages", { configurable: true, value: browserLanguages });
  history.replaceState(null, "", hint ? `/?lang=${encodeURIComponent(hint)}` : "/");
  await import("../../internal/lanshare/web/app.js");
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

beforeEach(() => {
  vi.useFakeTimers(); vi.resetModules();
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ authenticated: false }), { headers: { "Content-Type": "application/json" } })));
  const addDocument = document.addEventListener.bind(document), addWindow = window.addEventListener.bind(window);
  vi.spyOn(document, "addEventListener").mockImplementation((type, listener, options) => { listeners.push([type, listener, options]); addDocument(type, listener, options); });
  vi.spyOn(window, "addEventListener").mockImplementation((type, listener, options) => { windowListeners.push([type, listener, options]); addWindow(type, listener, options); });
});

afterEach(() => {
  for (const [type, listener, options] of listeners.splice(0)) document.removeEventListener(type, listener, options);
  for (const [type, listener, options] of windowListeners.splice(0)) window.removeEventListener(type, listener, options);
  vi.clearAllTimers(); vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks();
  Reflect.deleteProperty(navigator, "languages"); document.body.replaceChildren();
  hint = "ja"; saved = null; browserLanguages = ["en-US"];
});

describe("LAN QR language on visitor startup", () => {
  it.each(["zh-Hans", "zh-Hant", "en", "ja", "ko"])("opens first-time visitors in %s", async language => {
    hint = language; await launch();
    expect(document.documentElement.lang).toBe(language);
    expect(document.querySelector<HTMLInputElement>("#password")).not.toBeNull();
  });
  it("honors a saved visitor choice ahead of the QR hint", async () => {
    hint = "ja"; saved = "ko"; await launch();
    expect(document.documentElement.lang).toBe("ko");
  });
  it("falls back safely for invalid or absent hints", async () => {
    hint = "constructor"; browserLanguages = ["zh-CN"]; await launch();
    expect(document.documentElement.lang).toBe("zh-Hans");
  });
});

describe("LAN visitor password visibility", () => {
  it("toggles without changing the password or submitting the form", async () => {
    hint = "en"; await launch();
    const password = document.querySelector<HTMLInputElement>("#password")!;
    const eye = document.querySelector<HTMLButtonElement>(".password-eye")!;
    password.value = "not-a-real-password";
    expect(eye.getAttribute("aria-label")).toBe("Show password");
    eye.click();
    expect(password.type).toBe("text");
    expect(password.value).toBe("not-a-real-password");
    expect(eye.getAttribute("aria-label")).toBe("Hide password");
    expect(eye.getAttribute("aria-pressed")).toBe("true");
    eye.click();
    expect(password.type).toBe("password");
    expect(password.value).toBe("not-a-real-password");
    expect(eye.getAttribute("aria-pressed")).toBe("false");
    expect(vi.mocked(fetch)).not.toHaveBeenCalledWith("/api/login", expect.anything());
  });
});

describe('LAN installed window guidance', () => {
  it('does not prompt installation inside a standalone display', async () => {
    vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ matches: query === '(display-mode: standalone)', addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList);
    await launch();
    expect(document.querySelector<HTMLElement>('[data-install-entry]')!.hidden).toBe(true);
  });
  it('honors the iOS standalone flag even when the display media query is unavailable', async () => {
    Object.defineProperty(navigator, 'standalone', { configurable: true, value: true });
    try {
      await launch();
      expect(document.querySelector<HTMLElement>('[data-install-entry]')!.hidden).toBe(true);
    } finally { Reflect.deleteProperty(navigator, 'standalone'); }
  });
});
