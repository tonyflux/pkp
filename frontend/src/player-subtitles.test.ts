import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, h, nextTick } from "vue";
import type { AISubtitleState, Chapter, Entry, Track } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type * as Services from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  tracks: vi.fn<() => Promise<Track[]>>(),
  chapters: vi.fn<() => Promise<Chapter[]>>(),
  select: vi.fn<(kind: string, id: number) => Promise<void>>(),
  ai: vi.fn<(on: boolean, language: string) => Promise<void>>(),
  stage: vi.fn<() => Promise<void>>(),
  windowButtons: vi.fn<() => Promise<void>>(),
}));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", async (importOriginal) => {
  const services = await importOriginal<typeof Services>();
  return {
    ...services,
    PlayerService: {
      ...services.PlayerService,
      Tracks: backend.tracks, Chapters: backend.chapters, SelectTrack: backend.select,
      SetStage: backend.stage, ShowWindowButtons: backend.windowButtons,
    },
    DriveService: { ...services.DriveService, SetAISubtitles: backend.ai },
  };
});
import PlayerView from "./components/PlayerView.vue";
import StatusPill from "./components/StatusPill.vue";
import { player, ui } from "./store";

const unmounts = new Set<() => void>();
let nativeSubtitle = 1;
type Menu = "dock" | "sidebar";

