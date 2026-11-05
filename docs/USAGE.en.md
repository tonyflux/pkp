# Usage Guide

[简体中文](USAGE.md) · English

This document describes current product behavior and does not imply that a public release is available. See the [Installation Guide](INSTALL.en.md) for installation status and platform limitations.

## Accounts and Files

Sign in with your own PikPak account. QR sign-in is selected by default: scan the code with the PikPak app and confirm on your phone to enter your drive. Refresh an expired code to try again. Adding accounts and reauthentication also support QR sign-in; when reauthenticating, select the matching account on your phone. QR codes contain temporary authorization information; do not publish screenshots or forward them.

If a confirmation request is already in progress when the QR countdown ends, the page waits for its final result: success completes sign-in; otherwise it offers a refresh. Closing the sign-in view or changing sign-in methods still cancels the flow.

You can switch to “Password.” Its “Remember account password” option saves your password in the system credential store, and “Auto sign-in” depends on it. Both are selected by default and can be deselected before signing in. QR sign-in needs no password and leaves an existing account’s remembered-password settings unchanged. The resulting session is still stored locally and renewed with a refresh token. A revoked session requires renewed authorization; existing password auto-sign-in preferences continue to apply.

Use the account entry in the lower-left corner to add, switch, and manage accounts; switching the account you are browsing does not stop local transfers already running for other accounts. Accounts are not merged into a single cloud drive. Signing out and removing an account clears the corresponding local session and records. It does not delete your PikPak account, delete its cloud files, or revoke its shares; cloud-add tasks may continue running on the server.

The Files page supports folder browsing, filtering by type, searching, starring, sharing, and previews. Building the initial local index may take some time. After you create a share, anyone who obtains the link or access code may be able to access the shared content. Configure sharing according to your actual needs and revoke shares promptly when they are no longer needed.

The file grid and filters adapt to the space available in the window. In short windows the workspace can scroll while the playback bar stays visible. This does not change system display scaling or reset your sidebar and information-panel choices.

## Uploads, Downloads, and Cloud Add

- Use “New” to upload files, upload folders, or create a cloud folder. You can also drag local files onto the Files page.
- Choose “Download to computer” from a file’s More menu or the multi-selection action bar. The default location is the system Downloads directory, which can be changed under “Settings → Transfers.” Files with the same name are distinguished by numbering rather than directly overwriting existing files.
- Use “Cloud Add” in the top bar to submit links or torrents you are authorized to use. Task execution, availability, and quotas depend on the PikPak service.
- View progress in the “Transfer Center” and pause, resume, or retry tasks according to the operations each task supports. Quitting the application stops transfers on this computer but does not necessarily cancel cloud tasks.

### Open from a browser or this computer

- After installing the new PKP build, select it as the handler for `magnet:` links and `.torrent` files. Clicking a magnet link on a website or double-clicking a torrent starts PKP, or delivers it to the existing window when PKP is running.
- PKP opens the Cloud Add confirmation sheet; it never creates a task automatically. Magnets prefill the Links tab. Torrents are parsed locally so you can select files and confirm the current account and destination. This adds content to PikPak, not to a local BitTorrent downloader.
- Sign in or unlock first if necessary. The Links and Torrent tabs retain their inputs independently: an existing torrent does not block a new magnet from appearing on Links, and its file selection is preserved; opening a torrent also preserves existing links. Both tabs share the current destination. Requests of the same type wait in order behind that tab's unsubmitted input for the current run; submit or clear that input to continue. Requests wait while a torrent is being read or input is being submitted.
- To discard links, choose “Clear links” or edit/delete the text. “Cancel” discards the current tab's links or torrent and closes the sheet without changing the other tab or immediately opening the next request. The Close button or clicking outside the sheet still preserves drafts. Reopening Cloud Add or opening another external link/file resumes pending requests. Duplicate pending deliveries are coalesced so deleting input does not bring the same request back; deliberately reopening a discarded link still works. Quitting PKP does not persist unsubmitted requests.
- macOS: install the new `.app` in Applications and launch it once so the system discovers its handlers. For torrents, use Finder → Get Info → Open with → PKP → Change All. Magnet links follow the current system/browser protocol handler; PKP registration does not take over another app's existing default, so select PKP instead if necessary. Your browser may still ask for permission to open an external app.
- Windows: run the new installer, then choose PKP for `.torrent` and `MAGNET` under Settings → Apps → Default apps. Installation and uninstallation maintain PKP's candidate registrations without rewriting the user's default choices. Running a portable `.exe` alone does not register associations.

