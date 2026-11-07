import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mountGallery } from "../../internal/lanshare/web/gallery.js";
import { translate } from "../../internal/lanshare/web/locales.js";

const entries = ["first", "middle", "last"].map(id => ({
  id, name: `${id}.png`, parentId: "folder", mime: "image/png", ready: true,
}));
const validated = (index: number) => ({ ...entries[index], source: `/validated/${entries[index].id}` });
type ReadyImage = typeof entries[number] & { source: string };
let mounted: { destroy(): void; setLanguage(language: string): void } | undefined;
let panel: HTMLDialogElement;

beforeEach(() => {
  document.body.innerHTML = '<dialog id="panel" class="image-dialog" open></dialog>';
  panel = document.getElementById("panel") as HTMLDialogElement;
});
afterEach(() => {
  mounted?.destroy();
  mounted = undefined;
  vi.restoreAllMocks();
  document.body.replaceChildren();
});
const button = (action: string) => panel.querySelector<HTMLButtonElement>(`[data-gallery-action="${action}"]`)!;
const image = () => panel.querySelector<HTMLImageElement>("img")!;
const status = () => panel.querySelector<HTMLElement>(".gallery-status")!;
const settle = async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); };
function open(extra = {}) {
  mounted = mountGallery({
    container: panel, entry: validated(0), entries, language: "en",
    onSelect: async (id: string) => validated(entries.findIndex(entry => entry.id === id)),
    onDownload: vi.fn(), ...extra,
  });
  return mounted;
}
function key(key: string, target: Element = panel, extra = {}) {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...extra });
  target.dispatchEvent(event);
  return event;
}
function touch(type: string, points: { identifier: number; clientX: number; clientY: number }[], ended = points) {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperties(event, { touches: { value: points }, changedTouches: { value: ended } });
  panel.querySelector(".gallery-stage")!.dispatchEvent(event);
  return event;
}
function swipe(dx: number, dy = 0) {
  touch("touchstart", [{ identifier: 1, clientX: 160, clientY: 100 }]);
  touch("touchmove", [{ identifier: 1, clientX: 160 + dx, clientY: 100 + dy }]);
  return touch("touchend", [], [{ identifier: 1, clientX: 160 + dx, clientY: 100 + dy }]);
}
function deferred<T>() {
  return (Promise as PromiseConstructor & {
    withResolvers<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (reason?: unknown) => void };
  }).withResolvers<T>();
}

