import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, h, nextTick } from "vue";
import type { Account, AISubtitleState, Chapter, DriveStatus, Entry, Membership, Track } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type * as Services from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({
  tracks: vi.fn<() => Promise<Track[]>>(),
  chapters: vi.fn<() => Promise<Chapter[]>>(),
  select: vi.fn<(kind: string, id: number) => Promise<void>>(),
  ai: vi.fn<(on: boolean, language: string) => Promise<void>>(),
  membership: vi.fn<(account: string) => Promise<Membership>>(),
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
    DriveService: { ...services.DriveService, SetAISubtitles: backend.ai, Membership: backend.membership },
  };
});
import PlayerView from "./components/PlayerView.vue";
import StatusPill from "./components/StatusPill.vue";
import OptionList from "./components/OptionList.vue";
import { accounts, drive, memberships, player, ui } from "./store";
import { stamp } from "./format";

const member = (active = true): Membership => ({ active, expire: "2029-02-14T12:00:00Z", items: [], notice: "" });
const account = (id: string): Account => ({
  id, login: `${id}@example.com`, label: id, nickname: "", avatar: "", current: id === "browsing",
  needsPassword: false, usedAt: "", aiConsent: false, aiContentConsent: false,
});

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
  if (windowed) {
    player.ownerInfo = account(player.owner);
    player.owner = "";
  }
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
  player.owner = "playing";
  player.ownerInfo = null;
  accounts.list = [account("browsing"), account("playing")];
  drive.status = { ...drive.status, accountId: "browsing" } as DriveStatus;
  for (const id of Object.keys(memberships)) delete memberships[id];
  memberships.playing = { value: member(), loading: false, error: "" };
  player.presentation = "immersive";
  player.returnRoute = "files";
  ui.route = "player";
  ui.fullscreen = false;
  ui.message = "";
  player.state = {
    ...player.state, source: "test-video", duration: 120, position: 10, paused: true, error: "", loading: false,
    quality: { options: [], current: "", choice: "auto", switching: "", failed: "", error: "", lowered: false },
    aiSubtitles: {
      available: true, requiresMembership: true, on: false, language: "", status: "", error: "", errorRevision: 0,
      quota: { account: "playing", known: false, exceeded: false, limit: 0, usage: 0, nextResetTime: "" },
    },
  };
  backend.tracks.mockImplementation(async () => nativeTracks());
  backend.chapters.mockResolvedValue([]);
  backend.stage.mockResolvedValue(undefined);
  backend.windowButtons.mockResolvedValue(undefined);
  backend.membership.mockResolvedValue(member());
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