## Playback and Reading

Open a video or audio file to enter the built-in player. Depending on the file and service availability, you can switch quality levels, select subtitles, resume from your last playback position, or move playback to a separate window. Previews include text, images, PDF, DOCX, and XLSX. PPT/PPTX have no built-in preview; they remain classified as documents and show the existing unsupported-preview screen, with downloads and other operations unchanged. Network conditions, account permissions, server-side rate limits, and file encoding all affect playback; player failures may affect the entire application.

## Folder Sync and Organization

“Sync” lets you create relationships for two-way sync, local-to-cloud backup, or cloud-to-local download. Confirm the directories on both ends and the mode first, then review the scan preview before proceeding. Content verification may read cloud file bytes and consume download traffic. Sync is neither a real-time service nor a system-level background daemon; it works only while the PKP process is running.

Paused sync does not automatically resume when network connectivity is restored. Removing a relationship preserves files already completed on both ends; if an operation is awaiting confirmation, handle it through the interface first. Sync, batch renaming, cleanup, and AI organization can all modify files. Back up important content before proceeding, and do not treat “Undo” as a complete backup.

The AI assistant’s local rules and statistics can be used without configuring a model. Connecting a model or TMDB is optional; use your own service addresses and credentials. Read the data authorization notice before sending data, and inspect the actual file operation plan before execution. AI may make incorrect judgments, and model costs are determined by the selected service. See the [Privacy Notice](../PRIVACY.en.md) for details.

Gemini API: choose “Google Gemini” under the provider presets in “Settings → AI”, create your own API key in [Google AI Studio](https://aistudio.google.com/api-keys), save it, refresh the model catalog, and test the connection. The assistant’s connection sheet offers the same preset. Settings displays the complete model list returned by the current service, without a local allowlist or removing or rewriting prefixes such as `models/`. Selecting a model saves and sends its original ID. Preset model names are initial values, not a guarantee of account access; prefer the actual IDs in the official catalog. Catalog entries may be inaccessible to the account or unsupported by the chat endpoint; use Test Connection to check permissions and quota. Manual entries and saved selections are preserved without automatic replacement or per-model probes. Requests use Google’s official OpenAI-compatible endpoint at `https://generativelanguage.googleapis.com/v1beta/openai`. API quotas and billing are separate from Google AI Pro / Ultra subscriptions; Google account or subscription OAuth sign-in is not supported.

Newly added files trigger reminders only when actionable organization suggestions are available, consistently across the assistant home, navigation badge, and menu bar. Files without suggestions remain unprocessed; they are not automatically dismissed or marked as organized, and are checked again after index or naming-setting changes. Background checks do not automatically call a model.

## Settings and Quitting

Before signing in, open Privacy from the login page to turn off Send anonymous usage statistics in local settings. You can also switch to About & Updates there; neither requires a PikPak sign-in.

“Settings” lets you adjust the language, appearance, transfers, the application’s own network proxy, privacy and security, and other options. Proxy settings do not modify the system VPN or system proxy. On macOS 12/13, WebView image loading does not support the same proxy configuration as the application and may still connect directly.

On macOS, closing the main window only hides it; menu bar residency and background transfers for each account may continue. Use “Quit PKP” or `⌘Q` to fully quit. App lock is disabled by default. Locking does not stop already authorized background transfers or sync, nor does it encrypt downloaded files or the entire local data directory.

Before reporting an issue, redact account information, filenames, paths, share links, tokens, and API keys. Do not upload the entire configuration directory or raw logs.
