# Installation and First Launch

[简体中文](INSTALL.md) · English

## Download Status

**v0.4.0** adds read-only LAN sharing, Share Import previews, external magnet/torrent opening, unified grid/list pickers, and file context menus and shortcuts. Download from the [latest Release](https://github.com/tonyflux/pkp/releases/latest): [macOS Universal](https://github.com/tonyflux/pkp/releases/download/v0.4.0/PKP-0.4.0-macos-universal.dmg) / [Windows x64 and ARM64](https://github.com/tonyflux/pkp/releases/download/v0.4.0/PKP-0.4.0-windows-x64-arm64-setup.exe). Check against `SHA256SUMS` from the same Release and verify the signature. Obtain installers from this repository's Releases.

v0.2.0 was the first open-source release. v0.4.0 continues to include `PKP-0.4.0-source.tar.gz` (PKP-owned source corresponding to the installers) and `libmpv-sources.tar.gz` (third-party corresponding source, which does not contain PKP's own source). Share Import supports pasted PikPak share links, explicit `pkp://share` opening, and automatic detection of validated clipboard shares on desktop. After a read-only grid/list preview, choose the receiving account and destination and confirm; it never imports automatically. See the [user guide](USAGE.en.md) for operations and limits.

**v0.1.0 remains available** as a historical release: [macOS Universal](https://github.com/tonyflux/pkp/releases/download/v0.1.0/PKP-0.1.0-macos-universal.dmg) / [Windows x64 and ARM64](https://github.com/tonyflux/pkp/releases/download/v0.1.0/PKP-0.1.0-windows-x64-arm64-setup.exe). That Release also provides `libmpv-sources.tar.gz` and the terms in effect at the time. Its installers were not built from the current source and remain under [those proprietary terms](legal/v0.1.0-proprietary.txt). Prefer the latest version for new installs. Usage statistics defaulted to off in v0.2.0 and on in v0.1.0; v0.3.0 defaults daily active installation statistics to on.

## Verifying Release Assets

v0.1.0 has seven assets: the macOS DMG, the Windows installer, `update.json`, `update.json.sig`, `libmpv-sources.tar.gz`, `LICENSE`, and `SHA256SUMS`. The first two are application installers; the third-party source archive does not contain PKP's own source code.

v0.2.0 is published and provides eight assets: the seven kinds listed above, plus `PKP-0.2.0-source.tar.gz` (generically, `PKP-<version>-source.tar.gz`). The additional archive provides the complete PKP-owned source and build scripts corresponding to that version's installers; this repository also provides current source. It does not replace the separate third-party corresponding source archive, and it is not corresponding source for the v0.1.0 installers.

`SHA256SUMS` is used to check download integrity and cannot, on its own, prove the publisher's identity. In-app updates use a built-in public key to verify the Ed25519-signed manifest, then check the installer size and SHA-256 hash; do not continue installation if the signature or hash is invalid.

For a complete manual verification, place all assets from the same Release in a separate directory (seven for v0.1.0; eight for v0.2.0 and later) and download the public [verify_release.py](../.github/scripts/verify_release.py). After installing Python 3.11+ and OpenSSL 3+, run the following (replace the tag with the release you downloaded):

```sh
python3 verify_release.py /path/to/release-assets v0.2.0
```

The tool contains only the public verification key and does not require a GitHub token, account login, or private key; it reports failure if the signature, tag, installer, or asset set does not match. The public repository's “Verify public release” workflow automatically verifies GitHub assets and anonymous download URLs after an official release; a workflow failure is not a reason to bypass verification.

## macOS

Requires macOS 12 or later. The Universal package includes both Apple silicon and Intel architectures and bundles the playback library; there is no need to install mpv or Homebrew separately.

After obtaining and verifying the installer:

1. Open the DMG and drag PKP into “Applications.” Do not run it directly from the disk image.
2. Open PKP from “Applications.” The current build is **ad hoc signed and not notarized by Apple**, so the system may block its first launch; the prompt may also reappear after an update.
3. macOS 15 and later: first try opening the app and dismiss the blocking prompt, then go to “System Settings → Privacy & Security → Security,” find “Open Anyway” for PKP, and confirm as required by the system.
4. macOS 12–14: Control-click PKP in “Applications” (or right-click), choose “Open,” and confirm the system prompt.

If the system still says the app “is damaged,” or the steps above do not work, first confirm the download source and checksums; do not bypass security checks for unknown files. Only after confirming that the file is trustworthy and the installation path is correct should you remove the quarantine attribute for this application in Terminal:

```sh
xattr -dr com.apple.quarantine /Applications/PKP.app
```

This removes the application's quarantine flag; it is not a security certification. Do not extend this operation to the entire downloads directory or disable system Gatekeeper. Do not copy this command verbatim if the actual application name or path differs.

## Windows and Linux

Windows provides a shared x64 / ARM64 installer that selects the application and playback library according to the local machine's architecture. Users have reported that the initial release draft passed acceptance testing, but no complete device matrix has been provided, so an experience equivalent to macOS cannot be promised; playback surfaces, synchronization, notifications, and system authentication still need broader testing on real devices. Features such as the menu bar panel and WebView proxy have platform limitations, and the installer does not have an Authenticode publisher signature.

Linux support and installer releases are not currently promised.

## Updating the Application

Versions that support updates can check via “Settings → About & Updates”; when not logged in, use the entry with the same name on the login page. On macOS, you can also use “Check for Updates…” in the PKP menu. Automatic checks are enabled by default and can be disabled separately; updates are not downloaded or installed automatically. When a new version is found, you can view the release notes, ask to be reminded later, or ignore that version. Manual checks can still show ignored versions.

- macOS (from v0.2.0): first click “Download Update,” then “Restart & Update” after download and verification finish. During preparation, macOS may request permission to replace the app; quit any other PKP instances using the same installed app first. Once you confirm quitting, the update helper waits for PKP to exit, replaces the app in its original location, and reopens the same profile. Accounts and local settings are preserved. Canceling permission or quit confirmation does not force PKP to close. Update from the installed app in Applications, not a DMG, a read-only location, or a translocated temporary path. Updates do not remove quarantine flags or bypass Gatekeeper. The ad hoc signing and lack of notarization still apply, and reopening may require system approval.
- Windows: click “Quit and Run Installer,” then proceed through the quit confirmation and system administrator authorization. Canceling either quitting or authorization will not force PKP to close. The installer requires other PKP processes to exit and reuses the original installation directory; there is no need to uninstall first. Accounts and local settings are retained. Thorough acceptance testing on real Windows devices is still needed.
- The released macOS v0.1.0 does not include this replacement helper. To gain “Restart & Update,” upgrading to v0.2.0 or later still requires one manual replacement: download and open the new DMG, quit PKP, then drag it over the installed app. Do not run the app directly from the image. Accounts and local settings are preserved.
- Local test versions that lack update functionality or trust the old development public key require manually downloading and installing the official version first; no upgrade prompt will appear if both versions are 0.1.0.

v0.2.0 is published with this macOS update flow. Publishing the release does not mean native authorization, Gatekeeper, rollback, and upgrades across devices have all passed acceptance testing. Those checks are still outstanding.

## First Launch

- Log in with your own PikPak account. “Remember Account and Password” and “Automatic Login” are currently checked by default; uncheck them if you do not want your password saved.
- Read the [Privacy Notice](../PRIVACY.en.md) first. v0.1.0 defaulted usage statistics on; v0.2.0 defaulted them off. v0.3.0 defaults daily active installation statistics to on, including existing settings with no explicit choice, while preserving identifiable explicit opt-outs. A one-time notice explains the default and how to turn statistics off in Settings → Privacy & Security, also accessible before sign-in. Got it and dismissal are not consent and leave statistics enabled; the notice has no turn-off button. With an endpoint, recording starts at startup and sending can begin about 30 seconds later, before acknowledgment; development builds without an endpoint neither collect nor show the notice. Publishing the app does not deploy companion telemetry services. Successful sign-in also submits referral relationship information.
- The first login creates a local file index, during which you can still browse; search and drive-wide statistics may need to wait until the index is ready.
- macOS may request permission for system notifications; whether to allow them is up to you. Actual notification behavior in the current ad hoc build still awaits confirmation on real devices.
- Before using synchronization or bulk organization, back up important data and carefully review the previews and confirmation details.
