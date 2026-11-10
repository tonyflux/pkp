import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";
import { mountPlayer } from "../../internal/lanshare/web/player.js";

let mounted: { destroy(): void } | undefined;
let play: MockInstance;

beforeEach(() => {
  document.body.innerHTML = '<section id="player-test"></section>';
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  play = vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (this: HTMLMediaElement) {
    Object.defineProperty(this, "paused", { configurable: true, value: false });
    this.dispatchEvent(new Event("play"));
    this.dispatchEvent(new Event("playing"));
    return Promise.resolve();
  });
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(function (this: HTMLMediaElement) {
    Object.defineProperty(this, "paused", { configurable: true, value: true });
    this.dispatchEvent(new Event("pause"));
  });
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
});

afterEach(() => {
  mounted?.destroy();
  mounted = undefined;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  document.body.replaceChildren();
});

function open(extra = {}, metadataReady = true) {
  mounted = mountPlayer({
    container: document.getElementById("player-test")!,
    entry: { id: "film", name: "Film.mp4", mime: "video/mp4", size: 1024 },
    source: "/api/content?space=one&id=film&variant=original",
    language: "en",
    ...extra,
  });
  const media = document.querySelector<HTMLMediaElement>("video,audio")!;
  if (metadataReady) {
    Object.defineProperties(media, {
      duration: { configurable: true, value: 60 },
      readyState: { configurable: true, value: 4 },
    });
    media.dispatchEvent(new Event("loadedmetadata"));
  }
  return media;
}

const click = (action: string) => (document.querySelector(`.player-transport [data-player-action="${action}"]`) as HTMLButtonElement).click();
const settle = async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); };

