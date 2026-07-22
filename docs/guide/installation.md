# Installation

## Homebrew (macOS / Linux)

```bash
brew install sergiught/tap/ofga
```

## Arch Linux (AUR)

```bash
yay -S ofga-bin        # or: paru -S ofga-bin
```

## `go install`

```bash
go install github.com/sergiught/openfga-cli/cmd/ofga@latest
```

## Install script (recommended)

```bash
# Latest stable release.
curl -fsSL https://raw.githubusercontent.com/sergiught/openfga-cli/main/install.sh | bash

# A specific release.
curl -fsSL https://raw.githubusercontent.com/sergiught/openfga-cli/main/install.sh | bash -s -- v1.0.0

# Install without sudo.
BIN_DIR="$HOME/.local/bin" bash <(curl -fsSL https://raw.githubusercontent.com/sergiught/openfga-cli/main/install.sh)
```

The script detects Linux or macOS on x86-64 or ARM64, downloads the matching
GoReleaser archive, verifies its SHA-256 checksum, and installs `ofga`. Review
the [`install.sh`](../../install.sh) source before piping it to Bash if preferred.
Every [release](https://github.com/sergiught/openfga-cli/releases) also includes
checksums, an SPDX SBOM, and signed provenance.

## Docker

```bash
docker run --rm -it --network host ghcr.io/sergiught/ofga:latest stores list
```

## From source

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

## Upgrade and uninstall

Upgrade with the same installation method used to install `ofga`:

```bash
brew upgrade ofga
yay -Syu ofga-bin
go install github.com/sergiught/openfga-cli/cmd/ofga@latest
curl -fsSL https://raw.githubusercontent.com/sergiught/openfga-cli/main/install.sh | bash
```

Before removing the binary, use `ofga profiles remove <name>` for saved
profiles whose keyring credentials should also be deleted. Then uninstall the
package (`brew uninstall ofga`, your system package manager, or
`rm "$(command -v ofga)"`). Remove the remaining configuration directory only
if it is no longer needed:

```bash
rm -rf "$(dirname "$(ofga config path)")"
```