describe("AI subtitle access and server quota", () => {
  it.each(["dock", "sidebar"] as Menu[])("keeps native choices usable but blocks nonmembers in %s", async (menu) => {
    backend.membership.mockResolvedValue(member(false));
    const host = await mountPlayer();
    const choices = await openSubtitles(host, menu);
    expect(host.textContent).toContain("会员专享");
    expect(host.textContent).toContain("AI 字幕需要 PikPak 会员");
    const english = option(choices, "English");
    expect(english.matches(":disabled, [aria-disabled='true']")).toBe(true);
    english.click();
    if (menu === "dock") {
      english.focus();
      english.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      english.dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true }));
    }
    await settle();
    expect(backend.ai).not.toHaveBeenCalled();
    option(choices, "关").click();
    await settle();
    expectCurrent(host, "关");
    option(await openSubtitles(host, menu), "Native Japanese").click();
    await settle();
    expectCurrent(host, "Native Japanese");
    expect(backend.ai.mock.calls.every(([on]) => !on)).toBe(true);
  });

  it("uses the playing account and refreshes only on owner changes or relevant menu opens", async () => {
    memberships.browsing = { value: member(false), loading: false, error: "" };
    const host = await mountPlayer();
    expect(backend.membership).toHaveBeenCalledExactlyOnceWith("playing");
    let choices = await openSubtitles(host);
    expect(backend.membership).toHaveBeenCalledTimes(2);
    expect(option(choices, "English").hasAttribute("aria-disabled")).toBe(false);
    option(choices, "English").click();
    await settle();
    expect(backend.ai).toHaveBeenCalledWith(true, "en");
    player.state = { ...player.state, position: 20 };
    player.playlist = player.playlist.map((entry) => ({ ...entry }));
    accounts.list = accounts.list.map((entry) => ({ ...entry }));
    await settle();
    expect(backend.membership).toHaveBeenCalledTimes(2);

    backend.membership.mockResolvedValue(member(false));
    player.owner = "browsing";
    await settle();
    expect(backend.membership).toHaveBeenLastCalledWith("browsing");
    choices = await openSubtitles(host);
    expect(option(choices, "English").getAttribute("aria-disabled")).toBe("true");
    expect(host.textContent).toContain("AI 字幕需要 PikPak 会员");
  });

  it("does not flash nonmember while membership loads and updates an open menu when eligible", async () => {
    delete memberships.playing;
    const reading = deferred<Membership>();
    backend.membership.mockReturnValue(reading.promise);
    const host = await mountPlayer();
    const choices = await openSubtitles(host);
    expect(host.textContent).toContain("正在读取会员状态…");
    expect(host.textContent).not.toContain("AI 字幕需要 PikPak 会员");
    expect(option(choices, "English").getAttribute("aria-disabled")).toBe("true");
    expect(backend.membership).toHaveBeenCalledExactlyOnceWith("playing");
    expect(backend.ai).not.toHaveBeenCalled();
    reading.resolve(member());
    await settle();
    expect(option(choices, "English").hasAttribute("aria-disabled")).toBe(false);
    const original = option(choices, "原文");
    expect(original.tabIndex).toBe(0);
    original.focus();
    original.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    expect(document.activeElement).toBe(option(choices, "中文"));
    expect(host.textContent).not.toContain("正在读取会员状态…");
    option(choices, "English").click();
    await settle();
    expectCurrent(host, "AI English");
  });

  it("uses synchronized owner and quota in the separate player window", async () => {
    receiveAI({ quota: { account: "playing", known: true, exceeded: false, limit: 3600, usage: 600, nextResetTime: "" } });
    const host = await mountPlayer(true);
    expect(player.owner).toBe("");
    expect(backend.membership).toHaveBeenCalledExactlyOnceWith("playing");
    const choices = await openSubtitles(host);
    expect(choices.textContent).toContain("AI 剩余 50:00 / 1:00:00");
    expect(option(choices, "English").hasAttribute("aria-disabled")).toBe(false);
    backend.membership.mockResolvedValue(member(false));
    player.ownerInfo = account("browsing");
    await settle();
    expect(backend.membership).toHaveBeenLastCalledWith("browsing");
    expect(choices.textContent).not.toContain("AI 剩余");
    expect(option(choices, "English").getAttribute("aria-disabled")).toBe("true");
  });

  it("opens sidebar keyboard navigation on an enabled row while refreshing selected AI", async () => {
    const host = await mountPlayer();
    nativeSubtitle = 0;
    receiveAI({ on: true, language: "en", status: "live" });
    await settle();
    const reading = deferred<Membership>();
    backend.membership.mockReturnValue(reading.promise);
    const choices = await openSubtitles(host, "sidebar");
    expect(option(choices, "English").matches(":disabled")).toBe(true);
    expect(document.activeElement).toBe(option(choices, "关"));
    document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    expect(document.activeElement).toBe(option(choices, "Native English"));
    document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await settle();
    expect(document.querySelector("[data-select-list]")).toBeNull();
    reading.resolve(member());
    await settle();
  });

  it("distinguishes read failures from nonmembership and offers a working retry", async () => {
    backend.membership.mockRejectedValue(new Error("offline"));
    const host = await mountPlayer();
    const choices = await openSubtitles(host);
    expect(host.textContent).toContain("会员状态读取失败");
    expect(host.textContent).not.toContain("AI 字幕需要 PikPak 会员");
    expect(option(choices, "English").getAttribute("aria-disabled")).toBe("true");
    backend.membership.mockResolvedValue(member());
    const retry = choices.querySelector<HTMLButtonElement>(".ai-status button")!;
    retry.focus();
    const space = new KeyboardEvent("keydown", { key: " ", bubbles: true, cancelable: true });
    retry.dispatchEvent(space);
    expect(space.defaultPrevented).toBe(false);
    retry.click(); // jsdom does not synthesize native button activation from keyboard events.
    expect(backend.membership).toHaveBeenCalledTimes(3);
    await settle();
    expect(host.textContent).not.toContain("会员状态读取失败");
    option(choices, "English").click();
    await settle();
    expectCurrent(host, "AI English");
  });

  it("rechecks access when a previously enabled sidebar dispatches a stale command", async () => {
    const host = await mountPlayer();
    const face = host.querySelector<HTMLElement>('.settings [role="combobox"][aria-label="字幕"]')!;
    const select = (face as HTMLElement & { __vueParentComponent: { emit: (name: string, value: string) => void } }).__vueParentComponent;
    memberships.playing.value = member(false);
    select.emit("update:modelValue", "ai:en");
    await settle();
    expect(backend.ai).not.toHaveBeenCalled();
    expect(host.querySelector(".notice")?.textContent).toContain("AI 字幕需要 PikPak 会员");
    expectCurrent(host, "Native English");
  });

  it("keeps a Basic-auth development deployment usable without a membership lookup", async () => {
    receiveAI({ requiresMembership: false });
    delete memberships.playing;
    backend.membership.mockRejectedValue(new Error("not an official account"));
    const host = await mountPlayer();
    const choices = await openSubtitles(host);
    expect(backend.membership).not.toHaveBeenCalled();
    expect(host.textContent).not.toContain("会员专享");
    expect(option(choices, "English").hasAttribute("aria-disabled")).toBe(false);
    option(choices, "English").click();
    await settle();
    expectCurrent(host, "AI English");
  });

  it("keeps quota unknown until enabling, then reflects matching server snapshots and reset without ticking", async () => {
    const host = await mountPlayer();
    const choices = await openSubtitles(host);
    expect(choices.querySelector(".ai-status")?.textContent).toContain("AI 配额：开启后获取");
    expect(host.querySelector(".settings .ai-status")?.textContent).toContain("AI 配额：开启后获取");
    expect(host.textContent).not.toContain("AI 剩余");
    expect(backend.ai).not.toHaveBeenCalled();
    const reset = "2027-02-14T12:00:00Z";
    receiveAI({ quota: { account: "playing", known: true, exceeded: false, limit: 7200, usage: 1800, nextResetTime: reset } });
    await settle();
    for (const status of Array.from(host.querySelectorAll(".ai-status"))) {
      expect(status.textContent).toContain("AI 剩余 1:30:00 / 2:00:00");
      expect(status.textContent).toContain(`配额重置：${stamp(reset)}`);
    }
    await vi.advanceTimersByTimeAsync(60_000);
    expect(choices.querySelector(".quota")?.textContent).toContain("AI 剩余 1:30:00 / 2:00:00");
    receiveAI({ on: true, language: "en", status: "live", quota: { ...player.state.aiSubtitles.quota, usage: 2400 } });
    await settle();
    expect(choices.querySelector(".quota")?.textContent).toContain("AI 剩余 1:20:00 / 2:00:00");
    receiveAI({ on: false, status: "", error: "relay disconnected" });
    await settle();
    expect(choices.querySelector(".quota")?.textContent).toContain("AI 剩余 1:20:00 / 2:00:00");
    expect(backend.ai).not.toHaveBeenCalled();
  });

  it.each([
    { exceeded: true, usage: 60 },
    { exceeded: false, usage: 8000 },
  ])("shows exhausted quota as zero without permanently disabling retry: %j", async ({ exceeded, usage }) => {
    receiveAI({ quota: { account: "playing", known: true, exceeded, limit: 7200, usage, nextResetTime: "" } });
    const host = await mountPlayer();
    const choices = await openSubtitles(host);
    expect(choices.querySelector(".quota")?.textContent).toContain("AI 剩余 0:00 / 2:00:00");
    expect(option(choices, "English").hasAttribute("aria-disabled")).toBe(false);
    option(choices, "English").click();
    await settle();
    expect(backend.ai).toHaveBeenCalledWith(true, "en");
  });

  it("never shows another playing owner's quota and rejects that owner's late membership response", async () => {
    receiveAI({ quota: { account: "playing", known: true, exceeded: false, limit: 7200, usage: 60, nextResetTime: "" } });
    const oldRead = deferred<Membership>();
    backend.membership.mockReturnValueOnce(oldRead.promise);
    const host = await mountPlayer();
    player.owner = "browsing";
    backend.membership.mockResolvedValue(member(false));
    await settle();
    const choices = await openSubtitles(host);
    oldRead.resolve(member());
    await settle();
    expect(choices.querySelector(".quota")?.textContent).toContain("AI 配额：开启后获取");
    expect(host.textContent).not.toContain("AI 剩余");
    expect(option(choices, "English").getAttribute("aria-disabled")).toBe("true");
  });

  it("shows the same member requirement and quota in the overflow subtitle menu", async () => {
    let resizeControls = () => {};
    vi.stubGlobal("ResizeObserver", class {
      constructor(private callback: () => void) {}
      observe(el: HTMLElement) { if (el.classList.contains("controls")) resizeControls = this.callback; }
      disconnect() {}
    });
    vi.mocked(HTMLElement.prototype.getBoundingClientRect).mockImplementation(function (this: HTMLElement) {
      return new DOMRect(0, 0, this.classList.contains("controls") || this.classList.contains("spacer") ? 0 : 640, 40);
    });
    backend.membership.mockResolvedValue(member(false));
    receiveAI({ quota: { account: "playing", known: true, exceeded: false, limit: 3600, usage: 600, nextResetTime: "" } });
    const host = await mountPlayer();
    resizeControls();
    await settle();
    host.querySelector<HTMLButtonElement>("[data-player-more] > button")!.click();
    await settle();
    const choices = host.querySelector<HTMLElement>(".more-pop .track-options")!;
    expect(choices).not.toBeNull();
    expect(choices.textContent).toContain("AI 字幕需要 PikPak 会员");
    expect(choices.textContent).toContain("AI 剩余 50:00 / 1:00:00");
    expect(option(choices, "English").getAttribute("aria-disabled")).toBe("true");
  });
});