describe("LAN player consumer boundaries", () => {
  it("clamps skip controls at both ends and never seeks an unknown duration", () => {
    const media = open();
    media.currentTime = 4;
    click("backward");
    expect(media.currentTime).toBe(0);
    media.currentTime = 57;
    click("forward");
    expect(media.currentTime).toBe(60);
    Object.defineProperty(media, "duration", { configurable: true, value: Infinity });
    media.dispatchEvent(new Event("durationchange"));
    expect((document.querySelector(".player-seek") as HTMLInputElement).disabled).toBe(true);
    document.getElementById("player-test")!.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }));
    expect(media.currentTime).toBe(60);
  });

  it("keeps native input keys and changes the actual playback rate", () => {
    const media = open();
    media.currentTime = 20;
    const seek = document.querySelector(".player-seek") as HTMLInputElement;
    seek.focus();
    const key = new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true });
    seek.dispatchEvent(key);
    expect(key.defaultPrevented).toBe(false);
    expect(media.currentTime).toBe(20);
    const speed = document.querySelector<HTMLButtonElement>(".player-speed .select-face")!;
    speed.click();
    document.querySelector<HTMLButtonElement>('.player-speed [data-select-option="1.5"]')!.click();
    expect(media.playbackRate).toBe(1.5);
    expect(speed.getAttribute("aria-expanded")).toBe("false");
    expect(speed.textContent).toContain("1.5×");
    media.playbackRate = 0.75;
    media.dispatchEvent(new Event("ratechange"));
    expect(speed.textContent).toContain("0.75×");
  });

  it("lets users start playback after the browser rejects autoplay", async () => {
    play.mockRejectedValueOnce(new DOMException("User gesture required", "NotAllowedError"));
    const onError = vi.fn();
    const media = open({ onError });
    await settle();
    expect((document.querySelector(".player-status") as HTMLElement).dataset.state).toBe("gesture");
    expect(document.querySelector(".player-status-text")?.getAttribute("role")).toBe("status");
    expect(document.querySelector(".player-stage")?.getAttribute("aria-busy")).toBe("false");
    expect(onError).not.toHaveBeenCalled();
    click("play");
    await settle();
    expect(media.paused).toBe(false);
    expect(document.querySelector(".player-page")?.classList.contains("is-playing")).toBe(true);
  });

  it("keeps initial loading through waiting and metadata until a frame or playback is available", async () => {
    play.mockImplementationOnce(function (this: HTMLMediaElement) {
      Object.defineProperty(this, "paused", { configurable: true, value: false });
      this.dispatchEvent(new Event("play"));
      return Promise.resolve();
    });
    const media = open({}, false);
    const status = document.querySelector<HTMLElement>(".player-status")!;
    const text = status.querySelector<HTMLElement>(".player-status-text")!;
    const spinner = status.querySelector(".player-status-spinner")!;
    const stage = document.querySelector(".player-stage")!;
    const changes = new MutationObserver(() => {});
    changes.observe(text, { childList: true, characterData: true, subtree: true });
    expect(text.getAttribute("role")).toBe("status");
    expect(text.getAttribute("aria-live")).toBe("polite");
    expect(spinner.getAttribute("aria-hidden")).toBe("true");
    for (const event of ["loadstart", "play", "waiting", "stalled"]) {
      media.dispatchEvent(new Event(event));
      expect(status.dataset.state).toBe("loading");
      expect(stage.getAttribute("aria-busy")).toBe("true");
    }
    Object.defineProperty(media, "readyState", { configurable: true, value: 1 });
    for (const event of ["loadedmetadata", "durationchange", "waiting"]) {
      media.dispatchEvent(new Event(event));
      expect(status.dataset.state).toBe("loading");
    }
    Object.defineProperty(media, "readyState", { configurable: true, value: 2 });
    media.dispatchEvent(new Event("loadedmetadata"));
    expect(status.dataset.state).toBe("loading");
    expect(changes.takeRecords()).toHaveLength(0);
    changes.disconnect();
    expect(status.querySelector(".player-status-spinner")).toBe(spinner);
    Object.defineProperty(media, "readyState", { configurable: true, value: 2 });
    media.dispatchEvent(new Event("playing"));
    await settle();
    expect(status.dataset.state).toBe("playing");
    expect(status.hidden).toBe(true);
    expect(stage.getAttribute("aria-busy")).toBe("false");
    for (const readiness of [1, 2]) {
      Object.defineProperty(media, "readyState", { configurable: true, value: readiness });
      media.dispatchEvent(new Event("waiting"));
      expect(status.dataset.state).toBe("buffering");
      media.dispatchEvent(new Event("loadedmetadata"));
      expect(status.dataset.state).toBe("buffering");
      expect(stage.getAttribute("aria-busy")).toBe("true");
    }
    media.dispatchEvent(new Event("canplay"));
    expect(status.dataset.state).toBe("playing");
    expect(stage.getAttribute("aria-busy")).toBe("false");
    expect(status.querySelector(".player-status-spinner")).toBe(spinner);
  });

  it("accepts first-frame availability without waiting for the playing event", () => {
    play.mockImplementationOnce(function (this: HTMLMediaElement) {
      Object.defineProperty(this, "paused", { configurable: true, value: false });
      this.dispatchEvent(new Event("play"));
      return Promise.resolve();
    });
    const media = open({}, false);
    media.dispatchEvent(new Event("waiting"));
    Object.defineProperty(media, "readyState", { configurable: true, value: 2 });
    media.dispatchEvent(new Event("loadeddata"));
    expect(document.querySelector<HTMLElement>(".player-status")!.dataset.state).toBe("playing");
    expect(document.querySelector(".player-stage")?.getAttribute("aria-busy")).toBe("false");
  });

  it.each(["video/mp4", "audio/mpeg"])("recovers %s buffering only when fallback playback time advances", mime => {
    const media = open({ entry: { id: "film", name: "Media", mime, size: 1024 } });
    Object.assign(media, { requestVideoFrameCallback: undefined });
    const status = document.querySelector<HTMLElement>(".player-status")!;
    const stage = document.querySelector(".player-stage")!;
    media.currentTime = 10;
    media.dispatchEvent(new Event("waiting"));
    for (let index = 0; index < 3; index++) media.dispatchEvent(new Event("timeupdate"));
    expect(status.dataset.state).toBe("buffering");
    expect(stage.getAttribute("aria-busy")).toBe("true");
    Object.defineProperty(media, "readyState", { configurable: true, value: 2 });
    media.currentTime = 10.25;
    media.dispatchEvent(new Event("timeupdate"));
    expect(status.dataset.state).toBe("buffering");
    Object.defineProperty(media, "readyState", { configurable: true, value: 3 });
    media.currentTime = 10.5;
    media.dispatchEvent(new Event("timeupdate"));
    expect(status.hidden).toBe(true);
    expect(stage.getAttribute("aria-busy")).toBe("false");
    media.dispatchEvent(new Event("stalled"));
    expect(status.dataset.state).toBe("buffering");
    media.currentTime = 10.75;
    media.dispatchEvent(new Event("timeupdate"));
    expect(status.dataset.state).toBe("playing");
  });

  it("does not mistake seek jumps, metadata, pause or error for fallback recovery", async () => {
    const media = open();
    Object.assign(media, { requestVideoFrameCallback: undefined });
    const status = document.querySelector<HTMLElement>(".player-status")!;
    const stage = document.querySelector(".player-stage")!;
    media.currentTime = 10;
    media.dispatchEvent(new Event("waiting"));
    Object.defineProperty(media, "seeking", { configurable: true, value: true });
    media.dispatchEvent(new Event("seeking"));
    media.currentTime = 30;
    media.dispatchEvent(new Event("timeupdate"));
    Object.defineProperty(media, "seeking", { configurable: true, value: false });
    media.dispatchEvent(new Event("seeked"));
    media.dispatchEvent(new Event("loadedmetadata"));
    media.dispatchEvent(new Event("timeupdate"));
    expect(status.dataset.state).toBe("buffering");
    expect(stage.getAttribute("aria-busy")).toBe("true");
    media.pause();
    media.currentTime = 31;
    media.dispatchEvent(new Event("timeupdate"));
    expect(status.dataset.state).toBe("paused");
    Object.defineProperty(media, "paused", { configurable: true, value: false });
    media.dispatchEvent(new Event("play"));
    media.dispatchEvent(new Event("timeupdate"));
    expect(status.dataset.state).toBe("buffering");
    media.currentTime = 31.25;
    media.dispatchEvent(new Event("timeupdate"));
    expect(status.dataset.state).toBe("playing");
    media.dispatchEvent(new Event("waiting"));
    Object.defineProperty(media, "error", { configurable: true, value: { code: 3 } });
    media.dispatchEvent(new Event("error"));
    media.currentTime = 32;
    media.dispatchEvent(new Event("timeupdate"));
    await settle();
    expect(status.dataset.state).toBe("error");
    expect(stage.getAttribute("aria-busy")).toBe("false");
  });

  it("recovers only on advancing presented frames and discards callbacks across seeks and replacement", () => {
    const media = open();
    const callbacks = new Map<number, VideoFrameRequestCallback>();
    let nextFrame = 0;
    Object.assign(media, {
      requestVideoFrameCallback: (callback: VideoFrameRequestCallback) => { callbacks.set(++nextFrame, callback); return nextFrame; },
      cancelVideoFrameCallback: (id: number) => { callbacks.delete(id); },
    });
    const present = (mediaTime: number) => {
      const [id, callback] = callbacks.entries().next().value!;
      callbacks.delete(id);
      callback(0, { mediaTime } as VideoFrameCallbackMetadata);
    };
    const status = document.querySelector<HTMLElement>(".player-status")!;
    const stage = document.querySelector(".player-stage")!;
    media.currentTime = 10;
    media.dispatchEvent(new Event("waiting"));
    present(10);
    present(10);
    media.currentTime = 10.5;
    media.dispatchEvent(new Event("timeupdate"));
    expect(status.dataset.state).toBe("buffering");
    present(10.04);
    expect(status.hidden).toBe(true);
    expect(stage.getAttribute("aria-busy")).toBe("false");
    expect(callbacks.size).toBe(0);
    media.dispatchEvent(new Event("stalled"));
    present(10.04);
    const staleSeek = callbacks.values().next().value!;
    Object.defineProperty(media, "seeking", { configurable: true, value: true });
    media.dispatchEvent(new Event("seeking"));
    expect(callbacks.size).toBe(0);
    media.currentTime = 30;
    Object.defineProperty(media, "seeking", { configurable: true, value: false });
    media.dispatchEvent(new Event("seeked"));
    staleSeek(0, { mediaTime: 30 } as VideoFrameCallbackMetadata);
    present(30);
    expect(status.dataset.state).toBe("buffering");
    present(30.04);
    for (const event of ["loadstart", "emptied"]) {
      media.dispatchEvent(new Event(event));
      media.currentTime = 0;
      media.dispatchEvent(new Event("timeupdate"));
      expect(status.dataset.state).toBe("loading");
      present(0);
      expect(status.dataset.state).toBe("loading");
      present(0.04);
      expect(status.dataset.state).toBe("playing");
    }
    media.dispatchEvent(new Event("waiting"));
    media.pause();
    expect(callbacks.size).toBe(0);
    expect(status.dataset.state).toBe("paused");
    Object.defineProperty(media, "paused", { configurable: true, value: false });
    media.dispatchEvent(new Event("play"));
    expect(status.dataset.state).toBe("buffering");
    Object.defineProperty(media, "ended", { configurable: true, value: true });
    media.dispatchEvent(new Event("ended"));
    expect(callbacks.size).toBe(0);
    expect(status.dataset.state).toBe("ended");
    Object.defineProperty(media, "ended", { configurable: true, value: false });
    media.dispatchEvent(new Event("play"));
    const staleDestroy = callbacks.values().next().value!;
    Object.defineProperty(media, "error", { configurable: true, value: { code: 3 } });
    media.dispatchEvent(new Event("error"));
    expect(callbacks.size).toBe(0);
    expect(status.dataset.state).toBe("error");
    mounted!.destroy();
    expect(callbacks.size).toBe(0);
    const current = open();
    current.dispatchEvent(new Event("waiting"));
    staleDestroy(0, { mediaTime: 31 } as VideoFrameCallbackMetadata);
    expect(status.isConnected).toBe(false);
    expect(document.querySelector<HTMLElement>(".player-status")!.dataset.state).toBe("buffering");
    expect(document.querySelector(".player-stage")?.getAttribute("aria-busy")).toBe("true");
  });

  it("stops busy feedback for explicit pause, gesture, ended and error, and recovers", async () => {
    play.mockImplementationOnce(function (this: HTMLMediaElement) {
      Object.defineProperty(this, "paused", { configurable: true, value: false });
      this.dispatchEvent(new Event("play"));
      return Promise.resolve();
    });
    const media = open({}, false);
    const status = document.querySelector<HTMLElement>(".player-status")!;
    const stage = document.querySelector(".player-stage")!;
    media.pause();
    expect(status.dataset.state).toBe("paused");
    expect(stage.getAttribute("aria-busy")).toBe("false");
    media.dispatchEvent(new Event("loadstart"));
    expect(status.dataset.state).toBe("paused");
    expect(stage.getAttribute("aria-busy")).toBe("false");
    media.dispatchEvent(new Event("waiting"));
    expect(status.dataset.state).toBe("paused");
    play.mockRejectedValueOnce(new DOMException("User gesture required", "NotAllowedError"));
    click("play");
    await settle();
    expect(status.dataset.state).toBe("gesture");
    expect(stage.getAttribute("aria-busy")).toBe("false");
    click("play");
    await settle();
    expect(status.dataset.state).toBe("playing");
    Object.defineProperty(media, "ended", { configurable: true, value: true });
    media.dispatchEvent(new Event("ended"));
    expect(status.dataset.state).toBe("ended");
    expect(stage.getAttribute("aria-busy")).toBe("false");
    Object.defineProperty(media, "ended", { configurable: true, value: false });
    Object.defineProperty(media, "error", { configurable: true, value: { code: 3 } });
    media.dispatchEvent(new Event("error"));
    expect(status.dataset.state).toBe("error");
    expect(status.querySelector(".player-status-text")?.getAttribute("role")).toBe("alert");
    expect(stage.getAttribute("aria-busy")).toBe("false");
    Object.defineProperty(media, "error", { configurable: true, value: null });
    media.pause();
    click("play");
    await settle();
    expect(status.dataset.state).toBe("playing");
    expect(status.querySelector(".player-status-text")?.getAttribute("role")).toBe("status");
  });

  it("detaches the old media and keyboard handler when another file opens", () => {
    const old = open();
    old.currentTime = 11;
    const oldStatus = old.parentElement!.querySelector<HTMLElement>(".player-status")!;
    const oldState = oldStatus.dataset.state;
    mounted!.destroy();
    const current = open();
    current.currentTime = 20;
    document.getElementById("player-test")!.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    expect(current.currentTime).toBe(30);
    expect(old.currentTime).toBe(11);
    expect(old.paused).toBe(true);
    expect(old.getAttribute("src")).toBeNull();
    old.dispatchEvent(new Event("loadstart"));
    old.dispatchEvent(new Event("waiting"));
    expect(oldStatus.dataset.state).toBe(oldState);
    expect(oldStatus.isConnected).toBe(false);
  });

  it("uses real media times beyond one hour", () => {
    const media = open();
    Object.defineProperty(media, "duration", { configurable: true, value: 3661.8 });
    media.currentTime = 3660.2;
    media.dispatchEvent(new Event("durationchange"));
    expect(document.querySelector(".player-elapsed")?.textContent).toBe("1:01:00");
    expect(document.querySelector(".player-duration")?.textContent).toBe("1:01:01");
  });

  it("reveals every playlist entry in append-only chunks and keeps the current row disabled", async () => {
    const playlist = Array.from({ length: 123 }, (_, index) => ({ id: `media-${index}`, name: `Film ${index}.mp4`, mime: "video/mp4", size: 1024, ready: index !== 122 }));
    let finish!: () => void;
    const selection = new Promise<void>(resolve => { finish = resolve; });
    const onSelect = vi.fn(() => selection);
    open({ entry: playlist[100], playlist, onSelect });
    expect(document.querySelectorAll(".playlist-entry")).toHaveLength(0);
    document.querySelector<HTMLButtonElement>(".player-playlist-toggle")!.click();
    const first = document.querySelector(".playlist-entry")!;
    const current = first.querySelector<HTMLButtonElement>("button")!;
    expect(first.getAttribute("aria-current")).toBe("true");
    expect(current.dataset.playerSelect).toBe("media-100");
    expect(current.disabled).toBe(true);
    expect(document.querySelectorAll(".playlist-entry")).toHaveLength(50);
    expect(onSelect).not.toHaveBeenCalled();
    const more = document.querySelector<HTMLButtonElement>(".playlist-load-more")!;
    more.focus();
    more.click();
    expect(document.querySelectorAll(".playlist-entry")).toHaveLength(100);
    expect(document.querySelector(".playlist-entry")).toBe(first);
    expect((document.activeElement as HTMLButtonElement).dataset.playerSelect).toBe("media-49");
    more.focus();
    more.click();
    expect(document.querySelectorAll(".playlist-entry")).toHaveLength(123);
    expect(more.hidden).toBe(true);
    expect((document.activeElement as HTMLButtonElement).dataset.playerSelect).toBe("media-99");
    const ids = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-player-select]"), button => button.dataset.playerSelect);
    expect(ids).toEqual(["media-100", ...playlist.filter(item => item.id !== "media-100").map(item => item.id)]);
    const last = document.querySelector<HTMLButtonElement>('[data-player-select="media-122"]')!;
    expect(last.textContent).toBe("Prepare");
    expect(onSelect).not.toHaveBeenCalled();
    last.click();
    document.querySelector<HTMLButtonElement>('[data-player-select="media-0"]')!.click();
    await settle();
    expect(onSelect).toHaveBeenCalledExactlyOnceWith("media-122");
    expect(last.disabled).toBe(true);
    expect(document.querySelector(".player-playlist")?.getAttribute("aria-busy")).toBe("true");
    finish();
    await settle();
    expect(last.disabled).toBe(false);
    expect(current.disabled).toBe(true);
    expect(document.querySelector(".player-playlist")?.hasAttribute("aria-busy")).toBe(false);
  });

  it("bounds the automatic portrait playlist and retains revealed rows across Escape and reopen", () => {
    vi.spyOn(window, "matchMedia").mockReturnValue({ matches: true, addEventListener() {}, removeEventListener() {} } as unknown as MediaQueryList);
    const playlist = Array.from({ length: 5000 }, (_, index) => ({ id: `media-${index}`, name: `Film ${index}.mp4`, mime: "video/mp4", size: 1024, ready: true }));
    open({ entry: playlist[4999], playlist });
    const list = document.querySelector<HTMLElement>(".player-playlist")!;
    const toggle = document.querySelector<HTMLButtonElement>(".player-playlist-toggle")!;
    expect(list.hidden).toBe(false);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(document.querySelectorAll(".playlist-entry")).toHaveLength(50);
    const current = document.querySelector<HTMLButtonElement>('[data-player-select="media-4999"]')!;
    expect(current.disabled).toBe(true);
    document.querySelector<HTMLButtonElement>(".playlist-load-more")!.click();
    expect(document.querySelectorAll(".playlist-entry")).toHaveLength(100);
    list.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    expect(list.hidden).toBe(true);
    expect(document.activeElement).toBe(toggle);
    toggle.click();
    expect(list.hidden).toBe(false);
    expect(document.querySelectorAll(".playlist-entry")).toHaveLength(100);
    expect(document.querySelector('[data-player-select="media-4999"]')).toBe(current);
  });
});

