.PHONY: all help clean frontend libmpv libmpv-windows libmpv-sources build-go updater-helper app-go dist dmg windows windows-installer release-check release-local release-verify release-draft release-publish wails-build wails-package wails-dmg dev test

# Default target
all: build-go

help:
	@echo "PKP build commands:"
	@echo ""
	@echo "Option A (native Go build, no Wails v3 CLI required):"
	@echo "  make build-go       Build the frontend and universal binary (bin/pkp, arm64 + x86_64)"
	@echo "  make app-go         Build and package the macOS app (bin/pkp.app, bundled libmpv, macOS 12+)"
	@echo "  make dist           Package the drag-to-install DMG (bin/PKP-<version>.dmg, ad-hoc signed, not notarized)"
	@echo "  make windows        Cross-compile Windows binaries (bin/pkp-amd64.exe and bin/pkp-arm64.exe, no cgo)"
	@echo "  make windows-installer  Package the Windows NSIS installer (bin/pkp-amd64_arm64-installer.exe, bundled libmpv, requires makensis)"
	@echo "  make release-local RELEASE_NOTES=<file>  Build and sign release artifacts in bin/releases/v<version> (no upload)"
	@echo "  make release-verify Verify the release manifest signature and every artifact hash"
	@echo "  make release-draft  Verify and upload a draft to the public repository (excluded from automatic updates)"
	@echo "  make release-publish  Review the draft and publish it as Latest (included in automatic updates)"
	@echo ""
	@echo "Option B (Wails v3 CLI):"
	@echo "  make wails-build    Build the app with wails3"
	@echo "  make wails-package  Package the .app with wails3"
	@echo "  make wails-dmg      Package the DMG with wails3"
	@echo "  make dev            Start development mode with hot reload"
	@echo ""
	@echo "Utility commands:"
	@echo "  make frontend       Build only the frontend assets (frontend/dist)"
	@echo "  make libmpv         Build the pinned bundled libmpv from source (LGPL; about 5 minutes initially, then skipped)"
	@echo "  make libmpv-windows Cross-compile the pinned Windows libmpv-2.dll from source (LGPL, x64 + ARM64; about 10 minutes initially, then skipped)"
	@echo "  make libmpv-sources Package the complete corresponding source for bundled libraries (bin/libmpv-sources.tar.gz; upload with every release)"
	@echo "  make test           Run Go unit tests and frontend Vitest"
	@echo "  make clean          Remove bin and dangling symlinks"

