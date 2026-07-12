# Contributing to ofga

Thanks for your interest in improving `ofga`! This document covers how to build,
test, and submit changes.

## Development setup

You need **Go 1.26+**. Clone the repo and build:

```bash
git clone https://github.com/sergiught/openfga-cli
cd openfga-cli
go build ./cmd/ofga
```

## Build, test, lint

```bash
go build ./...          # compile everything
go test ./...           # run the test suite
go vet ./...            # static checks
gofmt -l .              # list unformatted files (should be empty)
golangci-lint run       # lint (see .golangci.yaml)
```

CI runs the same checks on every pull request.

### Testing the TUI

The interactive TUI writes to the config file on first run. When testing it,
isolate your config so it can't clobber your real profiles:

```bash
XDG_CONFIG_HOME=$(mktemp -d) go run ./cmd/ofga
```

## Commit messages

This project uses [**Conventional Commits**](https://www.conventionalcommits.org)
to drive automated releases and the changelog via
[release-please](https://github.com/googleapis/release-please). Prefix your
commit subjects accordingly:

| Prefix | Changelog section | Example |
| --- | --- | --- |
| `feat:` | Features | `feat(tui): add a keybinding overlay` |
| `fix:` | Bug fixes | `fix(query): honor --plain in list-objects` |
| `perf:` | Performance | `perf(tui): stop the spinner when idle` |
| `refactor:` | Refactors | `refactor(config): extract resolver` |
| `docs:` | Documentation | `docs: document env vars` |
| `test:` | Tests | `test(cli): cover the profiles lifecycle` |
| `build:` / `ci:` / `chore:` | (mostly hidden) | `chore(deps): bump go-openfga` |

Breaking changes: add a `!` (e.g. `feat!:`) or a `BREAKING CHANGE:` footer.

## Pull requests

1. Fork and create a branch off `main`.
2. Make your change with tests where it makes sense.
3. Ensure `go build`, `go test`, `go vet`, and `gofmt` all pass.
4. Open a PR with a Conventional-Commit title.

## Releases

Releases are automated. Merging to `main` updates a release-please PR; merging
that PR tags a version, writes the `CHANGELOG.md`, and triggers goreleaser to
build binaries, Linux packages, container images, and (when configured) the
Homebrew and AUR packages.

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](LICENSE).