function touchStage(type: string, x: number, y: number, id = 1) {
  const event = new Event(type, { bubbles: true, cancelable: true });
  Object.defineProperties(event, { pointerId: { value: id }, pointerType: { value: "touch" }, isPrimary: { value: id === 1 }, clientX: { value: x }, clientY: { value: y } });
  document.querySelector('.player-stage')!.dispatchEvent(event);
}
function stageGeometry() {
  vi.spyOn(document.querySelector('.player-stage')!, 'getBoundingClientRect').mockReturnValue({ x: 0, y: 0, left: 0, top: 0, right: 400, bottom: 240, width: 400, height: 240, toJSON() {} });
  vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(400);
}
describe("LAN custom mobile player", () => {
  it("retains custom controls in page fullscreen and restores scroll without invoking native video fullscreen", async () => {
    const media = open();
    const enterNativeVideo = vi.fn();
    Object.assign(media, { webkitEnterFullscreen: enterNativeVideo });
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    expect(document.querySelector('.player-page')?.classList.contains('is-fullscreen')).toBe(true);
    expect(document.body.style.overflow).toBe('hidden');
    expect(document.querySelector('.player-transport .player-play')).not.toBeNull();
    expect(enterNativeVideo).not.toHaveBeenCalled();
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    expect(document.querySelector('.player-page')?.classList.contains('is-fullscreen')).toBe(false);
    expect(document.body.style.overflow).toBe('');
  });
  it("falls back after container fullscreen rejection without declaring playback failure", async () => {
    open();
    const container = document.getElementById('player-test')!;
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true });
    const request = vi.fn(() => Promise.reject(new DOMException('denied', 'NotAllowedError')));
    Object.assign(container, { requestFullscreen: request });
    container.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    expect(request).toHaveBeenCalledTimes(1);
    expect(container.classList.contains('is-fullscreen')).toBe(true);
    expect(document.querySelector('.player-status')?.getAttribute('data-state')).not.toBe('error');
    Reflect.deleteProperty(document, 'fullscreenEnabled');
  });
  it("exits a supported whole-player fullscreen without replacing the playing media", async () => {
    const media = open();
    const container = document.getElementById('player-test')!;
    let full: Element | null = null;
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true });
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, get: () => full });
    Object.assign(container, { requestFullscreen: vi.fn(async () => { full = container; document.dispatchEvent(new Event('fullscreenchange')); }) });
    const exit = vi.fn(async () => { full = null; document.dispatchEvent(new Event('fullscreenchange')); });
    Object.defineProperty(document, 'exitFullscreen', { configurable: true, value: exit });
    try {
      container.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
      expect(full).toBe(container);
      container.querySelector<HTMLButtonElement>('.player-back')!.click(); await settle();
      expect(exit).toHaveBeenCalledTimes(1);
      expect(full).toBeNull();
      expect(document.querySelector('.player-media')).toBe(media);
      expect(container.classList.contains('is-fullscreen')).toBe(false);
      expect(document.body.style.overflow).toBe('');
    } finally {
      for (const key of ['fullscreenEnabled', 'fullscreenElement', 'exitFullscreen']) Reflect.deleteProperty(document, key);
    }
  });

  it("previews a horizontal gesture and commits once on release, but cancels another without seeking", () => {
    const media = open(); stageGeometry(); media.currentTime = 30;
    touchStage('pointerdown', 100, 120); touchStage('pointermove', 200, 121);
    expect(media.currentTime).toBe(30);
    expect(document.querySelector('.player-gesture-feedback')?.getAttribute('data-kind')).toBe('seek');
    touchStage('pointerup', 200, 121);
    expect(media.currentTime).toBe(33);
    touchStage('pointerdown', 100, 120); touchStage('pointermove', 200, 121); touchStage('pointercancel', 200, 121);
    expect(media.currentTime).toBe(33);
  });
  it.each(['loadstart', 'waiting'])('prioritizes seek feedback over %s and restores the actual busy state afterward', event => {
    vi.useFakeTimers();
    try {
      const media = open(); stageGeometry(); media.currentTime = 30;
      const status = document.querySelector<HTMLElement>('.player-status')!;
      const feedback = document.querySelector<HTMLElement>('.player-gesture-feedback')!;
      media.dispatchEvent(new Event(event));
      touchStage('pointerdown', 100, 120); touchStage('pointermove', 200, 121);
      expect(media.currentTime).toBe(30);
      expect(feedback.hidden).toBe(false);
      expect(status.hidden).toBe(true);
      expect(document.querySelector('.player-stage')!.getAttribute('aria-busy')).toBe('true');
      touchStage('pointerup', 200, 121);
      expect(media.currentTime).toBe(33);
      expect(status.hidden).toBe(true);
      vi.advanceTimersByTime(1500);
      expect(feedback.hidden).toBe(true);
      expect(status.hidden).toBe(false);
      expect(status.dataset.state).toBe(event === 'loadstart' ? 'loading' : 'buffering');
    } finally { vi.useRealTimers(); }
  });
  it('restores buffering on gesture cancellation and lets playback errors preempt seek feedback', () => {
    const media = open(); stageGeometry(); media.currentTime = 30;
    const status = document.querySelector<HTMLElement>('.player-status')!;
    const feedback = document.querySelector<HTMLElement>('.player-gesture-feedback')!;
    media.dispatchEvent(new Event('waiting'));
    touchStage('pointerdown', 100, 120); touchStage('pointermove', 200, 121);
    touchStage('pointercancel', 200, 121);
    expect(media.currentTime).toBe(30);
    expect(feedback.hidden).toBe(true);
    expect(status.hidden).toBe(false);
    expect(status.dataset.state).toBe('buffering');
    touchStage('pointerdown', 100, 120); touchStage('pointermove', 200, 121);
    Object.defineProperty(media, 'error', { configurable: true, value: { code: 3 } });
    media.dispatchEvent(new Event('error'));
    expect(feedback.hidden).toBe(true);
    expect(status.hidden).toBe(false);
    expect(status.querySelector('.player-status-text')!.getAttribute('role')).toBe('alert');
  });
  it.each([100, 300])('keeps double-tap skip feedback exclusive when buffering follows at x=%s', x => {
    vi.useFakeTimers();
    try {
      const media = open(); stageGeometry(); media.currentTime = 30;
      touchStage('pointerdown', x, 120); touchStage('pointerup', x, 120);
      touchStage('pointerdown', x, 120); touchStage('pointerup', x, 120);
      expect(media.currentTime).toBe(x < 200 ? 20 : 40);
      media.dispatchEvent(new Event('waiting'));
      expect(document.querySelector<HTMLElement>('.player-gesture-feedback')!.hidden).toBe(false);
      expect(document.querySelector<HTMLElement>('.player-status')!.hidden).toBe(true);
      vi.advanceTimersByTime(1500);
      expect(document.querySelector<HTMLElement>('.player-gesture-feedback')!.hidden).toBe(true);
      expect(document.querySelector<HTMLElement>('.player-status')!.hidden).toBe(false);
    } finally { vi.useRealTimers(); }
  });
  it.each([true, false])('previews range dragging without intermediate seeks and restores playing=%s only at the ready target', wasPlaying => {
    const media = open(); media.currentTime = 10;
    if (!wasPlaying) media.pause();
    const range = document.querySelector<HTMLInputElement>('.player-seek')!;
    range.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    for (const value of [20, 35, 42]) {
      range.value = String(value); range.dispatchEvent(new Event('input'));
      expect(media.currentTime).toBe(10);
      expect(media.paused).toBe(true);
      expect(range.getAttribute('aria-valuetext')).toBe(`00:${value} / 01:00`);
    }
    range.dispatchEvent(new Event('change'));
    document.dispatchEvent(new Event('pointerup'));
    expect(media.currentTime).toBe(42);
    expect(media.paused).toBe(true);
    Object.defineProperty(media, 'seeking', { configurable: true, value: true });
    media.dispatchEvent(new Event('seeking'));
    media.dispatchEvent(new Event('canplay'));
    expect(media.paused).toBe(true);
    Object.defineProperty(media, 'seeking', { configurable: true, value: false });
    Object.defineProperty(media, 'readyState', { configurable: true, value: 1 });
    media.dispatchEvent(new Event('seeked'));
    expect(media.paused).toBe(true);
    Object.defineProperty(media, 'readyState', { configurable: true, value: 3 });
    media.dispatchEvent(new Event('canplay'));
    expect(media.currentTime).toBe(42);
    expect(media.paused).toBe(!wasPlaying);
    expect(document.querySelector('.player-stage')!.getAttribute('aria-busy')).toBe('false');
  });
  it('cancels a range preview without seeking and keeps an explicit pause during a pending seek', () => {
    const media = open(); media.currentTime = 10;
    const range = document.querySelector<HTMLInputElement>('.player-seek')!;
    range.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    range.value = '30'; range.dispatchEvent(new Event('input'));
    document.dispatchEvent(new Event('pointercancel'));
    expect(media.currentTime).toBe(10);
    expect(media.paused).toBe(false);
    range.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    range.value = '45'; range.dispatchEvent(new Event('input'));
    range.dispatchEvent(new Event('change'));
    click('play');
    media.dispatchEvent(new Event('seeked'));
    media.dispatchEvent(new Event('canplay'));
    expect(media.currentTime).toBe(45);
    expect(media.paused).toBe(true);
  });
  it('does not resume an obsolete seek while a newer range target is being selected', () => {
    const media = open(); media.currentTime = 10;
    const range = document.querySelector<HTMLInputElement>('.player-seek')!;
    range.value = '30'; range.dispatchEvent(new Event('input')); range.dispatchEvent(new Event('change'));
    range.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    range.value = '50'; range.dispatchEvent(new Event('input'));
    media.dispatchEvent(new Event('seeked')); media.dispatchEvent(new Event('canplay'));
    expect(media.paused).toBe(true);
    expect(media.currentTime).toBe(30);
    range.dispatchEvent(new Event('change'));
    media.dispatchEvent(new Event('seeked'));
    expect(media.currentTime).toBe(50);
    expect(media.paused).toBe(false);
  });
  it("dims only the picture and changes real volume, while unsupported volume shows no fake percentage", () => {
    const media = open(); stageGeometry();
    touchStage('pointerdown', 100, 60); touchStage('pointermove', 100, 180); touchStage('pointerup', 100, 180);
    expect(document.querySelector<HTMLElement>('.player-brightness-shade')!.style.opacity).toBe('0.5');
    expect(document.querySelector<HTMLInputElement>('.player-brightness-range')!.value).toBe('0.5');
    touchStage('pointerdown', 300, 60); touchStage('pointermove', 300, 180); touchStage('pointerup', 300, 180);
    expect(media.volume).toBe(0.5);
    Object.defineProperty(media, 'volume', { configurable: true, get: () => 1, set() {} });
    touchStage('pointerdown', 300, 60); touchStage('pointermove', 300, 180); touchStage('pointerup', 300, 180);
    const feedback = document.querySelector<HTMLElement>('.player-gesture-feedback')!;
    expect(feedback.dataset.kind).toBe('system-volume');
    expect(feedback.textContent).not.toContain('%');
    expect(document.querySelector<HTMLInputElement>('.player-volume-range')!.disabled).toBe(true);
  });
  it.each(['gestures', 'brightness', 'menu', 'speed'])('dismisses the %s popover from the picture without also pausing playback', kind => {
    const media = open();
    if (kind === 'gestures') {
      document.querySelector<HTMLButtonElement>('.player-more')!.click();
      document.querySelector<HTMLButtonElement>('[data-player-action="gestures"]')!.click();
    } else if (kind === 'brightness') document.querySelector<HTMLButtonElement>('.player-brightness-toggle')!.click();
    else if (kind === 'speed') document.querySelector<HTMLButtonElement>('.player-speed .select-face')!.click();
    else document.querySelector<HTMLButtonElement>('.player-more')!.click();
    const panel = document.querySelector<HTMLElement>(kind === 'gestures' ? '.player-help' : kind === 'brightness' ? '.player-brightness' : kind === 'speed' ? '.player-speed .select-list' : '.player-menu')!;
    panel.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    expect(panel.hidden).toBe(false);
    const stage = document.querySelector<HTMLElement>('.player-stage')!;
    stage.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true, cancelable: true }));
    stage.dispatchEvent(new MouseEvent('pointerup', { bubbles: true, cancelable: true }));
    stage.click();
    expect(panel.hidden).toBe(true);
    expect(media.paused).toBe(false);
  });
  it('consumes a touch used to dismiss playback help instead of treating it as a picture gesture', () => {
    vi.useFakeTimers();
    try {
      const media = open(); stageGeometry();
      document.querySelector<HTMLButtonElement>('.player-more')!.click();
      document.querySelector<HTMLButtonElement>('[data-player-action="gestures"]')!.click();
      touchStage('pointerdown', 200, 120); touchStage('pointerup', 200, 120);
      vi.advanceTimersByTime(300);
      expect(document.querySelector<HTMLElement>('.player-help')!.hidden).toBe(true);
      expect(media.paused).toBe(false);
      expect(document.querySelector('.player-page')!.classList.contains('controls-hidden')).toBe(false);
    } finally { vi.useRealTimers(); }
  });
  it("cancels active interaction and restores page overflow when the player is destroyed", () => {
    open(); stageGeometry();
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click();
    touchStage('pointerdown', 100, 120); touchStage('pointermove', 200, 120);
    mounted!.destroy();
    expect(document.body.style.overflow).toBe('');
    expect(document.querySelector('.player-media')).toBeNull();
  });
});

