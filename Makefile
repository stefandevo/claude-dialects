.PHONY: build test verify notices install clean

VERSION ?= dev
PREFIX ?= $(HOME)/.local

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

clean:
	rm -rf dist
