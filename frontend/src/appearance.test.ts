import { afterEach, describe, expect, it, vi } from "vitest";

const backend = vi.hoisted(() => ({ clear: vi.fn() }));
vi.mock("../bindings/github.com/tonyflux/pkp/internal/services", () => ({
  AppearanceService: { ClearWallpaper: backend.clear },
  DriveService: { Status: vi.fn().mockResolvedValue(null) }, PlayerService: {}, AppLockService: {}, ExternalOpenService: {},
  MenuBarService: {}, NotifyService: {}, TelemetryService: {},
}));

import { clearWallpaper, forgetWallpaper, setContentOpacity, setReduceTransparency, ui } from "./store";

afterEach(() => {
  backend.clear.mockReset();
  forgetWallpaper();
  setReduceTransparency(false);
  setContentOpacity(80);
  localStorage.removeItem("pkp.contentOpacity");
  localStorage.removeItem("pkp.reduceTransparency");
  ui.message = "";
});

describe("restore default appearance", () => {
  it("clears the custom image and restores 80% even when reduced transparency is enabled", async () => {
    backend.clear.mockResolvedValue(undefined);
    ui.wallpaper = "custom";
    localStorage.setItem("pkp.wallpaper", "custom");
    setContentOpacity(17);
    setReduceTransparency(true);

    await clearWallpaper();

    expect(backend.clear).toHaveBeenCalledOnce();
    expect(ui.wallpaper).toBe("");
    expect(localStorage.getItem("pkp.wallpaper")).toBeNull();
    expect(ui.contentOpacity).toBe(80);
    expect(localStorage.getItem("pkp.contentOpacity")).toBe("80");
    expect(ui.reduceTransparency).toBe(true);
  });

  it("keeps the image and opacity when clearing the wallpaper fails", async () => {
    backend.clear.mockRejectedValue(new Error("disk error"));
    ui.wallpaper = "custom";
    localStorage.setItem("pkp.wallpaper", "custom");
    setContentOpacity(17);

    await clearWallpaper();

    expect(ui.wallpaper).toBe("custom");
    expect(localStorage.getItem("pkp.wallpaper")).toBe("custom");
    expect(ui.contentOpacity).toBe(17);
    expect(localStorage.getItem("pkp.contentOpacity")).toBe("17");
    expect(ui.messageTone).toBe("error");
  });
});