describe("player option disabled semantics", () => {
  it("skips disabled rows with arrows/Home/End and rejects pointer, Enter and Space while keeping native rows available", async () => {
    const choose = vi.fn();
    const host = document.createElement("div");
    document.body.append(host);
    const app = createApp(OptionList, {
      labelledby: "tracks",
      items: [
        { key: "ai:en", label: "AI English", selected: true, disabled: true },
        { key: "0", label: "Off", selected: false },
        { key: "ai:ja", label: "AI Japanese", selected: false, disabled: true },
        { key: "1", label: "Native", selected: false },
        { key: "ai:zh", label: "AI Chinese", selected: false, disabled: true },
      ],
      onChoose: choose,
    });
    app.mount(host);
    const unmount = () => { app.unmount(); host.remove(); unmounts.delete(unmount); };
    unmounts.add(unmount);
    await settle();
    const rows = host.querySelectorAll<HTMLElement>('[role="option"]');
    const key = (value: string) => document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key: value, bubbles: true }));
    expect(rows[0].tabIndex).toBe(-1);
    rows[1].focus();
    key("ArrowDown");
    expect(document.activeElement).toBe(rows[3]);
    key("ArrowUp");
    expect(document.activeElement).toBe(rows[1]);
    key("End");
    expect(document.activeElement).toBe(rows[3]);
    key("Home");
    expect(document.activeElement).toBe(rows[1]);
    rows[0].click();
    rows[0].focus();
    key("Enter");
    key(" ");
    expect(choose).not.toHaveBeenCalled();
    key("ArrowDown");
    key("Enter");
    expect(choose).toHaveBeenCalledExactlyOnceWith("0");
  });
});
