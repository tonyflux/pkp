import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, type App } from "vue";
import type { Account, DriveStatus, Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type * as Services from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({ toggle: vi.fn(), seek: vi.fn() }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", async (importOriginal) => {
  const services = await importOriginal<typeof Services>();
  return { ...services, PlayerService: { ...services.PlayerService, TogglePause: backend.toggle, SeekTo: backend.seek } };
});

import CompactPlaybackBar from "./components/CompactPlaybackBar.vue";
import { accounts, drive, player, ui } from "./store";

let app: App;
let host: HTMLElement;

beforeEach(async () => {
  backend.toggle.mockResolvedValue(undefined);
  backend.seek.mockResolvedValue(undefined);
  accounts.list = [
    { id: "a", label: "Tony", login: "first@example.com" },
    { id: "b", label: "Tony", login: "second@example.com" },
  ] as Account[];
  drive.status = { accountId: "b" } as DriveStatus;
  player.owner = "a";
  player.presentation = "bar";
  player.playlist = [{ id: "video", name: "Example video.mp4", category: "video" } as Entry];
  player.index = 0;
  player.state = { ...player.state, paused: true, position: 3, duration: 120 };
  ui.route = "files";
  host = document.createElement("div");
  document.body.append(host);
  app = createApp(CompactPlaybackBar);
  app.mount(host);
  await nextTick();
});

afterEach(() => {
  app.unmount();
  host.remove();
  vi.clearAllMocks();
  player.presentation = "off";
  player.playlist = [];
  accounts.list = [];
  drive.status = null;
});

describe("collapsed playback bar", () => {
  it("keeps the playing account identifiable and the progress keyboard-seekable after switching accounts", async () => {
    expect(host.textContent).toContain("所属账号 · Tony");
    expect(host.textContent).toContain("fi••@example.com");
    expect(host.querySelector('[title="所属账号 · Tony（fi••@example.com）"]')).not.toBeNull();
    expect(host.textContent).toContain("切回该账号");

    host.querySelector<HTMLButtonElement>('[aria-label="播放"]')!.click();
    const key = new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true });
    host.querySelector('[role="slider"]')!.dispatchEvent(key);
    await nextTick();
    expect(backend.toggle).toHaveBeenCalledOnce();
    expect(backend.seek).toHaveBeenCalledWith(8);
    expect(key.defaultPrevented).toBe(true);
    expect(host.querySelector('[role="slider"]')!.getAttribute("aria-valuenow")).toBe("8");
  });

  it("keeps the paused state and owner visible while the player is in a separate window", async () => {
    player.presentation = "window";
    await nextTick();
    expect(host.textContent).toContain("已暂停");
    expect(host.textContent).toContain("所属账号 · Tony");
    expect(host.querySelector('[title="在独立窗口播放 · 已暂停"]')).not.toBeNull();
    expect(host.querySelector('[aria-label="回到主窗口播放"]')).not.toBeNull();
  });

  it("restores the current player without dropping its session", async () => {
    host.querySelector<HTMLButtonElement>('[aria-label="恢复画面"]')!.click();
    await nextTick();
    expect(ui.route).toBe("player");
    expect(player.presentation).toBe("immersive");
    expect(player.playlist[0].id).toBe("video");
    expect(player.owner).toBe("a");
  });
});