describe('LAN automatic landscape fullscreen', () => {
  function portraitTouch() {
    vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ matches: query.includes('coarse') || query.includes('orientation: portrait'), addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList);
    vi.stubGlobal('innerWidth', 390); vi.stubGlobal('innerHeight', 844);
  }
  it('rotates only the fullscreen player when physical orientation lock is unavailable and restores on exit', async () => {
    portraitTouch(); const media = open();
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    const page = document.querySelector<HTMLElement>('.player-page')!;
    expect(page.classList.contains('is-rotated')).toBe(true);
    expect(page.style.getPropertyValue('--player-viewport-width')).toBe('390px');
    expect(page.style.getPropertyValue('--player-viewport-height')).toBe('844px');
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    expect(page.classList.contains('is-rotated')).toBe(false);
    expect(page.style.getPropertyValue('--player-viewport-width')).toBe('');
    expect(document.querySelector('video')).toBe(media);
  });
  it('requests landscape through the browser and releases only its own successful lock', async () => {
    portraitTouch();
    const orientation = new EventTarget() as EventTarget & { lock: ReturnType<typeof vi.fn>; unlock: ReturnType<typeof vi.fn> };
    orientation.lock = vi.fn(async () => {}); orientation.unlock = vi.fn();
    const previous = Object.getOwnPropertyDescriptor(screen, 'orientation');
    Object.defineProperty(screen, 'orientation', { configurable: true, value: orientation });
    try {
      open(); document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
      expect(orientation.lock).toHaveBeenCalledExactlyOnceWith('landscape');
      vi.stubGlobal('innerWidth', 844); vi.stubGlobal('innerHeight', 390); window.dispatchEvent(new Event('resize'));
      expect(document.querySelector('.player-page')!.classList.contains('is-rotated')).toBe(false);
      document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
      expect(orientation.unlock).toHaveBeenCalledTimes(1);
    } finally {
      if (previous) Object.defineProperty(screen, 'orientation', previous); else Reflect.deleteProperty(screen, 'orientation');
    }
  });
  it('keeps landscape display after a rejected direction lock without reporting playback failure', async () => {
    portraitTouch();
    const previous = Object.getOwnPropertyDescriptor(screen, 'orientation');
    Object.defineProperty(screen, 'orientation', { configurable: true, value: Object.assign(new EventTarget(), { lock: vi.fn(async () => { throw new DOMException('Denied', 'SecurityError'); }), unlock: vi.fn() }) });
    try {
      open(); document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
      expect(document.querySelector('.player-page')!.classList.contains('is-rotated')).toBe(true);
      expect(document.querySelector<HTMLElement>('.player-status')!.dataset.state).not.toBe('error');
      mounted!.destroy();
      expect(document.querySelector('.player-page')!.classList.contains('is-rotated')).toBe(false);
      expect(document.body.style.overflow).toBe('');
    } finally {
      if (previous) Object.defineProperty(screen, 'orientation', previous); else Reflect.deleteProperty(screen, 'orientation');
    }
  });
  it('releases a direction lock that resolves after the fullscreen player was destroyed', async () => {
    portraitTouch();
    let finish!: () => void;
    const pending = new Promise<void>(resolve => { finish = resolve; });
    const unlock = vi.fn();
    const previous = Object.getOwnPropertyDescriptor(screen, 'orientation');
    Object.defineProperty(screen, 'orientation', { configurable: true, value: Object.assign(new EventTarget(), { lock: vi.fn(() => pending), unlock }) });
    try {
      open(); document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
      mounted!.destroy(); finish(); await settle();
      expect(unlock).toHaveBeenCalledTimes(1);
      expect(document.querySelector('video')).toBeNull();
      expect(document.body.style.overflow).toBe('');
    } finally {
      if (previous) Object.defineProperty(screen, 'orientation', previous); else Reflect.deleteProperty(screen, 'orientation');
    }
  });
});

