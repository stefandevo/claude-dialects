# Agent and contributor guide

Instructions for AI assistants and human contributors working on Claude Dialects.

## Documentation sync (required)

**README.md and the `landing/` website must stay in sync.**

Every user-facing change to the CLI, presets, provider routes, configuration,
authentication flows, file layout, or behavior must be reflected in **both**:

1. **`README.md`** — canonical technical reference (install, provider guides,
   commands, security, file layout).
2. **`landing/`** — public marketing and docs site hosted at
   [claude-dialects.cc](https://claude-dialects.cc).

### When to update

Update README and website together whenever you change any of:

- CLI commands, flags, or subcommands (`cc-dialect …`)
- Preset names, default models, or provider mappings
- Authentication steps (OAuth providers, API keys, `auth` / `cursor install` /
  `copilot install`)
- Platform or dependency requirements (macOS, Linux, Go, Node.js, Claude Code)
- Per-dialect isolation, ports, proxy lifecycle, or state directories
- Disclaimers, third-party notices, or legal/platform warnings
- New or removed providers or bridge integrations

### Where to update on the website

| Change type | README section | Website page(s) |
| --- | --- | --- |
| Install, first dialect, multi-dialect | Install / Create your first dialect | `getting-started.html`, `index.html` (hero/configure tabs) |
| Provider setup | Provider guides table + per-provider blocks | `providers.html`, `index.html` (provider cards) |
| Model switching, custom dialects, proxy ops, security | Later README sections | `reference.html`, `legal.html` |
| Disclaimers, CLIProxyAPI credit, supported platforms | Important callout, Files and security | `legal.html`, footers on all pages |
| High-level value proposition | Introduction | `index.html` (hero, Why Claude Dialects) |

Keep wording consistent between README and the site. The README may be more
detailed; the website may summarize, but **facts must match** (command names,
preset IDs, env vars, ports, requirements).

### Pull request checklist

Before opening or merging a PR that touches user-facing behavior:

- [ ] README.md updated for the change
- [ ] Matching updates in the relevant `landing/*.html` page(s)
- [ ] Shared styles/scripts unchanged unless the site structure needs it
- [ ] Legal/disclaimer copy still accurate if providers or dependencies changed

## Project context

- **Product:** Multiple isolated Claude Code dialects, each with its own model,
  credentials, config, history, and embedded [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)
  instance — no manual proxy setup or global `~/.claude` changes.
- **Platform:** macOS and Linux, on amd64 and arm64. State lives in the
  platform's user configuration directory —
  `~/Library/Application Support/claude-dialects` on macOS,
  `${XDG_CONFIG_HOME:-~/.config}/claude-dialects` on Linux — or under
  `DIALECT_HOME`.
- **Build:** `make install` produces `~/.local/bin/cc-dialect`. No published
  binaries; see README “Build local assets”. The committed
  `internal/app/dashboard/dist/` frontend is embedded in that binary, so Node.js
  is needed only when contributors rebuild or verify dashboard source.
- **Contributing:** See [CONTRIBUTING.md](CONTRIBUTING.md) — accepted issue
  required before PRs.

## Website hosting

The static site in `landing/` is deployed to **claude-dialects.cc** via
**Cloudflare Pages**:

- No build step — output directory is `landing/`
- Connect the GitHub repository in Cloudflare Pages; deploy on push to `main`
- Point the `claude-dialects.cc` zone at Cloudflare and attach the custom
  domain to the Pages project

Local preview:

```sh
cd landing && python3 -m http.server 8765
```

## Code conventions

- Match existing Go style; run `gofmt`, `go test ./...`, `go vet ./...` before
  submitting. `make cross-build` proves every supported platform still compiles
  and vets clean — run it for any change to build tags or platform-specific
  code paths.
- Do not commit credentials, instance state, or generated binaries. The one
  generated exception is `internal/app/dashboard/dist/`: it is committed because
  Go embeds it, and dashboard source changes must include refreshed assets.
- Prefer minimal, focused diffs — no drive-by refactors.
- Third-party embedding: CLIProxyAPI (Go SDK), production npm dependencies in
  the embedded dashboard, and optional `@cursor/sdk` / `@github/copilot-sdk`
  runtimes — regenerate `THIRD_PARTY_NOTICES.md` when dependencies change.

## Key paths

| Path | Purpose |
| --- | --- |
| `internal/app/` | CLI, dialect lifecycle, proxy, dashboard, Cursor/Copilot bridges |
| `internal/app/dashboard/` | React dashboard source; committed `dist/` is embedded in the binary |
| `landing/` | Public website (HTML/CSS/JS) |
| `README.md` | Canonical user documentation |
| `THIRD_PARTY_NOTICES.md` | Embedded dependency licenses |
| `SECURITY.md` | Vulnerability reporting |
