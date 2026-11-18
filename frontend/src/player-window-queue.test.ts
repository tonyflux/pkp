import { afterEach, expect, it, vi } from "vitest";
import { Events } from "@wailsio/runtime";
import type { Entry } from "../bindings/github.com/tonyflux/pkp/internal/services";

const originalURL = location.href;
afterEach(() => { history.replaceState(null, "", originalURL); vi.restoreAllMocks(); });

it("sends a queue selection to the main player window without calling the drive", async () => {
  history.replaceState(null, "", "/?window=player");
  vi.resetModules();
  // Resetting modules creates a fresh runtime stub alongside the window-specific store.
  const runtime = await import("@wailsio/runtime");
  const events: typeof Events = Reflect.get(runtime, "Events");
  const emit = vi.spyOn(events, "Emit");
  // The store reads location at module load, so simulate a separate window before importing it.
  const { player, playAt } = await import("./store");
  player.playlist = [{ id: "second", name: "second.mp4", category: "video" } as Entry];
  player.index = 0;
  await playAt(0);
  expect(emit).toHaveBeenCalledWith("player:command", { name: "playAt", index: 0 });
  expect(emit).toHaveBeenCalledTimes(1);
});
