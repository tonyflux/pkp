import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, nextTick, type App } from "vue";
import type { AudioDetails, Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type * as Services from "../bindings/github.com/tonyflux/pkp/internal/services";

const backend = vi.hoisted(() => ({ details: vi.fn() }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", async (importOriginal) => {
  const services = await importOriginal<typeof Services>();
  return { ...services, PlayerService: { ...services.PlayerService, AudioDetails: backend.details } };
});

import MusicPlayer from "./components/MusicPlayer.vue";
import { player, ui } from "./store";

let app: App;
let host: HTMLElement;

beforeEach(() => {
  player.playlist = [{ id: "music", name: "原始文件名.mp3", category: "audio", thumbnail: "", duration: 0, size: 1 } as Entry];
  player.index = 0;
  player.presentation = "window";
  player.state = { ...player.state, duration: 180, loading: false, error: "", paused: false };
  ui.route = "player";
  host = document.createElement("div");
  document.body.append(host);
});

afterEach(() => {
  app.unmount();
  host.remove();
  player.playlist = [];
  player.presentation = "off";
  vi.clearAllMocks();
});

describe("music window metadata", () => {
  it("retains complete embedded title and artist text while displaying a cover", async () => {
    const details: AudioDetails = { fileId: "music", title: "很长的歌曲标题", artist: "很长的艺术家名称", album: "专辑名称", date: "2024", cover: "data:image/png;base64,AA==" };
    backend.details.mockResolvedValue(details);
    app = createApp(MusicPlayer, { windowed: true });
    app.mount(host);
    await vi.waitFor(() => expect(host.querySelector("h1")?.textContent).toBe(details.title));
    await nextTick();
    const heading = host.querySelector<HTMLHeadingElement>("h1")!;
    const byline = host.querySelector<HTMLElement>(".byline")!;
    expect(heading.title).toBe(details.title);
    expect(byline.title).toBe(byline.textContent);
    expect(byline.title).toContain(details.artist);
    expect(byline.title).toContain(details.album);
    expect(host.querySelector(".listening")?.classList.contains("art")).toBe(true);
  });
});
