# PKP 0.4.1

[简体中文 / Bilingual release notes](release-notes.md)

Refined About & Updates layout, stationary glass shell when signed out, and safe Markdown rendering for release notes.

## Highlights

- **About & Updates top card**: show check results, release version, download progress, and download/install actions together in the top version card. The default window exposes the main update action and the start of the release notes without scrolling; long notes scroll with the page, followed by automatic-check settings and community links. Signed-in, signed-out, notification, and menu entries return to the result area at the top while preserving signature verification, skipped versions, installation confirmation, all five UI languages, and light/dark themes.
- **Signed-out update shell & Markdown release notes**: keep the glass rim on a stationary outer shell while scrolling long content inside the inner body so border decoration no longer crosses text. Render release notes with the existing safe Markdown reader (headings, lists, emphasis, and code); open external links only on explicit clicks without loading remote images or interpreting raw HTML.
- **Includes v0.4.0 capabilities**: default-off read-only LAN sharing (with Windows first-time initialization and drive-alias resolution fixes), PikPak Share Import previews and desktop clipboard detection, external `magnet:` and `.torrent` opening, unified grid/list pickers across file/folder dialogs, and desktop file context menus and keyboard shortcuts.

## Boundaries and privacy (unchanged policy)

LAN pages use **HTTP on trusted networks only**; do not expose them through public port forwarding. QR codes exclude passwords. HTTP is unencrypted, and a separate password does not replace a trusted network.

**Daily active installation statistics have defaulted to ON since v0.3.0; v0.4.1 does not change this** (v0.2.0 defaulted to OFF). Identifiable explicit opt-outs are preserved; settings without an explicit choice follow the on default, and old saves without refusal markers cannot be distinguished from no choice. With an endpoint, recording starts at startup and sending can begin about 30 seconds later, before notice acknowledgment; without one, nothing is recorded/sent and no notice appears. Got it or dismissal acknowledges the notice, not consent, and leaves statistics enabled.

Reports contain a random installation ID, version, OS/architecture, language and active local dates—not feature actions, frequencies, logs, accounts, filenames, paths, searches or file contents. Turn them off in Settings → Privacy & Security, also available before sign-in. Opt-out deletes the local ID and pending reports, not server history. Counts reflect successfully reporting active installations, not verified people or exact installs; publishing the app does not deploy telemetry services.

## Release assets, verification and limitations

The release asset set has **eight items**: macOS Universal installer (macOS 12+, Apple silicon/Intel), combined Windows x64/ARM64 installer, update manifest and signature, `PKP-0.4.1-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE` and `SHA256SUMS`. PKP-owned source is GPL-3.0-only; historical licensing is not changed retroactively. PKP is an unofficial client.

Keep the existing Ed25519 update-signing trust and public key; no trust reset. Drafts are excluded from automatic updates, and publication does not enable automatic downloads or silent installation. macOS remains ad hoc signed and not Apple-notarized; Windows is unsigned by Authenticode and remains preview support. No Linux packages. **Full real-device, native installation/upgrade, device-matrix and production-cloud acceptance is not complete.** Never post passwords, tokens, API keys or the full configuration directory in feedback.

[Installation](https://github.com/tonyflux/pkp/blob/v0.4.1/docs/INSTALL.en.md) · [Usage](https://github.com/tonyflux/pkp/blob/v0.4.1/docs/USAGE.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.4.1/PRIVACY.en.md) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.4.0...v0.4.1)
