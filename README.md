<div align="center">

# ofga

**A modern CLI & TUI for [OpenFGA](https://openfga.dev).**

Manage stores, authorization models, relationship tuples, and run checks from your terminal — or explore everything interactively in a full-screen TUI.

[Quick start](#-quick-start) · [The TUI](#-the-interactive-tui) · [Commands](#-command-reference) · [Configuration](#-configuration) · [Contributing](#-contributing)

</div>

[![CI](https://github.com/sergiught/openfga-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/sergiught/openfga-cli/actions/workflows/ci.yml)
[![CodeQL](https://github.com/sergiught/openfga-cli/actions/workflows/codeql.yml/badge.svg)](https://github.com/sergiught/openfga-cli/actions/workflows/codeql.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/sergiught/openfga-cli)](https://goreportcard.com/report/github.com/sergiught/openfga-cli)
[![Release](https://img.shields.io/github/v/release/sergiught/openfga-cli?sort=semver)](https://github.com/sergiught/openfga-cli/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/sergiught/openfga-cli)](go.mod)
[![GHCR](https://img.shields.io/badge/ghcr.io-ofga-2496ed?logo=docker&logoColor=white)](https://github.com/sergiught/openfga-cli/pkgs/container/ofga)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Conventional Commits](https://img.shields.io/badge/Conventional%20Commits-1.0.0-fa6673.svg)](https://www.conventionalcommits.org)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

---

## 📑 Table of contents

- [✨ What is this?](#-what-is-this)
- [🚀 Quick start](#-quick-start)
- [📦 Installation](#-installation)
- [🖥 The interactive TUI](#-the-interactive-tui)
- [📋 Command reference](#-command-reference)
- [🛠 Configuration](#-configuration)
- [🔑 Authentication](#-authentication)
- [⌨️ Shell completion](#️-shell-completion)
- [🤝 Scripting & automation](#-scripting--automation)
- [🏗 Contributing](#-contributing)
- [⚖️ License](#️-license)

---

## ✨ What is this?

`ofga` is a single, dependency-free binary that gives you two ways to work with an OpenFGA server:

- 🧰 **A scriptable CLI** — create stores, write and inspect authorization models, manage relationship tuples, run `check`/`list-objects`/`list-users`, and run assertion suites. Every read command speaks `--json` (for `jq`) and `--plain` (for `grep`/`awk`), returns meaningful exit codes, and confirms destructive actions.
- 🖥 **A full-screen TUI** — launch it by running `ofga` with no arguments. Browse stores, visualize a model as a colored relation graph, edit tuples, run queries and expand their resolution trees, and manage assertions — all with the keyboard **or the mouse**.

It talks to any OpenFGA-compatible server and reuses your connection **profiles** so you can switch between local, staging, and production in one flag.

> **Naming:** the official OpenFGA CLI is `fga`. This is a separate, independent reimagining focused on ergonomics and an interactive TUI, distributed as `ofga`. It is not affiliated with OpenFGA.

---

## 🚀 Quick start

```bash
# 1. Point ofga at your server (guided; uses http://localhost:8080 by default)
ofga init

# 2. Create a store and make it active
ofga stores create demo --use

# 3. Write an authorization model (a minimal one to copy-paste)
cat > model.json <<'JSON'
{"schema_version":"1.1","type_definitions":[{"type":"user"},{"type":"document","relations":{"viewer":{"this":{}}},"metadata":{"relations":{"viewer":{"directly_related_user_types":[{"type":"user"}]}}}}]}
JSON
ofga model write --file model.json

# 4. Add a relationship tuple
ofga tuples write user:anne viewer document:roadmap

# 5. Ask an authorization question
ofga query check user:anne viewer document:roadmap
# ✓ ALLOWED  user:anne viewer document:roadmap

# 6. …or explore everything interactively
ofga
```

---

## 📦 Installation

### Homebrew (macOS / Linux)

```bash
brew install sergiught/tap/ofga
```

### Arch Linux (AUR)

```bash
yay -S ofga-bin        # or: paru -S ofga-bin
```

### `go install`

```bash
go install github.com/sergiught/openfga-cli/cmd/ofga@latest
```

### Pre-built binaries

Download a `.tar.gz`, `.deb`, `.rpm`, or `.apk` for your platform from the
[latest release](https://github.com/sergiught/openfga-cli/releases/latest), or:

```bash
# Linux/macOS one-liner (installs to /usr/local/bin)
curl -sSfL https://github.com/sergiught/openfga-cli/releases/latest/download/ofga_$(uname -s)_$(uname -m).tar.gz \
  | tar -xz ofga && sudo mv ofga /usr/local/bin/
```

### Docker

```bash
docker run --rm -it --network host ghcr.io/sergiught/ofga:latest stores list
```

### From source

```bash
git clone https://github.com/sergiught/openfga-cli
cd openfga-cli
go build -o ofga ./cmd/ofga
```

Verify your install:

```bash
ofga version
```

---

## 🖥 The interactive TUI

Run `ofga` with no arguments to launch the interactive playground. It's a keyboard- **and mouse**-driven cockpit for the whole OpenFGA surface.

<!-- TODO: add a demo GIF/asciinema here -->

**Sections** (switch with `tab`, the number keys `1`–`7`, `ctrl+k` for the command palette, or **click a tab**): Profiles · Stores · Model · Tuples · Changes · Tuple Queries · Assertions.

**Highlights**

- 🎨 **Model graph** — the authorization model rendered as a colored tree of types, relations, and inherited (tuple-to-userset) paths.
- 🔎 **Query + resolution tree** — run `check`/`list-objects`/`list-users`/`list-relations` and expand *why* a decision was made.
- ✍️ **Inline editing** — add/edit tuples, assertions, and the model DSL, with **inline validation** as you type.
- 🖱 **Full mouse support** — wheel-scroll the graph and lists, click tabs and list rows, click the footer keycaps as buttons, and click outside a dialog to dismiss it.
- 🎭 **Themes** — `aurora`, `catppuccin`, `charm`, `dracula`, `gruvbox`, `nord`, `tokyonight`, and a `mono` (NO_COLOR-friendly) theme.

**Keys:** press `?` at any time for the full, context-aware keybinding overlay.

> The TUI only launches on an interactive terminal. In a pipe or CI, bare `ofga` prints help instead of hanging.

---

## 📋 Command reference

| Command | What it does |
| --- | --- |
| `ofga` | Launch the interactive TUI |
| `ofga init` | Guided first-run setup (creates a connection profile) |
| `ofga stores` | Create, list, inspect and delete stores |
| `ofga model` | Write, list, inspect, and **visualize** authorization models (`model graph`) |
| `ofga tuples` | Write, delete, read relationship tuples and follow the changelog |
| `ofga query` | Ask authorization questions: `check`, `batch-check`, `expand`, `list-objects`, `list-users` |
| `ofga assertions` | Read, write, and **run** a model's assertion test-suite |
| `ofga api` | Send a raw request to the OpenFGA API using the active profile's auth |
| `ofga profiles` | Manage connection profiles (add/list/show/current/use/set/remove) |
| `ofga config` | Inspect configuration (`config path`) |
| `ofga theme` | Show or set the color theme |
| `ofga completion` | Generate a shell completion script |
| `ofga version` | Print version and build info |

Run `ofga <command> --help` for details and examples on any command.

---

## 🛠 Configuration

`ofga` stores its configuration in a TOML file under your XDG config dir. Find it with:

```bash
ofga config path
```

### Profiles

A **profile** bundles an API URL, an optional store and model ID, and auth settings. Switch between environments with `--profile`/`-p` or the `OPENFGA_PROFILE` env var.

```bash
ofga profiles add prod --api-url https://fga.example.com --token-stdin < token.txt
ofga profiles use prod
ofga profiles show                # resolved active config (secrets masked)
ofga --profile staging stores list
```

> **Flag naming:** the global `--store`/`--model` flags are a *runtime override* for the current invocation only (e.g. `ofga --store 01ABC query check …`). `--store-id`/`--model-id` on `ofga profiles add|set` and `ofga init` *persist* a value into a profile. They're named differently on purpose — `--store-id` on those commands would otherwise be shadowed by the global `--store` override — but they resolve the same store/model ID either way (see [Precedence](#precedence)).

### Precedence

Values are resolved in increasing order of precedence:

**profile → environment variables → command-line flags**

### Environment variables

| Variable | Purpose |
| --- | --- |
| `OPENFGA_API_URL` | API URL (alias: `FGA_API_URL`) |
| `OPENFGA_STORE_ID` | Active store ID (alias: `FGA_STORE_ID`) |
| `OPENFGA_MODEL_ID` | Authorization model ID (aliases: `OPENFGA_AUTHORIZATION_MODEL_ID`, `FGA_MODEL_ID`, `FGA_AUTHORIZATION_MODEL_ID`) |
| `OPENFGA_API_TOKEN` | API bearer token (alias: `FGA_API_TOKEN`) |
| `OPENFGA_CLIENT_ID` | OAuth2 client ID for `client_credentials` (alias: `FGA_CLIENT_ID`) |
| `OPENFGA_CLIENT_SECRET` | OAuth2 client secret for `client_credentials` (alias: `FGA_CLIENT_SECRET`) |
| `OPENFGA_TOKEN_URL` | OAuth2 token endpoint for `client_credentials` (alias: `FGA_TOKEN_URL`) |
| `OPENFGA_API_AUDIENCE` | OAuth2 audience for `client_credentials` (alias: `FGA_API_AUDIENCE`) |
| `OPENFGA_SCOPES` | OAuth2 scopes for `client_credentials` (alias: `FGA_SCOPES`) |
| `OPENFGA_KEY_FILE` | Path to the PEM signing key; applies to a `private_key_jwt` profile (alias: `FGA_KEY_FILE`) |
| `OPENFGA_PROFILE` | Profile to use (alias: `FGA_PROFILE`) |
| `OPENFGA_CONFIG` | Path to the config file (overridden by the `--config` flag) |
| `OPENFGA_ICONS` | Icon mode: `nerdfont` (default), `unicode`, or `off` |
| `OFGA_REDUCED_MOTION` | Suppress TUI animations (alias: `OPENFGA_REDUCED_MOTION`) |
| `NO_COLOR` | Disable colored output |
| `CLICOLOR_FORCE` | Force colored output even when piped or redirected |
| `FORCE_COLOR` | Force colored output even when piped or redirected (equivalent to `CLICOLOR_FORCE`) |

`FGA_*` aliases are accepted for compatibility with the official CLI.

---

## 🔑 Authentication

`ofga` supports the same auth methods as OpenFGA:

- **None** — for a local, unauthenticated server.
- **API token** — a pre-shared bearer token.
- **Client credentials** — OAuth2 client-credentials grant.
- **Private key JWT** — OAuth2 with a client-assertion JWT.

Secrets should be provided without exposing them in your shell history or `ps`:

```bash
ofga profiles set token --value-stdin < token.txt        # from stdin
ofga profiles set client_secret --value-file ./secret    # from a file
```

The config file is written with `0600` permissions, and `profiles show` masks all secrets.

---

## ⌨️ Shell completion

```bash
# bash
source <(ofga completion bash)
# zsh
ofga completion zsh > "${fpath[1]}/_ofga"
# fish
ofga completion fish | source
```

Completion is **dynamic**: `--profile`, `--store`, and `--model` (and the matching positional args) complete real profile names, store IDs, and model IDs from your server. Network-backed completions are bounded by a short timeout so they never hang your shell.

---

## 🤝 Scripting & automation

`ofga` is built to compose:

- `--json` on every read command emits clean, machine-readable JSON (secrets omitted) for `jq`.
- `-o yaml` (or `--output yaml`) emits the same structured data as YAML, for tools that prefer it (e.g. diffing against a YAML-based config).
- `--plain` emits unstyled, tab-separated rows for `grep`/`awk`; `query check --plain` prints `allowed`/`denied`.
- Meaningful **exit codes**: `0` success, `1` generic failure, `2` usage error, `3` failed `assertions test`, `4` network error.
- `--dry-run` on every server mutation previews the change without applying it.
- Destructive commands prompt on a TTY and require `--force` when non-interactive, so scripts fail safe.
- Piped output drops colors and box-drawing automatically.

> **Note:** `ofga tuples read` and `ofga stores list` auto-paginate and return **all** rows by default (`--page-size` only sets the per-request page size, not a total cap). Against a large store that can be a lot of output — cap it with `--max-results` (alias `--limit`), or pipe through `head` or `--json | jq`.

```bash
# Which documents can anne view?
ofga query list-objects document viewer user:anne --plain

# Fail a CI job if the assertion suite regresses
ofga assertions test || exit 1
```

---

## 🏗 Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for the build/test/lint workflow and the [Conventional Commits](https://www.conventionalcommits.org) convention used for automated releases.

```bash
go build ./...
go test ./...
```

---

## ⚖️ License

[MIT](LICENSE) © Sergiu Ghitea. Built for the excellent [OpenFGA](https://openfga.dev) project (not affiliated).