function nativeTracks(selected = nativeSubtitle): Track[] {
  return [{
    id: 1, type: "sub", title: "Native English", lang: "en", codec: "subrip",
    selected: selected === 1, default: true, channels: "", external: false,
  }, {
    id: 2, type: "sub", title: "Native Japanese", lang: "ja", codec: "subrip",
    selected: selected === 2, default: false, channels: "", external: false,
  }];
}
// Player events replace the state in every window; native track selection is
// queried separately and must not come from the menu's cached choice.
function receiveAI(change: Partial<AISubtitleState>) {
  player.state = { ...player.state, aiSubtitles: { ...player.state.aiSubtitles, ...change } };
}
function deferred<T>() {
  return (Promise as PromiseConstructor & {
    withResolvers<T>(): { promise: Promise<T>; resolve: (value: T | PromiseLike<T>) => void };
  }).withResolvers<T>();
}
async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await nextTick();
}
async function mountPlayer(windowed = false, followRoute = false) {
  const host = document.createElement("div");
  document.body.append(host);
  const app = createApp({
    setup: () => () => !followRoute || ui.route === "player"
      ? h(PlayerView, { windowed })
      : h(StatusPill, { aboveBar: true }),
  });
  app.mount(host);
  const unmount = () => { app.unmount(); host.remove(); unmounts.delete(unmount); };
  unmounts.add(unmount);
  await settle();
  return host;
}
async function openSubtitles(host: HTMLElement, menu: Menu = "dock") {
  const entry = menu === "dock"
    ? host.querySelector<HTMLButtonElement>('[data-fold="subtitles"] button.entry')
    : host.querySelector<HTMLElement>('.settings [role="combobox"][aria-label="字幕"]');
  expect(entry).not.toBeNull();
  entry!.click();
  await settle();
  const options = menu === "dock"
    ? host.querySelector<HTMLElement>(".track-options")
    : document.querySelector<HTMLElement>('[data-select-list][aria-label="字幕"]');
  expect(options).not.toBeNull();
  return options!;
}
function option(menu: HTMLElement, label: string) {
  const row = Array.from(menu.querySelectorAll<HTMLElement>('[role="option"]'))
    .find((element) => element.querySelector("span")?.textContent === label);
  expect(row, `subtitle option: ${label}`).toBeDefined();
  return row!;
}
function expectSelected(menu: HTMLElement, label: string) {
  const selected = Array.from(menu.querySelectorAll('[role="option"][aria-selected="true"]'))
    .map((element) => element.querySelector("span")?.textContent);
  expect(selected).toEqual([label]);
}
function expectCurrent(host: HTMLElement, label: string) {
  expect(host.querySelector('[data-fold="subtitles"] button.entry')?.getAttribute("aria-label")).toBe(`字幕：${label}`);
  const sidebar = host.querySelector('.settings [role="combobox"][aria-label="字幕"]');
  if (sidebar) expect(sidebar.querySelector("b")?.textContent).toBe(label);
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.resetAllMocks();
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 0, 640, 40));
  nativeSubtitle = 1;
  player.playlist = [];
  player.index = -1;
  player.owner = "";
  player.presentation = "immersive";
  player.returnRoute = "files";
  ui.route = "player";
  ui.fullscreen = false;
  ui.message = "";
  player.state = {
    ...player.state, source: "test-video", duration: 120, position: 10, paused: true, error: "", loading: false,
    quality: { options: [], current: "", choice: "auto", switching: "", failed: "", error: "", lowered: false },
    aiSubtitles: { available: true, on: false, language: "", status: "", error: "", errorRevision: 0 },
  };
  backend.tracks.mockImplementation(async () => nativeTracks());
  backend.chapters.mockResolvedValue([]);
  backend.stage.mockResolvedValue(undefined);
  backend.windowButtons.mockResolvedValue(undefined);
  backend.select.mockImplementation(async (kind, id) => {
    if (kind === "subtitle") nativeSubtitle = id;
  });
  backend.ai.mockImplementation(async (on, language) => {
    if (on) nativeSubtitle = 0;
    receiveAI({ on, language, status: on ? "live" : "", error: "" });
  });
});
afterEach(() => {
  for (const unmount of unmounts) unmount();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("player subtitle selection after AI changes", () => {
  it.each([
    { name: "main dock", windowed: false, menu: "dock" as Menu },
    { name: "docked playlist settings", windowed: false, menu: "sidebar" as Menu },
    { name: "separate player window", windowed: true, menu: "dock" as Menu },
  ])("shows native Off after AI fails and lets the previous track be chosen in $name", async ({ windowed, menu }) => {
    const host = await mountPlayer(windowed);
    expectCurrent(host, "Native English");
    let choices = await openSubtitles(host, menu);
    expectSelected(choices, "Native English");
    option(choices, "English").click();
    await settle();
    expect(nativeSubtitle).toBe(0);
    expectCurrent(host, "AI English");

    receiveAI({ on: false, status: "", error: "relay disconnected" });
    await settle();
    expectCurrent(host, "关");
    choices = await openSubtitles(host, menu);
    expectSelected(choices, "关");
    expect(option(choices, "Native English").getAttribute("aria-selected")).toBe("false");
    expect(backend.select).not.toHaveBeenCalled();

    option(choices, "Native English").click();
    await settle();
    expect(nativeSubtitle).toBe(1);
    expectCurrent(host, "Native English");
    expectSelected(await openSubtitles(host, menu), "Native English");
  });

  it.each([false, true])("synchronizes externally toggled AI in a windowed=%s player", async (windowed) => {
    const host = await mountPlayer(windowed);
    await backend.ai(true, "en");
    await settle();
    expectCurrent(host, "AI English");
    await backend.ai(false, "");
    await settle();
    expect(nativeSubtitle).toBe(0);
    expectCurrent(host, "关");
    expectSelected(await openSubtitles(host), "关");
  });

  it("does not let a late track query restore the pre-AI selection", async () => {
    const host = await mountPlayer();
    const stale = deferred<Track[]>();
    backend.tracks.mockReturnValueOnce(stale.promise);
    nativeSubtitle = 0;
    receiveAI({ on: true, language: "en", status: "connecting" });
    await settle();
    receiveAI({ on: false, status: "", error: "relay disconnected" });
    await settle();
    expectCurrent(host, "关");

    stale.resolve(nativeTracks(1));
    await settle();
    expectCurrent(host, "关");
    const choices = await openSubtitles(host);
    expectSelected(choices, "关");
    option(choices, "Native English").click();
    await settle();
    expect(nativeSubtitle).toBe(1);
    expectCurrent(host, "Native English");
  });

  it("refreshes after a failed AI command even when no AI state transition reaches the window", async () => {
    receiveAI({ error: "relay disconnected" });
    const host = await mountPlayer();
    backend.ai.mockImplementationOnce(async () => {
      nativeSubtitle = 0;
      throw new Error("relay disconnected");
    });
    option(await openSubtitles(host), "English").click();
    await settle();
    expectCurrent(host, "关");
    const choices = await openSubtitles(host);
    expectSelected(choices, "关");
    option(choices, "Native English").click();
    await settle();
    expect(nativeSubtitle).toBe(1);
    expectCurrent(host, "Native English");
  });

  it("waits for AI to be explicitly disabled before enabling an ordinary track", async () => {
    nativeSubtitle = 0;
    receiveAI({ on: true, language: "en", status: "live" });
    const host = await mountPlayer();
    const disabled = deferred<void>();
    backend.ai.mockImplementationOnce(async () => {
      receiveAI({ on: false, status: "" });
      await disabled.promise;
    });
    option(await openSubtitles(host), "Native English").click();
    await settle();
    expect(backend.select).not.toHaveBeenCalled();
    expect(nativeSubtitle).toBe(0);
    expectCurrent(host, "关");

    disabled.resolve(undefined);
    await settle();
    expect(nativeSubtitle).toBe(1);
    expectCurrent(host, "Native English");
    expectSelected(await openSubtitles(host), "Native English");
  });

  it("keeps AI selected if disabling it fails instead of enabling an ordinary track", async () => {
    nativeSubtitle = 0;
    receiveAI({ on: true, language: "en", status: "live" });
    const host = await mountPlayer();
    backend.ai.mockRejectedValueOnce(new Error("could not disable AI"));
    option(await openSubtitles(host), "Native English").click();
    await settle();
    expect(backend.select).not.toHaveBeenCalled();
    expect(nativeSubtitle).toBe(0);
    expectCurrent(host, "AI English");
    expectSelected(await openSubtitles(host), "English");
  });

  it("stops AI before selecting native subtitles even before the delayed On event", async () => {
    const host = await mountPlayer();
    let aiEnabled = false;
    backend.ai.mockImplementation(async (on) => {
      aiEnabled = on;
      if (on) nativeSubtitle = 0;
    });
    option(await openSubtitles(host), "English").click();
    await settle();
    expect(aiEnabled).toBe(true);
    expect(player.state.aiSubtitles.on).toBe(false);
    expectCurrent(host, "关");

    option(await openSubtitles(host), "Native English").click();
    await settle();
    // The startup continuation must not disable the newly chosen native track.
    if (aiEnabled) nativeSubtitle = 0;
    receiveAI({ on: aiEnabled, language: aiEnabled ? "en" : "", status: aiEnabled ? "live" : "" });
    await settle();
    expect(aiEnabled).toBe(false);
    expect(nativeSubtitle).toBe(1);
    expectCurrent(host, "Native English");
    expectSelected(await openSubtitles(host), "Native English");
  });

  it("keeps the latest native choice when Off arrives before AI shutdown finishes", async () => {
    nativeSubtitle = 0;
    receiveAI({ on: true, language: "en", status: "live" });
    const host = await mountPlayer();
    const shutdown = deferred<void>();
    backend.ai.mockImplementationOnce(async () => {
      receiveAI({ on: false, status: "" });
      await shutdown.promise;
    });
    option(await openSubtitles(host), "Native English").click();
    await settle();
    expectCurrent(host, "关");
    option(await openSubtitles(host), "Native Japanese").click();
    await settle();
    expect(nativeSubtitle).toBe(0);

    shutdown.resolve(undefined);
    await settle();
    expect(nativeSubtitle).toBe(2);
    expectCurrent(host, "Native Japanese");
    expectSelected(await openSubtitles(host), "Native Japanese");
    expect(backend.select).toHaveBeenCalledExactlyOnceWith("subtitle", 2);
  });

  it("finishes an in-flight native selection before applying the newer choice", async () => {
    nativeSubtitle = 0;
    const host = await mountPlayer();
    const selecting = deferred<void>();
    backend.select.mockImplementationOnce(async () => {
      await selecting.promise;
      nativeSubtitle = 1;
    });
    option(await openSubtitles(host), "Native English").click();
    await settle();
    option(await openSubtitles(host), "Native Japanese").click();
    await settle();
    selecting.resolve(undefined);
    await settle();
    expect(nativeSubtitle).toBe(2);
    expectCurrent(host, "Native Japanese");
    expectSelected(await openSubtitles(host), "Native Japanese");
  });

  it.each([false, true])("reports failure while collapsed even if it is immediately cleared=%s", async (cleared) => {
    receiveAI({ on: true, language: "en", status: "live" });
    const host = await mountPlayer(false, true);
    host.querySelector<HTMLButtonElement>("button.back")!.click();
    await settle();
    expect(host.querySelector(".player")).toBeNull();
    receiveAI({ on: false, status: "", error: "relay disconnected" });
    if (cleared) receiveAI({ error: "" });
    await settle();
    const notice = host.querySelector('[role="status"].alert');
    expect(notice?.textContent).toContain("AI 字幕已关闭");
    expect(notice?.textContent).toContain("relay disconnected");

    if (!cleared) {
      player.presentation = "immersive";
      ui.route = "player";
      await settle();
      expect(host.querySelector(".notice")?.textContent).toContain("relay disconnected");
    }
  });

  it.each([false, true])("keeps errors on the visible windowed=%s stage without a duplicate global notice", async (windowed) => {
    player.presentation = windowed ? "window" : "immersive";
    const host = await mountPlayer(windowed);
    receiveAI({ on: false, status: "", error: "relay disconnected" });
    await settle();
    expect(host.querySelector(".notice")?.textContent).toContain("AI 字幕已关闭");
    expect(host.querySelector(".notice")?.textContent).toContain("relay disconnected");
    expect(ui.message).toBe("");
  });

  it("does not duplicate the separate window's error notification in the browsing main window", async () => {
    player.presentation = "window";
    ui.route = "files";
    const host = await mountPlayer(false, true);
    receiveAI({ on: false, status: "", error: "relay disconnected" });
    await settle();
    expect(host.querySelector('[role="status"]')).toBeNull();
    expect(ui.message).toBe("");
  });

  it("keeps the stage failure when command completion coalesces error and clear events", async () => {
    const host = await mountPlayer();
    backend.ai.mockImplementationOnce(async () => {
      nativeSubtitle = 0;
      receiveAI({ on: false, status: "", error: "relay disconnected" });
      receiveAI({ error: "" });
    });
    option(await openSubtitles(host), "English").click();
    await settle();
    expectCurrent(host, "关");
    expect(host.querySelector(".notice")?.textContent).toContain("relay disconnected");
    expect(player.state.aiSubtitles.error).toBe("");
  });

  it.each([
    { name: "main dock", windowed: false, menu: "dock" as Menu },
    { name: "docked playlist settings", windowed: false, menu: "sidebar" as Menu },
    { name: "separate player window", windowed: true, menu: "dock" as Menu },
  ])("lets the selected Off cancel AI before its delayed On event in $name", async ({ windowed, menu }) => {
    const host = await mountPlayer(windowed);
    let aiEnabled = false;
    backend.ai.mockImplementation(async (on) => {
      aiEnabled = on;
      if (on) nativeSubtitle = 0;
    });
    option(await openSubtitles(host, menu), "English").click();
    await settle();
    expect(aiEnabled).toBe(true);
    expect(player.state.aiSubtitles.on).toBe(false);
    expectCurrent(host, "关");

    const choices = await openSubtitles(host, menu);
    expectSelected(choices, "关");
    option(choices, "关").click();
    await settle();
    receiveAI({ on: aiEnabled, language: aiEnabled ? "en" : "", status: aiEnabled ? "live" : "" });
    await settle();
    expect(aiEnabled).toBe(false);
    expect(nativeSubtitle).toBe(0);
    expectCurrent(host, "关");
    expectSelected(await openSubtitles(host, menu), "关");
  });

  it.each([
    { name: "main dock", windowed: false, menu: "dock" as Menu },
    { name: "docked playlist settings", windowed: false, menu: "sidebar" as Menu },
    { name: "separate player window", windowed: true, menu: "dock" as Menu },
  ])("lets the selected Off cancel a pending native choice during AI shutdown in $name", async ({ windowed, menu }) => {
    nativeSubtitle = 0;
    receiveAI({ on: true, language: "en", status: "live" });
    const host = await mountPlayer(windowed);
    const shutdown = deferred<void>();
    backend.ai.mockImplementationOnce(async () => {
      receiveAI({ on: false, status: "" });
      await shutdown.promise;
    });
    option(await openSubtitles(host, menu), "Native English").click();
    await settle();
    expectCurrent(host, "关");
    const choices = await openSubtitles(host, menu);
    expectSelected(choices, "关");
    option(choices, "关").click();
    await settle();
    expect(nativeSubtitle).toBe(0);

    shutdown.resolve(undefined);
    await settle();
    expect(nativeSubtitle).toBe(0);
    expectCurrent(host, "关");
    expectSelected(await openSubtitles(host, menu), "关");
    expect(backend.select).toHaveBeenCalledExactlyOnceWith("subtitle", 0);
  });

  it("cancels a pending native choice when the player is collapsed and remounted", async () => {
    nativeSubtitle = 0;
    receiveAI({ on: true, language: "en", status: "live" });
    const host = await mountPlayer(false, true);
    const shutdown = deferred<void>();
    backend.ai.mockImplementationOnce(async () => {
      receiveAI({ on: false, status: "" });
      await shutdown.promise;
    });
    option(await openSubtitles(host), "Native English").click();
    await settle();
    host.querySelector<HTMLButtonElement>("button.back")!.click();
    await settle();
    expect(host.querySelector(".player")).toBeNull();
    player.presentation = "immersive";
    ui.route = "player";
    await settle();
    expectCurrent(host, "关");
    const queries = backend.tracks.mock.calls.length;

    shutdown.resolve(undefined);
    await settle();
    expect(backend.select).not.toHaveBeenCalled();
    expect(backend.tracks).toHaveBeenCalledTimes(queries);
    expect(nativeSubtitle).toBe(0);
    expectSelected(await openSubtitles(host), "关");
  });

  it.each(["video", "rendition", "source"] as const)("does not apply an old raw track ID after the %s changes", async (context) => {
    nativeSubtitle = 0;
    receiveAI({ on: true, language: "en", status: "live" });
    const host = await mountPlayer();
    const shutdown = deferred<void>();
    backend.ai.mockImplementationOnce(async () => {
      receiveAI({ on: false, status: "" });
      await shutdown.promise;
    });
    option(await openSubtitles(host), "Native English").click();
    await settle();
    if (context === "video") {
      player.playlist = [{ id: "different-video", name: "Different video", category: "video" } as Entry];
      player.index = 0;
    } else if (context === "rendition") {
      player.state = { ...player.state, quality: { ...player.state.quality, switching: "720" } };
    } else {
      player.state = { ...player.state, source: "different-video-source" };
    }
    await settle();
    shutdown.resolve(undefined);
    await settle();
    expect(backend.select).not.toHaveBeenCalled();
    expect(nativeSubtitle).toBe(0);
    expectCurrent(host, "关");
    expectSelected(await openSubtitles(host), "关");
  });

  it("keeps a pending native choice when a session snapshot retains the same file", async () => {
    nativeSubtitle = 0;
    player.playlist = [{ id: "current-video", name: "Current video", category: "video" } as Entry];
    player.index = 0;
    player.presentation = "window";
    receiveAI({ on: true, language: "en", status: "live" });
    const host = await mountPlayer(true);
    const shutdown = deferred<void>();
    backend.ai.mockImplementationOnce(async () => {
      receiveAI({ on: false, status: "" });
      await shutdown.promise;
    });
    option(await openSubtitles(host), "Native English").click();
    await settle();
    player.playlist = player.playlist.map((entry) => ({ ...entry }));
    await settle();

    shutdown.resolve(undefined);
    await settle();
    expect(nativeSubtitle).toBe(1);
    expectCurrent(host, "Native English");
    expectSelected(await openSubtitles(host), "Native English");
  });

  it.each([false, true])("reports repeated identical final failure snapshots on a windowed=%s stage", async (windowed) => {
    nativeSubtitle = 0;
    player.presentation = windowed ? "window" : "immersive";
    const host = await mountPlayer(windowed);
    receiveAI({ on: false, status: "", error: "startup refused", errorRevision: 1 });
    await settle();
    expect(host.querySelector(".notice")?.textContent).toContain("startup refused");
    await vi.waitFor(() => expect(host.querySelector(".notice")).toBeNull(), { timeout: 5000 });

    // Only the final snapshot is delivered: no intermediate clear or On event.
    receiveAI({ on: false, status: "", error: "startup refused", errorRevision: 2 });
    await settle();
    expect(host.querySelector(".notice")?.textContent).toContain("startup refused");
    expectCurrent(host, "关");
    expect(ui.message).toBe("");
  });

  it("reports a repeated identical final failure snapshot after dismissing the background notice", async () => {
    player.presentation = "bar";
    ui.route = "files";
    const host = await mountPlayer(false, true);
    receiveAI({ on: false, status: "", error: "startup refused", errorRevision: 1 });
    await settle();
    expect(host.querySelector('[role="status"]')?.textContent).toContain("startup refused");
    host.querySelector<HTMLButtonElement>('button[title="关闭"]')!.click();
    await settle();
    expect(host.querySelector('[role="status"]')).toBeNull();
    const serial = ui.messageSerial;

    receiveAI({ on: false, status: "", error: "startup refused", errorRevision: 2 });
    await settle();
    expect(host.querySelector('[role="status"].alert')?.textContent).toContain("startup refused");
    expect(ui.messageSerial).toBe(serial + 1);
  });

  it("does not duplicate repeated separate-window failures in the browsing main window", async () => {
    player.presentation = "window";
    ui.route = "files";
    const host = await mountPlayer(false, true);
    receiveAI({ on: false, status: "", error: "startup refused", errorRevision: 1 });
    await settle();
    receiveAI({ on: false, status: "", error: "startup refused", errorRevision: 2 });
    await settle();
    expect(host.querySelector('[role="status"]')).toBeNull();
    expect(ui.message).toBe("");
  });
});
