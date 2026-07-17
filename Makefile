.PHONY: build test install clean

VERSION ?= dev
PREFIX ?= $(HOME)/.local

build:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o dist/dialect .

test:
	go test ./...

install: build
	mkdir -p "$(PREFIX)/bin"
	cp dist/dialect "$(PREFIX)/bin/dialect"

clean:
	rm -rf dist