describe('LAN fullscreen playlist restoration', () => {
  it.each([true, false])('restores the previous playlist visibility after fullscreen when expanded=%s', async expanded => {
    vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ matches: query.includes('coarse') || query.includes('orientation: portrait'), addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList);
    vi.stubGlobal('innerWidth', 390); vi.stubGlobal('innerHeight', 844);
    const playlist = [{ id: 'film', name: 'Film.mp4', mime: 'video/mp4', size: 1024, ready: true }, { id: 'next', name: 'Next.mp4', mime: 'video/mp4', size: 1024, ready: true }];
    const media = open({ playlist });
    const list = document.querySelector<HTMLElement>('.player-playlist')!;
    if (!expanded) document.querySelector<HTMLButtonElement>('.player-playlist-toggle')!.click();
    const rows = Array.from(list.querySelectorAll('.playlist-entry'));
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    expect(list.hidden).toBe(true);
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    expect(list.hidden).toBe(!expanded);
    expect(Array.from(list.querySelectorAll('.playlist-entry'))).toEqual(rows);
    expect(document.querySelector('video')).toBe(media);
  });
  it('enters Safari system fullscreen only by explicit choice and restores inline playback on native exit', async () => {
    vi.spyOn(window, 'matchMedia').mockImplementation(query => ({ matches: query.includes('coarse') || query.includes('orientation: portrait'), addEventListener() {}, removeEventListener() {} }) as unknown as MediaQueryList);
    vi.stubGlobal('innerWidth', 390); vi.stubGlobal('innerHeight', 844);
    const media = open({ playlist: [{ id: 'film', name: 'Film.mp4', mime: 'video/mp4', ready: true }] });
    const list = document.querySelector<HTMLElement>('.player-playlist')!;
    const systemControl = document.querySelector<HTMLButtonElement>('[data-player-action="system-fullscreen"]')!;
    expect(systemControl.hidden).toBe(true);
    const enter = vi.fn(() => media.dispatchEvent(new Event('webkitbeginfullscreen')));
    Object.assign(media, { webkitEnterFullscreen: enter, webkitSupportsFullscreen: true });
    media.dispatchEvent(new Event('loadedmetadata'));
    expect(systemControl.hidden).toBe(false);
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    expect(enter).not.toHaveBeenCalled();
    document.querySelector<HTMLButtonElement>('.player-more')!.click();
    systemControl.click(); await settle();
    expect(enter).toHaveBeenCalledTimes(1);
    expect(document.querySelector('.player-page')!.classList.contains('is-rotated')).toBe(false);
    media.dispatchEvent(new Event('webkitendfullscreen'));
    expect(list.hidden).toBe(false);
    expect(document.querySelector('.player-page')!.classList.contains('is-fullscreen')).toBe(false);
    expect(document.body.style.overflow).toBe('');
    expect(document.querySelector('video')).toBe(media);
  });
  it('restores the page immediately when destroyed before native container fullscreen finishes exiting', async () => {
    document.body.style.paddingTop = '7px';
    vi.stubGlobal('scrollY', 180);
    open();
    const container = document.getElementById('player-test')!;
    let full: Element | null = null;
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true });
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, get: () => full });
    Object.assign(container, { requestFullscreen: vi.fn(async () => { full = container; document.dispatchEvent(new Event('fullscreenchange')); }) });
    let finish!: () => void;
    Object.defineProperty(document, 'exitFullscreen', { configurable: true, value: () => new Promise<void>(resolve => { finish = () => { full = null; resolve(); }; }) });
    try {
      document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
      mounted!.destroy();
      expect(document.body.style.position).toBe('');
      expect(document.body.style.overflow).toBe('');
      expect(document.body.style.paddingTop).toBe('7px');
      expect(window.scrollTo).toHaveBeenLastCalledWith(0, 180);
      finish(); await settle();
    } finally {
      for (const key of ['fullscreenEnabled', 'fullscreenElement', 'exitFullscreen']) Reflect.deleteProperty(document, key);
      document.body.style.cssText = '';
    }
  });
});

