# Claude Dialects

Create multiple native-feeling Claude Code commands powered by different models.
Each generated dialect runs the real Claude Code interface with its own model,
environment, credentials, API key, and embedded
[CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) instance.

The proxy is linked into the `dialect` executable through CLIProxyAPI's Go SDK.
There is no separate proxy download, installation, container, or global
`~/.claude/settings.json` modification.

> Current target: macOS on Apple Silicon only.

## Build and install

Requirements:

- Go 1.26 or newer
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) available as
  `claude`

```sh
make install
export PATH="$HOME/.local/bin:$PATH"
```

This produces one static executable at `~/.local/bin/dialect`.

## Create several dialects

```sh
# GPT with Sol as both the main and subagent model
dialect create claudex --preset codex-sol
dialect auth claudex codex
dialect shim install claudex

# Kimi, fully isolated from claudex
dialect create kimi --preset kimi
dialect auth kimi kimi
dialect shim install kimi

# A second Codex setup gets another port and credential store
dialect create codex-work --preset codex
dialect auth codex-work codex
dialect shim install codex-work
```

You can now run all three simultaneously:

```sh
claudex
kimi
codex-work
```

Provider setup:

| Dialect | Connection | Setup |
| --- | --- | --- |
| OpenAI GPT / Codex | ChatGPT OAuth through CLIProxyAPI | `dialect auth claudex codex` |
| Kimi | Kimi OAuth through CLIProxyAPI | `dialect auth kimi kimi` |
| Google Gemini | Antigravity Google OAuth through CLIProxyAPI | `dialect auth gemini antigravity` |
| Claude | Anthropic OAuth through CLIProxyAPI | `dialect auth claude claude` |
| GLM | Z.ai Anthropic-compatible API through CLIProxyAPI | Set `ZAI_API_KEY` |

Create the Google runner with:

```sh
dialect create gemini --preset gemini
dialect auth gemini antigravity
dialect shim install gemini
```

Ports are actively checked and allocated per dialect starting at the high range
`43170`. A port already bound by any process is skipped during creation and
rejected again at startup:

```text
claudex      gpt-5.6-sol       127.0.0.1:43170
kimi         kimi-k2.7-code    127.0.0.1:43171
codex-work   gpt-5.6           127.0.0.1:43172
```

Pass Claude Code arguments normally:

```sh
claudex --permission-mode plan
kimi --allowedTools "Bash,Read"
```

## Presets and custom dialects

Included presets:

```sh
dialect presets
```

- `codex-sol`
- `codex`
- `kimi`
- `gemini`
- `claude`
- `glm`

Override the important parameters while creating or updating a dialect:

```sh
dialect create my-codex \
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
  --port 8400
```

For an Anthropic-compatible service such as Z.ai, route the upstream through
the dialect's isolated embedded proxy:

```sh
export MY_PROVIDER_TOKEN="..."
dialect create my-model \
  --model my-model-id \
  --base-url https://provider.example.com/api/anthropic \
  --token-env MY_PROVIDER_TOKEN
dialect shim install my-model
```

The upstream token is read only when that isolated proxy starts and is written
to its owner-only proxy configuration. The `glm` preset uses this mode with
`ZAI_API_KEY`, matching the behavior
of [xqsit94/glm](https://github.com/xqsit94/glm).

## Switch model and effort inside a conversation

Claude Code 2.x supports live switching without losing the conversation:

```text
/model opus
/model sonnet
/model haiku
/model <any model ID shown by `dialect models claudex`>
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
CLIProxyAPI translates
Claude's adaptive reasoning request into the upstream provider's reasoning
format when that provider supports it.

List the models actually exposed by an authenticated instance:

```sh
dialect models claudex
```

## Proxy and authentication commands

Every proxied dialect has an independent lifecycle:

```sh
dialect proxy claudex start
dialect proxy claudex status
dialect proxy claudex logs
dialect proxy claudex stop
```

The proxy starts automatically when its generated command runs and remains
available for later sessions. OAuth credentials are scoped to that dialect:

```sh
dialect auth claudex codex
dialect auth kimi kimi
dialect auth another claude
```

Supported embedded OAuth providers are `codex`, `claude`, `kimi`,
`antigravity`, and `xai`.

## Files and security

State lives under `~/Library/Application Support/claude-dialects` on macOS (or
`DIALECT_HOME` when set):

```text
config.json
instances/
  claudex/
    auth/
    proxy.yaml
    proxy.pid
    proxy.log
  kimi/
    auth/
    proxy.yaml
    proxy.pid
    proxy.log
```

Proxy servers bind only to `127.0.0.1`. Configuration, local API keys, and OAuth
credentials use owner-only permissions. The CLI changes environment variables
only for the launched Claude Code process.

If a Zsh alias already uses the generated command name, it takes precedence
over the executable. Remove the alias from `~/.zshrc`, then run `unalias
<name>` in terminals that were already open. Both `dialect shim install` and
`dialect doctor` detect these collisions.

## Useful commands

```sh
dialect list
dialect show claudex
dialect doctor
dialect remove claudex
dialect --version
```

CLIProxyAPI is pinned as a Go dependency so a new upstream release cannot alter
an already-built executable. Its MIT license permits embedding and
redistribution; its required notice is included in `THIRD_PARTY_NOTICES.md`.

## Sources used for the integration

- [CLIProxyAPI Claude Code configuration](https://help.router-for.me/agent-client/claude-code)
- [CLIProxyAPI provider and model overview](https://help.router-for.me/introduction/what-is-cliproxyapi)
- [CLIProxyAPI Codex setup](https://help.router-for.me/agent-client/codex)
- [Claude Code model and effort configuration](https://code.claude.com/docs/en/model-config)