describe("LAN ready-original image gallery", () => {
  it("loads only the selected source and never wraps at either boundary", async () => {
    const onSelect = vi.fn(async (id: string) => validated(entries.findIndex(entry => entry.id === id)));
    const onDownload = vi.fn();
    const fetch = vi.spyOn(globalThis, "fetch");
    open({ onSelect, onDownload });
    expect(panel.querySelectorAll("img")).toHaveLength(1);
    expect(image().getAttribute("src")).toBe("/validated/first");
    expect(button("previous").disabled).toBe(true);
    key("ArrowLeft");
    expect(onSelect).not.toHaveBeenCalled();
    button("next").click();
    await settle();
    expect(image().getAttribute("src")).toBe("/validated/middle");
    expect(panel.querySelector(".gallery-count")?.textContent).toBe("2 / 3");
    button("next").click();
    await settle();
    expect(image().getAttribute("src")).toBe("/validated/last");
    expect(button("next").disabled).toBe(true);
    key("ArrowRight");
    expect(onSelect.mock.calls.map(call => call[0])).toEqual(["middle", "last"]);
    expect(onDownload).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("handles an empty gallery and a single ready image", () => {
    open({ entry: null, entries: [] });
    expect(panel.querySelector("img")).toBeNull();
    expect(status().textContent).toBe(translate("en", "galleryEmpty"));
    expect(panel.querySelector(".gallery-count")?.textContent).toBe("0 / 0");
    expect(button("previous").disabled).toBe(true);
    expect(button("next").disabled).toBe(true);
    expect(button("download").disabled).toBe(true);
    mounted!.destroy();
    open({ entries: [entries[0]] });
    expect(panel.querySelector(".gallery-count")?.textContent).toBe("1 / 1");
    expect(button("previous").disabled).toBe(true);
    expect(button("next").disabled).toBe(true);
    expect(button("download").disabled).toBe(false);
  });

  it("excludes unready, nonimage, duplicate and other-folder neighbors without selecting them", async () => {
    const onSelect = vi.fn(async () => validated(2));
    open({ onSelect, entries: [entries[0], { ...entries[1], ready: false },
      { ...entries[1], id: "text", mime: "text/plain" }, { ...entries[1], parentId: "elsewhere" }, entries[0], entries[2]] });
    expect(panel.querySelector(".gallery-count")?.textContent).toBe("1 / 2");
    button("next").click();
    await settle();
    expect(onSelect).toHaveBeenCalledExactlyOnceWith("last");
    expect(image().getAttribute("src")).toBe("/validated/last");
    expect(button("next").disabled).toBe(true);
  });

  it("uses arrow keys without hijacking native editing, modified or composition keys", async () => {
    const onSelect = vi.fn(async () => validated(1));
    open({ onSelect });
    for (const tag of ["input", "textarea", "select"]) {
      const input = document.createElement(tag);
      panel.append(input);
      expect(key("ArrowRight", input).defaultPrevented).toBe(false);
    }
    const editor = document.createElement("div");
    editor.setAttribute("contenteditable", "true");
    const child = document.createElement("span");
    editor.append(child);
    panel.append(editor);
    expect(key("ArrowRight", child).defaultPrevented).toBe(false);
    expect(key("ArrowRight", panel, { ctrlKey: true }).defaultPrevented).toBe(false);
    expect(key("ArrowRight", panel, { isComposing: true }).defaultPrevented).toBe(false);
    expect(onSelect).not.toHaveBeenCalled();
    expect(key("ArrowRight").defaultPrevented).toBe(true);
    await settle();
    expect(image().getAttribute("src")).toBe("/validated/middle");
  });

  it("recognizes horizontal one-finger swipes but not short, vertical or diagonal gestures", async () => {
    const onSelect = vi.fn(async (id: string) => validated(entries.findIndex(entry => entry.id === id)));
    open({ onSelect });
    swipe(-30);
    swipe(-80, 100);
    swipe(-60, 45);
    expect(onSelect).not.toHaveBeenCalled();
    expect(swipe(-80).defaultPrevented).toBe(false);
    await settle();
    expect(image().getAttribute("src")).toBe("/validated/middle");
    swipe(80);
    await settle();
    expect(image().getAttribute("src")).toBe("/validated/first");
    swipe(80);
    expect(onSelect.mock.calls.map(call => call[0])).toEqual(["middle", "first"]);
  });

  it("does not navigate after pinch, touch cancellation or a vertical gesture that later turns horizontal", () => {
    const onSelect = vi.fn();
    open({ onSelect });
    const first = { identifier: 1, clientX: 160, clientY: 100 };
    const second = { identifier: 2, clientX: 200, clientY: 100 };
    touch("touchstart", [first]);
    touch("touchstart", [first, second]);
    touch("touchend", [], [{ ...first, clientX: 50 }]);
    touch("touchstart", [first]);
    touch("touchcancel", []);
    touch("touchend", [], [{ ...first, clientX: 50 }]);
    touch("touchstart", [first]);
    touch("touchmove", [{ ...first, clientY: 140 }]);
    touch("touchend", [], [{ ...first, clientX: 50, clientY: 140 }]);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("leaves swipes to native browser zoom while the viewport is magnified", () => {
    vi.stubGlobal("visualViewport", { scale: 2 });
    try {
      const onSelect = vi.fn();
      open({ onSelect });
      expect(swipe(-100).defaultPrevented).toBe(false);
      expect(onSelect).not.toHaveBeenCalled();
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("keeps loading state until the active image loads, and retains download after image failure", async () => {
    const onDownload = vi.fn();
    open({ onDownload });
    expect(panel.querySelector(".gallery-stage")?.getAttribute("aria-busy")).toBe("true");
    expect(image().hidden).toBe(true);
    image().dispatchEvent(new Event("load"));
    expect(image().hidden).toBe(false);
    expect(status().hidden).toBe(true);
    expect(panel.querySelector(".gallery-stage")?.getAttribute("aria-busy")).toBe("false");
    image().dispatchEvent(new Event("error"));
    expect(image().hidden).toBe(true);
    expect(status().getAttribute("role")).toBe("alert");
    expect(status().textContent).toBe(translate("en", "galleryLoadFailed"));
    expect(panel.querySelector(".gallery-stage")?.getAttribute("aria-busy")).toBe("false");
    button("download").click();
    await settle();
    expect(onDownload).toHaveBeenCalledExactlyOnceWith(validated(0));
    button("next").click();
    await settle();
    image().dispatchEvent(new Event("load"));
    expect(status().hidden).toBe(true);
  });

  it("shows supersedable validation work and ignores a slower previous selection", async () => {
    const middle = deferred<ReadyImage>();
    const last = deferred<ReadyImage>();
    const onSelect = vi.fn((id: string) => id === "middle" ? middle.promise : last.promise);
    open({ onSelect });
    const oldImage = image();
    oldImage.dispatchEvent(new Event("load"));
    button("next").click();
    expect(status().dataset.state).toBe("loading");
    expect(panel.querySelector(".gallery-stage")?.getAttribute("aria-busy")).toBe("true");
    expect(button("download").disabled).toBe(true);
    expect(button("next").disabled).toBe(false);
    button("next").click();
    last.resolve(validated(2));
    await settle();
    expect(image().getAttribute("src")).toBe("/validated/last");
    middle.resolve(validated(1));
    await settle();
    expect(image().getAttribute("src")).toBe("/validated/last");
    expect(panel.querySelector("#panel-title")?.textContent).toBe("last.png");
    oldImage.dispatchEvent(new Event("error"));
    expect(status().dataset.state).toBe("loading");
    image().dispatchEvent(new Event("load"));
    expect(status().hidden).toBe(true);
    expect(oldImage.getAttribute("src")).toBeNull();
  });

  it.each(["forbidden", "not_ready", "offline"])("keeps the current image when the next validation rejects %s", async code => {
    const onSelect = vi.fn(async () => { throw Object.assign(new Error(code), { code }); });
    const onDownload = vi.fn();
    open({ onSelect, onDownload });
    image().dispatchEvent(new Event("load"));
    button("next").click();
    await settle();
    expect(image().getAttribute("src")).toBe("/validated/first");
    expect(panel.querySelectorAll("img")).toHaveLength(1);
    expect(status().textContent).toBe(translate("en", code));
    expect(status().getAttribute("role")).toBe("alert");
    expect(panel.querySelector(".gallery-count")?.textContent).toBe("1 / 3");
    expect(button("previous").disabled).toBe(true);
    expect(onDownload).not.toHaveBeenCalled();
  });

  it.each([
    { ...validated(1), ready: false },
    { ...validated(1), source: "" },
    { ...validated(1), parentId: "elsewhere" },
    { ...validated(1), id: "unexpected" },
    { ...validated(1), mime: "text/html" },
  ])("never attaches a source for an unavailable or mismatched validated entry", async neighbor => {
    const onDownload = vi.fn();
    open({ onSelect: async () => neighbor, onDownload });
    button("next").click();
    await settle();
    expect(image().getAttribute("src")).toBe("/validated/first");
    expect(status().dataset.state).toBe("error");
    expect(onDownload).not.toHaveBeenCalled();
  });

  it("ignores null stale selection results without loading another image", async () => {
    open({ onSelect: async () => null });
    const active = image();
    button("next").click();
    await settle();
    expect(image()).toBe(active);
    expect(image().getAttribute("src")).toBe("/validated/first");
    expect(panel.querySelector(".gallery-count")?.textContent).toBe("1 / 3");
    expect(panel.querySelector("#panel-title")?.textContent).toBe("first.png");
  });

  it.each(["destroy", "close-button", "close", "cancel"])("stops pending work and all input after %s", async closing => {
    const pending = deferred<ReadyImage>();
    const onSelect = vi.fn(() => pending.promise);
    open({ onSelect });
    const oldImage = image();
    button("next").click();
    if (closing === "destroy") mounted!.destroy();
    else if (closing === "close-button") panel.querySelector<HTMLButtonElement>('[data-action="close"]')!.click();
    else panel.dispatchEvent(new Event(closing));
    const state = status().dataset.state;
    pending.resolve(validated(1));
    await settle();
    key("ArrowRight");
    swipe(-80);
    button("next").click();
    oldImage.dispatchEvent(new Event("load"));
    expect(status().dataset.state).toBe(state);
    expect(panel.querySelector("#panel-title")?.textContent).toBe("first.png");
    expect(panel.querySelector("img")).toBeNull();
    expect(oldImage.getAttribute("src")).toBeNull();
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it("renders long filenames as text and downloads the active validated original", async () => {
    const name = `${"portrait-landscape-".repeat(30)}<img src=x onerror=alert(1)>.png`;
    const selected = { ...validated(1), name };
    const onDownload = vi.fn();
    open({ onSelect: async () => selected, onDownload });
    button("next").click();
    await settle();
    expect(panel.querySelector("#panel-title")?.textContent).toBe(name);
    expect(image().alt).toBe(name);
    expect(panel.querySelectorAll("img")).toHaveLength(1);
    button("download").click();
    await settle();
    expect(onDownload).toHaveBeenCalledExactlyOnceWith(selected);
  });

});