describe('LAN mobile picture in picture', () => {
  it('uses Safari picture in picture without replacing playback and restores the page when leaving custom fullscreen', async () => {
    const media = open(); media.currentTime = 18;
    let mode = 'inline';
    Object.defineProperty(media, 'webkitPresentationMode', { configurable: true, get: () => mode });
    Object.assign(media, {
      webkitSupportsPresentationMode: (value: string) => value === 'picture-in-picture',
      webkitSetPresentationMode: (value: string) => { mode = value; media.dispatchEvent(new Event('webkitpresentationmodechanged')); },
    });
    media.dispatchEvent(new Event('loadedmetadata'));
    const pip = document.querySelector<HTMLButtonElement>('#player-menu [data-player-action="pip"]')!;
    expect(pip.hidden).toBe(false);
    document.querySelector<HTMLButtonElement>('.player-fullscreen')!.click(); await settle();
    document.querySelector<HTMLButtonElement>('.player-more')!.click(); pip.click(); await settle();
    expect(mode).toBe('picture-in-picture');
    expect(document.querySelector<HTMLElement>('#player-menu')!.hidden).toBe(true);
    expect(document.querySelector('.player-page')!.classList.contains('is-fullscreen')).toBe(false);
    expect(document.body.style.overflow).toBe('');
    expect(pip.getAttribute('aria-pressed')).toBe('true');
    expect(media.currentTime).toBe(18);
    expect(media.paused).toBe(false);
    document.querySelector<HTMLButtonElement>('.player-more')!.click(); pip.click(); await settle();
    expect(mode).toBe('inline');
    expect(pip.getAttribute('aria-pressed')).toBe('false');
    expect(document.querySelector('video')).toBe(media);
    expect(media.currentTime).toBe(18);
  });
  it('follows standard PiP entry and system exit while keeping the same paused video', async () => {
    const media = open(); media.currentTime = 22; media.pause();
    let active: Element | null = null;
    Object.defineProperty(document, 'pictureInPictureEnabled', { configurable: true, value: true });
    Object.defineProperty(document, 'pictureInPictureElement', { configurable: true, get: () => active });
    Object.assign(media, { requestPictureInPicture: async () => { active = media; media.dispatchEvent(new Event('enterpictureinpicture')); } });
    Object.defineProperty(document, 'exitPictureInPicture', { configurable: true, value: async () => { active = null; media.dispatchEvent(new Event('leavepictureinpicture')); } });
    try {
      media.dispatchEvent(new Event('loadedmetadata'));
      const pip = document.querySelector<HTMLButtonElement>('#player-menu [data-player-action="pip"]')!;
      expect(pip.hidden).toBe(false);
      document.querySelector<HTMLButtonElement>('.player-more')!.click(); pip.click(); await settle();
      expect(active).toBe(media);
      expect(pip.getAttribute('aria-pressed')).toBe('true');
      active = null; media.dispatchEvent(new Event('leavepictureinpicture'));
      expect(pip.getAttribute('aria-pressed')).toBe('false');
      expect(document.querySelector('video')).toBe(media);
      expect(media.currentTime).toBe(22);
      expect(media.paused).toBe(true);
    } finally {
      for (const key of ['pictureInPictureEnabled', 'pictureInPictureElement', 'exitPictureInPicture']) Reflect.deleteProperty(document, key);
    }
  });
  it('does not offer PiP when unavailable or disabled for the video', () => {
    const media = open();
    const pip = document.querySelector<HTMLButtonElement>('#player-menu [data-player-action="pip"]')!;
    expect(pip.hidden).toBe(true);
    Object.assign(media, { disablePictureInPicture: true, webkitSupportsPresentationMode: () => true, webkitSetPresentationMode() {} });
    media.dispatchEvent(new Event('loadedmetadata'));
    expect(pip.hidden).toBe(true);
  });
  it('treats the standard PiP owner as authoritative when Safari reports a stale presentation mode', async () => {
    const media = open();
    let owner: Element | null = null;
    Object.defineProperty(document, 'pictureInPictureEnabled', { configurable: true, value: true });
    Object.defineProperty(document, 'pictureInPictureElement', { configurable: true, get: () => owner });
    Object.defineProperty(media, 'webkitPresentationMode', { configurable: true, value: 'picture-in-picture' });
    const enter = vi.fn(async () => { owner = media; media.dispatchEvent(new Event('enterpictureinpicture')); });
    Object.assign(media, { requestPictureInPicture: enter });
    Object.defineProperty(document, 'exitPictureInPicture', { configurable: true, value: async () => { owner = null; media.dispatchEvent(new Event('leavepictureinpicture')); } });
    try {
      media.dispatchEvent(new Event('loadedmetadata'));
      const pip = document.querySelector<HTMLButtonElement>('#player-menu [data-player-action="pip"]')!;
      expect(pip.getAttribute('aria-pressed')).toBe('false');
      expect(pip.getAttribute('aria-label')).toBe('Picture in picture');
      document.querySelector<HTMLButtonElement>('.player-more')!.click(); pip.click(); await settle();
      expect(enter).toHaveBeenCalledTimes(1);
      expect(pip.getAttribute('aria-pressed')).toBe('true');
      owner = null; media.dispatchEvent(new Event('leavepictureinpicture'));
      expect(pip.getAttribute('aria-label')).toBe('Picture in picture');
      expect(pip.getAttribute('aria-pressed')).toBe('false');
    } finally {
      for (const key of ['pictureInPictureEnabled', 'pictureInPictureElement', 'exitPictureInPicture']) Reflect.deleteProperty(document, key);
    }
  });
  it('refreshes Safari-only PiP state when reopening the menu after a system return', () => {
    const media = open();
    let mode = 'picture-in-picture';
    Object.defineProperty(media, 'webkitPresentationMode', { configurable: true, get: () => mode });
    Object.assign(media, { webkitSupportsPresentationMode: () => true, webkitSetPresentationMode() {} });
    media.dispatchEvent(new Event('webkitpresentationmodechanged'));
    const pip = document.querySelector<HTMLButtonElement>('#player-menu [data-player-action="pip"]')!;
    expect(pip.getAttribute('aria-pressed')).toBe('true');
    mode = 'inline';
    document.querySelector<HTMLButtonElement>('.player-more')!.click();
    expect(pip.getAttribute('aria-pressed')).toBe('false');
    expect(pip.getAttribute('aria-label')).toBe('Picture in picture');
  });
});
