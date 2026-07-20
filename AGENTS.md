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
- Platform or dependency requirements (macOS, Go, Node.js, Claude Code)
- Per-dialect isolation, ports, proxy lifecycle, or state directories
- Disclaimers, third-party notices, or legal/platform warnings
- New or removed providers or bridge integrations

### Where to update on the website

| Change type | README section | Website page(s) |
| --- | --- | --- |
| Install, first dialect, multi-dialect | Install / Create your first dialect | `getting-started.html`, `index.html` (hero/configure tabs) |
| Provider setup | Provider guides table + per-provider blocks | `providers.html`, `index.html` (provider cards) |
| Model switching, custom dialects, proxy ops, security | Later README sections | `reference.html`, `legal.html` |
| Disclaimers, CLIProxyAPI credit, macOS-only | Important callout, Files and security | `legal.html`, footers on all pages |
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
- **Platform:** macOS only (state under
  `~/Library/Application Support/claude-dialects`).
- **Build:** `make install` produces `~/.local/bin/cc-dialect`. No published
  binaries; see README “Build local assets”.
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
  submitting.
- Do not commit credentials, instance state, or generated binaries.
- Prefer minimal, focused diffs — no drive-by refactors.
- Third-party embedding: CLIProxyAPI (Go SDK), optional `@cursor/sdk` and
  `@github/copilot-sdk` — regenerate `THIRD_PARTY_NOTICES.md` when dependencies
  change.

## Key paths

| Path | Purpose |
| --- | --- |
| `internal/app/` | CLI, dialect lifecycle, proxy, Cursor/Copilot bridges |
| `landing/` | Public website (HTML/CSS/JS) |
| `README.md` | Canonical user documentation |
| `THIRD_PARTY_NOTICES.md` | Embedded dependency licenses |
| `SECURITY.md` | Vulnerability reporting |
