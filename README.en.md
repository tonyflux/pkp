<div align="center">

# PKP

**Your cloud files, transfers, and playback. One desktop workspace.**

[简体中文](README.md) · English

[User guide](docs/USAGE.md) · [Releases](https://github.com/tonyflux/pkp/releases) · [Report an issue](https://github.com/tonyflux/pkp/issues)

</div>

PKP is a **closed-source, unofficial desktop client for PikPak**, built for managing your own cloud files, transfers across multiple accounts, and media playback. PKP is not affiliated with or endorsed by PikPak. Use remains subject to PikPak's terms of service and applicable laws.

> **Not released yet** · The first release has not been published, and no official installation packages are available. This repository provides public documentation, issue reporting, and a future download destination—not PKP application source code or build tools. Packages and release notes will appear in [Releases](https://github.com/tonyflux/pkp/releases) once published.

## A workspace for everyday file management

Browse your cloud drive, switch accounts, manage uploads and downloads, then preview or play a file without moving between separate tools.

![PKP file workspace with a Chinese interface and synthetic sample data](assets/screenshots/workspace.png)

*About the screenshots: all three images show the actual application running locally in an isolated environment with synthetic sample account and file data. The interface is in Simplified Chinese. They contain no real user accounts or content and are not evidence of verified production cloud operations.*

## Find your files faster

Search your own cloud files through a local index, with sorting, type filters, and favorites to keep everyday content within reach. The initial index takes time to build; search and drive-wide statistics may need to wait until it is ready. Local indexed search does not require sending file names to an AI model.

![PKP local indexed search with synthetic sample data](assets/screenshots/search.png)

## What you can do

| Your task | What PKP provides |
| --- | --- |
| Manage multiple accounts | Sign in and switch accounts, with independent background uploads and downloads. Accounts are not merged into a single drive. |
| Browse and share | Folder browsing, local indexed search, sorting, filters, favorites, sharing, and image, text, and PDF previews. |
| Manage transfers | Add links or torrents for cloud download, upload local files and folders, download to your computer, and manage transfer progress. |
| Play media | An integrated player with quality switching, subtitles, playback history, and a separate player window. Availability depends on the file and service. |
| Sync and organize | Two-way folder synchronization, local-to-cloud backup, and cloud-to-local downloads, with previews and conflict handling before execution; cleanup, rule-based renaming, and media organization. Optional connections to your own AI model service and TMDB credentials. |
| Make it yours | Simplified Chinese, Traditional Chinese, English, Japanese, and Korean interfaces, plus themes and wallpaper settings. |

Cloud capabilities, network access, and quotas depend on PikPak's service and your account. PKP does not provide content resources or bypass server-side permissions or Premium restrictions. Synchronization only runs while the PKP process is running; it is not a real-time service or a system-level background daemon. Back up important files and review operation previews before synchronization, cleanup, or bulk organization.

## Understand privacy before signing in

Privacy-related options are available in settings. The following behavior is also important to understand before using PKP.

![PKP privacy and security settings in the isolated sample environment, with a Chinese interface](assets/screenshots/privacy.png)

- **Usage statistics are on by default and can be disabled.** Production builds send usage statistics by default; turn them off under Settings → Privacy & Security without affecting normal use. Payloads do not contain accounts, file names, paths, search terms, question text, or file contents. A random installation identifier can link statistics from the same installation across days. Turning statistics off does not automatically delete data already received by the server.
- **Update checks have a separate switch.** Production builds enable update checks by default; disable them separately under About & Updates. Disabling usage statistics does not disable update checks. Packages are not downloaded or installed automatically.
- **Sign-in includes a referral submission.** After each successful form-based sign-in, including adding an account or signing in again, PKP submits a preset developer referral code to PikPak in the background to attempt to establish a referral relationship. **There is no separate opt-out switch.** Failure does not affect sign-in; restoring a session or refreshing a token does not trigger submission. The registration link also includes promotional parameters that may create referral attribution.
- **AI and TMDB connections are optional.** Use your own services and credentials, and review data authorization prompts before sending information. AI requests may include file names, paths, and other authorized context. The application lock does not encrypt local data.

See the [privacy notice](PRIVACY.md) for the full data scope, storage details, and deletion limitations.

## Platform and installation limits

**There are no official installation packages to download yet.** These are current implementation and build boundaries, not a claim of release readiness or cross-platform validation.

| Platform | Current status |
| --- | --- |
| macOS | Universal application targeting macOS 12 or later, for Apple silicon and Intel. Current packages use ad hoc signing and are not notarized by Apple. |
| Windows | Build paths exist for x64 and ARM64 installers, but sufficient testing on real Windows devices has not been completed. Playback, synchronization, notifications, and system authentication are not fully validated on real hardware. Some macOS integrations are unavailable on Windows. |
| Linux | Support and distribution packages are not currently promised. |

See the [installation guide](docs/INSTALL.md) for requirements and first-launch precautions. When a release is published, download packages only from this repository's official [Releases](https://github.com/tonyflux/pkp/releases) and follow the corresponding verification instructions.

## Plan to open the full source code

**Once PKP reaches its usage target, we will open-source the complete PKP-owned application source code—not just selected modules.**

The usage metric, target threshold, and measurement method have not yet been determined and will be announced in this repository. No specific source-release date is promised at this stage. This plan does not require users to enable usage statistics; disabling statistics does not affect normal use.

PKP remains closed-source for now, and the current [LICENSE](LICENSE) applies to existing versions. The source release will include an open-source license and build instructions. This plan does not grant advance access to the source code or open-source usage rights. Third-party components remain subject to their respective licenses.

## Guides and feedback

The detailed guides and reference documents below are currently in Simplified Chinese.

| Resource | What it covers |
| --- | --- |
| [Installation and first launch](docs/INSTALL.md) | Download status, platform requirements, first launch, and updates. |
| [User guide](docs/USAGE.md) | Accounts, transfers, playback, synchronization, and data-operation precautions. |
| [Privacy notice](PRIVACY.md) | Local data, external services, usage statistics, referrals, and deletion limitations. |
| [Changelog](CHANGELOG.md) · [Releases](https://github.com/tonyflux/pkp/releases) | Documentation and development status; published release attachments and notes will be authoritative once available. |
| [Third-party components](THIRD_PARTY.md) · [Software use terms](LICENSE) | Third-party distribution obligations, copyright, and software use terms. |

Report problems or suggest improvements through [Issues](https://github.com/tonyflux/pkp/issues). **Do not post passwords, tokens, API keys, share passcodes, complete account details, or unredacted logs and screenshots. Do not upload the entire configuration directory.**
