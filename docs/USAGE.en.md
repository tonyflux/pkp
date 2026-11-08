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

## Playback and Reading

Open a video or audio file to enter the built-in player. Depending on the file and service availability, you can switch quality levels, select subtitles, resume from your last playback position, or move playback to a separate window. Previews include text, images, PDF, DOCX, and XLSX. PPT/PPTX have no built-in preview; they remain classified as documents and show the existing unsupported-preview screen, with downloads and other operations unchanged. Network conditions, account permissions, server-side rate limits, and file encoding all affect playback; player failures may affect the entire application.

## Folder Sync and Organization

“Sync” lets you create relationships for two-way sync, local-to-cloud backup, or cloud-to-local download. Confirm the directories on both ends and the mode first, then review the scan preview before proceeding. Content verification may read cloud file bytes and consume download traffic. Sync is neither a real-time service nor a system-level background daemon; it works only while the PKP process is running.

Paused sync does not automatically resume when network connectivity is restored. Removing a relationship preserves files already completed on both ends; if an operation is awaiting confirmation, handle it through the interface first. Sync, batch renaming, cleanup, and AI organization can all modify files. Back up important content before proceeding, and do not treat “Undo” as a complete backup.

The AI assistant’s local rules and statistics can be used without configuring a model. Connecting a model or TMDB is optional; use your own service addresses and credentials. Read the data authorization notice before sending data, and inspect the actual file operation plan before execution. AI may make incorrect judgments, and model costs are determined by the selected service. See the [Privacy Notice](../PRIVACY.en.md) for details.

Plan preview groups AI review and cleanup actions together and shows the first few files and keep reasons immediately. “Review full plan with AI” covers all candidate groups, including rule-matched items; identifying only the remaining groups is still available to reduce the data sent and model usage. Full-plan review sends filename patterns and prior rule- or AI-generated identifications, not paths, sample files, or titles and years returned by TMDB. AI does not read actual file contents and only updates the suggestion preview. Manual names and existing operation selections are retained, but newly proposed moves to Trash start unselected. Reviews of conversation-backed plans are saved and retained on reopening. Execution still requires final confirmation.

Conversation search results offer two separate actions: “Organize and name” classifies media, moves files, and tidies names; “Clean up N displayed items” creates a cleanup preview only for the displayed, deduplicated results, without rescanning or automatically deciding which files are ads. Cleanup candidates start unselected. Confirm individual items to keep, or select them and use the final confirmation to move them to Trash. Permanent deletion is not available; unselected items are not cleaned up. Moving a folder includes all its contents, so review it first. Creating this cleanup preview does not make another model request.

Historical conversations remain usable. An open organization plan offers “Cleanup preview · N items,” including plans where everything is kept as is. Only the plan’s listed candidates enter cleanup; source folders inferred to become empty are not converted into whole-folder deletions. In an old message, you can also expand “Search process → View this search’s results” and clean up the loaded results directly. Neither path requires another prompt. Candidates start unselected, Back returns to the original conversation, and old organization plans are never automatically converted to deletion plans.

When a query is blocked because the local index is not ready, the latest safely retryable reply shows a wait notice, current index progress, and “Retry original question.” The button stays disabled until the index is ready. Clicking it reuses the original question and scope and follows the existing data-authorization flow, with no retyping required. Completing the index never automatically sends a model request. The same entry point recognizes saved index-not-ready tool errors in historical replies.

Gemini API: choose “Google Gemini” under the provider presets in “Settings → AI”, create your own API key in [Google AI Studio](https://aistudio.google.com/api-keys), save it, refresh the model catalog, and test the connection. The assistant’s connection sheet offers the same preset. Settings displays the complete model list returned by the current service, without a local allowlist or removing or rewriting prefixes such as `models/`. Selecting a model saves and sends its original ID. Preset model names are initial values, not a guarantee of account access; prefer the actual IDs in the official catalog. Catalog entries may be inaccessible to the account or unsupported by the chat endpoint; use Test Connection to check permissions and quota. Manual entries and saved selections are preserved without automatic replacement or per-model probes. Requests use Google’s official OpenAI-compatible endpoint at `https://generativelanguage.googleapis.com/v1beta/openai`. API quotas and billing are separate from Google AI Pro / Ultra subscriptions; Google account or subscription OAuth sign-in is not supported.

Newly added files trigger reminders only when actionable organization suggestions are available, consistently across the assistant home, navigation badge, and menu bar. Files without suggestions remain unprocessed; they are not automatically dismissed or marked as organized, and are checked again after index or naming-setting changes. Background checks do not automatically call a model.

## Settings and Quitting

Before signing in, open Privacy from the login page to turn off Send anonymous usage statistics in local settings. You can also switch to About & Updates there; neither requires a PikPak sign-in.

“Settings” lets you adjust the language, appearance, transfers, the application’s own network proxy, privacy and security, and other options. Proxy settings do not modify the system VPN or system proxy. On macOS 12/13, WebView image loading does not support the same proxy configuration as the application and may still connect directly.

On macOS, closing the main window only hides it; menu bar residency and background transfers for each account may continue. Use “Quit PKP” or `⌘Q` to fully quit. App lock is disabled by default. Locking does not stop already authorized background transfers or sync, nor does it encrypt downloaded files or the entire local data directory.

Before reporting an issue, redact account information, filenames, paths, share links, tokens, and API keys. Do not upload the entire configuration directory or raw logs.
