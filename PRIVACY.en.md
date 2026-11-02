# Privacy Notice

[简体中文](PRIVACY.md) · English

This notice covers the current PKP implementation and the released v0.1.0. It should be reviewed against the actual installation package and service configuration before subsequent releases. This document makes no guarantees about external services’ data retention periods or security.

## What Is Stored Locally

By default, PKP uses a `pkp` subdirectory in the current user’s configuration directory: typically `~/Library/Application Support/pkp` on macOS and `%AppData%\pkp` on Windows. It may contain account lists, session tokens, device identifiers, file indexes, filenames and paths, transfer and playback history, synchronization status, AI conversations and organization records, settings, and wallpapers. These are not ordinary diagnostic files suitable for public upload.

- When “Remember account and password” is unchecked, the login password remains only in process memory. When checked, it is stored in the macOS login Keychain or Windows Credential Manager, not in ordinary settings files. Currently, “Remember account and password” and “Auto login” are checked by default; you can uncheck them before logging in.
- After login, access / refresh tokens are still stored in local account credential JSON files; **not remembering a password does not mean there are no local session credentials, nor does it mean the entire configuration directory is encrypted**.
- AI API Keys, TMDB credentials, and custom proxy passwords use the system credential store; macOS uses the service identifier `com.pkp.desktop`. The AI and TMDB settings screens display only masked hints, not complete credentials.
- Downloaded files and player screenshots are saved in user directories, and the app lock does not encrypt them. The app lock restricts access within the application; it is not a substitute for operating system account protection or disk encryption.

## PikPak, Files, and Referral Relationships

Features such as login, session renewal, browsing, sharing, transfers, cloud adding, synchronization, and online archive extraction send requests to PikPak and its file storage/CDN according to the operation performed. Login credentials are submitted to PikPak’s login service; file contents are transmitted over the corresponding network paths during playback, previewing, uploads, downloads, and synchronization checks. Passwords for online archive extraction are sent to the extraction service when needed and should not be treated as passwords used only locally.

**Currently, after every successful login through the login form (including adding an account and logging in again), PKP submits a developer-preset invitation code to PikPak in the background to attempt to establish a referral relationship; there is currently no separate toggle for this.** This may have no effect if a relationship already exists or the service rejects it; failure does not affect login. Simply restoring a session on startup or refreshing a token does not trigger this submission. The registration entry point also includes promotional parameters that may result in referral attribution. Please understand this behavior before use; it is not a privacy authorization required for basic cloud drive operations.

Creating a share allows people who obtain its link or passcode to access the content according to server-side rules. Do not publicly disclose private share links, passcodes, or signed download URLs.

## Optional AI and TMDB

Local index searches, rule parsing, and local statistics do not require sending filenames to a model. After you configure and use AI, requests are sent to your chosen Anthropic or OpenAI-compatible service (which may also be a compatible local service). They may include your questions, conversation context, relevant filenames, folder paths, sizes, modification times, and subtitle text you have authorized, among other content.

Before sending, authorization prompts are displayed according to the task and data level. If you have selected “Always allow” for that account, you will not be asked each time for the corresponding level afterward; you can manage authorizations in “Settings → AI.” Folder names within paths may themselves reveal private information. Write operations separately require review and confirmation of a plan; authorizing data to be sent to a model does not mean the model’s judgments are reliable. The model service determines its data use, retention, and charges; HTTPS is recommended for services on the public internet.

TMDB title-matching requests are sent to the TMDB API only after users configure their own TMDB credentials. They do not use shared developer credentials or a PKP TMDB proxy. Requests include the title query used for matching, the language, and credentials; no requests are made to TMDB when it is not configured. Matching information returned by TMDB, such as titles, is not sent directly as model context, but content that has become part of users’ directory names may still be sent as part of paths.

## Usage Statistics

