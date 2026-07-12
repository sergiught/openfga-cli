# Packaging

`ofga` is distributed via goreleaser (see [`.goreleaser.yaml`](../.goreleaser.yaml)),
which **auto-generates and publishes** the Homebrew formula and the AUR
`PKGBUILD` on every release. Both are skipped cleanly until their credentials
are configured, so releases work before packaging is wired up.

This directory holds the one-time bootstrap material and setup steps.

---

## 🍺 Homebrew tap

goreleaser pushes a `Formula/ofga.rb` to the tap repo on each release. You only
need to create the tap repo once and give the release workflow a token.

1. Create a public repo named **`homebrew-tap`** under your account
   (`sergiught/homebrew-tap`). Seed it with
   [`homebrew-tap/README.md`](homebrew-tap/README.md).
2. Create a fine-grained **Personal Access Token** with *Contents: read & write*
   on that repo.
3. Add it as an Actions secret named **`HOMEBREW_TAP_GITHUB_TOKEN`** on this repo
   (Settings → Secrets and variables → Actions).

After the next release, users can:

```bash
brew install sergiught/tap/ofga
```

---

## 🐧 Arch User Repository (AUR)

goreleaser maintains the **`ofga-bin`** package (installs the pre-built release
binary). To enable it:

1. Create an [AUR account](https://aur.archlinux.org) and add your **SSH public
   key** to it.
2. Add the matching **SSH private key** as an Actions secret named **`AUR_KEY`**
   on this repo.

goreleaser will create/update the `ofga-bin` package on the next release (the
first push to `ssh://aur@aur.archlinux.org/ofga-bin.git` creates it).

Prefer to bootstrap it by hand first? The [`aur/PKGBUILD`](aur/PKGBUILD) here is a
working reference:

```bash
cd packaging/aur
# fill in real checksums for the release tarballs:
updpkgsums
makepkg --printsrcinfo > .SRCINFO
# then push to the AUR (once the repo exists)
```

After that, users can:

```bash
yay -S ofga-bin        # or: paru -S ofga-bin
```
