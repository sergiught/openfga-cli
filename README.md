<div align="center">

<img src="examples/playground.gif" alt="ofga — the interactive OpenFGA playground TUI" width="900">

# ofga

**A modern CLI & TUI for [OpenFGA](https://openfga.dev).**

Manage stores, authorization models, relationship tuples, and run checks from your terminal — or explore everything interactively in a full-screen TUI.

[Quick start](#-quick-start) · [The TUI](docs/guide/tui.md) · [Commands](#-command-reference) · [Configuration](docs/guide/configuration.md) · [Docs](#-documentation) · [Contributing](#-contributing)

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
- [📚 Documentation](#-documentation)
- [🏗 Contributing](#-contributing)
- [⚖️ License](#️-license)

---

## ✨ What is this?

`ofga` is a single, dependency-free binary that gives you two ways to work with an OpenFGA server:

- 🧰 **A scriptable CLI** — create stores, write and inspect authorization models, manage relationship tuples, run `check`/`list-objects`/`list-users`, and run assertion suites. Read commands provide consistent JSON/YAML output, tabular commands support `--plain`, and failures return meaningful exit codes.
- 🖥 **A full-screen TUI** — launch it by running `ofga` with no arguments. Browse stores, visualize a model as a colored relation graph, edit tuples, run queries and expand their resolution trees, and manage assertions — all with the keyboard **or the mouse**.

It talks to any OpenFGA-compatible server and reuses your connection **profiles** so you can switch between local, staging, and production in one flag.

> **Naming:** the official OpenFGA CLI is `fga`. This is a separate, independent reimagining focused on ergonomics and an interactive TUI, distributed as `ofga`. It is not affiliated with OpenFGA.

---

## 🚀 Quick start

![ofga CLI quickstart: create a store, write a model, write a tuple, check access](examples/quickstart.gif)

```bash
# 1. Start a local OpenFGA server in another terminal
docker run --rm --name openfga -p 8080:8080 openfga/openfga run

# 2. Point ofga at it (guided; uses http://localhost:8080 by default)
ofga init

# 3. Create a store and make it active
ofga stores create demo --use

# 4. Write an authorization model
cat > model.fga <<'FGA'
model
  schema 1.1

type user

type document
  relations
    define viewer: [user]
FGA
ofga model write --file model.fga
# `.fga` DSL is transformed to JSON for you. `--file` also takes a `.json`
# model, or `-` to read from stdin.

# 5. Add a relationship tuple
ofga tuples write user:anne viewer document:roadmap

# 6. Ask an authorization question
ofga query check user:anne viewer document:roadmap
# ✓ ALLOWED  user:anne viewer document:roadmap

# 7. …or explore everything interactively
ofga
```

Already have a server? Skip step 1 and pass its URL to `ofga init`.

---

## 📦 Installation

```bash
brew install sergiught/tap/ofga
```

```bash
curl -fsSL https://raw.githubusercontent.com/sergiught/openfga-cli/main/install.sh | bash
```

Full matrix (AUR, go install, Docker, source), upgrade, and uninstall → [docs/guide/installation.md](docs/guide/installation.md)

---

## 🖥 The interactive TUI

Run `ofga` with no arguments to launch the interactive playground — a keyboard- **and mouse**-driven cockpit covering profiles, stores, the model graph, tuples, queries with resolution trees, and assertions. Press `?` at any time for the full, context-aware keybinding overlay.

Full TUI guide & keybinding reference → [docs/guide/tui.md](docs/guide/tui.md)

---

## 📋 Command reference

| Command | What it does |
| --- | --- |
| [`ofga`](docs/guide/commands.md#playground) | Launch the interactive TUI |
| [`ofga playground`](docs/guide/commands.md#playground) | Explicit subcommand form of bare `ofga`; launches the interactive TUI |
| [`ofga init`](docs/guide/commands.md#init) | Guided first-run setup (creates a connection profile) |
| [`ofga stores`](docs/guide/commands.md#stores) | Create, list, inspect and delete stores |
| [`ofga model`](docs/guide/commands.md#model) | Write (from JSON **or `.fga` DSL**), list, inspect, **visualize** (`model graph`), and **test** (`model test`) authorization models |
| [`ofga tuples`](docs/guide/commands.md#tuples) | Write, delete, read relationship tuples and follow the changelog |
| [`ofga query`](docs/guide/commands.md#query) | Ask authorization questions: `check`, `batch-check`, `expand`, `list-objects`, `list-users` |
| [`ofga assertions`](docs/guide/commands.md#assertions) | Read, write, and **run** a model's assertion test-suite |
| [`ofga api`](docs/guide/commands.md#api) | Send a raw request to the OpenFGA API using the active profile's auth |
| [`ofga profiles`](docs/guide/commands.md#profiles) | Manage connection profiles (add/list/show/current/use/set/unset/remove/cleanup-credentials) |
| [`ofga config`](docs/guide/commands.md#config) | Inspect configuration (`config path`) |
| [`ofga theme`](docs/guide/commands.md#theme) | Show or set the color theme |
| [`ofga completion`](docs/guide/commands.md#completion) | Generate a shell completion script |
| [`ofga version`](docs/guide/commands.md#version) | Print version and build info |

Run `ofga <command> --help` for details and examples on any command.

---

## 📚 Documentation

- [Installation](docs/guide/installation.md) — install methods, upgrade, uninstall
- [The interactive TUI](docs/guide/tui.md) — playground tour + full keybinding reference
- [Command reference](docs/guide/commands.md) — every command and flag
- [Testing authorization models](docs/guide/model-testing.md) — the `model test` workspace, coverage, CI
- [Configuration](docs/guide/configuration.md) — config file, profiles, env vars
- [Authentication](docs/guide/authentication.md) — auth methods, secret files, keyring
- [Scripting & automation](docs/guide/scripting.md) — output formats, exit codes, pagination
- [Recipes](docs/guide/recipes.md) — common end-to-end flows
- [Troubleshooting](docs/guide/troubleshooting.md) — common issues + shell completion

---

## 🏗 Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for the build/test/lint workflow and the [Conventional Commits](https://www.conventionalcommits.org) convention used for automated releases.

Report bugs and request features through [GitHub Issues](https://github.com/sergiught/openfga-cli/issues). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

```bash
go build ./...
go test ./...
```

---

## ⚖️ License

[MIT](LICENSE) © Sergiu Ghitea. Built for the excellent [OpenFGA](https://openfga.dev) project (not affiliated).
