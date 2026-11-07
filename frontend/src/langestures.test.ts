import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mountPlayerGestures } from "../../internal/lanshare/web/gestures.js";

let stage: HTMLElement;
let media: HTMLVideoElement;
let mounted: { destroy(): void } | undefined;
const preview = vi.fn();
const commit = vi.fn();
const brightness = vi.fn();
const volume = vi.fn(() => true);
const tap = vi.fn();
const active = vi.fn();

beforeEach(() => {
  vi.useFakeTimers();
  document.body.innerHTML = '<div id="stage"><video></video><button><span>Play</span></button><input type="range"><select><option>1</option></select><a href="#">Link</a><div contenteditable="true"><span>Edit</span></div><div data-select-menu><span>Option</span></div><div role="slider"></div></div>';
  stage = document.getElementById("stage")!;
  media = stage.querySelector("video")!;
  Object.defineProperties(media, {
    duration: { configurable: true, value: 200 },
    error: { configurable: true, value: null },
  });
  media.currentTime = 50;
  media.volume = 0.5;
  vi.spyOn(stage, "getBoundingClientRect").mockReturnValue({ left: 0, top: 50, width: 400, height: 200, right: 400, bottom: 250, x: 0, y: 50, toJSON() {} });
  Object.defineProperties(stage, {
    setPointerCapture: { configurable: true, value: vi.fn() },
    hasPointerCapture: { configurable: true, value: vi.fn(() => true) },
    releasePointerCapture: { configurable: true, value: vi.fn() },
  });
  for (const callback of [preview, commit, brightness, volume, tap, active]) callback.mockClear();
  volume.mockReturnValue(true);
});
afterEach(() => {
  mounted?.destroy();
  mounted = undefined;
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  document.body.replaceChildren();
});
function open(extra = {}) {
  mounted = mountPlayerGestures({ stage, media, onPreview: preview, onCommitSeek: commit, onBrightness: brightness, getBrightness: () => 0.6, onVolume: volume, onTap: tap, onActiveChange: active, ...extra });
  return mounted;
}
function pointer(type: string, x = 100, y = 150, extra: Record<string, unknown> = {}, target: EventTarget = stage) {
  const event = new Event(type, { bubbles: true, cancelable: true });
  for (const [key, value] of Object.entries({ pointerId: 1, pointerType: "touch", isPrimary: true, clientX: x, clientY: y, button: 0, ...extra })) Object.defineProperty(event, key, { value });
  target.dispatchEvent(event);
  return event;
}
function click(x = 100, y = 150, target: Element = stage, extra = {}) {
  const event = new MouseEvent("click", { bubbles: true, cancelable: true, clientX: x, clientY: y, detail: 1, ...extra });
  target.dispatchEvent(event);
  return event;
}
function touchTap(x = 100, y = 150, extra = {}) {
  pointer("pointerdown", x, y, extra);
  pointer("pointerup", x, y, extra);
}
function rotateStage() {
  const page = document.createElement("div");
  page.className = "player-page is-rotated";
  stage.before(page);
  page.append(stage);
  vi.mocked(stage.getBoundingClientRect).mockReturnValue({ left: 50, top: 100, width: 200, height: 400, right: 250, bottom: 500, x: 50, y: 100, toJSON() {} });
  return page;
}

