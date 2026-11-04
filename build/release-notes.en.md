# PKP 0.3.0

[简体中文 / Bilingual release notes](release-notes.md)

## Highlights

- PikPak App QR sign-in for initial sign-in, adding accounts and reauthentication; password sign-in remains available. Unknown authentication failures no longer incorrectly claim that the password is wrong. Addresses [#3](https://github.com/tonyflux/pkp/issues/3).
- More reliable QR persistence, correct issuing-client identity across refresh/restart, successful in-flight results retained at expiry, and a single password-reveal button.
- Google Gemini API-key preset for the assistant and AI file organization: select Google Gemini in Settings → AI, enter a Google AI Studio API key, then select a returned model or enter its ID manually. Uses Google's official OpenAI-compatible endpoint directly, without a third-party proxy. Model listing does not guarantee account access/quota. API billing is separate from Gemini subscriptions; Google-account/subscription OAuth sign-in is not included. Addresses [#2](https://github.com/tonyflux/pkp/issues/2).
- Community & Feedback links to Telegram and GitHub before and after sign-in.
- Assistant reminders only for actionable organization suggestions.
- macOS in-app updates download only on request, replace the app after quit confirmation and reopen the same profile with accounts/settings preserved.

## Important privacy change

**Daily active installation statistics default to ON in v0.3.0; v0.2.0 defaulted to OFF.** Identifiable explicit opt-outs are preserved. Existing settings with no explicit choice follow the new on default and receive a one-time notice. An old save with no refusal marker cannot be distinguished from no choice.

Reports contain a random installation ID, version, OS/architecture, language and active local dates—not feature actions, frequencies, logs, accounts, filenames, paths, searches or file contents. Turn them off in Settings → Privacy & Security, also accessible before sign-in. Got it and dismissal only acknowledge the notice; they are not consent and leave statistics enabled. Recording starts at startup and sending can begin about 30 seconds later, before acknowledgment. Opt-out deletes the local ID and pending reports, not server history. Counts are observed active installations, not verified people or exact installs. Publishing this app does not deploy companion telemetry services.

## Packages and limitations

macOS Universal (macOS 12+, Apple silicon/Intel) and a combined Windows x64/ARM64 installer. Eight assets include both installers, the signed update manifest and its signature, `PKP-0.3.0-source.tar.gz`, third-party corresponding source, LICENSE and SHA256SUMS. PKP-owned source is GPL-3.0-only.

macOS remains ad hoc signed and not notarized; Windows has no Authenticode signature and remains preview support. Native installation/upgrade and a complete device matrix have not been fully validated. No Linux packages. Never post passwords, tokens, API keys or the full configuration directory in feedback.

[Installation](https://github.com/tonyflux/pkp/blob/v0.3.0/docs/INSTALL.en.md) · [Privacy](https://github.com/tonyflux/pkp/blob/v0.3.0/PRIVACY.en.md) · [Full changelog](https://github.com/tonyflux/pkp/compare/v0.2.0...v0.3.0)
