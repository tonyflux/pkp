# PKP 1.0.2

[简体中文 / Bilingual release notes](release-notes.md)

- macOS: Fix in-app updates mistaking the crash monitor for another PKP instance while retaining checks for actual competing instances.
- Windows: In-app updates select the matching machine-wide or per-user installer and, with supported installers, update and reopen PKP after confirmation. Older clients or installers may require the installation wizard. Real-device validation of Windows upgrade, authorization and rollback remains outstanding.

**Upgrading from macOS 1.0.1:** The existing updater may still report another instance because it uses the old helper. If this happens, quit PKP and manually replace the installed app with the app from the 1.0.2 DMG. Your accounts and settings are retained.

macOS builds are ad hoc signed and not notarized; Windows installers are not Authenticode-signed. On Linux, install the verified package manually or replace the AppImage after exiting PKP.

[Installation](https://github.com/tonyflux/pkp/blob/v1.0.2/docs/INSTALL.en.md) · [Changes since 1.0.1](https://github.com/tonyflux/pkp/compare/v1.0.1...v1.0.2)
