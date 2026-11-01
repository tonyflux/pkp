# Changelog

[简体中文](CHANGELOG.md) · English

## Unreleased

- Make the English project overview the default `README.md`, retain Simplified Chinese as `README.zh-CN.md`, and update language-switching and documentation links.

## First Open-Source Version v0.2.0 · Before Official Release

- The local public repository now includes the application source code, frontend, build scripts, and statistics service code, and has switched to GPL-3.0-only. The proprietary terms for the v0.1.0 installation packages remain in `docs/legal/v0.1.0-proprietary.txt`; the license change is not retroactive.
- Following review, editor state, build caches, private Git history, and internal release execution records were excluded. Invitation-code behavior is retained.
- The maintainer has confirmed that the current Android client identity parameters may be published and reused; authentication values, the signing algorithm, and referral behavior are unchanged. Public comparisons do not imply PikPak endorsement. Before the first open-source release, local history is reconstructed from the online documentation changes using GitHub noreply commit identities, without importing private source history.
- Internationalization cleanup: code comments, test names and developer diagnostics, build output, Issue templates, macOS installation assets, internal model instructions, and the statistics dashboard use English. Public guides now have linked English and Chinese versions. The upload drop hint uses the existing translation system; model replies still follow the interface language. Chinese parser grammar, naming presets, persisted labels, Unicode/localization fixtures, and historical legal terms remain intact; no new scan suppressions were added.
- Aligned packaging metadata for the first open-source release, 0.2.0, and documented build prerequisites, contributions, security reporting and release signing. CI builds installers and checks the statistics service. Installation packages are offered as a draft for manual acceptance, not an official release.
- The first open-source release uses eight assets, adding `PKP-<version>-source.tar.gz` with the complete PKP-owned source and build scripts corresponding to the installers; `libmpv-sources.tar.gz` continues to provide third-party corresponding source separately. The historical seven v0.1.0 assets and terms are unchanged.
- Usage statistics require opt-in; upgrades from older versions require a new choice and attempt to remove unsent local counts. The server restricts feature names, but anonymous reports can still be forged. Invitation behavior is unchanged.
- Installers now include Go and frontend dependency notices. The current source has removed `pptx-preview`, whose license conflict no longer blocks the current release; this does not resolve obligations for historical copies or existing distributions. Artwork provenance still needs verification.
- The file grid and filters adapt to available space at different display scales; short windows can scroll the workspace while keeping playback controls visible, without resetting sidebar choices.
- Remove remaining design-document titles, identifiers, and section references from source comments and test descriptions while retaining functional explanations and runtime behavior. Referral codes, referral binding, and authentication parameters are unchanged.
- Add fixed public-source comparisons for authentication identity parameters, signing salts and the algorithm, with explicit limits on version-marker provenance and authorization. Correct comments claiming that the default identity is the only usable one without changing authentication values, signing, login or referral behavior. Public comparisons do not establish redistribution permission or original provenance.
- Remove built-in PPT/PPTX previews, the `pptx-preview` dependency, slide parsing, and the speaker-notes interface; retain DOCX, PDF, XLSX, and other previews. PPT/PPTX remain classified as documents and use the existing unsupported-preview screen, with downloads and other operations unchanged.
- Record the maintainer's confirmation of permission to publish and distribute the current application mark. Retain the unofficial-client notice, artwork provenance and factual export-chain limits without presenting this confirmation as an independent legal audit.

## 0.1.0 · 2026-10-25

The first official version has been released: [Downloads and update notes](https://github.com/tonyflux/pkp/releases/tag/v0.1.0). It provides a macOS Universal installation package, a shared Windows x64 / ARM64 installation package, a signed update manifest, corresponding source code for third-party components, terms of use, and hash verification files.

- File workspace, local index search, file previews, audio and video playback, and multi-account transfer management.
- In-app checks for official updates, with verification of the Ed25519-signed manifest and installation packages; automatic checks can be disabled, and updates are not downloaded or installed automatically.
- Reused the draft attachments already accepted by the user, without rebuilding or re-signing them for release. Public anonymous download and signature verification checks passed after release.
- macOS still uses ad hoc signing and is not notarized by Apple; Windows has no Authenticode signature. Device and feature validation coverage still needs to be expanded; no installation packages are provided for Linux.

## Pre-Release Documentation and Tooling Records

The following records describe documentation, screenshot, and tooling preparations before release. They do not indicate that every demonstrated scenario underwent production-environment acceptance testing.

- Added an English project overview and links for switching between Chinese and English.
- Clarified the commitment to a future full open-source release: once usage targets are met and sensitive-information cleanup, third-party license checks, and build documentation preparation are complete, the full source code, build scripts, and developer documentation for PKP's own application will be made available—not just selected modules or a stripped-down version. Specific metrics, thresholds, and arrangements are to be announced; the current license remains unchanged.
- Reorganized the product introductions in the Chinese and English READMEs, adding actual interface screenshots of the file workspace, local index search, and privacy settings. The screenshots use synthetic example accounts and data in a locally isolated environment, contain no real user content, and do not indicate that production cloud operations have been verified. Added a feature overview, links to guides, and privacy notices, while retaining the unreleased status, platform limitations, and plan to make the complete source code available.
- Expanded the Chinese and English READMEs: added the application logo and separate introductions and images for the built-in player, AI assistant, and folder synchronization. Explained how the player demonstration frames were synthesized, that no model inference was performed on the assistant home page, and that synchronization only showed a scan preview of local synthetic test data and was not executed. Added details on authorization, operation confirmation, deletion protection, and operational boundaries; documentation updates do not mean that a version or installation package has been released.
- Added an Ed25519 signature verification tool for public Releases, completeness checks for the seven attachments, and a post-release anonymous download verification workflow; drafts do not enter the application's automatic update-check channel. Installation packages had still not been officially released.
- Established update-signing trust before the first public release and published the signature verification public key at the same time. No update clients had yet been distributed; older local test builds needed to be replaced manually, and no installation packages had been released.

In-app updates use official GitHub Releases and signed manifests, with support for viewing release notes, canceling downloads, and user-confirmed installation. macOS continues to use manual replacement via DMG, while the Windows installer provides existing-directory detection and process protection. The user has reported that draft acceptance testing passed; a complete platform/device matrix has not been provided, and this release is not regarded as completing validation for all scenarios or official platform signing and notarization.
