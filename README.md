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

> Current targets: macOS and Linux, on amd64 and arm64.

> [!NOTE]
> Linux support is new and not yet fully verified in day-to-day use. CI builds
> and runs the full test suite natively on Ubuntu (amd64); `linux/arm64` is
> cross-compiled and vetted but never executed by a runner. Provider OAuth
> flows, the Cursor and GitHub Copilot bridges, and packaging a macOS archive
> from a Linux host have not been exercised on Linux. macOS is unaffected.
> Please report anything that misbehaves in a
> [new issue](https://github.com/stefandevo/claude-dialects/issues/new/choose).

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
- [Context window and auto-compaction](#context-window-and-auto-compaction)
- [Switch model and effort](#switch-model-and-effort-inside-a-conversation)
- [Detect configured dialects](#detect-configured-and-running-dialects)
- [Web dashboard](#web-dashboard)
- [Operations and security](#proxy-and-authentication-commands)
- [Build local assets](#build-local-assets)

## Install Claude Dialects

Requirements:

- macOS or Linux, on amd64 or arm64
- Go 1.26.6 or newer
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
To make that PATH change persist across terminal restarts, append it to your
shell's startup file — `~/.zshrc` for Zsh (the macOS default) or `~/.bashrc`
for Bash (the default on most Linux distributions):

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc   # Zsh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc  # Bash
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
| [Cursor](#cursor) | `cursor-composer`, `cursor-composer-fast`, `cursor-grok`, `cursor-auto`, `cursor-mix` | Cursor API key | `cc-cursor` |
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

#### Troubleshooting `auth_unavailable`

If `cc-codex` reports `503 auth_unavailable` after ChatGPT OAuth already
succeeded, do not re-authenticate immediately. An upstream HTTP 502 or 503 can
put that model's credential into a short CLIProxyAPI cooldown (usually about
one minute). Retries during the cooldown can fail almost instantly without
another OpenAI request, so `auth_unavailable` does not necessarily mean the
OAuth credential is missing.

Run `cc-dialect doctor`. For an authenticated Codex dialect, it inspects recent
`auth/logs/error-v1-messages-*.log` and `proxy.log` entries and reports the
upstream HTTP failure separately from a fast cooldown retry. The diagnostic
names the failing request model when the log contains it and suggests a
different configured `/model` tier (for example, Sonnet/Terra when Sol failed),
restarting the embedded proxy with `cc-dialect proxy cc-codex restart`, and
opening the full logs with `cc-dialect proxy cc-codex logs`.

The native Codex CLI uses a WebSocket upstream, while a Codex dialect sends
Claude Code's requests through CLIProxyAPI's HTTP upstream. A model such as Sol
can therefore fail over HTTP while it still works in the native Codex CLI.
Restarting clears the generated proxy's in-memory cooldown, but it cannot make
an overloaded HTTP endpoint available.

### Z.ai GLM

GLM uses Z.ai's Anthropic-compatible API and current GLM-5.3 flagship:

```sh
export ZAI_API_KEY="your_zai_api_key"
cc-dialect create cc-glm --preset glm
cc-dialect shim install cc-glm
cc-glm
```

The GLM preset maps `opus` to `glm-5.3`, `sonnet` to `glm-5-turbo`, and
`haiku` to `glm-4.7`. Its `auto` effort setting leaves GLM-5.3 at the provider default
(`max`). GLM-5.3 runs three effort levels — `low`, `high`, and `max` — and Z.ai
folds Claude Code's scale onto them: `low` stays `low`, `medium` and `high` both
become `high`, and `xhigh` and `max` both become `max`. Thinking cannot be
turned off; asking for it disabled is converted to `low` rather than honored,
so the model still reasons briefly.

`glm-5.3` answered on a live GLM Coding Plan key on 2026-08-15. Z.ai's model
page still reads "The GLM-5.3 API is coming soon", so a pay-as-you-go key may
still be refused where a Coding Plan key succeeds — that half is untested. If
yours is refused, `glm-4.7` is the newest model the endpoint serves under its
own name, and it holds the same 200,000 tokens the preset declares. Overriding
the models changes the route, so a preset's window no longer applies on its own
and has to be restated — without it the dialect is left uncalibrated:

```sh
cc-dialect create cc-glm --preset glm \
  --model glm-4.7 \
  --subagent-model glm-4.7 \
  --opus-model glm-4.7 \
  --context-window 200000
```

There is no point pinning `glm-5.2`. Z.ai resolves retired model IDs forward on
this endpoint, so a request for `glm-5.2` is answered by `glm-5.3` (verified
2026-08-15); only an ID it does not recognize at all is rejected, with
`400 modelCode: does not exist`. A stale model name therefore never announces
itself — it quietly serves something newer.

The `sonnet` tier stays on GLM-5-Turbo (agent-optimized for heavier reasoning),
while the `haiku` tier uses GLM-4.7 at half the price ($0.60/$2.20 vs
$1.20/$4.00 per 1M tokens). Both hold 200,000 tokens, so the old window-spread
argument that once ruled out the cheaper option no longer applies — Claude Code
sends the `haiku` tier short auxiliary work (titles, summaries, classification)
that doesn't need agent capabilities. GLM-4.5-Air held only 131,072 and is no
longer served under its own name (Z.ai resolves it to `glm-4.7`, verified
2026-08-15), so it is not an option either way.

An existing `cc-glm` keeps whatever it was created with. Nothing rewrites a
window already on disk: [a recorded window is never raised on your
behalf](#custom-dialects), because silently widening one is the direction that
can outrun a provider, and a stored one is not narrowed either, because it
may have been set deliberately.

A dialect old enough to carry a `glm-4.5-air` haiku tier and a 131,072-token
window is already being answered by `glm-4.7` (Z.ai resolves the retired ID
forward), so re-running `cc-dialect create cc-glm --preset glm` lifts the
window to 200,000 — the half Claude Code cannot learn from the provider.
(A `cc-glm` that predates the stored window entirely no longer matches this
preset field for field, so it is left uncalibrated rather than given a window
its old tier cannot hold; `doctor` reports it with the same command.)

A dialect created against the previous preset (both lower tiers on
`glm-5-turbo`, window already 200,000) is not harmed by the mismatch: Turbo
is still the sonnet model, and the endpoint has no opinions about what a
dialect's internal haiku slot should be. Re-running `cc-dialect create cc-glm
--preset glm` updates the haiku route to `glm-4.7` without touching the
window. `doctor` reports both through [preset drift](#custom-dialects),
naming the tiers and window the preset has since changed, with the `create`
command that adopts the current preset — restating a moved window through
`--context-window`.

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
- `cursor-mix` — Composer 2.5, Grok 4.5, and Kimi K3 across the Opus, Sonnet, and Haiku tiers
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

The SDK writes checkpoints and run events to a local agent store and reads it
back in full. The bridge scopes one store directory to each Claude Code turn
(`cursor-workspace/.cursor-dialect-state/run-{uuid}/`), reuses the same Cursor
agent across tool-call steps within that turn, and deletes the store when the
turn finishes or goes idle. Every bridge launch starts from an empty parent
directory, so stores cannot grow without bound across unrelated turns.

Claude Code still sends the full transcript on every HTTP request; the bridge
matches a continuation by hashing the message prefix and only forwards the new
tool-result step to the live agent, so multi-tool turns avoid replaying the
whole prompt each step.

Streaming chat completions write SSE headers and the initial role chunk before
the Cursor run starts, then forward `text-delta` output from the SDK's `onDelta`
callback as incremental `delta.content` chunks. Summary and thinking progress
lines are streamed for visibility but are not merged into the billed assistant
text. A periodic SSE comment heartbeat keeps long runs from going idle on the
proxy path, and per-request timing (run start, first delta, first tool call, run
end) is logged to `cursor-bridge.log`. Non-streaming requests stay buffered
until the run settles.

Both SDK bridges pass the conversation to their model as flattened text, with
past tool calls and results introduced by `ASSISTANT TOOL CALL <tool>:` and
`CLAUDE CODE TOOL RESULT <tool>:` label lines. The bridge preamble tells the
model those labels are framing it must never reproduce, and the bridge also
filters its replies: a reply line that matches a label exactly — the label, one
tool name, a closing colon, and nothing else — is dropped along with the rest of
that reply, and the suppression is recorded in `cursor-bridge.log` or
`copilot-bridge.log`. Label text inside a fenced code block, indented, or used
mid-sentence is left alone, so a reply that explains the transcript format is
not affected. Tool calls themselves are unaffected: they travel as structured
calls, not as this text.

The bridge also survives faults raised outside a request, such as a broken pipe
inside the SDK. It logs them to `cursor-bridge.log`, fails the requests that
were in flight, and keeps serving instead of terminating. If the bridge does die
anyway, `cc-dialect proxy <name> status` and `cc-dialect doctor` report it as
**crashed** rather than stopped and point at `cc-dialect proxy <name> logs`,
where the fatal error is recorded; `cc-dialect doctor --fix` restarts a bridge
that crashed while its proxy is still running.

`@cursor/sdk` is pinned by `cc-dialect` for reproducible installs but remains a
separate Cursor-licensed dependency under Cursor's terms. Re-run
`cc-dialect cursor install` after updating `cc-dialect` when its pinned SDK
version changes. To apply a new `cc-dialect` build to an existing Cursor dialect,
re-run `cc-dialect create <name> --preset <preset>` — creating an existing name
is an upsert that preserves authentication, isolated state, and shims, so removing
and recreating the dialect is never the right response to an update.

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

`cursor-mix` spreads three Cursor-hosted models across the tier menu in one
route:

```sh
cc-dialect create cc-cursor-mix --preset cursor-mix
cc-dialect shim install cc-cursor-mix
cc-cursor-mix
```

Composer 2.5 is the default session model and `/model opus`, Grok 4.5 is
`/model sonnet`, and Kimi K3 is `/model haiku` — all served through the
Cursor SDK and `CURSOR_API_KEY`, with no OAuth splitting. Like the other
Cursor routes, Cursor publishes no per-model context window for this mix, so
it runs on the same 200,000-token conservative fallback as `cursor-composer`
and `cursor-grok`.

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

Replies are filtered for imitated transcript framing exactly as the Cursor
bridge does, with suppressions recorded in `copilot-bridge.log`. See the Cursor
bridge notes above for what the filter matches and what it leaves alone.

Reasoning effort is forwarded only when the live model metadata advertises it.
MAI-Code-1-Flash currently uses its adaptive provider behavior and does not
advertise configurable reasoning levels.

`@github/copilot-sdk` is pinned for reproducible installation. Re-run
`cc-dialect copilot install` after updating `cc-dialect` when the pinned SDK
version changes. It remains a separately installed GitHub dependency under
GitHub's terms. Copilot prompts consume the account's normal Copilot usage
allowance. To apply a new `cc-dialect` build to an existing Copilot dialect,
re-run `cc-dialect create <name> --preset <preset>` — creating an existing name
is an upsert that preserves authentication, isolated state, and shims, so removing
and recreating the dialect is never the right response to an update.

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

### Per-dialect statusline

Because each dialect runs with an isolated `CLAUDE_CONFIG_DIR`, the global
`~/.claude` statusline never applies. Instead every dialect gets a generated
statusline that names the dialect, so parallel terminals are easy to tell
apart:

```text
cc-codex · GPT-5.6 Sol · effort:auto · ctx 42%
```

`cc-dialect create` writes `instances/<name>/statusline.sh` and wires it into
the dialect's isolated `claude/settings.json`; dialects created before this
feature are backfilled the next time they run. The dialect name is colored per
provider route. The script uses `jq` — preinstalled on recent macOS, and
usually an explicit install on Linux (`apt install jq`, `dnf install jq`,
`pacman -S jq`). When `jq` is missing the statusline stays empty instead of
erroring.

- **Customize:** point the `statusLine` key in
  `instances/<name>/claude/settings.json` at your own script — a `statusLine`
  you set yourself is never overwritten. Avoid editing the generated
  `statusline.sh` in place: outdated generated scripts are refreshed on run
  after upgrades.
- **Remove:** delete the `statusLine` key from that `settings.json`. Once the
  generated script exists, the key is never re-added.

### No Claude attribution in commits

Claude Code adds a `Co-Authored-By: Claude <noreply@anthropic.com>` trailer to
commits, and an attribution block to pull request bodies, unless you turn it
off. The isolated `CLAUDE_CONFIG_DIR` means an opt-out in your global
`~/.claude/settings.json` never reaches a dialect, so each dialect gets its own:

```json
{
  "attribution": {
    "commit": "",
    "pr": ""
  }
}
```

An empty string hides the attribution. The key is only seeded when the dialect
has no attribution preference of its own — neither `attribution` nor the
deprecated `includeCoAuthoredBy` — so a value you set is never overwritten.

- **Customize:** set `attribution.commit` / `attribution.pr` in
  `instances/<name>/claude/settings.json` to your own trailer text.
- **Restore the trailer:** set an explicit value rather than deleting the key,
  e.g. `"attribution": {"commit": "Co-Authored-By: Claude <noreply@anthropic.com>"}`
  or `"includeCoAuthoredBy": true`. A missing key is re-seeded on the next run.
- **Backfill:** existing dialects are updated the next time they run, or all at
  once with `cc-dialect doctor --fix` — which `cc-dialect upgrade` invokes for
  you, so dialects you have not launched are covered too.

Project-level `.claude/settings.json` in a repository still takes precedence
over these per-dialect user settings.

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
  --context-window 372000 \
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

## Context window and auto-compaction

Claude Code compacts a conversation before it outgrows the model's context
window. To decide when, it needs to know how large that window is — and it
cannot recognize the provider model IDs a dialect routes to, such as
`gpt-5.6-sol` or `composer-2.5`. Without a declared capacity, a conversation can
reach the provider's real limit before any compaction happens, after which
further requests fail.

Every dialect therefore carries a **context window**: the number of tokens the
route can hold. When you launch a dialect, Claude Dialects exports it for that
Claude Code process as **both** `CLAUDE_CODE_AUTO_COMPACT_WINDOW` and
`CLAUDE_CODE_MAX_CONTEXT_TOKENS`, with the same value. Claude Code reads the two
through separate chains:

- `CLAUDE_CODE_AUTO_COMPACT_WINDOW` caps **when it compacts**.
- `CLAUDE_CODE_MAX_CONTEXT_TOKENS` is the window it **resolves for the model**.
  That is the denominator it reports against — the `ctx N%` in the statusline and
  its own `/context` view — and, because Claude Code compacts against the smaller
  of the two, a second cap on when it compacts. It applies only to model IDs
  Claude Code cannot recognize, so first-party `claude-…` IDs keep resolving
  through Claude Code's own registry.

Declaring only the first leaves the two indicators counting the same tokens over
different denominators. A `cc-glm` session showed `ctx 47%` beside `4% until
auto-compact` and compacted moments later: the percentage was dividing by Claude
Code's 200,000-token default instead of the 131,072 that dialect declared at the
time. Dialects with a window above 200,000 had the opposite fault — the
statusline sat at 100% for most of a long session.

The two readings still will not be identical. Claude Code holds back an output
allowance and a compaction buffer, so the countdown measures against a usable
ceiling roughly 33,000 tokens below the window while the percentage measures
against the window itself. That offset exists against first-party models too;
what the second variable removes is the wrong denominator, not the offset.

Because `CLAUDE_CODE_MAX_CONTEXT_TOKENS` is skipped for `claude-…` IDs, that
exclusion follows the **model in use**, not the dialect. `claude` and
`copilot-claude` map every tier to a Claude model, so they always report against
whatever Claude Code's own registry resolves, which need not equal the declared
window. `mixed-frontier` does so only on its main and subagent model
(`claude-fable-5`); switching to `/model opus`, `sonnet`, or `haiku` selects
GPT-5.6 Sol, Kimi K3, or Grok 4.5, and the declared window applies again.
**Compaction** can only come out tighter than the declared window, never looser,
because `CLAUDE_CODE_AUTO_COMPACT_WINDOW` has no such exclusion and Claude Code
takes the smaller of the two — so these conversations still cannot outrun the
route. Where the registry resolves *below* the declared window, though, they
compact earlier than that window implies and report against the registry value
rather than the declared one.

If you override either variable yourself through a dialect's `extraEnv`, note
that it is applied last and per variable — set **both**, or the reported
percentage and the compaction point go back to measuring against different
numbers, which is the split this section exists to close. `doctor` catches the
case where this leaves no usable window at all, but a one-sided override that is
still a valid number passes silently, so keeping the two in step is on you.

The division of responsibility is deliberate:

- **Claude Dialects supplies the capacity.** Nothing else. It never summarizes,
  truncates, or deletes conversation turns or tool results.
- **Claude Code owns compaction and every readout.** It applies its own
  threshold and buffer inside the declared capacity, and it uses the smaller of
  the configured and detected window. The value is raw model capacity, not a
  trigger point, so do not pre-apply a safety percentage yourself.

### Preset values

A preset that maps different models to `/model opus`, `sonnet`, `haiku`, or to
subagents uses the **smallest** window across every model it can select, because
Claude Code receives one window per process. That keeps switching models
mid-conversation and spawning subagents safe.

| Preset | Context window | Smallest supported model |
| --- | ---: | --- |
| `gemini` | 1,048,576 | Gemini Pro Agent and 3.5 Flash routes |
| `copilot-gemini` | 1,048,576 | Gemini 3.1 Pro and 3.5 Flash |
| `deepseek` | 1,000,000 | DeepSeek V4 Pro and V4 Flash |
| `grok` | 500,000 | Grok 4.5 |
| `codex-sol`, `codex` | 372,000 | GPT-5.6 Sol, Terra, and Luna |
| `mixed-frontier` | 372,000 | GPT-5.6 Sol |
| `kimi` | 262,144 | Kimi K2.7 Code Highspeed and K2.6 |
| `grok-build` | 256,000 | Grok Build 0.1 |
| `copilot-mai` | 256,000 | MAI-Code-1-Flash |
| `minimax` | 204,800 | MiniMax-M2.7 (input and output combined) |
| `claude` | 200,000 | Claude Sonnet 4.6 and Haiku 4.5 |
| `composer` | 200,000 | Grok Composer 2.5 Fast |
| `glm` | 200,000 | GLM-5-Turbo (sonnet), GLM-4.7 (haiku) |
| `cursor-composer`, `cursor-composer-fast` | 200,000 | Cursor Composer 2.5 route |
| `cursor-grok` | 200,000 | Cursor Grok route |
| `cursor-mix` | 200,000 | Cursor Composer/Grok/Kimi mixed route |
| `copilot-codex` | 200,000 | Copilot GPT-5.3-Codex route |
| `copilot-claude` | 200,000 | Claude Sonnet 4.6 and Haiku 4.5 |
| `cursor-auto`, `copilot-auto` | 128,000 | any model the route may select |

OAuth-backed values come from the embedded CLIProxyAPI model registry. Cursor
and GitHub publish no per-route context window, so those presets use a
conservative fallback rather than borrowing a number from a same-named direct
provider model; the `auto` routes are set low enough to cover whatever the
vendor selects. Each value carries its source and verification date in
`internal/app/context_window.go`, and a test fails the build if a preset is added
without one.

### Custom dialects

A custom model ID has no reviewed value, and Claude Dialects never guesses one
from a model name — a window larger than the route really supports is worse than
none. Declare it yourself:

```sh
cc-dialect create cc-my-model \
  --model vendor-model-id \
  --context-window 262144 \
  --base-url https://provider.example.com/api/anthropic \
  --token-env MY_PROVIDER_TOKEN
```

The value must be a positive integer of at most 20,000,000 tokens.

A capacity describes one specific set of models, so an existing stored value is
kept only while that set is unchanged. Updating a port, concurrency, or effort
keeps it; changing the primary model, a tier, or the upstream discards it. The
fully resolved route then adopts a built-in preset's reviewed window when every
model, tier, and upstream field matches exactly; otherwise `create` warns that
the dialect is uncalibrated unless you pass a new `--context-window`. Carrying
the old number across a model change would be the more damaging default: a
window larger than the new route supports is exactly what lets a conversation
run past the provider's real limit, while an unset one only returns to
uncalibrated behavior and says so. A stored value also survives naming the same
preset again: that asks for the preset's models, not for its window to be raised.
An explicit `--context-window` always wins.

Dialects created before this field existed are migrated on first read through
the same exact-route rule. The stored preset label supplies the window while it
still describes that route. If the label has diverged but the complete route is
field-for-field identical to another preset, the dialect adopts that route's
reviewed window while keeping its original label; a route that matches no preset
exactly remains unset and is reported by `doctor`. The `preset` key is itself
younger than some dialects, so one that records no label still qualifies for an
exact match — and the matched name is written alongside its window. An
unrecognized label may belong to a newer build, so it receives an exact route's
window without being renamed.

This route fallback calibrates only the window; it does not choose the preset a
remedy command may restate. A recognized but diverged label still produces a
command based on that stored preset plus the route overrides, while an
unrecognized label is not rewritten through today's match. Matching is equality,
not resemblance: a single hand-swapped tier that completes no preset route stays
uncalibrated, because a guessed window may be larger than that tier's model
supports. The migrated value takes effect immediately at launch;
`cc-dialect doctor --fix` records it in `config.json` so the file stops lagging
behind.

Presets themselves are compiled into the executable, so a revised preset window
arrives with `cc-dialect upgrade` and applies to newly created dialects.
An already recorded value is never raised on your behalf — nothing distinguishes
a window you measured from one a preset supplied, and silently increasing it
would delay compaction past a limit you set deliberately. Adopt a revised preset
window explicitly when you want it:

```sh
cc-dialect create cc-codex --preset codex-sol --context-window 372000
```

The same applies to preset revisions that change models. A dialect stores the
models it was created with, so it keeps them after the preset moves on — but
`create` stamps the exact preset revision behind the dialect
(`presetFingerprint` in `config.json`), and `doctor` compares that stamp
against today's preset to report the drift. A dialect untouched since creation
is reported with every field that changed and the `create` command that adopts
the current preset, restating a window the revision moved with
`--context-window` — a same-route `create` keeps the stored window otherwise;
one that cannot be told apart from a hand edit — created before the stamp
existed, or edited since — is reported as possibly either; and a dialect that
is field-for-field identical to some current preset is never reported as
drift, whatever its label says — doctor notes only that its label names
another preset. A dialect whose route matches another preset exactly but whose
window is behind that preset is reported as drift against the preset it
actually runs. `doctor --fix` never rewrites a model or window on your behalf;
adoption is always the printed command, run by you.

### Check the calibration

```sh
cc-dialect doctor
```

`doctor` reports dialects with a missing or invalid context window, the fill
level of the most recent request per dialect, and a Claude Code build that no
longer references either capacity variable — one line per variable, because
dropping `CLAUDE_CODE_AUTO_COMPACT_WINDOW` delays compaction, while dropping
`CLAUDE_CODE_MAX_CONTEXT_TOKENS` falls back to Claude Code's 200,000-token
default, which skews the reported percentage and compacts any larger declared
window early. It also reports preset drift — a dialect still running the
models or window of a preset revision it was created from, or a window behind
the preset its route exactly matches, after `cc-dialect upgrade` shipped a
newer one — naming the fields that changed and the `create` command that
adopts the current preset. Adding `--fix` records
migrated windows in `config.json`, together with the preset name an exact
route match resolved for a dialect that stored none. Existing labels are
preserved, a custom route or any route that matches no preset exactly stays
reported until you set a window yourself, and drift is never applied by
`--fix`: rewriting a model or a window is always the printed command, run by
you.

Each dialect's embedded proxy also records the latest request's input usage to
`instances/<name>/context.json` and warns once per 80%, 90%, and 95% threshold.
Only counters are recorded — never prompts, tool results, request bodies, or
credentials.

### Known limitation: one window per process

Claude Code takes a single auto-compact window for the whole process, so a
dialect's window covers every model it can select — which is why a preset uses
the smallest one, and why the dynamic Cursor and Copilot `auto` routes sit at a
conservative floor.

Selecting a model outside that set is therefore not calibrated for. Launching
with `cc-dialect run <name> --model <other>` warns when the model is not one the
dialect configures, and switching with `/model <arbitrary-id>` mid-session
cannot be re-calibrated at all, because the window is fixed for the life of the
process. For a model you use regularly, give it its own dialect so it gets its
own window.

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

The browser is launched with `open` on macOS and `xdg-open` on Linux. Headless
Linux hosts (SSH sessions, containers, WSL) often ship without `xdg-open`; on
those, install `xdg-utils` or use `--no-browser` and open the printed URL
yourself.

The dashboard can:

- inspect safe views of configured preset and custom dialects, their effective
  model and runtime settings, built-in presets, tracked native launchers, and
  Cursor runtime readiness;
- create and update dialects, including model aliases, effort, concurrency,
  [context window](#context-window-and-auto-compaction), ports, tool search, and
  custom Anthropic-compatible routing;
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

`status` reports the proxy and, for bridge-backed dialects, the managed bridge.
A bridge that died while its PID record remains on disk is reported as
**crashed** rather than stopped, with the log to read and the restart command to
run — the proxy in front of it otherwise keeps forwarding into a port nothing is
listening on.

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

State lives under the platform's user configuration directory — on macOS
`~/Library/Application Support/claude-dialects`, on Linux
`${XDG_CONFIG_HOME:-~/.config}/claude-dialects` — or under `DIALECT_HOME` when
that is set:

```text
config.json          # dialect configuration and tracked native-launcher registry
.state.lock          # owner-only cross-process mutation lock
instances/
  cc-codex/
    auth/
    claude/
    statusline.sh
    proxy.yaml
    proxy.pid
    proxy.log
    proxy.version
    context.json
  cc-kimi/
    auth/
    claude/
    statusline.sh
    proxy.yaml
    proxy.pid
    proxy.log
    proxy.version
    context.json
  cc-cursor/
    claude/
    statusline.sh
    cursor-workspace/
    cursor-bridge.pid
    cursor-bridge.log
    cursor-bridge.version
    proxy.yaml
    proxy.pid
    proxy.log
    proxy.version
    context.json
  cc-copilot-mai/
    claude/
    statusline.sh
    copilot-home/
    copilot-bridge.pid
    copilot-bridge.log
    copilot-bridge.version
    proxy.yaml
    proxy.pid
    proxy.log
    proxy.version
    context.json
cursor-runtime/
  cursor_bridge.mjs
  node_modules/@cursor/sdk/
copilot-runtime/
  copilot_bridge.mjs
  node_modules/@github/copilot-sdk/
defaults/
  mcp.json         # shared MCP server defaults (owner-only); merged into every dialect
```

Proxy servers bind only to `127.0.0.1`. Configuration, local API keys, and OAuth
credentials use owner-only permissions. The CLI changes environment variables
only for the launched Claude Code process. Mutating operations are serialized
across CLI and dashboard processes with the owner-only `.state.lock`; configuration
and launcher files are written atomically.

Per-dialect reads, writes and deletes **performed by `cc-dialect` itself** are
confined to that dialect's own directory by the operating system rather than by
path checks alone: the CLI opens `instances/<dialect>/` as a root and resolves
every path relative to it. A symlink planted inside cannot redirect one of those
reads, writes or removals — neither outside the state tree nor into a *sibling
dialect*. Dialect names remain restricted to `[a-z0-9_-]`.

Ownership of a running proxy or bridge is decided by the PID record under that
dialect's own directory, never by the health check alone. A port answers the
same way whichever directory the process behind it was started from, so a
runtime left over from a directory that has since been replaced would otherwise
be adopted by the replacement — which records nothing for a later `stop` or
`remove` to find. `cc-dialect start` therefore refuses a proxy or bridge that is
already serving the dialect's port while the dialect holds no live record of
owning it, rather than reporting a success that leaves the process unmanageable.
Stop it and start again.

The scope of that sentence is deliberate. It covers the CLI's own file
operations, and not the processes the CLI launches. Four limits are worth
stating plainly:

- **Removal is the exception.** Every other operation refuses to run when
  `instances/` or the dialect's own directory has been replaced by a symlink, and
  refuses before anything is changed. `cc-dialect remove` instead unlinks the
  symlink itself and commits the configuration change, so a dialect whose
  directory was tampered with can still be cleaned up. It unlinks the link and
  never follows it, so the link's target is left untouched.
- **Credentials written during OAuth are not root-confined.** `cc-dialect auth`
  hands the embedded CLIProxyAPI an absolute `auth-dir` and that dependency
  writes the token itself through provider-specific code, so the write is guarded
  by the pathname rather than by the root. Afterwards the CLI checks that the
  file it was handed back really is the one the dialect's own `auth/` resolves
  to — comparing file identity, not just the path string — and fails the login
  naming the file to inspect if it is not. That is detection, not confinement:
  the token has already been written by the time it runs. Closing this properly
  needs a root-aware persistence API in CLIProxyAPI.
- **Launched processes use ordinary path-based I/O.** Claude Code receives the
  dialect's `claude/` directory as an absolute `CLAUDE_CONFIG_DIR`, and the
  Cursor and Copilot bridges receive absolute `cursor-workspace/` and
  `copilot-home/` paths. `cc-dialect` creates and validates those directories
  through the root before handing them over, so an escape that is already in
  place is rejected — but the processes themselves are ordinary programs
  resolving ordinary paths. A symlink introduced *underneath* one of those
  directories after hand-off, or nested inside `claude/`, is followed by those
  processes like any other path. Confining them would mean changing Claude Code
  and the vendor SDKs, not this CLI.
- **The embedded proxy re-reads its own config by path.** The proxy runs as a
  separate `cc-dialect` process, which pins the dialect directory and refuses to
  serve if the name resolved somewhere else. Its initial `proxy.yaml` read goes
  through that root — but CLIProxyAPI is then handed the absolute path for its
  long-lived config watcher, and every later re-read and credential write it
  performs resolves that path itself, outside the root.

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

If a shell alias already uses the generated command name, it takes precedence
over the executable. Zsh and Bash are both checked, starting with your `$SHELL`.
Both `cc-dialect shim install` and `cc-dialect doctor` detect these collisions
and name the shell that defines the alias, along with that shell's startup file
(`~/.zshrc` for Zsh, `~/.bashrc` for Bash) as the place to start looking — the
definition may live in a file that startup file sources, in a framework such as
oh-my-zsh, or in system-wide configuration, which a shell does not report. Once
removed, run `unalias <name>` in terminals that were already open.

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
cc-dialect mcp list
cc-dialect --version
```

`cc-dialect doctor` detects misconfigurations (shadowed shims, missing API keys,
incorrect SDK versions) and distinguishes a recent Codex upstream HTTP failure
and fast cooldown retry from genuinely missing OAuth. Add the `--fix` flag
(`cc-dialect doctor --fix`) to automatically apply deterministic repairs: it
will restart any proxies or Node bridges that are running stale binaries (e.g.
after you updated `cc-dialect`), restart a Node bridge that crashed while its
proxy is still running, and re-install any Node SDK bridge runtimes that do not
match the current required version. Interactive steps like OAuth logins and
recovering from an overloaded upstream are left for you to complete.

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
its configuration, OAuth credentials, isolated Claude Code history, and any MCP
servers configured inside the dialect (they live in its `claude/.claude.json`).
Promote servers you want to keep across removals to the
[shared MCP defaults](#shared-mcp-server-defaults) first. Shims are ordinary
files and must be removed separately:

```sh
cc-dialect remove cc-gemini
rm ~/.local/bin/cc-gemini
```

To erase every currently configured dialect, remove each name shown by
`cc-dialect list`, then remove its shim. To uninstall the manager after that:

```sh
rm ~/.local/bin/cc-dialect
rm -rf "$HOME/Library/Application Support/claude-dialects"   # macOS
rm -rf "${XDG_CONFIG_HOME:-$HOME/.config}/claude-dialects"   # Linux
```

The final `rm -rf` is intentionally explicit because it permanently deletes
all remaining provider credentials and conversation history stored by Claude
Dialects.

### Shared MCP server defaults

MCP servers added inside a running dialect — via `claude mcp add`, or written
directly into a dialect's `claude/.claude.json` — live inside that dialect's
instance directory, so `cc-dialect remove` deletes them along with everything
else. To make a set of servers survive removal and apply to every dialect,
promote them to the shared defaults file, which `cc-dialect` owns outside
`instances/` and which `remove` structurally cannot delete:

```sh
# Copy this dialect's mcpServers into the shared defaults (owner-only file):
cc-dialect mcp import cc-codex
# Print the shared defaults file location, for manual editing:
cc-dialect mcp path
# List the shared servers (env values are never printed):
cc-dialect mcp list
```

`mcp import` and `doctor` read only the **user-scoped** `mcpServers` at the top
of a dialect's `claude/.claude.json` — the section `claude mcp add --scope user`
writes to. `claude mcp add` without `--scope` defaults to the `local` scope,
which Claude Code stores under a per-project path those commands do not read; so
to share a server, add it with `--scope user` (or edit the top-level `mcpServers`
object directly).

Unless the run already passes its own `--mcp-config`, every `cc-dialect run`
launches Claude Code with `--mcp-config <path>`, which **merges** the shared
servers into the dialect's own set rather than replacing it: a server present
only in the shared defaults becomes available, and nothing the dialect already
defines is removed. A run that already includes `--mcp-config` — for example
`cc-dialect run cc-codex -- --mcp-config ./mine.json` — keeps your file as-is and
does not add the shared defaults; include the shared path (printed by
`cc-dialect mcp path`) alongside your own `--mcp-config` if you want both merged. Claude Code does not document which definition
wins when a dialect and the shared defaults define a server with the same name, so
keep the two disjoint; `cc-dialect doctor` flags any dialect that redefines a
shared server locally so the duplicate can be cleaned up. `mcp import` never
overwrites a server already in the shared defaults unless you pass `--force`.

The defaults file is written with owner-only permissions (`0600`) because a
server's `env` may carry tokens, and `mcp list` deliberately omits `env`. A
missing or empty file injects nothing, so every dialect behaves exactly as before.
A malformed file is skipped with a warning rather than passed to Claude Code, so
one bad file cannot break every dialect at once.

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
Node.js release (Node.js 22.22.2+ or 24.15.0+), and must rebuild and commit the updated
`dist/` files. Production npm dependencies bundled into those assets are included
in `THIRD_PARTY_NOTICES.md`; development-only frontend packages are not shipped in
the binary.

This project does not publish prebuilt binaries or GitHub releases. Everyone
builds the executable from the checked-out source. To create a shareable local
archive and checksum instead of installing it:

```sh
make assets VERSION=dev
ls artifacts/
(cd artifacts && shasum -a 256 -c SHA256SUMS)  # macOS
(cd artifacts && sha256sum -c SHA256SUMS)      # Linux
```

The archive is named for the platform it was built on, and macOS gets a `.zip`
while Linux gets a `.tar.gz`. On an Apple silicon Mac the generated files are:

- `artifacts/cc-dialect_dev_darwin_arm64.zip`
- `artifacts/SHA256SUMS`

and on an x86-64 Linux host:

- `artifacts/cc-dialect_dev_linux_amd64.tar.gz`
- `artifacts/SHA256SUMS`

`make build` and `make assets` target the host platform by default; override
`GOOS` and `GOARCH` to cross-compile (for example
`make assets VERSION=dev GOOS=linux GOARCH=arm64`). Packaging a `.tar.gz` works
from either host. Packaging a macOS `.zip` uses `ditto` when it is available and
falls back to `zip`, so building a darwin archive from Linux needs `zip`
installed.

Set `VERSION` to any identifier you want in the filename and embedded
`cc-dialect --version` output. `make package` is an alias for `make assets`.
These locally produced assets carry no code signature; on macOS they are
neither signed nor notarized.

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
