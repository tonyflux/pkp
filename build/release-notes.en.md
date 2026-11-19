# PKP 1.0.1

[简体中文 / Bilingual release notes](release-notes.md)

- Windows: Reduce the top margin of the main workspace while retaining the native title bar. Player layouts and macOS are unchanged.
- Linux: Fix intermittent black video on X11 and Wayland by avoiding a faulty scaler path in older libmpv. The rendering checks cover frame colors, resizing while paused, screenshots, and moving playback between windows.

macOS builds are ad hoc signed and not notarized; Windows installers are not Authenticode-signed. On Linux, install the verified package manually or replace the AppImage after exiting PKP.

[Installation](https://github.com/tonyflux/pkp/blob/v1.0.1/docs/INSTALL.en.md) · [Changes since 1.0.0](https://github.com/tonyflux/pkp/compare/v1.0.0...v1.0.1)
