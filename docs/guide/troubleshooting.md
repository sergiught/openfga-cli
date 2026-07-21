# Troubleshooting

| Symptom | Resolution |
| --- | --- |
| `connection refused` at `localhost:8080` | Start OpenFGA (see [Quick start](../../README.md#-quick-start)) or select the correct `--profile`/`--api-url`. |
| `no store selected` | Pass `--store-id`, set `OPENFGA_STORE_ID`, or run `ofga profiles set store_id <id>`. |
| The OS keyring is unavailable in a headless container | Mount a secret and use a process-scoped `--auth-*-file` flag. Saved secrets deliberately fail closed rather than entering TOML. |
| Credential cleanup was deferred | Restore keyring access, then run `ofga profiles cleanup-credentials`. Pending exact-field cleanup is stored in the same config file and never deletes a credential still used by a profile. |
| `config changed on disk since it was loaded` | Another process saved first. Re-run the command or reopen the playground to load the newer file. |
| An API or OAuth URL returns a redirect | Configure the final destination URL directly. Redirects are intentionally disabled for credential safety. |
| The playground does not start | It requires an interactive terminal. Use `ofga playground` locally; in CI use a CLI subcommand with `--no-input`. |
| Warning about credentials over HTTP | Use HTTPS for both the OpenFGA API and OAuth token URL. HTTP is only treated as safe for loopback development. |

---

## Shell completion

```bash
# bash
source <(ofga completion bash)
# zsh
ofga completion zsh > "${fpath[1]}/_ofga"
# fish
ofga completion fish | source
# PowerShell
ofga completion powershell | Out-String | Invoke-Expression
```

Completion is **dynamic**: `--profile`, `--store-id`, and `--model-id` (and the matching positional args) complete real profile names, store IDs, and model IDs from your server. Network-backed completions are bounded by a short timeout so they never hang your shell.
