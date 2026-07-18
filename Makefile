.PHONY: build test verify notices install assets package clean

VERSION ?= dev
PREFIX ?= $(HOME)/.local
ASSET_NAME = dialect_$(VERSION)_darwin_arm64

build:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o dist/dialect .

test:
	go test ./...

verify:
	test -z "$$(gofmt -l .)"
	go mod verify
	go test ./...
	go vet ./...
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...

notices:
	./scripts/generate-third-party-notices.sh

install: build
	mkdir -p "$(PREFIX)/bin"
	tmp="$(PREFIX)/bin/.dialect.tmp.$$$$"; \
	cp dist/dialect "$$tmp"; \
	chmod 755 "$$tmp"; \
	mv -f "$$tmp" "$(PREFIX)/bin/dialect"

assets: build notices
	rm -rf "artifacts/$(ASSET_NAME)"
	mkdir -p "artifacts/$(ASSET_NAME)"
	cp dist/dialect LICENSE README.md THIRD_PARTY_NOTICES.md "artifacts/$(ASSET_NAME)/"
	cd artifacts && COPYFILE_DISABLE=1 ditto -c -k --norsrc --noextattr --keepParent "$(ASSET_NAME)" "$(ASSET_NAME).zip"
	cd artifacts && shasum -a 256 "$(ASSET_NAME).zip" > SHA256SUMS

package: assets

clean:
	rm -rf dist artifacts
