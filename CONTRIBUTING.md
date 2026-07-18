# Contributing

Claude Dialects accepts focused contributions that have been discussed with the
maintainer first.

## Before opening a pull request

1. Search the existing issues.
2. Open a bug report or feature request.
3. Wait until the maintainer adds the `status:accepted` label.
4. Implement only the accepted scope.

Pull requests without a linked accepted issue are closed automatically. Use a
closing keyword in the pull-request description, for example `Closes #123`.

## AI-assisted contributions

AI assistance is allowed only when the contributor understands, reviews, and
tests every submitted change. Disclose the tools used and what they generated
in the pull-request template.

Do not submit:

- bulk-generated changes;
- speculative refactors;
- generated tests that do not exercise meaningful behavior;
- changes you cannot explain or maintain;
- output copied from an agent without human verification.

Undisclosed or low-quality AI-generated submissions may be closed without
review.

## Development

Requirements:

- macOS on Apple Silicon;
- Go 1.26.5 or newer;
- Claude Code installed and available as `claude`.

Before submitting:

```sh
gofmt -w .
go mod verify
go test ./...
go vet ./...
govulncheck ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...
./scripts/generate-third-party-notices.sh
git diff --check
```

Do not commit generated binaries, credentials, OAuth files, instance state, or
personal Claude Code configuration.

By contributing, you agree that your contribution is licensed under the
repository's MIT License.
