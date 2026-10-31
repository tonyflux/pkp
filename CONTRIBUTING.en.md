# Contributing

[简体中文](CONTRIBUTING.md) · English

Thank you for contributing to PKP. Search existing issues and pull requests first. Use the [feature request template](https://github.com/tonyflux/pkp/issues/new?template=feature_request.yml) for suggestions and the bug report template for defects. Never include credentials, private files, or account information in public content. Do not report vulnerabilities publicly; see the [security policy](SECURITY.en.md).

## Development and pull requests

1. Install the prerequisites in the [local build guide](docs/DEVELOPMENT.en.md), then run `cd frontend && npm ci && cd ..`, `make app-go`, and `make test` from the repository root. The guide also describes the macOS host and Windows cross-compilation scope.
2. Create a branch from the current code, keep changes focused, add tests where appropriate, and follow the guide's language conventions. Do not edit generated Wails bindings by hand.
3. Open a pull request describing the problem, solution, test commands and results; include redacted screenshots for UI changes when useful. Note any verification not performed and link related issues.

By submitting a contribution, you confirm that you have the right to provide it under this repository's [GPL-3.0-only](LICENSE) license. When adding or distributing third-party code, assets, or dependencies, check license compatibility and retain required copyright and license notices.
