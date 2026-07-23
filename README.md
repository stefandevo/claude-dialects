# Claude Dialects

[![CI](https://github.com/stefandevo/claude-dialects/actions/workflows/ci.yml/badge.svg)](https://github.com/stefandevo/claude-dialects/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Website](https://img.shields.io/badge/website-claude--dialects.cc-d97757)](https://claude-dialects.cc)

Create multiple native-feeling Claude Code commands powered by different models.
Each generated dialect runs the real Claude Code interface with its own model,
environment, Claude Code configuration and history, local proxy key, ports,
runtime state, and embedded
[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) instance. Embedded
OAuth files are isolated per dialect; Cursor and Copilot provider credentials
may instead come from shared environment variables or system credentials.

The proxy is linked into the `cc-dialect` executable through CLIProxyAPI's Go SDK.
There is no separate proxy download, installation, container, or global
`~/.claude/settings.json` modification. Changes made with `/model`, `/effort`,
or other user-level Claude Code settings stay inside the active dialect.

> Current target: macOS only.

> [!IMPORTANT]
> This is an independent, unofficial project. It is not affiliated with or
> endorsed by Anthropic, OpenAI, Google, Moonshot AI, Z.ai, xAI, Cursor,
> GitHub, Microsoft, MiniMax, DeepSeek, or the CLIProxyAPI maintainers. Product
> and company names are trademarks of their respective owners. You are
> responsible for complying with each provider's terms, subscription rules,
> and usage policies.

## Contents

- [Install Claude Dialects](#install-claude-dialects)
- [Create your first dialect](#create-your-first-dialect)
- [Provider guides](#provider-guides)
  - [OpenAI Codex](#openai-codex)
  - [Z.ai GLM](#zai-glm)
  - [Moonshot Kimi](#moonshot-kimi)
  - [Google Gemini](#google-gemini)
  - [xAI Grok, Grok Build, and Composer](#xai-grok-grok-build-and-composer)
  - [MiniMax](#minimax)
  - [DeepSeek](#deepseek)
  - [Cursor](#cursor)
  - [GitHub Copilot](#github-copilot)
  - [Anthropic Claude](#anthropic-claude)
- [Mix multiple providers in one session](#mix-multiple-providers-in-one-session)
- [Run several dialects](#run-several-dialects)
- [Native Claude shortcuts](#native-claude-shortcuts)
- [Presets and custom dialects](#presets-and-custom-dialects)
- [Switch model and effort](#switch-model-and-effort-inside-a-conversation)
- [Detect configured dialects](#detect-configured-and-running-dialects)
- [Web dashboard](#web-dashboard)
- [Operations and security](#proxy-and-authentication-commands)
- [Build local assets](#build-local-assets)

## Install Claude Dialects

Requirements:

- macOS;
- Go 1.26.5 or newer
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) available as
  `claude`
- optionally, Node.js 22.13 or newer and npm for Cursor and GitHub Copilot SDK dialects

The dashboard frontend is already compiled into and embedded in the Go executable.
Node.js is not required to run the dashboard or for a normal `make build` or
`make install`; it is a contributor dependency only when rebuilding or verifying
the dashboard source.

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

### Update Claude Dialects

Once installed, update to the latest version without keeping the original
clone around:

```sh
cc-dialect upgrade
```

This shallow-clones the repository into a temporary directory, builds the
latest `main` (use `--ref <branch-or-tag>` for a specific ref), atomically
replaces the installed `cc-dialect` binary, and then runs
`cc-dialect doctor --fix` with the new binary to restart any proxies or
bridges still running the old build and reinstall bridge SDK runtimes whose
pinned versions changed. It needs the same git and Go toolchain as the
initial install — Node.js is not required. Existing dialects, credentials,
and installed shims are untouched; if anything fails before the replacement
step, the installed binary is left as it was. From a source checkout, use
`git pull && make install` instead — `upgrade` refuses to overwrite a
development build inside a checkout.

## Create your first dialect

Every dialect follows the same sequence:

1. Create its isolated configuration.
2. Authenticate with the provider or export its API key.
3. Install the generated shell command.
4. Run that command from any directory.

For example, create an OpenAI Codex dialect:

```sh
cc-dialect create cc-codex --preset codex-sol
cc-dialect auth cc-codex codex
cc-dialect shim install cc-codex
cc-codex
```

For preset-based routes, `create` prints the remaining required steps in the
correct order. An arbitrary custom model ID cannot determine its provider, so
you must supply and follow the appropriate authentication or token route
yourself. OAuth dialects will not launch or list models before authentication;
the error also includes the exact command to run.

Use the `cc-<provider>` naming convention. It makes generated commands easy to
recognize and avoids collisions with existing provider CLIs such as `gemini`
or `cursor`.

## Provider guides

Choose one provider below and follow its complete setup block. The recommended
command name is only a convention; each provider can have multiple independently
named dialects.

| Provider route | Presets | Authentication | Recommended command |
| --- | --- | --- | --- |
| [OpenAI Codex](#openai-codex) | `codex-sol`, `codex` | ChatGPT OAuth | `cc-codex` |
| [Z.ai GLM](#zai-glm) | `glm` | `ZAI_API_KEY` | `cc-glm` |
| [Moonshot Kimi](#moonshot-kimi) | `kimi` | Kimi OAuth | `cc-kimi` |
| [Google Gemini](#google-gemini) | `gemini` | Google OAuth through Antigravity | `cc-gemini` |
| [xAI](#xai-grok-grok-build-and-composer) | `grok`, `grok-build`, `composer` | xAI OAuth | `cc-grok` |
| [MiniMax](#minimax) | `minimax` | `MINIMAX_API_KEY` | `cc-minimax` |
| [DeepSeek](#deepseek) | `deepseek` | `DEEPSEEK_API_KEY` | `cc-deepseek` |
| [Cursor](#cursor) | `cursor-composer`, `cursor-composer-fast`, `cursor-grok`, `cursor-auto` | Cursor API key | `cc-cursor` |
| [GitHub Copilot](#github-copilot) | `copilot-auto`, `copilot-mai`, `copilot-codex`, `copilot-claude`, `copilot-gemini` | GitHub Copilot login | `cc-copilot` |
| [Anthropic Claude](#anthropic-claude) | `claude` | Anthropic OAuth | `cc-claude` |
| [Mix multiple providers](#mix-multiple-providers-in-one-session) | `mixed-frontier` | Several OAuth logins | `cc-mixed` |

### OpenAI Codex

Use `codex-sol` for GPT-5.6 Sol as both the main and subagent model:

```sh
cc-dialect create cc-codex --preset codex-sol
cc-dialect auth cc-codex codex
cc-dialect shim install cc-codex
cc-codex
```

Use `--preset codex` instead to make GPT-5.6 the main model while retaining
Sol, Terra, and Luna for the `opus`, `sonnet`, and `haiku` menu entries. Both
routes authenticate through ChatGPT OAuth and the embedded CLIProxyAPI
instance.

### Z.ai GLM

GLM uses Z.ai's Anthropic-compatible API and current GLM-5.2 flagship:

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

### Moonshot Kimi

Kimi authenticates through Moonshot's OAuth flow:

```sh
cc-dialect create cc-kimi --preset kimi
cc-dialect auth cc-kimi kimi
cc-dialect shim install cc-kimi
cc-kimi
```

The rolling `kimi` preset currently uses Kimi K3 as its main model. Kimi K3
uses `max` effort by default; Moonshot says lower effort levels will arrive in
later updates. Keep its preset at `auto` so the provider selects the supported
default. The `sonnet` and `haiku` menu entries select Kimi K2.7 Code Highspeed
and Kimi K2.6.

### Google Gemini

Gemini uses Google OAuth through CLIProxyAPI's Antigravity provider:

```sh
cc-dialect create cc-gemini --preset gemini
cc-dialect auth cc-gemini antigravity
cc-dialect shim install cc-gemini
cc-gemini
```

The preset uses `gemini-pro-agent` as its main and `opus` model, with Gemini
3.5 Flash variants for the lower tiers.

### xAI Grok, Grok Build, and Composer

xAI authentication is shared conceptually, but each model family gets its own
preset and can have its own dialect:

```sh
# Grok 4.5
cc-dialect create cc-grok --preset grok
cc-dialect auth cc-grok xai
cc-dialect shim install cc-grok

# Grok Build
cc-dialect create cc-grok-build --preset grok-build
cc-dialect auth cc-grok-build xai
cc-dialect shim install cc-grok-build

# Cursor Composer 2.5 Fast as exposed by xAI Grok Build
cc-dialect create cc-composer --preset composer
cc-dialect auth cc-composer xai
cc-dialect shim install cc-composer

cc-grok
# Or run cc-grok-build / cc-composer.
```

The `grok`, `grok-build`, and `composer` presets remain separate model
families. `composer` uses Cursor Composer 2.5 Fast as exposed by xAI Grok
Build; it is not a Grok foundation model. Availability depends on the models
enabled for the authenticated xAI account.

### MiniMax

MiniMax uses its Anthropic-compatible API:

```sh
export MINIMAX_API_KEY="your_minimax_api_key"
cc-dialect create cc-minimax --preset minimax
cc-dialect shim install cc-minimax
cc-minimax
```

The preset maps every Claude Code model alias to `MiniMax-M2.7`.

### DeepSeek

DeepSeek also uses an Anthropic-compatible API:

```sh
export DEEPSEEK_API_KEY="your_deepseek_api_key"
cc-dialect create cc-deepseek --preset deepseek
cc-dialect shim install cc-deepseek
cc-deepseek
```

The preset maps the main, subagent, and `opus` selections to
`deepseek-v4-pro`; `sonnet` and `haiku` use `deepseek-v4-flash`.

### Cursor

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

MCP and other tools remain owned by Claude Code. The bridge exposes the tool
definitions to Cursor as SDK custom tools, captures the selected call, and
returns it to Claude Code for permission approval and execution. The inner
Cursor SDK sandbox and Smart Auto Review are disabled because headless local
SDK runs cannot interactively approve calls to the SDK's synthetic
`custom-user-tools` MCP server. This does not bypass Claude Code's permission
prompt or execute the MCP action inside Cursor.

Cursor and Claude Code have built-in tools with overlapping names but different
argument schemas—for example, Cursor uses `path` where Claude Code's `Read`
expects `file_path`. The bridge therefore gives every forwarded tool a private
`cc_tool_` alias inside the Cursor SDK and translates the selected alias back to
the original Claude Code tool name. This prevents Composer from accidentally
calling a Cursor-native schema and returning invalid arguments to Claude Code.

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

### GitHub Copilot

GitHub Copilot dialects use the official `@github/copilot-sdk` and its bundled
Copilot CLI runtime. Install it and authenticate once:

```sh
cc-dialect copilot install
cc-dialect copilot login
cc-dialect copilot status
cc-dialect copilot models
```

Create a general Copilot dialect that lets Copilot choose from the models
enabled for the account:

```sh
cc-dialect create cc-copilot --preset copilot-auto
cc-dialect shim install cc-copilot
cc-copilot
```

Available Copilot presets are:

- `copilot-auto` — Copilot chooses from the models enabled for the account
- `copilot-mai` — Microsoft MAI-Code-1-Flash (`mai-code-1-flash`)
- `copilot-codex` — GPT-5.3-Codex
- `copilot-claude` — Claude Sonnet 4.6 with Claude Haiku 4.5 for the Haiku tier
- `copilot-gemini` — Gemini 3.1 Pro Preview with Gemini 3.5 Flash for lower tiers

For example, replace `copilot-auto` with `copilot-mai` and name the dialect
`cc-copilot-mai` to force Microsoft's Copilot-native MAI-Code-1-Flash model.

The live SDK catalog remains authoritative. GitHub model availability depends
on the Copilot plan and organization policy, and models may be added, replaced,
or retired. Use any currently enabled model without waiting for a new preset:

```sh
cc-dialect copilot models
cc-dialect create cc-copilot-custom \
  --preset copilot-auto \
  --model model-id-from-the-list
```

Authentication and SDK authorization are separate checks.
`cc-dialect copilot status` can show a valid GitHub login while
`cc-dialect copilot models` or a model request returns
`not authorized to use this Copilot feature`. That response comes from GitHub
and means the Copilot SDK/CLI feature or selected model is not enabled for the
account or its organization policy; it is not a local proxy or port failure.

The bridge runs the SDK in an empty host mode with only Claude Code's declared
tools. Copilot's own filesystem, shell, MCP, and agent tools are not exposed.
Tool calls are returned to Claude Code for its normal permission and execution
flow. Each dialect has a private bridge port and `COPILOT_HOME`; the GitHub
account login can come from Copilot's system credential, `COPILOT_GITHUB_TOKEN`,
`GH_TOKEN`, or `GITHUB_TOKEN`.

Reasoning effort is forwarded only when the live model metadata advertises it.
MAI-Code-1-Flash currently uses its adaptive provider behavior and does not
advertise configurable reasoning levels.

`@github/copilot-sdk` is pinned for reproducible installation. Re-run
`cc-dialect copilot install` after updating `cc-dialect` when the pinned SDK
version changes. It remains a separately installed GitHub dependency under
GitHub's terms. Copilot prompts consume the account's normal Copilot usage
allowance.

### Anthropic Claude

The `claude` preset routes Claude Code through the embedded proxy with a
separate Anthropic OAuth login and isolated Claude Code configuration:

```sh
cc-dialect create cc-claude --preset claude
cc-dialect auth cc-claude claude
cc-dialect shim install cc-claude
cc-claude
```

This differs from a native shortcut: `cc-claude` has private settings,
credentials, and history, while a native shortcut uses the regular
`~/.claude` environment. The preset currently maps the main and `opus` routes
to Claude Fable 5, with Claude Sonnet 4.6 and Claude Haiku 4.5 for the lower
tiers.

## Mix multiple providers in one session

Claude Code pins each subagent to a model tier — `opus`, `sonnet`, or `haiku` —
and Claude Dialects maps every tier to a model ID. Because a single dialect can
hold OAuth credentials for more than one provider at once, you can point each
tier at a **different** provider. The result is one Claude Code session whose
agents run on different providers: the main agent on one model, opus-tier
subagents on another, sonnet-tier on a third, and so on — all inside the same
conversation.

The `mixed-frontier` preset wires this up out of the box. It runs Claude Fable 5
as the main and subagent model and spreads the tiers across OpenAI, Moonshot,
and xAI:

| Tier / role | Model | Provider | OAuth login |
| --- | --- | --- | --- |
| Main + subagent | `claude-fable-5` | Anthropic | `cc-dialect auth cc-mixed claude` |
| `/model opus` | `gpt-5.6-sol` | OpenAI Codex | `cc-dialect auth cc-mixed codex` |
| `/model sonnet` | `kimi-k3` | Moonshot Kimi | `cc-dialect auth cc-mixed kimi` |
| `/model haiku` | `grok-4.5` | xAI Grok | `cc-dialect auth cc-mixed xai` |

Because the tiers span providers, the dialect needs each provider's OAuth login.
Authenticate them into the **same** dialect, one command per provider:

```sh
cc-dialect create cc-mixed --preset mixed-frontier
cc-dialect auth cc-mixed claude
cc-dialect auth cc-mixed codex
cc-dialect auth cc-mixed kimi
cc-dialect auth cc-mixed xai
cc-dialect shim install cc-mixed
cc-mixed
```

`cc-dialect create` and `cc-dialect doctor` report which providers still need a
login, and `cc-mixed` refuses to start until every tier's provider is
authenticated — it never silently serves a partial set. `cc-dialect models
cc-mixed` lists the full catalog aggregated across all authenticated providers,
and the [web dashboard](#web-dashboard) shows a per-provider authentication
status for the dialect.

Build your own mix by overriding any tier with the `--opus-model`,
`--sonnet-model`, and `--haiku-model` flags. Mixing is currently limited to the
five OAuth providers (Codex, Anthropic Claude, Kimi, Gemini via Antigravity, and
xAI); the model IDs above are current defaults and roll over as each provider
ships new versions.

> **Provider terms still apply.** Mixing providers in one session runs each
> request against that provider's own subscription and usage terms. Claude
> Dialects is an independent, unofficial project and is not affiliated with or
> endorsed by any provider — see the disclaimer in [Files and
> security](#files-and-security).

## Run several dialects

Each dialect gets checked high-numbered localhost ports, a private local proxy
key and runtime state, and its own Claude Code state. Provider credentials remain
route-dependent. Create as many as you need:

```sh
cc-dialect create cc-codex-work --preset codex
cc-dialect auth cc-codex-work codex
cc-dialect shim install cc-codex-work

cc-dialect create cc-kimi-work --preset kimi
cc-dialect auth cc-kimi-work kimi
cc-dialect shim install cc-kimi-work

cc-codex-work
cc-kimi-work
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

## Presets and custom dialects

List the presets included in your installed version:

```sh
cc-dialect presets
```

Preset names select provider defaults; dialect names become shell commands.
Arbitrary names and existing legacy names remain supported. Use the recommended
names in the [provider table](#provider-guides), or append a purpose or model
when you need more than one route, such as `cc-codex-work`,
`cc-cursor-grok`, or `cc-copilot-mai`.

Provider-named presets such as `kimi` are rolling defaults for newly created
dialects. Updating and reinstalling the `cc-dialect` executable does not silently
change an existing dialect. Apply the latest preset explicitly:

```sh
cc-dialect upgrade
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
CLIProxyAPI translates Claude's adaptive reasoning request into the upstream
provider's reasoning format when that provider supports it. Cursor SDK dialects
similarly select a matching catalog variant when Cursor exposes an
effort/thinking parameter; if the selected model has no such variant, its
catalog default is used.

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

## Web dashboard

Launch the local management dashboard with:

```sh
cc-dialect web
```

By default, `cc-dialect` binds an available ephemeral port on
`127.0.0.1`, prints the selected URL, and opens it in the default browser. The
server stays in the foreground; press Ctrl-C to stop it. Suppress only the
browser launch, or choose a fixed loopback address, with:

```sh
cc-dialect web --no-browser
cc-dialect web --listen 127.0.0.1:8765
cc-dialect web --listen '[::1]:8765'
```

`--listen` accepts a numeric loopback IP and port only. Hostnames such as
`localhost`, wildcard addresses such as `0.0.0.0`, LAN addresses, remote access,
and reverse-proxy deployment are not supported.

The dashboard can:

- inspect safe views of configured preset and custom dialects, their effective
  model and runtime settings, built-in presets, tracked native launchers, and
  Cursor runtime readiness;
- create and update dialects, including model aliases, effort, concurrency,
  ports, tool search, and custom Anthropic-compatible routing;
- start, stop, and restart dialect proxies and provider bridges;
- install or refresh the pinned Cursor runtime; after a successful update,
  currently running Cursor dialects are stopped so they cannot keep using stale
  bridge code, and must be started again explicitly;
- install, update, verify, and remove tracked native Claude launchers; and
- require exact typed-name confirmation before deleting a dialect or native
  launcher.

A dialect edit validates the replacement before stopping anything, then stops
the old runtime, saves the new configuration, and leaves it stopped. It preserves
the private local API key, isolated authentication and Claude Code state,
history, installed shims, the proxy port unless explicitly changed, and the
bridge port when the bridge type stays the same. Dashboard edits are full
replacements of the public fields: custom configurations that depend on hidden
`ExtraEnv` values, other unexposed bridge/authentication fields, or URL userinfo,
query strings, or fragments should be updated with the CLI instead.

Native launchers are tracked in `config.json` with their canonical path and a
content digest. A tracked launcher cannot be moved in place; remove and reinstall
it to use another path. Missing or externally modified launcher files cannot be
updated or removed through the dashboard. Older untracked launchers are not
scanned automatically and are adopted only when the file at the requested path
exactly matches the launcher that the current executable would generate for the
current `claude` path and dangerous-mode setting.

The dashboard API omits private local API keys, OAuth credential contents,
upstream token values, `ExtraEnv` values, the Cursor API key, and native-launcher
digests. It exposes only safe metadata such as token environment-variable names,
extra environment-variable key names, sanitized upstream URLs, and whether a
Cursor key is present.

The dashboard does not perform OAuth login, install or log in to GitHub Copilot,
show proxy logs, install or manage dialect shims, run doctor diagnostics, list
live provider models, or launch interactive Claude Code sessions. Use the
existing CLI commands for those workflows.

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
config.json          # dialect configuration and tracked native-launcher registry
.state.lock          # owner-only cross-process mutation lock
instances/
  cc-codex/
    auth/
    claude/
    proxy.yaml
    proxy.pid
    proxy.log
    proxy.version
  cc-kimi/
    auth/
    claude/
    proxy.yaml
    proxy.pid
    proxy.log
    proxy.version
  cc-cursor/
    claude/
    cursor-workspace/
    cursor-bridge.pid
    cursor-bridge.log
    cursor-bridge.version
    proxy.yaml
    proxy.pid
    proxy.log
    proxy.version
  cc-copilot-mai/
    claude/
    copilot-home/
    copilot-bridge.pid
    copilot-bridge.log
    copilot-bridge.version
    proxy.yaml
    proxy.pid
    proxy.log
    proxy.version
cursor-runtime/
  cursor_bridge.mjs
  node_modules/@cursor/sdk/
copilot-runtime/
  copilot_bridge.mjs
  node_modules/@github/copilot-sdk/
```

Proxy servers bind only to `127.0.0.1`. Configuration, local API keys, and OAuth
credentials use owner-only permissions. The CLI changes environment variables
only for the launched Claude Code process. Mutating operations are serialized
across CLI and dashboard processes with the owner-only `.state.lock`; configuration
and launcher files are written atomically.

The dashboard accepts only numeric loopback listeners. Every request must use
the exact bound `Host`; state-changing API requests must also use the exact local
`Origin` and the per-process CSRF token obtained by the embedded frontend. These
browser controls do not authenticate local processes, so the dashboard assumes
one trusted user and no hostile processes on the same machine. Do not expose it
through port forwarding, a reverse proxy, or another network interface.

The optional Cursor bridge also binds only to `127.0.0.1`, authenticates every
request with the dialect's private local key, and keeps its SDK workspace and
metadata separate per dialect. `CURSOR_API_KEY` is read from the environment at
startup and is not written into `config.json`.

The optional Copilot bridge follows the same localhost and private-key
boundaries. Its per-dialect `copilot-home/` isolates SDK state, while the
GitHub login may be read from the system credential store or standard Copilot
token environment variables. Tokens are never copied into `config.json`.

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
cc-dialect web
cc-dialect doctor
cc-dialect upgrade
cc-dialect remove cc-codex
cc-dialect --version
```

`cc-dialect doctor` detects misconfigurations (shadowed shims, missing API keys, incorrect SDK versions). Add the `--fix` flag (`cc-dialect doctor --fix`) to automatically apply deterministic repairs: it will restart any proxies or Node bridges that are running stale binaries (e.g. after you updated `cc-dialect`), and it will re-install any Node SDK bridge runtimes that do not match the current required version. Interactive steps like OAuth logins are left for you to complete.

`cc-dialect upgrade` fetches the latest source, rebuilds and atomically
replaces the installed binary, and finishes with `cc-dialect doctor --fix` so
stale runtimes are restarted in the same pass. See
[Update Claude Dialects](#update-claude-dialects).


When upgrading from the former `dialect` executable, `make install` removes that
old command. Existing configuration, OAuth credentials, and conversations are
preserved. Regenerate any older shims so they point directly to `cc-dialect`:

```sh
cc-dialect list
# Replace legacy-name with an existing dialect shown above.
cc-dialect shim install legacy-name --name cc-codex
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
redistribution. Licenses and notices for Go modules compiled into the binary and
production npm dependencies bundled into the embedded dashboard are included in
`THIRD_PARTY_NOTICES.md` and regenerated after dependency updates.

## Build local assets

The React dashboard is built to `internal/app/dashboard/dist/`, committed to the
repository, and embedded into the executable with Go's `embed` package. A normal
`make build` or `make install` compiles the committed assets and does not invoke
Node.js. Contributors who change dashboard source need npm and a package-compatible
Node.js release (Node.js 22.13.x or 24+), and must rebuild and commit the updated
`dist/` files. Production npm dependencies bundled into those assets are included
in `THIRD_PARTY_NOTICES.md`; development-only frontend packages are not shipped in
the binary.

This project does not publish prebuilt binaries or GitHub releases. Everyone
builds the executable from the checked-out source. To create a shareable local
macOS archive and checksum instead of installing it:

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
- [Official GitHub Copilot SDK](https://github.com/github/copilot-sdk)
- [GitHub Copilot SDK authentication](https://docs.github.com/en/copilot/how-tos/copilot-sdk/auth/authenticate)
- [GitHub Copilot CLI model identifiers](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference)
- [GitHub Copilot supported models](https://docs.github.com/en/copilot/reference/ai-models/supported-models)
- [MAI-Code-1-Flash announcement](https://github.blog/changelog/2026-06-02-mai-code-1-flash-is-now-available-for-github-copilot/)
- [MiniMax Anthropic-compatible API](https://platform.minimax.io/docs/api-reference/text-anthropic-api)
- [DeepSeek API documentation](https://api-docs.deepseek.com/)
