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

- macOS;
- Go 1.26.5 or newer;
- Claude Code installed and available as `claude`;
- npm and Node.js 22.13.x or 24+ when changing or verifying the dashboard.

The dashboard source is under `internal/app/dashboard/`. Its compiled
`internal/app/dashboard/dist/` output is committed because Go embeds it into the
single executable. Rebuild and include that directory whenever frontend source
changes. Do not commit the root `dist/` directory or other generated binaries.
Node.js and npm are contributor dependencies for dashboard work; they are not
required to run the embedded dashboard or for a normal `make build` or
`make install` from a clean checkout.

Run the frontend checks in the same order as CI:

```sh
npm --prefix internal/app/dashboard ci
npm --prefix internal/app/dashboard run typecheck
npm --prefix internal/app/dashboard test
npm --prefix internal/app/dashboard run build
git diff --exit-code -- internal/app/dashboard/dist
```

`make dashboard-verify` runs that sequence. `make verify` adds the normal Go
format, module, test, vet, and build checks. Before submitting, run the complete
verification set:

```sh
make verify
govulncheck ./...
./scripts/generate-third-party-notices.sh
git diff --exit-code -- THIRD_PARTY_NOTICES.md
git diff --check
```

`THIRD_PARTY_NOTICES.md` is committed generated output. Regenerate it after Go
or frontend dependency changes; it includes Go modules compiled into the binary
and production npm dependencies bundled into the dashboard, not frontend-only
development dependencies.

Do not commit generated binaries, credentials, OAuth files, instance state, or
personal Claude Code configuration.

By contributing, you agree that your contribution is licensed under the
repository's MIT License.
