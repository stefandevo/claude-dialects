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

- macOS or Linux, on amd64 or arm64;
- Go 1.26.6 or newer;
- Claude Code installed and available as `claude`;
- npm and Node.js 22.22.2+ or 24.15.0+ when changing or verifying the dashboard.

The dashboard source is under `internal/app/dashboard/`. Its compiled
`internal/app/dashboard/dist/` output is committed because Go embeds it into the
single executable. Rebuild and include that directory whenever frontend source
changes. Do not commit the root `dist/` directory or other generated binaries.
Node.js and npm are contributor dependencies for dashboard work; they are not
required to run the embedded dashboard or for a normal `make build` or
`make install` from a clean checkout.

Dashboard dependency bumps go through `./scripts/npm-bump.sh`, which holds new
npm versions to a 5-day minimum release age:

```sh
./scripts/npm-bump.sh jsdom@30.0.0
```

The script resolves against the registry as it stood 5 days ago, so a version
published inside that window is refused. Versions already in
`package-lock.json` are untouched. Set `MIN_RELEASE_AGE_DAYS=0` to bypass the
cooldown for an urgent security fix.

The same 5 days is enforced on the other two paths that can reach the
lockfile: `cooldown.default-days` in `.github/dependabot.yml` for Dependabot's
weekly bumps, and `minimumReleaseAge` in `internal/app/dashboard/bunfig.toml`
for the case where bun runs in that directory instead. Dependabot exempts
security updates from cooldown, so those are never held back. CI installs with
`npm ci` from the committed lockfile, so every gate applies at bump time only
and none can turn CI red.

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
