import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";
import { mountPlayer } from "../../internal/lanshare/web/player.js";

let mounted: { destroy(): void } | undefined;
let play: MockInstance;

beforeEach(() => {
  document.body.innerHTML = '<section id="player-test"></section>';
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
  const media = document.querySelector("video")!;
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

  it("uses real media times beyond one hour without rounding up the remaining time", () => {
    const media = open();
    Object.defineProperty(media, "duration", { configurable: true, value: 3661.8 });
    media.currentTime = 3660.2;
    media.dispatchEvent(new Event("durationchange"));
    expect(document.querySelector(".player-elapsed")?.textContent).toBe("1:01:00");
    expect(document.querySelector(".player-duration")?.textContent).toBe("1:01:01");
    expect(document.querySelector(".player-remaining")?.textContent?.endsWith("00:01")).toBe(true);
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
