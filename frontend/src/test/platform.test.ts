import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => {
  vi.restoreAllMocks();
  vi.resetModules();
  localStorage.setItem("pkp.language", "zh-Hans");
});

describe("desktop platform conventions", () => {
  it.each([
    { platform: "MacIntel", system: "macOS", mac: true, windows: false, linux: false, mod: "⌘", shortcut: "⇧⌘R", keychain: "Keychain", tray: "menu bar", settings: "System Settings", reveal: "Show in Finder" },
    { platform: "Win32", system: "Windows", mac: false, windows: true, linux: false, mod: "Ctrl", shortcut: "Ctrl+Shift+R", keychain: "Credential Manager", tray: "system tray", settings: "Windows Settings", reveal: "Show in File Explorer" },
    { platform: "Linux x86_64", system: "Linux", mac: false, windows: false, linux: true, mod: "Ctrl", shortcut: "Ctrl+Shift+R", keychain: "keyring (Secret Service)", tray: "system tray", settings: "System Settings", reveal: "Show in File Manager" },
    { platform: "Linux aarch64", system: "Linux", mac: false, windows: false, linux: true, mod: "Ctrl", shortcut: "Ctrl+Shift+R", keychain: "keyring (Secret Service)", tray: "system tray", settings: "System Settings", reveal: "Show in File Manager" },
  ])("names $platform without borrowing another desktop's conventions", async (expected) => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue(expected.platform);
    // Platform constants are evaluated at module load, so each simulated desktop needs a fresh import.
    const dom = await import("../dom");
    const { applyLanguage } = await import("../i18n");
    applyLanguage("en", "en");
    expect(dom.systemName).toBe(expected.system);
    expect(dom.onMac).toBe(expected.mac);
    expect(dom.onWindows).toBe(expected.windows);
    expect(dom.onLinux).toBe(expected.linux);
    expect(dom.modKey).toBe(expected.mod);
    expect(dom.shortcutText("R", true)).toBe(expected.shortcut);
    expect(dom.keychainName).toBe(expected.keychain);
    expect(dom.trayName).toBe(expected.tray);
    expect(dom.settingsAppName).toBe(expected.settings);
    expect(dom.revealFileLabel).toBe(expected.reveal);
    applyLanguage("ja", "ja");
    expect(dom.revealFileLabel).not.toBe(expected.reveal);
    applyLanguage("en", "en");
    expect(dom.revealFileLabel).toBe(expected.reveal);
  });
});
