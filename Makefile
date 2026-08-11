.PHONY: build test verify cross-build notices install assets package clean \
	dashboard-install dashboard-typecheck dashboard-test dashboard-build dashboard-verify

VERSION ?= dev
PREFIX ?= $(HOME)/.local
# Build for the host by default. Both are overridable, so `make build
# GOOS=linux GOARCH=arm64` still cross-compiles, and the archive name below
# follows whatever was actually built.
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ASSET_NAME = cc-dialect_$(VERSION)_$(GOOS)_$(GOARCH)
# The platforms CI proves keep building. linux/arm64 is cross-compiled only —
# no runner executes it.
CROSS_TARGETS = darwin/arm64 darwin/amd64 linux/amd64 linux/arm64
DASHBOARD_DIR = internal/app/dashboard
DASHBOARD_DIST = $(DASHBOARD_DIR)/dist

ifeq ($(GOOS),darwin)
ASSET_ARCHIVE = $(ASSET_NAME).zip
else
ASSET_ARCHIVE = $(ASSET_NAME).tar.gz
endif

build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o dist/cc-dialect .

test:
	go test ./...

cross-build:
	for target in $(CROSS_TARGETS); do \
		echo "cross-check $$target"; \
		CGO_ENABLED=0 GOOS="$${target%/*}" GOARCH="$${target#*/}" go build ./... || exit 1; \
		CGO_ENABLED=0 GOOS="$${target%/*}" GOARCH="$${target#*/}" go vet ./... || exit 1; \
	done

dashboard-install:
	npm --prefix "$(DASHBOARD_DIR)" ci

dashboard-typecheck: dashboard-install
	npm --prefix "$(DASHBOARD_DIR)" run typecheck

dashboard-test: dashboard-install
	npm --prefix "$(DASHBOARD_DIR)" test

dashboard-build: dashboard-install
	npm --prefix "$(DASHBOARD_DIR)" run build

dashboard-verify: dashboard-typecheck dashboard-test dashboard-build
	git ls-files --error-unmatch -- "$(DASHBOARD_DIST)/index.html" >/dev/null
	test -z "$$(git ls-files --others --exclude-standard -- "$(DASHBOARD_DIST)")"
	git diff --exit-code -- "$(DASHBOARD_DIST)"

verify: dashboard-verify cross-build
	test -z "$$(gofmt -l .)"
	go mod verify
	go test ./...

notices: dashboard-install
	./scripts/generate-third-party-notices.sh

install: build
	mkdir -p "$(PREFIX)/bin"
	tmp="$(PREFIX)/bin/.cc-dialect.tmp.$$$$"; \
	cp dist/cc-dialect "$$tmp"; \
	chmod 755 "$$tmp"; \
	mv -f "$$tmp" "$(PREFIX)/bin/cc-dialect"; \
	rm -f "$(PREFIX)/bin/dialect"

assets: build notices
	rm -rf "artifacts/$(ASSET_NAME)"
	mkdir -p "artifacts/$(ASSET_NAME)"
	cp dist/cc-dialect LICENSE README.md THIRD_PARTY_NOTICES.md "artifacts/$(ASSET_NAME)/"
ifeq ($(GOOS),darwin)
	# The format follows the target, but the tool has to follow the host: ditto
	# is macOS-only, so keying it off GOOS alone would break cross-packaging a
	# darwin archive from Linux. Prefer ditto where it exists — on a macOS host
	# it is what keeps resource forks and extended attributes out of the archive,
	# which Info-ZIP would store as AppleDouble entries instead. A non-macOS host
	# has no such metadata to strip, so any zip produces an equivalent archive.
	cd artifacts && if command -v ditto >/dev/null 2>&1; then \
		COPYFILE_DISABLE=1 ditto -c -k --norsrc --noextattr --keepParent "$(ASSET_NAME)" "$(ASSET_ARCHIVE)"; \
	elif command -v zip >/dev/null 2>&1; then \
		zip -qrX "$(ASSET_ARCHIVE)" "$(ASSET_NAME)"; \
	else \
		echo "packaging a macOS archive needs ditto (macOS) or zip; install zip or build on macOS" >&2; \
		exit 1; \
	fi
else
	# tar, not zip: zip is absent from many minimal Linux images, and tar.gz is
	# the platform convention anyway. tar is present on macOS too, so packaging
	# a Linux archive from either host works.
	cd artifacts && tar -czf "$(ASSET_ARCHIVE)" "$(ASSET_NAME)"
endif
	cd artifacts && if command -v sha256sum >/dev/null 2>&1; then \
		sha256sum "$(ASSET_ARCHIVE)" > SHA256SUMS; \
	else \
		shasum -a 256 "$(ASSET_ARCHIVE)" > SHA256SUMS; \
	fi

package: assets

clean:
	rm -rf dist artifacts
