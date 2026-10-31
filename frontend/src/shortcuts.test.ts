import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { commandFor, runCommand } from "./shortcuts";
import { appLock, drive, ui } from "./store";
import type { AppLockState, DriveStatus } from "../bindings/github.com/tonyflux/pkp/internal/services";

const press = (key: string, mods: Partial<{ meta: boolean; ctrl: boolean; alt: boolean; shift: boolean }> = {}, code = "") => ({
  key,
  code,
  metaKey: !!mods.meta,
  ctrlKey: !!mods.ctrl,
  altKey: !!mods.alt,
  shiftKey: !!mods.shift,
  isComposing: false,
});

describe("commandFor", () => {
  it("takes ⌘ alone on macOS and Ctrl alone elsewhere", () => {
    expect(commandFor(press(",", { meta: true }), true, false)).toBe("settings");
    expect(commandFor(press(",", { ctrl: true }), false, false)).toBe("settings");
    // Ctrl+K edits text on macOS; ⌃⌘F is the system's full screen.
    expect(commandFor(press("k", { ctrl: true }), true, false)).toBeNull();
    expect(commandFor(press("f", { meta: true, ctrl: true }), true, false)).toBeNull();
    expect(commandFor(press("k", { meta: true }), false, false)).toBeNull();
  });


  it("tells ⌘N from ⇧⌘N, and leaves ⌥ chords alone", () => {
    expect(commandFor(press("n", { meta: true }), true, false)).toBe("cloudAdd");
    expect(commandFor(press("N", { meta: true, shift: true }), true, false)).toBe("newFolder");
    expect(commandFor(press("U", { meta: true, shift: true }), true, false)).toBe("uploadFolder");
    expect(commandFor(press("∑", { meta: true, alt: true }, "KeyW"), true, false)).toBeNull();
  });

  it("reads letters by position under a non-Latin layout, punctuation by character", () => {
    expect(commandFor(press("к", { meta: true }, "KeyR"), true, false)).toBe("refresh");
    // A layout with "-" where US has "/": ⌘- stays the page zoom.
    expect(commandFor(press("-", { meta: true }, "Slash"), true, false)).toBeNull();
  });

  it("leaves text-editing keys to a field being typed in", () => {
    expect(commandFor(press("ArrowUp", { meta: true }), true, false)).toBe("back");
    expect(commandFor(press("ArrowUp", { meta: true }), true, true)).toBeNull();
    expect(commandFor(press("ArrowLeft", { alt: true }), false, false)).toBe("back");
    expect(commandFor(press("ArrowLeft", { alt: true }), false, true)).toBeNull();
    expect(commandFor(press("r", { meta: true }), true, true)).toBe("refresh");
    expect(commandFor({ ...press("k", { meta: true }), isComposing: true }, true, false)).toBeNull();
  });

  it("gives F5 and Alt+← only off macOS", () => {
    expect(commandFor(press("F5"), false, false)).toBe("refresh");
    expect(commandFor(press("F5"), true, false)).toBeNull();
    expect(commandFor(press("ArrowLeft", { alt: true }), true, false)).toBeNull();
  });
});

describe("runCommand", () => {
  beforeEach(() => {
    drive.status = { accountId: "a1", configured: true } as DriveStatus;
    ui.route = "files";
  });
  afterEach(() => {
    document.body.innerHTML = "";
    appLock.state = null;
    drive.status = null;
  });

  it("waits while a sheet asks for an answer", () => {
    document.body.innerHTML = '<div class="scrim"></div>';
    runCommand("settings");
    expect(ui.route).toBe("files");
    document.body.innerHTML = "";
    runCommand("settings");
    expect(ui.route).toBe("settings");
  });

  it("does nothing under the lock screen or without an account", () => {
    appLock.state = { locked: true } as AppLockState;
    runCommand("nav:transfers");
    expect(ui.route).toBe("files");
    appLock.state = null;
    drive.status = { configured: false } as DriveStatus;
    runCommand("nav:transfers");
    expect(ui.route).toBe("files");
  });
});