MACOS_MIN := 12.0
LIBMPV := build/darwin/libmpv/lib/libmpv.2.dylib
VERSION := $(shell /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' build/darwin/Info.plist)
DMG := bin/PKP-$(VERSION).dmg
APP_BUNDLE ?= bin/pkp.app
DMG_TOOLS := bin/dmg-tools/.ready
RELEASE_DIR := bin/releases/v$(VERSION)
RELEASE_NOTES ?=
RELEASE_KEY ?= $(HOME)/.ssh/pkp-release/update-ed25519.key
# Build once per architecture. The final -mmacosx-version-min in -extldflags
# overrides the Wails cgo directive that hard-codes 10.13. libmpv is resolved
# from pkp.app/Contents/Frameworks in packages, then a sibling Frameworks directory for bin/pkp.
GO_BUILD := CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=$(MACOS_MIN) CGO_CFLAGS="-O2 -g -mmacosx-version-min=$(MACOS_MIN)" \
	go build -tags production -trimpath -buildvcs=false \
	-ldflags="-w -s -X main.version=$(VERSION) '-extldflags=-mmacosx-version-min=$(MACOS_MIN) -Wl,-rpath,@executable_path/../Frameworks -Wl,-rpath,@executable_path/Frameworks'"

# Windows has no cgo code: pkp.exe loads the adjacent libmpv-2.dll at runtime.
# -H windowsgui prevents a console window when launched from Explorer.
WIN_ARCHS := amd64 arm64
WIN_GO_BUILD := GOOS=windows CGO_ENABLED=0 go build -tags production -trimpath -buildvcs=false -ldflags="-w -s -X main.version=$(VERSION) -H windowsgui"
WEBVIEW2_BOOTSTRAPPER := build/windows/nsis/MicrosoftEdgeWebview2Setup.exe

# Windows libmpv: build/windows/libmpv.sh uses llvm-mingw to cross-compile the
# pinned LGPL build for x64 and ARM64. Dependencies are linked statically and
# only system DLLs are loaded. Generated libraries and licenses stay untracked.
LIBMPV_WIN_DIR := build/windows/libmpv
LIBMPV_WIN_KEY := $(LIBMPV_WIN_DIR)/.key

# Remove build output, including dangling symlinks and regular directories.
clean:
	rm -rf bin

# Build frontend assets embedded by the Go binary.
frontend:
	wails3 generate bindings -ts -i
	cd frontend && npm run build

# Build bundled libmpv only when its inputs changed.
libmpv: $(LIBMPV)

$(LIBMPV): build/darwin/libmpv.sh build/libmpv-common.sh
	build/darwin/libmpv.sh

libmpv-windows: $(LIBMPV_WIN_KEY)

$(LIBMPV_WIN_KEY): build/windows/libmpv.sh build/libmpv-common.sh
	build/windows/libmpv.sh

# LGPL requires complete corresponding source for bundled libraries in every release.
libmpv-sources:
	mkdir -p bin
	build/libmpv-sources.sh bin/libmpv-sources.tar.gz

# Option A: build the native universal Go binary.
build-go: frontend $(LIBMPV)
	mkdir -p bin
	GOARCH=arm64 $(GO_BUILD) -o bin/pkp-arm64 .
	GOARCH=amd64 $(GO_BUILD) -o bin/pkp-amd64 .
	lipo -create -output bin/pkp bin/pkp-arm64 bin/pkp-amd64
	rm bin/pkp-arm64 bin/pkp-amd64
# Copy only versioned libraries; libmpv.dylib is the linker's symlink.
	rm -rf bin/Frameworks
	mkdir -p bin/Frameworks
	cp build/darwin/libmpv/lib/*.*.dylib bin/Frameworks/


# The updater must keep running after replacing the main app, without libmpv.
updater-helper: bin/pkp-updater

bin/pkp-updater: build/darwin/updater.swift
	mkdir -p bin
	xcrun swiftc -O -target arm64-apple-macos$(MACOS_MIN) -o bin/pkp-updater-arm64 $<
	xcrun swiftc -O -target x86_64-apple-macos$(MACOS_MIN) -o bin/pkp-updater-amd64 $<
	lipo -create -output $@ bin/pkp-updater-arm64 bin/pkp-updater-amd64
	rm bin/pkp-updater-arm64 bin/pkp-updater-amd64
	codesign --force --sign - $@
# Option A: build and assemble the macOS app bundle.
app-go: build-go updater-helper
	rm -rf bin/pkp.app
	mkdir -p bin/pkp.app/Contents/MacOS bin/pkp.app/Contents/Resources bin/pkp.app/Contents/Helpers
	cp bin/pkp bin/pkp.app/Contents/MacOS/
	cp bin/pkp-updater bin/pkp.app/Contents/Helpers/
	cp -R bin/Frameworks bin/pkp.app/Contents/
	cp build/darwin/Info.plist bin/pkp.app/Contents/
	cp build/darwin/icons.icns bin/pkp.app/Contents/Resources/
	cp -R build/darwin/libmpv/licenses bin/pkp.app/Contents/Resources/
	python3 build/go-licenses.py darwin bin/pkp.app/Contents/Resources/licenses/GO_LICENSES.txt
	cp frontend/dist/THIRD_PARTY_LICENSES.txt bin/pkp.app/Contents/Resources/licenses/THIRD_PARTY_LICENSES.txt
	cp LICENSE bin/pkp.app/Contents/Resources/LICENSE.txt
	cp build/darwin/dmg-readme.txt "bin/pkp.app/Contents/Resources/README-FIRST.txt"
	@if [ -f build/darwin/Assets.car ]; then cp build/darwin/Assets.car bin/pkp.app/Contents/Resources/; fi
	xattr -cr bin/pkp.app
	codesign --force --deep --sign - bin/pkp.app
	@echo "macOS app packaged: bin/pkp.app"

# dmgbuild writes the Finder layout without opening Finder.
$(DMG_TOOLS): build/darwin/dmg-requirements.txt
	python3 -m venv bin/dmg-tools
	bin/dmg-tools/bin/python -m pip install -r $<
	touch $@

dist: app-go
	$(MAKE) dmg

# The Wails packaging path reuses this layout; the first-launch guide and license ship with the app.
dmg: $(DMG_TOOLS)
	codesign --verify --deep --strict "$(APP_BUNDLE)"
	swift build/darwin/dmg-background.swift bin/dmg-background.tiff
	bin/dmg-tools/bin/dmgbuild -s build/darwin/dmg-settings.py -D app="$(APP_BUNDLE)" PKP "$(DMG)"
# The DMG file icon uses a resource fork. Local copies and AirDrop retain it; web downloads may not.
	cp build/darwin/icons.icns bin/dmg-icon.icns
	sips -i bin/dmg-icon.icns >/dev/null
	DeRez -only icns bin/dmg-icon.icns > bin/dmg-icon.rsrc
	Rez -append bin/dmg-icon.rsrc -o $(DMG)
	SetFile -a C $(DMG)
	rm -f bin/dmg-icon.*
	@echo "Distribution image: $(DMG)"

# Build one .syso per Windows architecture (icon, version and DPI manifest),
# use it only for the matching GOOS/GOARCH build, then remove it even on failure.
windows: frontend
	mkdir -p bin
	@for arch in $(WIN_ARCHS); do \
		echo "==> windows/$$arch"; \
		(cd build && wails3 generate syso -arch $$arch -icon windows/icon.ico -manifest windows/wails.exe.manifest \
			-info windows/info.json -out ../wails_windows_$$arch.syso) && \
		GOARCH=$$arch $(WIN_GO_BUILD) -o bin/pkp-$$arch.exe .; \
		status=$$?; rm -f wails_windows_$$arch.syso; [ $$status -eq 0 ] || exit $$status; \
	done

# Download the WebView2 bootstrapper used only when the runtime is absent (untracked).
$(WEBVIEW2_BOOTSTRAPPER):
	wails3 generate webview2bootstrapper -dir build/windows/nsis

# One Windows installer carries amd64 and arm64 binaries and installs only the
# current architecture. Bundled-library licenses go in the installer's licenses directory.
windows-installer: windows $(WEBVIEW2_BOOTSTRAPPER) $(LIBMPV_WIN_KEY)
	python3 build/go-licenses.py windows bin/GO_LICENSES-windows.txt
	cd build/windows/nsis && makensis -V2 \
		-DARG_WAILS_AMD64_BINARY="$(CURDIR)/bin/pkp-amd64.exe" \
		-DARG_WAILS_ARM64_BINARY="$(CURDIR)/bin/pkp-arm64.exe" \
		-DARG_LIBMPV_AMD64="$(CURDIR)/$(LIBMPV_WIN_DIR)/amd64/libmpv-2.dll" \
		-DARG_LIBMPV_ARM64="$(CURDIR)/$(LIBMPV_WIN_DIR)/arm64/libmpv-2.dll" \
		-DARG_LIBMPV_LICENSES="$(CURDIR)/$(LIBMPV_WIN_DIR)/licenses" \
		-DARG_GO_LICENSES="$(CURDIR)/bin/GO_LICENSES-windows.txt" \
		-DARG_FRONTEND_LICENSES="$(CURDIR)/frontend/dist/THIRD_PARTY_LICENSES.txt" \
		project.nsi
	@echo "Windows installer packaged: bin/pkp-amd64_arm64-installer.exe"

# Assemble only allowlisted artifacts from a clean committed source snapshot.
release-check:
	@test -n "$(RELEASE_NOTES)" || { echo "RELEASE_NOTES=<user-facing release notes file> is required"; exit 1; }
	@test ! -e "$(RELEASE_DIR)" || { echo "Refusing to overwrite existing release directory: $(RELEASE_DIR)"; exit 1; }
	go run ./cmd/pkp-release check -version "$(VERSION)" -key "$(RELEASE_KEY)" -notes "$(RELEASE_NOTES)"

release-local: release-check
	@set -eu; source_commit=$$(go run ./cmd/pkp-release snapshot); \
		$(MAKE) dist windows-installer libmpv-sources; \
		mkdir -p bin/releases; \
		mkdir "$(RELEASE_DIR)"; \
		go run ./cmd/pkp-release source -version "$(VERSION)" -dir "$(RELEASE_DIR)" -source-commit "$$source_commit"
	cp "$(DMG)" "$(RELEASE_DIR)/PKP-$(VERSION)-macos-universal.dmg"
	cp bin/pkp-amd64_arm64-installer.exe "$(RELEASE_DIR)/PKP-$(VERSION)-windows-x64-arm64-setup.exe"
	cp bin/libmpv-sources.tar.gz LICENSE "$(RELEASE_DIR)/"
	go run ./cmd/pkp-release prepare -version "$(VERSION)" -dir "$(RELEASE_DIR)" -key "$(RELEASE_KEY)" -notes "$(RELEASE_NOTES)"
	cd "$(RELEASE_DIR)" && shasum -a 256 \
		"PKP-$(VERSION)-macos-universal.dmg" \
		"PKP-$(VERSION)-windows-x64-arm64-setup.exe" \
		update.json update.json.sig libmpv-sources.tar.gz LICENSE "PKP-$(VERSION)-source.tar.gz" > SHA256SUMS
	@echo "Local release artifacts: $(RELEASE_DIR) (not uploaded; see RELEASE.md for release checks)"

release-verify:
	go run ./cmd/pkp-release verify -dir "$(RELEASE_DIR)"

release-draft:
	go run ./cmd/pkp-release publish -dir "$(RELEASE_DIR)"

release-publish:
	go run ./cmd/pkp-release publish -dir "$(RELEASE_DIR)" -publish

# Option B: build with the Wails CLI.
wails-build:
	wails3 build

# Option B: package the .app with the Wails CLI.
wails-package:
	wails3 task darwin:package

# Option B: package the DMG with the Wails CLI.
wails-dmg:
	wails3 task darwin:package:dmg

# Start local development with hot reload.
dev:
	wails3 dev

# Unit tests: Go and frontend pure functions.
test: $(LIBMPV)
	go test ./internal/...
	cd frontend && npm test
