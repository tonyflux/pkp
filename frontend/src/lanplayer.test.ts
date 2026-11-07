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

function open(extra = {}) {
  mounted = mountPlayer({
    container: document.getElementById("player-test")!,
    entry: { id: "film", name: "Film.mp4", mime: "video/mp4", size: 1024 },
    source: "/api/content?space=one&id=film&variant=original",
    language: "en",
    ...extra,
  });
  const media = document.querySelector("video")!;
  Object.defineProperties(media, {
    duration: { configurable: true, value: 60 },
    readyState: { configurable: true, value: 4 },
  });
  media.dispatchEvent(new Event("loadedmetadata"));
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
    expect(document.querySelector(".player-status")?.getAttribute("role")).toBe("status");
    expect(onError).not.toHaveBeenCalled();
    click("play");
    await settle();
    expect(media.paused).toBe(false);
    expect(document.querySelector(".player-page")?.classList.contains("is-playing")).toBe(true);
  });

  it("detaches the old media and keyboard handler when another file opens", () => {
    const old = open();
    old.currentTime = 11;
    mounted!.destroy();
    const current = open();
    current.currentTime = 20;
    document.getElementById("player-test")!.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    expect(current.currentTime).toBe(30);
    expect(old.currentTime).toBe(11);
    expect(old.paused).toBe(true);
    expect(old.getAttribute("src")).toBeNull();
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
});