describe("LAN player touch and pen gestures", () => {
  it("previews horizontal seeking without changing media or committing until release", () => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 109);
    expect(preview).not.toHaveBeenCalled();
    expect(active).not.toHaveBeenCalled();
    pointer("pointermove", 200);
    expect(preview).toHaveBeenLastCalledWith({ kind: "seek", value: 60, delta: 10 });
    expect(active).toHaveBeenCalledExactlyOnceWith(true);
    expect(stage.setPointerCapture).toHaveBeenCalledExactlyOnceWith(1);
    expect(commit).not.toHaveBeenCalled();
    expect(media.currentTime).toBe(50);
    pointer("pointerup", 200);
    expect(commit).toHaveBeenCalledExactlyOnceWith(60);
    expect(active.mock.calls).toEqual([[true], [false]]);
    expect(stage.releasePointerCapture).toHaveBeenCalledExactlyOnceWith(1);
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
  });

  it("seeks along the rotated displayed horizontal axis, locks direction, and commits only on release", () => {
    rotateStage();
    open();
    pointer("pointerdown", 150, 200);
    pointer("pointermove", 150, 209);
    expect(preview).not.toHaveBeenCalled();
    pointer("pointermove", 162, 212);
    expect(active).not.toHaveBeenCalled();
    pointer("pointermove", 162, 300);
    expect(preview).toHaveBeenLastCalledWith({ kind: "seek", value: 60, delta: 10 });
    expect(active).toHaveBeenCalledExactlyOnceWith(true);
    expect(stage.setPointerCapture).toHaveBeenCalledExactlyOnceWith(1);
    pointer("pointermove", 220, 300);
    expect(preview).toHaveBeenLastCalledWith({ kind: "seek", value: 60, delta: 10 });
    expect(brightness).not.toHaveBeenCalled();
    expect(volume).not.toHaveBeenCalled();
    expect(commit).not.toHaveBeenCalled();
    expect(media.currentTime).toBe(50);
    pointer("pointerup", 220, 400);
    expect(commit).toHaveBeenCalledExactlyOnceWith(70);
    expect(active.mock.calls).toEqual([[true], [false]]);
    expect(stage.releasePointerCapture).toHaveBeenCalledExactlyOnceWith(1);
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
  });

  it.each([
    [200, "brightness", 0.85],
    [400, "volume", 0.75],
  ] as const)("uses displayed side at physical y=%s for rotated %s", (y, kind, value) => {
    rotateStage();
    open();
    pointer("pointerdown", 150, y);
    pointer("pointermove", 200, y);
    const effect = kind === "brightness" ? brightness : volume;
    const other = kind === "brightness" ? volume : brightness;
    expect(effect).toHaveBeenLastCalledWith(value);
    expect(preview).toHaveBeenLastCalledWith({ kind, value });
    pointer("pointermove", 200, y === 200 ? 400 : 200);
    expect(effect).toHaveBeenLastCalledWith(value);
    expect(other).not.toHaveBeenCalled();
    pointer("pointerup", 100, y);
    expect(effect).toHaveBeenLastCalledWith(kind === "brightness" ? 0.35 : 0.25);
    expect(commit).not.toHaveBeenCalled();
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
  });

  it.each([false, true])("normalizes tap positions with rotated=%s without changing physical click suppression", rotated => {
    if (rotated) rotateStage();
    open();
    const x = rotated ? 200 : 100;
    const y = rotated ? 200 : 100;
    touchTap(x, y);
    const compatibilityClick = new MouseEvent("click", { bubbles: true, cancelable: true, clientX: x, clientY: y });
    Object.defineProperty(compatibilityClick, "pointerType", { value: "mouse" });
    stage.dispatchEvent(compatibilityClick);
    expect(compatibilityClick.defaultPrevented).toBe(true);
    expect(click(x + 100, y + 100).defaultPrevented).toBe(false);
    vi.runAllTimers();
    expect(tap).toHaveBeenCalledExactlyOnceWith({ x: 0.25, y: 0.25, double: false });
    pointer("pointerdown", x, y, { pointerType: "mouse" });
    expect(click(x, y).defaultPrevented).toBe(false);
  });

  it.each([39, 41])("keeps the rotated double-tap distance in pixels at distance %s", distance => {
    rotateStage();
    open();
    touchTap(200, 200);
    vi.advanceTimersByTime(100);
    touchTap(200, 200 + distance);
    const point = { x: (100 + distance) / 400, y: 0.25 };
    if (distance <= 40) {
      expect(tap).toHaveBeenCalledExactlyOnceWith({ ...point, double: true });
    } else {
      expect(tap).toHaveBeenCalledExactlyOnceWith({ x: 0.25, y: 0.25, double: false });
    }
    vi.runAllTimers();
    expect(tap.mock.calls).toEqual(distance <= 40
      ? [[{ ...point, double: true }]]
      : [[{ x: 0.25, y: 0.25, double: false }], [{ ...point, double: false }]]);
  });

  it("reserves physical screen edges when rotated, but accepts displayed and inset stage edges", () => {
    rotateStage();
    open();
    for (const x of [0, 16, window.innerWidth - 16, window.innerWidth]) {
      pointer("pointerdown", x, 200);
      pointer("pointermove", x, 300);
      pointer("pointerup", x, 300);
    }
    vi.runAllTimers();
    expect(commit).not.toHaveBeenCalled();
    expect(tap).not.toHaveBeenCalled();
    touchTap(150, 101);
    vi.runAllTimers();
    touchTap(51, 300);
    vi.runAllTimers();
    expect(tap.mock.calls).toEqual([
      [{ x: 0.0025, y: 0.5, double: false }],
      [{ x: 0.5, y: 0.995, double: false }],
    ]);
  });

  it.each(["resize", "orientationchange", "playerrotationchange"])("cancels rotated seek and pending taps on %s before new geometry is used", type => {
    const page = rotateStage();
    open();
    touchTap(200, 200);
    window.dispatchEvent(new Event(type));
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
    pointer("pointerdown", 150, 200);
    pointer("pointermove", 150, 300);
    expect(preview).toHaveBeenLastCalledWith({ kind: "seek", value: 60, delta: 10 });
    window.dispatchEvent(new Event(type));
    page.classList.remove("is-rotated");
    pointer("pointerup", 150, 300);
    vi.runAllTimers();
    expect(commit).not.toHaveBeenCalled();
    expect(tap).not.toHaveBeenCalled();
    expect(preview).toHaveBeenLastCalledWith(null);
    expect(active.mock.calls).toEqual([[true], [false]]);
    touchTap(100, 200);
    vi.runAllTimers();
    expect(tap).toHaveBeenCalledExactlyOnceWith({ x: 0.25, y: 0.25, double: false });
  });

  it.each(["resize", "orientationchange", "playerrotationchange"])("cancels a below-threshold contact on %s without turning its release into a tap", type => {
    rotateStage();
    open();
    pointer("pointerdown", 150, 200);
    window.dispatchEvent(new Event(type));
    pointer("pointerup", 150, 200);
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
    expect(commit).not.toHaveBeenCalled();
  });

  it.each(["visual-viewport", "screen-orientation", "fullscreen", "webkit-fullscreen"])("cancels pending taps and active gestures on %s geometry changes", source => {
    const target = new EventTarget();
    if (source === "visual-viewport") vi.stubGlobal("visualViewport", target);
    if (source === "screen-orientation") vi.stubGlobal("screen", { orientation: target });
    const dispatchChange = () => {
      if (source === "visual-viewport") target.dispatchEvent(new Event("resize"));
      if (source === "screen-orientation") target.dispatchEvent(new Event("change"));
      if (source === "fullscreen") document.dispatchEvent(new Event("fullscreenchange"));
      if (source === "webkit-fullscreen") document.dispatchEvent(new Event("webkitfullscreenchange"));
    };
    rotateStage();
    open();
    touchTap(200, 200);
    dispatchChange();
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
    pointer("pointerdown", 150, 200);
    pointer("pointermove", 150, 300);
    dispatchChange();
    pointer("pointerup", 150, 300);
    expect(commit).not.toHaveBeenCalled();
    expect(active.mock.calls).toEqual([[true], [false]]);
    expect(preview).toHaveBeenLastCalledWith(null);
    mounted!.destroy();
    preview.mockClear();
    dispatchChange();
    window.dispatchEvent(new Event("resize"));
    window.dispatchEvent(new Event("orientationchange"));
    window.dispatchEvent(new Event("playerrotationchange"));
    expect(preview).not.toHaveBeenCalled();
  });

  it("uses the final release position and a snapshot of duration and starting time", () => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 200);
    media.currentTime = 80;
    Object.defineProperty(media, "duration", { configurable: true, value: 400 });
    pointer("pointerup", 300);
    expect(commit).toHaveBeenCalledExactlyOnceWith(70);
  });

  it.each([
    [5, 2, -400, 0],
    [5, 2, 500, 5],
    [10000, 5000, 500, 5120],
  ])("clamps duration %s/start %s after a full-width drag", (duration, start, end, expected) => {
    Object.defineProperty(media, "duration", { configurable: true, value: duration });
    media.currentTime = start;
    open();
    pointer("pointerdown");
    pointer("pointermove", end);
    pointer("pointerup", end);
    expect(commit).toHaveBeenCalledExactlyOnceWith(expected);
    expect(Number.isFinite(commit.mock.calls[0][0])).toBe(true);
  });

  it.each([NaN, Infinity, 0, -1])("does not seek or report a tap with duration %s", duration => {
    Object.defineProperty(media, "duration", { configurable: true, value: duration });
    open();
    pointer("pointerdown");
    pointer("pointermove", 200);
    pointer("pointerup", 200);
    vi.runAllTimers();
    expect(commit).not.toHaveBeenCalled();
    expect(tap).not.toHaveBeenCalled();
    expect(preview.mock.calls.some(call => call[0]?.kind === "seek")).toBe(false);
    expect(active).not.toHaveBeenCalled();
  });

  it("does not seek or tap when media errors, including an error arriving during a drag", () => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 200);
    Object.defineProperty(media, "error", { configurable: true, value: { code: 3 } });
    pointer("pointerup", 200);
    touchTap();
    vi.runAllTimers();
    expect(commit).not.toHaveBeenCalled();
    expect(tap).not.toHaveBeenCalled();
    expect(preview).toHaveBeenLastCalledWith(null);
    expect(active.mock.calls).toEqual([[true], [false]]);
  });

  it("does not commit a duration that becomes unknown before release", () => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 200);
    Object.defineProperty(media, "duration", { configurable: true, value: Infinity });
    pointer("pointerup", 200);
    expect(commit).not.toHaveBeenCalled();
  });

  it("waits for axis dominance, then locks to that axis", () => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 112, 162);
    expect(active).not.toHaveBeenCalled();
    pointer("pointermove", 150, 162);
    expect(preview).toHaveBeenLastCalledWith({ kind: "seek", value: 55, delta: 5 });
    pointer("pointermove", 150, 230);
    expect(preview).toHaveBeenLastCalledWith({ kind: "seek", value: 55, delta: 5 });
    expect(brightness).not.toHaveBeenCalled();
    pointer("pointerup", 150, 230);
    expect(commit).toHaveBeenCalledExactlyOnceWith(55);
  });

  it("never reinterprets an ambiguous drag as a tap", () => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 112, 162);
    pointer("pointerup", 100, 150);
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
    expect(commit).not.toHaveBeenCalled();
  });

  it("adjusts left-side brightness continuously within 0.2–1 and keeps the first-down side", () => {
    open();
    pointer("pointerdown", 100);
    pointer("pointermove", 100, 100);
    expect(brightness).toHaveBeenLastCalledWith(0.85);
    expect(preview).toHaveBeenLastCalledWith({ kind: "brightness", value: 0.85 });
    pointer("pointermove", 300, -100);
    expect(brightness).toHaveBeenLastCalledWith(1);
    pointer("pointermove", 300, 500);
    expect(brightness).toHaveBeenLastCalledWith(0.2);
    pointer("pointerup", 300, 500);
    expect(volume).not.toHaveBeenCalled();
    expect(commit).not.toHaveBeenCalled();
  });

  it("adjusts right-side actual volume and clamps both ends", () => {
    open();
    pointer("pointerdown", 300);
    pointer("pointermove", 300, 100);
    expect(volume).toHaveBeenLastCalledWith(0.75);
    expect(preview).toHaveBeenLastCalledWith({ kind: "volume", value: 0.75 });
    pointer("pointermove", 300, -100);
    expect(volume).toHaveBeenLastCalledWith(1);
    pointer("pointermove", 300, 500);
    expect(volume).toHaveBeenLastCalledWith(0);
    pointer("pointerup", 300, 500);
    expect(brightness).not.toHaveBeenCalled();
  });

  it("reports system volume without a fake percentage when actual volume is unsupported", () => {
    volume.mockReturnValue(false);
    open();
    pointer("pointerdown", 300);
    pointer("pointermove", 300, 100);
    expect(volume).toHaveBeenCalledExactlyOnceWith(0.75);
    expect(preview).toHaveBeenLastCalledWith({ kind: "system-volume" });
    expect(media.volume).toBe(0.5);
  });

  it.each(["pointercancel", "lostpointercapture"])("cancels on %s without committing or leaving active feedback", type => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 200);
    pointer(type, 200);
    pointer("pointerup", 200);
    vi.runAllTimers();
    expect(commit).not.toHaveBeenCalled();
    expect(tap).not.toHaveBeenCalled();
    expect(preview).toHaveBeenLastCalledWith(null);
    expect(active.mock.calls).toEqual([[true], [false]]);
  });

  it.each([false, true])("cancels a second pointer inside or outside stage (%s) without preventing pinch", outside => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 200);
    const second = pointer("pointerdown", 300, 150, { pointerId: 2, isPrimary: false }, outside ? document.body : stage);
    expect(second.defaultPrevented).toBe(false);
    pointer("pointermove", 250);
    pointer("pointerup", 250);
    pointer("pointerup", 300, 150, { pointerId: 2, isPrimary: false });
    vi.runAllTimers();
    expect(commit).not.toHaveBeenCalled();
    expect(tap).not.toHaveBeenCalled();
    expect(preview).toHaveBeenLastCalledWith(null);
    expect(active.mock.calls).toEqual([[true], [false]]);
  });

  it("tracks a drag released outside the stage even when capture is unavailable", () => {
    Object.defineProperty(stage, "setPointerCapture", { value: vi.fn(() => { throw new DOMException("Unavailable"); }) });
    open();
    pointer("pointerdown");
    pointer("pointermove", 200);
    pointer("pointermove", 500, 150, {}, document.body);
    pointer("pointerup", 500, 150, {}, document.body);
    expect(commit).toHaveBeenCalledExactlyOnceWith(90);
    expect(active.mock.calls).toEqual([[true], [false]]);
  });

  it("does not tap after a below-threshold release outside the stage", () => {
    open();
    pointer("pointerdown", 100, 52);
    pointer("pointerup", 100, 48, {}, document.body);
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
  });

  it("delays one touch tap, consumes its compatibility click, and reports normalized coordinates", () => {
    open();
    const parentClick = vi.fn();
    stage.addEventListener("click", parentClick);
    touchTap(100, 100);
    expect(tap).not.toHaveBeenCalled();
    expect(click(100, 100).defaultPrevented).toBe(true);
    expect(parentClick).not.toHaveBeenCalled();
    vi.advanceTimersByTime(279);
    expect(tap).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(tap).toHaveBeenCalledExactlyOnceWith({ x: 0.25, y: 0.25, double: false });
    vi.runAllTimers();
    expect(tap).toHaveBeenCalledTimes(1);
  });

  it.each([80, 200, 320])("reports a double tap at x=%s only once without running the pending single tap", x => {
    open();
    touchTap(x);
    expect(click(x).defaultPrevented).toBe(true);
    vi.advanceTimersByTime(200);
    touchTap(x + 2);
    expect(click(x + 2).defaultPrevented).toBe(true);
    expect(tap).toHaveBeenCalledExactlyOnceWith({ x: (x + 2) / 400, y: 0.5, double: true });
    vi.runAllTimers();
    expect(tap).toHaveBeenCalledTimes(1);
    expect(commit).not.toHaveBeenCalled();
  });

  it("keeps distant taps separate rather than interpreting them as a double", () => {
    open();
    touchTap(80);
    vi.advanceTimersByTime(100);
    touchTap(320);
    vi.runAllTimers();
    expect(tap.mock.calls).toEqual([[{ x: 0.2, y: 0.5, double: false }], [{ x: 0.8, y: 0.5, double: false }]]);
  });

  it("consumes WebKit's mouse-labelled compatibility click after touch, but allows a real mouse press", () => {
    open();
    touchTap(100, 150);
    const compatibilityClick = new MouseEvent('click', { bubbles: true, cancelable: true, clientX: 100, clientY: 150 });
    Object.defineProperty(compatibilityClick, 'pointerType', { value: 'mouse' });
    stage.dispatchEvent(compatibilityClick);
    expect(compatibilityClick.defaultPrevented).toBe(true);
    pointer('pointerdown', 100, 150, { pointerType: 'mouse' });
    const realClick = new MouseEvent('click', { bubbles: true, cancelable: true, clientX: 100, clientY: 150 });
    Object.defineProperty(realClick, 'pointerType', { value: 'mouse' });
    stage.dispatchEvent(realClick);
    expect(realClick.defaultPrevented).toBe(false);
  });

  it("reports pen taps immediately while leaving mouse pointer input and click untouched", () => {
    open();
    touchTap(100, 150, { pointerType: "pen" });
    expect(tap).toHaveBeenCalledExactlyOnceWith({ x: 0.25, y: 0.5, double: false });
    pointer("pointerdown", 100, 150, { pointerType: "mouse" });
    pointer("pointermove", 200, 150, { pointerType: "mouse" });
    pointer("pointerup", 200, 150, { pointerType: "mouse" });
    expect(click(100).defaultPrevented).toBe(false);
    expect(active).not.toHaveBeenCalled();
    expect(commit).not.toHaveBeenCalled();
    vi.runAllTimers();
    expect(tap).toHaveBeenCalledTimes(1);
  });

  it("suppresses the drag's synthetic click but not buttons, ranges, menus or native editing", () => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 200);
    pointer("pointerup", 200);
    expect(click(200).defaultPrevented).toBe(true);
    expect(click(200, 150, stage, { detail: 0 }).defaultPrevented).toBe(true);
    for (const selector of ["button span", "input", "select", "a", "[contenteditable] span", "[data-select-menu] span", '[role="slider"]']) {
      const target = stage.querySelector(selector)!;
      const nativeClick = vi.fn();
      target.addEventListener("click", nativeClick);
      expect(pointer("pointerdown", 200, 150, {}, target).defaultPrevented).toBe(false);
      expect(pointer("pointermove", 300, 100, {}, target).defaultPrevented).toBe(false);
      pointer("pointerup", 300, 100, {}, target);
      expect(click(200, 150, target).defaultPrevented).toBe(false);
      expect(nativeClick).toHaveBeenCalledTimes(1);
    }
    vi.runAllTimers();
    expect(commit).toHaveBeenCalledTimes(1);
    expect(tap).not.toHaveBeenCalled();
    expect(brightness).not.toHaveBeenCalled();
    expect(volume).not.toHaveBeenCalled();
  });

  it("reserves physical screen edges, but does not exclude an inset stage's edge", () => {
    open();
    for (const x of [0, 16, window.innerWidth - 16, window.innerWidth]) {
      pointer("pointerdown", x);
      pointer("pointermove", 200);
      pointer("pointerup", 200);
    }
    vi.runAllTimers();
    expect(commit).not.toHaveBeenCalled();
    expect(tap).not.toHaveBeenCalled();
    vi.mocked(stage.getBoundingClientRect).mockReturnValue({ left: 100, top: 50, width: 400, height: 200, right: 500, bottom: 250, x: 100, y: 50, toJSON() {} });
    touchTap(101);
    vi.runAllTimers();
    expect(tap).toHaveBeenCalledExactlyOnceWith({ x: 0.0025, y: 0.5, double: false });
  });

  it.each(["cancel", "multitouch", "destroy", "abort"])("clears a pending single tap on %s", action => {
    const controller = new AbortController();
    open({ signal: controller.signal });
    touchTap();
    if (action === "cancel") pointer("pointercancel");
    if (action === "multitouch") {
      pointer("pointerdown");
      pointer("pointerdown", 200, 150, { pointerId: 2, isPrimary: false });
      pointer("pointerup");
      pointer("pointerup", 200, 150, { pointerId: 2, isPrimary: false });
    }
    if (action === "destroy") mounted!.destroy();
    if (action === "abort") controller.abort();
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
  });

  it("cancels a drag and removes listeners on destroy, with no late commits or click interception", () => {
    open();
    pointer("pointerdown");
    pointer("pointermove", 200);
    mounted!.destroy();
    mounted!.destroy();
    pointer("pointerup", 200);
    touchTap();
    vi.runAllTimers();
    expect(commit).not.toHaveBeenCalled();
    expect(tap).not.toHaveBeenCalled();
    expect(preview).toHaveBeenLastCalledWith(null);
    expect(active.mock.calls).toEqual([[true], [false]]);
    expect(click(200).defaultPrevented).toBe(false);
  });

  it("does not register a recognizer when its signal is already aborted", () => {
    const controller = new AbortController();
    controller.abort();
    open({ signal: controller.signal });
    touchTap();
    vi.runAllTimers();
    expect(tap).not.toHaveBeenCalled();
    expect(click().defaultPrevented).toBe(false);
  });
});