The released v0.1.0 production installers enable usage statistics by default; you can disable them in “Settings → Privacy & Security.” Published v0.2.0 and current source default to off, and counting and sending start only after you enable them there. Older versions omitted the field when enabled, so an explicit enable cannot be distinguished from the old default. Upgrades therefore turn statistics off and require a new opt-in; the app attempts to delete old unsent local counts and the installation ID on startup. The production default collection endpoint, including for source builds made with `make app-go`, is `https://telemetry.pkpapp.cc/v1/usage`, hosted on Cloudflare. Development and test builds have no default endpoint; `PKP_TELEMETRY_API` can override it, but statistics still require opt-in.

The statistics payload includes a random installation ID, app version, operating system, architecture, interface language, and fixed feature names with usage counts aggregated by local date (for example, counts of launches, page visits, playback, downloads, searches, or AI questions). **The payload does not include accounts, PikPak user IDs, filenames, paths, search terms, question text, or file contents.** The random ID is unrelated to accounts, but it allows statistics from the same installation to be linked across multiple days; it does not mean that every request is unlinkable.

Counts are first stored locally in `telemetry.json`. The first send attempt occurs approximately 30 seconds after startup, followed by approximately hourly attempts; local records awaiting transmission are retained for no more than 30 days. The server updates daily statistics by installation ID and date. The receiving endpoint uses IP addresses for rate limiting, and the current statistics database fields do not store IP addresses. Network service providers still process connection information such as IP addresses, so this is not a basis for guaranteeing that no infrastructure logs exist.

The receiver does not authenticate installations. Rate limiting does not prevent forged reports or impersonation of installation IDs, so server statistics are not tamper-proof.

Turning the toggle off stops subsequent counting/sending and deletes the local statistics file and installation ID. Turning it on again generates a new ID; **this does not automatically delete statistics that have already reached the server, nor can it retract requests already sent**. There is currently no server-side automatic deletion period or user self-service deletion endpoint that can be promised on this basis.

## App Update Checks

Official production builds that support in-app updates enable automatic checks by default. Approximately 30 seconds after startup, and then at most once every 24 hours, they query GitHub’s public `tonyflux/pkp` repository for the latest stable Release. You can disable this in “Settings → About & Updates”; “About & Updates” is also accessible before login. Manual checks still send queries. This toggle is independent of anonymous usage statistics; disabling statistics does not stop update checks.

Checks request the GitHub API, update manifest, and signature. The installation package is requested from GitHub / its attachment CDN only after the user clicks Download; it is not downloaded or installed automatically. Update requests do not carry PKP accounts, file contents, PikPak tokens, or the statistics installation ID. GitHub, CDNs, and network service providers still process connection information such as IP addresses and request times, subject to their respective privacy policies; the absence of access logs cannot be guaranteed.

Local settings store the toggle state, last check time, ignored version, and reminder time for postponement. Installation packages are temporarily stored in a PKP-specific directory in the system cache; temporary downloads for the current configuration are cleaned up on exit or a subsequent startup. Installing over an existing installation does not proactively delete accounts, configuration, or system-stored credentials; any future data format migrations must be assessed according to the release notes for the actual version.

## Signing Out, Deletion, and Issue Reporting

Using “Sign out and remove” for an account clears its local credentials, remembered login password, and corresponding records such as indexes, transfers, and AI records, with related synchronization relationships handled first. It does not close the cloud account, delete cloud files, or revoke shares. You must not assume that global settings, independently configured service keys, downloaded files, and screenshots are all deleted when an account is removed; service keys can be removed separately in their corresponding settings. Uninstalling the application should likewise not be treated as having cleared all local or external service data.

Even when some credentials have been masked, local logs and error messages may still contain account identifiers, private filenames, paths, or content returned by services. Before posting feedback publicly, inspect logs and screenshots item by item and remove passwords, tokens, API Keys, share passcodes, private links, and personal information; do not upload the entire configuration directory. The feedback repository is <https://github.com/tonyflux/pkp>; public Issues are not suitable for submitting sensitive information.
