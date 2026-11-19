# PKP 1.0.0

[简体中文 / Bilingual release notes](release-notes.md)

PKP 1.0.0 brings a refined workspace and Linux desktop builds, alongside playback, account browsing and reliability improvements since 0.7.1.

- A softer theme-adaptive mist backdrop, refined light and dark workspaces, clearer file cards, navigation, refresh icon and playback controls. Custom wallpapers, panel opacity and reduced-transparency settings remain available.
- Browse locally indexed files across signed-in accounts, with account ownership and incomplete-index warnings; drives and playback progress remain separate.
- Refined video and music playback, file previews, queues and collapsed playback controls, including previous/next video actions and retained subtitle membership and quota notices.
- Linux x86_64 and ARM64 builds provide deb, rpm and AppImage packages, native playback, safe synchronization and LAN sharing. Linux updates are verified before manual installation or replacement.
- Fix app-lock authentication bypass risks, video-surface shutdown deadlocks, Linux proxy and portal issues, and other platform reliability problems. Sign-in no longer submits developer referral attribution; previously established relationships are unchanged.

This draft is for validation before publication. The planned 16 assets include macOS Universal and Windows x64/ARM64 installers, signed update manifest and signature, source and media-source archives, license, checksums, and Linux deb/rpm/AppImage and runtime sources for both architectures. macOS is ad hoc signed and not Apple-notarized; Windows lacks Authenticode signing and remains preview support. Linux needs a compatible desktop, keyring and authentication agent; standalone AppImage does not install system polkit policy and cannot guarantee screenshot protection. Installation, upgrade and real-device acceptance remain to be verified.

[Installation](https://github.com/tonyflux/pkp/blob/v1.0.0/docs/INSTALL.en.md) · [Usage](https://github.com/tonyflux/pkp/blob/v1.0.0/docs/USAGE.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v1.0.0/PRIVACY.en.md) · [Changes since 0.7.1](https://github.com/tonyflux/pkp/compare/v0.7.1...v1.0.0)
