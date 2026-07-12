<!--
👋 Thanks for the PR! Quick conventions:
  • One change per PR — split refactors from behavior changes.
  • PR title follows Conventional Commits (it becomes the squash-merge subject
    and drives the changelog).
  • See CONTRIBUTING.md for the full workflow.
-->

## 📝 Summary

<!-- One or two sentences on what changes and *why*. The diff shows the what. -->

## 🏷 Type of change

- [ ] 🐛 Bug fix (non-breaking)
- [ ] ✨ Feature (non-breaking)
- [ ] 💥 Breaking change (users must update)
- [ ] ♻️ Refactor (no functional change)
- [ ] 📚 Docs only
- [ ] 🛠 Build / CI / tooling

## 🔗 Related issues

<!-- `Closes #123` / `Refs #456`. -->

## ✅ How to verify

```bash
make check   # fmt, vet, lint, test
```

<!-- If it affects the TUI, note how you drove it (isolate config first):
     XDG_CONFIG_HOME=$(mktemp -d) go run ./cmd/ofga -->

## 📋 Checklist

- [ ] 🧪 Tests added or updated where it makes sense
- [ ] 📖 Docs updated (README / command `--help` examples) if behavior changed
- [ ] 🏷 PR title follows [Conventional Commits](https://www.conventionalcommits.org/) (release-please derives `CHANGELOG.md` from it — don't hand-edit the changelog)
- [ ] 🟢 `make check` passes locally
