# Claude Dialects

[![CI](https://github.com/stefandevo/claude-dialects/actions/workflows/ci.yml/badge.svg)](https://github.com/stefandevo/claude-dialects/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Create multiple native-feeling Claude Code commands powered by different models.
Each generated dialect runs the real Claude Code interface with its own model,
environment, Claude Code configuration and history, credentials, API key, and embedded
[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) instance.

The proxy is linked into the `cc-dialect` executable through CLIProxyAPI's Go SDK.
There is no separate proxy download, installation, container, or global
`~/.claude/settings.json` modification. Changes made with `/model`, `/effort`,
or other user-level Claude Code settings stay inside the active dialect.

> Current target: macOS on Apple Silicon only.

> [!IMPORTANT]
> This is an independent, unofficial project. It is not affiliated with or
> endorsed by Anthropic, OpenAI, Google, Moonshot AI, Z.ai, xAI, Cursor,
> MiniMax, DeepSeek, or the CLIProxyAPI maintainers. Product and company names
> are trademarks of their respective owners. You are responsible for complying
> with each provider's terms, subscription rules, and usage policies.

## Get the code, build, and install

Requirements:

- macOS on Apple Silicon;
- Go 1.26.5 or newer
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) available as
  `claude`
- optionally, Node.js 22.13 or newer and npm for Cursor SDK dialects

```sh
git clone https://github.com/stefandevo/claude-dialects.git
cd claude-dialects
make install
export PATH="$HOME/.local/bin:$PATH"
```

This produces one static executable at `~/.local/bin/cc-dialect`.
To make that PATH change persist across terminal restarts:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

When upgrading from the former `dialect` executable, `make install` removes that
old command. Existing configuration, OAuth credentials, and conversations are
preserved. Regenerate any older shims so they point directly to `cc-dialect`:

```sh
cc-dialect list
# Replace legacy-name with an existing dialect shown above.
cc-dialect shim install legacy-name --name cc-codex
```

This project does not publish prebuilt binaries or GitHub releases. Everyone
builds the executable from the checked-out source. To create a shareable local
Apple Silicon archive and checksum instead of installing it:

```sh
make assets VERSION=dev
ls artifacts/
(cd artifacts && shasum -a 256 -c SHA256SUMS)
```

The generated files are:

- `artifacts/cc-dialect_dev_darwin_arm64.zip`
- `artifacts/SHA256SUMS`

Set `VERSION` to any identifier you want in the filename and embedded
`cc-dialect --version` output. `make package` is an alias for `make assets`.
These locally produced assets are not signed or notarized by this project.

## Create several dialects

```sh
# GPT with Sol as both the main and subagent model
cc-dialect create cc-codex --preset codex-sol
cc-dialect auth cc-codex codex
cc-dialect shim install cc-codex

# Kimi, fully isolated from cc-codex and prefixed to avoid Kimi CLI
cc-dialect create cc-kimi --preset kimi
cc-dialect auth cc-kimi kimi
cc-dialect shim install cc-kimi

# A second Codex setup gets another port and credential store
cc-dialect create cc-codex-work --preset codex
cc-dialect auth cc-codex-work codex
cc-dialect shim install cc-codex-work
```

`create` prints these required steps in order. OAuth presets will not launch or
list models until their instance has been authenticated, and the error includes
the exact `cc-dialect auth` command to run.

You can now run all three simultaneously:

```sh
cc-codex
cc-kimi
cc-codex-work
```

## Native Claude shortcuts

Claude Dialects can also install a lightweight shortcut for the normal Claude
Code application without starting the proxy or changing its configuration. For
example, replace a separate `cld` launcher with:

```sh
cc-dialect native install cld --dangerous
cld
```

This launches the installed Claude Code executable with
`--dangerously-skip-permissions` and passes through any additional arguments.
It deliberately uses the regular `~/.claude` settings, authentication, and
conversation history, so it is a shortcut for your existing Claude Max setup,
not an isolated model dialect. Use dangerous mode only in directories you
trust.

Provider setup:

| Dialect | Connection | Setup |
| --- | --- | --- |
| OpenAI GPT / Codex | ChatGPT OAuth through CLIProxyAPI | `cc-dialect auth cc-codex codex` |
| Kimi | Kimi OAuth through CLIProxyAPI | `cc-dialect auth cc-kimi kimi` |
| Google Gemini | Antigravity Google OAuth through CLIProxyAPI | `cc-dialect auth cc-gemini antigravity` |
| Claude | Anthropic OAuth through CLIProxyAPI | `cc-dialect auth cc-claude claude` |
| xAI Grok / Grok Build / Composer | xAI OAuth through CLIProxyAPI | `cc-dialect auth cc-grok xai` |
| Cursor Composer / Grok / Auto | Official Cursor SDK bridge | Install the bridge and set `CURSOR_API_KEY` |
| GLM | Z.ai Anthropic-compatible API through CLIProxyAPI | Set `ZAI_API_KEY` |
| MiniMax | MiniMax Anthropic-compatible API through CLIProxyAPI | Set `MINIMAX_API_KEY` |
| DeepSeek | DeepSeek Anthropic-compatible API through CLIProxyAPI | Set `DEEPSEEK_API_KEY` |

Create a GLM runner using Z.ai's current GLM-5.2 flagship:

```sh
export ZAI_API_KEY="your_zai_api_key"
cc-dialect create cc-glm --preset glm
cc-dialect shim install cc-glm
cc-glm
```

The GLM preset maps `opus` to `glm-5.2`, `sonnet` to `glm-5-turbo`, and
`haiku` to `glm-4.5-air`. Its `auto` effort setting leaves GLM-5.2 at the
provider default (`max`). Inside Claude Code, GLM-5.2 accepts `high` or `max`;
the provider maps `xhigh` to `max` and maps `low` or `medium` to `high`.

Create xAI runners with OAuth:

```sh
cc-dialect create cc-grok --preset grok
cc-dialect auth cc-grok xai
cc-dialect shim install cc-grok

cc-dialect create cc-composer --preset composer
cc-dialect auth cc-composer xai
cc-dialect shim install cc-composer
```

The `grok`, `grok-build`, and `composer` presets remain separate model
families. `composer` uses Cursor Composer 2.5 Fast as exposed by xAI Grok
Build; it is not a Grok foundation model. Availability depends on the models
enabled for the authenticated xAI account.

### Cursor Composer through the official Cursor SDK

Cursor dialects use a small local OpenAI-compatible bridge backed by the
official `@cursor/sdk`. The SDK is installed on demand rather than bundled in
this repository:

```sh
cc-dialect cursor install
export CURSOR_API_KEY="your_cursor_api_key"

cc-dialect create cc-cursor --preset cursor-composer
cc-dialect shim install cc-cursor
cc-cursor
```

Create the key in the Cursor dashboard. This uses Cursor's API/SDK billing and
permissions; it does not extract or reuse credentials from the Cursor desktop
app or Agent CLI. Check the models currently enabled for the key with:

```sh
cc-dialect cursor models
cc-dialect models cc-cursor
cc-dialect cursor status
```

Available Cursor presets are:

- `cursor-composer` — Composer 2.5 with Fast and Standard menu mappings
- `cursor-composer-fast` — explicitly forces Composer 2.5 Fast
- `cursor-grok` — Cursor Grok 4.5
- `cursor-auto` — Cursor's `auto` selection

The bridge discovers the live model catalog from Cursor, supplies the catalog's
default parameter variant, and maps Claude Code effort requests onto a matching
Cursor model variant when the catalog advertises one. Every Cursor dialect has
two independently reserved localhost ports: one for the embedded CLIProxyAPI
instance and one for its private SDK bridge. Both are started and stopped by
the normal `cc-dialect proxy` lifecycle.

Cursor exposes Fast as a parameter of `composer-2.5`, not as a separate SDK
model ID. Fast is Cursor's default and is billed at its higher Fast rate. The
bridge adds `composer-2.5-fast` and `composer-2.5-standard` as local aliases and
translates them to the official SDK's `fast=true` and `fast=false` parameters.
With the `cursor-composer` preset, `/model opus` selects Fast while `/model
sonnet` or `/model haiku` selects Standard.

`@cursor/sdk` is pinned by `cc-dialect` for reproducible installs but remains a
separate Cursor-licensed dependency under Cursor's terms. Re-run
`cc-dialect cursor install` after updating `cc-dialect` when its pinned SDK
version changes.

Cursor Grok 4.5 is a different route from the direct xAI preset:

```sh
cc-dialect create cc-cursor-grok --preset cursor-grok
cc-dialect shim install cc-cursor-grok
cc-cursor-grok
```

`cursor-grok` uses the `grok-4.5` model in Cursor's first-party model pool
through the Cursor SDK and `CURSOR_API_KEY`. The plain `grok` preset instead
uses CLIProxyAPI's direct xAI OAuth provider. Cursor exposes Grok 4.5 effort
settings through its live SDK catalog when supported, so the bridge maps
Claude Code's `/effort` choice onto the advertised variant.

Create MiniMax and DeepSeek runners with provider API keys:

```sh
export MINIMAX_API_KEY="your_minimax_api_key"
cc-dialect create cc-minimax --preset minimax
cc-dialect shim install cc-minimax

export DEEPSEEK_API_KEY="your_deepseek_api_key"
cc-dialect create cc-deepseek --preset deepseek
cc-dialect shim install cc-deepseek
```

MiniMax uses `MiniMax-M2.7` for every Claude Code model alias. DeepSeek maps
the main, subagent, and `opus` selections to `deepseek-v4-pro`, while `sonnet`
and `haiku` use `deepseek-v4-flash`.

Create the Google runner with:

```sh
cc-dialect create cc-gemini --preset gemini
cc-dialect auth cc-gemini antigravity
cc-dialect shim install cc-gemini
```

Ports are actively checked and allocated per dialect starting at the high range
`43170`. A port already bound by any process is skipped during creation and
rejected again at startup:

```text
cc-codex       codex-sol    gpt-5.6-sol       embedded proxy :43170
cc-kimi        kimi         kimi-k3           embedded proxy :43171
cc-codex-work  codex        gpt-5.6           embedded proxy :43172
```

Pass Claude Code arguments normally:

```sh
cc-codex --permission-mode plan
cc-kimi --allowedTools "Bash,Read"
```

## Presets and custom dialects

Included presets:

```sh
cc-dialect presets
```

- `codex-sol`
- `codex`
- `kimi`
- `gemini`
- `claude`
- `glm`
- `grok`
- `grok-build`
- `composer`
- `minimax`
- `deepseek`
- `cursor-composer`
- `cursor-composer-fast`
- `cursor-grok`
- `cursor-auto`

Preset names describe providers; dialect names become shell commands. Prefer
the `cc-<provider>` convention so generated commands are clearly Claude Code
dialects and do not replace providers' existing CLIs. Arbitrary names and
existing legacy names remain supported:

| Preset | Recommended dialect command |
| --- | --- |
| `codex-sol` / `codex` | `cc-codex` |
| `kimi` | `cc-kimi` |
| `gemini` | `cc-gemini` |
| `claude` | `cc-claude` |
| `glm` | `cc-glm` |
| `grok` | `cc-grok` |
| `grok-build` | `cc-grok-build` |
| `composer` | `cc-composer` |
| `minimax` | `cc-minimax` |
| `deepseek` | `cc-deepseek` |
| `cursor-composer` | `cc-cursor` |
| `cursor-composer-fast` | `cc-cursor-fast` |
| `cursor-grok` | `cc-cursor-grok` |
| `cursor-auto` | `cc-cursor-auto` |

Provider-named presets such as `kimi` are rolling defaults for newly created
dialects. Updating and reinstalling the `cc-dialect` executable does not silently
change an existing dialect. Apply the latest preset explicitly:

```sh
make install
cc-dialect create cc-kimi --preset kimi
```

This updates the models and behavior flags while preserving the dialect's port,
local API key, OAuth credentials, isolated Claude Code configuration and
history, and installed shim. A running proxy for that dialect is stopped so its
next launch uses the newly installed embedded proxy. Start a new conversation
after changing the underlying model. In particular, Moonshot warns that
switching an existing conversation from another model to Kimi K3 can produce
unstable output because K3 requires its thinking history to be preserved.

To stay on a specific model instead, use a custom model ID. It will remain
unchanged until you run another `cc-dialect create` command for that name:

```sh
cc-dialect create cc-kimi-code --model kimi-k2.7-code
cc-dialect auth cc-kimi-code kimi
cc-dialect shim install cc-kimi-code
```

Override the important parameters while creating or updating a dialect:

```sh
cc-dialect create cc-my-codex \
  --preset codex \
  --model gpt-5.6 \
  --subagent-model gpt-5.6-sol \
  --opus-model gpt-5.6-sol \
  --sonnet-model gpt-5.6-terra \
  --haiku-model gpt-5.6-luna \
  --effort-level auto \
  --concurrency 3 \
  --effort=true \
  --tool-search=false \
  --port 53170
```

For an Anthropic-compatible service such as Z.ai, route the upstream through
the dialect's isolated embedded proxy:

```sh
export MY_PROVIDER_TOKEN="..."
cc-dialect create cc-my-model \
  --model my-model-id \
  --base-url https://provider.example.com/api/anthropic \
  --token-env MY_PROVIDER_TOKEN
cc-dialect shim install cc-my-model
```

The upstream token is read only when that isolated proxy starts and is written
to its owner-only proxy configuration. The `glm` preset uses this mode with
`ZAI_API_KEY` and Z.ai's current `https://api.z.ai/api/anthropic` endpoint,
matching the behavior
of [xqsit94/glm](https://github.com/xqsit94/glm).

## Switch model and effort inside a conversation

Claude Code 2.x supports live switching without losing the conversation:

```text
/model opus
/model sonnet
/model haiku
/model <any model ID shown by `cc-dialect models cc-codex`>
/effort low
/effort high
/effort xhigh
/effort max
```

Each dialect maps the standard `opus`, `sonnet`, and `haiku` choices to its own
three configured model IDs. The Codex preset maps them to Sol, Terra, and Luna.
Use `--opus-model`, `--sonnet-model`, and `--haiku-model` to change that mapping.
`/model` also lets you adjust effort with the arrow keys, and `/effort` changes
it immediately. We deliberately do not set `CLAUDE_CODE_EFFORT_LEVEL`, because
that environment variable would take precedence over live `/effort` changes.
Claude Code stores these interactive choices in the dialect's own configuration
directory, so changing `cc-codex` does not change regular `claude`, `cc-kimi`, or
another dialect.
CLIProxyAPI translates
Claude's adaptive reasoning request into the upstream provider's reasoning
format when that provider supports it. Cursor SDK dialects similarly select a
matching catalog variant when Cursor exposes an effort/thinking parameter; if
the selected model has no such variant, its catalog default is used.

Kimi K3 currently uses `max` effort by default; Moonshot says lower effort
levels will arrive in later updates. Keep its preset at `auto` so the provider
selects the supported default.

List the models actually exposed by an authenticated instance:

```sh
cc-dialect models cc-codex
```

## Detect configured and running dialects

Every newly created dialect records the preset it came from. Older
configurations are recognized from their saved provider and model settings, so
they do not need to be recreated.

Human-readable detection:

```sh
cc-dialect detect
cc-dialect detect codex
cc-dialect detect glm --running
```

`codex` matches both the `codex` and `codex-sol` presets. An exact query such as
`codex-sol` matches only that preset. `cc-dialect doctor` also displays the
detected preset beside every instance.

For another tool, use JSON when it needs instance details:

```sh
cc-dialect detect --running --json
cc-dialect detect kimi --running --json
```

Or use a silent exit-status check:

```sh
if cc-dialect detect glm --running --quiet; then
  echo "A GLM Claude Code dialect is running"
fi
```

For a preset or provider query, exit status `0` means at least one matching
dialect was found and exit status `1` means none matched. JSON records contain
the command name, exact preset, provider family, model, port, and running state.

## Proxy and authentication commands

Every proxied dialect has an independent lifecycle:

```sh
cc-dialect proxy cc-codex start
cc-dialect proxy cc-codex status
cc-dialect proxy cc-codex logs
cc-dialect proxy cc-codex stop
```

The proxy starts automatically when its generated command runs and remains
available for later sessions. OAuth credentials are scoped to that dialect:

```sh
cc-dialect auth cc-codex codex
cc-dialect auth cc-kimi kimi
cc-dialect auth cc-claude claude
```

Supported embedded OAuth providers are `codex`, `claude`, `kimi`,
`antigravity`, and `xai`.

## Files and security

State lives under `~/Library/Application Support/claude-dialects` on macOS (or
`DIALECT_HOME` when set):

```text
config.json
instances/
  cc-codex/
    auth/
    claude/
    proxy.yaml
    proxy.pid
    proxy.log
  cc-kimi/
    auth/
    claude/
    proxy.yaml
    proxy.pid
    proxy.log
  cc-cursor/
    claude/
    cursor-workspace/
    cursor-bridge.pid
    cursor-bridge.log
    proxy.yaml
    proxy.pid
    proxy.log
cursor-runtime/
  cursor_bridge.mjs
  node_modules/@cursor/sdk/
```

Proxy servers bind only to `127.0.0.1`. Configuration, local API keys, and OAuth
credentials use owner-only permissions. The CLI changes environment variables
only for the launched Claude Code process.

The optional Cursor bridge also binds only to `127.0.0.1`, authenticates every
request with the dialect's private local key, and keeps its SDK workspace and
metadata separate per dialect. `CURSOR_API_KEY` is read from the environment at
startup and is not written into `config.json`.

The `claude/` directory is supplied to Claude Code through
`CLAUDE_CONFIG_DIR`. It contains that dialect's user settings, session history,
plugins, commands, agents, and other Claude Code state. Project-level
`.claude/` files in the repository you are working in continue to work normally.

Existing dialects are migrated automatically: after updating and reinstalling
`cc-dialect`, their private `claude/` directory is created on the next launch. You
do not need to recreate the dialect, re-authenticate its proxy, or reinstall its
shim. Conversations previously stored in the shared `~/.claude` directory do
not automatically appear in the new isolated history.

If a Zsh alias already uses the generated command name, it takes precedence
over the executable. Remove the alias from `~/.zshrc`, then run `unalias
<name>` in terminals that were already open. Both `cc-dialect shim install` and
`cc-dialect doctor` detect these collisions.

The same applies to existing executables. `cc-dialect create` checks the
preferred `cc-` command name and recommends an unambiguous alternative when it
is already an alias or executable. Shim installation refuses an ambiguous name
and lists the conflicting paths. Existing dialect configurations can keep a
legacy internal name such as `gemini`; shim installation automatically adds the
recommended `cc-` prefix:

```sh
cc-dialect shim install gemini
cc-gemini
```

Use `--name` only when you want a different command name.

## Useful commands

```sh
cc-dialect list
cc-dialect show cc-codex
cc-dialect doctor
cc-dialect remove cc-codex
cc-dialect --version
```

`cc-dialect remove <name>` stops that dialect's proxy and permanently removes
its configuration, OAuth credentials, and isolated Claude Code history. Shims
are ordinary files and must be removed separately:

```sh
cc-dialect remove cc-gemini
rm ~/.local/bin/cc-gemini
```

To erase every currently configured dialect, remove each name shown by
`cc-dialect list`, then remove its shim. To uninstall the manager after that:

```sh
rm ~/.local/bin/cc-dialect
rm -rf "$HOME/Library/Application Support/claude-dialects"
```

The final `rm -rf` is intentionally explicit because it permanently deletes
all remaining provider credentials and conversation history stored by Claude
Dialects.

CLIProxyAPI is pinned as a Go dependency so a new upstream release cannot alter
an already-built executable. Its MIT license permits embedding and
redistribution. Licenses and notices for all modules compiled into the binary
are included in `THIRD_PARTY_NOTICES.md` and regenerated after dependency
updates.

## Contributing and security

Discuss changes in an issue before opening a pull request. External pull
requests must link an issue carrying the `status:accepted` label; unsolicited
pull requests are closed automatically. AI-assisted contributions must be
disclosed and fully understood, reviewed, and tested by the contributor. See
[CONTRIBUTING.md](CONTRIBUTING.md).

Report vulnerabilities privately through
[GitHub Security Advisories](https://github.com/stefandevo/claude-dialects/security/advisories/new),
not through public issues. See [SECURITY.md](SECURITY.md).

## License

Claude Dialects is available under the [MIT License](LICENSE).

## Sources used for the integration

- [CLIProxyAPI Claude Code configuration](https://help.router-for.me/agent-client/claude-code)
- [CLIProxyAPI provider and model overview](https://help.router-for.me/introduction/what-is-cliproxyapi)
- [CLIProxyAPI Codex setup](https://help.router-for.me/agent-client/codex)
- [Claude Code model and effort configuration](https://code.claude.com/docs/en/model-config)
- [Kimi K3 model, API identifier, effort, and compatibility notes](https://www.kimi.com/blog/kimi-k3)
- [xAI Grok 4.5 model documentation](https://docs.x.ai/developers/grok-4-5)
- [xAI Composer 2.5 announcement](https://x.ai/news/composer-2-5)
- [Cursor Composer model documentation](https://cursor.com/composer)
- [Cursor SDK announcement and local-agent example](https://cursor.com/changelog/sdk-release)
- [Cursor SDK custom tools and stores](https://cursor.com/changelog/sdk-updates-jun-2026)
- [Cursor Composer 2.5 variants and pricing](https://cursor.com/changelog/composer-2-5)
- [Cursor Grok 4.5 SDK availability](https://cursor.com/blog/grok-4-5)
- [MiniMax Anthropic-compatible API](https://platform.minimax.io/docs/api-reference/text-anthropic-api)
- [DeepSeek API documentation](https://api-docs.deepseek.com/)
