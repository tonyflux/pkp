# PKP 0.4.0

[简体中文 / Bilingual release notes](release-notes.md)

Read-only LAN sharing, Share Import previews, and improved desktop file actions.

## Highlights

- **Share Import**: paste PikPak share links or open `pkp://share`, with access codes, thumbnails, grid/list views, nested pagination, cross-folder selection and read-only media previews. Explicitly confirm the receiving account and destination before submission. Cancellation preserves magnet/torrent drafts; uncertain submissions are not automatically replayed. Close, lock and stale replies reclaim preview resources.
- **Desktop clipboard detection** checks validated shares at startup, foreground activation and account readiness. Opens previews only: no automatic import, background polling or interruption of existing dialogs, and no repeated canceled clipboard input. Server mode never reads the host clipboard.
- **Default-off read-only LAN sharing** adds a separate password, fixed account/folder scope, address QR codes, responsive five-language visitor pages, original downloads and desktop advance preparation. Stop, password changes and space disable revoke access. Optional idle-sleep prevention is also off by default and does not prevent manual or lid-close sleep.
- **Mobile playback and preparation**: responsive custom controls, seek gestures, supported fullscreen/PiP, an image viewer and lightweight Home Screen/PWA guidance. Prefer supported originals; when necessary, fully prepare compatible copies at up to 1080p before manual playback. Lossless startup optimization remains an explicit choice. Packages include FFmpeg/ffprobe. On-demand pagination, thumbnails and bounded memory caches reduce repeated loading. No Service Worker or offline private-content cache; HTTP installation depends on the browser.
- **Storage and authorization safeguards**: separate original and compatible/lossless-copy locations; reuse completed sync originals only after identity, version and SHA-256 checks without creating or modifying sync. Hide sync staging contents. Cleanup removes only verified, unused owned copies, never originals; revalidate sources, permissions and versions, with explicit resumption for held work.
- **External opens and file actions**: browser `magnet:` links and local `.torrent` files enter Cloud Add confirmation at cold start or while running, without automatic submission or taking over system defaults. Preserve sign-in, lock, account-switch and draft queues. Files context menus cover single items, multi-selection and the canvas; shortcuts include `F2` rename, arrows/`Shift` selection and in-drive copy/cut/paste.
- **Reliability and release workflow**: fix native player handle lifetime during shutdown concurrent with background track restoration on macOS/Windows. Release builds are manually dispatched from `main` only to avoid duplicate tag-triggered signing/uploads; post-publication public-download verification remains separate.

## Boundaries and privacy (unchanged policy)

LAN pages use **HTTP on trusted networks only**; do not expose them through public port forwarding. QR codes exclude passwords. HTTP is unencrypted, and a separate password does not replace a trusted network.

**Daily active installation statistics have defaulted to ON since v0.3.0; v0.4.0 does not change this** (v0.2.0 defaulted to OFF). Identifiable explicit opt-outs are preserved; settings without an explicit choice follow the on default, and old saves without refusal markers cannot be distinguished from no choice. With an endpoint, recording starts at startup and sending can begin about 30 seconds later, before notice acknowledgment; without one, nothing is recorded/sent and no notice appears. Got it or dismissal acknowledges the notice, not consent, and leaves statistics enabled.

Reports contain a random installation ID, version, OS/architecture, language and active local dates—not feature actions, frequencies, logs, accounts, filenames, paths, searches or file contents. Turn them off in Settings → Privacy & Security, also available before sign-in. Opt-out deletes the local ID and pending reports, not server history. Counts reflect successfully reporting active installations, not verified people or exact installs; publishing the app does not deploy telemetry services.

## Release assets, verification and limitations

The release asset set has **eight items**: macOS Universal installer (macOS 12+, Apple silicon/Intel), combined Windows x64/ARM64 installer, update manifest and signature, `PKP-0.4.0-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE` and `SHA256SUMS`. PKP-owned source is GPL-3.0-only; historical licensing is not changed retroactively. PKP is an unofficial client.

Keep the existing Ed25519 update-signing trust and public key; no trust reset. Drafts are excluded from automatic updates, and publication does not enable automatic downloads or silent installation. macOS remains ad hoc signed and not Apple-notarized; Windows is unsigned by Authenticode and remains preview support. No Linux packages. **Full real-device, native installation/upgrade, device-matrix and production-cloud acceptance is not complete.** Never post passwords, tokens, API keys or the full configuration directory in feedback.

[Installation](https://github.com/tonyflux/pkp/blob/v0.4.0/docs/INSTALL.en.md) · [Usage](https://github.com/tonyflux/pkp/blob/v0.4.0/docs/USAGE.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.4.0/PRIVACY.en.md) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.3.0...v0.4.0)
