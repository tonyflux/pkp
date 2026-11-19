# PKP 0.7.1

[简体中文 / Bilingual release notes](release-notes.md)

This patch improves crash diagnostics and fixes native player-window lifetime risks on macOS.

- Unhandled Go panics, runtime fatal errors and recorded startup/Wails fatal errors produce a local `PKP-Crash-*.log` on the desktop, or in the system cache if the desktop is unavailable. Normal exits do not produce reports; forced termination, some native-library failures and very early startup failures may not produce one either.
- On the next launch, you can review and edit an available report before choosing whether to send it. Nothing is uploaded unless you select **Send report**. Don't send or closing keeps the local file and stops reminders for that report; sending failures can be retried manually. Feedback is independent of the anonymous usage-statistics setting.
- Reports may contain filenames, paths, links, account details or tokens. Remove sensitive information before sending. The report is processed by Cloudflare and accessible to the developer; a daily cleanup is configured for reports older than 29 days, though failures can delay deletion. Sent reports cannot be automatically recalled.
- Fix a macOS native-window lifetime race when reshaping the separate player during close or shutdown, and an error-message lifetime issue in the video surface. These are confirmed code risks; the cause of an individual reported crash still requires its diagnostics.
- The crash-report and usage-statistics dashboards now adapt to mobile screens, including report review, pagination and touch inspection of statistics charts.

The release contains eight assets: macOS Universal and Windows x64/ARM64 installers, signed update manifest and signature, `PKP-0.7.1-source.tar.gz`, `libmpv-sources.tar.gz`, `LICENSE` and `SHA256SUMS`. macOS is ad hoc signed and not Apple-notarized; Windows lacks Authenticode signing and remains preview support. Native installation and upgrade, real-device matrix and production-cloud acceptance have not been completed. No Linux packages are provided.

[Installation](https://github.com/tonyflux/pkp/blob/v0.7.1/docs/INSTALL.en.md) · [Usage guide](https://github.com/tonyflux/pkp/blob/v0.7.1/docs/USAGE.en.md#settings-and-quitting) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.7.1/PRIVACY.en.md#signing-out-deletion-and-issue-reporting) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.7.0...v0.7.1)
