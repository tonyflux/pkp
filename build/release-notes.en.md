# PKP 0.2.0

[简体中文](release-notes.md) · English

Planned first open-source release; these notes do not imply that 0.2.0 or its installation packages have been published. PKP's own source is available under GPL-3.0-only; the terms of the previously released v0.1.0 packages remain unchanged. This release must provide eight assets, including the complete PKP-owned source corresponding to the installers as `PKP-0.2.0-source.tar.gz`; `libmpv-sources.tar.gz` separately provides third-party corresponding source.

- File workspace, local index search, previews, playback, transfers, and multiple accounts.
- Signed update manifests and package integrity verification; updates are not downloaded or installed automatically.
- Planned packages: macOS Universal (macOS 12 or later) and a combined Windows x64 / ARM64 installer.
- Usage statistics now default to off; after upgrading from an older version, opt in again under Privacy settings.
- Removed built-in PPT/PPTX previews and the `pptx-preview` dependency; DOCX, PDF, XLSX, and other previews remain. PPT/PPTX remain classified as documents and use the existing unsupported-preview screen, with downloads and other operations unchanged.

## Known limitations

- macOS builds use ad hoc signing and are not notarized; Windows builds have no Authenticode signature. Physical-device acceptance coverage remains incomplete.
- Linux installation packages are not provided.

[Installation guide](https://github.com/tonyflux/pkp/blob/main/docs/INSTALL.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/main/PRIVACY.en.md)
